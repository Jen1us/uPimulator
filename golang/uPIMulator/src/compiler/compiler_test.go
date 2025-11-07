package compiler

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

type kernelManifest struct {
	Kernels []kernelTemplate `json:"kernels"`
}

type kernelTemplate struct {
	Name     string                 `json:"name"`
	Target   string                 `json:"target"`
	Opcode   string                 `json:"opcode"`
	Default  map[string]interface{} `json:"default"`
	Metadata map[string]interface{} `json:"metadata"`
}

func TestEmitChipletKernelManifest(t *testing.T) {
	tempDir := t.TempDir()

	compiler := &Compiler{bin_dirpath: tempDir}
	compiler.EmitChipletKernelManifest()

	manifestPath := filepath.Join(tempDir, "chiplet_kernels.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	var manifest kernelManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}

	if len(manifest.Kernels) != 13 {
		t.Fatalf("expected 13 kernels, got %d", len(manifest.Kernels))
	}

	lookup := make(map[string]kernelTemplate)
	for _, k := range manifest.Kernels {
		lookup[k.Name] = k
		if k.Default == nil {
			t.Fatalf("kernel %s missing default section", k.Name)
		}
		if k.Metadata == nil {
			t.Fatalf("kernel %s missing metadata section", k.Name)
		}
	}

	tokenPrep, ok := lookup["token_prep"]
	if !ok {
		t.Fatalf("token_prep kernel not found")
	}
	assertIntField(t, tokenPrep.Default, "rows", 256)
	assertIntField(t, tokenPrep.Default, "latency", 64)
	assertStringField(t, tokenPrep.Metadata, "op", "token_prep")

	transferDr, ok := lookup["transfer_dr"]
	if !ok {
		t.Fatalf("transfer_dr kernel not found")
	}
	assertIntField(t, transferDr.Default, "bytes", 16*1024*1024)
	assertStringField(t, transferDr.Metadata, "direction", "digital_to_rram")

	moeSpu, ok := lookup["moe_gating_scores"]
	if !ok {
		t.Fatalf("moe_gating_scores kernel not found")
	}
	assertStringField(t, moeSpu.Metadata, "op", "moe_gating_scores")
	assertIntField(t, moeSpu.Default, "scalar_ops", 256*256)

	topk, ok := lookup["moe_topk_select"]
	if !ok {
		t.Fatalf("moe_topk_select kernel not found")
	}
	assertStringField(t, topk.Metadata, "op", "topk_select")
	assertIntField(t, topk.Default, "scalar_ops", 256*2)
}

func assertIntField(t *testing.T, m map[string]interface{}, key string, want int) {
	t.Helper()
	val, ok := m[key]
	if !ok {
		t.Fatalf("missing key %s", key)
	}
	switch v := val.(type) {
	case float64:
		if int(math.Round(v)) != want {
			t.Fatalf("unexpected value for %s: got %v want %d", key, v, want)
		}
	case int:
		if v != want {
			t.Fatalf("unexpected value for %s: got %d want %d", key, v, want)
		}
	default:
		t.Fatalf("unexpected type for %s: %T", key, val)
	}
}

func assertStringField(t *testing.T, m map[string]interface{}, key, want string) {
	t.Helper()
	val, ok := m[key]
	if !ok {
		t.Fatalf("missing key %s", key)
	}
	s, ok := val.(string)
	if !ok {
		t.Fatalf("unexpected type for %s: %T", key, val)
	}
	if s != want {
		t.Fatalf("unexpected value for %s: got %s want %s", key, s, want)
	}
}
