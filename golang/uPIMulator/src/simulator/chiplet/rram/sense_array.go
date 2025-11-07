package rram

// SenseArray 模拟单个 RRAM CIM SA，包含 DAC、ADC 以及对应的预/后处理钩子。
type SenseArray struct {
	ID       int
	Rows     int
	Cols     int
	CellBits int
	DacBits  int
	AdcBits  int

	Preprocessor  *Preprocessor
	Postprocessor *Postprocessor

	activeTask *Task
}

func NewSenseArray(id, rows, cols, cellBits, dacBits, adcBits int, pre *Preprocessor, post *Postprocessor) *SenseArray {
	if pre == nil {
		pre = NewPreprocessor(12, 2)
	}
	if post == nil {
		post = NewPostprocessor(32)
	}
	return &SenseArray{
		ID:            id,
		Rows:          rows,
		Cols:          cols,
		CellBits:      cellBits,
		DacBits:       dacBits,
		AdcBits:       adcBits,
		Preprocessor:  pre,
		Postprocessor: post,
	}
}

// LatencyHint 返回执行一次列向量乘累加的大致周期估计。
func (sa *SenseArray) LatencyHint() int {
	if sa.Rows <= 0 {
		return 1
	}
	return sa.Rows
}

func (sa *SenseArray) AssignTask(task *Task) {
	sa.activeTask = task
}

func (sa *SenseArray) Tick() {
	if sa.activeTask == nil {
		return
	}

	task := sa.activeTask

	if task.PreCyclesRemaining > 0 {
		task.PreCyclesRemaining--
		if task.RemainingCycles > 0 {
			task.RemainingCycles--
		}
		return
	}

	if task.PulseCyclesRemaining > 0 {
		task.PulseCyclesRemaining--
		task.PulsesCompleted++
		cols := sa.Cols
		if cols <= 0 {
			cols = 1
		}
		task.AdcSamplesCompleted += cols
		if task.AdcSamples > 0 && task.AdcSamplesCompleted > task.AdcSamples {
			task.AdcSamplesCompleted = task.AdcSamples
		}
		if task.RemainingCycles > 0 {
			task.RemainingCycles--
		}
		return
	}

	if task.PostCyclesRemaining > 0 {
		task.PostCyclesRemaining--
		if task.RemainingCycles > 0 {
			task.RemainingCycles--
		}
		return
	}

	if task.RemainingCycles > 0 {
		task.RemainingCycles--
	}

	if task.RemainingCycles <= 0 {
		sa.activeTask = nil
	}
}
