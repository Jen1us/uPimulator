package misc

import (
	"os"
	"path/filepath"
	"strings"
)

type ConfigLoader struct{}

type runtimeConfig struct {
	memoryType        string
	rramReadLatency   int
	rramWriteLatency  int
	rramProgramPulses int
	rramArrayRows     int
	rramArrayCols     int
	rramCellPrecision int
	rramDataWidth     int
	rramOffset        int64
	rramSize          int64
}

type chipletRuntimeConfig struct {
	numDigitalChiplets      int
	numRramChiplets         int
	digitalPesPerChiplet    int
	digitalPeRows           int
	digitalPeCols           int
	digitalSpusPerChiplet   int
	digitalClockMhz         int
	rramTilesPerDim         int
	rramSasPerTileDim       int
	rramSaRows              int
	rramSaCols              int
	rramCellBits            int
	rramDacBits             int
	rramAdcBits             int
	rramClockMhz            int
	interconnectClockMhz    int
	transferBandwidthDr     int64
	transferBandwidthRd     int64
	hostDmaBandwidth        int64
	hostDmaUseRamulator     bool
	hostDmaRamulatorConfig  string
	nocUseBooksim           bool
	nocBooksimConfig        string
	nocBooksimBinary        string
	nocBooksimTimeoutMs     int
	kvCacheBytes            int64
	digitalActivationBuffer int64
	digitalScratchBuffer    int64
	rramInputBuffer         int64
	rramOutputBuffer        int64
	hostLimitResources      bool
	hostStreamTotalBatches  int
	hostStreamLowWatermark  int
	hostStreamHighWatermark int
}

var globalConfig = runtimeConfig{
	memoryType:        "mram",
	rramReadLatency:   40,
	rramWriteLatency:  80,
	rramProgramPulses: 16,
	rramArrayRows:     128,
	rramArrayCols:     128,
	rramCellPrecision: 2,
	rramDataWidth:     8,
	rramOffset:        640 * 1024 * 1024,
	rramSize:          64 * 1024 * 1024,
}

var globalChipletConfig = chipletRuntimeConfig{
	numDigitalChiplets:      4,
	numRramChiplets:         8,
	digitalPesPerChiplet:    4,
	digitalPeRows:           128,
	digitalPeCols:           128,
	digitalSpusPerChiplet:   4,
	digitalClockMhz:         1000,
	rramTilesPerDim:         16,
	rramSasPerTileDim:       16,
	rramSaRows:              128,
	rramSaCols:              128,
	rramCellBits:            2,
	rramDacBits:             2,
	rramAdcBits:             12,
	rramClockMhz:            800,
	interconnectClockMhz:    600,
	transferBandwidthDr:     4096,
	transferBandwidthRd:     4096,
	hostDmaBandwidth:        8192,
	hostDmaUseRamulator:     false,
	hostDmaRamulatorConfig:  "",
	nocUseBooksim:           false,
	nocBooksimConfig:        "",
	nocBooksimBinary:        "",
	nocBooksimTimeoutMs:     5000,
	kvCacheBytes:            256 * 1024 * 1024,
	digitalActivationBuffer: 8 * 1024 * 1024,
	digitalScratchBuffer:    8 * 1024 * 1024,
	rramInputBuffer:         8 * 1024 * 1024,
	rramOutputBuffer:        8 * 1024 * 1024,
	hostLimitResources:      false,
	hostStreamTotalBatches:  1,
	hostStreamLowWatermark:  1,
	hostStreamHighWatermark: 2,
}

func ConfigureRuntime(parser *CommandLineParser) {
	if parser == nil {
		return
	}

	if mode, ok := PlatformModeFromString(parser.StringParameter("platform_mode")); ok {
		SetRuntimePlatformMode(mode)
	}

	globalConfig.memoryType = parser.StringParameter("memory_type")
	globalConfig.rramReadLatency = int(parser.IntParameter("rram_read_latency"))
	globalConfig.rramWriteLatency = int(parser.IntParameter("rram_write_latency"))
	globalConfig.rramProgramPulses = int(parser.IntParameter("rram_program_pulses"))
	globalConfig.rramArrayRows = int(parser.IntParameter("rram_array_rows"))
	globalConfig.rramArrayCols = int(parser.IntParameter("rram_array_cols"))
	globalConfig.rramCellPrecision = int(parser.IntParameter("rram_cell_precision"))
	globalConfig.rramDataWidth = 8

	globalChipletConfig.numDigitalChiplets = int(parser.IntParameter("chiplet_num_digital"))
	globalChipletConfig.numRramChiplets = int(parser.IntParameter("chiplet_num_rram"))
	globalChipletConfig.digitalPesPerChiplet = int(parser.IntParameter("chiplet_digital_pes_per_chiplet"))
	globalChipletConfig.digitalPeRows = int(parser.IntParameter("chiplet_digital_pe_rows"))
	globalChipletConfig.digitalPeCols = int(parser.IntParameter("chiplet_digital_pe_cols"))
	globalChipletConfig.digitalSpusPerChiplet = int(parser.IntParameter("chiplet_digital_spus_per_chiplet"))
	globalChipletConfig.digitalClockMhz = int(parser.IntParameter("chiplet_digital_clock_mhz"))
	globalChipletConfig.rramTilesPerDim = int(parser.IntParameter("chiplet_rram_tiles_per_dim"))
	globalChipletConfig.rramSasPerTileDim = int(parser.IntParameter("chiplet_rram_sas_per_tile_dim"))
	globalChipletConfig.rramSaRows = int(parser.IntParameter("chiplet_rram_sa_rows"))
	globalChipletConfig.rramSaCols = int(parser.IntParameter("chiplet_rram_sa_cols"))
	globalChipletConfig.rramCellBits = int(parser.IntParameter("chiplet_rram_cell_bits"))
	globalChipletConfig.rramDacBits = int(parser.IntParameter("chiplet_rram_dac_bits"))
	globalChipletConfig.rramAdcBits = int(parser.IntParameter("chiplet_rram_adc_bits"))
	globalChipletConfig.rramClockMhz = int(parser.IntParameter("chiplet_rram_clock_mhz"))
	globalChipletConfig.interconnectClockMhz = int(parser.IntParameter("chiplet_interconnect_clock_mhz"))
	globalChipletConfig.transferBandwidthDr = int64(parser.IntParameter("chiplet_transfer_bw_dr"))
	globalChipletConfig.transferBandwidthRd = int64(parser.IntParameter("chiplet_transfer_bw_rd"))
	globalChipletConfig.hostDmaBandwidth = int64(parser.IntParameter("chiplet_host_dma_bw"))
	globalChipletConfig.hostDmaUseRamulator = parser.IntParameter("chiplet_host_dma_ramulator_enabled") != 0
	rootDir := parser.StringParameter("root_dirpath")
	rawRamulatorConfig := parser.StringParameter("chiplet_host_dma_ramulator_config")
	globalChipletConfig.hostDmaRamulatorConfig = resolveRamulatorConfigPath(rawRamulatorConfig, rootDir)
	globalChipletConfig.nocUseBooksim = parser.IntParameter("chiplet_noc_booksim_enabled") != 0
	rawBooksimConfig := parser.StringParameter("chiplet_noc_booksim_config")
	globalChipletConfig.nocBooksimConfig = resolveConfigPath(rawBooksimConfig, rootDir)
	rawBooksimBinary := parser.StringParameter("chiplet_noc_booksim_binary")
	globalChipletConfig.nocBooksimBinary = resolveExecutablePath(rawBooksimBinary, rootDir)
	globalChipletConfig.nocBooksimTimeoutMs = int(parser.IntParameter("chiplet_noc_booksim_timeout_ms"))
	globalChipletConfig.kvCacheBytes = int64(parser.IntParameter("chiplet_kv_cache_bytes"))
	globalChipletConfig.digitalActivationBuffer = int64(parser.IntParameter("chiplet_digital_activation_buffer"))
	globalChipletConfig.digitalScratchBuffer = int64(parser.IntParameter("chiplet_digital_scratch_buffer"))
	globalChipletConfig.rramInputBuffer = int64(parser.IntParameter("chiplet_rram_input_buffer"))
	globalChipletConfig.rramOutputBuffer = int64(parser.IntParameter("chiplet_rram_output_buffer"))
	globalChipletConfig.hostLimitResources = parser.IntParameter("chiplet_host_limit_resources") != 0
	globalChipletConfig.hostStreamTotalBatches = int(parser.IntParameter("chiplet_host_stream_total_batches"))
	globalChipletConfig.hostStreamLowWatermark = int(parser.IntParameter("chiplet_host_stream_low_watermark"))
	globalChipletConfig.hostStreamHighWatermark = int(parser.IntParameter("chiplet_host_stream_high_watermark"))
}

func (this *ConfigLoader) Init() {}

func (this *ConfigLoader) AddressWidth() int {
	return 32
}

func (this *ConfigLoader) AtomicDataWidth() int {
	return 32
}

func (this *ConfigLoader) AtomicOffset() int64 {
	return 0
}

func (this *ConfigLoader) AtomicSize() int64 {
	return 256
}

func (this *ConfigLoader) IramDataWidth() int {
	return 96
}

func (this *ConfigLoader) IramOffset() int64 {
	return 384 * 1024
}

func (this *ConfigLoader) IramSize() int64 {
	return 48 * 1024
}

func (this *ConfigLoader) WramDataWidth() int {
	return 32
}

func (this *ConfigLoader) WramOffset() int64 {
	return 512
}

func (this *ConfigLoader) WramSize() int64 {
	return 128 * 1024
}

func (this *ConfigLoader) MramDataWidth() int {
	return 32
}

func (this *ConfigLoader) MramOffset() int64 {
	if globalConfig.memoryType == "rram" {
		return globalConfig.rramOffset
	}
	return 512 * 1024
}

func (this *ConfigLoader) MramSize() int64 {
	if globalConfig.memoryType == "rram" {
		return globalConfig.rramSize
	}
	return 64 * 1024 * 1024
}

func (this *ConfigLoader) RramDataWidth() int {
	return globalConfig.rramDataWidth
}

func (this *ConfigLoader) RramOffset() int64 {
	return globalConfig.rramOffset
}

func (this *ConfigLoader) RramSize() int64 {
	return globalConfig.rramSize
}

func (this *ConfigLoader) RramArrayRows() int {
	return globalConfig.rramArrayRows
}

func (this *ConfigLoader) RramArrayCols() int {
	return globalConfig.rramArrayCols
}

func (this *ConfigLoader) RramCellPrecision() int {
	return globalConfig.rramCellPrecision
}

func (this *ConfigLoader) RramReadLatency() int {
	return globalConfig.rramReadLatency
}

func (this *ConfigLoader) RramWriteLatency() int {
	return globalConfig.rramWriteLatency
}

func (this *ConfigLoader) RramProgramPulses() int {
	return globalConfig.rramProgramPulses
}

func (this *ConfigLoader) MemoryType() string {
	return globalConfig.memoryType
}

func (this *ConfigLoader) PrimaryMemoryOffset() int64 {
	if globalConfig.memoryType == "rram" {
		return globalConfig.rramOffset
	}
	return this.MramOffset()
}

func (this *ConfigLoader) PrimaryMemorySize() int64 {
	if globalConfig.memoryType == "rram" {
		return globalConfig.rramSize
	}
	return this.MramSize()
}

func (this *ConfigLoader) PrimaryMemoryDataWidth() int {
	if globalConfig.memoryType == "rram" {
		return globalConfig.rramDataWidth
	}
	return this.MramDataWidth()
}

func (this *ConfigLoader) StackSize() int64 {
	return 2 * 1024
}

func (this *ConfigLoader) HeapSize() int64 {
	return 4 * 1024
}

func (this *ConfigLoader) NumGpRegisters() int {
	return 24
}

func (this *ConfigLoader) MaxNumTasklets() int {
	return 24
}

func (this *ConfigLoader) ChipletNumDigitalChiplets() int {
	return globalChipletConfig.numDigitalChiplets
}

func (this *ConfigLoader) ChipletNumRramChiplets() int {
	return globalChipletConfig.numRramChiplets
}

func (this *ConfigLoader) ChipletDigitalPesPerChiplet() int {
	return globalChipletConfig.digitalPesPerChiplet
}

func (this *ConfigLoader) ChipletDigitalPeRows() int {
	return globalChipletConfig.digitalPeRows
}

func (this *ConfigLoader) ChipletDigitalPeCols() int {
	return globalChipletConfig.digitalPeCols
}

func (this *ConfigLoader) ChipletDigitalSpusPerChiplet() int {
	return globalChipletConfig.digitalSpusPerChiplet
}

func (this *ConfigLoader) ChipletDigitalClockMhz() int {
	return globalChipletConfig.digitalClockMhz
}

func (this *ConfigLoader) ChipletRramTilesPerDim() int {
	return globalChipletConfig.rramTilesPerDim
}

func (this *ConfigLoader) ChipletRramSasPerTileDim() int {
	return globalChipletConfig.rramSasPerTileDim
}

func (this *ConfigLoader) ChipletRramSaRows() int {
	return globalChipletConfig.rramSaRows
}

func (this *ConfigLoader) ChipletRramSaCols() int {
	return globalChipletConfig.rramSaCols
}

func (this *ConfigLoader) ChipletRramCellBits() int {
	return globalChipletConfig.rramCellBits
}

func (this *ConfigLoader) ChipletRramDacBits() int {
	return globalChipletConfig.rramDacBits
}

func (this *ConfigLoader) ChipletRramAdcBits() int {
	return globalChipletConfig.rramAdcBits
}

func (this *ConfigLoader) ChipletRramClockMhz() int {
	return globalChipletConfig.rramClockMhz
}

func (this *ConfigLoader) ChipletInterconnectClockMhz() int {
	return globalChipletConfig.interconnectClockMhz
}

func (this *ConfigLoader) ChipletTransferBandwidthDr() int64 {
	return globalChipletConfig.transferBandwidthDr
}

func (this *ConfigLoader) ChipletTransferBandwidthRd() int64 {
	return globalChipletConfig.transferBandwidthRd
}

func (this *ConfigLoader) ChipletHostDmaBandwidth() int64 {
	return globalChipletConfig.hostDmaBandwidth
}

func (this *ConfigLoader) ChipletHostDmaUseRamulator() bool {
	return globalChipletConfig.hostDmaUseRamulator
}

func (this *ConfigLoader) ChipletHostDmaRamulatorConfig() string {
	return globalChipletConfig.hostDmaRamulatorConfig
}

func (this *ConfigLoader) ChipletNocUseBooksim() bool {
	return globalChipletConfig.nocUseBooksim
}

func (this *ConfigLoader) ChipletNocBooksimConfig() string {
	return globalChipletConfig.nocBooksimConfig
}

func (this *ConfigLoader) ChipletNocBooksimBinary() string {
	return globalChipletConfig.nocBooksimBinary
}

func (this *ConfigLoader) ChipletNocBooksimTimeoutMs() int {
	return globalChipletConfig.nocBooksimTimeoutMs
}

func (this *ConfigLoader) ChipletKvCacheBytes() int64 {
	return globalChipletConfig.kvCacheBytes
}

func (this *ConfigLoader) ChipletDigitalActivationBuffer() int64 {
	return globalChipletConfig.digitalActivationBuffer
}

func (this *ConfigLoader) ChipletDigitalScratchBuffer() int64 {
	return globalChipletConfig.digitalScratchBuffer
}

func (this *ConfigLoader) ChipletRramInputBuffer() int64 {
	return globalChipletConfig.rramInputBuffer
}

func (this *ConfigLoader) ChipletRramOutputBuffer() int64 {
	return globalChipletConfig.rramOutputBuffer
}

func (this *ConfigLoader) ChipletHostLimitResources() bool {
	return globalChipletConfig.hostLimitResources
}

func (this *ConfigLoader) ChipletHostStreamTotalBatches() int {
	return globalChipletConfig.hostStreamTotalBatches
}

func (this *ConfigLoader) ChipletHostStreamLowWatermark() int {
	return globalChipletConfig.hostStreamLowWatermark
}

func (this *ConfigLoader) ChipletHostStreamHighWatermark() int {
	return globalChipletConfig.hostStreamHighWatermark
}

func resolveRamulatorConfigPath(configPath, rootDir string) string {
	return resolveConfigPath(configPath, rootDir)
}

func resolveConfigPath(configPath, rootDir string) string {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return ""
	}

	if filepath.IsAbs(configPath) {
		if resolved, err := filepath.Abs(configPath); err == nil {
			return resolved
		}
		return configPath
	}

	candidates := make([]string, 0, 8)

	if root := strings.TrimSpace(rootDir); root != "" {
		base := filepath.Clean(root)
		for {
			candidates = append(candidates, filepath.Join(base, configPath))
			parent := filepath.Dir(base)
			if parent == base {
				break
			}
			base = parent
		}
	}

	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, configPath))
	}

	candidates = append(candidates, configPath)

	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		cleaned := filepath.Clean(candidate)
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		if info, err := os.Stat(cleaned); err == nil && !info.IsDir() {
			if abs, err := filepath.Abs(cleaned); err == nil {
				return abs
			}
			return cleaned
		}
	}

	if abs, err := filepath.Abs(configPath); err == nil {
		return abs
	}
	return configPath
}

func resolveExecutablePath(pathValue, rootDir string) string {
	pathValue = strings.TrimSpace(pathValue)
	if pathValue == "" {
		return ""
	}
	if filepath.IsAbs(pathValue) {
		if resolved, err := filepath.Abs(pathValue); err == nil {
			return resolved
		}
		return pathValue
	}

	base := strings.TrimSpace(rootDir)
	if base == "" {
		if cwd, err := os.Getwd(); err == nil {
			base = cwd
		}
	}
	if base == "" {
		return pathValue
	}
	if resolved, err := filepath.Abs(filepath.Join(base, pathValue)); err == nil {
		return resolved
	}
	return filepath.Join(base, pathValue)
}
