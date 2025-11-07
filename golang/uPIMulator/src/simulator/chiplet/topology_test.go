package chiplet

import "testing"

func TestBuildMeshTopology(t *testing.T) {
	cfg := &Config{
		NumDigitalChiplets:    5,
		NumRramChiplets:       3,
		DigitalPesPerChiplet:  4,
		DigitalPeRows:         64,
		DigitalPeCols:         64,
		DigitalSpusPerChiplet: 2,
		RramTilesPerDim:       1,
		RramSasPerTileDim:     1,
		RramSaRows:            128,
		RramSaCols:            128,
		RramCellBits:          2,
		RramDacBits:           2,
		RramAdcBits:           12,
	}

	topology := BuildTopology(cfg)

	if len(topology.Digital.MeshCoords) != cfg.NumDigitalChiplets {
		t.Fatalf("expected %d digital coords, got %d", cfg.NumDigitalChiplets, len(topology.Digital.MeshCoords))
	}
	if len(topology.Rram.MeshCoords) != cfg.NumRramChiplets {
		t.Fatalf("expected %d rram coords, got %d", cfg.NumRramChiplets, len(topology.Rram.MeshCoords))
	}

	for idx, coord := range topology.Digital.MeshCoords {
		if coord.X < topology.Digital.MeshOffsetX || coord.Y < topology.Digital.MeshOffsetY {
			t.Fatalf("digital coord[%d] invalid %+v", idx, coord)
		}
		if coord.Y >= topology.Digital.MeshOffsetY+topology.Digital.MeshRows || coord.X >= topology.Digital.MeshOffsetX+topology.Digital.MeshCols {
			t.Fatalf("digital coord[%d] %+v outside grid %dx%d offset(%d,%d)", idx, coord, topology.Digital.MeshCols, topology.Digital.MeshRows, topology.Digital.MeshOffsetX, topology.Digital.MeshOffsetY)
		}
	}

	for idx, coord := range topology.Rram.MeshCoords {
		if coord.X < topology.Rram.MeshOffsetX || coord.Y < topology.Rram.MeshOffsetY {
			t.Fatalf("rram coord[%d] invalid %+v", idx, coord)
		}
		if coord.Y >= topology.Rram.MeshOffsetY+topology.Rram.MeshRows || coord.X >= topology.Rram.MeshOffsetX+topology.Rram.MeshCols {
			t.Fatalf("rram coord[%d] %+v outside grid %dx%d offset(%d,%d)", idx, coord, topology.Rram.MeshCols, topology.Rram.MeshRows, topology.Rram.MeshOffsetX, topology.Rram.MeshOffsetY)
		}
	}

	a, _ := topology.DigitalCoord(0)
	b, _ := topology.DigitalCoord(4)
	if dist := topology.DigitalHopDistance(0, 4); dist != ManhattanDistance(a, b) {
		t.Fatalf("digital hop distance mismatch: got %d, want %d", dist, ManhattanDistance(a, b))
	}
}
