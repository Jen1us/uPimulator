package rram

import "math"

// Controller 负责调度 Tile/SenseArray 的执行，这里提供最小骨架，实现周期估算与占位接口。
type Controller struct {
	tiles       []*Tile
	rrIndex     int
	defaultTile *Tile
	globalStats Stats
	weights     *WeightDirectory
}

func NewController(tiles []*Tile) *Controller {
	ctrl := &Controller{
		tiles:   tiles,
		weights: NewWeightDirectory(),
	}
	if len(tiles) > 0 {
		ctrl.defaultTile = tiles[0]
	}
	return ctrl
}

// EstimateCycles 返回一次 CIM 任务需要的周期数。后续 Phase 将依据 DAC/ADC、脉冲数等细化。
func (c *Controller) EstimateCycles(requested int) int {
	if requested > 0 {
		return requested
	}
	tile := c.selectTile()
	if tile == nil {
		return 1
	}
	return tile.DefaultLatency()
}

// Reserve 将任务分配给一个 tile，并返回实际占用周期。
func (c *Controller) Reserve(requested int, task *Task) int {
	tile := c.selectTile()
	if tile == nil {
		return 1
	}
	cycles := tile.Reserve(requested, task)
	if cycles < 1 {
		cycles = 1
	}
	return cycles
}

func (c *Controller) Tick() Stats {
	delta := Stats{}
	for _, tile := range c.tiles {
		delta.Accumulate(tile.Tick())
	}
	c.globalStats.Accumulate(delta)
	return delta
}

func (c *Controller) IsBusy() bool {
	for _, tile := range c.tiles {
		if tile.IsBusy() {
			return true
		}
	}
	return false
}

func (c *Controller) nextTile() *Tile {
	return c.selectTile()
}

func (c *Controller) selectTile() *Tile {
	if len(c.tiles) == 0 {
		return c.defaultTile
	}
	if len(c.tiles) == 1 {
		return c.tiles[0]
	}

	start := c.rrIndex % len(c.tiles)
	bestIdx := -1
	bestLoad := math.MaxInt

	for offset := 0; offset < len(c.tiles); offset++ {
		idx := (start + offset) % len(c.tiles)
		tile := c.tiles[idx]
		load := tile.PendingCycleBudget()
		if tile.IsBusy() {
			load++
		}
		if load < bestLoad {
			bestLoad = load
			bestIdx = idx
		}
	}

	if bestIdx < 0 {
		bestIdx = start
	}
	c.rrIndex = (bestIdx + 1) % len(c.tiles)
	return c.tiles[bestIdx]
}

// WeightDirectory exposes the internal weight tracking directory.
func (c *Controller) WeightDirectory() *WeightDirectory {
	if c == nil {
		return nil
	}
	return c.weights
}

// RegisterWeights records that the specified tile/array/tag combination has
// been loaded. Returns true when the load hits an existing resident chunk.
func (c *Controller) RegisterWeights(tileID, arrayID int, tag string, bytes int64, tick int) bool {
	if c == nil || c.weights == nil {
		return false
	}
	return c.weights.Register(tileID, arrayID, tag, bytes, tick)
}

// LookupWeights retrieves the record for the given key.
func (c *Controller) LookupWeights(tileID, arrayID int, tag string) (*WeightRecord, bool) {
	if c == nil || c.weights == nil {
		return nil, false
	}
	return c.weights.Lookup(tileID, arrayID, tag)
}

// EvictWeights removes the tracked weights from the directory.
func (c *Controller) EvictWeights(tileID, arrayID int, tag string) {
	if c == nil || c.weights == nil {
		return
	}
	c.weights.Evict(tileID, arrayID, tag)
}

// TotalWeightBytes reports the aggregated resident bytes.
func (c *Controller) TotalWeightBytes() int64 {
	if c == nil || c.weights == nil {
		return 0
	}
	return c.weights.TotalBytes()
}

// PeakWeightBytes reports the historical peak bytes tracked for this controller.
func (c *Controller) PeakWeightBytes() int64 {
	if c == nil || c.weights == nil {
		return 0
	}
	return c.weights.PeakBytes()
}
