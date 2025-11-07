package chiplet

import "math"

// MeshCoordinate identifies a chiplet position on the 2D mesh interconnect.
type MeshCoordinate struct {
	X int
	Y int
}

// ManhattanDistance returns the hop distance between two mesh coordinates.
func ManhattanDistance(a, b MeshCoordinate) int {
	dx := a.X - b.X
	if dx < 0 {
		dx = -dx
	}
	dy := a.Y - b.Y
	if dy < 0 {
		dy = -dy
	}
	return dx + dy
}

// DigitalTopology describes digital chiplet resources such as PE arrays and SPU
// clusters together with their placement on the mesh.
type DigitalTopology struct {
	NumChiplets    int
	PesPerChiplet  int
	PeRows         int
	PeCols         int
	SpusPerChiplet int
	MeshRows       int
	MeshCols       int
	MeshCoords     []MeshCoordinate
	MeshOffsetX    int
	MeshOffsetY    int
}

// RramTopology captures the layout of CIM chiplets, including tile/SAs and
// mesh placement.
type RramTopology struct {
	NumChiplets   int
	TilesPerDim   int
	SasPerTileDim int
	SaRows        int
	SaCols        int
	CellBits      int
	DacBits       int
	AdcBits       int
	MeshRows      int
	MeshCols      int
	MeshCoords    []MeshCoordinate
	MeshOffsetX   int
	MeshOffsetY   int
}

// Topology aggregates the overall chiplet system configuration.
type Topology struct {
	Digital DigitalTopology
	Rram    RramTopology
}

// BuildTopology constructs a topology object from the runtime config.
func BuildTopology(config *Config) *Topology {
	topology := new(Topology)

	topology.Digital.NumChiplets = config.NumDigitalChiplets
	topology.Digital.PesPerChiplet = config.DigitalPesPerChiplet
	topology.Digital.PeRows = config.DigitalPeRows
	topology.Digital.PeCols = config.DigitalPeCols
	topology.Digital.SpusPerChiplet = config.DigitalSpusPerChiplet
	topology.Digital.MeshRows, topology.Digital.MeshCols, topology.Digital.MeshCoords = buildMesh(config.NumDigitalChiplets, 0, 0)
	topology.Digital.MeshOffsetX = 0
	topology.Digital.MeshOffsetY = 0

	topology.Rram.NumChiplets = config.NumRramChiplets
	topology.Rram.TilesPerDim = config.RramTilesPerDim
	topology.Rram.SasPerTileDim = config.RramSasPerTileDim
	topology.Rram.SaRows = config.RramSaRows
	topology.Rram.SaCols = config.RramSaCols
	topology.Rram.CellBits = config.RramCellBits
	topology.Rram.DacBits = config.RramDacBits
	topology.Rram.AdcBits = config.RramAdcBits
	rramOffsetY := topology.Digital.MeshRows + 1
	topology.Rram.MeshRows, topology.Rram.MeshCols, topology.Rram.MeshCoords = buildMesh(config.NumRramChiplets, 0, rramOffsetY)
	topology.Rram.MeshOffsetX = 0
	topology.Rram.MeshOffsetY = rramOffsetY

	return topology
}

// DigitalCoord returns the mesh coordinate for the requested digital chiplet.
func (topology *Topology) DigitalCoord(id int) (MeshCoordinate, bool) {
	if topology == nil || id < 0 || id >= len(topology.Digital.MeshCoords) {
		return MeshCoordinate{}, false
	}
	return topology.Digital.MeshCoords[id], true
}

// RramCoord returns the mesh coordinate for the requested RRAM chiplet.
func (topology *Topology) RramCoord(id int) (MeshCoordinate, bool) {
	if topology == nil || id < 0 || id >= len(topology.Rram.MeshCoords) {
		return MeshCoordinate{}, false
	}
	return topology.Rram.MeshCoords[id], true
}

// DigitalHopDistance calculates the Manhattan distance between two digital chiplets.
func (topology *Topology) DigitalHopDistance(src, dst int) int {
	a, okA := topology.DigitalCoord(src)
	b, okB := topology.DigitalCoord(dst)
	if !okA || !okB {
		return 0
	}
	return ManhattanDistance(a, b)
}

// RramHopDistance calculates the Manhattan distance between two RRAM chiplets.
func (topology *Topology) RramHopDistance(src, dst int) int {
	a, okA := topology.RramCoord(src)
	b, okB := topology.RramCoord(dst)
	if !okA || !okB {
		return 0
	}
	return ManhattanDistance(a, b)
}

// DigitalToRramHopDistance estimates the hop distance between a digital source and RRAM destination.
func (topology *Topology) DigitalToRramHopDistance(srcDigital, dstRram int) int {
	if topology == nil {
		return 0
	}
	dCoord, okD := topology.DigitalCoord(srcDigital)
	rCoord, okR := topology.RramCoord(dstRram)
	if !okD || !okR {
		return 0
	}
	offset := topology.Digital.MeshRows + 1
	rramSpace := MeshCoordinate{X: rCoord.X, Y: rCoord.Y + offset}
	return ManhattanDistance(dCoord, rramSpace)
}

// RramToDigitalHopDistance estimates the hop distance between an RRAM source and digital destination.
func (topology *Topology) RramToDigitalHopDistance(srcRram, dstDigital int) int {
	if topology == nil {
		return 0
	}
	rCoord, okR := topology.RramCoord(srcRram)
	dCoord, okD := topology.DigitalCoord(dstDigital)
	if !okD || !okR {
		return 0
	}
	offset := topology.Digital.MeshRows + 1
	rSpace := MeshCoordinate{X: rCoord.X, Y: rCoord.Y + offset}
	return ManhattanDistance(rSpace, dCoord)
}

func buildMesh(count int, offsetX, offsetY int) (rows int, cols int, coords []MeshCoordinate) {
	if count <= 0 {
		return 0, 0, nil
	}
	side := int(math.Ceil(math.Sqrt(float64(count))))
	cols = side
	rows = int(math.Ceil(float64(count) / float64(cols)))
	coords = make([]MeshCoordinate, 0, count)
	for idx := 0; idx < count; idx++ {
		x := idx%cols + offsetX
		y := idx/cols + offsetY
		coords = append(coords, MeshCoordinate{X: x, Y: y})
	}
	return rows, cols, coords
}
