package prim

import (
	"uPIMulator/src/abi/encoding"
	"uPIMulator/src/misc"
)

// Transformer prepares placeholder host/DPU data for the chiplet transformer
// benchmark. Actual chiplet execution relies on the generated
// chiplet_commands.json; therefore the data streams remain empty.
type Transformer struct {
	numDPUs     int
	numTasklets int
}

func (t *Transformer) Init(parser *misc.CommandLineParser) {
	numChannels := int(parser.IntParameter("num_channels"))
	numRanks := int(parser.IntParameter("num_ranks_per_channel"))
	numDPUsPerRank := int(parser.IntParameter("num_dpus_per_rank"))

	t.numDPUs = numChannels * numRanks * numDPUsPerRank
	t.numTasklets = int(parser.IntParameter("num_tasklets"))
}

func (t *Transformer) NumExecutions() int {
	return 1
}

func (t *Transformer) emptyStream() *encoding.ByteStream {
	stream := new(encoding.ByteStream)
	stream.Init()
	return stream
}

func (t *Transformer) InputDpuHost(execution int, dpuID int) map[string]*encoding.ByteStream {
	return make(map[string]*encoding.ByteStream)
}

func (t *Transformer) OutputDpuHost(execution int, dpuID int) map[string]*encoding.ByteStream {
	return make(map[string]*encoding.ByteStream)
}

func (t *Transformer) InputDpuMramHeapPointerName(execution int, dpuID int) (int64, *encoding.ByteStream) {
	return 0, t.emptyStream()
}

func (t *Transformer) OutputDpuMramHeapPointerName(execution int, dpuID int) (int64, *encoding.ByteStream) {
	return 0, t.emptyStream()
}
