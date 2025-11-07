package chiplet

import "testing"

func TestHostOrchestratorAdvanceBatchesReadyTasks(t *testing.T) {
	t.Parallel()

	config := &Config{
		NumDigitalChiplets:  2,
		NumRramChiplets:     1,
		TransferBandwidthDr: 8192,
		TransferBandwidthRd: 8192,
		HostLimitResources:  false,
	}
	topology := BuildTopology(config)

	orch := new(HostOrchestrator)
	orch.Init(config, topology, "")
	defer orch.Fini()

	orch.maxIssuePerCycle = 8

	graph := NewOpGraph()
	graph.AddNode(&OpNode{
		ID:      0,
		Type:    TaskTypeCompute,
		Target:  TaskTargetDigital,
		Latency: 10,
		Payload: "expert0",
	})
	graph.AddNode(&OpNode{
		ID:      1,
		Type:    TaskTypeCompute,
		Target:  TaskTargetDigital,
		Latency: 8,
		Payload: "expert1",
	})
	graph.AddNode(&OpNode{
		ID:      2,
		Type:    TaskTypeDataMove,
		Target:  TaskTargetTransfer,
		Latency: 6,
		Deps:    []int{0, 1},
		Payload: "merge",
	})
	orch.setGraph(graph)

	tasks := orch.Advance()
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks from first advance, got %d", len(tasks))
	}
	for _, task := range tasks {
		if task == nil {
			t.Fatalf("task should not be nil")
		}
	}

	if next := orch.Advance(); next != nil {
		t.Fatalf("expected nil tasks after issuing first wave, got %d", len(next))
	}

	for _, task := range tasks {
		orch.NotifyTaskCompletion(task.NodeID)
	}

	next := orch.Advance()
	if len(next) != 1 {
		t.Fatalf("expected transfer task after dependencies resolved, got %d", len(next))
	}
	if next[0].Target != TaskTargetTransfer {
		t.Fatalf("expected transfer task target, got %v", next[0].Target)
	}
}

func TestHostOrchestratorRespectsBufferLimits(t *testing.T) {
	t.Parallel()

	config := &Config{
		NumDigitalChiplets:      2,
		NumRramChiplets:         1,
		TransferBandwidthDr:     4096,
		TransferBandwidthRd:     4096,
		DigitalActivationBuffer: 200 << 10, // ~204 KB
		DigitalScratchBuffer:    100 << 10,
		HostLimitResources:      true,
	}
	topology := BuildTopology(config)

	orch := new(HostOrchestrator)
	orch.Init(config, topology, "")
	defer orch.Fini()

	graph := NewOpGraph()
	graph.AddNode(&OpNode{
		ID:      0,
		Type:    TaskTypeCompute,
		Target:  TaskTargetDigital,
		Latency: 10,
		Payload: &CommandDescriptor{
			ID:     0,
			Kind:   CommandKindPeGemm,
			Target: TaskTargetDigital,
			Aux0:   256,
			Aux1:   256,
			Aux2:   128,
			Queue:  256,
		},
	})
	graph.AddNode(&OpNode{
		ID:      1,
		Type:    TaskTypeCompute,
		Target:  TaskTargetDigital,
		Latency: 10,
		Payload: &CommandDescriptor{
			ID:     1,
			Kind:   CommandKindPeGemm,
			Target: TaskTargetDigital,
			Aux0:   256,
			Aux1:   256,
			Aux2:   128,
			Queue:  256,
		},
	})
	graph.AddNode(&OpNode{
		ID:      2,
		Type:    TaskTypeDataMove,
		Target:  TaskTargetTransfer,
		Latency: 4,
		Deps:    []int{0, 1},
		Payload: &CommandDescriptor{
			ID:           2,
			Kind:         CommandKindTransferSchedule,
			Target:       TaskTargetTransfer,
			PayloadBytes: 64 * 1024,
		},
	})
	orch.setGraph(graph)

	first := orch.Advance()
	if len(first) != 1 {
		t.Fatalf("expected 1 task due to buffer limit, got %d", len(first))
	}
	firstID := first[0].NodeID

	if next := orch.Advance(); len(next) != 0 {
		t.Fatalf("expected no additional tasks while buffer occupied, got %d", len(next))
	}

	orch.NotifyTaskCompletion(firstID)

	second := orch.Advance()
	if len(second) != 1 {
		t.Fatalf("expected second task after release, got %d", len(second))
	}
	secondID := second[0].NodeID
	if secondID == firstID {
		t.Fatalf("expected different node after release, got %d twice", secondID)
	}
}

func TestHostOrchestratorStreamsBatchesUsingWatermarks(t *testing.T) {
	t.Parallel()

	config := &Config{
		NumDigitalChiplets:      2,
		NumRramChiplets:         1,
		HostStreamTotalBatches:  3,
		HostStreamLowWatermark:  1,
		HostStreamHighWatermark: 2,
	}
	topology := BuildTopology(config)

	orch := new(HostOrchestrator)
	orch.Init(config, topology, "")
	defer orch.Fini()

	graph := NewOpGraph()
	graph.AddNode(&OpNode{
		ID:      0,
		Type:    TaskTypeCompute,
		Target:  TaskTargetDigital,
		Latency: 4,
		Payload: "stage0",
	})
	graph.AddNode(&OpNode{
		ID:      1,
		Type:    TaskTypeCompute,
		Target:  TaskTargetDigital,
		Latency: 3,
		Deps:    []int{0},
		Payload: "stage1",
	})

	orch.setGraph(graph)
	orch.maxIssuePerCycle = 8

	if got := len(orch.readyQueue); got != 2 {
		t.Fatalf("expected ready queue to contain two roots, got %d", got)
	}
	if orch.readyQueue[0] == orch.readyQueue[1] {
		t.Fatalf("ready queue contains duplicate node id %d", orch.readyQueue[0])
	}
	if orch.maxDigitalPerCycle < 2 {
		t.Fatalf("expected maxDigitalPerCycle >= 2, got %d", orch.maxDigitalPerCycle)
	}

	firstWave := orch.Advance()
	if len(firstWave) != 2 {
		t.Fatalf("expected 2 root tasks from double-buffer batches, got %d (remaining ready queue=%d)", len(firstWave), len(orch.readyQueue))
	}

	batches := make(map[int]struct{})
	for _, task := range firstWave {
		if task == nil {
			t.Fatalf("nil task returned in first wave")
		}
		batchID, ok := orch.nodeBatch[task.NodeID]
		if !ok {
			t.Fatalf("missing batch mapping for node %d", task.NodeID)
		}
		batches[batchID] = struct{}{}
	}
	if len(batches) != 2 {
		t.Fatalf("expected tasks from two batches, got %d distinct batches", len(batches))
	}

	for _, task := range firstWave {
		orch.NotifyTaskCompletion(task.NodeID)
	}

	secondWave := orch.Advance()
	if len(secondWave) != 2 {
		t.Fatalf("expected 2 tail tasks for initial batches, got %d", len(secondWave))
	}
	for _, task := range secondWave {
		if task == nil {
			t.Fatalf("nil task returned in second wave")
		}
		batchID, ok := orch.nodeBatch[task.NodeID]
		if !ok {
			t.Fatalf("missing batch mapping for node %d in second wave", task.NodeID)
		}
		if batchID > 1 {
			t.Fatalf("expected batch 0 or 1 in second wave, got %d", batchID)
		}
		orch.NotifyTaskCompletion(task.NodeID)
	}

	thirdWave := orch.Advance()
	if len(thirdWave) != 1 {
		t.Fatalf("expected 1 root task for the final batch, got %d", len(thirdWave))
	}
	rootTask := thirdWave[0]
	batchID, ok := orch.nodeBatch[rootTask.NodeID]
	if !ok {
		t.Fatalf("missing batch mapping for node %d in third wave", rootTask.NodeID)
	}
	if batchID != 2 {
		t.Fatalf("expected batch id 2 for final batch root, got %d", batchID)
	}
	orch.NotifyTaskCompletion(rootTask.NodeID)

	fourthWave := orch.Advance()
	if len(fourthWave) != 1 {
		t.Fatalf("expected 1 tail task for the final batch, got %d", len(fourthWave))
	}
	if tailBatch, ok := orch.nodeBatch[fourthWave[0].NodeID]; !ok || tailBatch != 2 {
		t.Fatalf("expected final task to belong to batch 2, got batch %d (ok=%v)", tailBatch, ok)
	}
	orch.NotifyTaskCompletion(fourthWave[0].NodeID)

	if next := orch.Advance(); next != nil {
		t.Fatalf("expected no further tasks after streaming batches drained, got %d", len(next))
	}
}

func TestHostOrchestratorHasPendingWorkTracksLifecycle(t *testing.T) {
	t.Parallel()

	config := &Config{
		NumDigitalChiplets:      1,
		NumRramChiplets:         1,
		HostStreamTotalBatches:  2,
		HostStreamLowWatermark:  1,
		HostStreamHighWatermark: 2,
	}
	topology := BuildTopology(config)

	orch := new(HostOrchestrator)
	orch.Init(config, topology, "")
	defer orch.Fini()

	graph := NewOpGraph()
	graph.AddNode(&OpNode{
		ID:      0,
		Type:    TaskTypeCompute,
		Target:  TaskTargetDigital,
		Latency: 2,
	})
	graph.AddNode(&OpNode{
		ID:      1,
		Type:    TaskTypeCompute,
		Target:  TaskTargetDigital,
		Latency: 2,
		Deps:    []int{0},
	})
	orch.setGraph(graph)

	if !orch.HasPendingWork() {
		t.Fatalf("expected pending work before issuing any tasks")
	}

	for step := 0; step < 10 && orch.HasPendingWork(); step++ {
		for _, task := range orch.Advance() {
			orch.NotifyTaskCompletion(task.NodeID)
		}
	}

	if orch.HasPendingWork() {
		t.Fatalf("expected no remaining work once all tasks completed")
	}
}

func TestHostOrchestratorWatermarkComparison(t *testing.T) {
	t.Parallel()

	baseGraph := NewOpGraph()
	baseGraph.AddNode(&OpNode{
		ID:      0,
		Type:    TaskTypeCompute,
		Target:  TaskTargetDigital,
		Latency: 4,
	})
	baseGraph.AddNode(&OpNode{
		ID:      1,
		Type:    TaskTypeCompute,
		Target:  TaskTargetDigital,
		Latency: 4,
		Deps:    []int{0},
	})

	noStreamCfg := &Config{
		NumDigitalChiplets:     2,
		NumRramChiplets:        1,
		HostStreamTotalBatches: 1,
	}
	topology := BuildTopology(noStreamCfg)
	noStream := new(HostOrchestrator)
	noStream.Init(noStreamCfg, topology, "")
	defer noStream.Fini()
	noStream.setGraph(baseGraph)
	firstWave := noStream.Advance()
	if len(firstWave) != 1 {
		t.Fatalf("expected 1 task without streaming, got %d", len(firstWave))
	}

	streamCfg := &Config{
		NumDigitalChiplets:      2,
		NumRramChiplets:         1,
		HostStreamTotalBatches:  3,
		HostStreamLowWatermark:  1,
		HostStreamHighWatermark: 2,
	}
	stream := new(HostOrchestrator)
	stream.Init(streamCfg, topology, "")
	defer stream.Fini()
	stream.setGraph(baseGraph)
	streamWave := stream.Advance()
	if len(streamWave) != 2 {
		t.Fatalf("expected 2 tasks from streaming double-buffer, got %d", len(streamWave))
	}

	t.Logf("no_stream_first_wave=%d stream_first_wave=%d", len(firstWave), len(streamWave))
}
