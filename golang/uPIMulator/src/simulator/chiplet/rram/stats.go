package rram

// Stats records per-chiplet counters for pulse、延迟与误差指标。
type Stats struct {
	CimTasks               int64
	ProgramTasks           int64
	TotalReadLatency       int64
	TotalWriteLatency      int64
	TotalCimLatency        int64
	PulseCountRead         int64
	PulseCountWrite        int64
	PulseCountCim          int64
	TotalAdcSamples        int64
	TotalPreprocessCycles  int64
	TotalPostprocessCycles int64
	LastErrorAbs           float64
	MaxErrorAbs            float64
	AccumulatedErrorAbs    float64
	ErrorSamples           int64
	LastSummary            ResultSummary
}

func (s *Stats) Reset() {
	*s = Stats{}
}

func (s *Stats) Accumulate(other Stats) {
	s.CimTasks += other.CimTasks
	s.ProgramTasks += other.ProgramTasks
	s.TotalReadLatency += other.TotalReadLatency
	s.TotalWriteLatency += other.TotalWriteLatency
	s.TotalCimLatency += other.TotalCimLatency
	s.PulseCountRead += other.PulseCountRead
	s.PulseCountWrite += other.PulseCountWrite
	s.PulseCountCim += other.PulseCountCim
	s.TotalAdcSamples += other.TotalAdcSamples
	s.TotalPreprocessCycles += other.TotalPreprocessCycles
	s.TotalPostprocessCycles += other.TotalPostprocessCycles

	if other.MaxErrorAbs > s.MaxErrorAbs {
		s.MaxErrorAbs = other.MaxErrorAbs
	}
	s.AccumulatedErrorAbs += other.AccumulatedErrorAbs
	s.ErrorSamples += other.ErrorSamples
	if other.ErrorSamples > 0 {
		s.LastErrorAbs = other.LastErrorAbs
	}
	if other.LastSummary.Valid {
		s.LastSummary = other.LastSummary
	}
}
