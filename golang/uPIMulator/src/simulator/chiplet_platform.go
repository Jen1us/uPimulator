package simulator

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"uPIMulator/src/misc"
	"uPIMulator/src/simulator/chiplet"
	"uPIMulator/src/simulator/chiplet/digital"
	"uPIMulator/src/simulator/chiplet/rram"
	"uPIMulator/src/simulator/host"
	"uPIMulator/src/simulator/host/ramulator"
	"uPIMulator/src/simulator/host/tokenizer"
	"uPIMulator/src/simulator/noc/booksim"
)

// ChipletPlatform is a skeleton implementation that will eventually model the
// heterogeneous chiplet architecture. It currently wires up resource holders
// and a basic FIFO scheduler so that future work can incrementally add
// functionality.
type ChipletPlatform struct {
	config                        *chiplet.Config
	topology                      *chiplet.Topology
	digitalChiplets               []*digital.Chiplet
	rramChiplets                  []*rram.Chiplet
	orchestrator                  *chiplet.HostOrchestrator
	stager                        *chiplet.HostTaskStager
	scheduler                     chiplet.Scheduler
	executedDigitalTasks          int
	executedRramTasks             int
	executedHostTasks             int
	executedTransferTasks         int
	totalTransferBytes            int64
	totalTransferHops             int64
	totalTransferToRramBytes      int64
	totalTransferToDigitalBytes   int64
	totalTransferHostLoadBytes    int64
	totalTransferHostStoreBytes   int64
	transferThrottleEventsTotal   int64
	transferThrottleCyclesTotal   int64
	totalDigitalLoadBytesRuntime  int64
	totalDigitalStoreBytesRuntime int64
	totalDigitalCompleted         int
	transferThrottleUntil         int
	transferThrottleEvents        int
	hostDmaController             *host.DMAController
	booksimClient                 *booksim.Client
	digitalBytesLoaded            int64
	digitalBytesStored            int64
	digitalScalarOps              int64
	digitalVectorOps              int64
	kvCache                       *host.KVCache
	kvCacheLoads                  int64
	kvCacheStores                 int64
	kvCacheHits                   int64
	kvCacheMisses                 int64
	kvCacheLoadBytes              int64
	kvCacheStoreBytes             int64
	kvCacheHitBytes               int64
	kvCacheMissBytes              int64
	kvCacheEvictedBytes           int64
	kvCachePeakBytes              int64
	digitalClockMhz               int
	rramClockMhz                  int
	interconnectClockMhz          int
	clockBaseMhz                  int
	digitalPhase                  int
	rramPhase                     int
	interconnectPhase             int
	binDirpath                    string
	statFactory                   *misc.StatFactory
	currentCycle                  int
	maxWaitCycles                 int
	maxDigitalThroughput          int
	maxRramThroughput             int
	maxTransferThroughput         int
	cycleDigitalExec              int
	cycleRramExec                 int
	cycleTransferExec             int
	cycleTransferBytes            int64
	cycleTransferHops             int
	cycleHostDmaLoadBytes         int64
	cycleHostDmaStoreBytes        int64
	cycleKvHits                   int
	cycleKvMisses                 int
	cycleKvLoadBytes              int64
	cycleKvStoreBytes             int64
	cycleThrottleEvents           int
	cycleDigitalLoadBytes         int64
	cycleDigitalStoreBytes        int64
	cycleDigitalPeActive          int
	cycleDigitalSpuActive         int
	cycleDigitalVpuActive         int
	cycleDigitalCompleted         int
	digitalDeferrals              []int
	digitalSaturation             []int
	rramDeferrals                 []int
	rramSaturation                []int
	cycleLog                      []string
	resultLog                     []string
	digitalDomainCycles           int
	rramDomainCycles              int
	interconnectDomainCycles      int
	lastDigitalTicks              int
	lastRramTicks                 int
	lastInterconnectTicks         int
	hostDmaLoadBytesTotal         int64
	hostDmaStoreBytesTotal        int64

	transferAdaptiveCycles int
	tokenizer              tokenizer.Tokenizer
	progressInterval       int
	nextProgressCycle      int
	statsFlushInterval     int
	nextStatsFlushCycle    int
	rramInputBuffered      []int64
	rramProcessingBytes    []int64
	rramOutputBuffered     []int64
	gatingQueues           map[gatingKey][]*moeGatingSnapshot
	moeEventMetrics        map[int]*moeEventMetrics
	moeEventsTotal         int64
	moeTokensTotal         int64
	moeExpertsTotal        int64
	moeLatencyTotal        int64
	moeLatencySamples      int64
	moeLatencyMax          int
	moeSnapshotHits        int64
	moeSnapshotMisses      int64
	moeFallbackEvents      int64
	moeSessionsCompleted   int64
	moeSummaryAppended     bool
}

type gatingKey struct {
	digitalID int
	bufferID  int
}

type moeGatingSnapshot struct {
	commandID       int32
	issuedCycle     int
	tokens          int
	features        int
	topK            int
	candidate       []int
	selected        []int
	activationBytes int
	weightBytes     int
	outputBytes     int
	metadata        map[string]interface{}
}

type moeEventMetrics struct {
	startCycle int
	tokens     int
	experts    int
	fallback   bool
}

func (this *ChipletPlatform) Init(command_line_parser *misc.CommandLineParser) {
	config_loader := new(misc.ConfigLoader)
	config_loader.Init()

	config := chiplet.LoadConfig(config_loader)
	topology := chiplet.BuildTopology(config)
	binDirpath := command_line_parser.StringParameter("bin_dirpath")

	digitalChiplets := make([]*digital.Chiplet, 0, topology.Digital.NumChiplets)
	digitalParams := digital.DefaultParameters()
	if config.DigitalClockMhz > 0 {
		digitalParams.ClockMHz = config.DigitalClockMhz
	}
	if config.DigitalActivationBuffer > 0 {
		digitalParams.Buffer.ActivationBytes = config.DigitalActivationBuffer
	}
	if config.DigitalScratchBuffer > 0 {
		digitalParams.Buffer.ScratchBytes = config.DigitalScratchBuffer
	}
	if config.TransferBandwidthDr > 0 {
		digitalParams.Interconnect.BytesPerCycle = config.TransferBandwidthDr
	}
	if config.TransferBandwidthRd > 0 && config.TransferBandwidthRd < digitalParams.Interconnect.BytesPerCycle {
		digitalParams.Interconnect.BytesPerCycle = config.TransferBandwidthRd
	}
	for i := 0; i < topology.Digital.NumChiplets; i++ {
		digitalChiplets = append(digitalChiplets, digital.NewChiplet(
			i,
			topology.Digital.PesPerChiplet,
			topology.Digital.PeRows,
			topology.Digital.PeCols,
			topology.Digital.SpusPerChiplet,
			config.DigitalActivationBuffer,
			config.DigitalScratchBuffer,
			digitalParams,
		))
	}

	rramChiplets := make([]*rram.Chiplet, 0, topology.Rram.NumChiplets)
	rramParams := rram.DefaultParameters()
	if config.RramClockMhz > 0 {
		rramParams.ClockMHz = config.RramClockMhz
	}
	for i := 0; i < topology.Rram.NumChiplets; i++ {
		rramChiplets = append(rramChiplets, rram.NewChiplet(
			i,
			topology.Rram.TilesPerDim,
			topology.Rram.SasPerTileDim,
			topology.Rram.SaRows,
			topology.Rram.SaCols,
			topology.Rram.CellBits,
			topology.Rram.DacBits,
			topology.Rram.AdcBits,
			config.RramInputBuffer,
			config.RramOutputBuffer,
			rramParams,
		))
	}

	stager := new(chiplet.HostTaskStager)
	stager.Init()

	orchestrator := new(chiplet.HostOrchestrator)
	commandFile := ""
	if binDirpath != "" {
		commandFile = filepath.Join(binDirpath, "chiplet_commands.json")
	}
	orchestrator.Init(config, topology, commandFile)

	scheduler := new(chiplet.BasicScheduler)
	statFactory := new(misc.StatFactory)
	statFactory.Init("ChipletPlatform")

	digitalClock := config.DigitalClockMhz
	if digitalClock <= 0 {
		digitalClock = 1
	}
	rramClock := config.RramClockMhz
	if rramClock <= 0 {
		rramClock = digitalClock
	}
	interconnectClock := config.InterconnectClockMhz
	if interconnectClock <= 0 {
		interconnectClock = digitalClock
	}
	clockBase := digitalClock
	if rramClock > clockBase {
		clockBase = rramClock
	}
	if interconnectClock > clockBase {
		clockBase = interconnectClock
	}

	this.config = config
	this.topology = topology
	this.digitalChiplets = digitalChiplets
	this.rramChiplets = rramChiplets
	this.orchestrator = orchestrator
	this.stager = stager
	this.scheduler = scheduler
	this.executedDigitalTasks = 0
	this.executedRramTasks = 0
	this.binDirpath = binDirpath
	this.statFactory = statFactory
	var ramulatorClient *ramulator.Client
	if config.HostDmaUseRamulator {
		if config.HostDmaRamulatorConfig == "" {
			fmt.Println("[chiplet] warning: Ramulator enabled but config path empty; falling back to bandwidth model")
		} else {
			client, err := ramulator.NewClient(config.HostDmaRamulatorConfig)
			if err != nil {
				fmt.Printf("[chiplet] warning: Ramulator client init failed: %v (fallback to bandwidth model)\n", err)
			} else {
				ramulatorClient = client
			}
		}
	}
	this.hostDmaController = host.NewDMAController(config.HostDmaBandwidth, ramulatorClient)
	var booksimClient *booksim.Client
	if config.NocUseBooksim {
		if strings.TrimSpace(config.NocBooksimConfig) == "" {
			fmt.Println("[chiplet] warning: BookSim enabled but config path empty; falling back to bandwidth model")
		} else {
			timeout := time.Duration(config.NocBooksimTimeoutMs) * time.Millisecond
			if timeout < 0 {
				timeout = 0
			}
			client, err := booksim.NewClient(config.NocBooksimBinary, config.NocBooksimConfig, timeout)
			if err != nil {
				fmt.Printf("[chiplet] warning: BookSim client init failed: %v (fallback to bandwidth model)\n", err)
			} else {
				booksimClient = client
			}
		}
	}
	this.booksimClient = booksimClient
	orchestrator.SetTransferLatencyEstimator(this.buildTransferLatencyEstimator())
	this.kvCache = host.NewKVCache(config.KvCacheBytes)
	this.currentCycle = 0
	this.maxWaitCycles = 0
	this.maxDigitalThroughput = 0
	this.maxRramThroughput = 0
	this.maxTransferThroughput = 0
	this.cycleDigitalExec = 0
	this.cycleDigitalCompleted = 0
	this.cycleDigitalLoadBytes = 0
	this.cycleDigitalStoreBytes = 0
	this.cycleDigitalPeActive = 0
	this.cycleDigitalSpuActive = 0
	this.cycleDigitalVpuActive = 0
	this.cycleRramExec = 0
	this.cycleTransferExec = 0
	this.cycleTransferBytes = 0
	this.executedTransferTasks = 0
	this.totalTransferBytes = 0
	this.totalTransferHops = 0
	this.totalTransferToRramBytes = 0
	this.totalTransferToDigitalBytes = 0
	this.totalTransferHostLoadBytes = 0
	this.totalTransferHostStoreBytes = 0
	this.transferThrottleEventsTotal = 0
	this.transferThrottleCyclesTotal = 0
	this.totalDigitalLoadBytesRuntime = 0
	this.totalDigitalStoreBytesRuntime = 0
	this.totalDigitalCompleted = 0
	this.digitalBytesLoaded = 0
	this.digitalBytesStored = 0
	this.digitalScalarOps = 0
	this.digitalVectorOps = 0
	this.digitalClockMhz = digitalClock
	this.rramClockMhz = rramClock
	this.interconnectClockMhz = interconnectClock
	this.clockBaseMhz = clockBase
	this.digitalPhase = 0
	this.rramPhase = 0
	this.interconnectPhase = 0
	this.digitalDeferrals = make([]int, len(digitalChiplets))
	this.digitalSaturation = make([]int, len(digitalChiplets))
	this.rramDeferrals = make([]int, len(rramChiplets))
	this.rramSaturation = make([]int, len(rramChiplets))
	this.rramInputBuffered = make([]int64, len(rramChiplets))
	this.rramProcessingBytes = make([]int64, len(rramChiplets))
	this.rramOutputBuffered = make([]int64, len(rramChiplets))
	this.gatingQueues = make(map[gatingKey][]*moeGatingSnapshot)
	this.moeEventMetrics = make(map[int]*moeEventMetrics)
	this.cycleLog = []string{"cycle,digital_exec,digital_completed,rram_exec,transfer_exec,transfer_bytes,transfer_hops,host_dma_load_bytes,host_dma_store_bytes,kv_hits,kv_misses,kv_load_bytes,kv_store_bytes,digital_load_bytes,digital_store_bytes,digital_pe_active,digital_spu_active,digital_vpu_active,throttle_until,throttle_events,deferrals,avg_wait,digital_util,rram_util,digital_ticks,rram_ticks,interconnect_ticks,host_tasks,outstanding_digital,outstanding_rram,outstanding_transfer,outstanding_dma,transfer_to_rram_bytes,transfer_to_digital_bytes,transfer_host_load_bytes,transfer_host_store_bytes,transfer_throttle_events_total,transfer_throttle_cycles_total"}
	this.resultLog = []string{"cycle,chiplet_id,raw_om,final,reference,scale,zero_point,moe_events_total,moe_avg_latency,moe_latency_max,moe_snapshot_hit_rate,moe_fallback_rate"}
	this.transferAdaptiveCycles = 0
	this.tokenizer = tokenizer.NewStaticTokenizer(nil)

	progressInterval := int(command_line_parser.IntParameter("chiplet_progress_interval"))
	if progressInterval < 0 {
		progressInterval = 0
	}
	this.progressInterval = progressInterval
	if progressInterval > 0 {
		this.nextProgressCycle = progressInterval
		fmt.Printf("[chiplet] 初始化完成，将每 %d 个周期输出一次进度。\n", progressInterval)
	} else {
		fmt.Println("[chiplet] 初始化完成，进度打印处于关闭状态。")
	}

	statsFlushInterval := int(command_line_parser.IntParameter("chiplet_stats_flush_interval"))
	if statsFlushInterval < 0 {
		statsFlushInterval = 0
	}
	this.statsFlushInterval = statsFlushInterval
	if statsFlushInterval > 0 {
		this.nextStatsFlushCycle = statsFlushInterval
		fmt.Printf("[chiplet] 统计快照将每 %d 个周期刷新一次。\n", statsFlushInterval)
	} else {
		fmt.Println("[chiplet] 统计快照将仅在仿真结束时写入。")
	}

	if this.scheduler != nil {
		this.scheduler.Init(config, topology, this)
	}
}

func (this *ChipletPlatform) Fini() {
	if this.scheduler != nil {
		this.scheduler.Fini()
	}

	if this.stager != nil {
		this.stager.Fini()
	}

	if this.orchestrator != nil {
		this.orchestrator.Fini()
	}

	if this.booksimClient != nil {
		_ = this.booksimClient.Close()
		this.booksimClient = nil
	}
}

// SetTokenizer allows the host runtime to replace the default tokenizer.
func (this *ChipletPlatform) SetTokenizer(tok tokenizer.Tokenizer) {
	if tok == nil {
		return
	}
	this.tokenizer = tok
}

func (this *ChipletPlatform) IsFinished() bool {
	if this.scheduler == nil {
		return true
	}

	if !this.scheduler.IsIdle() {
		return false
	}

	if this.stager != nil && this.stager.HasPending() {
		return false
	}

	if this.orchestrator != nil && this.orchestrator.HasPendingWork() {
		return false
	}

	return true
}

func (this *ChipletPlatform) Cycle() {
	if this.scheduler == nil {
		return
	}

	digitalTicks := this.advanceDomainTicks(this.digitalClockMhz, &this.digitalPhase)
	rramTicks := this.advanceDomainTicks(this.rramClockMhz, &this.rramPhase)
	interconnectTicks := this.advanceDomainTicks(this.interconnectClockMhz, &this.interconnectPhase)
	if digitalTicks == 0 && rramTicks == 0 && interconnectTicks == 0 {
		// Ensure progress even if frequencies were misconfigured.
		digitalTicks = 1
	}

	this.currentCycle++
	this.cycleDigitalExec = 0
	this.cycleDigitalCompleted = 0
	this.cycleDigitalLoadBytes = 0
	this.cycleDigitalStoreBytes = 0
	this.cycleDigitalPeActive = 0
	this.cycleDigitalSpuActive = 0
	this.cycleDigitalVpuActive = 0
	this.cycleRramExec = 0
	this.cycleTransferExec = 0
	this.cycleTransferBytes = 0
	this.cycleTransferHops = 0
	this.cycleHostDmaLoadBytes = 0
	this.cycleHostDmaStoreBytes = 0
	this.cycleKvHits = 0
	this.cycleKvMisses = 0
	this.cycleKvLoadBytes = 0
	this.cycleKvStoreBytes = 0
	this.cycleThrottleEvents = 0
	cycleDeferrals := 0

	if this.statFactory != nil {
		this.statFactory.Increment("cycles", 1)
	}

	for i := 0; i < digitalTicks; i++ {
		cycleDeferrals += this.runDigitalTick()
	}

	for i := 0; i < rramTicks; i++ {
		this.runRramTick()
	}

	throttleActive := this.transferThrottleUntil > 0
	for i := 0; i < interconnectTicks; i++ {
		this.runInterconnectTick()
	}

	if throttleActive || this.transferThrottleUntil > 0 {
		this.transferThrottleCyclesTotal++
		if this.statFactory != nil {
			this.statFactory.Increment("transfer_throttle_cycles_total", 1)
		}
	}

	if digitalTicks > 0 {
		for _, chip := range this.digitalChiplets {
			if chip != nil {
				chip.AccumulateStaticEnergy(digitalTicks)
			}
		}
	}

	if this.cycleDigitalExec > this.maxDigitalThroughput {
		this.maxDigitalThroughput = this.cycleDigitalExec
	}

	if this.cycleRramExec > this.maxRramThroughput {
		this.maxRramThroughput = this.cycleRramExec
	}

	if this.cycleTransferExec > this.maxTransferThroughput {
		this.maxTransferThroughput = this.cycleTransferExec
	}

	this.lastDigitalTicks = digitalTicks
	this.lastRramTicks = rramTicks
	this.lastInterconnectTicks = interconnectTicks
	this.digitalDomainCycles += digitalTicks
	this.rramDomainCycles += rramTicks
	this.interconnectDomainCycles += interconnectTicks
	if this.statFactory != nil {
		if digitalTicks > 0 {
			this.statFactory.Increment("digital_domain_cycles", int64(digitalTicks))
		}
		if rramTicks > 0 {
			this.statFactory.Increment("rram_domain_cycles", int64(rramTicks))
		}
		if interconnectTicks > 0 {
			this.statFactory.Increment("interconnect_domain_cycles", int64(interconnectTicks))
		}
	}

	this.logCycleMetrics(cycleDeferrals)
	this.emitProgress(cycleDeferrals)
	this.maybeFlushStats()
}

func (this *ChipletPlatform) runDigitalTick() int {
	deferrals := 0

	if this.orchestrator != nil {
		if tasks := this.orchestrator.Advance(); tasks != nil {
			for _, task := range tasks {
				this.SubmitTask(task)
			}
		}
	}

	if this.stager != nil {
		deferred := make([]*chiplet.Task, 0)

		for this.stager.HasPending() {
			task, ok := this.stager.Pop()
			if !ok || task == nil {
				continue
			}

			if this.transferThrottleUntil > 0 && task.Target == chiplet.TaskTargetTransfer {
				deferred = append(deferred, task)
				if this.statFactory != nil {
					this.statFactory.Increment("transfer_throttle_deferred", 1)
				}
				continue
			}

			if this.isTargetBusy(task) {
				deferred = append(deferred, task)
				if this.statFactory != nil {
					this.statFactory.Increment("task_deferrals", 1)
				}
				deferrals++
				this.recordDeferral(task)
				continue
			}

			this.scheduler.EnqueueTask(task)
		}

		for _, task := range deferred {
			this.stager.Enqueue(task)
		}
	}

	this.scheduler.Tick()

	for _, chiplet := range this.digitalChiplets {
		chiplet.Tick()
		this.cycleDigitalLoadBytes += chiplet.CycleLoadBytes
		this.cycleDigitalStoreBytes += chiplet.CycleStoreBytes
		this.cycleDigitalPeActive += chiplet.CyclePeActive
		this.cycleDigitalSpuActive += chiplet.CycleSpuActive
		this.cycleDigitalVpuActive += chiplet.CycleVpuActive
		this.cycleDigitalCompleted += chiplet.CycleTasksCompleted
		this.totalDigitalLoadBytesRuntime += chiplet.CycleLoadBytes
		this.totalDigitalStoreBytesRuntime += chiplet.CycleStoreBytes
		this.totalDigitalCompleted += chiplet.CycleTasksCompleted
	}

	return deferrals
}

func (this *ChipletPlatform) runRramTick() {
	for _, chiplet := range this.rramChiplets {
		chiplet.Tick()
		if summary, ok := chiplet.ConsumeLastResult(); ok {
			this.recordRramResult(chiplet.ID, summary)
		}
	}
}

func (this *ChipletPlatform) runInterconnectTick() {
	if this.transferThrottleUntil > 0 {
		this.transferThrottleUntil--
	}
}

func (this *ChipletPlatform) advanceDomainTicks(freq int, phase *int) int {
	if freq <= 0 {
		return 0
	}
	if this.clockBaseMhz <= 0 {
		return 1
	}
	*phase += freq
	ticks := 0
	for *phase >= this.clockBaseMhz {
		*phase -= this.clockBaseMhz
		ticks++
	}
	return ticks
}

func (this *ChipletPlatform) emitProgress(cycleDeferrals int) {
	if this.progressInterval <= 0 {
		return
	}

	if this.currentCycle < this.nextProgressCycle {
		return
	}

	this.nextProgressCycle += this.progressInterval

	digitalPending := 0
	for _, chiplet := range this.digitalChiplets {
		if chiplet != nil {
			digitalPending += chiplet.PendingTasks
		}
	}

	rramPending := 0
	for _, chiplet := range this.rramChiplets {
		if chiplet != nil {
			rramPending += chiplet.PendingTasks
		}
	}

	stagerState := "空"
	if this.stager != nil && this.stager.HasPending() {
		stagerState = "等待"
	}

	orchestratorState := "空闲"
	if this.orchestrator != nil && this.orchestrator.HasPendingWork() {
		orchestratorState = "活跃"
	}

	fmt.Printf(
		"[chiplet] 周期=%d | 数字任务 累计=%d 待处理=%d | RRAM任务 累计=%d 待处理=%d | 传输累计=%d | 本周期调度 数字=%d RRAM=%d 传输=%d | 本周期传输字节=%d | 回压=%d | Stager=%s | Orchestrator=%s\n",
		this.currentCycle,
		this.executedDigitalTasks,
		digitalPending,
		this.executedRramTasks,
		rramPending,
		this.executedTransferTasks,
		this.cycleDigitalExec,
		this.cycleRramExec,
		this.cycleTransferExec,
		this.cycleTransferBytes,
		cycleDeferrals,
		stagerState,
		orchestratorState,
	)
}

func (this *ChipletPlatform) maybeFlushStats() {
	if this.statsFlushInterval <= 0 {
		return
	}
	if this.currentCycle < this.nextStatsFlushCycle {
		return
	}
	this.nextStatsFlushCycle += this.statsFlushInterval
	this.writeStatsFiles(false)
}

func (this *ChipletPlatform) recordRramResult(chipletID int, summary rram.ResultSummary) {
	if !summary.Valid {
		return
	}
	line := fmt.Sprintf("%d,%d,%d,%.6f,%.6f,%.6f,%d,0,0,0,0,0",
		this.currentCycle,
		chipletID,
		summary.RawOM,
		summary.Final,
		summary.Reference,
		summary.Scale,
		summary.ZeroPt,
	)
	this.resultLog = append(this.resultLog, line)
	if this.statFactory != nil {
		this.statFactory.Increment("rram_results_recorded", 1)
	}
}

func (this *ChipletPlatform) Dump() {
	this.writeStatsFiles(true)
}

func (this *ChipletPlatform) writeStatsFiles(final bool) {
	if this.binDirpath == "" {
		return
	}

	file_dumper := new(misc.FileDumper)
	file_dumper.Init(filepath.Join(this.binDirpath, "chiplet_log.txt"))

	lines := make([]string, 0)

	if this.statFactory != nil {
		this.statFactory.Increment("digital_tasks_total", 0)
		this.statFactory.Increment("rram_tasks_total", 0)
		lines = append(lines, this.statFactory.ToLines()...)
	}

	lines = append(lines,
		fmt.Sprintf("ChipletPlatform_digital_tasks_total: %d", this.executedDigitalTasks),
		fmt.Sprintf("ChipletPlatform_rram_tasks_total: %d", this.executedRramTasks),
		fmt.Sprintf("ChipletPlatform_transfer_tasks_total: %d", this.executedTransferTasks),
		fmt.Sprintf("ChipletPlatform_transfer_bytes_total: %d", this.totalTransferBytes),
		fmt.Sprintf("ChipletPlatform_transfer_hops_total: %d", this.totalTransferHops),
		fmt.Sprintf("ChipletPlatform_transfer_to_rram_bytes_total: %d", this.totalTransferToRramBytes),
		fmt.Sprintf("ChipletPlatform_transfer_to_digital_bytes_total: %d", this.totalTransferToDigitalBytes),
		fmt.Sprintf("ChipletPlatform_transfer_host_load_bytes_total: %d", this.totalTransferHostLoadBytes),
		fmt.Sprintf("ChipletPlatform_transfer_host_store_bytes_total: %d", this.totalTransferHostStoreBytes),
		fmt.Sprintf("ChipletPlatform_transfer_throttle_events_total: %d", this.transferThrottleEventsTotal),
		fmt.Sprintf("ChipletPlatform_transfer_throttle_cycles_total: %d", this.transferThrottleCyclesTotal),
		fmt.Sprintf("ChipletPlatform_host_dma_load_bytes_total: %d", this.hostDmaLoadBytesTotal),
		fmt.Sprintf("ChipletPlatform_host_dma_store_bytes_total: %d", this.hostDmaStoreBytesTotal),
		fmt.Sprintf("ChipletPlatform_kv_cache_loads_total: %d", this.kvCacheLoads),
		fmt.Sprintf("ChipletPlatform_kv_cache_stores_total: %d", this.kvCacheStores),
		fmt.Sprintf("ChipletPlatform_kv_cache_hits_total: %d", this.kvCacheHits),
		fmt.Sprintf("ChipletPlatform_kv_cache_misses_total: %d", this.kvCacheMisses),
		fmt.Sprintf("ChipletPlatform_kv_cache_load_bytes_total: %d", this.kvCacheLoadBytes),
		fmt.Sprintf("ChipletPlatform_kv_cache_store_bytes_total: %d", this.kvCacheStoreBytes),
		fmt.Sprintf("ChipletPlatform_kv_cache_hit_bytes_total: %d", this.kvCacheHitBytes),
		fmt.Sprintf("ChipletPlatform_kv_cache_miss_bytes_total: %d", this.kvCacheMissBytes),
		fmt.Sprintf("ChipletPlatform_kv_cache_evicted_bytes_total: %d", this.kvCacheEvictedBytes),
		fmt.Sprintf("ChipletPlatform_kv_cache_resident_peak_bytes: %d", this.kvCachePeakBytes),
		fmt.Sprintf("ChipletPlatform_digital_load_bytes_runtime_total: %d", this.totalDigitalLoadBytesRuntime),
		fmt.Sprintf("ChipletPlatform_digital_store_bytes_runtime_total: %d", this.totalDigitalStoreBytesRuntime),
		fmt.Sprintf("ChipletPlatform_digital_tasks_completed_total: %d", this.totalDigitalCompleted),
		fmt.Sprintf("ChipletPlatform_digital_load_bytes_total: %d", this.digitalBytesLoaded),
		fmt.Sprintf("ChipletPlatform_digital_store_bytes_total: %d", this.digitalBytesStored),
		fmt.Sprintf("ChipletPlatform_digital_scalar_ops_total: %d", this.digitalScalarOps),
		fmt.Sprintf("ChipletPlatform_digital_vector_ops_total: %d", this.digitalVectorOps),
		fmt.Sprintf("ChipletPlatform_digital_domain_cycles: %d", this.digitalDomainCycles),
		fmt.Sprintf("ChipletPlatform_rram_domain_cycles: %d", this.rramDomainCycles),
		fmt.Sprintf("ChipletPlatform_interconnect_domain_cycles: %d", this.interconnectDomainCycles),
		fmt.Sprintf("ChipletPlatform_digital_clock_mhz: %d", this.digitalClockMhz),
		fmt.Sprintf("ChipletPlatform_rram_clock_mhz: %d", this.rramClockMhz),
		fmt.Sprintf("ChipletPlatform_interconnect_clock_mhz: %d", this.interconnectClockMhz),
		fmt.Sprintf("ChipletPlatform_max_wait_cycles: %d", this.maxWaitCycles),
		fmt.Sprintf("ChipletPlatform_max_digital_throughput: %d", this.maxDigitalThroughput),
		fmt.Sprintf("ChipletPlatform_max_rram_throughput: %d", this.maxRramThroughput),
		fmt.Sprintf("ChipletPlatform_max_transfer_throughput: %d", this.maxTransferThroughput),
		fmt.Sprintf("ChipletPlatform_moe_events_total: %d", this.moeEventsTotal),
		fmt.Sprintf("ChipletPlatform_moe_tokens_total: %d", this.moeTokensTotal),
		fmt.Sprintf("ChipletPlatform_moe_experts_total: %d", this.moeExpertsTotal),
		fmt.Sprintf("ChipletPlatform_moe_snapshot_hits_total: %d", this.moeSnapshotHits),
		fmt.Sprintf("ChipletPlatform_moe_snapshot_misses_total: %d", this.moeSnapshotMisses),
		fmt.Sprintf("ChipletPlatform_moe_fallback_events_total: %d", this.moeFallbackEvents),
		fmt.Sprintf("ChipletPlatform_moe_sessions_completed_total: %d", this.moeSessionsCompleted),
		fmt.Sprintf("ChipletPlatform_moe_latency_samples: %d", this.moeLatencySamples),
		fmt.Sprintf("ChipletPlatform_moe_latency_total_cycles: %d", this.moeLatencyTotal),
		fmt.Sprintf("ChipletPlatform_moe_latency_max_cycles: %d", this.moeLatencyMax),
	)

	if this.statFactory != nil {
		waitSamples := this.statFactory.Value("task_wait_samples")
		waitTotal := this.statFactory.Value("task_wait_cycles_total")
		if waitSamples > 0 {
			average := float64(waitTotal) / float64(waitSamples)
			lines = append(lines, fmt.Sprintf("ChipletPlatform_avg_wait_cycles: %.2f", average))
		}
	}

	totalDigitalBusy := 0
	totalRramBusy := 0
	totalDigitalDeferrals := 0
	totalRramDeferrals := 0
	totalDigitalSaturation := 0
	totalRramSaturation := 0
	totalDigitalMacs := int64(0)
	totalSpuScalar := int64(0)
	totalSpuVector := int64(0)
	totalSpuSpecial := int64(0)
	totalSpuBusy := int64(0)
	totalPeEnergy := 0.0
	totalSpuEnergy := 0.0
	totalVpuEnergy := 0.0
	totalReduceEnergy := 0.0
	totalRramStageEnergy := 0.0
	totalRramExecuteEnergy := 0.0
	totalRramPostEnergy := 0.0
	totalRramWeightEnergy := 0.0
	totalWeightResident := int64(0)
	totalWeightPeak := int64(0)
	totalWeightLoads := int64(0)
	totalWeightHits := int64(0)
	totalRramPulses := int64(0)
	totalRramAdcSamples := int64(0)
	totalRramPreCycles := int64(0)
	totalRramPostCycles := int64(0)
	totalRramErrorSamples := int64(0)
	totalRramErrorAccum := 0.0
	maxRramError := 0.0
	lastRramError := 0.0

	for _, chiplet := range this.digitalChiplets {
		line := fmt.Sprintf("DigitalChiplet[%d]_executed_tasks: %d", chiplet.ID, chiplet.ExecutedTasks)
		lines = append(lines, line)
		line = fmt.Sprintf("DigitalChiplet[%d]_pending_cycles: %d", chiplet.ID, chiplet.PendingCycles)
		lines = append(lines, line)
		line = fmt.Sprintf("DigitalChiplet[%d]_busy_cycles: %d", chiplet.ID, chiplet.BusyCycles)
		lines = append(lines, line)
		line = fmt.Sprintf("DigitalChiplet[%d]_deferrals: %d", chiplet.ID, this.digitalDeferrals[chiplet.ID])
		lines = append(lines, line)
		line = fmt.Sprintf("DigitalChiplet[%d]_saturation: %d", chiplet.ID, this.digitalSaturation[chiplet.ID])
		lines = append(lines, line)
		line = fmt.Sprintf("DigitalChiplet[%d]_macs_total: %d", chiplet.ID, chiplet.TotalMacs)
		lines = append(lines, line)
		line = fmt.Sprintf("DigitalChiplet[%d]_spu_scalar_ops: %d", chiplet.ID, chiplet.SpuScalarOps)
		lines = append(lines, line)
		line = fmt.Sprintf("DigitalChiplet[%d]_spu_vector_ops: %d", chiplet.ID, chiplet.SpuVectorOps)
		lines = append(lines, line)
		line = fmt.Sprintf("DigitalChiplet[%d]_spu_special_ops: %d", chiplet.ID, chiplet.SpuSpecialOps)
		lines = append(lines, line)
		line = fmt.Sprintf("DigitalChiplet[%d]_spu_busy_cycles: %d", chiplet.ID, chiplet.SpuBusyCycles)
		lines = append(lines, line)
		lines = append(lines,
			fmt.Sprintf("DigitalChiplet[%d]_energy_pe_pj: %.6f", chiplet.ID, chiplet.PeEnergyPJ),
			fmt.Sprintf("DigitalChiplet[%d]_energy_spu_pj: %.6f", chiplet.ID, chiplet.SpuEnergyPJ),
			fmt.Sprintf("DigitalChiplet[%d]_energy_reduce_pj: %.6f", chiplet.ID, chiplet.ReduceEnergyPJ),
			fmt.Sprintf("DigitalChiplet[%d]_energy_vpu_pj: %.6f", chiplet.ID, chiplet.VpuEnergyPJ),
			fmt.Sprintf("DigitalChiplet[%d]_energy_dynamic_pj: %.6f", chiplet.ID, chiplet.DynamicEnergyPJ),
		)
		for idx, cycles := range chiplet.PeBusyCycles {
			lines = append(lines, fmt.Sprintf("DigitalChiplet[%d]_pe[%d]_busy_cycles: %d", chiplet.ID, idx, cycles))
		}
		for idx, cycles := range chiplet.SpuClusterBusy {
			lines = append(lines, fmt.Sprintf("DigitalChiplet[%d]_spu_cluster[%d]_busy_cycles: %d", chiplet.ID, idx, cycles))
		}
		for name, occ := range chiplet.BufferOccupancy {
			lines = append(lines, fmt.Sprintf("DigitalChiplet[%d]_buffer_%s: %d", chiplet.ID, name, occ))
			if peak := chiplet.BufferPeakUsage[name]; peak > 0 {
				lines = append(lines, fmt.Sprintf("DigitalChiplet[%d]_buffer_%s_peak: %d", chiplet.ID, name, peak))
			}
		}
		totalDigitalBusy += chiplet.BusyCycles
		totalDigitalDeferrals += this.digitalDeferrals[chiplet.ID]
		totalDigitalSaturation += this.digitalSaturation[chiplet.ID]
		totalDigitalMacs += chiplet.TotalMacs
		totalSpuScalar += chiplet.SpuScalarOps
		totalSpuVector += chiplet.SpuVectorOps
		totalSpuSpecial += chiplet.SpuSpecialOps
		totalSpuBusy += chiplet.SpuBusyCycles
		totalPeEnergy += chiplet.PeEnergyPJ
		totalSpuEnergy += chiplet.SpuEnergyPJ
		totalVpuEnergy += chiplet.VpuEnergyPJ
		totalReduceEnergy += chiplet.ReduceEnergyPJ
	}

	totalInputPeak := int64(0)
	totalOutputPeak := int64(0)

	for _, chiplet := range this.rramChiplets {
		line := fmt.Sprintf("RramChiplet[%d]_executed_tasks: %d", chiplet.ID, chiplet.ExecutedTasks)
		lines = append(lines, line)
		line = fmt.Sprintf("RramChiplet[%d]_pending_cycles: %d", chiplet.ID, chiplet.PendingCycles)
		lines = append(lines, line)
		line = fmt.Sprintf("RramChiplet[%d]_busy_cycles: %d", chiplet.ID, chiplet.BusyCycles)
		lines = append(lines, line)
		line = fmt.Sprintf("RramChiplet[%d]_deferrals: %d", chiplet.ID, this.rramDeferrals[chiplet.ID])
		lines = append(lines, line)
		line = fmt.Sprintf("RramChiplet[%d]_saturation: %d", chiplet.ID, this.rramSaturation[chiplet.ID])
		lines = append(lines, line)
		stats := chiplet.Stats()
		lines = append(lines, fmt.Sprintf("RramChiplet[%d]_cim_tasks: %d", chiplet.ID, stats.CimTasks))
		lines = append(lines, fmt.Sprintf("RramChiplet[%d]_pulse_count: %d", chiplet.ID, stats.PulseCountCim))
		lines = append(lines, fmt.Sprintf("RramChiplet[%d]_adc_samples: %d", chiplet.ID, stats.TotalAdcSamples))
		lines = append(lines, fmt.Sprintf("RramChiplet[%d]_preprocess_cycles: %d", chiplet.ID, stats.TotalPreprocessCycles))
		lines = append(lines, fmt.Sprintf("RramChiplet[%d]_postprocess_cycles: %d", chiplet.ID, stats.TotalPostprocessCycles))
		lines = append(lines,
			fmt.Sprintf("RramChiplet[%d]_stage_energy_pj: %.6f", chiplet.ID, chiplet.StageEnergyPJ),
			fmt.Sprintf("RramChiplet[%d]_execute_energy_pj: %.6f", chiplet.ID, chiplet.ExecuteEnergyPJ),
			fmt.Sprintf("RramChiplet[%d]_post_energy_pj: %.6f", chiplet.ID, chiplet.PostEnergyPJ),
			fmt.Sprintf("RramChiplet[%d]_weight_load_energy_pj: %.6f", chiplet.ID, chiplet.WeightLoadEnergyPJ),
			fmt.Sprintf("RramChiplet[%d]_dynamic_energy_pj: %.6f", chiplet.ID, chiplet.DynamicEnergyPJ),
			fmt.Sprintf("RramChiplet[%d]_static_energy_pj: %.6f", chiplet.ID, chiplet.StaticEnergyPJ),
		)
		lines = append(lines,
			fmt.Sprintf("RramChiplet[%d]_weights_resident_bytes: %d", chiplet.ID, chiplet.WeightBytesResident),
			fmt.Sprintf("RramChiplet[%d]_weights_peak_bytes: %d", chiplet.ID, chiplet.WeightBytesPeak),
			fmt.Sprintf("RramChiplet[%d]_weights_loads: %d", chiplet.ID, chiplet.WeightLoads),
			fmt.Sprintf("RramChiplet[%d]_weights_hits: %d", chiplet.ID, chiplet.WeightLoadHits),
		)
		if stats.ErrorSamples > 0 {
			avgError := stats.AccumulatedErrorAbs / float64(stats.ErrorSamples)
			lines = append(lines, fmt.Sprintf("RramChiplet[%d]_error_last: %.6f", chiplet.ID, stats.LastErrorAbs))
			lines = append(lines, fmt.Sprintf("RramChiplet[%d]_error_max: %.6f", chiplet.ID, stats.MaxErrorAbs))
			lines = append(lines, fmt.Sprintf("RramChiplet[%d]_error_avg: %.6f", chiplet.ID, avgError))
		}
		if stats.LastSummary.Valid {
			lines = append(lines, fmt.Sprintf("RramChiplet[%d]_result_final: %.6f", chiplet.ID, stats.LastSummary.Final))
			if stats.LastSummary.HasReference {
				lines = append(lines, fmt.Sprintf("RramChiplet[%d]_result_reference: %.6f", chiplet.ID, stats.LastSummary.Reference))
			}
		}
		lines = append(lines,
			fmt.Sprintf("RramChiplet[%d]_buffer_input: %d", chiplet.ID, chiplet.BufferUsage("input")),
			fmt.Sprintf("RramChiplet[%d]_buffer_output: %d", chiplet.ID, chiplet.BufferUsage("output")),
		)
		lines = append(lines,
			fmt.Sprintf("RramChiplet[%d]_buffer_input_peak: %d", chiplet.ID, chiplet.BufferPeak("input")),
			fmt.Sprintf("RramChiplet[%d]_buffer_output_peak: %d", chiplet.ID, chiplet.BufferPeak("output")),
		)
		lines = append(lines,
			fmt.Sprintf("RramChiplet[%d]_input_buffer_peak_bytes: %d", chiplet.ID, chiplet.InputBufferPeak),
			fmt.Sprintf("RramChiplet[%d]_output_buffer_peak_bytes: %d", chiplet.ID, chiplet.OutputBufferPeak),
		)
		totalRramBusy += chiplet.BusyCycles
		totalRramDeferrals += this.rramDeferrals[chiplet.ID]
		totalRramSaturation += this.rramSaturation[chiplet.ID]
		totalRramPulses += stats.PulseCountCim
		totalRramAdcSamples += stats.TotalAdcSamples
		totalRramPreCycles += stats.TotalPreprocessCycles
		totalRramPostCycles += stats.TotalPostprocessCycles
		totalRramErrorSamples += stats.ErrorSamples
		totalRramErrorAccum += stats.AccumulatedErrorAbs
		if stats.ErrorSamples > 0 {
			lastRramError = stats.LastErrorAbs
			if stats.MaxErrorAbs > maxRramError {
				maxRramError = stats.MaxErrorAbs
			}
		}
		totalRramStageEnergy += chiplet.StageEnergyPJ
		totalRramExecuteEnergy += chiplet.ExecuteEnergyPJ
		totalRramPostEnergy += chiplet.PostEnergyPJ
		totalRramWeightEnergy += chiplet.WeightLoadEnergyPJ
		totalWeightResident += chiplet.WeightBytesResident
		if chiplet.WeightBytesPeak > totalWeightPeak {
			totalWeightPeak = chiplet.WeightBytesPeak
		}
		totalWeightLoads += chiplet.WeightLoads
		totalWeightHits += chiplet.WeightLoadHits
		if chiplet.InputBufferPeak > totalInputPeak {
			totalInputPeak = chiplet.InputBufferPeak
		}
		if chiplet.OutputBufferPeak > totalOutputPeak {
			totalOutputPeak = chiplet.OutputBufferPeak
		}
	}

	if this.currentCycle > 0 {
		lines = append(lines,
			fmt.Sprintf("ChipletPlatform_avg_digital_throughput: %.4f", float64(this.executedDigitalTasks)/float64(this.currentCycle)),
			fmt.Sprintf("ChipletPlatform_avg_rram_throughput: %.4f", float64(this.executedRramTasks)/float64(this.currentCycle)),
			fmt.Sprintf("ChipletPlatform_avg_transfer_throughput: %.4f", float64(this.executedTransferTasks)/float64(this.currentCycle)),
			fmt.Sprintf("ChipletPlatform_avg_transfer_bandwidth_bytes_per_cycle: %.4f", float64(this.totalTransferBytes)/float64(this.currentCycle)),
			fmt.Sprintf("ChipletPlatform_total_digital_deferrals: %d", totalDigitalDeferrals),
			fmt.Sprintf("ChipletPlatform_total_rram_deferrals: %d", totalRramDeferrals),
			fmt.Sprintf("ChipletPlatform_total_digital_saturation: %d", totalDigitalSaturation),
			fmt.Sprintf("ChipletPlatform_total_rram_saturation: %d", totalRramSaturation),
			fmt.Sprintf("ChipletPlatform_digital_macs_total: %d", totalDigitalMacs),
			fmt.Sprintf("ChipletPlatform_spu_scalar_ops_total: %d", totalSpuScalar),
			fmt.Sprintf("ChipletPlatform_spu_vector_ops_total: %d", totalSpuVector),
			fmt.Sprintf("ChipletPlatform_spu_special_ops_total: %d", totalSpuSpecial),
			fmt.Sprintf("ChipletPlatform_spu_busy_cycles_total: %d", totalSpuBusy),
			fmt.Sprintf("ChipletPlatform_energy_pe_pj_total: %.6f", totalPeEnergy),
			fmt.Sprintf("ChipletPlatform_energy_spu_pj_total: %.6f", totalSpuEnergy),
			fmt.Sprintf("ChipletPlatform_energy_reduce_pj_total: %.6f", totalReduceEnergy),
			fmt.Sprintf("ChipletPlatform_energy_vpu_pj_total: %.6f", totalVpuEnergy),
			fmt.Sprintf("ChipletPlatform_energy_rram_stage_pj_total: %.6f", totalRramStageEnergy),
			fmt.Sprintf("ChipletPlatform_energy_rram_execute_pj_total: %.6f", totalRramExecuteEnergy),
			fmt.Sprintf("ChipletPlatform_energy_rram_post_pj_total: %.6f", totalRramPostEnergy),
			fmt.Sprintf("ChipletPlatform_energy_rram_weight_load_pj_total: %.6f", totalRramWeightEnergy),
			fmt.Sprintf("ChipletPlatform_rram_pulse_count_total: %d", totalRramPulses),
			fmt.Sprintf("ChipletPlatform_rram_adc_samples_total: %d", totalRramAdcSamples),
			fmt.Sprintf("ChipletPlatform_rram_preprocess_cycles_total: %d", totalRramPreCycles),
			fmt.Sprintf("ChipletPlatform_rram_postprocess_cycles_total: %d", totalRramPostCycles),
		)
		lines = append(lines,
			fmt.Sprintf("ChipletPlatform_rram_weight_resident_bytes_total: %d", totalWeightResident),
			fmt.Sprintf("ChipletPlatform_rram_weight_peak_bytes: %d", totalWeightPeak),
			fmt.Sprintf("ChipletPlatform_rram_weight_loads_total: %d", totalWeightLoads),
			fmt.Sprintf("ChipletPlatform_rram_weight_hits_total: %d", totalWeightHits),
			fmt.Sprintf("ChipletPlatform_rram_input_buffer_peak_bytes: %d", totalInputPeak),
			fmt.Sprintf("ChipletPlatform_rram_output_buffer_peak_bytes: %d", totalOutputPeak),
		)
		if totalRramErrorSamples > 0 {
			avgErr := totalRramErrorAccum / float64(totalRramErrorSamples)
			lines = append(lines,
				fmt.Sprintf("ChipletPlatform_rram_error_samples: %d", totalRramErrorSamples),
				fmt.Sprintf("ChipletPlatform_rram_error_last: %.6f", lastRramError),
				fmt.Sprintf("ChipletPlatform_rram_error_max: %.6f", maxRramError),
				fmt.Sprintf("ChipletPlatform_rram_error_avg: %.6f", avgErr),
			)
		}

		if len(this.digitalChiplets) > 0 {
			util := float64(totalDigitalBusy) / float64(len(this.digitalChiplets)*this.currentCycle)
			lines = append(lines, fmt.Sprintf("ChipletPlatform_digital_utilization: %.4f", util))
		}
		if len(this.rramChiplets) > 0 {
			util := float64(totalRramBusy) / float64(len(this.rramChiplets)*this.currentCycle)
			lines = append(lines, fmt.Sprintf("ChipletPlatform_rram_utilization: %.4f", util))
		}
		lines = append(lines, fmt.Sprintf("ChipletPlatform_host_tasks_total: %d", this.executedHostTasks))
		if this.orchestrator != nil {
			tracker := this.orchestrator.Outstanding()
			lines = append(lines,
				fmt.Sprintf("ChipletPlatform_outstanding_digital_bytes: %d", tracker.Digital),
				fmt.Sprintf("ChipletPlatform_outstanding_rram_bytes: %d", tracker.Rram),
				fmt.Sprintf("ChipletPlatform_outstanding_transfer_bytes: %d", tracker.Transfer),
				fmt.Sprintf("ChipletPlatform_outstanding_dma_bytes: %d", tracker.Dma),
			)
		}
	}

	file_dumper.WriteLines(lines)

	if len(this.cycleLog) > 1 {
		cycle_logger := new(misc.FileDumper)
		cycle_logger.Init(filepath.Join(this.binDirpath, "chiplet_cycle_log.csv"))
		cycle_logger.WriteLines(this.cycleLog)
	}

	if final {
		this.appendMoeSummaryRow()
	}
	if len(this.resultLog) > 1 {
		resultLogger := new(misc.FileDumper)
		resultLogger.Init(filepath.Join(this.binDirpath, "chiplet_results.csv"))
		resultLogger.WriteLines(this.resultLog)
	}
}

// SubmitTask enqueues a chiplet task for execution. Future host orchestration
// logic will call this to drive workload execution.
func (this *ChipletPlatform) SubmitTask(task *chiplet.Task) {
	if this.stager == nil {
		return
	}

	if task == nil {
		return
	}

	if task.EnqueueCycle == 0 {
		task.EnqueueCycle = this.currentCycle
	}

	this.stager.Enqueue(task)
}

// ExecuteTask satisfies chiplet.TaskExecutor and dispatches tasks to the
// appropriate subsystem.
func (this *ChipletPlatform) ExecuteTask(task *chiplet.Task) {
	if task == nil {
		return
	}

	waitCycles := this.currentCycle - task.EnqueueCycle
	if waitCycles < 0 {
		waitCycles = 0
	}

	if this.statFactory != nil {
		this.statFactory.Increment("task_wait_cycles_total", int64(waitCycles))
		this.statFactory.Increment("task_wait_samples", 1)
	}

	if waitCycles > this.maxWaitCycles {
		this.maxWaitCycles = waitCycles
	}

	if this.orchestrator != nil {
		this.orchestrator.NotifyBackpressure(waitCycles)
	}

	switch task.Target {
	case chiplet.TaskTargetDigital:
		this.handleDigitalTask(task)
		this.cycleDigitalExec++
	case chiplet.TaskTargetRram:
		this.handleRramTask(task)
		this.cycleRramExec++
	case chiplet.TaskTargetTransfer:
		this.handleTransferTask(task)
		this.cycleTransferExec++
	case chiplet.TaskTargetHost:
		this.handleHostTask(task)
		this.executedHostTasks++
		if this.statFactory != nil {
			this.statFactory.Increment("host_tasks_total", 1)
		}
	default:
		// Transfers and other task types are not yet modeled.
	}

	if this.orchestrator != nil {
		this.orchestrator.NotifyTaskCompletion(task.NodeID)
	}
}

func (this *ChipletPlatform) handleDigitalTask(task *chiplet.Task) {
	chipletID, ok := extractChipletID(task.Payload)
	if !ok || chipletID < 0 || chipletID >= len(this.digitalChiplets) {
		return
	}

	if cmd, ok := task.Payload.(*chiplet.CommandDescriptor); ok && cmd != nil {
		if cmd.Kind == chiplet.CommandKindPeTokenPrep {
			this.executedDigitalTasks++
			if this.statFactory != nil {
				this.statFactory.Increment("digital_tasks_total", 1)
			}
			return
		}
		if cmd.Target == chiplet.TaskTargetTransfer {
			if cmd.Flags&chiplet.TransferFlagDirectionMask == chiplet.TransferFlagDigitalToRram {
				if cmd.Queue < 0 {
					cmd.Queue = int32(chipletID)
				}
			} else if cmd.Flags&chiplet.TransferFlagDirectionMask == chiplet.TransferFlagRramToDigital {
				if cmd.ChipletID < 0 {
					cmd.ChipletID = int32(chipletID)
				}
			}
		}
	}

	if descriptor := this.buildDigitalTaskDescriptor(task, chipletID); descriptor != nil {
		if cmd, ok := task.Payload.(*chiplet.CommandDescriptor); ok && cmd != nil {
			if descriptor.Description == "" {
				descriptor.Description = cmd.Kind.String()
			}
		}
		if this.digitalChiplets[chipletID].SubmitDescriptor(descriptor) {
			if cmd, ok := task.Payload.(*chiplet.CommandDescriptor); ok && cmd != nil {
				this.recordGatingSnapshotFromCommand(chipletID, cmd)
				this.recordMoeBarrierMetrics(cmd)
			}
			this.executedDigitalTasks++
			if this.statFactory != nil {
				this.statFactory.Increment("digital_tasks_total", 1)
				this.statFactory.Increment("digital_load_bytes_total", descriptor.InputBytes+descriptor.WeightBytes)
				this.statFactory.Increment("digital_store_bytes_total", descriptor.OutputBytes)
				this.statFactory.Increment("digital_scalar_ops_total", int64(descriptor.ScalarOps))
				this.statFactory.Increment("digital_vector_ops_total", int64(descriptor.VectorOps))
			}
			this.digitalBytesLoaded += descriptor.InputBytes + descriptor.WeightBytes
			this.digitalBytesStored += descriptor.OutputBytes
			this.digitalScalarOps += int64(descriptor.ScalarOps)
			this.digitalVectorOps += int64(descriptor.VectorOps)
			return
		}
	}

	this.digitalChiplets[chipletID].ScheduleTask(task.Latency)
	this.executedDigitalTasks++
	if this.statFactory != nil {
		this.statFactory.Increment("digital_tasks_total", 1)
	}
}

func (this *ChipletPlatform) handleRramTask(task *chiplet.Task) {
	chipletID, ok := extractChipletID(task.Payload)
	if !ok || chipletID < 0 || chipletID >= len(this.rramChiplets) {
		return
	}

	spec := this.buildRramTaskSpec(task)
	this.rramChiplets[chipletID].ScheduleTask(task.Latency, spec)
	this.executedRramTasks++
	if this.statFactory != nil {
		this.statFactory.Increment("rram_tasks_total", 1)
		if spec != nil {
			if spec.PulseCount > 0 {
				this.statFactory.Increment("rram_pulse_count_total", int64(spec.PulseCount))
			}
			if spec.AdcSamples > 0 {
				this.statFactory.Increment("rram_adc_samples_total", int64(spec.AdcSamples))
			}
			if spec.PreCycles > 0 {
				this.statFactory.Increment("rram_preprocess_cycles_total", int64(spec.PreCycles))
			}
			if spec.PostCycles > 0 {
				this.statFactory.Increment("rram_postprocess_cycles_total", int64(spec.PostCycles))
			}
			if spec.ErrorAbs != 0 {
				this.statFactory.Increment("rram_error_samples", 1)
			}
		}
	}

	var cmdKind chiplet.CommandKind
	var stageLabel string
	var cmdDescriptor *chiplet.CommandDescriptor
	if cmd, ok := task.Payload.(*chiplet.CommandDescriptor); ok && cmd != nil {
		cmdKind = cmd.Kind
		cmdDescriptor = cmd
	} else if payloadMap, ok := task.Payload.(map[string]interface{}); ok {
		if s, ok := payloadMap["stage"].(string); ok {
			stageLabel = strings.ToLower(s)
		}
	}

	if cmdKind == chiplet.CommandKindRramStageAct || stageLabel == "stage_act" || stageLabel == "stage" {
		expectedBytes := int64(spec.ActivationSize)
		if expectedBytes <= 0 {
			expectedBytes = this.rramInputBuffered[chipletID]
		}
		this.consumeRramInput(chipletID, expectedBytes)
		outputBytes := int64(spec.OutputSize)
		if outputBytes <= 0 {
			outputBytes = expectedBytes
		}
		if chipletID >= 0 && chipletID < len(this.rramProcessingBytes) {
			this.rramProcessingBytes[chipletID] = outputBytes
		}
		if chipletID >= 0 && chipletID < len(this.rramChiplets) && spec != nil {
			if chip := this.rramChiplets[chipletID]; chip != nil {
				tileID, arrayID, weightTag := deriveWeightKey(cmdDescriptor, spec)
				if _, ok := chip.LookupWeights(tileID, arrayID, weightTag); !ok {
					chip.RegisterWeights(tileID, arrayID, weightTag, estimateWeightBytes(spec), this.currentCycle)
				}
			}
		}
	}
	if cmdKind == chiplet.CommandKindRramPost || stageLabel == "post" || stageLabel == "rram_post" {
		outputBytes := int64(spec.OutputSize)
		if outputBytes <= 0 {
			outputBytes = this.rramProcessingBytes[chipletID]
		}
		this.releaseRramOutputForChiplet(chipletID, outputBytes)
	}
	if cmdKind == chiplet.CommandKindRramWeightLoad && chipletID >= 0 && chipletID < len(this.rramChiplets) && spec != nil {
		if chip := this.rramChiplets[chipletID]; chip != nil {
			tileID, arrayID, weightTag := deriveWeightKey(cmdDescriptor, spec)
			weightBytes := estimateWeightBytes(spec)
			if _, ok := chip.LookupWeights(tileID, arrayID, weightTag); ok {
				chip.WeightLoads++
				chip.WeightLoadHits++
				if this.statFactory != nil {
					this.statFactory.Increment("rram_weight_loads_total", 1)
					this.statFactory.Increment("rram_weight_hits_total", 1)
				}
			} else {
				latency := 0
				if cmdDescriptor != nil {
					latency = int(cmdDescriptor.Latency)
				}
				chip.ScheduleWeightLoad(tileID, arrayID, weightTag, weightBytes, latency, this.currentCycle)
				if this.statFactory != nil {
					this.statFactory.Increment("rram_weight_loads_total", 1)
					this.statFactory.Increment("rram_weight_bytes_total", weightBytes)
				}
			}
		}
	}
}

func (this *ChipletPlatform) handleHostTask(task *chiplet.Task) {
	if task == nil {
		return
	}

	cmd, ok := task.Payload.(*chiplet.CommandDescriptor)
	if !ok || cmd == nil {
		return
	}

	meta := cloneMetadata(cmd.Metadata)
	topK := firstPositive(int(cmd.SubOp), metadataInt(meta, "top_k", 0))
	if topK <= 0 {
		topK = 1
	}
	tokens := firstPositive(int(cmd.Aux0), metadataInt(meta, "tokens", 0))
	features := firstPositive(int(cmd.Aux1), metadataInt(meta, "features", 0))
	bufferID := firstPositive(int(cmd.BufferID), metadataInt(meta, "buffer_id", 0))
	digitalID := metadataInt(meta, "digital_chiplet", int(cmd.ChipletID))
	if digitalID < 0 {
		digitalID = int(cmd.ChipletID)
	}
	if digitalID < 0 && this.topology != nil && this.topology.Digital.NumChiplets > 0 {
		digitalID = 0
	}

	snapshot := this.consumeGatingSnapshot(digitalID, bufferID)
	snapshotHit := snapshot != nil

	var candidates []int
	if snapshot != nil && len(snapshot.candidate) > 0 {
		candidates = cloneIntSlice(snapshot.candidate)
		meta["candidate_experts"] = cloneIntSlice(candidates)
	} else {
		candidates = this.ensureCandidateExperts(meta, metadataIntSlice(meta, "candidate_experts"))
	}

	var selected []int
	if snapshot != nil && len(snapshot.selected) > 0 {
		selected = cloneIntSlice(snapshot.selected)
	} else {
		selected = cloneIntSlice(metadataIntSlice(meta, "selected_experts"))
	}

	usedFallback := !snapshotHit
	if snapshot != nil {
		if snapshot.topK > 0 {
			topK = firstPositive(topK, snapshot.topK)
		}
		tokens = firstPositive(tokens, snapshot.tokens)
		features = firstPositive(features, snapshot.features)
		if snapshot.metadata != nil {
			for key, value := range snapshot.metadata {
				if _, exists := meta[key]; !exists {
					meta[key] = value
				}
			}
		}
	}

	if len(selected) == 0 {
		selected = selectTopExperts(candidates, topK)
		usedFallback = true
	}
	meta["selected_experts"] = cloneIntSlice(selected)

	activationBytes := metadataInt(meta, "activation_bytes", int(cmd.PayloadBytes))
	weightBytes := metadataInt(meta, "weight_bytes", int(cmd.PayloadAddr))
	outputBytes := metadataInt(meta, "output_bytes", int(cmd.PayloadBytes))

	if snapshot != nil {
		if snapshot.activationBytes > 0 {
			activationBytes = snapshot.activationBytes
		}
		if snapshot.weightBytes > 0 {
			weightBytes = snapshot.weightBytes
		}
		if snapshot.outputBytes > 0 {
			outputBytes = snapshot.outputBytes
		}
	}

	event := &chiplet.HostEvent{
		Kind:             cmd.Kind,
		Metadata:         meta,
		BufferID:         bufferID,
		TopK:             topK,
		Tokens:           tokens,
		Features:         features,
		CandidateExperts: cloneIntSlice(candidates),
		SelectedExperts:  cloneIntSlice(selected),
		ActivationBytes:  activationBytes,
		WeightBytes:      weightBytes,
		OutputBytes:      outputBytes,
		DigitalChiplet:   digitalID,
	}

	if strings.ToLower(metadataString(meta, "op", "")) == "moe_gating_fetch" {
		this.trackMoeEvent(task.NodeID, tokens, len(selected), snapshotHit, usedFallback)
	}

	if this.orchestrator != nil {
		this.orchestrator.NotifyHostEvent(task.NodeID, event)
	}
}

type bufferKind int

const (
	bufferKindDigital bufferKind = iota
	bufferKindRram
)

type stateKind int

const (
	stateKindRramInput stateKind = iota
	stateKindRramOutput
)

type bufferAdjustment struct {
	kind       bufferKind
	chipletID  int
	bufferName string
	delta      int64
}

type stateAdjustment struct {
	kind  stateKind
	index int
	delta int64
}

type transferAdjustmentTracker struct {
	buffers []bufferAdjustment
	states  []stateAdjustment
}

func (t *transferAdjustmentTracker) addBuffer(kind bufferKind, chipletID int, buffer string, delta int64) {
	if delta == 0 {
		return
	}
	t.buffers = append(t.buffers, bufferAdjustment{
		kind:       kind,
		chipletID:  chipletID,
		bufferName: strings.ToLower(buffer),
		delta:      delta,
	})
}

func (t *transferAdjustmentTracker) addState(kind stateKind, index int, delta int64) {
	if delta == 0 {
		return
	}
	t.states = append(t.states, stateAdjustment{
		kind:  kind,
		index: index,
		delta: delta,
	})
}

func (this *ChipletPlatform) handleTransferTask(task *chiplet.Task) {
	if task == nil {
		return
	}

	bytes := int64(1024)
	stage := "transfer_to_rram"
	var payloadMap map[string]interface{}

	srcDigitalIndex := -1
	dstDigitalIndex := -1
	srcRramIndex := -1
	dstRramIndex := -1
	hopCount := -1
	meta := cmdMetadata(task.Payload)

	if cmd, ok := task.Payload.(*chiplet.CommandDescriptor); ok && cmd != nil {
		if cmd.PayloadBytes > 0 {
			bytes = int64(cmd.PayloadBytes)
		}
		switch cmd.Kind {
		case chiplet.CommandKindTransferHost2D:
			stage = "transfer_host2d"
			dstDigitalIndex = int(cmd.ChipletID)
		case chiplet.CommandKindTransferD2Host:
			stage = "transfer_d2host"
			srcDigitalIndex = int(cmd.Queue)
		default:
			direction := cmd.Flags & chiplet.TransferFlagDirectionMask
			if direction == chiplet.TransferFlagRramToDigital {
				stage = "transfer_to_digital"
				srcRramIndex = int(cmd.Queue)
				dstDigitalIndex = int(cmd.ChipletID)
			} else {
				stage = "transfer_to_rram"
				srcDigitalIndex = int(cmd.Queue)
				dstRramIndex = int(cmd.ChipletID)
			}
		}
		if cmd.Metadata != nil {
			if v := metadataInt(cmd.Metadata, chiplet.MetadataKeySrcDigital, srcDigitalIndex); v >= 0 {
				srcDigitalIndex = v
			}
			if v := metadataInt(cmd.Metadata, chiplet.MetadataKeyDstDigital, dstDigitalIndex); v >= 0 {
				dstDigitalIndex = v
			}
			if v := metadataInt(cmd.Metadata, chiplet.MetadataKeySrcRram, srcRramIndex); v >= 0 {
				srcRramIndex = v
			}
			if v := metadataInt(cmd.Metadata, chiplet.MetadataKeyDstRram, dstRramIndex); v >= 0 {
				dstRramIndex = v
			}
			hopCount = metadataInt(cmd.Metadata, chiplet.MetadataKeyTransferHops, hopCount)
		}
	} else if payload, ok := task.Payload.(map[string]interface{}); ok {
		payloadMap = payload
		if value, ok := payload["bytes"]; ok {
			if iv, ok := toInt(value); ok && iv > 0 {
				bytes = int64(iv)
			} else if fv, ok := value.(float64); ok && fv > 0 {
				bytes = int64(fv)
			}
		}
		if s, ok := payload["stage"].(string); ok && s != "" {
			stage = strings.ToLower(s)
		}
		if v, ok := payload[chiplet.MetadataKeySrcDigital]; ok {
			if iv, ok := toInt(v); ok {
				srcDigitalIndex = iv
			}
		}
		if v, ok := payload[chiplet.MetadataKeyDstDigital]; ok {
			if iv, ok := toInt(v); ok {
				dstDigitalIndex = iv
			}
		}
		if v, ok := payload[chiplet.MetadataKeySrcRram]; ok {
			if iv, ok := toInt(v); ok {
				srcRramIndex = iv
			}
		}
		if v, ok := payload[chiplet.MetadataKeyDstRram]; ok {
			if iv, ok := toInt(v); ok {
				dstRramIndex = iv
			}
		}
		if v, ok := payload[chiplet.MetadataKeyTransferHops]; ok {
			if iv, ok := toInt(v); ok && iv > 0 {
				hopCount = iv
			}
		}
	}

	if bytes < 0 {
		bytes = 0
	}

	stageLower := strings.ToLower(stage)
	adjustments := transferAdjustmentTracker{}
	success := true
	failureReason := ""

	switch stageLower {
	case "transfer_to_rram":
		if payloadMap != nil {
			if val, ok := payloadMap[chiplet.MetadataKeySrcDigital]; ok {
				if iv, ok := toInt(val); ok {
					srcDigitalIndex = iv
				}
			}
			if val, ok := payloadMap[chiplet.MetadataKeyDstRram]; ok {
				if iv, ok := toInt(val); ok {
					dstRramIndex = iv
				}
			}
		}
		if srcDigitalIndex < 0 && len(this.digitalChiplets) > 0 {
			srcDigitalIndex = 0
		}
		if dstRramIndex < 0 && len(this.rramChiplets) > 0 {
			dstRramIndex = 0
		}
		if srcDigitalIndex < 0 || srcDigitalIndex >= len(this.digitalChiplets) {
			success = false
			failureReason = fmt.Sprintf("digital_source_missing chiplet=%d", srcDigitalIndex)
			break
		}
		if dstRramIndex < 0 || dstRramIndex >= len(this.rramChiplets) {
			success = false
			failureReason = fmt.Sprintf("rram_target_missing chiplet=%d", dstRramIndex)
			break
		}

		if chip := this.digitalChiplets[srcDigitalIndex]; chip != nil {
			usage := chip.BufferUsage("scratch")
			release := bytes
			if usage < release {
				release = usage
			}
			if release > 0 {
				if !chip.AdjustBuffer("Scratch", -release) {
					if failureReason == "" {
						failureReason = fmt.Sprintf("digital_scratch_release_fail chiplet=%d release=%d usage=%d", srcDigitalIndex, release, usage)
					}
					success = false
				} else {
					adjustments.addBuffer(bufferKindDigital, srcDigitalIndex, "scratch", -release)
				}
			}
		}

		if !success {
			break
		}

		if chip := this.rramChiplets[dstRramIndex]; chip != nil {
			if !chip.AdjustBuffer("input", bytes) {
				if failureReason == "" {
					current := chip.BufferUsage("input")
					failureReason = fmt.Sprintf("rram_input_reserve_fail chiplet=%d bytes=%d usage=%d cap=%d", dstRramIndex, bytes, current, chip.InputBufferCapacity)
				}
				success = false
				this.rramDeferrals[dstRramIndex]++
				this.rramSaturation[dstRramIndex]++
			} else {
				adjustments.addBuffer(bufferKindRram, dstRramIndex, "input", bytes)
				if dstRramIndex >= 0 && dstRramIndex < len(this.rramInputBuffered) {
					this.rramInputBuffered[dstRramIndex] += bytes
					adjustments.addState(stateKindRramInput, dstRramIndex, bytes)
				}
			}
		}
	case "transfer_to_digital":
		if payloadMap != nil {
			if val, ok := payloadMap[chiplet.MetadataKeySrcRram]; ok {
				if iv, ok := toInt(val); ok {
					srcRramIndex = iv
				}
			}
			if val, ok := payloadMap[chiplet.MetadataKeyDstDigital]; ok {
				if iv, ok := toInt(val); ok {
					dstDigitalIndex = iv
				}
			}
		}
		if srcRramIndex < 0 && len(this.rramChiplets) > 0 {
			srcRramIndex = 0
		}
		if dstDigitalIndex < 0 && len(this.digitalChiplets) > 0 {
			dstDigitalIndex = 0
		}
		if srcRramIndex < 0 || srcRramIndex >= len(this.rramChiplets) {
			success = false
			failureReason = fmt.Sprintf("rram_output_missing chiplet=%d", srcRramIndex)
			break
		}
		if dstDigitalIndex < 0 || dstDigitalIndex >= len(this.digitalChiplets) {
			success = false
			failureReason = fmt.Sprintf("digital_target_missing chiplet=%d", dstDigitalIndex)
			break
		}

		if chip := this.rramChiplets[srcRramIndex]; chip != nil {
			usage := chip.BufferUsage("output")
			release := bytes
			if usage < release {
				release = usage
			}
			if release > 0 {
				if !chip.AdjustBuffer("output", -release) {
					failureReason = fmt.Sprintf("rram_output_release_fail chiplet=%d release=%d usage=%d", srcRramIndex, release, usage)
					success = false
				} else {
					adjustments.addBuffer(bufferKindRram, srcRramIndex, "output", -release)
					if srcRramIndex >= 0 && srcRramIndex < len(this.rramOutputBuffered) {
						this.rramOutputBuffered[srcRramIndex] -= release
						if this.rramOutputBuffered[srcRramIndex] < 0 {
							this.rramOutputBuffered[srcRramIndex] = 0
						}
						adjustments.addState(stateKindRramOutput, srcRramIndex, -release)
					}
				}
			}
		}

		if !success {
			break
		}

		if !this.digitalChiplets[dstDigitalIndex].AdjustBuffer("Scratch", bytes) {
			usage := this.digitalChiplets[dstDigitalIndex].BufferUsage("scratch")
			failureReason = fmt.Sprintf("digital_scratch_reserve_fail chiplet=%d bytes=%d usage=%d", dstDigitalIndex, bytes, usage)
			success = false
			this.digitalDeferrals[dstDigitalIndex]++
			this.digitalSaturation[dstDigitalIndex]++
		} else {
			adjustments.addBuffer(bufferKindDigital, dstDigitalIndex, "scratch", bytes)
		}
	case "transfer_host2d":
		if payloadMap != nil {
			if val, ok := payloadMap[chiplet.MetadataKeyDstDigital]; ok {
				if iv, ok := toInt(val); ok {
					dstDigitalIndex = iv
				}
			}
		}
		if dstDigitalIndex < 0 && len(this.digitalChiplets) > 0 {
			dstDigitalIndex = 0
		}
		if dstDigitalIndex < 0 || dstDigitalIndex >= len(this.digitalChiplets) {
			success = false
			failureReason = fmt.Sprintf("digital_target_missing chiplet=%d", dstDigitalIndex)
			break
		}
		if !this.digitalChiplets[dstDigitalIndex].AdjustBuffer("Activation", bytes) {
			usage := this.digitalChiplets[dstDigitalIndex].BufferUsage("activation")
			failureReason = fmt.Sprintf("digital_activation_reserve_fail chiplet=%d bytes=%d usage=%d", dstDigitalIndex, bytes, usage)
			success = false
			this.digitalDeferrals[dstDigitalIndex]++
			this.digitalSaturation[dstDigitalIndex]++
		} else {
			adjustments.addBuffer(bufferKindDigital, dstDigitalIndex, "activation", bytes)
		}
	case "transfer_d2host":
		if payloadMap != nil {
			if val, ok := payloadMap[chiplet.MetadataKeySrcDigital]; ok {
				if iv, ok := toInt(val); ok {
					srcDigitalIndex = iv
				}
			}
		}
		if srcDigitalIndex < 0 && len(this.digitalChiplets) > 0 {
			srcDigitalIndex = 0
		}
		if srcDigitalIndex < 0 || srcDigitalIndex >= len(this.digitalChiplets) {
			success = false
			failureReason = fmt.Sprintf("digital_source_missing chiplet=%d", srcDigitalIndex)
			break
		}
		if chip := this.digitalChiplets[srcDigitalIndex]; chip != nil {
			usage := chip.BufferUsage("activation")
			release := bytes
			if usage < release {
				release = usage
			}
			if release > 0 {
				if !chip.AdjustBuffer("Activation", -release) {
					failureReason = fmt.Sprintf("digital_activation_release_fail chiplet=%d release=%d usage=%d", srcDigitalIndex, release, usage)
					success = false
				} else {
					adjustments.addBuffer(bufferKindDigital, srcDigitalIndex, "activation", -release)
				}
			}
		}
	default:
		// Unrecognized stage; no buffer adjustments.
	}

	if !success {
		rollbackTransferBuffers(&adjustments, this)
		if failureReason == "" {
			failureReason = "unspecified"
		}
		fmt.Printf("[chiplet-debug] transfer stage=%s failed bytes=%d reason=%s\n", stageLower, bytes, failureReason)
		this.transferThrottleUntil += 2
		this.transferThrottleEvents++
		this.cycleThrottleEvents++
		this.transferThrottleEventsTotal++
		if this.statFactory != nil {
			this.statFactory.Increment("transfer_buffer_saturation", 1)
			this.statFactory.Increment("transfer_throttle_events_total", 1)
		}
		return
	}

	this.handleKvAccess(stageLower, bytes, meta)

	if hopCount <= 0 && this.topology != nil {
		switch stageLower {
		case "transfer_to_rram":
			if srcDigitalIndex >= 0 && dstRramIndex >= 0 {
				hopCount = this.topology.DigitalToRramHopDistance(srcDigitalIndex, dstRramIndex)
			}
		case "transfer_to_digital":
			if srcRramIndex >= 0 && dstDigitalIndex >= 0 {
				hopCount = this.topology.RramToDigitalHopDistance(srcRramIndex, dstDigitalIndex)
			}
		}
	}
	if hopCount <= 0 {
		hopCount = 1
	}

	switch stageLower {
	case "transfer_to_rram":
		this.totalTransferToRramBytes += bytes
	case "transfer_to_digital":
		this.totalTransferToDigitalBytes += bytes
	case "transfer_host2d":
		this.totalTransferHostLoadBytes += bytes
	case "transfer_d2host":
		this.totalTransferHostStoreBytes += bytes
	}

	energyBytes := hopWeightedBytes(bytes, hopCount)
	fmt.Printf("[chiplet-debug] transfer stage=%s bytes=%d hops=%d srcDigital=%d dstDigital=%d srcRram=%d dstRram=%d\n",
		stageLower, bytes, hopCount, srcDigitalIndex, dstDigitalIndex, srcRramIndex, dstRramIndex)
	switch stageLower {
	case "transfer_to_rram":
		if srcDigitalIndex >= 0 && srcDigitalIndex < len(this.digitalChiplets) {
			if chip := this.digitalChiplets[srcDigitalIndex]; chip != nil {
				chip.AddInterconnectEnergy(energyBytes)
			}
		}
		if dstRramIndex >= 0 && dstRramIndex < len(this.rramChiplets) {
			if chip := this.rramChiplets[dstRramIndex]; chip != nil {
				chip.AddInputTransferEnergy(energyBytes)
			}
		}
		estimated := this.estimateNocCycles(stageLower, bytes, hopCount, srcDigitalIndex, dstRramIndex, srcRramIndex, dstDigitalIndex, meta)
		if estimated > 0 {
			this.transferThrottleUntil += estimated
		}
	case "transfer_to_digital":
		if dstDigitalIndex >= 0 && dstDigitalIndex < len(this.digitalChiplets) {
			if chip := this.digitalChiplets[dstDigitalIndex]; chip != nil {
				chip.AddInterconnectEnergy(energyBytes)
			}
		}
		if srcRramIndex >= 0 && srcRramIndex < len(this.rramChiplets) {
			if chip := this.rramChiplets[srcRramIndex]; chip != nil {
				chip.AddOutputTransferEnergy(energyBytes)
			}
		}
		estimated := this.estimateNocCycles(stageLower, bytes, hopCount, srcDigitalIndex, dstRramIndex, srcRramIndex, dstDigitalIndex, meta)
		if estimated > 0 {
			this.transferThrottleUntil += estimated
		}
	case "transfer_host2d":
		if dstDigitalIndex >= 0 && dstDigitalIndex < len(this.digitalChiplets) {
			if chip := this.digitalChiplets[dstDigitalIndex]; chip != nil {
				chip.AddInterconnectEnergy(energyBytes)
			}
		}
	case "transfer_d2host":
		if srcDigitalIndex >= 0 && srcDigitalIndex < len(this.digitalChiplets) {
			if chip := this.digitalChiplets[srcDigitalIndex]; chip != nil {
				chip.AddInterconnectEnergy(energyBytes)
			}
		}
	}

	this.executedTransferTasks++
	this.cycleTransferBytes += bytes
	this.totalTransferBytes += bytes
	this.cycleTransferHops += hopCount
	this.totalTransferHops += int64(hopCount)

	switch stageLower {
	case "transfer_to_rram":
		if srcDigitalIndex >= 0 && srcDigitalIndex < len(this.digitalChiplets) {
			if chip := this.digitalChiplets[srcDigitalIndex]; chip != nil {
				chip.AddInterconnectEnergy(energyBytes)
			}
		}
		if dstRramIndex >= 0 && dstRramIndex < len(this.rramChiplets) {
			if chip := this.rramChiplets[dstRramIndex]; chip != nil {
				chip.AddInputTransferEnergy(energyBytes)
			}
		}
	case "transfer_to_digital":
		if dstDigitalIndex >= 0 && dstDigitalIndex < len(this.digitalChiplets) {
			if chip := this.digitalChiplets[dstDigitalIndex]; chip != nil {
				chip.AddInterconnectEnergy(energyBytes)
			}
		}
		if srcRramIndex >= 0 && srcRramIndex < len(this.rramChiplets) {
			if chip := this.rramChiplets[srcRramIndex]; chip != nil {
				chip.AddOutputTransferEnergy(energyBytes)
			}
		}
	case "transfer_host2d":
		if dstDigitalIndex >= 0 && dstDigitalIndex < len(this.digitalChiplets) {
			if chip := this.digitalChiplets[dstDigitalIndex]; chip != nil {
				chip.AddInterconnectEnergy(energyBytes)
			}
		}
		this.cycleHostDmaLoadBytes += bytes
		this.hostDmaLoadBytesTotal += bytes
		estimated := 0
		if this.hostDmaController != nil {
			this.hostDmaController.Record(host.DMATransferHostToDigital, bytes, hopCount)
			estimated = this.hostDmaController.EstimateCycles(bytes, hopCount, meta)
		}
		if estimated > 0 {
			this.transferThrottleUntil += estimated
		}
	case "transfer_d2host":
		if srcDigitalIndex >= 0 && srcDigitalIndex < len(this.digitalChiplets) {
			if chip := this.digitalChiplets[srcDigitalIndex]; chip != nil {
				chip.AddInterconnectEnergy(energyBytes)
			}
		}
		this.cycleHostDmaStoreBytes += bytes
		this.hostDmaStoreBytesTotal += bytes
		estimated := 0
		if this.hostDmaController != nil {
			this.hostDmaController.Record(host.DMATransferDigitalToHost, bytes, hopCount)
			estimated = this.hostDmaController.EstimateCycles(bytes, hopCount, meta)
		}
		if estimated > 0 {
			this.transferThrottleUntil += estimated
		}
	}

	if this.statFactory != nil {
		this.statFactory.Increment("transfer_tasks_total", 1)
		this.statFactory.Increment("transfer_bytes_total", bytes)
		this.statFactory.Increment("transfer_hops_total", int64(hopCount))
		switch stageLower {
		case "transfer_to_rram":
			this.statFactory.Increment("transfer_to_rram_bytes_total", bytes)
			this.statFactory.Increment("transfer_to_rram_hops_total", int64(hopCount))
		case "transfer_to_digital":
			this.statFactory.Increment("transfer_to_digital_bytes_total", bytes)
			this.statFactory.Increment("transfer_to_digital_hops_total", int64(hopCount))
		case "transfer_host2d":
			this.statFactory.Increment("host_dma_load_bytes_total", bytes)
			this.statFactory.Increment("transfer_host2d_hops_total", int64(hopCount))
		case "transfer_d2host":
			this.statFactory.Increment("host_dma_store_bytes_total", bytes)
			this.statFactory.Increment("transfer_d2host_hops_total", int64(hopCount))
		}
	}
}

func (this *ChipletPlatform) estimateNocCycles(stage string, bytes int64, hops int, srcDigital int, dstRram int, srcRram int, dstDigital int, meta map[string]interface{}) int {
	if bytes <= 0 {
		return 0
	}

	stageLower := strings.ToLower(stage)
	bandwidth := int64(0)
	if this.config != nil {
		switch stageLower {
		case "transfer_to_rram":
			bandwidth = this.config.TransferBandwidthDr
		case "transfer_to_digital":
			bandwidth = this.config.TransferBandwidthRd
		}
	}

	fallback := estimateTransferCycles(bytes, bandwidth, hops)

	client := this.booksimClient
	if client == nil || !client.Enabled() {
		return fallback
	}

	totalDigital := len(this.digitalChiplets)
	totalRram := len(this.rramChiplets)

	switch stageLower {
	case "transfer_to_rram":
		srcNode := this.nocDigitalNodeID(srcDigital, totalDigital)
		dstNode := this.nocRramNodeID(dstRram, totalDigital, totalRram)
		if srcNode < 0 || dstNode < 0 {
			return fallback
		}
		if cycles, ok := client.Estimate(srcNode, dstNode, bytes, meta); ok && cycles > 0 {
			return cycles
		}
	case "transfer_to_digital":
		srcNode := this.nocRramNodeID(srcRram, totalDigital, totalRram)
		dstNode := this.nocDigitalNodeID(dstDigital, totalDigital)
		if srcNode < 0 || dstNode < 0 {
			return fallback
		}
		if cycles, ok := client.Estimate(srcNode, dstNode, bytes, meta); ok && cycles > 0 {
			return cycles
		}
	}

	return fallback
}

func estimateTransferCycles(bytes int64, bandwidth int64, hops int) int {
	if bandwidth <= 0 {
		bandwidth = 4096
	}
	cycles := int((bytes + bandwidth - 1) / bandwidth)
	if cycles <= 0 {
		cycles = 1
	}
	if hops > 0 {
		cycles += hops
	}
	return cycles
}

func (this *ChipletPlatform) handleKvAccess(stage string, bytes int64, meta map[string]interface{}) {
	if this.kvCache == nil || bytes <= 0 {
		return
	}

	op, info, ok := kvAccessMetadata(stage, meta)
	if !ok {
		return
	}

	result := this.kvCache.Access(op, info, bytes)

	switch result.Op {
	case host.KVCacheOpLoad:
		this.kvCacheLoads++
		this.kvCacheLoadBytes += result.Bytes
		this.cycleKvLoadBytes += result.Bytes
		if this.statFactory != nil {
			this.statFactory.Increment("kv_cache_loads_total", 1)
			this.statFactory.Increment("kv_cache_load_bytes_total", result.Bytes)
		}
	case host.KVCacheOpStore:
		this.kvCacheStores++
		this.kvCacheStoreBytes += result.Bytes
		this.cycleKvStoreBytes += result.Bytes
		if this.statFactory != nil {
			this.statFactory.Increment("kv_cache_stores_total", 1)
			this.statFactory.Increment("kv_cache_store_bytes_total", result.Bytes)
		}
	}

	if result.Hit {
		this.kvCacheHits++
		this.kvCacheHitBytes += result.Bytes
		this.cycleKvHits++
		if this.statFactory != nil {
			this.statFactory.Increment("kv_cache_hits_total", 1)
			this.statFactory.Increment("kv_cache_hit_bytes_total", result.Bytes)
		}
	} else {
		this.kvCacheMisses++
		this.kvCacheMissBytes += result.Bytes
		this.cycleKvMisses++
		if this.statFactory != nil {
			this.statFactory.Increment("kv_cache_misses_total", 1)
			this.statFactory.Increment("kv_cache_miss_bytes_total", result.Bytes)
		}
	}

	if result.EvictedBytes > 0 {
		this.kvCacheEvictedBytes += result.EvictedBytes
		if this.statFactory != nil {
			this.statFactory.Increment("kv_cache_evicted_bytes_total", result.EvictedBytes)
		}
	}

	if result.Resident > this.kvCachePeakBytes {
		this.kvCachePeakBytes = result.Resident
	}
}

func (this *ChipletPlatform) consumeRramInput(chipletID int, bytes int64) {
	if chipletID < 0 || chipletID >= len(this.rramChiplets) {
		return
	}
	chiplet := this.rramChiplets[chipletID]
	if chiplet == nil {
		return
	}

	if bytes <= 0 {
		bytes = this.rramInputBuffered[chipletID]
	}
	if bytes <= 0 {
		return
	}

	if bytes > this.rramInputBuffered[chipletID] {
		bytes = this.rramInputBuffered[chipletID]
	}

	if bytes <= 0 {
		return
	}

	before := chiplet.BufferUsage("input")
	if !chiplet.AdjustBuffer("input", -bytes) {
		// AdjustBuffer may clamp occupancy; recompute actual consumed bytes.
	}
	after := chiplet.BufferUsage("input")
	consumed := before - after
	if consumed < 0 {
		consumed = 0
	}
	if consumed > 0 {
		this.rramInputBuffered[chipletID] -= consumed
		if this.rramInputBuffered[chipletID] < 0 {
			this.rramInputBuffered[chipletID] = 0
		}
		this.rramProcessingBytes[chipletID] += consumed
	}
}

func (this *ChipletPlatform) releaseRramOutputForChiplet(chipletID int, bytes int64) {
	if chipletID < 0 || chipletID >= len(this.rramChiplets) {
		return
	}
	chiplet := this.rramChiplets[chipletID]
	if chiplet == nil {
		return
	}

	produce := bytes
	if produce <= 0 {
		produce = this.rramProcessingBytes[chipletID]
	}
	if produce <= 0 {
		return
	}

	before := chiplet.BufferUsage("output")
	if !chiplet.AdjustBuffer("output", produce) {
		// Adjustment may saturate at capacity; rely on occupancy delta.
	}
	after := chiplet.BufferUsage("output")
	added := after - before
	if added < 0 {
		added = 0
	}
	if added > 0 {
		if added > this.rramProcessingBytes[chipletID] {
			this.rramProcessingBytes[chipletID] = 0
		} else {
			this.rramProcessingBytes[chipletID] -= added
		}
		this.rramOutputBuffered[chipletID] += added
		current := chiplet.BufferUsage("output")
		if this.rramOutputBuffered[chipletID] > current {
			this.rramOutputBuffered[chipletID] = current
		}
	}
	if this.rramProcessingBytes[chipletID] < 0 {
		this.rramProcessingBytes[chipletID] = 0
	}
}

func extractChipletID(payload interface{}) (int, bool) {
	switch v := payload.(type) {
	case map[string]int:
		id, found := v["chiplet_id"]
		return id, found
	case map[string]interface{}:
		if val, found := v["chiplet_id"]; found {
			switch id := val.(type) {
			case int:
				return id, true
			case int32:
				return int(id), true
			case int64:
				return int(id), true
			case float64:
				return int(id), true
			}
		}
	case *chiplet.CommandDescriptor:
		if v == nil {
			return 0, false
		}
		return int(v.ChipletID), v.ChipletID >= 0
	}

	return 0, false
}

func cloneMetadata(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return nil
	}
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func kvAccessMetadata(stage string, meta map[string]interface{}) (host.KVCacheOp, host.KVAccessInfo, bool) {
	stageLower := strings.ToLower(stage)
	var defaultOp host.KVCacheOp
	switch stageLower {
	case "transfer_host2d":
		defaultOp = host.KVCacheOpLoad
	case "transfer_d2host":
		defaultOp = host.KVCacheOpStore
	default:
		return host.KVCacheOpUnknown, host.KVAccessInfo{}, false
	}

	opStr := strings.ToLower(metadataString(meta, "kv_op", ""))
	op := defaultOp
	switch opStr {
	case "load":
		op = host.KVCacheOpLoad
	case "store":
		op = host.KVCacheOpStore
	case "":
		// keep default
	default:
		// 未知字符串，沿用默认操作
	}

	if op == host.KVCacheOpUnknown {
		return host.KVCacheOpUnknown, host.KVAccessInfo{}, false
	}

	info := host.KVAccessInfo{
		Layer:    metadataInt(meta, "kv_layer", -1),
		Head:     metadataInt(meta, "kv_head", -1),
		Sequence: metadataInt(meta, "kv_seq", -1),
		Token:    metadataInt(meta, "kv_token", -1),
		Batch:    metadataInt(meta, "kv_batch", -1),
	}
	if key := metadataString(meta, "kv_key", ""); key != "" {
		info.Key = key
	}
	return op, info, true
}

func metadataInt(meta map[string]interface{}, key string, fallback int) int {
	if meta == nil {
		return fallback
	}
	if value, exists := meta[key]; exists {
		if iv, ok := toInt(value); ok {
			return iv
		}
	}
	return fallback
}

func metadataIntSlice(meta map[string]interface{}, key string) []int {
	if meta == nil {
		return nil
	}
	raw, exists := meta[key]
	if !exists {
		return nil
	}
	if slice, ok := toIntSlice(raw); ok {
		return slice
	}
	return nil
}

func metadataFloatSlice(meta map[string]interface{}, key string) []float64 {
	if meta == nil {
		return nil
	}
	raw, exists := meta[key]
	if !exists {
		return nil
	}
	if slice, ok := toFloatSlice(raw); ok {
		return slice
	}
	return nil
}

func cmdMetadata(payload interface{}) map[string]interface{} {
	switch v := payload.(type) {
	case *chiplet.CommandDescriptor:
		return cloneMetadata(v.Metadata)
	case map[string]interface{}:
		return v
	default:
		return nil
	}
}

func (this *ChipletPlatform) buildTransferLatencyEstimator() chiplet.TransferLatencyEstimator {
	client := this.booksimClient
	if client == nil || !client.Enabled() {
		return nil
	}

	totalDigital := 0
	totalRram := 0
	if this.config != nil {
		totalDigital = this.config.NumDigitalChiplets
		totalRram = this.config.NumRramChiplets
	}

	return func(query chiplet.TransferLatencyQuery) (int, bool) {
		client := this.booksimClient
		if client == nil || !client.Enabled() || query.Bytes <= 0 {
			return 0, false
		}

		stage := strings.ToLower(query.Stage)
		src := -1
		dst := -1
		switch {
		case strings.Contains(stage, "to_rram") || strings.HasSuffix(stage, "transfer_in"):
			src = this.nocDigitalNodeID(query.SrcDigital, totalDigital)
			dst = this.nocRramNodeID(query.DstRram, totalDigital, totalRram)
		case strings.Contains(stage, "to_digital") || strings.HasSuffix(stage, "transfer_out"):
			src = this.nocRramNodeID(query.SrcRram, totalDigital, totalRram)
			dst = this.nocDigitalNodeID(query.DstDigital, totalDigital)
		default:
			return 0, false
		}
		if src < 0 || dst < 0 {
			return 0, false
		}

		cycles, ok := client.Estimate(src, dst, query.Bytes, query.Metadata)
		return cycles, ok
	}
}

func (this *ChipletPlatform) nocDigitalNodeID(id int, total int) int {
	if total <= 0 || id < 0 || id >= total {
		return -1
	}
	return id
}

func (this *ChipletPlatform) nocRramNodeID(id int, totalDigital int, totalRram int) int {
	if totalRram <= 0 || id < 0 || id >= totalRram {
		return -1
	}
	return totalDigital + id
}

func hopWeightedBytes(bytes int64, hops int) int64 {
	if bytes < 0 {
		bytes = 0
	}
	if hops <= 0 {
		hops = 1
	}
	return bytes * int64(hops)
}

func rollbackTransferBuffers(adjustments *transferAdjustmentTracker, platform *ChipletPlatform) {
	if platform == nil || adjustments == nil {
		return
	}

	for i := len(adjustments.buffers) - 1; i >= 0; i-- {
		adj := adjustments.buffers[i]
		switch adj.kind {
		case bufferKindDigital:
			if adj.chipletID >= 0 && adj.chipletID < len(platform.digitalChiplets) {
				platform.digitalChiplets[adj.chipletID].AdjustBuffer(adj.bufferName, -adj.delta)
			}
		case bufferKindRram:
			if adj.chipletID >= 0 && adj.chipletID < len(platform.rramChiplets) {
				platform.rramChiplets[adj.chipletID].AdjustBuffer(adj.bufferName, -adj.delta)
			}
		}
	}

	for i := len(adjustments.states) - 1; i >= 0; i-- {
		state := adjustments.states[i]
		switch state.kind {
		case stateKindRramInput:
			if state.index >= 0 && state.index < len(platform.rramInputBuffered) {
				platform.rramInputBuffered[state.index] -= state.delta
				if platform.rramInputBuffered[state.index] < 0 {
					platform.rramInputBuffered[state.index] = 0
				}
			}
		case stateKindRramOutput:
			if state.index >= 0 && state.index < len(platform.rramOutputBuffered) {
				platform.rramOutputBuffered[state.index] -= state.delta
				if platform.rramOutputBuffered[state.index] < 0 {
					platform.rramOutputBuffered[state.index] = 0
				}
				if state.index >= 0 && state.index < len(platform.rramChiplets) {
					if chip := platform.rramChiplets[state.index]; chip != nil {
						current := chip.BufferUsage("output")
						if platform.rramOutputBuffered[state.index] > current {
							platform.rramOutputBuffered[state.index] = current
						}
					}
				}
			}
		}
	}
}

func selectTopExperts(candidates []int, topK int) []int {
	if topK <= 0 {
		topK = 1
	}
	if len(candidates) == 0 {
		result := make([]int, topK)
		for i := 0; i < topK; i++ {
			result[i] = i
		}
		return result
	}
	if len(candidates) <= topK {
		return append([]int(nil), candidates...)
	}
	result := make([]int, 0, topK)
	for i := 0; i < topK; i++ {
		idx := i % len(candidates)
		result = append(result, candidates[idx])
	}
	return result
}

func cloneIntSlice(src []int) []int {
	if len(src) == 0 {
		return nil
	}
	dst := make([]int, len(src))
	copy(dst, src)
	return dst
}

func (this *ChipletPlatform) ensureCandidateExperts(meta map[string]interface{}, candidates []int) []int {
	if len(candidates) > 0 {
		cloned := cloneIntSlice(candidates)
		if meta != nil {
			meta["candidate_experts"] = cloneIntSlice(cloned)
		}
		return cloned
	}

	defaultCnt := 4
	if this.topology != nil && this.topology.Rram.NumChiplets > 0 {
		defaultCnt = this.topology.Rram.NumChiplets
	}
	if defaultCnt <= 0 {
		defaultCnt = 1
	}
	generated := make([]int, defaultCnt)
	for i := 0; i < defaultCnt; i++ {
		generated[i] = i
	}
	if meta != nil {
		meta["candidate_experts"] = cloneIntSlice(generated)
	}
	return generated
}

func topExpertsByScore(scores []float64, candidates []int, topK int) []int {
	if len(scores) == 0 || len(candidates) == 0 {
		return nil
	}
	if topK <= 0 {
		topK = 1
	}
	type scorePair struct {
		index int
		score float64
	}
	limit := len(candidates)
	if len(scores) < limit {
		limit = len(scores)
	}
	pairs := make([]scorePair, limit)
	for i := 0; i < limit; i++ {
		pairs[i] = scorePair{index: i, score: scores[i]}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].score == pairs[j].score {
			return candidates[pairs[i].index] < candidates[pairs[j].index]
		}
		return pairs[i].score > pairs[j].score
	})
	result := make([]int, 0, topK)
	seen := make(map[int]struct{}, topK)
	for _, pair := range pairs {
		if len(result) >= topK {
			break
		}
		expert := candidates[pair.index]
		if _, exists := seen[expert]; exists {
			continue
		}
		seen[expert] = struct{}{}
		result = append(result, expert)
	}
	return result
}

func firstPositive(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

func (this *ChipletPlatform) recordGatingSnapshotFromCommand(digitalID int, cmd *chiplet.CommandDescriptor) {
	if this == nil || cmd == nil {
		return
	}
	if strings.ToLower(metadataString(cmd.Metadata, "op", "")) != "topk_select" {
		return
	}
	if this.gatingQueues == nil {
		this.gatingQueues = make(map[gatingKey][]*moeGatingSnapshot)
	}

	meta := cloneMetadata(cmd.Metadata)
	bufferID := metadataInt(meta, "buffer_id", int(cmd.BufferID))
	if bufferID < 0 {
		bufferID = 0
	}

	topK := firstPositive(metadataInt(meta, "top_k", 0), int(cmd.Aux1))
	if topK <= 0 {
		topK = 1
	}
	tokens := firstPositive(metadataInt(meta, "tokens", 0), int(cmd.Aux0))
	features := firstPositive(metadataInt(meta, "features", 0), int(cmd.Queue))

	candidates := this.ensureCandidateExperts(meta, metadataIntSlice(meta, "candidate_experts"))
	selected := cloneIntSlice(metadataIntSlice(meta, "selected_experts"))

	if len(selected) == 0 {
		if scores := metadataFloatSlice(meta, "gating_scores"); len(scores) > 0 {
			if picked := topExpertsByScore(scores, candidates, topK); len(picked) > 0 {
				selected = picked
			}
		}
	}
	if len(selected) == 0 {
		selected = selectTopExperts(candidates, topK)
	} else if len(selected) > topK {
		selected = selected[:topK]
	}
	meta["selected_experts"] = cloneIntSlice(selected)

	activationBytes := metadataInt(meta, "activation_bytes", int(cmd.PayloadBytes))
	weightBytes := metadataInt(meta, "weight_bytes", int(cmd.PayloadAddr))
	outputBytes := metadataInt(meta, "output_bytes", int(cmd.PayloadBytes))

	snapshot := &moeGatingSnapshot{
		commandID:       cmd.ID,
		issuedCycle:     this.currentCycle,
		tokens:          tokens,
		features:        features,
		topK:            topK,
		candidate:       cloneIntSlice(candidates),
		selected:        cloneIntSlice(selected),
		activationBytes: activationBytes,
		weightBytes:     weightBytes,
		outputBytes:     outputBytes,
		metadata:        meta,
	}

	key := gatingKey{digitalID: digitalID, bufferID: bufferID}
	this.gatingQueues[key] = append(this.gatingQueues[key], snapshot)
}

func (this *ChipletPlatform) consumeGatingSnapshot(digitalID int, bufferID int) *moeGatingSnapshot {
	if this == nil || this.gatingQueues == nil {
		return nil
	}
	key := gatingKey{digitalID: digitalID, bufferID: bufferID}
	queue := this.gatingQueues[key]
	if len(queue) == 0 {
		return nil
	}
	snapshot := queue[0]
	if len(queue) == 1 {
		delete(this.gatingQueues, key)
	} else {
		this.gatingQueues[key] = queue[1:]
	}
	return snapshot
}

func (this *ChipletPlatform) trackMoeEvent(nodeID int, tokens int, experts int, snapshotHit bool, fallback bool) {
	if nodeID < 0 {
		return
	}
	this.moeEventsTotal++
	this.moeTokensTotal += int64(tokens)
	this.moeExpertsTotal += int64(experts)
	if snapshotHit {
		this.moeSnapshotHits++
		if this.statFactory != nil {
			this.statFactory.Increment("moe_snapshot_hits_total", 1)
		}
	} else {
		this.moeSnapshotMisses++
		if this.statFactory != nil {
			this.statFactory.Increment("moe_snapshot_misses_total", 1)
		}
	}
	if fallback {
		this.moeFallbackEvents++
		if this.statFactory != nil {
			this.statFactory.Increment("moe_fallback_events_total", 1)
		}
	}
	if this.statFactory != nil {
		this.statFactory.Increment("moe_events_total", 1)
		this.statFactory.Increment("moe_tokens_total", int64(tokens))
		this.statFactory.Increment("moe_experts_total", int64(experts))
	}
	if this.moeEventMetrics == nil {
		this.moeEventMetrics = make(map[int]*moeEventMetrics)
	}
	this.moeEventMetrics[nodeID] = &moeEventMetrics{
		startCycle: this.currentCycle,
		tokens:     tokens,
		experts:    experts,
		fallback:   fallback,
	}
}

func (this *ChipletPlatform) recordMoeBarrierMetrics(cmd *chiplet.CommandDescriptor) {
	if cmd == nil {
		return
	}
	if strings.ToLower(metadataString(cmd.Metadata, "op", "")) != "moe_barrier" {
		return
	}
	parent := metadataInt(cmd.Metadata, "parent_node", -1)
	if parent < 0 {
		return
	}
	metrics, ok := this.moeEventMetrics[parent]
	if !ok {
		return
	}
	latency := this.currentCycle - metrics.startCycle
	if latency < 0 {
		latency = 0
	}
	this.moeLatencySamples++
	this.moeLatencyTotal += int64(latency)
	if latency > this.moeLatencyMax {
		this.moeLatencyMax = latency
	}
	this.moeSessionsCompleted++
	delete(this.moeEventMetrics, parent)
	if this.statFactory != nil {
		this.statFactory.Increment("moe_latency_samples", 1)
		this.statFactory.Increment("moe_latency_total_cycles", int64(latency))
		this.statFactory.Increment("moe_sessions_completed_total", 1)
		currentMax := this.statFactory.Value("moe_latency_max_cycles")
		if int64(this.moeLatencyMax) > currentMax {
			this.statFactory.Increment("moe_latency_max_cycles", int64(this.moeLatencyMax)-currentMax)
		}
	}
}

func (this *ChipletPlatform) appendMoeSummaryRow() {
	if this == nil || this.moeSummaryAppended || this.moeEventsTotal == 0 {
		return
	}
	this.moeSummaryAppended = true
	avgLatency := 0.0
	if this.moeLatencySamples > 0 {
		avgLatency = float64(this.moeLatencyTotal) / float64(this.moeLatencySamples)
	}
	hitTotal := this.moeSnapshotHits + this.moeSnapshotMisses
	hitRate := 0.0
	if hitTotal > 0 {
		hitRate = float64(this.moeSnapshotHits) / float64(hitTotal)
	}
	fallbackRate := 0.0
	if this.moeEventsTotal > 0 {
		fallbackRate = float64(this.moeFallbackEvents) / float64(this.moeEventsTotal)
	}
	line := fmt.Sprintf("%d,-1,0,0,0,0,0,%d,%.6f,%d,%.6f,%.6f",
		this.currentCycle,
		this.moeEventsTotal,
		avgLatency,
		this.moeLatencyMax,
		hitRate,
		fallbackRate,
	)
	this.resultLog = append(this.resultLog, line)
}

func (this *ChipletPlatform) buildDigitalTaskDescriptor(task *chiplet.Task, chipletID int) *digital.TaskDescriptor {
	if task == nil {
		return nil
	}

	if desc, ok := task.Payload.(*chiplet.CommandDescriptor); ok && desc != nil {
		return this.buildDigitalDescriptorFromCommand(desc, chipletID)
	}

	payloadMap, ok := task.Payload.(map[string]interface{})
	if !ok {
		return nil
	}

	stageRaw, ok := payloadMap["stage"].(string)
	if !ok {
		return nil
	}

	stage := strings.ToLower(stageRaw)
	const bytesPerF16 = 2

	desc := &digital.TaskDescriptor{
		Description: stageRaw,
	}

	defaultRows := 128
	defaultCols := 128
	if this.topology != nil {
		if this.topology.Digital.PeRows > 0 {
			defaultRows = this.topology.Digital.PeRows
		}
		if this.topology.Digital.PeCols > 0 {
			defaultCols = this.topology.Digital.PeCols
		}
	}

	switch stage {
	case "attention", "gemm", "matmul":
		tileM := defaultRows
		tileN := defaultCols
		tileK := defaultRows
		if value, exists := payloadMap["tile_m"]; exists {
			if iv, ok := toInt(value); ok && iv > 0 {
				tileM = iv
			}
		}
		if value, exists := payloadMap["tile_n"]; exists {
			if iv, ok := toInt(value); ok && iv > 0 {
				tileN = iv
			}
		}
		if value, exists := payloadMap["tile_k"]; exists {
			if iv, ok := toInt(value); ok && iv > 0 {
				tileK = iv
			}
		}

		problemM := tileM
		problemN := tileN
		problemK := tileK
		if value, exists := payloadMap["problem_m"]; exists {
			if iv, ok := toInt(value); ok && iv > 0 {
				problemM = iv
			}
		}
		if value, exists := payloadMap["problem_n"]; exists {
			if iv, ok := toInt(value); ok && iv > 0 {
				problemN = iv
			}
		}
		if value, exists := payloadMap["problem_k"]; exists {
			if iv, ok := toInt(value); ok && iv > 0 {
				problemK = iv
			}
		}

		if problemM <= 0 {
			problemM = defaultRows
		}
		if problemN <= 0 {
			problemN = defaultCols
		}
		if problemK <= 0 {
			problemK = defaultRows
		}

		if tileM <= 0 {
			tileM = problemM
		}
		if tileN <= 0 {
			tileN = problemN
		}
		if tileK <= 0 {
			tileK = problemK
		}

		if tileM > problemM {
			tileM = problemM
		}
		if tileN > problemN {
			tileN = problemN
		}
		if tileK > problemK {
			tileK = problemK
		}
		if tileM > defaultRows {
			tileM = defaultRows
		}
		if tileN > defaultCols {
			tileN = defaultCols
		}
		if tileK > defaultCols {
			tileK = defaultCols
		}

		macs := int64(problemM) * int64(problemN) * int64(problemK)
		if macs <= 0 {
			macs = int64(problemM) * int64(problemN)
		}
		inputBytes := int64(problemM) * int64(problemK) * bytesPerF16
		weightBytes := int64(problemK) * int64(problemN) * bytesPerF16
		outputBytes := int64(problemM) * int64(problemN) * bytesPerF16

		desc.Kind = digital.TaskKindTileGemm
		desc.RequiresPe = true
		desc.ExecUnit = digital.ExecUnitPe
		desc.TileM = tileM
		desc.TileN = tileN
		desc.TileK = tileK
		desc.ProblemM = problemM
		desc.ProblemN = problemN
		desc.ProblemK = problemK
		desc.InputBytes = inputBytes
		desc.WeightBytes = weightBytes
		desc.OutputBytes = outputBytes
		desc.RegistersRd = tileK
		desc.RegistersWr = tileN
		scalarOps := macs
		if scalarOps > math.MaxInt32 {
			scalarOps = math.MaxInt32
		}
		if scalarOps < 0 {
			scalarOps = 0
		}
		desc.ScalarOps = int(scalarOps)
		desc.VectorOps = int(scalarOps)
	case "postprocess", "activation", "layernorm":
		elems := defaultRows
		if value, exists := payloadMap["elements"]; exists {
			if iv, ok := toInt(value); ok && iv > 0 {
				elems = iv
			}
		}
		if elems < 1 {
			elems = defaultRows
		}

		desc.Kind = digital.TaskKindElementwise
		desc.RequiresSpu = true
		desc.ExecUnit = digital.ExecUnitSpu
		desc.TileM = elems
		desc.TileN = 1
		desc.TileK = 1
		desc.ProblemM = elems
		desc.ProblemN = 1
		desc.ProblemK = 1
		desc.InputBytes = int64(elems * bytesPerF16)
		desc.OutputBytes = int64(elems * bytesPerF16)
		desc.ScalarOps = elems
		desc.VectorOps = elems
		desc.SpecialOps = elems / 8
		desc.RegistersRd = 32
		desc.RegistersWr = 16
	case "tokenize", "embedding":
		elements := defaultRows * defaultCols / (chipletID + 1)
		if value, exists := payloadMap["tokens"]; exists {
			if iv, ok := toInt(value); ok && iv > 0 {
				elements = iv
			}
		}
		if elements < 512 {
			elements = 512
		}

		desc.Kind = digital.TaskKindTokenPreprocess
		desc.RequiresSpu = true
		desc.ExecUnit = digital.ExecUnitSpu
		desc.ExecUnit = digital.ExecUnitSpu
		desc.TileM = elements
		desc.TileN = 1
		desc.TileK = 1
		desc.ProblemM = elements
		desc.ProblemN = 1
		desc.ProblemK = 1
		desc.InputBytes = int64(elements * bytesPerF16)
		desc.OutputBytes = int64(elements * bytesPerF16 / 2)
		desc.ScalarOps = elements
		desc.VectorOps = elements / 2
		desc.SpecialOps = elements / 16
		desc.RegistersRd = 16
		desc.RegistersWr = 8
	default:
		return nil
	}

	return desc
}

func (this *ChipletPlatform) buildDigitalDescriptorFromCommand(cmd *chiplet.CommandDescriptor, chipletID int) *digital.TaskDescriptor {
	if cmd == nil {
		return nil
	}

	stage := cmd.Kind.String()
	desc := &digital.TaskDescriptor{
		Description: stage,
		ExecUnit:    digital.ExecUnitUnknown,
	}

	defaultRows := 128
	defaultCols := 128
	if this.topology != nil {
		if this.topology.Digital.PeRows > 0 {
			defaultRows = this.topology.Digital.PeRows
		}
		if this.topology.Digital.PeCols > 0 {
			defaultCols = this.topology.Digital.PeCols
		}
	}

	problemM := int(cmd.Aux0)
	problemN := int(cmd.Aux1)
	problemK := int(cmd.Aux2)
	if problemM <= 0 {
		problemM = defaultRows
	}
	if problemN <= 0 {
		problemN = defaultCols
	}
	if problemK <= 0 {
		problemK = defaultRows
	}

	tileM := int(cmd.Queue)
	tileN := int(cmd.PayloadAddr)
	tileK := int(cmd.PayloadBytes)
	if tileM <= 0 {
		tileM = problemM
	}
	if tileN <= 0 {
		tileN = problemN
	}
	if tileK <= 0 {
		tileK = problemK
	}
	if cmd.Metadata != nil {
		tileM = firstPositive(metadataInt(cmd.Metadata, "tile_m", tileM), tileM)
		tileM = firstPositive(metadataInt(cmd.Metadata, "pe_tile_m", tileM), tileM)
		tileN = firstPositive(metadataInt(cmd.Metadata, "tile_n", tileN), tileN)
		tileN = firstPositive(metadataInt(cmd.Metadata, "pe_tile_n", tileN), tileN)
		tileK = firstPositive(metadataInt(cmd.Metadata, "tile_k", tileK), tileK)
		tileK = firstPositive(metadataInt(cmd.Metadata, "pe_tile_k", tileK), tileK)
		problemM = firstPositive(metadataInt(cmd.Metadata, "problem_m", problemM), problemM)
		problemN = firstPositive(metadataInt(cmd.Metadata, "problem_n", problemN), problemN)
		problemK = firstPositive(metadataInt(cmd.Metadata, "problem_k", problemK), problemK)
	}
	if tileM > problemM {
		tileM = problemM
	}
	if tileN > problemN {
		tileN = problemN
	}
	if tileK > problemK {
		tileK = problemK
	}
	if tileM > defaultRows {
		tileM = defaultRows
	}
	if tileN > defaultCols {
		tileN = defaultCols
	}
	if tileK > defaultCols {
		tileK = defaultCols
	}

	const bytesPerF16 = 2
	inputBytes := int64(problemM) * int64(problemK) * bytesPerF16
	weightBytes := int64(problemK) * int64(problemN) * bytesPerF16
	outputBytes := int64(problemM) * int64(problemN) * bytesPerF16

	desc.Kind = digital.TaskKindTileGemm
	desc.RequiresPe = true
	desc.ExecUnit = digital.ExecUnitPe
	desc.TileM = tileM
	desc.TileN = tileN
	desc.TileK = tileK
	desc.ProblemM = problemM
	desc.ProblemN = problemN
	desc.ProblemK = problemK
	desc.InputBytes = inputBytes
	desc.WeightBytes = weightBytes
	desc.OutputBytes = outputBytes

	scalarOps := int64(problemM) * int64(problemN) * int64(problemK)
	if scalarOps <= 0 {
		scalarOps = int64(problemM) * int64(problemN)
	}
	if scalarOps > math.MaxInt32 {
		scalarOps = math.MaxInt32
	}
	if scalarOps < 0 {
		scalarOps = 0
	}
	desc.ScalarOps = int(scalarOps)
	desc.VectorOps = int(scalarOps)
	desc.PeConcurrency = metadataInt(cmd.Metadata, "pe_concurrency", metadataInt(cmd.Metadata, "pe_parallel", 0))

	switch cmd.Kind {
	case chiplet.CommandKindPeAttentionHead:
		desc.Description = "attention"
	case chiplet.CommandKindPeElementwise:
		desc.Description = "elementwise"
		desc.RequiresPe = false
		desc.RequiresSpu = true
		desc.ExecUnit = digital.ExecUnitSpu
		elements := int64(problemM) * int64(problemN)
		if elements <= 0 {
			elements = int64(problemM)
		}
		if elements <= 0 {
			elements = 1
		}
		vectorOps := elements
		if vectorOps > math.MaxInt32 {
			vectorOps = math.MaxInt32
		}
		desc.ScalarOps = 0
		desc.VectorOps = int(vectorOps)
		desc.SpecialOps = desc.VectorOps / 32
	case chiplet.CommandKindPeTokenPrep:
		desc.Description = "tokenize"
		desc.RequiresPe = false
		desc.RequiresSpu = true
		desc.ExecUnit = digital.ExecUnitSpu
		scalarOps = int64(problemM) * int64(problemN)
		if scalarOps > math.MaxInt32 {
			scalarOps = math.MaxInt32
		}
		if scalarOps < 0 {
			scalarOps = 0
		}
		desc.ScalarOps = int(scalarOps)
		desc.VectorOps = desc.ScalarOps / 2
		desc.SpecialOps = desc.ScalarOps / 16
	case chiplet.CommandKindPeSpuOp:
		desc.Description = "spu_op"
		desc.Kind = digital.TaskKindSpuOp
		desc.RequiresPe = false
		desc.RequiresSpu = true
		desc.ExecUnit = digital.ExecUnitSpu
		desc.ScalarOps = firstPositive(metadataInt(cmd.Metadata, "scalar_ops", desc.ScalarOps), desc.ScalarOps)
		desc.VectorOps = firstPositive(metadataInt(cmd.Metadata, "vector_ops", desc.VectorOps), desc.VectorOps)
		desc.SpecialOps = firstPositive(metadataInt(cmd.Metadata, "special_ops", desc.SpecialOps), desc.SpecialOps)
	case chiplet.CommandKindPeVpuOp:
		desc.Description = "vpu_op"
		desc.Kind = digital.TaskKindVpuOp
		desc.RequiresPe = false
		desc.RequiresSpu = false
		desc.RequiresVpu = true
		desc.ExecUnit = digital.ExecUnitVpu
		desc.ScalarOps = 0
		desc.VectorOps = firstPositive(metadataInt(cmd.Metadata, "vector_ops", desc.VectorOps), desc.VectorOps)
		desc.SpecialOps = 0
	case chiplet.CommandKindPeReduce:
		desc.Description = "reduce"
		desc.Kind = digital.TaskKindReduction
		desc.RequiresPe = false
		desc.RequiresSpu = true
		desc.ExecUnit = digital.ExecUnitSpu
		topK := firstPositive(int(cmd.SubOp), metadataInt(cmd.Metadata, "top_k", 1))
		if topK < 1 {
			topK = 1
		}
		tokens := problemM
		totalOps := tokens * topK
		reduceOps := metadataInt(cmd.Metadata, "reduce_ops", totalOps)
		if reduceOps > 0 {
			totalOps = reduceOps
		}
		if totalOps < 1 {
			totalOps = 1
		}
		if totalOps > math.MaxInt32 {
			totalOps = math.MaxInt32
		}
		desc.ScalarOps = totalOps
		desc.VectorOps = 0
		desc.SpecialOps = 0
	case chiplet.CommandKindPeBufferAlloc, chiplet.CommandKindPeBufferRelease:
		if cmd.Kind == chiplet.CommandKindPeBufferAlloc {
			desc.Kind = digital.TaskKindBufferAlloc
			desc.Description = "buffer_alloc"
		} else {
			desc.Kind = digital.TaskKindBufferRelease
			desc.Description = "buffer_release"
		}
		desc.RequiresPe = false
		desc.RequiresSpu = false
		desc.ExecUnit = digital.ExecUnitBuffer
		bufferName := "scratch"
		if cmd.Metadata != nil {
			if value, ok := cmd.Metadata["buffer"].(string); ok {
				trimmed := strings.TrimSpace(value)
				if trimmed != "" {
					bufferName = trimmed
				}
			}
			if value, ok := cmd.Metadata["target"].(string); ok {
				trimmed := strings.TrimSpace(value)
				if trimmed != "" {
					bufferName = trimmed
				}
			}
		}
		desc.TargetBuffer = bufferName
		desc.BufferBytes = int64(metadataInt(cmd.Metadata, "bytes", int(desc.OutputBytes)))
		if desc.BufferBytes <= 0 {
			desc.BufferBytes = int64(cmd.PayloadBytes)
		}
		desc.InputBytes = 0
		desc.WeightBytes = 0
		desc.OutputBytes = 0
	case chiplet.CommandKindPeBarrier:
		desc.Description = "barrier"
		desc.Kind = digital.TaskKindBarrier
		desc.RequiresPe = false
		desc.RequiresSpu = false
		desc.ExecUnit = digital.ExecUnitBarrier
		desc.InputBytes = 0
		desc.WeightBytes = 0
		desc.OutputBytes = 0
	case chiplet.CommandKindPeGemm:
		desc.Description = "gemm"
	default:
		// leave defaults
	}

	if cmd.Metadata != nil {
		switch strings.ToLower(metadataString(cmd.Metadata, "op", "")) {
		case "moe_gating_scores":
			rows := firstPositive(int(cmd.Aux0), metadataInt(cmd.Metadata, "tokens", problemM))
			features := firstPositive(int(cmd.Aux1), metadataInt(cmd.Metadata, "features", problemN))
			topK := firstPositive(int(cmd.Aux2), metadataInt(cmd.Metadata, "top_k", 2))
			if rows <= 0 {
				rows = problemM
			}
			if features <= 0 {
				features = problemN
			}
			if features <= 0 {
				features = rows
			}
			if topK <= 0 {
				topK = 2
			}

			desc.Description = "moe_gating_scores"
			desc.Kind = digital.TaskKindSpuOp
			desc.RequiresPe = false
			desc.RequiresSpu = true
			desc.RequiresVpu = false
			desc.ExecUnit = digital.ExecUnitSpu
			desc.ProblemM = rows
			desc.ProblemN = features
			desc.ProblemK = features
			desc.TileM = firstPositive(metadataInt(cmd.Metadata, "tile_m", rows), rows)
			desc.TileN = firstPositive(metadataInt(cmd.Metadata, "tile_n", features), features)
			desc.TileK = firstPositive(metadataInt(cmd.Metadata, "tile_k", features), features)

			inputBytes := metadataInt(cmd.Metadata, "activation_bytes", rows*features*bytesPerF16)
			outputBytes := metadataInt(cmd.Metadata, "output_bytes", rows*topK*bytesPerF16)
			if outputBytes <= 0 {
				outputBytes = rows * features * bytesPerF16
			}
			desc.InputBytes = int64(inputBytes)
			desc.OutputBytes = int64(outputBytes)
			if desc.InputBytes <= 0 {
				desc.InputBytes = int64(rows * features * bytesPerF16)
			}
			if desc.OutputBytes <= 0 {
				desc.OutputBytes = int64(rows * topK * bytesPerF16)
			}
			desc.WeightBytes = 0
			desc.ScalarOps = firstPositive(metadataInt(cmd.Metadata, "scalar_ops", desc.ScalarOps), desc.ScalarOps)
			desc.VectorOps = firstPositive(metadataInt(cmd.Metadata, "vector_ops", desc.VectorOps), desc.VectorOps)
			if desc.ScalarOps <= 0 && desc.VectorOps <= 0 {
				desc.ScalarOps = rows * features
				desc.VectorOps = rows * features / 2
			}
			desc.TargetBuffer = metadataString(cmd.Metadata, "target_buffer", "scratch")
			desc.RegistersRd = problemK
			desc.RegistersWr = problemN
		case "topk_select":
			desc.Description = "topk_select"
			desc.Kind = digital.TaskKindReduction
			desc.RequiresPe = false
			desc.RequiresSpu = true
			desc.ExecUnit = digital.ExecUnitSpu
			tokens := firstPositive(metadataInt(cmd.Metadata, "tokens", problemM), problemM)
			topK := firstPositive(metadataInt(cmd.Metadata, "top_k", problemN), problemN)
			if tokens <= 0 {
				tokens = problemM
			}
			if topK <= 0 {
				topK = 1
			}
			totalOps := tokens * topK
			totalOps = firstPositive(metadataInt(cmd.Metadata, "reduce_ops", totalOps), totalOps)
			if totalOps > math.MaxInt32 {
				totalOps = math.MaxInt32
			}
			desc.ScalarOps = totalOps
			desc.VectorOps = 0
			desc.ProblemM = tokens
			desc.ProblemN = topK
			desc.ProblemK = topK
			inputBytes := metadataInt(cmd.Metadata, "activation_bytes", tokens*problemN*bytesPerF16)
			outputBytes := metadataInt(cmd.Metadata, "output_bytes", tokens*topK*bytesPerF16)
			desc.InputBytes = int64(inputBytes)
			desc.OutputBytes = int64(outputBytes)
			desc.WeightBytes = 0
			desc.TargetBuffer = metadataString(cmd.Metadata, "target_buffer", "scratch")
		}
	}

	desc.RegistersRd = problemK
	desc.RegistersWr = problemN

	return desc
}

func (this *ChipletPlatform) buildRramTaskSpec(task *chiplet.Task) *rram.TaskSpec {
	if task == nil {
		return nil
	}

	if desc, ok := task.Payload.(*chiplet.CommandDescriptor); ok && desc != nil {
		return this.buildRramSpecFromCommand(desc)
	}

	payloadMap, ok := task.Payload.(map[string]interface{})
	if !ok {
		return nil
	}

	spec := new(rram.TaskSpec)
	used := false

	var signs []int
	var exponents []int
	var mantissas []int
	var hasFPComponents bool

	if value, exists := payloadMap["activation_bits"]; exists {
		if iv, ok := toInt(value); ok {
			spec.ActivationBits = iv
			used = true
		}
	}
	if value, exists := payloadMap["slice_bits"]; exists {
		if iv, ok := toInt(value); ok {
			spec.SliceBits = iv
			used = true
		}
	}
	if value, exists := payloadMap["pulse_count"]; exists {
		if iv, ok := toInt(value); ok {
			spec.PulseCount = iv
			used = true
		}
	}
	if value, exists := payloadMap["pulses"]; exists {
		if iv, ok := toInt(value); ok {
			spec.PulseCount = iv
			used = true
		}
	}
	if value, exists := payloadMap["adc_samples"]; exists {
		if iv, ok := toInt(value); ok {
			spec.AdcSamples = iv
			used = true
		}
	}
	if value, exists := payloadMap["pre_cycles"]; exists {
		if iv, ok := toInt(value); ok {
			spec.PreCycles = iv
			used = true
		}
	}
	if value, exists := payloadMap["post_cycles"]; exists {
		if iv, ok := toInt(value); ok {
			spec.PostCycles = iv
			used = true
		}
	}
	if value, exists := payloadMap["scale"]; exists {
		if fv, ok := toFloat(value); ok {
			spec.Scale = fv
			used = true
		}
	}
	if value, exists := payloadMap["zero_point"]; exists {
		if iv, ok := toInt(value); ok {
			spec.ZeroPoint = iv
			used = true
		}
	}
	if value, exists := payloadMap["activation_size"]; exists {
		if iv, ok := toInt(value); ok {
			spec.ActivationSize = iv
			used = true
		}
	}
	if value, exists := payloadMap["weight_size"]; exists {
		if iv, ok := toInt(value); ok {
			spec.WeightSize = iv
			used = true
		}
	}
	if value, exists := payloadMap["tile_id"]; exists {
		if iv, ok := toInt(value); ok {
			spec.WeightTile = iv
			used = true
		}
	}
	if value, exists := payloadMap["array_id"]; exists {
		if iv, ok := toInt(value); ok {
			spec.WeightArray = iv
			used = true
		}
	}
	if value, exists := payloadMap["weight_tag"]; exists {
		if sv, ok := value.(string); ok {
			spec.WeightTag = strings.ToLower(sv)
			used = true
		}
	}
	if value, exists := payloadMap["rows"]; exists {
		if iv, ok := toInt(value); ok {
			spec.Rows = iv
			used = true
		}
	}
	if value, exists := payloadMap["cols"]; exists {
		if iv, ok := toInt(value); ok {
			spec.Cols = iv
			used = true
		}
	}
	if value, exists := payloadMap["depth"]; exists {
		if iv, ok := toInt(value); ok {
			spec.Depth = iv
			used = true
		}
	}
	if value, exists := payloadMap["error_abs"]; exists {
		if fv, ok := toFloat(value); ok {
			spec.ErrorAbs = math.Abs(fv)
			used = true
		}
	}
	if value, exists := payloadMap["signs"]; exists {
		if slice, ok := toIntSlice(value); ok {
			signs = slice
			hasFPComponents = true
			used = true
		}
	}
	if value, exists := payloadMap["exponents"]; exists {
		if slice, ok := toIntSlice(value); ok {
			exponents = slice
			hasFPComponents = true
			used = true
		}
	}
	if value, exists := payloadMap["mantissas"]; exists {
		if slice, ok := toIntSlice(value); ok {
			mantissas = slice
			hasFPComponents = true
			used = true
		}
	}
	phaseLabel := ""
	if value, exists := payloadMap["phase"]; exists {
		if sv, ok := value.(string); ok {
			phaseLabel = strings.ToLower(sv)
			used = true
		}
	}
	if phaseLabel == "" {
		if value, exists := payloadMap["stage"]; exists {
			if sv, ok := value.(string); ok {
				phaseLabel = strings.ToLower(sv)
				used = true
			}
		}
	}
	if value, exists := payloadMap["i_sum"]; exists {
		if iv, ok := toInt64(value); ok {
			spec.ISum = iv
			used = true
		}
	}
	if value, exists := payloadMap["p_sum"]; exists {
		if iv, ok := toInt64(value); ok {
			spec.PSum = iv
			used = true
		}
	}
	if value, exists := payloadMap["max_exponent"]; exists {
		if iv, ok := toInt(value); ok {
			spec.MaxExponent = iv
			used = true
		}
	}
	if value, exists := payloadMap["a_sum"]; exists {
		if fv, ok := toFloat(value); ok {
			spec.ASum = fv
			used = true
		}
	}
	if value, exists := payloadMap["expected"]; exists {
		if fv, ok := toFloat(value); ok {
			spec.Expected = fv
			spec.HasExpected = true
			used = true
		}
	}

	if !used {
		return nil
	}

	if hasFPComponents {
		var pre *rram.Preprocessor
		if len(this.rramChiplets) > 0 && this.rramChiplets[0].Preprocess != nil {
			pre = this.rramChiplets[0].Preprocess
		} else {
			pre = rram.NewPreprocessor(spec.ActivationBits, spec.SliceBits)
		}
		if len(signs) > 0 && len(signs) == len(exponents) && len(signs) == len(mantissas) {
			_, maxExp, pSum, aSum := pre.Prepare(signs, exponents, mantissas)
			spec.MaxExponent = maxExp
			spec.PSum = int64(pSum)
			spec.ASum = aSum
			if spec.ISum == 0 {
				spec.ISum = spec.PSum
			}
		}
	}

	switch phaseLabel {
	case "stage", "stage_act", "rram_stage":
		spec.Phase = rram.TaskPhaseStage
	case "execute", "rram_execute":
		spec.Phase = rram.TaskPhaseExecute
	case "post", "rram_post":
		spec.Phase = rram.TaskPhasePost
	}

	return spec
}

func (this *ChipletPlatform) buildRramSpecFromCommand(cmd *chiplet.CommandDescriptor) *rram.TaskSpec {
	if cmd == nil {
		return nil
	}

	rows := int(cmd.Aux0)
	cols := int(cmd.Aux1)
	depth := int(cmd.Aux2)
	if rows <= 0 {
		rows = 128
	}
	if cols <= 0 {
		cols = 128
	}
	if depth <= 0 {
		depth = rows
	}

	activationBytes := int(cmd.PayloadBytes)
	if activationBytes <= 0 {
		activationBytes = rows * depth * 2
	}
	weightBytes := int(cmd.PayloadAddr)
	if weightBytes <= 0 {
		weightBits := depth * cols * 4
		weightBytes = (weightBits + 7) / 8
	}
	outputBytes := int(cmd.Aux3)
	if outputBytes <= 0 {
		outputBytes = rows * cols * 2
	}

	pulseCount := depth
	if pulseCount <= 0 {
		pulseCount = rows
	}
	adcSamples := cols * depth
	if adcSamples <= 0 {
		adcSamples = cols
	}
	preCycles := rows
	if preCycles <= 0 {
		preCycles = 1
	}
	postCycles := cols / 8
	if postCycles <= 0 {
		postCycles = 4
	}

	spec := new(rram.TaskSpec)
	spec.ActivationBits = 12
	spec.SliceBits = 2
	spec.PulseCount = pulseCount
	spec.AdcSamples = adcSamples
	spec.PreCycles = preCycles
	spec.PostCycles = postCycles
	spec.Scale = 1.0
	spec.ZeroPoint = 0
	spec.ActivationSize = activationBytes
	spec.WeightSize = weightBytes
	spec.OutputSize = outputBytes
	spec.Rows = rows
	spec.Cols = cols
	spec.Depth = depth
	if cmd.Metadata != nil {
		spec.WeightTile = metadataInt(cmd.Metadata, "tile_id", spec.WeightTile)
		spec.WeightArray = metadataInt(cmd.Metadata, "array_id", spec.WeightArray)
		spec.WeightTag = metadataString(cmd.Metadata, "weight_tag", spec.WeightTag)
	}
	if spec.WeightTile == 0 {
		spec.WeightTile = int(cmd.Queue)
	}
	if spec.WeightTag == "" {
		spec.WeightTag = fmt.Sprintf("cmd_%d", cmd.ID)
	}
	spec.ASum = 0
	spec.Expected = 0
	spec.HasExpected = false
	switch cmd.Kind {
	case chiplet.CommandKindRramStageAct:
		spec.Phase = rram.TaskPhaseStage
	case chiplet.CommandKindRramExecute:
		spec.Phase = rram.TaskPhaseExecute
	case chiplet.CommandKindRramPost:
		spec.Phase = rram.TaskPhasePost
	default:
		spec.Phase = rram.TaskPhaseUnknown
	}
	return spec
}

func (this *ChipletPlatform) isTargetBusy(task *chiplet.Task) bool {
	if task == nil {
		return false
	}

	switch task.Target {
	case chiplet.TaskTargetDigital:
		chipletID, ok := extractChipletID(task.Payload)
		if ok && chipletID >= 0 && chipletID < len(this.digitalChiplets) {
			chip := this.digitalChiplets[chipletID]
			if chip == nil {
				return false
			}
			limit := chip.PendingCapacity()
			if limit <= 0 {
				limit = 1
			}
			return chip.PendingTasks >= limit
		}
	case chiplet.TaskTargetRram:
		chipletID, ok := extractChipletID(task.Payload)
		if ok && chipletID >= 0 && chipletID < len(this.rramChiplets) {
			chip := this.rramChiplets[chipletID]
			if chip == nil {
				return false
			}
			limit := chip.PendingCapacity()
			if limit <= 0 {
				limit = 1
			}
			return chip.PendingTasks >= limit
		}
	}

	return false
}

func (this *ChipletPlatform) recordDeferral(task *chiplet.Task) {
	if task == nil {
		return
	}

	switch task.Target {
	case chiplet.TaskTargetDigital:
		if id, ok := extractChipletID(task.Payload); ok && id >= 0 && id < len(this.digitalDeferrals) {
			this.digitalDeferrals[id]++
		}
	case chiplet.TaskTargetRram:
		if id, ok := extractChipletID(task.Payload); ok && id >= 0 && id < len(this.rramDeferrals) {
			this.rramDeferrals[id]++
		}
	}
}

func (this *ChipletPlatform) logCycleMetrics(cycleDeferrals int) {
	if this.binDirpath == "" {
		return
	}

	waitSamples := int64(0)
	waitTotal := int64(0)
	if this.statFactory != nil {
		waitSamples = this.statFactory.Value("task_wait_samples")
		waitTotal = this.statFactory.Value("task_wait_cycles_total")
	}

	avgWait := 0.0
	if waitSamples > 0 {
		avgWait = float64(waitTotal) / float64(waitSamples)
	}

	totalDigitalBusy := 0
	for _, chiplet := range this.digitalChiplets {
		totalDigitalBusy += chiplet.BusyCycles
	}

	totalRramBusy := 0
	for _, chiplet := range this.rramChiplets {
		totalRramBusy += chiplet.BusyCycles
	}

	digitalUtil := 0.0
	if this.currentCycle > 0 && len(this.digitalChiplets) > 0 {
		digitalUtil = float64(totalDigitalBusy) / float64(len(this.digitalChiplets)*this.currentCycle)
	}

	rramUtil := 0.0
	if this.currentCycle > 0 && len(this.rramChiplets) > 0 {
		rramUtil = float64(totalRramBusy) / float64(len(this.rramChiplets)*this.currentCycle)
	}

	hostTasks := this.executedHostTasks
	outstandingDigital := int64(0)
	outstandingRram := int64(0)
	outstandingTransfer := int64(0)
	outstandingDma := int64(0)
	if this.orchestrator != nil {
		tracker := this.orchestrator.Outstanding()
		outstandingDigital = tracker.Digital
		outstandingRram = tracker.Rram
		outstandingTransfer = tracker.Transfer
		outstandingDma = tracker.Dma
	}

	entry := fmt.Sprintf("%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%.2f,%.4f,%.4f,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d",
		this.currentCycle,
		this.cycleDigitalExec,
		this.cycleDigitalCompleted,
		this.cycleRramExec,
		this.cycleTransferExec,
		this.cycleTransferBytes,
		this.cycleTransferHops,
		this.cycleHostDmaLoadBytes,
		this.cycleHostDmaStoreBytes,
		this.cycleKvHits,
		this.cycleKvMisses,
		this.cycleKvLoadBytes,
		this.cycleKvStoreBytes,
		this.cycleDigitalLoadBytes,
		this.cycleDigitalStoreBytes,
		this.cycleDigitalPeActive,
		this.cycleDigitalSpuActive,
		this.cycleDigitalVpuActive,
		this.transferThrottleUntil,
		this.cycleThrottleEvents,
		cycleDeferrals,
		avgWait,
		digitalUtil,
		rramUtil,
		this.lastDigitalTicks,
		this.lastRramTicks,
		this.lastInterconnectTicks,
		hostTasks,
		outstandingDigital,
		outstandingRram,
		outstandingTransfer,
		outstandingDma,
		this.totalTransferToRramBytes,
		this.totalTransferToDigitalBytes,
		this.totalTransferHostLoadBytes,
		this.totalTransferHostStoreBytes,
		this.transferThrottleEventsTotal,
		this.transferThrottleCyclesTotal,
	)

	this.cycleLog = append(this.cycleLog, entry)
}

func toInt(value interface{}) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return parsed, true
		}
	default:
		return 0, false
	}
	return 0, false
}

func toInt64(value interface{}) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case string:
		if parsed, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return parsed, true
		}
	default:
		return 0, false
	}
	return 0, false
}

func toFloat(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case float32:
		return float64(v), true
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func toFloatSlice(value interface{}) ([]float64, bool) {
	switch v := value.(type) {
	case []float64:
		return append([]float64(nil), v...), true
	case []interface{}:
		slice := make([]float64, 0, len(v))
		for _, item := range v {
			fv, ok := toFloat(item)
			if !ok {
				return nil, false
			}
			slice = append(slice, fv)
		}
		return slice, true
	case string:
		text := strings.TrimSpace(v)
		if text == "" {
			return nil, false
		}
		parts := strings.Split(text, ",")
		slice := make([]float64, 0, len(parts))
		for _, part := range parts {
			fv, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
			if err != nil {
				return nil, false
			}
			slice = append(slice, fv)
		}
		return slice, true
	default:
		return nil, false
	}
}

func toIntSlice(value interface{}) ([]int, bool) {
	switch v := value.(type) {
	case []int:
		return append([]int(nil), v...), true
	case []interface{}:
		slice := make([]int, 0, len(v))
		for _, item := range v {
			iv, ok := toInt(item)
			if !ok {
				return nil, false
			}
			slice = append(slice, iv)
		}
		return slice, true
	case string:
		text := strings.TrimSpace(v)
		if text == "" {
			return nil, false
		}
		parts := strings.Split(text, ",")
		slice := make([]int, 0, len(parts))
		for _, part := range parts {
			iv, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil {
				return nil, false
			}
			slice = append(slice, iv)
		}
		return slice, true
	default:
		return nil, false
	}
}

func metadataString(meta map[string]interface{}, key, fallback string) string {
	if meta == nil {
		return fallback
	}
	if value, ok := meta[key]; ok {
		if text, ok := value.(string); ok {
			trimmed := strings.TrimSpace(text)
			if trimmed != "" {
				return trimmed
			}
		}
	}
	return fallback
}

func deriveWeightKey(cmd *chiplet.CommandDescriptor, spec *rram.TaskSpec) (int, int, string) {
	tileID := 0
	arrayID := 0
	tag := ""
	if spec != nil {
		tileID = spec.WeightTile
		arrayID = spec.WeightArray
		tag = spec.WeightTag
	}
	if cmd != nil {
		if tileID == 0 {
			tileID = int(cmd.Queue)
		}
		if cmd.Metadata != nil {
			tileID = metadataInt(cmd.Metadata, "tile_id", tileID)
			arrayID = metadataInt(cmd.Metadata, "array_id", arrayID)
			tag = metadataString(cmd.Metadata, "weight_tag", tag)
		}
	}
	if tag == "" {
		tag = fmt.Sprintf("tile%d_array%d", tileID, arrayID)
	}
	return tileID, arrayID, strings.ToLower(tag)
}

func estimateWeightBytes(spec *rram.TaskSpec) int64 {
	if spec == nil {
		return 0
	}
	if spec.WeightSize > 0 {
		return int64(spec.WeightSize)
	}
	rows := spec.Rows
	cols := spec.Cols
	depth := spec.Depth
	if rows <= 0 || cols <= 0 || depth <= 0 {
		return 0
	}
	bitsPerWeight := 4
	weightBits := depth * cols * bitsPerWeight
	if weightBits <= 0 {
		return 0
	}
	return int64((weightBits + 7) / 8)
}
