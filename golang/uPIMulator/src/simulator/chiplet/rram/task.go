package rram

// Task captures a CIM workload dispatched到 RRAM Chiplet.
type Task struct {
	EstimatedCycles      int
	RemainingCycles      int
	PulseCount           int
	AdcSamples           int
	PreprocessCycles     int
	PostprocessCycles    int
	ErrorSampled         bool
	ErrorAbs             float64
	Spec                 *TaskSpec
	Summary              ResultSummary
	PreCyclesRemaining   int
	PulseCyclesRemaining int
	PostCyclesRemaining  int
	PulsesCompleted      int
	AdcSamplesCompleted  int
	Phase                TaskPhase
}

func (t *Task) clone() *Task {
	if t == nil {
		return nil
	}
	copy := *t
	return &copy
}

func (t *Task) resetProgress() {
	total := t.PreprocessCycles + t.PulseCount + t.PostprocessCycles
	if total <= 0 {
		total = t.EstimatedCycles
	}
	if total <= 0 {
		total = 1
	}
	t.PreCyclesRemaining = t.PreprocessCycles
	t.PulseCyclesRemaining = t.PulseCount
	t.PostCyclesRemaining = t.PostprocessCycles
	t.PulsesCompleted = 0
	t.AdcSamplesCompleted = 0
	t.RemainingCycles = total
	if t.EstimatedCycles <= 0 {
		t.EstimatedCycles = total
	}
}

func (t *Task) TotalCycles() int {
	total := t.PreprocessCycles + t.PulseCount + t.PostprocessCycles
	if total <= 0 {
		total = t.EstimatedCycles
	}
	if total <= 0 {
		total = 1
	}
	return total
}

// TaskSpec carries high-level workload parameters derived from the host payload.
type TaskSpec struct {
	ActivationBits int
	SliceBits      int
	PulseCount     int
	AdcSamples     int
	PreCycles      int
	PostCycles     int
	Scale          float64
	ZeroPoint      int
	ActivationSize int
	WeightSize     int
	OutputSize     int
	WeightTag      string
	WeightTile     int
	WeightArray    int
	Rows           int
	Cols           int
	Depth          int
	ErrorAbs       float64
	ISum           int64
	PSum           int64
	MaxExponent    int
	ASum           float64
	Expected       float64
	HasExpected    bool
	Phase          TaskPhase
}

// TaskPhase identifies the pipeline stage for a task.
type TaskPhase int

const (
	TaskPhaseUnknown TaskPhase = iota
	TaskPhaseStage
	TaskPhaseExecute
	TaskPhasePost
)
