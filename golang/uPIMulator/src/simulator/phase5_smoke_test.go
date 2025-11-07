package simulator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"uPIMulator/src/misc"
)

func TestChipletPhase5Smoke(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	parser := new(misc.CommandLineParser)
	parser.Init()
	parser.AddOption(misc.STRING, "bin_dirpath", tempDir, tempDir)
	parser.AddOption(misc.INT, "chiplet_progress_interval", "0", "disable progress logging for tests")
	parser.AddOption(misc.INT, "chiplet_stats_flush_interval", "0", "disable periodic stats flush for tests")

	platform := new(ChipletPlatform)
	platform.Init(parser)
	defer platform.Fini()

	maxCycles := 5000
	for i := 0; i < maxCycles; i++ {
		platform.Cycle()
	}

	platform.Dump()

	if exportDir := os.Getenv("CHIPLET_TEST_EXPORT_DIR"); exportDir != "" {
		if err := os.MkdirAll(exportDir, 0o755); err == nil {
			toCopy := []string{
				filepath.Join(tempDir, "chiplet_log.txt"),
				filepath.Join(tempDir, "chiplet_cycle_log.csv"),
				filepath.Join(tempDir, "chiplet_results.csv"),
			}
			for _, src := range toCopy {
				data, err := os.ReadFile(src)
				if err != nil {
					continue
				}
				dst := filepath.Join(exportDir, filepath.Base(src))
				_ = os.WriteFile(dst, data, 0o644)
			}
		}
	}

	if platform.executedRramTasks == 0 {
		t.Fatalf("expected RRAM tasks executed after %d cycles", maxCycles)
	}

	if platform.digitalDomainCycles <= 0 {
		t.Fatalf("expected digital domain cycles > 0, got %d", platform.digitalDomainCycles)
	}
	if platform.rramDomainCycles <= 0 {
		t.Fatalf("expected rram domain cycles > 0, got %d", platform.rramDomainCycles)
	}
	if len(platform.resultLog) <= 1 {
		t.Fatalf("expected result entries, got %d", len(platform.resultLog))
	}

	cyclePath := filepath.Join(tempDir, "chiplet_cycle_log.csv")
	cycleData, err := os.ReadFile(cyclePath)
	if err != nil {
		t.Fatalf("reading cycle log: %v", err)
	}
	cycleLines := strings.Split(strings.TrimSpace(string(cycleData)), "\n")
	if len(cycleLines) <= 1 {
		t.Fatalf("expected cycle log entries, got %d lines", len(cycleLines))
	}
	expectedHeader := []string{
		"cycle",
		"digital_exec",
		"digital_completed",
		"rram_exec",
		"transfer_exec",
		"transfer_bytes",
		"transfer_hops",
		"host_dma_load_bytes",
		"host_dma_store_bytes",
		"kv_hits",
		"kv_misses",
		"kv_load_bytes",
		"kv_store_bytes",
		"digital_load_bytes",
		"digital_store_bytes",
		"digital_pe_active",
		"digital_spu_active",
		"digital_vpu_active",
		"throttle_until",
		"throttle_events",
		"deferrals",
		"avg_wait",
		"digital_util",
		"rram_util",
		"digital_ticks",
		"rram_ticks",
		"interconnect_ticks",
		"host_tasks",
		"outstanding_digital",
		"outstanding_rram",
		"outstanding_transfer",
		"outstanding_dma",
		"transfer_to_rram_bytes",
		"transfer_to_digital_bytes",
		"transfer_host_load_bytes",
		"transfer_host_store_bytes",
		"transfer_throttle_events_total",
		"transfer_throttle_cycles_total",
	}
	if header := cycleLines[0]; header != strings.Join(expectedHeader, ",") {
		t.Fatalf("unexpected cycle log header: %s", header)
	}
	dataFields := strings.Split(cycleLines[1], ",")
	if len(dataFields) != len(expectedHeader) {
		t.Fatalf("expected %d columns, got %d", len(expectedHeader), len(dataFields))
	}

	resultPath := filepath.Join(tempDir, "chiplet_results.csv")
	resultData, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("reading result log: %v", err)
	}
	if lines := strings.Split(strings.TrimSpace(string(resultData)), "\n"); len(lines) <= 1 {
		t.Fatalf("expected result log entries, got %d lines", len(lines))
	}

	logPath := filepath.Join(tempDir, "chiplet_log.txt")
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading chiplet log: %v", err)
	}
	logText := string(logData)
	requiredKeys := []string{
		"ChipletPlatform_digital_load_bytes_runtime_total",
		"ChipletPlatform_digital_store_bytes_runtime_total",
		"ChipletPlatform_digital_tasks_completed_total",
		"ChipletPlatform_transfer_to_rram_bytes_total",
		"ChipletPlatform_transfer_to_digital_bytes_total",
		"ChipletPlatform_transfer_host_load_bytes_total",
		"ChipletPlatform_transfer_host_store_bytes_total",
		"ChipletPlatform_transfer_throttle_events_total",
		"ChipletPlatform_transfer_throttle_cycles_total",
		"ChipletPlatform_kv_cache_loads_total",
		"ChipletPlatform_kv_cache_hits_total",
	}
	for _, key := range requiredKeys {
		if !strings.Contains(logText, key+":") {
			t.Fatalf("missing %s in chiplet_log.txt", key)
		}
	}
}
