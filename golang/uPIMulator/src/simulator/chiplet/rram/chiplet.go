package rram

import "strings"

// Chiplet groups together multiple tiles belonging to the same RRAM die. It
// wires the controller, pre/post processing modules and buffer accounting.
type Chiplet struct {
	ID int

	Tiles           []*Tile
	Controller      *Controller
	Preprocess      *Preprocessor
	Postprocess     *Postprocessor
	WeightDirectory *WeightDirectory
	stats           Stats
	lastResult      ResultSummary

	ExecutedTasks        int
	PendingCycles        int
	PendingTasks         int
	BusyCycles           int
	InputBufferCapacity  int64
	OutputBufferCapacity int64
	InputBufferPeak      int64
	OutputBufferPeak     int64
	BufferOccupancy      map[string]int64
	WeightBytesResident  int64
	WeightBytesPeak      int64
	WeightLoads          int64
	WeightLoadHits       int64
	WeightLoadEnergyPJ   float64
	StageEnergyPJ        float64
	ExecuteEnergyPJ      float64
	PostEnergyPJ         float64
	weightLoadQueue      []*weightLoadTask
	weightLoadActive     *weightLoadTask
	params               Parameters
	DynamicEnergyPJ      float64
	StaticEnergyPJ       float64
	AreaMm2              float64
	bufferPeak           map[string]int64
}

type weightLoadTask struct {
	TileID    int
	ArrayID   int
	Tag       string
	Bytes     int64
	Remaining int
	StartTick int
}

// NewChiplet constructs an RRAM chiplet with uniform tile/array configuration.
func NewChiplet(
	id int,
	tilesPerDim int,
	sasPerTileDim int,
	saRows int,
	saCols int,
	cellBits int,
	dacBits int,
	adcBits int,
	inputBuffer int64,
	outputBuffer int64,
	params Parameters,
) *Chiplet {
	if params.ClockMHz <= 0 {
		params = DefaultParameters()
	}
	activationPre := NewPreprocessor(12, 2)
	resultPost := NewPostprocessor(32)

	numTiles := tilesPerDim * tilesPerDim
	tiles := make([]*Tile, 0, numTiles)
	arrayGlobalIndex := 0
	for tileIdx := 0; tileIdx < numTiles; tileIdx++ {
		tile := NewTile(tileIdx, sasPerTileDim, func(int) *SenseArray {
			sa := NewSenseArray(arrayGlobalIndex, saRows, saCols, cellBits, dacBits, adcBits, activationPre, resultPost)
			arrayGlobalIndex++
			return sa
		})
		tiles = append(tiles, tile)
	}

	controller := NewController(tiles)

	chip := &Chiplet{
		ID:                   id,
		Tiles:                tiles,
		Controller:           controller,
		Preprocess:           activationPre,
		Postprocess:          resultPost,
		WeightDirectory:      controller.WeightDirectory(),
		ExecutedTasks:        0,
		PendingCycles:        0,
		PendingTasks:         0,
		BusyCycles:           0,
		InputBufferCapacity:  inputBuffer,
		OutputBufferCapacity: outputBuffer,
		BufferOccupancy: map[string]int64{
			"input":  0,
			"output": 0,
		},
		bufferPeak: map[string]int64{
			"input":  0,
			"output": 0,
		},
		weightLoadQueue: make([]*weightLoadTask, 0),
		params:          params,
	}

	areaPerTile := params.Tile.SenseArrayAreaMm2 + params.Tile.ControllerAreaMm2
	chip.AreaMm2 = params.BaseAreaMm2 + float64(numTiles)*areaPerTile

	return chip
}

func (c *Chiplet) AddInputTransferEnergy(bytes int64) {
	if bytes <= 0 {
		return
	}
	c.DynamicEnergyPJ += float64(bytes) * c.params.InputReadEnergyPJPerByte
}

func (c *Chiplet) AddOutputTransferEnergy(bytes int64) {
	if bytes <= 0 {
		return
	}
	c.DynamicEnergyPJ += float64(bytes) * c.params.OutputWriteEnergyPJPerByte
}

func (c *Chiplet) AddWeightLoadEnergy(bytes int64) {
	if bytes <= 0 {
		return
	}
	c.DynamicEnergyPJ += float64(bytes) * c.params.WeightReadEnergyPJPerByte
	c.WeightLoadEnergyPJ += float64(bytes) * c.params.WeightReadEnergyPJPerByte
}

// RegisterWeights records residency for a tile/array weight chunk. Returns true on cache hit.
func (c *Chiplet) RegisterWeights(tileID, arrayID int, tag string, bytes int64, tick int) bool {
	if c == nil || c.Controller == nil {
		return false
	}
	hit := c.Controller.RegisterWeights(tileID, arrayID, tag, bytes, tick)
	c.WeightBytesResident = c.Controller.TotalWeightBytes()
	if c.WeightBytesResident > c.WeightBytesPeak {
		c.WeightBytesPeak = c.WeightBytesResident
	}
	if hit {
		c.WeightLoadHits++
	}
	return hit
}

// ScheduleWeightLoad enqueues a DMA-style weight transfer to the chiplet.
func (c *Chiplet) ScheduleWeightLoad(tileID, arrayID int, tag string, bytes int64, latency int, startTick int) {
	if c == nil {
		return
	}
	if bytes < 0 {
		bytes = 0
	}
	if latency <= 0 {
		bandwidth := c.params.WeightLoadBytesPerCycle
		if bandwidth <= 0 {
			bandwidth = 4096
		}
		latency = int((bytes + bandwidth - 1) / bandwidth)
	}
	if latency < 1 {
		latency = 1
	}
	task := &weightLoadTask{
		TileID:    tileID,
		ArrayID:   arrayID,
		Tag:       strings.ToLower(tag),
		Bytes:     bytes,
		Remaining: latency,
		StartTick: startTick,
	}
	c.weightLoadQueue = append(c.weightLoadQueue, task)
	c.PendingTasks++
	c.PendingCycles += latency
	c.WeightLoads++
}

// LookupWeights returns the directory record for the provided key.
func (c *Chiplet) LookupWeights(tileID, arrayID int, tag string) (*WeightRecord, bool) {
	if c == nil || c.Controller == nil {
		return nil, false
	}
	return c.Controller.LookupWeights(tileID, arrayID, tag)
}

// EvictWeights removes tracked residency metadata.
func (c *Chiplet) EvictWeights(tileID, arrayID int, tag string) {
	if c == nil || c.Controller == nil {
		return
	}
	c.Controller.EvictWeights(tileID, arrayID, tag)
	c.WeightBytesResident = c.Controller.TotalWeightBytes()
	if c.WeightBytesResident < 0 {
		c.WeightBytesResident = 0
	}
}

func (c *Chiplet) energyPerCyclePJ(powerMw float64) float64 {
	if c.params.ClockMHz <= 0 {
		return 0
	}
	return powerMw * 1e3 / float64(c.params.ClockMHz)
}

// RecordCimTask increments the executed task counter.
func (c *Chiplet) RecordCimTask() {
	c.ExecutedTasks++
}

// ScheduleTask assigns placeholder cycles based on either the requested latency
// or the controller estimation.
func (c *Chiplet) ScheduleTask(latency int, spec *TaskSpec) {
	if c.Controller == nil {
		c.PendingCycles += c.estimateFallbackLatency(latency)
		c.PendingTasks++
		return
	}

	task := c.buildTask(latency, spec)
	cycles := c.Controller.Reserve(latency, task)
	c.PendingCycles += cycles
	c.PendingTasks++
}

func (c *Chiplet) processWeightLoads() {
	if c.weightLoadActive == nil && len(c.weightLoadQueue) > 0 {
		c.weightLoadActive = c.weightLoadQueue[0]
		c.weightLoadQueue = c.weightLoadQueue[1:]
	}
	if c.weightLoadActive == nil {
		return
	}
	if c.weightLoadActive.Remaining > 0 {
		c.weightLoadActive.Remaining--
	}
	if c.weightLoadActive.Remaining > 0 {
		return
	}
	task := c.weightLoadActive
	c.weightLoadActive = nil
	if task.Bytes > 0 {
		c.AddWeightLoadEnergy(task.Bytes)
	}
	c.RegisterWeights(task.TileID, task.ArrayID, task.Tag, task.Bytes, task.StartTick)
	c.PendingTasks--
	if c.PendingTasks < 0 {
		c.PendingTasks = 0
	}
}

// Tick advances the internal timing counter by one cycle.
func (c *Chiplet) Tick() {
	if c.PendingCycles > 0 {
		c.PendingCycles--
	}

	if c.Controller != nil {
		delta := c.Controller.Tick()
		stageEnergy := float64(delta.TotalPreprocessCycles) * c.params.PreprocessEnergyPJPerCycle
		executeEnergy := float64(delta.PulseCountCim)*(c.params.PulseEnergyPJ+c.params.DacEnergyPJ) +
			float64(delta.TotalAdcSamples)*c.params.AdcEnergyPJ
		postEnergy := float64(delta.TotalPostprocessCycles) * c.params.PostprocessEnergyPJPerCycle
		c.DynamicEnergyPJ += stageEnergy + executeEnergy + postEnergy
		c.StageEnergyPJ += stageEnergy
		c.ExecuteEnergyPJ += executeEnergy
		c.PostEnergyPJ += postEnergy
		c.stats.PulseCountCim += delta.PulseCountCim
		c.stats.TotalCimLatency += delta.TotalCimLatency
		c.stats.CimTasks += delta.CimTasks
		c.stats.TotalAdcSamples += delta.TotalAdcSamples
		c.stats.TotalPreprocessCycles += delta.TotalPreprocessCycles
		c.stats.TotalPostprocessCycles += delta.TotalPostprocessCycles
		if delta.ErrorSamples > 0 {
			c.stats.LastErrorAbs = delta.LastErrorAbs
			if delta.MaxErrorAbs > c.stats.MaxErrorAbs {
				c.stats.MaxErrorAbs = delta.MaxErrorAbs
			}
			c.stats.AccumulatedErrorAbs += delta.AccumulatedErrorAbs
			c.stats.ErrorSamples += delta.ErrorSamples
		}
		if delta.LastSummary.Valid {
			c.stats.LastSummary = delta.LastSummary
			c.lastResult = delta.LastSummary
		}
		if delta.CimTasks > 0 {
			c.ExecutedTasks += int(delta.CimTasks)
			c.PendingTasks -= int(delta.CimTasks)
			if c.PendingTasks < 0 {
				c.PendingTasks = 0
			}
		}
	}

	c.processWeightLoads()

	if c.PendingTasks > 0 {
		c.BusyCycles++
	}

	totalMw := c.params.StaticPowerMw + float64(len(c.Tiles))*c.params.Tile.LeakagePowerMw
	c.StaticEnergyPJ += c.energyPerCyclePJ(totalMw)
}

// Busy reports whether the chiplet is still processing a scheduled task.
func (c *Chiplet) Busy() bool {
	if c.PendingTasks > 0 {
		return true
	}
	if c.Controller != nil {
		return c.Controller.IsBusy()
	}
	return false
}

// PendingCapacity returns an approximate number of outstanding tasks the chiplet
// can sustain before additional submissions should be deferred. Allow at least
// a couple of in-flight operations so preparation, execution and post-processing
// can overlap across tiles.
func (c *Chiplet) PendingCapacity() int {
	if c == nil {
		return 1
	}
	tiles := len(c.Tiles)
	if tiles <= 0 {
		return 2
	}
	capacity := tiles
	if capacity < 2 {
		capacity = 2
	}
	return capacity
}

func (c *Chiplet) Stats() Stats {
	return c.stats
}

func (c *Chiplet) ConsumeLastResult() (ResultSummary, bool) {
	if c.lastResult.Valid {
		result := c.lastResult
		c.lastResult = ResultSummary{}
		return result, true
	}
	return ResultSummary{}, false
}
func (c *Chiplet) AdjustBuffer(name string, delta int64) bool {
	if c.BufferOccupancy == nil {
		c.BufferOccupancy = make(map[string]int64)
	}
	if c.bufferPeak == nil {
		c.bufferPeak = make(map[string]int64)
	}

	key := strings.ToLower(name)
	capacity := c.bufferCapacity(name)
	updated := c.BufferOccupancy[key] + delta
	if updated < 0 {
		c.BufferOccupancy[key] = 0
		return false
	}
	if capacity > 0 && updated > capacity {
		c.BufferOccupancy[key] = capacity
		return false
	}
	c.BufferOccupancy[key] = updated
	if updated > c.bufferPeak[key] {
		c.bufferPeak[key] = updated
		if strings.EqualFold(key, "input") {
			c.InputBufferPeak = updated
		} else if strings.EqualFold(key, "output") {
			c.OutputBufferPeak = updated
		}
	}
	return true
}

func (c *Chiplet) BufferUsage(name string) int64 {
	if c.BufferOccupancy == nil {
		return 0
	}

	return c.BufferOccupancy[strings.ToLower(name)]
}

func (c *Chiplet) bufferCapacity(name string) int64 {
	if strings.EqualFold(name, "input") {
		return c.InputBufferCapacity
	}
	if strings.EqualFold(name, "output") {
		return c.OutputBufferCapacity
	}
	return 0
}

func (c *Chiplet) BufferPeak(name string) int64 {
	if c.bufferPeak == nil {
		return 0
	}
	return c.bufferPeak[strings.ToLower(name)]
}

func (c *Chiplet) IsReady() (int64, bool) {
	bytes := c.BufferUsage("output")
	return bytes, bytes > 0
}

func (c *Chiplet) estimateFallbackLatency(latency int) int {
	cycles := latency
	if cycles <= 0 {
		if len(c.Tiles) == 0 {
			cycles = 1
		} else {
			cycles = c.Tiles[0].DefaultLatency()
		}
	}
	if cycles < 1 {
		cycles = 1
	}
	return cycles
}

func (c *Chiplet) buildTask(latency int, spec *TaskSpec) *Task {
	activationBits := 12
	sliceBits := 2
	phase := TaskPhaseUnknown
	if c.Preprocess != nil {
		activationBits = c.Preprocess.ActivationBitWidth()
		sliceBits = c.Preprocess.SliceBits()
	}
	if spec != nil {
		if spec.Phase != TaskPhaseUnknown {
			phase = spec.Phase
		}
		if spec.ActivationBits > 0 {
			activationBits = spec.ActivationBits
		}
		if spec.SliceBits > 0 {
			sliceBits = spec.SliceBits
		}
	}
	if sliceBits <= 0 {
		sliceBits = 1
	}
	cycles := (activationBits + sliceBits - 1) / sliceBits
	if cycles < 1 {
		cycles = 1
	}
	pulseCount := cycles
	adcSamples := 0
	if len(c.Tiles) > 0 && len(c.Tiles[0].Arrays) > 0 {
		cols := c.Tiles[0].Arrays[0].Cols
		if cols > 0 {
			adcSamples = cols * cycles
		}
	}
	if adcSamples <= 0 {
		adcSamples = cycles
	}
	preCycles := cycles
	postCycles := 2
	if c.Postprocess != nil {
		accBits := c.Postprocess.AccumulatorBitWidth()
		if accBits > 0 {
			postCycles += accBits / 16
		}
	}
	if spec != nil {
		if spec.PulseCount > 0 {
			pulseCount = spec.PulseCount
		}
		if spec.AdcSamples > 0 {
			adcSamples = spec.AdcSamples
		}
		if spec.PreCycles > 0 {
			preCycles = spec.PreCycles
		}
		if spec.PostCycles > 0 {
			postCycles = spec.PostCycles
		}
	}

	switch phase {
	case TaskPhaseStage:
		if preCycles <= 0 {
			preCycles = cycles
		}
		pulseCount = 0
		adcSamples = 0
		postCycles = 0
	case TaskPhaseExecute:
		preCycles = 0
		if pulseCount <= 0 {
			pulseCount = cycles
		}
		if adcSamples <= 0 {
			adcSamples = pulseCount
		}
		postCycles = 0
	case TaskPhasePost:
		preCycles = 0
		pulseCount = 0
		adcSamples = 0
		if postCycles <= 0 {
			postCycles = 1
		}
	default:
		// Keep the combined pipeline defaults.
	}

	estimatedCycles := latency
	if estimatedCycles <= 0 {
		switch phase {
		case TaskPhaseStage:
			estimatedCycles = preCycles
		case TaskPhaseExecute:
			estimatedCycles = pulseCount
		case TaskPhasePost:
			estimatedCycles = postCycles
		default:
			estimatedCycles = preCycles + pulseCount + postCycles
		}
	}
	if estimatedCycles <= 0 {
		estimatedCycles = pulseCount
	}
	if estimatedCycles <= 0 {
		estimatedCycles = 1
	}

	task := &Task{
		EstimatedCycles:   estimatedCycles,
		PulseCount:        pulseCount,
		AdcSamples:        adcSamples,
		PreprocessCycles:  preCycles,
		PostprocessCycles: postCycles,
		Phase:             phase,
	}
	if spec != nil && spec.ErrorAbs != 0 {
		task.ErrorSampled = true
		task.ErrorAbs = spec.ErrorAbs
	}
	if spec != nil {
		spec.Phase = phase
		task.Spec = spec
	}
	task.resetProgress()
	return task
}
