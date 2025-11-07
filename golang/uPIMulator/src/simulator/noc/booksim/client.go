package booksim

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
	serviceEnvOverride = "UPIMULATOR_BOOKSIM_SERVICE"
	defaultTimeout     = 30 * time.Second
)

// Response payload produced by booksim_service.
type serviceResponse struct {
	OK      bool    `json:"ok"`
	Cycles  float64 `json:"cycles,omitempty"`
	Error   string  `json:"error,omitempty"`
	Details string  `json:"details,omitempty"`
}

// Client 管理 BookSim 服务进程并提供延迟估算接口。
type Client struct {
	mu         sync.Mutex
	configPath string
	binaryPath string
	timeout    time.Duration
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     *bufio.Reader
	cancel     context.CancelFunc
	enabled    bool
	closed     bool
	lastErr    error
}

// NewClient 启动 booksim_service 并完成握手。
func NewClient(binaryPath, configPath string, timeout time.Duration) (*Client, error) {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return nil, fmt.Errorf("booksim config path is empty")
	}
	absConfig, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("booksim: resolve config path: %w", err)
	}

	servicePath, err := locateServiceExecutable(binaryPath, absConfig)
	if err != nil {
		return nil, err
	}

	if timeout <= 0 {
		timeout = 0
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, servicePath, "--config_file", absConfig)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("booksim: stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("booksim: stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("booksim: start service: %w", err)
	}

	client := &Client{
		configPath: absConfig,
		binaryPath: servicePath,
		timeout:    timeout,
		cmd:        cmd,
		stdin:      stdin,
		stdout:     bufio.NewReader(stdoutPipe),
		cancel:     cancel,
		enabled:    true,
	}
	runtime.SetFinalizer(client, func(c *Client) {
		_ = c.Close()
	})

	if err := client.ping(); err != nil {
		client.Close()
		return nil, fmt.Errorf("booksim: handshake failed: %w", err)
	}

	return client, nil
}

// Enabled 返回客户端是否处于可用状态。
func (c *Client) Enabled() bool {
	return c != nil && c.enabled
}

// Estimate 计算 (src,dst) 之间传输指定字节数的延迟（周期）。
func (c *Client) Estimate(src, dst int, bytes int64, metadata map[string]interface{}) (int, bool) {
	if c == nil || !c.Enabled() || bytes <= 0 {
		return 0, false
	}

	payload := map[string]interface{}{
		"op":    "estimate",
		"src":   src,
		"dst":   dst,
		"bytes": bytes,
	}
	// 目前 BookSim 服务忽略 metadata，为简化解析，这里不发送。

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

// Close 终止服务进程。
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

// ping 检查服务可用性。
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
		return serviceResponse{}, errors.New("booksim client closed")
	}
	if c.stdin == nil || c.stdout == nil {
		return serviceResponse{}, errors.New("booksim client is not ready")
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

	deadline := defaultTimeout
	if c.timeout > 0 {
		deadline = c.timeout
	}

	select {
	case res := <-ch:
		if res.err != nil && !errors.Is(res.err, io.EOF) {
			return serviceResponse{}, annotateWithNoise(res.err, res.noise)
		}
		if res.line == "" {
			if res.err != nil {
				return serviceResponse{}, annotateWithNoise(res.err, res.noise)
			}
			return serviceResponse{}, annotateWithNoise(errors.New("empty response from booksim service"), res.noise)
		}
		var resp serviceResponse
		if err := json.Unmarshal([]byte(res.line), &resp); err != nil {
			return serviceResponse{}, annotateWithNoise(fmt.Errorf("invalid response: %w", err), res.noise)
		}
		if !resp.OK && resp.Error != "" {
			return resp, errors.New(resp.Error)
		}
		return resp, nil
	case <-time.After(deadline):
		if c.cancel != nil {
			c.cancel()
		}
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		return serviceResponse{}, errors.New("booksim service timeout")
	}
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

func annotateWithNoise(err error, noise []string) error {
	if err == nil || len(noise) == 0 {
		return err
	}
	return fmt.Errorf("%w (booksim stdout: %s)", err, strings.Join(noise, "; "))
}

func locateServiceExecutable(binaryPath, configPath string) (string, error) {
	if override := strings.TrimSpace(os.Getenv(serviceEnvOverride)); override != "" {
		if existsAndExecutable(override) {
			return override, nil
		}
		return "", fmt.Errorf("booksim service override not found: %s", override)
	}

	if strings.TrimSpace(binaryPath) != "" {
		if existsAndExecutable(binaryPath) {
			if resolved, err := filepath.Abs(binaryPath); err == nil {
				return resolved, nil
			}
			return binaryPath, nil
		}
		return "", fmt.Errorf("booksim service binary not found: %s", binaryPath)
	}

	searchRoots := uniqueStrings([]string{
		filepath.Dir(configPath),
		mustGetwd(),
	})

	candidates := make([]string, 0, 16)
	for _, root := range searchRoots {
		for dir := root; dir != string(filepath.Separator); {
			candidates = append(candidates,
				filepath.Join(dir, "booksim_service"),
				filepath.Join(dir, "build", "booksim_service"),
				filepath.Join(dir, "booksim2", "build", "booksim_service"),
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
			if resolved, err := filepath.Abs(candidate); err == nil {
				return resolved, nil
			}
			return candidate, nil
		}
	}

	return "", fmt.Errorf("booksim service binary not found; set %s to override", serviceEnvOverride)
}

func existsAndExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir() && info.Mode()&0o111 != 0
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
