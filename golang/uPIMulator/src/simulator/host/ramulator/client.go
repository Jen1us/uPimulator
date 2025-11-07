package ramulator

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	defaultBurstBytes   = 64
	defaultMaxCycles    = 5_000_000
	defaultDrainCycles  = 1_000_000
	requestTimeout      = 30 * time.Second
	serviceEnvOverride  = "UPIMULATOR_RAMULATOR_SERVICE"
	serviceRelativeHint = "ramulator2"
)

// serviceResponse mirrors the JSON payload produced by ramulator2_service.
type serviceResponse struct {
	OK                bool    `json:"ok"`
	Cycles            float64 `json:"cycles,omitempty"`
	StartCycle        float64 `json:"start_cycle,omitempty"`
	CompleteCycle     float64 `json:"complete_cycle,omitempty"`
	RequestsIssued    float64 `json:"requests_issued,omitempty"`
	RequestsCompleted float64 `json:"requests_completed,omitempty"`
	Error             string  `json:"error,omitempty"`
}

// Client launches the ramulator2_service process and performs blocking JSON exchanges
// to obtain cycle estimates for host DMA requests.
type Client struct {
	mu          sync.Mutex
	enabled     bool
	configPath  string
	servicePath string
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      *bufio.Reader
	cancel      context.CancelFunc
	closed      bool
	lastErr     error
}

// NewClient constructs a Ramulator client when a configuration path is provided.
// It spawns ramulator2_service and performs an initial ping handshake.
func NewClient(configPath string) (*Client, error) {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return nil, fmt.Errorf("ramulator config path is empty")
	}

	absConfig := configPath
	if !filepath.IsAbs(configPath) {
		if resolved, err := filepath.Abs(configPath); err == nil {
			absConfig = resolved
		}
	}

	servicePath, err := locateServiceExecutable(absConfig)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, servicePath, "--config_file", absConfig)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("ramulator: stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("ramulator: stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("ramulator: start service: %w", err)
	}

	client := &Client{
		enabled:     true,
		configPath:  absConfig,
		servicePath: servicePath,
		cmd:         cmd,
		stdin:       stdin,
		stdout:      bufio.NewReader(stdoutPipe),
		cancel:      cancel,
	}

	// Ensure child process is not leaked if the client is GC'd without Close.
	runtime.SetFinalizer(client, func(c *Client) {
		_ = c.Close()
	})

	if err := client.ping(); err != nil {
		client.Close()
		return nil, fmt.Errorf("ramulator: handshake failed: %w", err)
	}

	return client, nil
}

// Enabled reports whether the Ramulator client should be used.
func (c *Client) Enabled() bool {
	return c != nil && c.enabled
}

// Estimate returns the cycles predicted by Ramulator. When the service cannot
// provide a value the function returns ok=false so the caller can fall back to
// the bandwidth model.
func (c *Client) Estimate(bytes int64, metadata map[string]interface{}) (int, bool) {
	if bytes <= 0 || !c.Enabled() {
		return 0, false
	}

	payload := make(map[string]interface{})
	payload["op"] = "estimate"
	payload["bytes"] = bytes
	payload["access"] = classifyAccess(metadata)
	payload["burst_bytes"] = extractInt64(metadata, "burst_bytes", int64(defaultBurstBytes))
	payload["base_addr"] = extractInt64(metadata, "base_addr", 0)
	payload["max_cycles"] = extractInt64(metadata, "max_cycles", int64(defaultMaxCycles))
	payload["drain_cycles"] = extractInt64(metadata, "drain_cycles", int64(defaultDrainCycles))
	if metadata != nil && len(metadata) > 0 {
		payload["metadata"] = metadata
	}

	resp, err := c.sendRequest(payload)
	if err != nil {
		c.disable(err)
		return 0, false
	}

	if !resp.OK || resp.Cycles <= 0 {
		return 0, false
	}
	return int(resp.Cycles + 0.5), true
}

// Close terminates the ramulator service and releases resources.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return c.lastErr
	}
	c.closed = true
	cancel := c.cancel
	stdin := c.stdin
	cmd := c.cmd
	c.stdin = nil
	c.stdout = nil
	c.cancel = nil
	c.cmd = nil
	c.enabled = false
	c.mu.Unlock()

	// Attempt a graceful shutdown.
	if stdin != nil {
		_, _ = io.WriteString(stdin, "{\"op\":\"shutdown\"}\n")
		_ = stdin.Close()
	}
	if cancel != nil {
		cancel()
	}
	if cmd != nil && cmd.Process != nil {
		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
		}()
		select {
		case err := <-done:
			return err
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}
	return c.lastErr
}

func (c *Client) disable(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled {
		return
	}
	c.enabled = false
	c.lastErr = err
	if c.cancel != nil {
		c.cancel()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}

func (c *Client) ping() error {
	resp, err := c.sendRequest(map[string]interface{}{"op": "ping"})
	if err != nil {
		return err
	}
	if !resp.OK {
		if resp.Error == "" {
			return errors.New("ping failed")
		}
		return fmt.Errorf("ping failed: %s", resp.Error)
	}
	return nil
}

func (c *Client) sendRequest(payload map[string]interface{}) (serviceResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return serviceResponse{}, errors.New("ramulator client closed")
	}
	if c.stdin == nil || c.stdout == nil {
		return serviceResponse{}, errors.New("ramulator client is not ready")
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return serviceResponse{}, err
	}
	raw = append(raw, '\n')
	if _, err := c.stdin.Write(raw); err != nil {
		return serviceResponse{}, err
	}

	type result struct {
		line  string
		err   error
		noise []string
	}
	ch := make(chan result, 1)
	go func() {
		noise := make([]string, 0, 8)
		for {
			line, readErr := c.stdout.ReadString('\n')
			trimmed := strings.TrimSpace(line)

			// Skip empty lines and non-JSON noise (e.g., service logs on stdout).
			if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
				if trimmed != "" && len(noise) < cap(noise) {
					noise = append(noise, trimmed)
				}
				if readErr != nil {
					ch <- result{line: "", err: readErr, noise: noise}
					return
				}
				continue
			}

			ch <- result{line: trimmed, err: readErr, noise: noise}
			return
		}
	}()

	select {
	case res := <-ch:
		if res.err != nil && !errors.Is(res.err, io.EOF) {
			return serviceResponse{}, annotateWithNoise(res.err, res.noise)
		}
		if res.line == "" {
			if res.err != nil {
				return serviceResponse{}, annotateWithNoise(res.err, res.noise)
			}
			return serviceResponse{}, annotateWithNoise(errors.New("empty response from ramulator service"), res.noise)
		}

		var resp serviceResponse
		if err := json.Unmarshal([]byte(res.line), &resp); err != nil {
			return serviceResponse{}, annotateWithNoise(fmt.Errorf("invalid response: %w", err), res.noise)
		}
		if !resp.OK && resp.Error != "" {
			return resp, errors.New(resp.Error)
		}
		return resp, nil
	case <-time.After(requestTimeout):
		if c.cancel != nil {
			c.cancel()
		}
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		return serviceResponse{}, errors.New("ramulator service timeout")
	}
}

func annotateWithNoise(err error, noise []string) error {
	if err == nil || len(noise) == 0 {
		return err
	}
	return fmt.Errorf("%w (ramulator stdout: %s)", err, strings.Join(noise, "; "))
}

func classifyAccess(metadata map[string]interface{}) string {
	if metadata == nil {
		return "read"
	}
	for _, key := range []string{"host_dma", "host_direction", "direction", "access"} {
		if raw, ok := metadata[key]; ok {
			if s, ok := raw.(string); ok {
				switch strings.ToLower(strings.TrimSpace(s)) {
				case "store", "write", "d2host":
					return "write"
				case "load", "read", "host2d", "host_to_digital":
					return "read"
				}
			}
		}
	}
	return "read"
}

func extractInt64(metadata map[string]interface{}, key string, fallback int64) int64 {
	if metadata == nil {
		return fallback
	}
	if raw, ok := metadata[key]; ok {
		switch v := raw.(type) {
		case int:
			return int64(v)
		case int32:
			return int64(v)
		case int64:
			return v
		case float32:
			return int64(v)
		case float64:
			return int64(v)
		case json.Number:
			if parsed, err := v.Int64(); err == nil {
				return parsed
			}
		}
	}
	return fallback
}

func locateServiceExecutable(configPath string) (string, error) {
	if override := strings.TrimSpace(os.Getenv(serviceEnvOverride)); override != "" {
		if existsAndExecutable(override) {
			return override, nil
		}
		return "", fmt.Errorf("ramulator service override not found: %s", override)
	}

	searchRoots := uniqueStrings([]string{
		filepath.Dir(configPath),
		mustGetwd(),
	})

	var candidates []string
	for _, root := range searchRoots {
		for dir := root; dir != string(filepath.Separator); {
			candidates = append(candidates,
				filepath.Join(dir, "ramulator2_service"),
				filepath.Join(dir, "ramulator_service"),
				filepath.Join(dir, "build", "ramulator2_service"),
				filepath.Join(dir, "build", "ramulator_service"),
				filepath.Join(dir, serviceRelativeHint, "build", "ramulator2_service"),
				filepath.Join(dir, serviceRelativeHint, "ramulator2_service"),
			)
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	for _, candidate := range candidates {
		if existsAndExecutable(candidate) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("ramulator service binary not found; set %s to override", serviceEnvOverride)
}

func existsAndExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir() && info.Mode()&0111 != 0
}

func uniqueStrings(input []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(input))
	for _, item := range input {
		if item == "" {
			continue
		}
		abs, err := filepath.Abs(item)
		if err != nil {
			abs = item
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		result = append(result, abs)
	}
	return result
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}
