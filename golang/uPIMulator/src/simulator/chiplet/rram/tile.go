package rram

import "math"

// Tile 表示 Chiplet 内的一个二维 tile，由若干 SenseArray 组成。
type Tile struct {
	ID                 int
	ArraysPerDim       int
	Arrays             []*SenseArray
	activeIndex        int
	stageQueue         []*Task
	executeQueue       []*Task
	postQueue          []*Task
	compositeQueue     []*Task
	activeTask         *Task
	activePhase        TaskPhase
	pendingCycleBudget int
}

func NewTile(id, arraysPerDim int, arrayFactory func(index int) *SenseArray) *Tile {
	total := arraysPerDim * arraysPerDim
	arrays := make([]*SenseArray, 0, total)
	for i := 0; i < total; i++ {
		if arrayFactory != nil {
			arrays = append(arrays, arrayFactory(i))
		}
	}
	return &Tile{
		ID:             id,
		ArraysPerDim:   arraysPerDim,
		Arrays:         arrays,
		stageQueue:     make([]*Task, 0),
		executeQueue:   make([]*Task, 0),
		postQueue:      make([]*Task, 0),
		compositeQueue: make([]*Task, 0),
	}
}

func (t *Tile) DefaultLatency() int {
	if len(t.Arrays) == 0 || t.Arrays[0] == nil {
		return 1
	}
	return t.Arrays[0].LatencyHint()
}

func (t *Tile) Reserve(requested int, task *Task) int {
	if task == nil {
		task = &Task{}
	}
	configured := task.PreprocessCycles > 0 || task.PulseCount > 0 || task.PostprocessCycles > 0
	if !configured {
		cycles := requested
		if cycles <= 0 {
			cycles = t.DefaultLatency()
		}
		if cycles < 1 {
			cycles = 1
		}
		task.EstimatedCycles = cycles
		task.PulseCount = cycles
		task.PreprocessCycles = 0
		task.PostprocessCycles = 0
		task.AdcSamples = cycles
	}
	if requested > 0 {
		task.EstimatedCycles = requested
	}
	task.resetProgress()
	switch task.Phase {
	case TaskPhaseStage:
		t.stageQueue = append(t.stageQueue, task)
	case TaskPhaseExecute:
		t.executeQueue = append(t.executeQueue, task)
	case TaskPhasePost:
		t.postQueue = append(t.postQueue, task)
	default:
		t.compositeQueue = append(t.compositeQueue, task)
	}
	t.pendingCycleBudget += task.TotalCycles()
	return task.RemainingCycles
}

func (t *Tile) Tick() Stats {
	stats := Stats{}
	if len(t.Arrays) == 0 {
		return stats
	}

	if t.activeTask == nil {
		var next *Task
		switch {
		case len(t.stageQueue) > 0:
			next = t.stageQueue[0]
			t.stageQueue = t.stageQueue[1:]
		case len(t.executeQueue) > 0:
			next = t.executeQueue[0]
			t.executeQueue = t.executeQueue[1:]
		case len(t.postQueue) > 0:
			next = t.postQueue[0]
			t.postQueue = t.postQueue[1:]
		case len(t.compositeQueue) > 0:
			next = t.compositeQueue[0]
			t.compositeQueue = t.compositeQueue[1:]
		}
		if next != nil {
			t.activeTask = next
			t.activePhase = next.Phase
			array := t.Arrays[t.activeIndex%len(t.Arrays)]
			array.AssignTask(t.activeTask)
		}
	}

	if t.activeTask == nil {
		return stats
	}

	array := t.Arrays[t.activeIndex%len(t.Arrays)]
	array.Tick()
	if t.pendingCycleBudget > 0 && t.IsBusy() {
		t.pendingCycleBudget--
		if t.pendingCycleBudget < 0 {
			t.pendingCycleBudget = 0
		}
	}

	if t.activeTask.RemainingCycles <= 0 {
		spec := t.activeTask.Spec
		switch t.activeTask.Phase {
		case TaskPhaseStage:
			stageCycles := t.activeTask.PreprocessCycles
			if stageCycles <= 0 && spec != nil {
				stageCycles = spec.PreCycles
			}
			if stageCycles <= 0 {
				stageCycles = 1
			}
			stats.TotalCimLatency += int64(stageCycles)
			stats.TotalPreprocessCycles += int64(stageCycles)
			stats.CimTasks++
		case TaskPhaseExecute:
			pulses := t.activeTask.PulsesCompleted
			if pulses == 0 && t.activeTask.PulseCount > 0 {
				pulses = t.activeTask.PulseCount
			}
			samples := t.activeTask.AdcSamplesCompleted
			if samples == 0 && t.activeTask.AdcSamples > 0 {
				samples = t.activeTask.AdcSamples
			}
			actualLatency := pulses
			if actualLatency <= 0 {
				actualLatency = t.activeTask.EstimatedCycles
			}
			if actualLatency <= 0 {
				actualLatency = 1
			}
			t.activeTask.PulseCount = pulses
			t.activeTask.AdcSamples = samples
			stats.TotalCimLatency += int64(actualLatency)
			stats.PulseCountCim += int64(pulses)
			stats.TotalAdcSamples += int64(samples)
			stats.CimTasks++
		case TaskPhasePost:
			postCycles := t.activeTask.PostprocessCycles
			if postCycles <= 0 && spec != nil {
				postCycles = spec.PostCycles
			}
			if postCycles <= 0 {
				postCycles = 1
			}
			stats.TotalCimLatency += int64(postCycles)
			stats.TotalPostprocessCycles += int64(postCycles)
			stats.CimTasks++
			if spec != nil && array.Postprocessor != nil {
				summary := array.Postprocessor.FinalizeResult(spec.ISum, spec.PSum, spec.MaxExponent, spec, spec.ASum)
				t.activeTask.Summary = summary
				if spec.HasExpected {
					err := math.Abs(summary.Final - spec.Expected)
					t.activeTask.ErrorSampled = true
					t.activeTask.ErrorAbs = err
				}
				stats.LastSummary = summary
			}
			if t.activeTask.ErrorSampled {
				stats.LastErrorAbs = t.activeTask.ErrorAbs
				if t.activeTask.ErrorAbs > stats.MaxErrorAbs {
					stats.MaxErrorAbs = t.activeTask.ErrorAbs
				}
				stats.AccumulatedErrorAbs += t.activeTask.ErrorAbs
				stats.ErrorSamples++
			}
		default:
			if spec != nil && array.Postprocessor != nil {
				summary := array.Postprocessor.FinalizeResult(spec.ISum, spec.PSum, spec.MaxExponent, spec, spec.ASum)
				t.activeTask.Summary = summary
				if spec.HasExpected {
					err := math.Abs(summary.Final - spec.Expected)
					t.activeTask.ErrorSampled = true
					t.activeTask.ErrorAbs = err
				}
				stats.LastSummary = summary
			}
			pulses := t.activeTask.PulsesCompleted
			if pulses == 0 && t.activeTask.PulseCount > 0 {
				pulses = t.activeTask.PulseCount
			}
			samples := t.activeTask.AdcSamplesCompleted
			if samples == 0 && t.activeTask.AdcSamples > 0 {
				samples = t.activeTask.AdcSamples
			}
			actualLatency := t.activeTask.PreprocessCycles + pulses + t.activeTask.PostprocessCycles
			if actualLatency <= 0 {
				actualLatency = t.activeTask.EstimatedCycles
			}
			if actualLatency <= 0 {
				actualLatency = pulses
			}
			if actualLatency <= 0 {
				actualLatency = 1
			}
			t.activeTask.PulseCount = pulses
			t.activeTask.AdcSamples = samples
			stats.TotalCimLatency += int64(actualLatency)
			stats.PulseCountCim += int64(pulses)
			stats.TotalAdcSamples += int64(samples)
			stats.TotalPreprocessCycles += int64(t.activeTask.PreprocessCycles)
			stats.TotalPostprocessCycles += int64(t.activeTask.PostprocessCycles)
			stats.CimTasks++
			if t.activeTask.ErrorSampled {
				stats.LastErrorAbs = t.activeTask.ErrorAbs
				if t.activeTask.ErrorAbs > stats.MaxErrorAbs {
					stats.MaxErrorAbs = t.activeTask.ErrorAbs
				}
				stats.AccumulatedErrorAbs += t.activeTask.ErrorAbs
				stats.ErrorSamples++
			}
		}
		t.activeTask = nil
		t.activePhase = TaskPhaseUnknown
		t.activeIndex = (t.activeIndex + 1) % len(t.Arrays)
	}

	return stats
}

func (t *Tile) IsBusy() bool {
	return t.activeTask != nil ||
		len(t.stageQueue) > 0 ||
		len(t.executeQueue) > 0 ||
		len(t.postQueue) > 0 ||
		len(t.compositeQueue) > 0
}

func (t *Tile) PendingCycleBudget() int {
	if t.pendingCycleBudget < 0 {
		return 0
	}
	return t.pendingCycleBudget
}
