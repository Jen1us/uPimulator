package chiplet

// TaskTarget differentiates which subsystem should execute a given task.
type TaskTarget int

const (
	TaskTargetDigital TaskTarget = iota
	TaskTargetRram
	TaskTargetTransfer
	TaskTargetHost
)

func (t TaskTarget) String() string {
	switch t {
	case TaskTargetDigital:
		return "digital"
	case TaskTargetRram:
		return "rram"
	case TaskTargetTransfer:
		return "transfer"
	case TaskTargetHost:
		return "host"
	default:
		return "unknown"
	}
}

// TaskType models the high-level operation to be performed.
type TaskType int

const (
	TaskTypeCompute TaskType = iota
	TaskTypeCim
	TaskTypeDataMove
	TaskTypeSync
)

// Task encodes the metadata required for the scheduler to dispatch work to a
// specific chiplet. Detailed payload semantics will be filled in later as the
// execution model matures.
type Task struct {
	NodeID        int
	ID            int
	Target        TaskTarget
	Type          TaskType
	Opcode        CommandKind
	ExecDomain    ExecDomain
	Payload       interface{}
	Metadata      map[string]interface{}
	Latency       int
	EnqueueCycle  int
	MeshSrcX      int
	MeshSrcY      int
	MeshDstX      int
	MeshDstY      int
	HostAddress   uint64
	BufferID      int
	SubOperation  uint32
	RequestBytes  int64
	ResponseBytes int64
}

// OpNode represents a node in a workload DAG that will be mapped onto chiplet tasks.
type OpNode struct {
	ID      int
	Type    TaskType
	Target  TaskTarget
	Latency int
	Deps    []int
	Payload interface{}
	Batch   int
}

// OpGraph captures a lightweight DAG with dependency edges.
type OpGraph struct {
	Nodes     map[int]*OpNode
	Adjacency map[int][]int
}

func NewOpGraph() *OpGraph {
	graph := &OpGraph{
		Nodes:     make(map[int]*OpNode),
		Adjacency: make(map[int][]int),
	}
	return graph
}

// Clone returns a deep copy of the DAG, duplicating node metadata and
// dependency lists. Payloads are shallow-copied; callers may replace them if
// they need per-instance state.
func (g *OpGraph) Clone() *OpGraph {
	if g == nil {
		return nil
	}

	clone := NewOpGraph()
	for id, node := range g.Nodes {
		if node == nil {
			continue
		}

		newNode := &OpNode{
			ID:      node.ID,
			Type:    node.Type,
			Target:  node.Target,
			Latency: node.Latency,
			Deps:    append([]int(nil), node.Deps...),
			Payload: node.Payload,
			Batch:   node.Batch,
		}
		clone.Nodes[id] = newNode
	}

	for id, succs := range g.Adjacency {
		clone.Adjacency[id] = append([]int(nil), succs...)
	}

	return clone
}

func (g *OpGraph) AddNode(node *OpNode) {
	if node == nil {
		return
	}
	g.Nodes[node.ID] = node
	if len(node.Deps) == 0 {
		return
	}
	for _, dep := range node.Deps {
		g.Adjacency[dep] = append(g.Adjacency[dep], node.ID)
	}
}

func (g *OpGraph) Roots() []int {
	roots := make([]int, 0)
	incoming := make(map[int]int)
	for id := range g.Nodes {
		incoming[id] = 0
	}
	for _, targets := range g.Adjacency {
		for _, t := range targets {
			incoming[t]++
		}
	}
	for id, count := range incoming {
		if count == 0 {
			roots = append(roots, id)
		}
	}
	return roots
}

func (g *OpGraph) Successors(id int) []int {
	return g.Adjacency[id]
}

func (g *OpGraph) AddEdge(from int, to int) {
	if g == nil {
		return
	}
	succs := g.Adjacency[from]
	for _, existing := range succs {
		if existing == to {
			return
		}
	}
	g.Adjacency[from] = append(succs, to)
}

func (g *OpGraph) RemoveEdge(from int, to int) {
	if g == nil {
		return
	}
	succs, exists := g.Adjacency[from]
	if !exists || len(succs) == 0 {
		return
	}
	updated := make([]int, 0, len(succs))
	removed := false
	for _, id := range succs {
		if id == to {
			removed = true
			continue
		}
		updated = append(updated, id)
	}
	if !removed {
		return
	}
	if len(updated) == 0 {
		delete(g.Adjacency, from)
	} else {
		g.Adjacency[from] = updated
	}
}
