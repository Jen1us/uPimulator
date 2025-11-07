package rram

import "strings"

// WeightKey identifies a weight chunk resident on a specific tile/SenseArray.
type WeightKey struct {
	TileID  int
	ArrayID int
	Tag     string
}

// WeightRecord captures residency metadata for a weight chunk.
type WeightRecord struct {
	Key          WeightKey
	Bytes        int64
	Hits         int
	LastLoadTick int
}

// WeightDirectory keeps track of all resident weight chunks and aggregate usage.
type WeightDirectory struct {
	entries   map[WeightKey]*WeightRecord
	total     int64
	peakTotal int64
}

// NewWeightDirectory creates an empty directory.
func NewWeightDirectory() *WeightDirectory {
	return &WeightDirectory{
		entries: make(map[WeightKey]*WeightRecord),
	}
}

// TotalBytes returns the cumulative resident bytes across all tiles.
func (wd *WeightDirectory) TotalBytes() int64 {
	return wd.total
}

// PeakBytes returns the historical peak bytes tracked by the directory.
func (wd *WeightDirectory) PeakBytes() int64 {
	return wd.peakTotal
}

func (wd *WeightDirectory) makeKey(tileID, arrayID int, tag string) WeightKey {
	return WeightKey{
		TileID:  tileID,
		ArrayID: arrayID,
		Tag:     strings.ToLower(tag),
	}
}

// Lookup returns the record and whether it exists.
func (wd *WeightDirectory) Lookup(tileID, arrayID int, tag string) (*WeightRecord, bool) {
	if wd == nil {
		return nil, false
	}
	rec, ok := wd.entries[wd.makeKey(tileID, arrayID, tag)]
	return rec, ok
}

// Register notes that weights for the given tile/array/tag combination have been
// (re)loaded. Returns true when the registration represents a cache hit.
func (wd *WeightDirectory) Register(tileID, arrayID int, tag string, bytes int64, tick int) bool {
	if wd == nil {
		return false
	}
	if bytes < 0 {
		bytes = 0
	}
	key := wd.makeKey(tileID, arrayID, tag)
	if rec, ok := wd.entries[key]; ok {
		rec.Hits++
		rec.LastLoadTick = tick
		return true
	}

	rec := &WeightRecord{
		Key:          key,
		Bytes:        bytes,
		Hits:         0,
		LastLoadTick: tick,
	}
	wd.entries[key] = rec
	wd.total += bytes
	if wd.total > wd.peakTotal {
		wd.peakTotal = wd.total
	}
	return false
}

// Evict removes the tracked weights (e.g., during explicit unload).
func (wd *WeightDirectory) Evict(tileID, arrayID int, tag string) {
	if wd == nil {
		return
	}
	key := wd.makeKey(tileID, arrayID, tag)
	if rec, ok := wd.entries[key]; ok {
		wd.total -= rec.Bytes
		if wd.total < 0 {
			wd.total = 0
		}
		delete(wd.entries, key)
	}
}

// Reset clears the directory state (useful for unit tests).
func (wd *WeightDirectory) Reset() {
	wd.entries = make(map[WeightKey]*WeightRecord)
	wd.total = 0
	wd.peakTotal = 0
}
