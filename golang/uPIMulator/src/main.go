package main

import (
	"fmt"
	"os"
	"path/filepath"
	"uPIMulator/src/assembler"
	"uPIMulator/src/compiler"
	"uPIMulator/src/linker"
	"uPIMulator/src/misc"
	"uPIMulator/src/simulator"
)

func main() {
	command_line_parser := InitCommandLineParser()
	command_line_parser.Parse(os.Args)

	if command_line_parser.IsArgSet("help") {
		fmt.Printf("%s", command_line_parser.StringifyHelpMsgs())
	} else {
		misc.ConfigureRuntime(command_line_parser)
		mode := misc.RuntimePlatformMode()
		if mode == misc.PlatformModeChiplet {
			fmt.Println("[chiplet] 开始初始化 Chiplet 模式模拟器…")
		}

		command_line_validator := new(misc.CommandLineValidator)
		command_line_validator.Init(command_line_parser)
		command_line_validator.Validate()

		config_loader := new(misc.ConfigLoader)
		config_loader.Init()

		config_validator := new(misc.ConfigValidator)
		config_validator.Init(config_loader)
		config_validator.Validate()

		bin_dirpath := command_line_parser.StringParameter("bin_dirpath")
		args_filepath := filepath.Join(bin_dirpath, "args.txt")
		options_filepath := filepath.Join(bin_dirpath, "options.txt")

		args_file_dumper := new(misc.FileDumper)
		args_file_dumper.Init(args_filepath)
		args_file_dumper.WriteLines([]string{command_line_parser.StringifyArgs()})

		options_file_dumper := new(misc.FileDumper)
		options_file_dumper.Init(options_filepath)
		options_file_dumper.WriteLines([]string{command_line_parser.StringifyOptions()})

		compiler_ := new(compiler.Compiler)
		compiler_.Init(command_line_parser)
		compiler_.Compile()
		if mode == misc.PlatformModeChiplet {
			fmt.Println("[chiplet] 编译阶段完成，进入装配流程…")
		}

		if mode != misc.PlatformModeChiplet {
			linker_ := new(linker.Linker)
			linker_.Init(command_line_parser)
			linker_.Link()
		}

		assembler_ := new(assembler.Assembler)
		assembler_.Init(command_line_parser)
		assembler_.Assemble()
		if mode == misc.PlatformModeChiplet {
			fmt.Println("[chiplet] 装配完成，启动 Chiplet 平台模拟…")
		}

		simulator_ := new(simulator.Simulator)
		simulator_.Init(command_line_parser)

		for !simulator_.IsFinished() {
			simulator_.Cycle()
		}

		simulator_.Dump()
		simulator_.Fini()

		if mode == misc.PlatformModeChiplet {
			fmt.Println("[chiplet] Chiplet 模式模拟完成。")
		}
	}
}

func InitCommandLineParser() *misc.CommandLineParser {
	command_line_parser := new(misc.CommandLineParser)
	command_line_parser.Init()

	// NOTE(dongjae.lee@kaist.ac.kr): Explanation of verbose level
	// level 0: Only prints simulation output
	// level 1: level 0 + prints UPMEM instruction executed per each logic cycle
	// level 2: level + prints UPMEM register file values per each logic cycle
	command_line_parser.AddOption(misc.INT, "verbose", "0", "verbosity of the simulation")

	command_line_parser.AddOption(misc.INT, "num_simulation_threads", "16",
		"number of simulation threads to launch")

	command_line_parser.AddOption(misc.STRING, "benchmark", "BS", "benchmark name")
	command_line_parser.AddOption(
		misc.STRING,
		"platform_mode",
		string(misc.DefaultPlatformMode()),
		"simulation platform mode (upmem|chiplet)",
	)

	command_line_parser.AddOption(misc.INT, "num_channels", "1", "number of PIM memory channels")
	command_line_parser.AddOption(
		misc.INT,
		"num_ranks_per_channel",
		"1",
		"number of ranks per channel",
	)
	command_line_parser.AddOption(misc.INT, "num_dpus_per_rank", "1", "number of DPUs per rank")

	command_line_parser.AddOption(misc.INT, "num_tasklets", "1", "number of tasklets")
	command_line_parser.AddOption(misc.STRING, "data_prep_params", "8192",
		"data preparation parameter")

	command_line_parser.AddOption(
		misc.STRING,
		"memory_type",
		"rram",
		"type of the DPU memory system (mram|rram)",
	)
	command_line_parser.AddOption(
		misc.INT,
		"rram_read_latency",
		"40",
		"RRAM read latency in host cycles",
	)
	command_line_parser.AddOption(
		misc.INT,
		"rram_write_latency",
		"80",
		"RRAM write latency in host cycles",
	)
	command_line_parser.AddOption(
		misc.INT,
		"rram_program_pulses",
		"16",
		"RRAM program pulse count per write",
	)
	command_line_parser.AddOption(
		misc.INT,
		"rram_array_rows",
		"128",
		"RRAM array rows per tile",
	)
	command_line_parser.AddOption(
		misc.INT,
		"rram_array_cols",
		"128",
		"RRAM array columns per tile",
	)
	command_line_parser.AddOption(
		misc.INT,
		"rram_cell_precision",
		"2",
		"RRAM cell precision in bits (MLC, configurable)",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_num_digital",
		"4",
		"number of digital chiplets",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_num_rram",
		"8",
		"number of RRAM chiplets",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_digital_pes_per_chiplet",
		"4",
		"digital PE arrays per chiplet",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_digital_pe_rows",
		"128",
		"digital PE array rows",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_digital_pe_cols",
		"128",
		"digital PE array columns",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_digital_spus_per_chiplet",
		"4",
		"digital SPU clusters per chiplet",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_digital_clock_mhz",
		"1000",
		"digital chiplet clock frequency in MHz",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_rram_tiles_per_dim",
		"16",
		"RRAM tiles per dimension",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_rram_sas_per_tile_dim",
		"16",
		"sense arrays per tile dimension",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_rram_sa_rows",
		"128",
		"RRAM sense array rows",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_rram_sa_cols",
		"128",
		"RRAM sense array columns",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_rram_cell_bits",
		"2",
		"RRAM cell precision in bits",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_rram_dac_bits",
		"2",
		"RRAM DAC precision in bits",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_rram_adc_bits",
		"12",
		"RRAM ADC precision in bits",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_rram_clock_mhz",
		"800",
		"RRAM chiplet clock frequency in MHz",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_interconnect_clock_mhz",
		"600",
		"chiplet interconnect clock frequency in MHz",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_transfer_bw_dr",
		"4096",
		"transfer bandwidth from digital to RRAM chiplets (bytes/cycle)",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_transfer_bw_rd",
		"4096",
		"transfer bandwidth from RRAM to digital chiplets (bytes/cycle)",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_host_dma_bw",
		"8192",
		"host DMA bandwidth for DRAM access (bytes/cycle)",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_kv_cache_bytes",
		"268435456",
		"host KV cache容量（字节，<=0 表示禁用）",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_digital_activation_buffer",
		"8388608",
		"digital chiplet activation buffer size in bytes",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_digital_scratch_buffer",
		"8388608",
		"digital chiplet scratch buffer size in bytes",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_rram_input_buffer",
		"8388608",
		"RRAM chiplet input buffer size in bytes",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_rram_output_buffer",
		"8388608",
		"RRAM chiplet output buffer size in bytes",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_host_limit_resources",
		"0",
		"enable host-side resource limiting (0|1)",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_host_stream_total_batches",
		"1",
		"total host batches to stream (<=0 for unlimited)",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_host_stream_low_watermark",
		"1",
		"host streaming low watermark",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_host_stream_high_watermark",
		"2",
		"host streaming high watermark",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_host_dma_ramulator_enabled",
		"0",
		"enable Ramulator-backed host DMA timing (1=yes, 0=no)",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_noc_booksim_enabled",
		"0",
		"enable BookSim-backed on-chip network timing (1=yes, 0=no)",
	)
	command_line_parser.AddOption(
		misc.STRING,
		"chiplet_noc_booksim_config",
		"",
		"BookSim configuration file describing the NoC (optional)",
	)
	command_line_parser.AddOption(
		misc.STRING,
		"chiplet_noc_booksim_binary",
		"",
		"Path to the BookSim service executable (optional)",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_noc_booksim_timeout_ms",
		"5000",
		"BookSim request timeout in milliseconds (<=0 disables per-request timeout)",
	)
	command_line_parser.AddOption(
		misc.STRING,
		"chiplet_host_dma_ramulator_config",
		"",
		"Ramulator configuration file for host DMA (optional)",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_progress_interval",
		"1000",
		"chiplet 模式进度打印间隔（周期，<=0 关闭）",
	)
	command_line_parser.AddOption(
		misc.INT,
		"chiplet_stats_flush_interval",
		"0",
		"chiplet 统计文件刷新间隔（周期，<=0 仅在结束时刷新）",
	)
	command_line_parser.AddOption(
		misc.STRING,
		"chiplet_model_path",
		"",
		"path to an optional chiplet model JSON",
	)

	command_line_parser.AddOption(
		misc.STRING,
		"root_dirpath",
		"/home/via/uPIMulator/golang/uPIMulator",
		"path to the root directory",
	)

	command_line_parser.AddOption(misc.STRING, "bin_dirpath",
		"/home/via/uPIMulator/golang/uPIMulator/bin", "path to the bin directory")

	command_line_parser.AddOption(misc.STRING, "log_dirpath",
		"/home/via/uPIMulator/golang/log", "path to the log directory")

	command_line_parser.AddOption(misc.INT, "logic_frequency", "350", "DPU logic frequency in MHz")
	command_line_parser.AddOption(misc.INT, "memory_frequency", "2400",
		"DPU MRAM frequency in MHz")

	command_line_parser.AddOption(misc.INT, "num_pipeline_stages", "14",
		"number of DPU logic pipeline stages")
	command_line_parser.AddOption(misc.INT, "num_revolver_scheduling_cycles", "11",
		"number of DPU logic revolver scheduling cycles")

	command_line_parser.AddOption(misc.INT, "wordline_size", "1024",
		"row buffer size per single DPU's MRAM in bytes")
	command_line_parser.AddOption(misc.INT, "min_access_granularity", "8",
		"DPU MRAM's minimum access granularity in bytes")

	command_line_parser.AddOption(
		misc.INT,
		"t_rcd",
		"32",
		"DPU MRAM t_rcd timing parameter [cycle]",
	)
	command_line_parser.AddOption(
		misc.INT,
		"t_ras",
		"78",
		"DPU MRAM t_ras timing parameter [cycle]",
	)
	command_line_parser.AddOption(misc.INT, "t_rp", "32", "DPU MRAM t_rp timing parameter [cycle]")
	command_line_parser.AddOption(misc.INT, "t_cl", "32", "DPU MRAM t_cl timing parameter [cycle]")
	command_line_parser.AddOption(misc.INT, "t_bl", "8", "DPU MRAM t_bl timing parameter [cycle]")

	command_line_parser.AddOption(
		misc.INT,
		"read_bandwidth",
		"1",
		"read bandwidth per DPU per rank [bytes/cycle]",
	)
	command_line_parser.AddOption(
		misc.INT,
		"write_bandwidth",
		"3",
		"write bandwidth per DPU per rank [bytes/cycle]",
	)

	return command_line_parser
}
