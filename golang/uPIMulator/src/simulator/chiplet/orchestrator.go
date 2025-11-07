package chiplet

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"uPIMulator/src/simulator/chiplet/rram"
)

// HostOrchestrator is responsible for translating high-level workload intents
// (token batches, operator graphs, etc.) into chiplet tasks. The current
// placeholder emits one batch of initialization tasks so that the scheduling
// pipeline can be exercised end-to-end.
type HostOrchestrator struct {
	config                  *Config
	topology                *Topology
	graph                   *OpGraph
	commandPath             string
	remainingDeps           map[int]int
	readyQueue              []int
	inFlight                map[int]bool
	nodeResources           map[int]*resourceUsage
	nodeBatch               map[int]int
	batchOutstanding        map[int]int
	minWaitCycles           int
	throttleCycles          int
	digitalRR               int
	rramRR                  int
	lastDigitalID           int
	lastRramID              int
	transferBytesEstimate   int64
	transferBandwidthBytes  int64
	maxIssuePerCycle        int
	maxDigitalPerCycle      int
	maxRramPerCycle         int
	maxTransferBytes        int64
	digitalBufferLimit      int64
	rramBufferLimit         int64
	interconnectBufferLimit int64
	enableResourceLimits    bool
	streamEnabled           bool
	streamTemplate          *OpGraph
	streamLowWatermark      int
	streamHighWatermark     int
	streamTotalBatches      int
	streamBatchesIssued     int
	streamBatchesCompleted  int
	streamActiveBatches     int
	nextNodeID              int
	hostEvents              map[int]*HostEvent
	outstanding             outstandingTracker
	moeSessions             map[int]*moeDispatchSession
	moeMergeOwners          map[int]int
	transferEstimator       TransferLatencyEstimator
}

const debugMaxDebugEvents = 50

var debugIssueCounter int
var debugCompleteCounter int

func defaultExecDomainForKind(kind CommandKind) ExecDomain {
	switch kind {
	case CommandKindPeGemm, CommandKindPeAttentionHead:
		return ExecDomainPeArray
	case CommandKindPeSpuOp:
		return ExecDomainSpu
	case CommandKindPeVpuOp:
		return ExecDomainVpu
	case CommandKindPeReduce:
		return ExecDomainReduce
	case CommandKindPeBarrier, CommandKindPeBufferAlloc, CommandKindPeBufferRelease:
		return ExecDomainHost
	case CommandKindRramStageAct, CommandKindRramExecute, CommandKindRramPost, CommandKindRramWeightLoad:
		return ExecDomainCim
	case CommandKindTransferSchedule, CommandKindTransferC2D, CommandKindTransferD2C, CommandKindTransferHost2D, CommandKindTransferD2Host:
		return ExecDomainDma
	case CommandKindHostEmbedLookup, CommandKindHostRouterPrep, CommandKindHostSynchronize, CommandKindHostGatingFetch, CommandKindHostLmHead:
		return ExecDomainHost
	default:
		return ExecDomainUndefined
	}
}

func (this *HostOrchestrator) Init(config *Config, topology *Topology, commandPath string) {
	this.config = config
	this.topology = topology
	this.commandPath = commandPath
	debugIssueCounter = 0
	debugCompleteCounter = 0
	this.remainingDeps = make(map[int]int)
	this.readyQueue = make([]int, 0)
	this.inFlight = make(map[int]bool)
	this.enableResourceLimits = config.HostLimitResources
	this.nodeBatch = make(map[int]int)
	this.batchOutstanding = make(map[int]int)
	if this.enableResourceLimits {
		this.nodeResources = make(map[int]*resourceUsage)
	} else {
		this.nodeResources = nil
	}
	this.streamTotalBatches = config.HostStreamTotalBatches
	this.streamLowWatermark = config.HostStreamLowWatermark
	this.streamHighWatermark = config.HostStreamHighWatermark
	this.streamBatchesIssued = 0
	this.streamBatchesCompleted = 0
	this.streamActiveBatches = 0
	this.streamTemplate = nil
	this.nextNodeID = 0
	this.hostEvents = make(map[int]*HostEvent)
	if this.streamLowWatermark < 0 {
		this.streamLowWatermark = 0
	}
	if this.streamHighWatermark <= 0 {
		this.streamHighWatermark = this.streamLowWatermark + 1
	}
	if this.streamHighWatermark < this.streamLowWatermark {
		this.streamHighWatermark = this.streamLowWatermark
	}
	this.streamEnabled = false
	if this.streamTotalBatches <= 0 {
		// Interpret zero/negative as unlimited streaming.
		this.streamEnabled = true
		this.streamTotalBatches = 0
	} else if this.streamTotalBatches > 1 {
		this.streamEnabled = true
	}
	if this.streamEnabled && this.streamHighWatermark <= this.streamLowWatermark {
		this.streamHighWatermark = this.streamLowWatermark + 1
	}
	this.minWaitCycles = 8
	this.throttleCycles = 0
	this.digitalRR = 0
	this.rramRR = 0
	this.lastDigitalID = -1
	this.lastRramID = -1
	this.transferBytesEstimate = 1024
	if config.TransferBandwidthDr > 0 {
		this.transferBandwidthBytes = config.TransferBandwidthDr
	} else {
		this.transferBandwidthBytes = 4096
	}
	if config.HostDmaBandwidth > 0 && config.HostDmaBandwidth > this.transferBandwidthBytes {
		this.transferBandwidthBytes = config.HostDmaBandwidth
	}
	this.maxIssuePerCycle = config.NumDigitalChiplets + config.NumRramChiplets
	if this.maxIssuePerCycle <= 0 {
		this.maxIssuePerCycle = 4
	}
	if this.maxIssuePerCycle > 32 {
		this.maxIssuePerCycle = 32
	}
	this.maxDigitalPerCycle = config.NumDigitalChiplets
	if this.maxDigitalPerCycle <= 0 {
		this.maxDigitalPerCycle = 2
	}
	this.maxRramPerCycle = config.NumRramChiplets
	if this.maxRramPerCycle <= 0 {
		this.maxRramPerCycle = 2
	}
	this.maxTransferBytes = this.transferBandwidthBytes
	if config.TransferBandwidthRd > this.maxTransferBytes {
		this.maxTransferBytes = config.TransferBandwidthRd
	}
	if config.HostDmaBandwidth > this.maxTransferBytes {
		this.maxTransferBytes = config.HostDmaBandwidth
	}
	if this.maxTransferBytes <= 0 {
		this.maxTransferBytes = 1 << 20
	}
	const fallbackLimit = int64(^uint64(0) >> 1)
	this.digitalBufferLimit = config.DigitalActivationBuffer + config.DigitalScratchBuffer
	if this.digitalBufferLimit <= 0 {
		this.digitalBufferLimit = fallbackLimit
	}
	this.rramBufferLimit = config.RramInputBuffer + config.RramOutputBuffer
	if this.rramBufferLimit <= 0 {
		this.rramBufferLimit = fallbackLimit
	}
	this.interconnectBufferLimit = this.maxTransferBytes * int64(this.minWaitCycles+1)
	if this.interconnectBufferLimit <= 0 {
		this.interconnectBufferLimit = fallbackLimit
	}
	this.outstanding = outstandingTracker{}
	this.moeSessions = make(map[int]*moeDispatchSession)
	this.moeMergeOwners = make(map[int]int)

	if topology != nil {
		if topology.Digital.PeCols > 0 {
			value := topology.Digital.PeCols / 4
			if value < 1 {
				value = 1
			}
			this.minWaitCycles = value
			this.transferBytesEstimate = int64(topology.Digital.PeCols*topology.Digital.PeRows) * 2
			if config.TransferBandwidthDr > 0 {
				this.transferBandwidthBytes = config.TransferBandwidthDr
			}
		} else if topology.Rram.SaRows > 0 {
			value := topology.Rram.SaRows / 4
			if value < 1 {
				value = 1
			}
			this.minWaitCycles = value
			this.transferBytesEstimate = int64(topology.Rram.SaRows*topology.Rram.SaCols) * 2
			if config.TransferBandwidthRd > 0 {
				this.transferBandwidthBytes = config.TransferBandwidthRd
			}
		}
	}
	if commandPath != "" && this.loadCommandGraph(commandPath) {
		return
	}
	this.bootstrapTasks()
}

func (this *HostOrchestrator) Fini() {
	this.config = nil
	this.topology = nil
	this.graph = nil
	this.remainingDeps = nil
	this.readyQueue = nil
	this.inFlight = nil
	this.nodeResources = nil
	this.nodeBatch = nil
	this.batchOutstanding = nil
	this.throttleCycles = 0
	this.digitalRR = 0
	this.rramRR = 0
	this.lastDigitalID = -1
	this.lastRramID = -1
	this.transferBytesEstimate = 1024
	this.transferBandwidthBytes = 4096
	this.maxIssuePerCycle = 4
	this.maxDigitalPerCycle = 2
	this.maxRramPerCycle = 2
	this.maxTransferBytes = 1 << 20
	this.digitalBufferLimit = 0
	this.rramBufferLimit = 0
	this.interconnectBufferLimit = 0
	this.outstanding.Reset()
	this.enableResourceLimits = false
	this.streamEnabled = false
	this.streamTemplate = nil
	this.streamLowWatermark = 0
	this.streamHighWatermark = 0
	this.streamTotalBatches = 0
	this.streamBatchesIssued = 0
	this.streamBatchesCompleted = 0
	this.streamActiveBatches = 0
	this.nextNodeID = 0
	this.hostEvents = nil
	this.moeSessions = nil
	this.moeMergeOwners = nil
}

// Advance returns the next task to stage. Future versions will incorporate
// dependency checks and adaptive batching.
func (this *HostOrchestrator) Advance() []*Task {
	this.ensureStreamingCapacity()

	if this.throttleCycles > 0 {
		this.throttleCycles--
		return nil
	}

	result := make([]*Task, 0)
	digitalIssued := 0
	rramIssued := 0
	var transferIssued int64 = 0
	requeue := make([]int, 0)

	for len(this.readyQueue) > 0 {
		if this.maxIssuePerCycle > 0 && len(result) >= this.maxIssuePerCycle {
			break
		}

		nodeID := this.readyQueue[0]
		this.readyQueue = this.readyQueue[1:]

		if this.inFlight[nodeID] {
			continue
		}

		node := this.graph.Nodes[nodeID]
		if node == nil {
			continue
		}

		if !this.canIssueNode(node, &digitalIssued, &rramIssued, &transferIssued) {
			requeue = append(requeue, nodeID)
			continue
		}

		task := this.createTaskFromNode(node)
		this.inFlight[nodeID] = true
		if task != nil {
			result = append(result, task)
			if debugIssueCounter < debugMaxDebugEvents {
				debugIssueCounter++
				kind := ""
				if cmd, ok := task.Payload.(*CommandDescriptor); ok && cmd != nil {
					kind = cmd.Kind.String()
				}
				fmt.Printf("[chiplet-debug] issue node=%d target=%s kind=%s latency=%d batch=%d\n",
					node.ID, node.Target.String(), kind, task.Latency, node.Batch)
			}
		}
	}

	if len(requeue) > 0 {
		this.readyQueue = append(requeue, this.readyQueue...)
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

func (this *HostOrchestrator) bootstrapTasks() {
	if this.topology == nil {
		return
	}

	graph := NewOpGraph()
	this.lastDigitalID = -1
	this.lastRramID = -1

	graph.AddNode(&OpNode{
		ID:      0,
		Type:    TaskTypeDataMove,
		Target:  TaskTargetDigital,
		Latency: 4,
		Payload: "tokenize",
	})
	graph.AddNode(&OpNode{
		ID:      1,
		Type:    TaskTypeCompute,
		Target:  TaskTargetDigital,
		Latency: this.topology.Digital.PeCols,
		Deps:    []int{0},
		Payload: "attention",
	})
	graph.AddNode(&OpNode{
		ID:      2,
		Type:    TaskTypeDataMove,
		Target:  TaskTargetTransfer,
		Latency: this.topology.Digital.PeCols / 8,
		Deps:    []int{1},
		Payload: "transfer_to_rram",
	})
	graph.AddNode(&OpNode{
		ID:      3,
		Type:    TaskTypeCim,
		Target:  TaskTargetRram,
		Latency: this.topology.Rram.SaRows,
		Deps:    []int{2},
		Payload: "cim",
	})
	graph.AddNode(&OpNode{
		ID:      4,
		Type:    TaskTypeDataMove,
		Target:  TaskTargetTransfer,
		Latency: this.topology.Digital.PeCols / 8,
		Deps:    []int{3},
		Payload: "transfer_to_digital",
	})
	graph.AddNode(&OpNode{
		ID:      5,
		Type:    TaskTypeCompute,
		Target:  TaskTargetDigital,
		Latency: this.topology.Digital.PeCols / 2,
		Deps:    []int{4},
		Payload: "postprocess",
	})

	this.setGraph(graph)
}

func (this *HostOrchestrator) setGraph(graph *OpGraph) {
	this.remainingDeps = make(map[int]int)
	this.readyQueue = make([]int, 0)
	this.inFlight = make(map[int]bool)
	if this.enableResourceLimits {
		this.nodeResources = make(map[int]*resourceUsage)
	} else {
		this.nodeResources = nil
	}
	this.nodeBatch = make(map[int]int)
	this.batchOutstanding = make(map[int]int)
	this.streamBatchesIssued = 0
	this.streamBatchesCompleted = 0
	this.streamActiveBatches = 0
	this.nextNodeID = 0

	if this.streamEnabled && graph != nil {
		this.streamTemplate = graph.Clone()
		this.graph = NewOpGraph()
		this.ensureStreamingCapacity()
		return
	}

	this.streamTemplate = nil
	this.graph = graph
	if graph == nil {
		return
	}

	maxID := 0
	first := true
	for id, node := range graph.Nodes {
		this.remainingDeps[id] = len(node.Deps)
		if len(node.Deps) == 0 {
			this.readyQueue = append(this.readyQueue, id)
		}
		if first || id > maxID {
			maxID = id
			first = false
		}
		this.nodeBatch[id] = 0
	}
	if first {
		this.nextNodeID = 0
	} else {
		this.nextNodeID = maxID + 1
	}
}

func (this *HostOrchestrator) ensureStreamingCapacity() {
	if !this.streamEnabled {
		return
	}
	if this.streamTemplate == nil || this.graph == nil {
		return
	}

	for {
		if !this.shouldSpawnStreamBatch() {
			break
		}
		if !this.instantiateStreamBatch() {
			break
		}
	}
}

func (this *HostOrchestrator) shouldSpawnStreamBatch() bool {
	if !this.streamEnabled || this.streamTemplate == nil || this.graph == nil {
		return false
	}
	if this.streamHighWatermark > 0 && this.streamActiveBatches >= this.streamHighWatermark {
		return false
	}
	if this.streamTotalBatches > 0 && this.streamBatchesIssued >= this.streamTotalBatches {
		return false
	}
	if this.streamActiveBatches > this.streamLowWatermark {
		return false
	}

	ready := len(this.readyQueue)
	inFlight := len(this.inFlight)
	slackThreshold := this.maxIssuePerCycle
	if slackThreshold <= 0 {
		slackThreshold = 1
	}

	if ready+inFlight > slackThreshold {
		return false
	}

	if this.enableResourceLimits {
		outstanding := this.outstanding.Clone()
		if this.digitalBufferLimit > 0 && outstanding.Digital >= this.digitalBufferLimit {
			return false
		}
		if this.rramBufferLimit > 0 && outstanding.Rram >= this.rramBufferLimit {
			return false
		}
		if this.interconnectBufferLimit > 0 && outstanding.Transfer >= this.interconnectBufferLimit {
			return false
		}
	}

	return true
}

func (this *HostOrchestrator) instantiateStreamBatch() bool {
	if this.streamTemplate == nil || this.graph == nil {
		return false
	}

	templateIDs := sortedNodeIDs(this.streamTemplate)
	if len(templateIDs) == 0 {
		return false
	}
	if this.streamTotalBatches > 0 && this.streamBatchesIssued >= this.streamTotalBatches {
		return false
	}

	batchID := this.streamBatchesIssued
	clones := make([]*OpNode, len(templateIDs))
	idMap := make(map[int]int, len(templateIDs))

	for idx, templateID := range templateIDs {
		tmplNode := this.streamTemplate.Nodes[templateID]
		if tmplNode == nil {
			continue
		}
		newID := this.nextNodeID
		this.nextNodeID++
		clone := &OpNode{
			ID:      newID,
			Type:    tmplNode.Type,
			Target:  tmplNode.Target,
			Latency: tmplNode.Latency,
			Payload: clonePayload(tmplNode.Payload),
			Batch:   batchID,
		}
		clones[idx] = clone
		idMap[templateID] = newID
	}

	for idx, templateID := range templateIDs {
		tmplNode := this.streamTemplate.Nodes[templateID]
		if tmplNode == nil {
			continue
		}
		clone := clones[idx]
		if clone == nil {
			continue
		}

		if len(tmplNode.Deps) > 0 {
			clone.Deps = make([]int, 0, len(tmplNode.Deps))
			for _, dep := range tmplNode.Deps {
				if mapped, ok := idMap[dep]; ok {
					clone.Deps = append(clone.Deps, mapped)
				}
			}
		}

		switch payload := clone.Payload.(type) {
		case *CommandDescriptor:
			cmdCopy := *payload
			if len(clone.Deps) > 0 {
				deps := make([]int32, len(clone.Deps))
				for i, depID := range clone.Deps {
					deps[i] = int32(depID)
				}
				cmdCopy.Dependencies = deps
			} else {
				cmdCopy.Dependencies = nil
			}
			cmdCopy.ID = int32(clone.ID)
			annotateStreamCommand(&cmdCopy, batchID, templateID)
			clone.Payload = &cmdCopy
		case CommandDescriptor:
			cmdCopy := payload
			if len(clone.Deps) > 0 {
				deps := make([]int32, len(clone.Deps))
				for i, depID := range clone.Deps {
					deps[i] = int32(depID)
				}
				cmdCopy.Dependencies = deps
			} else {
				cmdCopy.Dependencies = nil
			}
			cmdCopy.ID = int32(clone.ID)
			annotateStreamCommand(&cmdCopy, batchID, templateID)
			clone.Payload = cmdCopy
		}
	}

	outstanding := 0
	for _, clone := range clones {
		if clone == nil {
			continue
		}
		this.graph.AddNode(clone)
		this.remainingDeps[clone.ID] = len(clone.Deps)
		if len(clone.Deps) == 0 {
			this.readyQueue = append(this.readyQueue, clone.ID)
		}
		this.nodeBatch[clone.ID] = batchID
		outstanding++
	}

	if outstanding == 0 {
		return false
	}

	this.batchOutstanding[batchID] = outstanding
	this.streamActiveBatches++
	this.streamBatchesIssued++

	return true
}

func sortedNodeIDs(graph *OpGraph) []int {
	ids := make([]int, 0, len(graph.Nodes))
	for id := range graph.Nodes {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func clonePayload(payload interface{}) interface{} {
	switch value := payload.(type) {
	case nil:
		return nil
	case *CommandDescriptor:
		if value == nil {
			return nil
		}
		copy := *value
		if len(value.Dependencies) > 0 {
			copy.Dependencies = append([]int32(nil), value.Dependencies...)
		} else {
			copy.Dependencies = nil
		}
		if len(value.Metadata) > 0 {
			copy.Metadata = cloneMetadata(value.Metadata)
		}
		return &copy
	case CommandDescriptor:
		copy := value
		if len(value.Dependencies) > 0 {
			copy.Dependencies = append([]int32(nil), value.Dependencies...)
		} else {
			copy.Dependencies = nil
		}
		if len(value.Metadata) > 0 {
			copy.Metadata = cloneMetadata(value.Metadata)
		}
		return copy
	case string:
		return value
	case []byte:
		dup := make([]byte, len(value))
		copy(dup, value)
		return dup
	case map[string]interface{}:
		dup := make(map[string]interface{}, len(value))
		for k, v := range value {
			dup[k] = v
		}
		return dup
	default:
		return value
	}
}

func annotateStreamCommand(cmd *CommandDescriptor, batchID int, templateID int) {
	if cmd == nil {
		return
	}
	if batchID < 0 {
		return
	}
	meta := cloneMetadata(cmd.Metadata)
	if meta == nil {
		meta = make(map[string]interface{})
	}
	meta["stream_batch_id"] = batchID
	meta["stream_template_id"] = templateID
	cmd.Metadata = meta
}

func (this *HostOrchestrator) loadCommandGraph(commandPath string) bool {
	path := filepath.Clean(commandPath)
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	var commands []CommandDescriptor
	if err := json.Unmarshal(data, &commands); err != nil {
		return false
	}
	if len(commands) == 0 {
		return false
	}

	graph := NewOpGraph()
	this.lastDigitalID = -1
	this.lastRramID = -1
	prevID := -1

	for idx := range commands {
		cmdCopy := commands[idx]
		nodeID := int(cmdCopy.ID)
		if nodeID <= prevID {
			nodeID = prevID + 1
			cmdCopy.ID = int32(nodeID)
		}
		if nodeID < 0 {
			nodeID = idx
			cmdCopy.ID = int32(nodeID)
		}

		taskType := TaskTypeDataMove
		switch cmdCopy.Target {
		case TaskTargetDigital:
			taskType = TaskTypeCompute
		case TaskTargetRram:
			taskType = TaskTypeCim
		case TaskTargetTransfer:
			taskType = TaskTypeDataMove
		default:
			taskType = TaskTypeSync
		}
		if cmdCopy.Kind == CommandKindSync {
			taskType = TaskTypeSync
		}

		deps := make([]int, 0, len(cmdCopy.Dependencies))
		for _, dep := range cmdCopy.Dependencies {
			deps = append(deps, int(dep))
		}
		if len(deps) == 0 && prevID >= 0 {
			deps = append(deps, prevID)
		}

		latency := int(cmdCopy.Latency)
		if latency <= 0 {
			latency = this.minWaitCycles
			if latency <= 0 {
				latency = 1
			}
		}

		node := &OpNode{
			ID:      nodeID,
			Type:    taskType,
			Target:  cmdCopy.Target,
			Latency: latency,
			Deps:    deps,
			Payload: &cmdCopy,
		}

		graph.AddNode(node)
		prevID = nodeID
	}

	this.setGraph(graph)
	return true
}

func (this *HostOrchestrator) canIssueNode(node *OpNode, digitalIssued *int, rramIssued *int, transferIssued *int64) bool {
	if node == nil {
		return false
	}

	var usage resourceUsage
	if this.enableResourceLimits {
		usage = this.estimateResourceUsage(node)
	}
	cmd, isCommand := node.Payload.(*CommandDescriptor)
	if cmd == nil {
		isCommand = false
	}
	useLimits := this.enableResourceLimits && isCommand
	if useLimits && usage.Digital > 0 && this.digitalBufferLimit > 0 &&
		this.outstanding.Digital+usage.Digital > this.digitalBufferLimit {
		return false
	}
	if useLimits && usage.Rram > 0 && this.rramBufferLimit > 0 &&
		this.outstanding.Rram+usage.Rram > this.rramBufferLimit {
		return false
	}
	if useLimits && usage.Transfer > 0 && this.interconnectBufferLimit > 0 &&
		this.outstanding.Transfer+usage.Transfer > this.interconnectBufferLimit {
		return false
	}
	dmaBandwidth := this.config.TransferBandwidthRd
	if this.config.HostDmaBandwidth > dmaBandwidth {
		dmaBandwidth = this.config.HostDmaBandwidth
	}
	if dmaBandwidth <= 0 {
		dmaBandwidth = this.maxTransferBytes
	}
	if dmaBandwidth <= 0 {
		dmaBandwidth = int64(1 << 16)
	}
	dmaLimit := dmaBandwidth * int64(this.minWaitCycles+1)
	if useLimits && usage.Dma > 0 && dmaLimit > 0 &&
		this.outstanding.Dma+usage.Dma > dmaLimit {
		return false
	}

	target := node.Target
	if isCommand {
		if cmd.Target == TaskTargetDigital || cmd.Target == TaskTargetRram || cmd.Target == TaskTargetTransfer {
			target = cmd.Target
		}
	}

	switch target {
	case TaskTargetDigital:
		if this.maxDigitalPerCycle > 0 && *digitalIssued >= this.maxDigitalPerCycle {
			return false
		}
		*digitalIssued++
		if useLimits && usage.Digital > 0 {
			this.outstanding.Digital += usage.Digital
		}
	case TaskTargetRram:
		if this.maxRramPerCycle > 0 && *rramIssued >= this.maxRramPerCycle {
			return false
		}
		*rramIssued++
		if useLimits && usage.Rram > 0 {
			this.outstanding.Rram += usage.Rram
		}
	case TaskTargetTransfer:
		bytes := this.transferBytesEstimate
		if isCommand && cmd.PayloadBytes > 0 {
			bytes = int64(cmd.PayloadBytes)
		}
		limitCost := bytes
		if this.maxTransferBytes > 0 && limitCost > this.maxTransferBytes {
			limitCost = this.maxTransferBytes
		}
		if this.maxTransferBytes > 0 && *transferIssued+limitCost > this.maxTransferBytes {
			return false
		}
		*transferIssued += limitCost
		if useLimits {
			if usage.Transfer <= 0 {
				usage.Transfer = bytes
			}
			this.outstanding.Transfer += usage.Transfer
			if usage.Dma <= 0 {
				usage.Dma = bytes
			}
			this.outstanding.Dma += usage.Dma
		}
	case TaskTargetHost:
		// Host 指令暂不受 per-cycle 限制
	default:
		// generic task, leave counters unchanged
	}

	if useLimits {
		this.nodeResources[node.ID] = &usage
	}
	return true
}

func (this *HostOrchestrator) NotifyBackpressure(waitCycles int) {
	if waitCycles <= this.minWaitCycles {
		return
	}

	throttle := waitCycles / this.minWaitCycles
	if throttle <= 0 {
		return
	}

	if throttle > this.throttleCycles {
		this.throttleCycles = throttle
	}
}

func (this *HostOrchestrator) NotifyTaskCompletion(nodeID int) {
	if this.graph == nil {
		return
	}

	delete(this.inFlight, nodeID)
	if this.enableResourceLimits {
		if usage, ok := this.nodeResources[nodeID]; ok && usage != nil {
			if usage.Digital > 0 {
				this.outstanding.Digital -= usage.Digital
				if this.outstanding.Digital < 0 {
					this.outstanding.Digital = 0
				}
			}
			if usage.Rram > 0 {
				this.outstanding.Rram -= usage.Rram
				if this.outstanding.Rram < 0 {
					this.outstanding.Rram = 0
				}
			}
			if usage.Transfer > 0 {
				this.outstanding.Transfer -= usage.Transfer
				if this.outstanding.Transfer < 0 {
					this.outstanding.Transfer = 0
				}
			}
			if usage.Dma > 0 {
				this.outstanding.Dma -= usage.Dma
				if this.outstanding.Dma < 0 {
					this.outstanding.Dma = 0
				}
			}
			delete(this.nodeResources, nodeID)
		}
	}
	if this.nodeBatch != nil {
		if batchID, ok := this.nodeBatch[nodeID]; ok {
			if this.batchOutstanding != nil {
				if remaining, exists := this.batchOutstanding[batchID]; exists {
					remaining--
					if remaining <= 0 {
						delete(this.batchOutstanding, batchID)
						this.streamActiveBatches--
						if this.streamActiveBatches < 0 {
							this.streamActiveBatches = 0
						}
						this.streamBatchesCompleted++
					} else {
						this.batchOutstanding[batchID] = remaining
					}
				}
			}
			delete(this.nodeBatch, nodeID)
		}
	}

	this.handleMoeMergeCompletion(nodeID)

	if event, ok := this.ConsumeHostEvent(nodeID); ok && event != nil {
		this.handleHostEvent(nodeID, event)
	}

	for _, succ := range this.graph.Successors(nodeID) {
		if _, exists := this.remainingDeps[succ]; exists {
			if this.remainingDeps[succ] > 0 {
				this.remainingDeps[succ]--
			}
			if this.remainingDeps[succ] == 0 {
				this.readyQueue = append(this.readyQueue, succ)
			}
		}
	}

	if debugCompleteCounter < debugMaxDebugEvents {
		if node, ok := this.graph.Nodes[nodeID]; ok && node != nil {
			kind := ""
			if cmd, ok := node.Payload.(*CommandDescriptor); ok && cmd != nil {
				kind = cmd.Kind.String()
			}
			debugCompleteCounter++
			fmt.Printf("[chiplet-debug] complete node=%d target=%s kind=%s batch=%d\n",
				node.ID, node.Target.String(), kind, node.Batch)
		}
	}
}

func (this *HostOrchestrator) NotifyHostEvent(nodeID int, event *HostEvent) {
	if this == nil || event == nil {
		return
	}
	if this.hostEvents == nil {
		this.hostEvents = make(map[int]*HostEvent)
	}
	this.hostEvents[nodeID] = event
}

func (this *HostOrchestrator) handleMoeMergeCompletion(nodeID int) {
	if this == nil {
		return
	}
	owner, ok := this.moeMergeOwners[nodeID]
	if !ok {
		return
	}
	delete(this.moeMergeOwners, nodeID)
	session, exists := this.moeSessions[owner]
	if !exists || session == nil {
		return
	}
	if _, present := session.mergeNodes[nodeID]; present {
		delete(session.mergeNodes, nodeID)
		if session.outstanding > 0 {
			session.outstanding--
		}
	}
	if session.outstanding <= 0 {
		this.finalizeMoeSession(owner, session)
	}
}

func (this *HostOrchestrator) ConsumeHostEvent(nodeID int) (*HostEvent, bool) {
	if this == nil || this.hostEvents == nil {
		return nil, false
	}
	event, ok := this.hostEvents[nodeID]
	if ok {
		delete(this.hostEvents, nodeID)
	}
	return event, ok
}

func (this *HostOrchestrator) handleHostEvent(nodeID int, event *HostEvent) {
	if event == nil {
		return
	}

	switch event.Kind {
	case CommandKindHostGatingFetch:
		this.handleGatingFetchEvent(nodeID, event)
	default:
		// Other host events will be wired in future phases.
	}
}

func (this *HostOrchestrator) handleGatingFetchEvent(nodeID int, event *HostEvent) {
	if event == nil {
		return
	}

	selected := append([]int(nil), event.SelectedExperts...)
	if len(selected) == 0 {
		selected = fallbackExperts(event.CandidateExperts, event.TopK)
	}
	if len(selected) == 0 {
		selected = []int{0}
	}

	if debugIssueCounter < debugMaxDebugEvents {
		fmt.Printf("[chiplet-debug] host_event node=%d kind=%s selected=%v\n",
			nodeID, event.Kind.String(), selected)
	}

	if event.Metadata != nil {
		event.Metadata["selected_experts"] = append([]int(nil), selected...)
	}
	if len(event.CandidateExperts) == 0 {
		event.CandidateExperts = append([]int(nil), selected...)
	}
	event.SelectedExperts = append([]int(nil), selected...)

	session := &moeDispatchSession{
		parentNode:  nodeID,
		digitalID:   event.DigitalChiplet,
		bufferID:    event.BufferID,
		topK:        event.TopK,
		tokens:      event.Tokens,
		features:    event.Features,
		expertIDs:   make([]int, 0, len(selected)),
		mergeNodes:  make(map[int]struct{}),
		barrierNode: -1,
	}
	if this.graph != nil {
		session.successors = dedupeIntSlice(this.graph.Successors(nodeID))
	}

	baseDeps := []int{nodeID}
	parentBatch, hasBatch := this.nodeBatch[nodeID]
	mergeNodeIDs := make([]int, 0, len(selected))
	resolvedDigitalID := event.DigitalChiplet

	for _, expertID := range selected {
		group := this.buildExpertCommandGroup(event, expertID)
		if len(group) == 0 {
			continue
		}
		newIDs := this.AppendCommandGroup(group, baseDeps, true)
		if hasBatch {
			this.assignNodesToBatch(newIDs, parentBatch)
		}
		if len(newIDs) > 0 {
			mergeNodeID := newIDs[len(newIDs)-1]
			session.outstanding++
			session.mergeNodes[mergeNodeID] = struct{}{}
			this.moeMergeOwners[mergeNodeID] = nodeID
			session.expertIDs = append(session.expertIDs, expertID)
			mergeNodeIDs = append(mergeNodeIDs, mergeNodeID)
			if len(group) > 0 {
				resolvedDigitalID = int(group[len(group)-1].ChipletID)
			}
		}
	}

	if resolvedDigitalID < 0 {
		if this.config != nil && this.config.NumDigitalChiplets > 0 {
			resolvedDigitalID = 0
		} else {
			resolvedDigitalID = 0
		}
	}
	session.digitalID = resolvedDigitalID

	if session.outstanding > 0 && len(mergeNodeIDs) > 0 {
		barrierMeta := cloneMetadata(event.Metadata)
		if barrierMeta == nil {
			barrierMeta = make(map[string]interface{})
		}
		barrierMeta["op"] = "moe_barrier"
		barrierMeta["parent_node"] = nodeID
		barrierMeta["buffer_id"] = event.BufferID
		barrierMeta["top_k"] = event.TopK
		barrierMeta["tokens"] = event.Tokens
		barrierMeta["features"] = event.Features
		barrierMeta["selected_experts"] = append([]int(nil), selected...)
		barrierMeta["candidate_experts"] = append([]int(nil), event.CandidateExperts...)

		barrierLatency := metadataInt(event.Metadata, "barrier_latency", 1)
		if barrierLatency <= 0 {
			barrierLatency = 1
		}

		barrierCmd := CommandDescriptor{
			Kind:      CommandKindPeBarrier,
			Target:    TaskTargetDigital,
			ChipletID: int32(resolvedDigitalID),
			Latency:   int32(barrierLatency),
			Metadata:  barrierMeta,
		}

		barrierIDs := this.AppendCommandGroup([]CommandDescriptor{barrierCmd}, mergeNodeIDs, false)
		if hasBatch {
			this.assignNodesToBatch(barrierIDs, parentBatch)
		}
		if len(barrierIDs) > 0 {
			barrierID := barrierIDs[0]
			session.barrierNode = barrierID
			session.outstanding++
			session.mergeNodes[barrierID] = struct{}{}
			this.moeMergeOwners[barrierID] = nodeID
			mergeNodeIDs = append(mergeNodeIDs, barrierID)
			if event.Metadata != nil {
				event.Metadata["moe_barrier_node"] = barrierID
			}
		}
	}

	if event.Metadata != nil {
		event.Metadata["pending_experts"] = session.outstanding
	}

	if session.outstanding > 0 {
		this.deferSuccessorsForSession(session)
		this.moeSessions[nodeID] = session
	} else {
		this.finalizeMoeSession(nodeID, session)
	}
}

func (this *HostOrchestrator) buildExpertCommandGroup(event *HostEvent, expertID int) []CommandDescriptor {
	if event == nil {
		return nil
	}

	rows := positiveOrFallback(event.Tokens, 128)
	cols := positiveOrFallback(event.Features, 128)
	kDim := positiveOrFallback(metadataInt(event.Metadata, "inner_dim", event.Features), cols)

	activationBytes := positiveOrFallback(event.ActivationBytes, rows*kDim*2)
	weightBytes := positiveOrFallback(event.WeightBytes, kDim*cols*2)
	outputBytes := positiveOrFallback(event.OutputBytes, rows*cols*2)

	digitalID := event.DigitalChiplet
	if digitalID < 0 && this.config != nil && this.config.NumDigitalChiplets > 0 {
		digitalID = 0
	}
	if this.config != nil && this.config.NumDigitalChiplets > 0 {
		digitalID = ((digitalID % this.config.NumDigitalChiplets) + this.config.NumDigitalChiplets) % this.config.NumDigitalChiplets
	}

	rramID := expertID
	if this.config != nil && this.config.NumRramChiplets > 0 {
		rramID = ((expertID % this.config.NumRramChiplets) + this.config.NumRramChiplets) % this.config.NumRramChiplets
	}
	if rramID < 0 {
		rramID = 0
	}

	stageLatency := positiveOrFallback(metadataInt(event.Metadata, "stage_latency", 28), 28)
	execLatency := positiveOrFallback(metadataInt(event.Metadata, "execute_latency", 56), 56)
	postLatency := positiveOrFallback(metadataInt(event.Metadata, "post_latency", 12), 12)
	mergeLatency := positiveOrFallback(metadataInt(event.Metadata, "merge_latency", 24), 24)

	bandwidthDr := 0
	bandwidthRd := 0
	if this.config != nil {
		bandwidthDr = int(this.config.TransferBandwidthDr)
		bandwidthRd = int(this.config.TransferBandwidthRd)
	}

	transferInMeta := cloneMetadata(event.Metadata)
	if transferInMeta == nil {
		transferInMeta = make(map[string]interface{})
	}
	if bandwidthDr > 0 {
		transferInMeta["transfer_bandwidth_bytes"] = bandwidthDr
	}
	transferInLatency := positiveOrFallback(
		metadataInt(event.Metadata, "transfer_in_latency", this.applyTransferLatencyEstimator(
			&TransferLatencyQuery{
				Stage:      "moe_transfer_in",
				Bytes:      int64(activationBytes),
				SrcDigital: digitalID,
				DstRram:    rramID,
				Metadata:   transferInMeta,
			},
			estimateTransferLatency(activationBytes, bandwidthDr),
		)),
		8,
	)

	transferOutMeta := cloneMetadata(event.Metadata)
	if transferOutMeta == nil {
		transferOutMeta = make(map[string]interface{})
	}
	if bandwidthRd > 0 {
		transferOutMeta["transfer_bandwidth_bytes"] = bandwidthRd
	}
	transferOutLatency := positiveOrFallback(
		metadataInt(event.Metadata, "transfer_out_latency", this.applyTransferLatencyEstimator(
			&TransferLatencyQuery{
				Stage:      "moe_transfer_out",
				Bytes:      int64(outputBytes),
				SrcRram:    rramID,
				DstDigital: digitalID,
				Metadata:   transferOutMeta,
			},
			estimateTransferLatency(outputBytes, bandwidthRd),
		)),
		8,
	)

	bufferID := event.BufferID
	if bufferID < 0 {
		bufferID = 0
	}

	expertMeta := func(op string) map[string]interface{} {
		meta := cloneMetadata(event.Metadata)
		if meta == nil {
			meta = make(map[string]interface{})
		}
		meta["op"] = op
		meta["expert_id"] = expertID
		meta["selected_experts"] = append([]int(nil), event.SelectedExperts...)
		meta["candidate_experts"] = append([]int(nil), event.CandidateExperts...)
		meta["top_k"] = event.TopK
		meta["tokens"] = rows
		meta["features"] = cols
		meta["inner_dim"] = kDim
		meta["activation_bytes"] = activationBytes
		meta["weight_bytes"] = weightBytes
		meta["output_bytes"] = outputBytes
		return meta
	}

	transferInDescMeta := expertMeta("moe_expert_transfer_in")
	if bandwidthDr > 0 {
		transferInDescMeta["transfer_bandwidth_bytes"] = bandwidthDr
	}
	group := []CommandDescriptor{
		{
			Kind:         CommandKindTransferSchedule,
			Target:       TaskTargetTransfer,
			ChipletID:    int32(rramID),
			Queue:        int32(digitalID),
			PayloadBytes: uint32(activationBytes),
			Latency:      int32(transferInLatency),
			Flags:        TransferFlagDigitalToRram,
			BufferID:     int32(bufferID),
			Metadata:     transferInDescMeta,
		},
		{
			Kind:         CommandKindRramStageAct,
			Target:       TaskTargetRram,
			ChipletID:    int32(rramID),
			PayloadBytes: uint32(activationBytes),
			PayloadAddr:  uint32(weightBytes),
			Aux0:         uint32(rows),
			Aux1:         uint32(cols),
			Aux2:         uint32(kDim),
			Aux3:         uint32(outputBytes),
			Latency:      int32(stageLatency),
			Metadata:     expertMeta("moe_expert_stage"),
		},
		{
			Kind:         CommandKindRramExecute,
			Target:       TaskTargetRram,
			ChipletID:    int32(rramID),
			PayloadBytes: uint32(activationBytes),
			PayloadAddr:  uint32(weightBytes),
			Aux0:         uint32(rows),
			Aux1:         uint32(cols),
			Aux2:         uint32(kDim),
			Aux3:         uint32(outputBytes),
			Latency:      int32(execLatency),
			Metadata:     expertMeta("moe_expert_execute"),
		},
		{
			Kind:         CommandKindRramPost,
			Target:       TaskTargetRram,
			ChipletID:    int32(rramID),
			PayloadBytes: uint32(activationBytes),
			PayloadAddr:  uint32(weightBytes),
			Aux0:         uint32(rows),
			Aux1:         uint32(cols),
			Aux2:         uint32(kDim),
			Aux3:         uint32(outputBytes),
			Latency:      int32(postLatency),
			Metadata:     expertMeta("moe_expert_post"),
		},
	}

	transferOutDescMeta := expertMeta("moe_expert_transfer_out")
	if bandwidthRd > 0 {
		transferOutDescMeta["transfer_bandwidth_bytes"] = bandwidthRd
	}

	group = append(group,
		CommandDescriptor{
			Kind:         CommandKindTransferSchedule,
			Target:       TaskTargetTransfer,
			ChipletID:    int32(digitalID),
			Queue:        int32(rramID),
			PayloadBytes: uint32(outputBytes),
			Latency:      int32(transferOutLatency),
			Flags:        TransferFlagRramToDigital,
			BufferID:     int32(bufferID),
			Metadata:     transferOutDescMeta,
		},
		CommandDescriptor{
			Kind:      CommandKindPeElementwise,
			Target:    TaskTargetDigital,
			ChipletID: int32(digitalID),
			Queue:     int32(cols),
			Aux0:      uint32(rows),
			Aux1:      uint32(cols),
			Aux2:      uint32(event.TopK),
			Latency:   int32(mergeLatency),
			BufferID:  int32(bufferID),
			Metadata:  expertMeta("moe_expert_merge"),
		},
	)

	return group
}

func (this *HostOrchestrator) assignNodesToBatch(nodeIDs []int, batchID int) {
	if len(nodeIDs) == 0 || batchID < 0 {
		return
	}
	if this.nodeBatch == nil {
		this.nodeBatch = make(map[int]int)
	}
	for _, id := range nodeIDs {
		this.nodeBatch[id] = batchID
	}
	if this.batchOutstanding == nil {
		this.batchOutstanding = make(map[int]int)
	}
	if _, exists := this.batchOutstanding[batchID]; !exists {
		this.batchOutstanding[batchID] = 0
		this.reactivateBatch(batchID)
	}
	this.batchOutstanding[batchID] += len(nodeIDs)
}

func (this *HostOrchestrator) reactivateBatch(batchID int) {
	if !this.streamEnabled {
		return
	}
	this.streamActiveBatches++
	if this.streamBatchesCompleted > 0 {
		this.streamBatchesCompleted--
	}
}

func (this *HostOrchestrator) Outstanding() outstandingTracker {
	if this == nil {
		return outstandingTracker{}
	}
	return this.outstanding.Clone()
}

// HasPendingWork reports whether any tasks remain to be issued or completed.
// It accounts for ready nodes, in-flight tasks, and additional streaming batches
// that have not yet been instantiated.
func (this *HostOrchestrator) HasPendingWork() bool {
	if this == nil {
		return false
	}

	if len(this.readyQueue) > 0 {
		return true
	}

	if len(this.inFlight) > 0 {
		return true
	}

	if !this.streamEnabled {
		return false
	}

	if this.streamTemplate == nil {
		return false
	}

	if this.streamActiveBatches > 0 {
		return true
	}

	if this.streamTotalBatches > 0 && this.streamBatchesIssued < this.streamTotalBatches {
		return true
	}

	return false
}

func (this *HostOrchestrator) createTaskFromNode(node *OpNode) *Task {
	latency := node.Latency
	var payload interface{}

	if cmd, ok := node.Payload.(*CommandDescriptor); ok && cmd != nil {
		payload = cmd
		if cmd.Latency > 0 {
			latency = int(cmd.Latency)
		}
		switch node.Target {
		case TaskTargetDigital:
			chipletID := int(cmd.ChipletID)
			if chipletID < 0 && this.topology.Digital.NumChiplets > 0 {
				chipletID = this.digitalRR % this.topology.Digital.NumChiplets
				cmd.ChipletID = int32(chipletID)
			}
			this.digitalRR++
			this.lastDigitalID = chipletID
		case TaskTargetRram:
			chipletID := int(cmd.ChipletID)
			if chipletID < 0 && this.topology.Rram.NumChiplets > 0 {
				chipletID = this.rramRR % this.topology.Rram.NumChiplets
				cmd.ChipletID = int32(chipletID)
			}
			this.rramRR++
			this.lastRramID = chipletID
		case TaskTargetTransfer:
			bandwidth := this.transferBandwidthBytes
			transferHops := 0
			if cmd.Flags&TransferFlagDirectionMask == TransferFlagDigitalToRram {
				if this.config.TransferBandwidthDr > 0 {
					bandwidth = this.config.TransferBandwidthDr
				}
				if src := int(cmd.Queue); src >= 0 {
					this.lastDigitalID = src
				}
				if dst := int(cmd.ChipletID); dst >= 0 {
					this.lastRramID = dst
				}
				srcDigital := int(cmd.Queue)
				if srcDigital < 0 {
					if this.lastDigitalID >= 0 {
						srcDigital = this.lastDigitalID
					} else if this.topology != nil && this.topology.Digital.NumChiplets > 0 {
						srcDigital = 0
					}
					cmd.Queue = int32(srcDigital)
				}
				dstRram := int(cmd.ChipletID)
				if dstRram < 0 {
					if this.topology != nil && this.topology.Rram.NumChiplets > 0 {
						dstRram = this.rramRR % this.topology.Rram.NumChiplets
					} else {
						dstRram = 0
					}
					cmd.ChipletID = int32(dstRram)
					this.lastRramID = dstRram
				}
				if cmd.Metadata == nil {
					cmd.Metadata = make(map[string]interface{})
				}
				cmd.Metadata[MetadataKeySrcDigital] = srcDigital
				cmd.Metadata[MetadataKeyDstRram] = dstRram
				if this.topology != nil {
					if coord, ok := this.topology.DigitalCoord(srcDigital); ok {
						cmd.MeshSrcX = int32(coord.X)
						cmd.MeshSrcY = int32(coord.Y)
					}
					if coord, ok := this.topology.RramCoord(dstRram); ok {
						cmd.MeshDstX = int32(coord.X)
						cmd.MeshDstY = int32(coord.Y + this.topology.Digital.MeshRows + 1)
					}
					transferHops = this.topology.DigitalToRramHopDistance(srcDigital, dstRram)
				}
			} else if cmd.Flags&TransferFlagDirectionMask == TransferFlagRramToDigital {
				if this.config.TransferBandwidthRd > 0 {
					bandwidth = this.config.TransferBandwidthRd
				}
				if src := int(cmd.Queue); src >= 0 {
					this.lastRramID = src
				}
				if dst := int(cmd.ChipletID); dst >= 0 {
					this.lastDigitalID = dst
				}
				srcRram := int(cmd.Queue)
				if srcRram < 0 {
					if this.lastRramID >= 0 {
						srcRram = this.lastRramID
					} else if this.topology != nil && this.topology.Rram.NumChiplets > 0 {
						srcRram = 0
					}
					cmd.Queue = int32(srcRram)
				}
				dstDigital := int(cmd.ChipletID)
				if dstDigital < 0 {
					if this.topology != nil && this.topology.Digital.NumChiplets > 0 {
						dstDigital = this.digitalRR % this.topology.Digital.NumChiplets
					} else {
						dstDigital = 0
					}
					cmd.ChipletID = int32(dstDigital)
					this.lastDigitalID = dstDigital
				}
				if cmd.Metadata == nil {
					cmd.Metadata = make(map[string]interface{})
				}
				cmd.Metadata[MetadataKeySrcRram] = srcRram
				cmd.Metadata[MetadataKeyDstDigital] = dstDigital
				if this.topology != nil {
					if coord, ok := this.topology.RramCoord(srcRram); ok {
						cmd.MeshSrcX = int32(coord.X)
						cmd.MeshSrcY = int32(coord.Y + this.topology.Digital.MeshRows + 1)
					}
					if coord, ok := this.topology.DigitalCoord(dstDigital); ok {
						cmd.MeshDstX = int32(coord.X)
						cmd.MeshDstY = int32(coord.Y)
					}
					transferHops = this.topology.RramToDigitalHopDistance(srcRram, dstDigital)
				}
			}
			if cmd.PayloadBytes > 0 && bandwidth > 0 {
				bytes := int64(cmd.PayloadBytes)
				cycles := int((bytes + bandwidth - 1) / bandwidth)
				if cycles <= 0 {
					cycles = 1
				}
				latency = cycles
			}
			if transferHops > 0 {
				latency += transferHops
				if cmd.Metadata == nil {
					cmd.Metadata = make(map[string]interface{})
				}
				cmd.Metadata[MetadataKeyTransferHops] = transferHops
			}
		case TaskTargetHost:
			// Host 指令无需 RR 轮询，后续可扩展 Thread/Context。
		}
	} else {
		payloadMap := make(map[string]interface{})
		if str, ok := node.Payload.(string); ok {
			payloadMap["stage"] = str
		}

		switch node.Target {
		case TaskTargetDigital:
			if this.topology.Digital.NumChiplets > 0 {
				chipletID := this.digitalRR % this.topology.Digital.NumChiplets
				this.digitalRR++
				payloadMap["chiplet_id"] = chipletID
				this.lastDigitalID = chipletID
			}
		case TaskTargetRram:
			if this.topology.Rram.NumChiplets > 0 {
				chipletID := this.rramRR % this.topology.Rram.NumChiplets
				this.rramRR++
				payloadMap["chiplet_id"] = chipletID
				this.lastRramID = chipletID
			}
			this.populateSampleCimPayload(payloadMap)
		case TaskTargetTransfer:
			if stage, ok := payloadMap["stage"].(string); ok {
				bytes := this.transferBytesEstimate
				bandwidth := this.transferBandwidthBytes
				if strings.EqualFold(stage, "transfer_to_rram") && this.config.TransferBandwidthDr > 0 {
					bandwidth = this.config.TransferBandwidthDr
				} else if strings.EqualFold(stage, "transfer_to_digital") && this.config.TransferBandwidthRd > 0 {
					bandwidth = this.config.TransferBandwidthRd
				}
				if bandwidth <= 0 {
					bandwidth = 4096
				}
				payloadMap["transfer_bandwidth_bytes"] = bandwidth
				payloadMap["bytes"] = int(bytes)
				stageLower := strings.ToLower(stage)
				if stageLower == "transfer_to_rram" {
					if this.lastDigitalID < 0 && this.topology.Digital.NumChiplets > 0 {
						this.lastDigitalID = 0
					}
					src := this.lastDigitalID
					payloadMap["src_digital"] = src
					if this.topology.Rram.NumChiplets > 0 {
						chipletID := this.rramRR % this.topology.Rram.NumChiplets
						this.rramRR++
						this.lastRramID = chipletID
						payloadMap["dst_rram"] = chipletID
						hops := 0
						if this.topology != nil {
							hops = this.topology.DigitalToRramHopDistance(src, chipletID)
						}
						payloadMap["transfer_hops"] = hops
						latency = this.applyTransferLatencyEstimator(&TransferLatencyQuery{
							Stage:      stageLower,
							Bytes:      bytes,
							SrcDigital: src,
							DstRram:    chipletID,
							Metadata:   payloadMap,
						}, transferLatencyFromBandwidth(bytes, int64(bandwidth), hops))
					}
				} else if stageLower == "transfer_to_digital" {
					if this.lastRramID < 0 && this.topology.Rram.NumChiplets > 0 {
						this.lastRramID = 0
					}
					src := this.lastRramID
					payloadMap["src_rram"] = src
					if this.lastDigitalID < 0 && this.topology.Digital.NumChiplets > 0 {
						this.lastDigitalID = 0
					}
					dst := this.lastDigitalID
					payloadMap["dst_digital"] = dst
					hops := 0
					if this.topology != nil {
						hops = this.topology.RramToDigitalHopDistance(src, dst)
					}
					payloadMap["transfer_hops"] = hops
					latency = this.applyTransferLatencyEstimator(&TransferLatencyQuery{
						Stage:      stageLower,
						Bytes:      bytes,
						SrcRram:    src,
						DstDigital: dst,
						Metadata:   payloadMap,
					}, transferLatencyFromBandwidth(bytes, int64(bandwidth), hops))
				}
			}
		}

		payload = payloadMap
	}

	if latency <= 0 {
		latency = 1
	}

	task := &Task{
		NodeID:  node.ID,
		Target:  node.Target,
		Type:    node.Type,
		Latency: latency,
		Payload: payload,
	}
	if cmd, ok := payload.(*CommandDescriptor); ok && cmd != nil {
		task.Opcode = cmd.Kind
		task.ExecDomain = cmd.ExecDomain
		if task.ExecDomain == ExecDomainUndefined {
			task.ExecDomain = defaultExecDomainForKind(cmd.Kind)
		}
		task.MeshSrcX = int(cmd.MeshSrcX)
		task.MeshSrcY = int(cmd.MeshSrcY)
		task.MeshDstX = int(cmd.MeshDstX)
		task.MeshDstY = int(cmd.MeshDstY)
		task.HostAddress = cmd.CacheLine
		task.BufferID = int(cmd.BufferID)
		task.SubOperation = cmd.SubOp
		task.RequestBytes = int64(cmd.PayloadBytes)
		if cmd.Metadata != nil {
			task.Metadata = cmd.Metadata
		}
	}
	return task
}

func transferLatencyFromBandwidth(bytes int64, bandwidth int64, hops int) int {
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

type resourceUsage struct {
	Digital  int64
	Rram     int64
	Transfer int64
	Dma      int64
}

type HostEvent struct {
	Kind             CommandKind
	Metadata         map[string]interface{}
	BufferID         int
	TopK             int
	Tokens           int
	Features         int
	CandidateExperts []int
	SelectedExperts  []int
	ActivationBytes  int
	WeightBytes      int
	OutputBytes      int
	DigitalChiplet   int
}

type moeDispatchSession struct {
	parentNode  int
	digitalID   int
	bufferID    int
	topK        int
	tokens      int
	features    int
	expertIDs   []int
	mergeNodes  map[int]struct{}
	successors  []int
	barrierNode int
	outstanding int
}

func (s *moeDispatchSession) mergeList() []int {
	if s == nil || len(s.mergeNodes) == 0 {
		return nil
	}
	ids := make([]int, 0, len(s.mergeNodes))
	for id := range s.mergeNodes {
		ids = append(ids, id)
	}
	return ids
}

func (this *HostOrchestrator) finalizeMoeSession(owner int, session *moeDispatchSession) {
	if session == nil {
		session = this.moeSessions[owner]
	}
	if session != nil {
		for mergeID := range session.mergeNodes {
			delete(this.moeMergeOwners, mergeID)
		}
	}
	delete(this.moeSessions, owner)
	if session != nil && debugIssueCounter < debugMaxDebugEvents {
		fmt.Printf("[chiplet-debug] host_moe_complete node=%d experts=%d\n", owner, len(session.expertIDs))
	}
}

func (this *HostOrchestrator) SetTransferLatencyEstimator(estimator TransferLatencyEstimator) {
	this.transferEstimator = estimator
}

func (this *HostOrchestrator) applyTransferLatencyEstimator(query *TransferLatencyQuery, fallback int) int {
	if fallback <= 0 {
		fallback = 1
	}
	if query == nil {
		return fallback
	}
	if this.transferEstimator != nil {
		if result, ok := this.transferEstimator(*query); ok && result > 0 {
			return result
		}
	}
	return fallback
}

func (this *HostOrchestrator) removeFromReadyQueue(nodeID int) {
	if len(this.readyQueue) == 0 {
		return
	}
	filtered := this.readyQueue[:0]
	for _, id := range this.readyQueue {
		if id == nodeID {
			continue
		}
		filtered = append(filtered, id)
	}
	this.readyQueue = filtered
}

type outstandingTracker struct {
	Digital  int64
	Rram     int64
	Transfer int64
	Dma      int64
}

func (t *outstandingTracker) Reset() {
	t.Digital = 0
	t.Rram = 0
	t.Transfer = 0
	t.Dma = 0
}

func (t *outstandingTracker) Clone() outstandingTracker {
	return outstandingTracker{Digital: t.Digital, Rram: t.Rram, Transfer: t.Transfer, Dma: t.Dma}
}

func (this *HostOrchestrator) estimateResourceUsage(node *OpNode) resourceUsage {
	if node == nil {
		return resourceUsage{}
	}

	if cmd, ok := node.Payload.(*CommandDescriptor); ok && cmd != nil {
		return estimateUsageFromCommand(cmd, node.Target, this.transferBytesEstimate)
	}

	// Legacy/string payload节点不做资源限制，保持旧流水线行为。
	return resourceUsage{}
}

func estimateUsageFromCommand(cmd *CommandDescriptor, fallback TaskTarget, defaultBytes int64) resourceUsage {
	if cmd == nil {
		return resourceUsage{}
	}

	target := cmd.Target
	if target != TaskTargetDigital && target != TaskTargetRram && target != TaskTargetTransfer {
		target = fallback
	}

	const bytesPerElement = 2
	switch target {
	case TaskTargetDigital:
		rows := int64(cmd.Aux0)
		if rows <= 0 {
			rows = int64(cmd.Queue)
		}
		cols := int64(cmd.Aux1)
		if cols <= 0 {
			cols = int64(cmd.PayloadAddr)
		}
		k := int64(cmd.Aux2)
		if k <= 0 {
			k = int64(cmd.PayloadBytes)
		}
		if rows <= 0 {
			rows = 128
		}
		if cols <= 0 {
			cols = 128
		}
		if k <= 0 {
			k = 64
		}
		input := rows * k * bytesPerElement
		weight := k * cols * bytesPerElement
		output := rows * cols * bytesPerElement
		return resourceUsage{Digital: input + weight + output}
	case TaskTargetRram:
		activation := int64(cmd.PayloadBytes)
		if activation <= 0 {
			activation = defaultBytes
		}
		weight := int64(cmd.PayloadAddr)
		if weight <= 0 {
			weight = activation
		}
		return resourceUsage{Rram: activation + weight}
	case TaskTargetTransfer:
		bytes := int64(cmd.PayloadBytes)
		if bytes <= 0 {
			bytes = defaultBytes
		}
		return resourceUsage{Transfer: bytes, Dma: bytes}
	case TaskTargetHost:
		return resourceUsage{}
	default:
		return resourceUsage{}
	}
}

// AppendCommandGroup dynamically materializes a set of commands as OpNodes and
// wires them into the DAG. baseDeps 表示所有命令共享的依赖；chain=true 时会在组内建立线性依赖。
func (this *HostOrchestrator) AppendCommandGroup(commands []CommandDescriptor, baseDeps []int, chain bool) []int {
	if this == nil || len(commands) == 0 {
		return nil
	}
	if this.graph == nil {
		this.graph = NewOpGraph()
	}
	if baseDeps == nil {
		baseDeps = make([]int, 0)
	}
	ids := make([]int, 0, len(commands))
	prevID := -1
	for i := range commands {
		cmdCopy := commands[i]
		nodeID := this.nextNodeID
		this.nextNodeID++
		cmdCopy.ID = int32(nodeID)
		node := &OpNode{
			ID:      nodeID,
			Type:    taskTypeForCommand(&cmdCopy),
			Target:  cmdCopy.Target,
			Latency: defaultLatency(&cmdCopy, this.minWaitCycles),
			Payload: &cmdCopy,
		}
		deps := make([]int, 0)
		if len(cmdCopy.Dependencies) > 0 {
			for _, dep := range cmdCopy.Dependencies {
				deps = append(deps, int(dep))
			}
		} else if len(baseDeps) > 0 {
			deps = append(deps, baseDeps...)
		}
		if chain && prevID >= 0 {
			deps = append(deps, prevID)
		}
		if len(deps) > 0 {
			node.Deps = dedupeIntSlice(deps)
		}
		this.graph.AddNode(node)
		this.remainingDeps[nodeID] = len(node.Deps)
		if len(node.Deps) == 0 {
			this.readyQueue = append(this.readyQueue, nodeID)
		}
		this.nodeBatch[nodeID] = 0
		ids = append(ids, nodeID)
		prevID = nodeID
	}
	return ids
}

func taskTypeForCommand(cmd *CommandDescriptor) TaskType {
	if cmd == nil {
		return TaskTypeDataMove
	}
	switch cmd.Target {
	case TaskTargetDigital:
		return TaskTypeCompute
	case TaskTargetRram:
		return TaskTypeCim
	case TaskTargetTransfer:
		return TaskTypeDataMove
	case TaskTargetHost:
		return TaskTypeSync
	default:
		return TaskTypeDataMove
	}
}

func defaultLatency(cmd *CommandDescriptor, fallback int) int {
	if cmd != nil && cmd.Latency > 0 {
		return int(cmd.Latency)
	}
	if fallback > 0 {
		return fallback
	}
	return 1
}

func dedupeIntSlice(values []int) []int {
	if len(values) <= 1 {
		return values
	}
	seen := make(map[int]struct{}, len(values))
	result := make([]int, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}
	return result
}

func (this *HostOrchestrator) deferSuccessorsForSession(session *moeDispatchSession) {
	if this == nil || session == nil || this.graph == nil {
		return
	}
	merges := session.mergeList()
	if len(merges) == 0 {
		return
	}
	merges = dedupeIntSlice(merges)
	session.successors = append([]int(nil), session.successors...)
	for _, succ := range session.successors {
		node := this.graph.Nodes[succ]
		if node == nil {
			continue
		}
		removedParent := false
		deps := make([]int, 0, len(node.Deps)+len(merges))
		for _, dep := range node.Deps {
			if dep == session.parentNode {
				removedParent = true
				continue
			}
			deps = append(deps, dep)
		}
		if !removedParent {
			continue
		}
		deps = append(deps, merges...)
		node.Deps = dedupeIntSlice(deps)
		if count, ok := this.remainingDeps[succ]; ok {
			count--
			if count < 0 {
				count = 0
			}
			this.remainingDeps[succ] = count
		}
		this.remainingDeps[succ] += len(merges)
		this.removeFromReadyQueue(succ)
		for _, mergeID := range merges {
			this.graph.AddEdge(mergeID, succ)
		}
		this.graph.RemoveEdge(session.parentNode, succ)
	}
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

func metadataInt(meta map[string]interface{}, key string, fallback int) int {
	if meta == nil {
		return fallback
	}
	if value, exists := meta[key]; exists {
		switch v := value.(type) {
		case int:
			return v
		case int32:
			return int(v)
		case int64:
			return int(v)
		case float64:
			return int(v)
		case string:
			if trimmed := strings.TrimSpace(v); trimmed != "" {
				if parsed, err := strconv.Atoi(trimmed); err == nil {
					return parsed
				}
			}
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
	switch v := raw.(type) {
	case []int:
		return append([]int(nil), v...)
	case []interface{}:
		result := make([]int, 0, len(v))
		for _, item := range v {
			switch iv := item.(type) {
			case int:
				result = append(result, iv)
			case int32:
				result = append(result, int(iv))
			case int64:
				result = append(result, int(iv))
			case float64:
				result = append(result, int(iv))
			case string:
				if trimmed := strings.TrimSpace(iv); trimmed != "" {
					if parsed, err := strconv.Atoi(trimmed); err == nil {
						result = append(result, parsed)
					}
				}
			}
		}
		return result
	default:
		return nil
	}
}

func positiveOrFallback(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func fallbackExperts(candidates []int, topK int) []int {
	if topK <= 0 {
		topK = 1
	}
	if len(candidates) == 0 {
		result := make([]int, 0, topK)
		for i := 0; i < topK; i++ {
			result = append(result, i)
		}
		return result
	}
	if len(candidates) <= topK {
		return append([]int(nil), candidates...)
	}
	result := make([]int, 0, topK)
	seen := make(map[int]struct{}, len(candidates))
	for _, cand := range candidates {
		if len(result) >= topK {
			break
		}
		if _, exists := seen[cand]; exists {
			continue
		}
		seen[cand] = struct{}{}
		result = append(result, cand)
	}
	for len(result) < topK {
		result = append(result, candidates[len(result)%len(candidates)])
	}
	return result
}

func estimateTransferLatency(bytes int, bandwidth int) int {
	if bytes <= 0 {
		return 8
	}
	if bandwidth <= 0 {
		return 8
	}
	return latencyFromBandwidth(bytes, bandwidth)
}

func latencyFromBandwidth(bytes int, bandwidth int) int {
	if bandwidth <= 0 {
		return 8
	}
	cycles := bytes / bandwidth
	if bytes%bandwidth != 0 {
		cycles++
	}
	if cycles <= 0 {
		cycles = 1
	}
	return cycles
}

func (this *HostOrchestrator) populateSampleCimPayload(payload map[string]interface{}) {
	if payload == nil {
		return
	}
	stage, ok := payload["stage"].(string)
	if !ok || !strings.EqualFold(stage, "cim") {
		return
	}

	ensure := func(key string, value interface{}) {
		if _, exists := payload[key]; !exists {
			payload[key] = value
		}
	}

	activationBits := 12
	sliceBits := 2
	signs := []int{0, 1}
	exponents := []int{15, 14}
	mantissas := []int{0, 0}
	activations := []float64{1.0, -0.5}
	weights := []float64{3.0, -2.0}
	scale := 0.1
	zeroPoint := 0

	ensure("activation_bits", activationBits)
	ensure("slice_bits", sliceBits)
	ensure("signs", signs)
	ensure("exponents", exponents)
	ensure("mantissas", mantissas)
	ensure("activation_size", len(activations))
	ensure("weight_size", len(weights))

	pre := rram.NewPreprocessor(activationBits, sliceBits)
	_, maxExp, pSum, aSum := pre.Prepare(signs, exponents, mantissas)

	pulseCount := (activationBits + sliceBits - 1) / sliceBits
	if pulseCount < 1 {
		pulseCount = 1
	}
	adcSamples := len(weights) * pulseCount
	preCycles := pulseCount
	postCycles := 2 + pre.ActivationBitWidth()/16

	expected := 0.0
	for i := range activations {
		expected += activations[i] * (weights[i] - float64(zeroPoint))
	}
	expected *= scale

	actualExp := (maxExp - 10) - 15
	pow := math.Pow(2.0, float64(actualExp))
	if pow == 0 {
		pow = 1
	}
	o := expected / scale
	oM := o / pow
	iSum := int64(math.Round(float64(pSum*8) + oM))

	ensure("pulse_count", pulseCount)
	ensure("adc_samples", adcSamples)
	ensure("pre_cycles", preCycles)
	ensure("post_cycles", postCycles)
	ensure("scale", scale)
	ensure("zero_point", zeroPoint)
	ensure("i_sum", iSum)
	ensure("p_sum", int64(pSum))
	ensure("max_exponent", maxExp)
	ensure("a_sum", aSum)
	ensure("expected", expected)
}
