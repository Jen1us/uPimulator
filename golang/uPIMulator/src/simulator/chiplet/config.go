package chiplet

import "uPIMulator/src/misc"

// Config bundles runtime parameters required to construct the chiplet platform.
type Config struct {
	NumDigitalChiplets      int
	NumRramChiplets         int
	DigitalPesPerChiplet    int
	DigitalPeRows           int
	DigitalPeCols           int
	DigitalSpusPerChiplet   int
	DigitalClockMhz         int
	RramTilesPerDim         int
	RramSasPerTileDim       int
	RramSaRows              int
	RramSaCols              int
	RramCellBits            int
	RramDacBits             int
	RramAdcBits             int
	RramClockMhz            int
	InterconnectClockMhz    int
	TransferBandwidthDr     int64
	TransferBandwidthRd     int64
	HostDmaBandwidth        int64
	HostDmaUseRamulator     bool
	HostDmaRamulatorConfig  string
	NocUseBooksim           bool
	NocBooksimConfig        string
	NocBooksimBinary        string
	NocBooksimTimeoutMs     int
	KvCacheBytes            int64
	DigitalActivationBuffer int64
	DigitalScratchBuffer    int64
	RramInputBuffer         int64
	RramOutputBuffer        int64
	HostLimitResources      bool
	HostStreamTotalBatches  int
	HostStreamLowWatermark  int
	HostStreamHighWatermark int
}

// LoadConfig pulls chiplet-specific parameters from the shared ConfigLoader.
func LoadConfig(loader *misc.ConfigLoader) *Config {
	config := new(Config)

	config.NumDigitalChiplets = loader.ChipletNumDigitalChiplets()
	config.NumRramChiplets = loader.ChipletNumRramChiplets()
	config.DigitalPesPerChiplet = loader.ChipletDigitalPesPerChiplet()
	config.DigitalPeRows = loader.ChipletDigitalPeRows()
	config.DigitalPeCols = loader.ChipletDigitalPeCols()
	config.DigitalSpusPerChiplet = loader.ChipletDigitalSpusPerChiplet()
	config.DigitalClockMhz = loader.ChipletDigitalClockMhz()
	config.RramTilesPerDim = loader.ChipletRramTilesPerDim()
	config.RramSasPerTileDim = loader.ChipletRramSasPerTileDim()
	config.RramSaRows = loader.ChipletRramSaRows()
	config.RramSaCols = loader.ChipletRramSaCols()
	config.RramCellBits = loader.ChipletRramCellBits()
	config.RramDacBits = loader.ChipletRramDacBits()
	config.RramAdcBits = loader.ChipletRramAdcBits()
	config.RramClockMhz = loader.ChipletRramClockMhz()
	config.InterconnectClockMhz = loader.ChipletInterconnectClockMhz()
	config.TransferBandwidthDr = loader.ChipletTransferBandwidthDr()
	config.TransferBandwidthRd = loader.ChipletTransferBandwidthRd()
	config.HostDmaBandwidth = loader.ChipletHostDmaBandwidth()
	config.HostDmaUseRamulator = loader.ChipletHostDmaUseRamulator()
	config.HostDmaRamulatorConfig = loader.ChipletHostDmaRamulatorConfig()
	config.NocUseBooksim = loader.ChipletNocUseBooksim()
	config.NocBooksimConfig = loader.ChipletNocBooksimConfig()
	config.NocBooksimBinary = loader.ChipletNocBooksimBinary()
	config.NocBooksimTimeoutMs = loader.ChipletNocBooksimTimeoutMs()
	config.KvCacheBytes = loader.ChipletKvCacheBytes()
	config.DigitalActivationBuffer = loader.ChipletDigitalActivationBuffer()
	config.DigitalScratchBuffer = loader.ChipletDigitalScratchBuffer()
	config.RramInputBuffer = loader.ChipletRramInputBuffer()
	config.RramOutputBuffer = loader.ChipletRramOutputBuffer()
	config.HostLimitResources = loader.ChipletHostLimitResources()
	config.HostStreamTotalBatches = loader.ChipletHostStreamTotalBatches()
	config.HostStreamLowWatermark = loader.ChipletHostStreamLowWatermark()
	config.HostStreamHighWatermark = loader.ChipletHostStreamHighWatermark()

	return config
}
