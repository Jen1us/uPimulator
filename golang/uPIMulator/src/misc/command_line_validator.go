package misc

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type CommandLineValidator struct {
	command_line_parser *CommandLineParser
}

func (this *CommandLineValidator) Init(command_line_parser *CommandLineParser) {
	this.command_line_parser = command_line_parser
}

func (this *CommandLineValidator) Validate() {
	if this.command_line_parser.IntParameter("num_simulation_threads") <= 0 {
		err := errors.New("num_simulation_threads <= 0")
		panic(err)
	}

	platform_mode := this.command_line_parser.StringParameter("platform_mode")
	if _, ok := PlatformModeFromString(platform_mode); !ok {
		err := fmt.Errorf("platform_mode %s is not supported", platform_mode)
		panic(err)
	}

	if platform_mode == string(PlatformModeChiplet) {
		if this.command_line_parser.IntParameter("chiplet_num_digital") <= 0 {
			err := errors.New("chiplet_num_digital <= 0")
			panic(err)
		}

		if this.command_line_parser.IntParameter("chiplet_num_rram") <= 0 {
			err := errors.New("chiplet_num_rram <= 0")
			panic(err)
		}

		if this.command_line_parser.IntParameter("chiplet_digital_pes_per_chiplet") <= 0 {
			err := errors.New("chiplet_digital_pes_per_chiplet <= 0")
			panic(err)
		}

		if this.command_line_parser.IntParameter("chiplet_digital_pe_rows") <= 0 {
			err := errors.New("chiplet_digital_pe_rows <= 0")
			panic(err)
		}

		if this.command_line_parser.IntParameter("chiplet_digital_pe_cols") <= 0 {
			err := errors.New("chiplet_digital_pe_cols <= 0")
			panic(err)
		}

		if this.command_line_parser.IntParameter("chiplet_digital_spus_per_chiplet") <= 0 {
			err := errors.New("chiplet_digital_spus_per_chiplet <= 0")
			panic(err)
		}

		if this.command_line_parser.IntParameter("chiplet_digital_clock_mhz") <= 0 {
			err := errors.New("chiplet_digital_clock_mhz <= 0")
			panic(err)
		}

		if this.command_line_parser.IntParameter("chiplet_rram_tiles_per_dim") <= 0 {
			err := errors.New("chiplet_rram_tiles_per_dim <= 0")
			panic(err)
		}

		if this.command_line_parser.IntParameter("chiplet_rram_sas_per_tile_dim") <= 0 {
			err := errors.New("chiplet_rram_sas_per_tile_dim <= 0")
			panic(err)
		}

		if this.command_line_parser.IntParameter("chiplet_rram_sa_rows") <= 0 {
			err := errors.New("chiplet_rram_sa_rows <= 0")
			panic(err)
		}

		if this.command_line_parser.IntParameter("chiplet_rram_sa_cols") <= 0 {
			err := errors.New("chiplet_rram_sa_cols <= 0")
			panic(err)
		}

		if this.command_line_parser.IntParameter("chiplet_rram_cell_bits") <= 0 {
			err := errors.New("chiplet_rram_cell_bits <= 0")
			panic(err)
		}

		if this.command_line_parser.IntParameter("chiplet_rram_dac_bits") <= 0 {
			err := errors.New("chiplet_rram_dac_bits <= 0")
			panic(err)
		}

		if this.command_line_parser.IntParameter("chiplet_rram_adc_bits") <= 0 {
			err := errors.New("chiplet_rram_adc_bits <= 0")
			panic(err)
		}

		if this.command_line_parser.IntParameter("chiplet_rram_clock_mhz") <= 0 {
			err := errors.New("chiplet_rram_clock_mhz <= 0")
			panic(err)
		}

		if this.command_line_parser.IntParameter("chiplet_interconnect_clock_mhz") <= 0 {
			err := errors.New("chiplet_interconnect_clock_mhz <= 0")
			panic(err)
		}

		if this.command_line_parser.IntParameter("chiplet_transfer_bw_dr") <= 0 {
			err := errors.New("chiplet_transfer_bw_dr <= 0")
			panic(err)
		}

		if this.command_line_parser.IntParameter("chiplet_transfer_bw_rd") <= 0 {
			err := errors.New("chiplet_transfer_bw_rd <= 0")
			panic(err)
		}

		if this.command_line_parser.IntParameter("chiplet_digital_activation_buffer") <= 0 {
			err := errors.New("chiplet_digital_activation_buffer <= 0")
			panic(err)
		}

		if this.command_line_parser.IntParameter("chiplet_digital_scratch_buffer") <= 0 {
			err := errors.New("chiplet_digital_scratch_buffer <= 0")
			panic(err)
		}

		if this.command_line_parser.IntParameter("chiplet_rram_input_buffer") <= 0 {
			err := errors.New("chiplet_rram_input_buffer <= 0")
			panic(err)
		}

		if this.command_line_parser.IntParameter("chiplet_rram_output_buffer") <= 0 {
			err := errors.New("chiplet_rram_output_buffer <= 0")
			panic(err)
		}

		modelPath := strings.TrimSpace(this.command_line_parser.StringParameter("chiplet_model_path"))
		if modelPath != "" {
			if _, statErr := os.Stat(modelPath); os.IsNotExist(statErr) {
				panic(fmt.Errorf("chiplet_model_path %s does not exist", modelPath))
			}
		}
	}

	memory_type := this.command_line_parser.StringParameter("memory_type")
	if memory_type != "mram" && memory_type != "rram" {
		err := fmt.Errorf("memory_type %s is not supported", memory_type)
		panic(err)
	}

	if this.command_line_parser.IntParameter("rram_read_latency") <= 0 {
		err := errors.New("rram_read_latency <= 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("rram_write_latency") <= 0 {
		err := errors.New("rram_write_latency <= 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("rram_program_pulses") <= 0 {
		err := errors.New("rram_program_pulses <= 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("rram_array_rows") <= 0 {
		err := errors.New("rram_array_rows <= 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("rram_array_cols") <= 0 {
		err := errors.New("rram_array_cols <= 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("rram_cell_precision") <= 0 {
		err := errors.New("rram_cell_precision <= 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("num_channels") <= 0 {
		err := errors.New("num_channels <= 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("num_ranks_per_channel") <= 0 {
		err := errors.New("num_ranks <= 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("num_dpus_per_rank") <= 0 {
		err := errors.New("num_dpus <= 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("num_tasklets") <= 0 {
		err := errors.New("num_tasklets <= 0")
		panic(err)
	}

	if _, stat_err := os.Stat(this.command_line_parser.StringParameter("root_dirpath")); os.IsNotExist(
		stat_err,
	) {
		fmt.Println(this.command_line_parser.StringParameter("root_dirpath"))

		err := errors.New("root_dirpath does not exist")
		panic(err)
	}

	if this.command_line_parser.IntParameter("logic_frequency") <= 0 {
		err := errors.New("logic_frequency <= 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("memory_frequency") <= 0 {
		err := errors.New("memory_frequency <= 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("num_pipeline_stages") <= 0 {
		err := errors.New("num_pipeline_stages <= 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("num_revolver_scheduling_cycles") < 0 {
		err := errors.New("num_revolver_scheduling_cycles < 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("wordline_size") <= 0 {
		err := errors.New("wordline_size <= 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("min_access_granularity") <= 0 {
		err := errors.New("min_access_granularity <= 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("min_access_granularity") <= 0 {
		err := errors.New("min_access_granularity <= 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("t_rcd") < 0 {
		err := errors.New("t_rcd < 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("t_ras") < 0 {
		err := errors.New("t_ras < 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("t_rp") < 0 {
		err := errors.New("t_rp < 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("t_cl") < 0 {
		err := errors.New("t_cl < 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("t_bl") < 0 {
		err := errors.New("t_bl < 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("read_bandwidth") <= 0 {
		err := errors.New("read_bandwidth <= 0")
		panic(err)
	}

	if this.command_line_parser.IntParameter("write_bandwidth") <= 0 {
		err := errors.New("write_bandwidth <= 0")
		panic(err)
	}
}
