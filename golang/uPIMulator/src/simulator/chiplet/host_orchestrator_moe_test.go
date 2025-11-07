package chiplet

import "testing"

func TestMoeSessionLifecycle(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		NumDigitalChiplets:     1,
		NumRramChiplets:        1,
		HostStreamTotalBatches: 1,
	}
	orch := new(HostOrchestrator)
	orch.Init(cfg, nil, "")
	defer orch.Fini()

	gatingID := 42
	gatingCmd := &CommandDescriptor{
		Kind:     CommandKindHostGatingFetch,
		Target:   TaskTargetHost,
		BufferID: 3,
	}

	graph := NewOpGraph()
	graph.AddNode(&OpNode{
		ID:      gatingID,
		Target:  TaskTargetHost,
		Payload: gatingCmd,
	})
	residualID := 43
	graph.AddNode(&OpNode{
		ID:      residualID,
		Target:  TaskTargetDigital,
		Latency: 1,
		Deps:    []int{gatingID},
		Payload: &CommandDescriptor{
			Kind:   CommandKindPeElementwise,
			Target: TaskTargetDigital,
		},
	})
	orch.setGraph(graph)

	event := &HostEvent{
		Kind:             CommandKindHostGatingFetch,
		BufferID:         3,
		TopK:             2,
		Tokens:           4,
		Features:         64,
		CandidateExperts: []int{0, 1},
		SelectedExperts:  []int{0, 1},
		ActivationBytes:  1024,
		WeightBytes:      512,
		OutputBytes:      256,
		DigitalChiplet:   0,
		Metadata:         map[string]interface{}{"op": "moe_gating_fetch"},
	}

	orch.handleGatingFetchEvent(gatingID, event)

	session, ok := orch.moeSessions[gatingID]
	if !ok || session == nil {
		t.Fatalf("expected moe session to be registered")
	}
	expectedOutstanding := len(event.SelectedExperts)
	if expectedOutstanding > 0 {
		expectedOutstanding++ // barrier node
	}
	if session.outstanding != expectedOutstanding {
		t.Fatalf("unexpected outstanding count: got %d want %d", session.outstanding, expectedOutstanding)
	}
	if session.barrierNode < 0 {
		t.Fatalf("expected barrier node to be registered")
	}

	mergeIDs := session.mergeList()
	if len(mergeIDs) != expectedOutstanding {
		t.Fatalf("expected %d tracked nodes, got %d", expectedOutstanding, len(mergeIDs))
	}

	var barrierID int = -1
	for _, mergeID := range mergeIDs {
		node := orch.graph.Nodes[mergeID]
		if node == nil {
			t.Fatalf("merge node %d missing in graph", mergeID)
		}
		if cmd, ok := node.Payload.(*CommandDescriptor); ok && cmd != nil && cmd.Kind == CommandKindPeBarrier {
			barrierID = mergeID
			break
		}
	}
	if barrierID < 0 {
		t.Fatalf("expected barrier node among merge nodes")
	}

	deps := orch.graph.Nodes[residualID].Deps
	for _, dep := range deps {
		if dep == gatingID {
			t.Fatalf("residual dependency still points to gating node")
		}
	}
	foundBarrier := false
	for _, dep := range deps {
		if dep == barrierID {
			foundBarrier = true
			break
		}
	}
	if !foundBarrier {
		t.Fatalf("residual node should depend on barrier node %d", barrierID)
	}

	for _, mergeID := range mergeIDs {
		if mergeID == barrierID {
			continue
		}
		orch.NotifyTaskCompletion(mergeID)
	}

	if session.outstanding != 1 {
		t.Fatalf("expected only barrier outstanding after merges, got %d", session.outstanding)
	}
	if _, exists := orch.moeSessions[gatingID]; !exists {
		t.Fatalf("session cleared before barrier completion")
	}

	orch.NotifyTaskCompletion(barrierID)

	if _, exists := orch.moeSessions[gatingID]; exists {
		t.Fatalf("moe session should be finalized after all merges complete")
	}
	if _, ok := orch.moeMergeOwners[barrierID]; ok {
		t.Fatalf("barrier %d should be cleared from owner map", barrierID)
	}
}
