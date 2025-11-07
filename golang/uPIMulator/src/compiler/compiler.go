package compiler

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"uPIMulator/src/misc"
)

type Compiler struct {
	command_line_parser *misc.CommandLineParser

	root_dirpath string
	bin_dirpath  string
	benchmark    string

	num_dpus        int
	num_tasklets    int
	dockerAvailable bool
}

func (this *Compiler) Init(command_line_parser *misc.CommandLineParser) {
	this.command_line_parser = command_line_parser

	this.root_dirpath = command_line_parser.StringParameter("root_dirpath")
	this.bin_dirpath = command_line_parser.StringParameter("bin_dirpath")
	this.benchmark = command_line_parser.StringParameter("benchmark")

	num_channels := int(command_line_parser.IntParameter("num_channels"))
	num_ranks_per_channel := int(command_line_parser.IntParameter("num_ranks_per_channel"))
	num_dpus_per_rank := int(command_line_parser.IntParameter("num_dpus_per_rank"))
	this.num_dpus = num_channels * num_ranks_per_channel * num_dpus_per_rank

	this.num_tasklets = int(command_line_parser.IntParameter("num_tasklets"))

	if _, err := exec.LookPath("docker"); err == nil {
		this.dockerAvailable = true
		this.Build()
	} else {
		fmt.Println("warning: docker not found in PATH; skipping containerized build steps")
		this.dockerAvailable = false
	}
}

func (this *Compiler) Build() {
	if !this.dockerAvailable {
		return
	}
	docker_dirpath := filepath.Join(this.root_dirpath, "docker")

	command := exec.Command(
		"docker",
		"buildx",
		"build",
		"--platform",
		"linux/amd64",
		"-t",
		"bongjoonhyun/upimulator",
		"--load",
		docker_dirpath,
	)

	err := command.Run()

	if err != nil {
		panic(err)
	}
}

func (this *Compiler) Compile() {
	mode := misc.RuntimePlatformMode()

	if mode == misc.PlatformModeChiplet {
		this.EmitChipletKernelManifest()
	}

	if !this.dockerAvailable {
		return
	}

	if mode == misc.PlatformModeChiplet {
		return
	}

	this.CompileBenchmark()
	this.CompileSdk()
}

func (this *Compiler) CompileBenchmark() {
	command := exec.Command(
		"docker",
		"run",
		"--privileged",
		"--rm",
		"--platform",
		"linux/amd64",
		"-v",
		this.root_dirpath+":/root/uPIMulator",
		"bongjoonhyun/upimulator",
		"python3",
		"/root/uPIMulator/benchmark/build.py",
		"--num_dpus",
		strconv.Itoa(this.num_dpus),
		"--num_tasklets",
		strconv.Itoa(this.num_tasklets),
	)

	output, err := command.CombinedOutput()

	if err != nil {
		fmt.Println("docker benchmark command failed")
		fmt.Printf("docker benchmark output: %q\n", string(output))
		panic(err)
	}
}

func (this *Compiler) CompileSdk() {
	command := exec.Command(
		"docker",
		"run",
		"--privileged",
		"--rm",
		"--platform",
		"linux/amd64",
		"-v",
		this.root_dirpath+":/root/uPIMulator",
		"bongjoonhyun/upimulator",
		"python3",
		"/root/uPIMulator/sdk/build.py",
		"--num_tasklets",
		strconv.Itoa(this.num_tasklets),
	)

	output, err := command.CombinedOutput()

	if err != nil {
		fmt.Println("docker sdk command failed")
		fmt.Printf("docker sdk output: %q\n", string(output))
		panic(err)
	}
}

func (this *Compiler) EmitChipletKernelManifest() {
	if this.bin_dirpath == "" {
		return
	}
	if err := os.MkdirAll(this.bin_dirpath, 0o755); err != nil {
		panic(err)
	}

	type kernelTemplate struct {
		Name     string                 `json:"name"`
		Target   string                 `json:"target"`
		Opcode   string                 `json:"opcode"`
		Default  map[string]interface{} `json:"default,omitempty"`
		Metadata map[string]interface{} `json:"metadata,omitempty"`
	}

	kernels := []kernelTemplate{
		{
			Name:   "token_prep",
			Target: "digital",
			Opcode: "pe_cmd_token_prep",
			Default: map[string]interface{}{
				"rows":     256,
				"cols":     256,
				"tokens":   256,
				"features": 256,
				"latency":  64,
			},
			Metadata: map[string]interface{}{
				"op": "token_prep",
			},
		},
		{
			Name:   "attention_head",
			Target: "digital",
			Opcode: "pe_cmd_attention_head",
			Default: map[string]interface{}{
				"rows":    256,
				"cols":    256,
				"k":       256,
				"latency": 64,
			},
			Metadata: map[string]interface{}{
				"op":         "attention",
				"activation": "fp16",
			},
		},
		{
			Name:   "elementwise_post",
			Target: "digital",
			Opcode: "pe_cmd_elementwise",
			Default: map[string]interface{}{
				"rows":    256,
				"cols":    256,
				"latency": 16,
			},
			Metadata: map[string]interface{}{
				"op": "elementwise",
			},
		},
		{
			Name:   "gemm_tile",
			Target: "digital",
			Opcode: "pe_cmd_gemm",
			Default: map[string]interface{}{
				"rows":             256,
				"cols":             256,
				"k":                128,
				"activation_bytes": 256 * 128 * 2,
				"weight_bytes":     128 * 256 / 2,
				"output_bytes":     256 * 256 * 2,
				"latency":          128,
			},
			Metadata: map[string]interface{}{
				"op": "gemm",
			},
		},
		{
			Name:   "moe_gating_scores",
			Target: "digital",
			Opcode: "pe_cmd_spu_op",
			Default: map[string]interface{}{
				"tokens":     256,
				"features":   256,
				"scalar_ops": 256 * 256,
				"vector_ops": 256 * 256,
				"latency":    24,
			},
			Metadata: map[string]interface{}{
				"op":        "moe_gating_scores",
				"precision": "fp16",
			},
		},
		{
			Name:   "moe_topk_select",
			Target: "digital",
			Opcode: "pe_cmd_reduce",
			Default: map[string]interface{}{
				"tokens":     256,
				"top_k":      2,
				"scalar_ops": 256 * 2,
				"latency":    16,
			},
			Metadata: map[string]interface{}{
				"op":          "topk_select",
				"reduce_kind": "topk",
			},
		},
		{
			Name:   "layernorm_spu",
			Target: "digital",
			Opcode: "pe_cmd_spu_op",
			Default: map[string]interface{}{
				"rows":       256,
				"cols":       256,
				"scalar_ops": 256 * 256,
				"vector_ops": 256 * 256,
				"latency":    32,
			},
			Metadata: map[string]interface{}{
				"op": "layernorm",
			},
		},
		{
			Name:   "swiglu_vpu",
			Target: "digital",
			Opcode: "pe_cmd_vpu_op",
			Default: map[string]interface{}{
				"rows":       256,
				"cols":       256,
				"vector_ops": 256 * 256,
				"latency":    32,
			},
			Metadata: map[string]interface{}{
				"op": "swiglu",
			},
		},
		{
			Name:   "rram_stage",
			Target: "rram",
			Opcode: "rram_cmd_stage_act",
			Default: map[string]interface{}{
				"rows":             128,
				"cols":             128,
				"k":                128,
				"activation_bytes": 128 * 128 * 2,
				"weight_bytes":     128 * 128 / 2,
				"output_bytes":     128 * 128 * 2,
				"pulse_count":      32,
				"pre_cycles":       12,
			},
			Metadata: map[string]interface{}{
				"stage": "stage",
			},
		},
		{
			Name:   "rram_execute",
			Target: "rram",
			Opcode: "rram_cmd_execute",
			Default: map[string]interface{}{
				"rows":        128,
				"cols":        128,
				"k":           128,
				"pulse_count": 32,
				"adc_samples": 2048,
				"latency":     64,
			},
			Metadata: map[string]interface{}{
				"stage": "execute",
			},
		},
		{
			Name:   "rram_post",
			Target: "rram",
			Opcode: "rram_cmd_post",
			Default: map[string]interface{}{
				"rows":        128,
				"cols":        128,
				"k":           128,
				"post_cycles": 10,
				"latency":     12,
			},
			Metadata: map[string]interface{}{
				"stage": "post",
			},
		},
		{
			Name:   "transfer_dr",
			Target: "transfer",
			Opcode: "xfer_cmd_schedule",
			Default: map[string]interface{}{
				"bytes":   16 * 1024 * 1024,
				"latency": 1024,
				"src":     "digital",
				"dst":     "rram",
			},
			Metadata: map[string]interface{}{
				"direction": "digital_to_rram",
				"op":        "transfer_host2cim",
			},
		},
		{
			Name:   "transfer_rd",
			Target: "transfer",
			Opcode: "xfer_cmd_schedule",
			Default: map[string]interface{}{
				"bytes":   16 * 1024 * 1024,
				"latency": 1024,
				"src":     "rram",
				"dst":     "digital",
			},
			Metadata: map[string]interface{}{
				"direction": "rram_to_digital",
				"op":        "transfer_cim2digital",
			},
		},
	}

	manifest := map[string]interface{}{
		"kernels": kernels,
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		panic(err)
	}

	path := filepath.Join(this.bin_dirpath, "chiplet_kernels.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("[chiplet] 已生成内核清单：%s\n", path)
}
