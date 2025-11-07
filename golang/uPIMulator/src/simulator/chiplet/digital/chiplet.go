package digital

import (
	"fmt"
	"math"
	"strings"
)

// TaskKind enumerates the coarse operations that a digital chiplet can
// execute. The list captures GEMM-style systolic tiles as well as SPU-driven
// element-wise phases. A legacy kind is provided for callers that only know
// about aggregate latency (backwards compatibility with Phase 2 behaviour).
type TaskKind int

const (
	TaskKindTileGemm TaskKind = iota
	TaskKindElementwise
	TaskKindReduction
	TaskKindTokenPreprocess
	TaskKindSpuOp
	TaskKindVpuOp
	TaskKindBufferAlloc
	TaskKindBufferRelease
	TaskKindBarrier
	TaskKindLegacy
)

type ExecUnit int

const (
	ExecUnitUnknown ExecUnit = iota
	ExecUnitPe
	ExecUnitSpu
	ExecUnitVpu
	ExecUnitBuffer
	ExecUnitBarrier
)

// TaskDescriptor captures the minimal information required to instantiate a
// digital task. Higher levels (Host orchestrator) convert DAG nodes into this
// descriptor before handing them to the chiplet.
type TaskDescriptor struct {
	Kind        TaskKind
	Description string
	ExecUnit    ExecUnit

	ProblemM int
	ProblemN int
	ProblemK int

	TileM int
	TileN int
	TileK int

	InputBytes       int64
	WeightBytes      int64
	OutputBytes      int64
	ScalarOps        int
	VectorOps        int
	SpecialOps       int
	VpuOps           int
	RegistersRd      int
	RegistersWr      int
	RequiresSpu      bool
	RequiresVpu      bool
	RequiresPe       bool
	PreferredCluster int
	TargetBuffer     string
	BufferBytes      int64
	PeConcurrency    int
}

type taskPhase int

const (
	taskPhaseLoad taskPhase = iota
	taskPhaseCompute
	taskPhaseStore
	taskPhaseSpu
	taskPhaseVpu
	taskPhaseComplete
)

type digitalTask struct {
	id              int
	clusterID       int
	kind            TaskKind
	description     string
	execUnit        ExecUnit
	activationBytes int64
	weightBytes     int64
	outputBytes     int64

	loadRemaining    int
	computeRemaining int
	storeRemaining   int
	spuRemaining     int
	vpuRemaining     int
	currentPhase     taskPhase
	requiresPe       bool
	requiresSpu      bool
	requiresVpu      bool

	peCyclesPerTile      int
	peWaveArrays         []int
	peWaveIndex          int
	peWaveCycle          int
	computeCycleConsumed bool
	macCount             int64
	peConcurrency        int

	spuActiveClusters int
	spuCycleConsumed  bool
	scalarOps         int
	vectorOps         int
	specialOps        int
	vpuOps            int
	registersRd       int
	registersWr       int
	vpuActiveUnits    int
	vpuCycleConsumed  bool
	totalLoadBytes    int64
	totalStoreBytes   int64
	prefetchBytes     int64
	writebackBytes    int64
	loadProgress      int64
	storeProgress     int64
	prefetchActive    bool
	writebackActive   bool
	targetBuffer      string
	storeBuffer       string
	bufferBytes       int64
}

type computeCluster struct {
	id                      int
	peArrays                []PEArray
	spuClusters             []SPUCluster
	vpuUnits                []VPUUnit
	buffers                 map[string]*Buffer
	bufferOrder             []string
	waitingPe               []*digitalTask
	waitingSpu              []*digitalTask
	waitingVpu              []*digitalTask
	waitingBuffer           []*digitalTask
	waitingBarrier          []*digitalTask
	waitingMisc             []*digitalTask
	loadActive              []*digitalTask
	computeActive           []*digitalTask
	storeActive             []*digitalTask
	spuActive               []*digitalTask
	vpuActive               []*digitalTask
	peRotation              int
	vpuRotation             int
	pendingCycles           int
	executedTasks           int
	totalMacs               int64
	spuScalarOps            int64
	spuVectorOps            int64
	spuSpecialOps           int64
	vpuVectorOps            int64
	peBusyCycles            []int64
	spuBusyCycles           int64
	spuClusterBusy          []int64
	vpuBusyCycles           int64
	peOffset                int
	spuOffset               int
	vpuOffset               int
	loadBandwidth           int64
	storeBandwidth          int64
	loadBytesThisCycle      int64
	storeBytesThisCycle     int64
	peActiveThisCycle       int
	spuActiveThisCycle      int
	vpuActiveThisCycle      int
	tasksCompletedThisCycle int
	totalLoadBytes          int64
	totalStoreBytes         int64
	parent                  *Chiplet
}

const debugDigitalEventLimit = 50

var debugDigitalEvents int

func newComputeClusterWithOffsets(
	id int,
	peCount int,
	peRows int,
	peCols int,
	spuCount int,
	activationBuffer int64,
	scratchBuffer int64,
	peOffset int,
	spuOffset int,
	vpuOffset int,
	parent *Chiplet,
	params Parameters,
) *computeCluster {
	if peCount <= 0 {
		peCount = 1
	}
	if spuCount <= 0 {
		spuCount = 1
	}

	peArrays := make([]PEArray, 0, peCount)
	for i := 0; i < peCount; i++ {
		peArrays = append(peArrays, NewPEArray(peRows, peCols))
	}

	spuClusters := make([]SPUCluster, 0, spuCount)
	for i := 0; i < spuCount; i++ {
		spuClusters = append(spuClusters, NewSPUCluster(2, 2, 128, true))
	}

	vpuCount := params.Vpu.UnitsPerCluster
	if vpuCount <= 0 {
		vpuCount = len(spuClusters)
		if vpuCount <= 0 {
			vpuCount = 1
		}
	}
	vpuUnits := make([]VPUUnit, 0, vpuCount)
	for i := 0; i < vpuCount; i++ {
		vpuUnits = append(vpuUnits, NewVPUUnit(params.Vpu.VectorLanes, params.Vpu.IssueWidth, params.Vpu.LatencyCycles))
	}

	loadBW := params.PeArray.LoadBandwidthBytesPerCycle
	if loadBW <= 0 {
		loadBW = 1024
	}
	storeBW := params.PeArray.StoreBandwidthBytesPerCycle
	if storeBW <= 0 {
		storeBW = 1024
	}

	buffers := map[string]*Buffer{
		"activation": NewBuffer(fmt.Sprintf("Cluster%dActivation", id), activationBuffer, loadBW),
		"weights":    NewBuffer(fmt.Sprintf("Cluster%dWeights", id), activationBuffer, loadBW),
		"scratch":    NewBuffer(fmt.Sprintf("Cluster%dScratch", id), scratchBuffer, storeBW),
	}

	return &computeCluster{
		id:             id,
		peArrays:       peArrays,
		spuClusters:    spuClusters,
		vpuUnits:       vpuUnits,
		buffers:        buffers,
		bufferOrder:    []string{"activation", "weights", "scratch"},
		waitingPe:      make([]*digitalTask, 0),
		waitingSpu:     make([]*digitalTask, 0),
		waitingVpu:     make([]*digitalTask, 0),
		waitingBuffer:  make([]*digitalTask, 0),
		waitingBarrier: make([]*digitalTask, 0),
		waitingMisc:    make([]*digitalTask, 0),
		loadActive:     make([]*digitalTask, 0),
		computeActive:  make([]*digitalTask, 0),
		storeActive:    make([]*digitalTask, 0),
		spuActive:      make([]*digitalTask, 0),
		vpuActive:      make([]*digitalTask, 0),
		peRotation:     0,
		vpuRotation:    0,
		pendingCycles:  0,
		executedTasks:  0,
		totalMacs:      0,
		spuScalarOps:   0,
		spuVectorOps:   0,
		spuSpecialOps:  0,
		peBusyCycles:   make([]int64, len(peArrays)),
		spuBusyCycles:  0,
		spuClusterBusy: make([]int64, len(spuClusters)),
		vpuBusyCycles:  0,
		peOffset:       peOffset,
		spuOffset:      spuOffset,
		vpuOffset:      vpuOffset,
		loadBandwidth:  loadBW,
		storeBandwidth: storeBW,
		parent:         parent,
	}
}

func (cluster *computeCluster) buffer(name string) *Buffer {
	if cluster.buffers == nil {
		return nil
	}
	return cluster.buffers[strings.ToLower(name)]
}

func (cluster *computeCluster) bufferCapacity(name string) int64 {
	if buf := cluster.buffer(name); buf != nil {
		return buf.Capacity()
	}
	return 0
}

func (cluster *computeCluster) bufferUsage(name string) int64 {
	if buf := cluster.buffer(name); buf != nil {
		return buf.Occupancy()
	}
	return 0
}

func (cluster *computeCluster) freeCapacity(name string) int64 {
	buf := cluster.buffer(name)
	if buf == nil {
		return 0
	}
	return buf.Capacity() - buf.Occupancy()
}

func (cluster *computeCluster) canAcceptDescriptor(desc *TaskDescriptor) bool {
	if desc == nil {
		return false
	}
	if desc.InputBytes > cluster.bufferCapacity("activation") {
		return false
	}
	if desc.WeightBytes > cluster.bufferCapacity("weights") {
		return false
	}
	if desc.OutputBytes > cluster.bufferCapacity("scratch") {
		return false
	}
	return true
}

func (cluster *computeCluster) processLoad(chiplet *Chiplet) bool {
	if len(cluster.loadActive) == 0 {
		return false
	}

	bandwidth := cluster.loadBandwidth
	if bandwidth <= 0 {
		bandwidth = 2048
	}
	remaining := int64(bandwidth)
	progress := false
	bytesTransferred := int64(0)

	next := cluster.loadActive[:0]
	for _, task := range cluster.loadActive {
		if remaining <= 0 {
			next = append(next, task)
			continue
		}
		if debugDigitalEvents < debugDigitalEventLimit {
			debugDigitalEvents++
			fmt.Printf("[chiplet-debug] load cluster=%d task=%s loadRemaining=%d loadProgress=%d totalLoad=%d activation=%d weight=%d\n",
				cluster.id,
				task.description,
				task.loadRemaining,
				task.loadProgress,
				task.totalLoadBytes,
				task.activationBytes,
				task.weightBytes,
			)
		}
		before := remaining
		consumed := cluster.consumeLoad(task, &remaining)
		if consumed > 0 {
			progress = true
			bytesTransferred += consumed
		}
		if task.loadRemaining == 0 {
			cluster.queuePostLoad(task, chiplet)
		} else {
			next = append(next, task)
		}
		if before == remaining {
			// bandwidth exhausted or unable to progress; keep task queued
		}
	}
	cluster.loadActive = next
	if bytesTransferred > 0 {
		cluster.loadBytesThisCycle += bytesTransferred
		cluster.totalLoadBytes += bytesTransferred
	}
	if progress {
		cluster.promoteWaiting()
	}
	return progress
}

func (cluster *computeCluster) processCompute(chiplet *Chiplet) bool {
	if len(cluster.computeActive) == 0 {
		return false
	}

	peAvailable := len(cluster.peArrays)
	if peAvailable <= 0 {
		return false
	}

	progress := false
	next := cluster.computeActive[:0]
	for _, task := range cluster.computeActive {
		if peAvailable <= 0 {
			next = append(next, task)
			continue
		}
		if debugDigitalEvents < debugDigitalEventLimit {
			debugDigitalEvents++
			fmt.Printf("[chiplet-debug] compute cluster=%d task=%s computeRemaining=%d waveIndex=%d/%d waveCycle=%d demand=%d peAvailable=%d\n",
				cluster.id,
				task.description,
				task.computeRemaining,
				task.peWaveIndex,
				len(task.peWaveArrays),
				task.peWaveCycle,
				task.computeDemand(),
				peAvailable,
			)
		}
		demand := task.computeDemand()
		if demand <= 0 {
			demand = peAvailable
		}
		if demand > peAvailable {
			next = append(next, task)
			continue
		}

		busy := task.consumeComputeCycle()
		if busy <= 0 {
			next = append(next, task)
			continue
		}
		if busy > peAvailable {
			busy = peAvailable
		}
		peAvailable -= busy
		cluster.recordPeActivity(chiplet, busy)
		progress = true

		if task.computeRemaining <= 0 {
			cluster.queuePostCompute(task, chiplet)
		} else {
			next = append(next, task)
		}
	}
	cluster.computeActive = next
	return progress
}

func (cluster *computeCluster) processStore(chiplet *Chiplet) bool {
	if len(cluster.storeActive) == 0 {
		return false
	}

	bandwidth := cluster.storeBandwidth
	if bandwidth <= 0 {
		bandwidth = 4096
	}
	remaining := int64(bandwidth)
	progress := false
	bytesTransferred := int64(0)

	next := cluster.storeActive[:0]
	for _, task := range cluster.storeActive {
		if remaining <= 0 {
			next = append(next, task)
			continue
		}
		if debugDigitalEvents < debugDigitalEventLimit {
			debugDigitalEvents++
			fmt.Printf("[chiplet-debug] store cluster=%d task=%s storeRemaining=%d storeProgress=%d totalStore=%d writeback=%d\n",
				cluster.id,
				task.description,
				task.storeRemaining,
				task.storeProgress,
				task.totalStoreBytes,
				task.writebackBytes,
			)
		}
		before := remaining
		consumed := cluster.consumeStore(task, &remaining)
		if consumed > 0 {
			progress = true
			bytesTransferred += consumed
		}
		if task.storeRemaining == 0 {
			cluster.queuePostStore(task, chiplet)
		} else {
			next = append(next, task)
		}
		if before == remaining {
			// unable to progress this cycle
		}
	}
	cluster.storeActive = next
	if bytesTransferred > 0 {
		cluster.storeBytesThisCycle += bytesTransferred
		cluster.totalStoreBytes += bytesTransferred
	}
	if progress {
		cluster.promoteWaiting()
	}
	return progress
}

func (cluster *computeCluster) processSpu(chiplet *Chiplet) bool {
	if len(cluster.spuActive) == 0 {
		return false
	}

	clustersAvailable := len(cluster.spuClusters)
	if clustersAvailable <= 0 {
		return false
	}

	progress := false
	next := cluster.spuActive[:0]
	for _, task := range cluster.spuActive {
		if clustersAvailable <= 0 {
			next = append(next, task)
			continue
		}
		demand := task.spuDemand(len(cluster.spuClusters))
		if demand > clustersAvailable {
			next = append(next, task)
			continue
		}

		busy := task.consumeSpuCycle(len(cluster.spuClusters))
		if busy <= 0 {
			next = append(next, task)
			continue
		}
		if busy > clustersAvailable {
			busy = clustersAvailable
		}
		clustersAvailable -= busy
		cluster.recordSpuActivity(chiplet, busy)
		cluster.spuActiveThisCycle += busy
		progress = true

		if task.spuRemaining <= 0 {
			cluster.scheduleNextPhase(task)
		} else {
			next = append(next, task)
		}
	}
	cluster.spuActive = next
	if progress {
		cluster.promoteWaiting()
	}
	return progress
}

func (cluster *computeCluster) processVpu(chiplet *Chiplet) bool {
	if len(cluster.vpuActive) == 0 {
		return false
	}

	unitsAvailable := len(cluster.vpuUnits)
	if unitsAvailable <= 0 {
		return false
	}

	progress := false
	next := cluster.vpuActive[:0]
	for _, task := range cluster.vpuActive {
		if unitsAvailable <= 0 {
			next = append(next, task)
			continue
		}

		demand := task.vpuDemand(len(cluster.vpuUnits))
		if demand > unitsAvailable {
			next = append(next, task)
			continue
		}

		busy := task.consumeVpuCycle(len(cluster.vpuUnits))
		if busy <= 0 {
			next = append(next, task)
			continue
		}
		if busy > unitsAvailable {
			busy = unitsAvailable
		}
		unitsAvailable -= busy
		cluster.recordVpuActivity(chiplet, busy)
		cluster.vpuActiveThisCycle += busy
		progress = true

		if task.vpuRemaining <= 0 {
			cluster.scheduleNextPhase(task)
		} else {
			next = append(next, task)
		}
	}
	cluster.vpuActive = next
	if progress {
		cluster.promoteWaiting()
	}
	return progress
}

func (cluster *computeCluster) queuePostLoad(task *digitalTask, chiplet *Chiplet) {
	if task == nil {
		return
	}

	task.loadRemaining = 0
	task.prefetchActive = false
	if chiplet != nil {
		chiplet.addLoadEnergyForTask(task)
	}

	cluster.scheduleNextPhase(task)
}

func (cluster *computeCluster) queuePostCompute(task *digitalTask, chiplet *Chiplet) {
	if task == nil {
		return
	}

	task.computeRemaining = 0
	task.computeCycleConsumed = false
	if task.totalStoreBytes > 0 && task.storeRemaining > 0 {
		task.writebackActive = true
	}
	cluster.scheduleNextPhase(task)
}

func (cluster *computeCluster) queuePostStore(task *digitalTask, chiplet *Chiplet) {
	if task == nil {
		return
	}

	task.storeRemaining = 0
	task.writebackActive = false
	if chiplet != nil {
		chiplet.addStoreEnergyForTask(task)
	}
	cluster.scheduleNextPhase(task)
}

func (cluster *computeCluster) enqueueTask(task *digitalTask) {
	if task == nil {
		return
	}
	if debugDigitalEvents > debugDigitalEventLimit {
		debugDigitalEvents = debugDigitalEventLimit
	} else {
		debugDigitalEvents = 0
	}
	if task.remainingCycles() <= 0 {
		cluster.finishTask(task, cluster.parent)
		return
	}
	cluster.routeTaskToWaiting(task)
	cluster.pendingCycles += task.remainingCycles()
	cluster.promoteWaiting()
}

func (cluster *computeCluster) promoteWaiting() {
	cluster.promoteQueue(&cluster.waitingBuffer)
	cluster.promoteQueue(&cluster.waitingBarrier)
	cluster.promoteQueue(&cluster.waitingPe)
	cluster.promoteQueue(&cluster.waitingVpu)
	cluster.promoteQueue(&cluster.waitingSpu)
	cluster.promoteQueue(&cluster.waitingMisc)
}

func (cluster *computeCluster) promoteQueue(queue *[]*digitalTask) {
	if queue == nil || len(*queue) == 0 {
		return
	}

	pending := (*queue)[:0]
	for _, task := range *queue {
		if !cluster.reserveResources(task) {
			pending = append(pending, task)
			continue
		}
		cluster.activateTask(task)
	}
	*queue = pending
}

func (cluster *computeCluster) routeTaskToWaiting(task *digitalTask) {
	if task == nil {
		return
	}
	switch task.execUnit {
	case ExecUnitPe:
		cluster.waitingPe = append(cluster.waitingPe, task)
	case ExecUnitSpu:
		cluster.waitingSpu = append(cluster.waitingSpu, task)
	case ExecUnitVpu:
		cluster.waitingVpu = append(cluster.waitingVpu, task)
	case ExecUnitBuffer:
		cluster.waitingBuffer = append(cluster.waitingBuffer, task)
	case ExecUnitBarrier:
		cluster.waitingBarrier = append(cluster.waitingBarrier, task)
	default:
		if task.requiresPe {
			cluster.waitingPe = append(cluster.waitingPe, task)
		} else if task.requiresSpu {
			cluster.waitingSpu = append(cluster.waitingSpu, task)
		} else {
			cluster.waitingMisc = append(cluster.waitingMisc, task)
		}
	}
}

func (cluster *computeCluster) activateTask(task *digitalTask) {
	if task == nil {
		return
	}
	switch {
	case task.loadRemaining > 0:
		task.currentPhase = taskPhaseLoad
		cluster.loadActive = append(cluster.loadActive, task)
	default:
		cluster.scheduleNextPhase(task)
	}
}

func (cluster *computeCluster) scheduleNextPhase(task *digitalTask) {
	if task == nil {
		return
	}
	chiplet := cluster.parent
	switch {
	case task.computeRemaining > 0:
		task.currentPhase = taskPhaseCompute
		cluster.computeActive = append(cluster.computeActive, task)
	case task.storeRemaining > 0:
		task.currentPhase = taskPhaseStore
		cluster.storeActive = append(cluster.storeActive, task)
	case task.vpuRemaining > 0:
		task.currentPhase = taskPhaseVpu
		cluster.vpuActive = append(cluster.vpuActive, task)
	case task.spuRemaining > 0:
		task.currentPhase = taskPhaseSpu
		cluster.spuActive = append(cluster.spuActive, task)
	default:
		cluster.finishTask(task, chiplet)
	}
}

func (cluster *computeCluster) totalWaiting() int {
	return len(cluster.waitingPe) +
		len(cluster.waitingSpu) +
		len(cluster.waitingVpu) +
		len(cluster.waitingBuffer) +
		len(cluster.waitingBarrier) +
		len(cluster.waitingMisc)
}

func (cluster *computeCluster) reserveResources(task *digitalTask) bool {
	if task == nil {
		return false
	}

	task.writebackBytes = 0

	if task.activationBytes > 0 {
		buffer := cluster.buffer("activation")
		if buffer != nil {
			if !buffer.Reserve(task.activationBytes) {
				fmt.Printf("[chiplet-debug] cluster %d activation reserve failed: req=%d cap=%d occ=%d\n",
					cluster.id,
					task.activationBytes,
					buffer.Capacity(),
					buffer.Occupancy(),
				)
				return false
			}
		}
	}

	if task.weightBytes > 0 {
		buffer := cluster.buffer("weights")
		if buffer != nil {
			if !buffer.Reserve(task.weightBytes) {
				fmt.Printf("[chiplet-debug] cluster %d weight reserve failed: req=%d cap=%d occ=%d\n",
					cluster.id,
					task.weightBytes,
					buffer.Capacity(),
					buffer.Occupancy(),
				)
				if task.activationBytes > 0 {
					if act := cluster.buffer("activation"); act != nil {
						act.Release(task.activationBytes)
					}
				}
				return false
			}
		}
	}

	if task.outputBytes > 0 {
		dest := strings.ToLower(strings.TrimSpace(task.storeBuffer))
		if dest == "" {
			dest = "scratch"
			if task.targetBuffer != "" {
				dest = strings.ToLower(strings.TrimSpace(task.targetBuffer))
			}
		}
		buffer := cluster.buffer(dest)
		if buffer != nil {
			if !buffer.Reserve(task.outputBytes) {
				fmt.Printf("[chiplet-debug] cluster %d %s reserve failed: req=%d cap=%d occ=%d\n",
					cluster.id,
					dest,
					task.outputBytes,
					buffer.Capacity(),
					buffer.Occupancy(),
				)
				if task.weightBytes > 0 {
					if w := cluster.buffer("weights"); w != nil {
						w.Release(task.weightBytes)
					}
				}
				if task.activationBytes > 0 {
					if act := cluster.buffer("activation"); act != nil {
						act.Release(task.activationBytes)
					}
				}
				return false
			}
			task.storeBuffer = dest
			task.writebackBytes = task.outputBytes
		}
	}

	return true
}

func (cluster *computeCluster) releaseResources(task *digitalTask) {
	if task == nil {
		return
	}

	if task.activationBytes > 0 {
		if buffer := cluster.buffer("activation"); buffer != nil {
			buffer.Release(task.activationBytes)
		}
	}

	if task.weightBytes > 0 {
		if buffer := cluster.buffer("weights"); buffer != nil {
			buffer.Release(task.weightBytes)
		}
	}

	if task.writebackBytes > 0 {
		dest := task.storeBuffer
		if dest == "" {
			dest = "scratch"
		}
		if buffer := cluster.buffer(dest); buffer != nil {
			buffer.Release(task.writebackBytes)
		}
		task.writebackBytes = 0
	}

	if bytes := task.bufferBytes; bytes > 0 {
		switch strings.ToLower(task.targetBuffer) {
		case "activation", "activations":
			if buffer := cluster.buffer("activation"); buffer != nil {
				buffer.Release(bytes)
			}
		case "scratch":
			if buffer := cluster.buffer("scratch"); buffer != nil {
				buffer.Release(bytes)
			}
		case "weights", "weight":
			if buffer := cluster.buffer("weights"); buffer != nil {
				buffer.Release(bytes)
			}
		}
		task.bufferBytes = 0
	}
}

func (cluster *computeCluster) recordPeActivity(chiplet *Chiplet, busy int) {
	if busy <= 0 || len(cluster.peArrays) == 0 {
		return
	}

	if busy > len(cluster.peArrays) {
		busy = len(cluster.peArrays)
	}

	for i := 0; i < busy; i++ {
		index := (cluster.peRotation + i) % len(cluster.peArrays)
		cluster.peBusyCycles[index]++
		if chiplet != nil {
			aggIndex := cluster.peOffset + index
			if aggIndex >= 0 && aggIndex < len(chiplet.PeBusyCycles) {
				chiplet.PeBusyCycles[aggIndex]++
			}
		}
	}
	cluster.peRotation = (cluster.peRotation + busy) % len(cluster.peArrays)
	cluster.peActiveThisCycle += busy
}

func (cluster *computeCluster) recordSpuActivity(chiplet *Chiplet, busy int) {
	if busy <= 0 || len(cluster.spuClusters) == 0 {
		return
	}

	if busy > len(cluster.spuClusters) {
		busy = len(cluster.spuClusters)
	}

	for i := 0; i < busy; i++ {
		cluster.spuClusterBusy[i]++
		if chiplet != nil {
			aggIndex := cluster.spuOffset + i
			if aggIndex >= 0 && aggIndex < len(chiplet.SpuClusterBusy) {
				chiplet.SpuClusterBusy[aggIndex]++
			}
		}
	}
	cluster.spuBusyCycles++
	if chiplet != nil {
		chiplet.SpuBusyCycles++
	}
}

func (cluster *computeCluster) recordVpuActivity(chiplet *Chiplet, busy int) {
	if busy <= 0 || len(cluster.vpuUnits) == 0 {
		return
	}

	if busy > len(cluster.vpuUnits) {
		busy = len(cluster.vpuUnits)
	}

	for i := 0; i < busy; i++ {
		index := (cluster.vpuRotation + i) % len(cluster.vpuUnits)
		if chiplet != nil {
			aggIndex := cluster.vpuOffset + index
			if aggIndex >= 0 && aggIndex < len(chiplet.VpuUnitBusy) {
				chiplet.VpuUnitBusy[aggIndex]++
			}
		}
	}
	cluster.vpuRotation = (cluster.vpuRotation + busy) % len(cluster.vpuUnits)
	cluster.vpuBusyCycles++
	if chiplet != nil {
		chiplet.VpuBusyCycles++
	}
}

func (cluster *computeCluster) finishTask(task *digitalTask, chiplet *Chiplet) {
	if task == nil {
		return
	}

	cluster.releaseResources(task)

	if task.macCount > 0 {
		cluster.totalMacs += task.macCount
		if chiplet != nil {
			chiplet.TotalMacs += task.macCount
		}
	}
	if task.scalarOps > 0 {
		cluster.spuScalarOps += int64(task.scalarOps)
		if chiplet != nil {
			chiplet.SpuScalarOps += int64(task.scalarOps)
		}
	}
	if task.vectorOps > 0 {
		cluster.spuVectorOps += int64(task.vectorOps)
		if chiplet != nil {
			chiplet.SpuVectorOps += int64(task.vectorOps)
		}
	}
	if task.vpuOps > 0 {
		cluster.vpuVectorOps += int64(task.vpuOps)
		if chiplet != nil {
			chiplet.VpuVectorOps += int64(task.vpuOps)
		}
	}
	if task.specialOps > 0 {
		cluster.spuSpecialOps += int64(task.specialOps)
		if chiplet != nil {
			chiplet.SpuSpecialOps += int64(task.specialOps)
		}
	}

	cluster.executedTasks++
	if cluster.pendingCycles < 0 {
		cluster.pendingCycles = 0
	}
	cluster.tasksCompletedThisCycle++
	if chiplet != nil {
		chiplet.ExecutedTasks++
		if chiplet.PendingTasks > 0 {
			chiplet.PendingTasks--
		}
	}
	if chiplet != nil && chiplet.PendingTasks < 0 {
		chiplet.PendingTasks = 0
	}
	if chiplet != nil {
		chiplet.recordTaskEnergy(task)
	}
	task.currentPhase = taskPhaseComplete
	cluster.promoteWaiting()
}

func (cluster *computeCluster) snapshotBuffers() map[string]int64 {
	occupancy := make(map[string]int64, len(cluster.bufferOrder))
	for _, name := range cluster.bufferOrder {
		if buffer := cluster.buffer(name); buffer != nil {
			occupancy[name] = buffer.Occupancy()
		}
	}
	return occupancy
}

func (cluster *computeCluster) consumeLoad(task *digitalTask, budget *int64) int64 {
	total := task.totalLoadBytes
	if total <= 0 {
		task.loadRemaining = 0
		task.prefetchActive = false
		return 0
	}

	remaining := total - task.loadProgress
	if remaining <= 0 {
		task.loadRemaining = 0
		task.prefetchActive = false
		return 0
	}

	bandwidth := cluster.loadBandwidth
	if bandwidth <= 0 {
		bandwidth = 2048
	}
	transfer := int64(bandwidth)
	if transfer > remaining {
		transfer = remaining
	}
	if budget != nil && transfer > *budget {
		transfer = *budget
	}
	if transfer <= 0 {
		return 0
	}

	task.loadProgress += transfer
	if task.loadRemaining > 0 {
		task.loadRemaining--
		if task.loadRemaining < 0 {
			task.loadRemaining = 0
		}
	}

	if task.loadProgress >= total {
		task.prefetchActive = false
		task.loadRemaining = 0
	} else {
		task.prefetchActive = true
	}

	if budget != nil {
		*budget -= transfer
		if *budget < 0 {
			*budget = 0
		}
	}

	return transfer
}

func (cluster *computeCluster) consumeStore(task *digitalTask, budget *int64) int64 {
	total := task.totalStoreBytes
	if total <= 0 {
		task.storeRemaining = 0
		task.writebackActive = false
		return 0
	}

	remaining := total - task.storeProgress
	if remaining <= 0 {
		task.storeRemaining = 0
		task.writebackActive = false
		return 0
	}

	bandwidth := cluster.storeBandwidth
	if bandwidth <= 0 {
		bandwidth = 4096
	}
	transfer := int64(bandwidth)
	if transfer > remaining {
		transfer = remaining
	}
	if budget != nil && transfer > *budget {
		transfer = *budget
	}
	if transfer <= 0 {
		return 0
	}

	if task.writebackBytes > 0 {
		dest := task.storeBuffer
		if dest == "" {
			dest = "scratch"
		}
		if transfer > task.writebackBytes {
			transfer = task.writebackBytes
		}
		if transfer <= 0 {
			return 0
		}
		if buffer := cluster.buffer(dest); buffer != nil {
			if !buffer.ApplyDelta(-transfer) {
				return 0
			}
		}
		task.writebackBytes -= transfer
		if task.writebackBytes < 0 {
			task.writebackBytes = 0
		}
	}

	task.storeProgress += transfer
	if task.storeRemaining > 0 {
		task.storeRemaining--
		if task.storeRemaining < 0 {
			task.storeRemaining = 0
		}
	}

	if task.storeProgress >= total {
		task.writebackActive = false
		task.storeRemaining = 0
	} else {
		task.writebackActive = true
	}

	if budget != nil {
		*budget -= transfer
		if *budget < 0 {
			*budget = 0
		}
	}

	return transfer
}

func (cluster *computeCluster) tick(chiplet *Chiplet) (int, bool) {
	cluster.loadBytesThisCycle = 0
	cluster.storeBytesThisCycle = 0
	cluster.peActiveThisCycle = 0
	cluster.spuActiveThisCycle = 0
	cluster.vpuActiveThisCycle = 0
	cluster.tasksCompletedThisCycle = 0
	cluster.promoteWaiting()

	loadProgress := cluster.processLoad(chiplet)
	computeProgress := cluster.processCompute(chiplet)
	storeProgress := cluster.processStore(chiplet)
	spuProgress := cluster.processSpu(chiplet)
	vpuProgress := cluster.processVpu(chiplet)

	progress := loadProgress || computeProgress || storeProgress || spuProgress || vpuProgress
	if progress && cluster.pendingCycles > 0 {
		cluster.pendingCycles--
		if cluster.pendingCycles < 0 {
			cluster.pendingCycles = 0
		}
	}

	if cluster.totalWaiting() == 0 &&
		len(cluster.loadActive) == 0 &&
		len(cluster.computeActive) == 0 &&
		len(cluster.storeActive) == 0 &&
		len(cluster.spuActive) == 0 &&
		len(cluster.vpuActive) == 0 &&
		cluster.pendingCycles > 0 {
		cluster.pendingCycles = 0
	}

	if progress {
		return 1, true
	}
	return 0, false
}

func (cluster *computeCluster) buildTaskFromDescriptor(desc *TaskDescriptor, taskID int) *digitalTask {
	task := &digitalTask{
		id:              taskID,
		clusterID:       cluster.id,
		kind:            desc.Kind,
		description:     desc.Description,
		execUnit:        desc.ExecUnit,
		activationBytes: desc.InputBytes,
		weightBytes:     desc.WeightBytes,
		outputBytes:     desc.OutputBytes,
		currentPhase:    taskPhaseLoad,
		requiresPe:      desc.RequiresPe,
		requiresSpu:     desc.RequiresSpu,
		requiresVpu:     desc.RequiresVpu,
		targetBuffer:    desc.TargetBuffer,
		storeBuffer:     strings.ToLower(strings.TrimSpace(desc.TargetBuffer)),
		bufferBytes:     desc.BufferBytes,
	}

	task.totalLoadBytes = desc.InputBytes + desc.WeightBytes
	task.totalStoreBytes = desc.OutputBytes
	task.prefetchBytes = 0
	task.writebackBytes = 0
	task.prefetchActive = task.totalLoadBytes > 0
	task.writebackActive = false

	loadCycles := cluster.estimateLoadCycles(desc)
	if loadCycles > 0 {
		task.loadRemaining += loadCycles
	}

	storeCycles := cluster.estimateStoreCycles(desc)
	if storeCycles > 0 {
		task.storeRemaining += storeCycles
	}

	if desc.RequiresPe {
		problemM := desc.ProblemM
		problemN := desc.ProblemN
		problemK := desc.ProblemK

		if len(cluster.peArrays) > 0 {
			defaultRows := cluster.peArrays[0].Rows
			defaultCols := cluster.peArrays[0].Cols
			if problemM <= 0 {
				problemM = defaultRows
			}
			if problemN <= 0 {
				problemN = defaultCols
			}
			if problemK <= 0 {
				problemK = defaultCols
			}
		} else {
			if problemM <= 0 {
				problemM = 128
			}
			if problemN <= 0 {
				problemN = 128
			}
			if problemK <= 0 {
				problemK = 128
			}
		}

		tileM := desc.TileM
		tileN := desc.TileN
		tileK := desc.TileK

		if tileM <= 0 {
			if len(cluster.peArrays) > 0 {
				tileM = cluster.peArrays[0].Rows
			} else {
				tileM = problemM
			}
		}
		if tileN <= 0 {
			if len(cluster.peArrays) > 0 {
				tileN = cluster.peArrays[0].Cols
			} else {
				tileN = problemN
			}
		}
		if tileK <= 0 {
			tileK = problemK
			if len(cluster.peArrays) > 0 && tileK <= 0 {
				tileK = cluster.peArrays[0].Cols
			}
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

		if tileM <= 0 {
			tileM = 1
		}
		if tileN <= 0 {
			tileN = 1
		}
		if tileK <= 0 {
			tileK = 1
		}

		tilesM := int(math.Ceil(float64(problemM) / float64(tileM)))
		tilesN := int(math.Ceil(float64(problemN) / float64(tileN)))
		if tilesM < 1 {
			tilesM = 1
		}
		if tilesN < 1 {
			tilesN = 1
		}
		totalTiles := tilesM * tilesN
		if totalTiles < 1 {
			totalTiles = 1
		}

		cyclesPerTile := 1
		if len(cluster.peArrays) > 0 {
			cyclesPerTile = cluster.peArrays[0].EstimateMatmulCycles(tileM, tileN, tileK)
			if cyclesPerTile < 1 {
				cyclesPerTile = 1
			}
		}

		parallelArrays := len(cluster.peArrays)
		task.peConcurrency = parallelArrays
		if desc.PeConcurrency > 0 && desc.PeConcurrency < parallelArrays {
			parallelArrays = desc.PeConcurrency
		}
		if parallelArrays <= 0 {
			parallelArrays = 1
		}
		task.peConcurrency = parallelArrays

		wavesFull := totalTiles / parallelArrays
		waveRemainder := totalTiles % parallelArrays

		waveArrays := make([]int, 0, wavesFull+1)
		computeCycles := 0

		for i := 0; i < wavesFull; i++ {
			waveArrays = append(waveArrays, parallelArrays)
			computeCycles += cyclesPerTile
		}
		if waveRemainder > 0 {
			waveArrays = append(waveArrays, waveRemainder)
			computeCycles += cyclesPerTile
		}
		if len(waveArrays) == 0 {
			waveArrays = append(waveArrays, 1)
			computeCycles += cyclesPerTile
		}

		task.computeRemaining += computeCycles
		task.peCyclesPerTile = cyclesPerTile
		task.peWaveArrays = waveArrays
		task.macCount = int64(problemM) * int64(problemN) * int64(problemK)
	}

	if desc.RequiresSpu {
		cycles, activeClusters := cluster.estimateSpuWork(desc)
		task.spuRemaining += cycles
		task.spuActiveClusters = activeClusters
	}

	if desc.RequiresVpu {
		cycles, activeUnits := cluster.estimateVpuWork(desc)
		task.vpuRemaining += cycles
		task.vpuActiveUnits = activeUnits
		task.vpuOps = desc.VectorOps
		if task.vpuOps <= 0 {
			task.vpuOps = desc.ScalarOps
		}
	}

	task.scalarOps = desc.ScalarOps
	task.vectorOps = desc.VectorOps
	task.specialOps = desc.SpecialOps
	task.registersRd = desc.RegistersRd
	task.registersWr = desc.RegistersWr
	if desc.RequiresVpu && !desc.RequiresSpu {
		task.vectorOps = 0
		task.specialOps = 0
	}

	switch {
	case task.loadRemaining > 0:
		task.currentPhase = taskPhaseLoad
	case task.computeRemaining > 0:
		task.currentPhase = taskPhaseCompute
	case task.storeRemaining > 0:
		task.currentPhase = taskPhaseStore
	case task.vpuRemaining > 0:
		task.currentPhase = taskPhaseVpu
	case task.spuRemaining > 0:
		task.currentPhase = taskPhaseSpu
	default:
		task.computeRemaining = 1
		task.currentPhase = taskPhaseCompute
		task.peCyclesPerTile = 1
		task.peWaveArrays = []int{1}
		task.macCount = 0
	}

	return task
}

func (cluster *computeCluster) estimateLoadCycles(desc *TaskDescriptor) int {
	total := 0
	total += cluster.transferCyclesForBuffer("activation", desc.InputBytes, 2048)
	total += cluster.transferCyclesForBuffer("weights", desc.WeightBytes, 2048)
	return total
}

func (cluster *computeCluster) estimateStoreCycles(desc *TaskDescriptor) int {
	if desc.OutputBytes <= 0 {
		return 0
	}
	buffer := strings.ToLower(strings.TrimSpace(desc.TargetBuffer))
	if buffer == "" {
		buffer = "scratch"
	}
	return cluster.transferCyclesForBuffer(buffer, desc.OutputBytes, 4096)
}

func (cluster *computeCluster) transferCyclesForBuffer(name string, bytes int64, fallbackBandwidth int64) int {
	if bytes <= 0 {
		return 0
	}

	if buffer := cluster.buffer(name); buffer != nil {
		return buffer.TransferCycles(bytes)
	}

	if fallbackBandwidth <= 0 {
		fallbackBandwidth = 1024
	}

	cycles := int(math.Ceil(float64(bytes) / float64(fallbackBandwidth)))
	if cycles < 1 {
		cycles = 1
	}
	return cycles
}

func (cluster *computeCluster) estimateSpuWork(desc *TaskDescriptor) (int, int) {
	if len(cluster.spuClusters) == 0 {
		totalOps := desc.ScalarOps + desc.VectorOps + desc.SpecialOps
		if totalOps <= 0 {
			totalOps = 1
		}
		return totalOps, 1
	}

	scalarPerCycle := 0
	vectorPerCycle := 0
	specialLatency := 0
	specialClusters := 0

	for _, spu := range cluster.spuClusters {
		scalarPerCycle += spu.ScalarThroughput()
		vectorPerCycle += spu.VectorThroughput()
		if spu.HasSpecialUnit {
			specialClusters++
			latency := spu.SpecialLatency()
			if latency > specialLatency {
				specialLatency = latency
			}
		}
	}

	if scalarPerCycle <= 0 {
		scalarPerCycle = len(cluster.spuClusters)
	}
	if vectorPerCycle <= 0 {
		vectorPerCycle = len(cluster.spuClusters)
	}
	if specialLatency <= 0 {
		specialLatency = 8
	}
	if specialClusters <= 0 {
		specialClusters = 1
	}

	scalarCycles := 0
	if desc.ScalarOps > 0 {
		scalarCycles = int(math.Ceil(float64(desc.ScalarOps) / float64(scalarPerCycle)))
	}
	vectorCycles := 0
	if desc.VectorOps > 0 {
		vectorCycles = int(math.Ceil(float64(desc.VectorOps) / float64(vectorPerCycle)))
	}

	cycles := scalarCycles
	if vectorCycles > cycles {
		cycles = vectorCycles
	}

	if desc.SpecialOps > 0 {
		specialThroughput := specialClusters
		if len(cluster.spuClusters) > 0 {
			avgVector := vectorPerCycle / len(cluster.spuClusters)
			if avgVector > 0 {
				specialThroughput *= avgVector
			}
		}
		if specialThroughput <= 0 {
			specialThroughput = specialClusters
		}
		specialWaves := int(math.Ceil(float64(desc.SpecialOps) / float64(specialThroughput)))
		specialCycles := specialWaves * specialLatency
		if specialCycles > cycles {
			cycles = specialCycles
		}
	}

	if cycles < 1 {
		cycles = 1
	}

	avgScalarPerCluster := scalarPerCycle / len(cluster.spuClusters)
	if avgScalarPerCluster < 1 {
		avgScalarPerCluster = 1
	}
	avgVectorPerCluster := vectorPerCycle / len(cluster.spuClusters)
	if avgVectorPerCluster < 1 {
		avgVectorPerCluster = 1
	}

	requiredClustersScalar := 0
	if desc.ScalarOps > 0 {
		requiredClustersScalar = int(math.Ceil(float64(desc.ScalarOps) / float64(avgScalarPerCluster*cycles)))
	}
	requiredClustersVector := 0
	if desc.VectorOps > 0 {
		requiredClustersVector = int(math.Ceil(float64(desc.VectorOps) / float64(avgVectorPerCluster*cycles)))
	}
	requiredClusters := requiredClustersScalar
	if requiredClustersVector > requiredClusters {
		requiredClusters = requiredClustersVector
	}
	if requiredClusters < 1 {
		requiredClusters = 1
	}
	if requiredClusters > len(cluster.spuClusters) {
		requiredClusters = len(cluster.spuClusters)
	}

	registerPressure := desc.RegistersRd + desc.RegistersWr
	pressureThreshold := len(cluster.spuClusters) * 32
	if registerPressure > pressureThreshold {
		penalty := int(math.Ceil(float64(registerPressure-pressureThreshold) / 16.0))
		cycles += penalty
	}

	return cycles, requiredClusters
}

func (cluster *computeCluster) estimateVpuWork(desc *TaskDescriptor) (int, int) {
	ops := desc.VectorOps
	if ops <= 0 {
		ops = int(desc.OutputBytes / 2)
	}
	if ops <= 0 {
		ops = desc.ScalarOps
	}
	if ops <= 0 {
		ops = 1
	}

	if len(cluster.vpuUnits) == 0 {
		return ops, 1
	}

	totalThroughput := 0
	maxLatency := 0
	for _, unit := range cluster.vpuUnits {
		throughput := unit.VectorThroughput()
		if throughput <= 0 {
			throughput = 1
		}
		totalThroughput += throughput
		latency := unit.LatencyCycles()
		if latency > maxLatency {
			maxLatency = latency
		}
	}
	if totalThroughput <= 0 {
		totalThroughput = len(cluster.vpuUnits)
	}

	cycles := int(math.Ceil(float64(ops) / float64(totalThroughput)))
	if cycles < 1 {
		cycles = 1
	}
	if maxLatency > cycles {
		cycles = maxLatency
	}

	opsPerCycle := float64(ops) / float64(cycles)
	avgPerUnit := float64(totalThroughput) / float64(len(cluster.vpuUnits))
	if avgPerUnit <= 0 {
		avgPerUnit = 1
	}

	activeUnits := int(math.Ceil(opsPerCycle / avgPerUnit))
	if activeUnits < 1 {
		activeUnits = 1
	}
	if activeUnits > len(cluster.vpuUnits) {
		activeUnits = len(cluster.vpuUnits)
	}

	return cycles, activeUnits
}

func (t *digitalTask) remainingCycles() int {
	return t.loadRemaining +
		t.computeRemaining +
		t.storeRemaining +
		t.spuRemaining +
		t.vpuRemaining
}

func (t *digitalTask) advancePhase() {
	switch {
	case t.loadRemaining > 0:
		t.loadRemaining--
		if t.loadRemaining == 0 && t.computeRemaining == 0 && t.storeRemaining == 0 && t.spuRemaining == 0 && t.vpuRemaining == 0 {
			t.currentPhase = taskPhaseComplete
		} else if t.computeRemaining > 0 {
			t.currentPhase = taskPhaseCompute
		} else if t.storeRemaining > 0 {
			t.currentPhase = taskPhaseStore
		} else if t.vpuRemaining > 0 {
			t.currentPhase = taskPhaseVpu
		} else if t.spuRemaining > 0 {
			t.currentPhase = taskPhaseSpu
		}
	case t.computeRemaining > 0:
		if t.computeCycleConsumed {
			t.computeCycleConsumed = false
		} else {
			t.computeRemaining--
		}
		if t.computeRemaining == 0 {
			if t.storeRemaining > 0 {
				t.currentPhase = taskPhaseStore
			} else if t.vpuRemaining > 0 {
				t.currentPhase = taskPhaseVpu
			} else if t.spuRemaining > 0 {
				t.currentPhase = taskPhaseSpu
			} else {
				t.currentPhase = taskPhaseComplete
			}
		}
	case t.storeRemaining > 0:
		t.storeRemaining--
		if t.storeRemaining == 0 {
			if t.vpuRemaining > 0 {
				t.currentPhase = taskPhaseVpu
			} else if t.spuRemaining > 0 {
				t.currentPhase = taskPhaseSpu
			} else {
				t.currentPhase = taskPhaseComplete
			}
		}
	case t.vpuRemaining > 0:
		if t.vpuCycleConsumed {
			t.vpuCycleConsumed = false
		} else {
			t.vpuRemaining--
		}
		if t.vpuRemaining == 0 {
			if t.spuRemaining > 0 {
				t.currentPhase = taskPhaseSpu
			} else {
				t.currentPhase = taskPhaseComplete
			}
		}
	case t.spuRemaining > 0:
		if t.spuCycleConsumed {
			t.spuCycleConsumed = false
		} else {
			t.spuRemaining--
		}
		if t.spuRemaining == 0 {
			t.currentPhase = taskPhaseComplete
		}
	default:
		t.currentPhase = taskPhaseComplete
	}
}

func (t *digitalTask) consumeComputeCycle() int {
	if t.computeRemaining <= 0 {
		t.computeCycleConsumed = true
		return 0
	}

	if len(t.peWaveArrays) == 0 || t.peCyclesPerTile <= 0 {
		t.computeRemaining--
		t.computeCycleConsumed = true
		return 0
	}

	busyArrays := t.peWaveArrays[t.peWaveIndex]
	if t.peConcurrency > 0 && busyArrays > t.peConcurrency {
		busyArrays = t.peConcurrency
	}
	if busyArrays < 0 {
		busyArrays = 0
	}

	t.peWaveCycle++
	if t.peWaveCycle >= t.peCyclesPerTile {
		t.peWaveCycle = 0
		if t.peWaveIndex+1 < len(t.peWaveArrays) {
			t.peWaveIndex++
		}
	}

	t.computeRemaining--
	t.computeCycleConsumed = true
	return busyArrays
}

func (t *digitalTask) computeDemand() int {
	if t.computeRemaining <= 0 {
		return 0
	}
	if len(t.peWaveArrays) == 0 {
		if t.requiresPe {
			return 1
		}
		return 0
	}
	index := t.peWaveIndex
	if index < 0 || index >= len(t.peWaveArrays) {
		index = len(t.peWaveArrays) - 1
		if index < 0 {
			index = 0
		}
	}
	demand := t.peWaveArrays[index]
	if demand <= 0 && t.requiresPe {
		demand = 1
	}
	if t.peConcurrency > 0 && demand > t.peConcurrency {
		demand = t.peConcurrency
	}
	return demand
}

func (t *digitalTask) consumeSpuCycle(totalClusters int) int {
	if t.spuRemaining <= 0 {
		t.spuCycleConsumed = true
		return 0
	}

	active := t.spuActiveClusters
	if active <= 0 {
		active = totalClusters
	}
	if totalClusters > 0 && active > totalClusters {
		active = totalClusters
	}
	if active < 0 {
		active = 0
	}

	t.spuRemaining--
	t.spuCycleConsumed = true
	return active
}

func (t *digitalTask) spuDemand(totalClusters int) int {
	if t.spuRemaining <= 0 {
		return 0
	}
	demand := t.spuActiveClusters
	if demand <= 0 {
		demand = totalClusters
	}
	if totalClusters > 0 && demand > totalClusters {
		demand = totalClusters
	}
	if demand < 0 {
		demand = 0
	}
	return demand
}

func (t *digitalTask) consumeVpuCycle(totalUnits int) int {
	if t.vpuRemaining <= 0 {
		t.vpuCycleConsumed = true
		return 0
	}

	active := t.vpuActiveUnits
	if active <= 0 {
		active = totalUnits
	}
	if totalUnits > 0 && active > totalUnits {
		active = totalUnits
	}
	if active < 0 {
		active = 0
	}

	t.vpuRemaining--
	t.vpuCycleConsumed = true
	return active
}

func (t *digitalTask) vpuDemand(totalUnits int) int {
	if t.vpuRemaining <= 0 {
		return 0
	}
	demand := t.vpuActiveUnits
	if demand <= 0 {
		demand = totalUnits
	}
	if totalUnits > 0 && demand > totalUnits {
		demand = totalUnits
	}
	if demand < 0 {
		demand = 0
	}
	return demand
}

// Chiplet groups together the PE arrays, SPU clusters and local buffers that
// form one digital acceleration tile.
type Chiplet struct {
	ID          int
	clusters    []*computeCluster
	nextTaskID  int
	nextCluster int

	ExecutedTasks   int
	PendingCycles   int
	PendingTasks    int
	BusyCycles      int
	BufferOccupancy map[string]int64
	BufferPeakUsage map[string]int64

	TotalMacs           int64
	PeBusyCycles        []int64
	SpuScalarOps        int64
	SpuVectorOps        int64
	SpuSpecialOps       int64
	SpuBusyCycles       int64
	SpuClusterBusy      []int64
	VpuVectorOps        int64
	VpuBusyCycles       int64
	VpuUnitBusy         []int64
	CycleLoadBytes      int64
	CycleStoreBytes     int64
	CyclePeActive       int
	CycleSpuActive      int
	CycleVpuActive      int
	CycleTasksCompleted int
	TotalLoadBytes      int64
	TotalStoreBytes     int64

	params               Parameters
	DynamicEnergyPJ      float64
	StaticEnergyPJ       float64
	InterconnectEnergyPJ float64
	AreaMm2              float64
	PeEnergyPJ           float64
	SpuEnergyPJ          float64
	VpuEnergyPJ          float64
	ReduceEnergyPJ       float64
}

// NewChiplet constructs a chiplet with homogeneous PE arrays, SPU clusters and
// buffers. The buffer bandwidths are currently conservative heuristics: 2KB per
// cycle for activations/weights and 4KB per cycle for scratch space to reflect
// wider write-back paths.
func NewChiplet(
	id int,
	pesPerChiplet int,
	peRows int,
	peCols int,
	spusPerChiplet int,
	activationBuffer int64,
	scratchBuffer int64,
	params Parameters,
) *Chiplet {
	if pesPerChiplet <= 0 {
		pesPerChiplet = 4
	}
	if spusPerChiplet <= 0 {
		spusPerChiplet = 4
	}
	if params.ClockMHz <= 0 {
		params = DefaultParameters()
	}
	if activationBuffer <= 0 {
		activationBuffer = params.Buffer.ActivationBytes
	}
	if scratchBuffer <= 0 {
		scratchBuffer = params.Buffer.ScratchBytes
	}
	if activationBuffer <= 0 {
		activationBuffer = 4 * 1024 * 1024
	}
	if scratchBuffer <= 0 {
		scratchBuffer = 4 * 1024 * 1024
	}

	clusterCount := 4
	if pesPerChiplet < clusterCount {
		clusterCount = pesPerChiplet
	}
	if clusterCount <= 0 {
		clusterCount = 1
	}
	pePerCluster := pesPerChiplet / clusterCount
	peRemainder := pesPerChiplet % clusterCount
	if pePerCluster <= 0 {
		pePerCluster = 1
	}
	spuPerCluster := spusPerChiplet / clusterCount
	spuRemainder := spusPerChiplet % clusterCount
	if spuPerCluster <= 0 {
		spuPerCluster = 1
	}

	activationPerCluster := activationBuffer / int64(clusterCount)
	scratchPerCluster := scratchBuffer / int64(clusterCount)
	if activationPerCluster <= 0 {
		activationPerCluster = activationBuffer
	}
	if scratchPerCluster <= 0 {
		scratchPerCluster = scratchBuffer
	}

	chiplet := &Chiplet{
		ID:          id,
		nextTaskID:  1,
		nextCluster: 0,
		BufferOccupancy: map[string]int64{
			"activation": 0,
			"weights":    0,
			"scratch":    0,
		},
		BufferPeakUsage: map[string]int64{
			"activation": 0,
			"weights":    0,
			"scratch":    0,
		},
		params: params,
	}

	clusters := make([]*computeCluster, 0, clusterCount)
	totalPe := 0
	totalSpu := 0
	totalVpu := 0
	peOffset := 0
	spuOffset := 0
	vpuOffset := 0
	for idx := 0; idx < clusterCount; idx++ {
		peCount := pePerCluster
		if peRemainder > 0 {
			peCount++
			peRemainder--
		}
		spuCount := spuPerCluster
		if spuRemainder > 0 {
			spuCount++
			spuRemainder--
		}
		if peCount <= 0 {
			peCount = 1
		}
		if spuCount <= 0 {
			spuCount = 1
		}

		cluster := newComputeClusterWithOffsets(
			idx,
			peCount,
			peRows,
			peCols,
			spuCount,
			activationPerCluster,
			scratchPerCluster,
			peOffset,
			spuOffset,
			vpuOffset,
			chiplet,
			params,
		)
		clusters = append(clusters, cluster)
		totalPe += len(cluster.peArrays)
		totalSpu += len(cluster.spuClusters)
		totalVpu += len(cluster.vpuUnits)
		peOffset += len(cluster.peArrays)
		spuOffset += len(cluster.spuClusters)
		vpuOffset += len(cluster.vpuUnits)
	}

	chiplet.clusters = clusters
	chiplet.PeBusyCycles = make([]int64, totalPe)
	chiplet.SpuClusterBusy = make([]int64, totalSpu)
	chiplet.VpuUnitBusy = make([]int64, totalVpu)
	chiplet.AreaMm2 = params.BaseAreaMm2 +
		float64(pesPerChiplet)*params.PeArray.MacAreaMm2 +
		float64(spusPerChiplet)*params.Spu.ClusterAreaMm2 +
		float64(totalVpu)*params.Vpu.UnitAreaMm2 +
		params.Buffer.AreaMm2

	return chiplet
}

func (c *Chiplet) addLoadEnergyForTask(task *digitalTask) {
	if task == nil {
		return
	}
	activationEnergy := float64(task.activationBytes) * (c.params.Buffer.ReadEnergyPJPerByte + c.params.PeArray.ActivationReadPJPerByte)
	weightEnergy := float64(task.weightBytes) * (c.params.Buffer.ReadEnergyPJPerByte + c.params.PeArray.WeightReadPJPerByte)
	c.DynamicEnergyPJ += activationEnergy + weightEnergy
}

func (c *Chiplet) addStoreEnergyForTask(task *digitalTask) {
	if task == nil {
		return
	}
	storeBytes := task.outputBytes
	if storeBytes <= 0 {
		storeBytes = task.totalStoreBytes
	}
	if storeBytes <= 0 {
		return
	}
	energy := float64(storeBytes) * (c.params.Buffer.WriteEnergyPJPerByte + c.params.PeArray.OutputWritePJPerByte)
	c.DynamicEnergyPJ += energy
}

func (c *Chiplet) recordTaskEnergy(task *digitalTask) {
	if task == nil {
		return
	}
	peEnergy := float64(task.macCount) * c.params.PeArray.MacEnergyPJ
	spuScalarEnergy := float64(task.scalarOps) * c.params.Spu.ScalarEnergyPJ
	spuVectorEnergy := float64(task.vectorOps) * c.params.Spu.VectorEnergyPJ
	spuSpecialEnergy := float64(task.specialOps) * c.params.Spu.SpecialEnergyPJ
	spuEnergy := spuScalarEnergy + spuVectorEnergy + spuSpecialEnergy
	vpuEnergy := float64(task.vpuOps) * c.params.Vpu.VectorEnergyPJ

	if peEnergy > 0 {
		c.PeEnergyPJ += peEnergy
	}
	if vpuEnergy > 0 {
		c.VpuEnergyPJ += vpuEnergy
	}
	if spuEnergy > 0 {
		if task.kind == TaskKindReduction {
			c.ReduceEnergyPJ += spuEnergy
		} else {
			c.SpuEnergyPJ += spuEnergy
		}
	}

	c.DynamicEnergyPJ += peEnergy + spuEnergy + vpuEnergy
}

func (c *Chiplet) AddInterconnectEnergy(bytes int64) {
	if bytes <= 0 {
		return
	}
	c.InterconnectEnergyPJ += float64(bytes) * c.params.Interconnect.EnergyPJPerByte
}

func (c *Chiplet) AccumulateStaticEnergy(cycles int) {
	if cycles <= 0 {
		return
	}
	totalMw := c.params.StaticPowerMw + c.params.Buffer.LeakagePowerMw + c.params.LeakageOverheadMw
	perCycle := c.energyPerCyclePJ(totalMw)
	c.StaticEnergyPJ += float64(cycles) * perCycle
}

func (c *Chiplet) energyPerCyclePJ(powerMw float64) float64 {
	if c.params.ClockMHz <= 0 {
		return 0
	}
	return powerMw * 1e3 / float64(c.params.ClockMHz)
}

// SubmitDescriptor enqueues a high-level task for execution. The chiplet will
// lazily reserve buffers when the task becomes active; if the descriptor
// requires more capacity than the buffers expose the submission fails.
func (c *Chiplet) SubmitDescriptor(desc *TaskDescriptor) bool {
	if desc == nil {
		return false
	}

	cluster := c.selectCluster(desc)
	if cluster == nil {
		return false
	}

	task := cluster.buildTaskFromDescriptor(desc, c.nextTaskID)
	if task == nil {
		return false
	}

	c.nextTaskID++
	task.clusterID = cluster.id

	cluster.enqueueTask(task)
	c.PendingTasks++
	c.PendingCycles += task.remainingCycles()
	return true
}

func (c *Chiplet) selectCluster(desc *TaskDescriptor) *computeCluster {
	if len(c.clusters) == 0 {
		return nil
	}

	if desc != nil && desc.PreferredCluster >= 0 && desc.PreferredCluster < len(c.clusters) {
		return c.clusters[desc.PreferredCluster]
	}

	bestIndex := -1
	bestScore := math.MaxInt
	for idx, cluster := range c.clusters {
		if desc != nil && !cluster.canAcceptDescriptor(desc) {
			continue
		}
		queueDepth := cluster.totalWaiting() +
			len(cluster.loadActive) +
			len(cluster.computeActive) +
			len(cluster.storeActive) +
			len(cluster.spuActive) +
			len(cluster.vpuActive)
		if queueDepth < bestScore {
			bestScore = queueDepth
			bestIndex = idx
		}
	}

	if bestIndex >= 0 {
		return c.clusters[bestIndex]
	}

	// Fallback: no cluster had immediate capacity; choose least loaded for backpressure awareness.
	if bestIndex >= 0 {
		return c.clusters[bestIndex]
	}

	return c.clusters[0]
}

// RecordComputeTask is kept for backwards compatibility with Phase 2 callers.
// The new pipeline accounts for executions automatically, so this becomes a
// no-op.
func (c *Chiplet) RecordComputeTask() {}

// ScheduleTask keeps the legacy latency-driven scheduling path functioning.
func (c *Chiplet) ScheduleTask(latency int) {
	cycles := latency
	if cycles <= 0 {
		if len(c.clusters) == 0 || len(c.clusters[0].peArrays) == 0 {
			cycles = 1
		} else {
			cycles = c.clusters[0].peArrays[0].Cols
		}
	}

	if len(c.clusters) == 0 {
		return
	}

	cluster := c.clusters[c.nextCluster%len(c.clusters)]
	c.nextCluster = (c.nextCluster + 1) % len(c.clusters)

	task := &digitalTask{
		id:               c.nextTaskID,
		clusterID:        cluster.id,
		kind:             TaskKindLegacy,
		description:      "legacy",
		loadRemaining:    0,
		computeRemaining: cycles,
		storeRemaining:   0,
		spuRemaining:     0,
		currentPhase:     taskPhaseCompute,
	}
	c.nextTaskID++

	cluster.enqueueTask(task)
	c.PendingTasks++
	c.PendingCycles += cycles
}

// Tick advances the internal timing counter by one cycle.
func (c *Chiplet) Tick() {
	if len(c.clusters) == 0 {
		return
	}

	cyclesConsumed := 0
	busyClusters := 0
	c.CycleLoadBytes = 0
	c.CycleStoreBytes = 0
	c.CyclePeActive = 0
	c.CycleSpuActive = 0
	c.CycleVpuActive = 0
	c.CycleTasksCompleted = 0

	for _, cluster := range c.clusters {
		consumed, busy := cluster.tick(c)
		cyclesConsumed += consumed
		if busy {
			busyClusters++
		}
		c.CycleLoadBytes += cluster.loadBytesThisCycle
		c.CycleStoreBytes += cluster.storeBytesThisCycle
		c.CyclePeActive += cluster.peActiveThisCycle
		c.CycleSpuActive += cluster.spuActiveThisCycle
		c.CycleVpuActive += cluster.vpuActiveThisCycle
		c.CycleTasksCompleted += cluster.tasksCompletedThisCycle
		c.TotalLoadBytes += cluster.loadBytesThisCycle
		c.TotalStoreBytes += cluster.storeBytesThisCycle
	}

	if cyclesConsumed > 0 {
		if cyclesConsumed >= c.PendingCycles {
			c.PendingCycles = 0
		} else {
			c.PendingCycles -= cyclesConsumed
		}
	}

	if c.PendingTasks == 0 {
		c.PendingCycles = 0
	}

	c.BusyCycles += busyClusters
	c.refreshBufferOccupancy()
}

// Busy reports whether the chiplet is still processing a scheduled task.
func (c *Chiplet) Busy() bool {
	if c.PendingTasks > 0 {
		return true
	}
	for _, cluster := range c.clusters {
		if cluster.totalWaiting() > 0 ||
			len(cluster.loadActive) > 0 ||
			len(cluster.computeActive) > 0 ||
			len(cluster.storeActive) > 0 ||
			len(cluster.spuActive) > 0 ||
			len(cluster.vpuActive) > 0 {
			return true
		}
	}
	return false
}

// PendingCapacity returns an approximate limit of how many tasks the chiplet
// can keep in flight before additional submissions should be throttled. The
// heuristic allows each compute cluster to queue a pair of tasks so load and
// compute/store phases can overlap.
func (c *Chiplet) PendingCapacity() int {
	if c == nil {
		return 1
	}
	clusterCount := len(c.clusters)
	if clusterCount <= 0 {
		return 2
	}
	capacity := clusterCount * 2
	if capacity < 2 {
		capacity = 2
	}
	return capacity
}

// AdjustBuffer allows external modules (e.g. transfer tasks) to manipulate the
// instantaneous buffer occupancy. The value is applied directly to the relevant
// buffer, enforcing capacity bounds.
func (c *Chiplet) AdjustBuffer(name string, delta int64) bool {
	name = strings.ToLower(name)
	cluster := c.selectClusterForBuffer(name, delta)
	if cluster == nil {
		return false
	}

	if !cluster.applyBufferDelta(name, delta) {
		return false
	}

	c.refreshBufferOccupancy()
	return true
}

// BufferUsage returns the current occupancy in bytes.
func (c *Chiplet) BufferUsage(name string) int64 {
	name = strings.ToLower(name)
	var total int64
	for _, cluster := range c.clusters {
		total += cluster.bufferUsage(name)
	}
	if c.BufferOccupancy == nil {
		c.BufferOccupancy = make(map[string]int64)
	}
	c.BufferOccupancy[name] = total
	return total
}

// bufferCapacity exposes the configured capacity for a buffer.
func (c *Chiplet) bufferCapacity(name string) int64 {
	var total int64
	for _, cluster := range c.clusters {
		total += cluster.bufferCapacity(name)
	}
	return total
}

func (c *Chiplet) refreshBufferOccupancy() {
	if c.BufferOccupancy == nil {
		c.BufferOccupancy = make(map[string]int64)
	}
	if c.BufferPeakUsage == nil {
		c.BufferPeakUsage = make(map[string]int64)
	}
	totals := make(map[string]int64)
	for _, cluster := range c.clusters {
		for name, value := range cluster.snapshotBuffers() {
			key := strings.ToLower(name)
			totals[key] += value
		}
	}
	for name, value := range totals {
		c.BufferOccupancy[name] = value
		if value > c.BufferPeakUsage[name] {
			c.BufferPeakUsage[name] = value
		}
	}
}

func (c *Chiplet) selectClusterForBuffer(name string, quantity int64) *computeCluster {
	if len(c.clusters) == 0 {
		return nil
	}

	name = strings.ToLower(name)
	best := c.clusters[0]
	if quantity < 0 {
		bestUsage := best.bufferUsage(name)
		for _, cluster := range c.clusters {
			usage := cluster.bufferUsage(name)
			if usage > bestUsage {
				best = cluster
				bestUsage = usage
			}
		}
		return best
	}

	bestFree := best.freeCapacity(name)
	for _, cluster := range c.clusters {
		free := cluster.freeCapacity(name)
		if free > bestFree {
			best = cluster
			bestFree = free
		}
	}
	return best
}

func (cluster *computeCluster) applyBufferDelta(name string, delta int64) bool {
	buf := cluster.buffer(name)
	if buf == nil {
		return false
	}
	return buf.ApplyDelta(delta)
}

func (c *Chiplet) buildTaskFromDescriptor(desc *TaskDescriptor) *digitalTask {
	if desc == nil {
		return nil
	}
	cluster := c.selectCluster(desc)
	if cluster == nil {
		return nil
	}
	return cluster.buildTaskFromDescriptor(desc, c.nextTaskID)
}

func (c *Chiplet) estimateLoadCycles(desc *TaskDescriptor) int {
	cluster := c.selectCluster(desc)
	if cluster == nil {
		return 0
	}
	return cluster.estimateLoadCycles(desc)
}

func (c *Chiplet) estimateStoreCycles(desc *TaskDescriptor) int {
	cluster := c.selectCluster(desc)
	if cluster == nil {
		return 0
	}
	return cluster.estimateStoreCycles(desc)
}

func (c *Chiplet) transferCyclesForBuffer(name string, bytes int64, fallbackBandwidth int64) int {
	cluster := c.selectClusterForBuffer(name, bytes)
	if cluster == nil {
		return 0
	}
	return cluster.transferCyclesForBuffer(name, bytes, fallbackBandwidth)
}

func (c *Chiplet) estimateSpuWork(desc *TaskDescriptor) (int, int) {
	cluster := c.selectCluster(desc)
	if cluster == nil {
		return 0, 0
	}
	return cluster.estimateSpuWork(desc)
}

func (c *Chiplet) snapshotBuffers() {
	c.refreshBufferOccupancy()
}

func (c *Chiplet) buffer(name string) *Buffer {
	cluster := c.selectClusterForBuffer(name, 0)
	if cluster == nil {
		return nil
	}
	return cluster.buffer(name)
}
