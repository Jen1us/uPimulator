package rram

import (
	"fmt"
	"uPIMulator/src/abi/encoding"
	"uPIMulator/src/misc"
	"uPIMulator/src/simulator/dpu/dram"
)

type MemoryController struct {
	channel_id int
	rank_id    int
	dpu_id     int

	array *Array

	activationBuffer []float32

	input_q *dram.DmaCommandQ
	ready_q *dram.DmaCommandQ

	active_command  *dram.DmaCommand
	remaining_cycle int

	read_latency  int
	write_latency int

	stat_factory      *misc.StatFactory
	scheduler_stat    *misc.StatFactory
	array_buffer_stat *misc.StatFactory
}

func (this *MemoryController) Init(
	channel_id int,
	rank_id int,
	dpu_id int,
	command_line_parser *misc.CommandLineParser,
) {
	this.channel_id = channel_id
	this.rank_id = rank_id
	this.dpu_id = dpu_id

	this.array = nil

	this.input_q = new(dram.DmaCommandQ)
	this.input_q.Init(-1, 0)

	this.ready_q = new(dram.DmaCommandQ)
	this.ready_q.Init(-1, 0)

	this.active_command = nil
	this.remaining_cycle = 0

	this.read_latency = int(command_line_parser.IntParameter("rram_read_latency"))
	this.write_latency = int(command_line_parser.IntParameter("rram_write_latency"))

	name := fmt.Sprintf("RRAMMemoryController[%d_%d_%d]", channel_id, rank_id, dpu_id)
	this.stat_factory = new(misc.StatFactory)
	this.stat_factory.Init(name)

	this.scheduler_stat = new(misc.StatFactory)
	this.scheduler_stat.Init(fmt.Sprintf("RRAMScheduler[%d_%d_%d]", channel_id, rank_id, dpu_id))

	this.array_buffer_stat = new(misc.StatFactory)
	this.array_buffer_stat.Init(fmt.Sprintf("RRAMArray[%d_%d_%d]", channel_id, rank_id, dpu_id))

}

func (this *MemoryController) Fini() {
	this.input_q.Fini()
	this.ready_q.Fini()

	if this.active_command != nil {
		this.completeActiveCommand()
	}
}

func (this *MemoryController) ConnectArray(array *Array) {
	if this.array != nil {
		err := fmt.Errorf("RRAM array is already connected")
		panic(err)
	}

	this.array = array
}

func (this *MemoryController) Array() *Array {
	return this.array
}

func (this *MemoryController) StatFactory() *misc.StatFactory {
	return this.stat_factory
}

func (this *MemoryController) MemorySchedulerStatFactory() *misc.StatFactory {
	return this.scheduler_stat
}

func (this *MemoryController) RowBufferStatFactory() *misc.StatFactory {
	return this.array_buffer_stat
}

func (this *MemoryController) IsEmpty() bool {
	return this.active_command == nil && this.input_q.IsEmpty() && this.ready_q.IsEmpty()
}

func (this *MemoryController) CanPush() bool {
	return this.input_q.CanPush(1)
}

func (this *MemoryController) Push(dma_command *dram.DmaCommand) {
	if !this.CanPush() {
		err := fmt.Errorf("RRAM controller cannot be pushed")
		panic(err)
	}

	this.input_q.Push(dma_command)
}

func (this *MemoryController) CanPop() bool {
	return this.ready_q.CanPop(1)
}

func (this *MemoryController) Pop() *dram.DmaCommand {
	if !this.CanPop() {
		err := fmt.Errorf("RRAM controller cannot be popped")
		panic(err)
	}

	return this.ready_q.Pop()
}

func (this *MemoryController) Read(address int64, size int64) *encoding.ByteStream {
	if this.array == nil {
		err := fmt.Errorf("RRAM array is not connected")
		panic(err)
	}

	return this.array.Read(address, size)
}

func (this *MemoryController) Write(address int64, size int64, byte_stream *encoding.ByteStream) {
	if this.array == nil {
		err := fmt.Errorf("RRAM array is not connected")
		panic(err)
	}

	if size != byte_stream.Size() {
		err := fmt.Errorf("size != byte stream size")
		panic(err)
	}

	this.array.Write(address, byte_stream)
}

func (this *MemoryController) Flush() {
	for this.active_command != nil || !this.input_q.IsEmpty() {
		this.Cycle()
	}
}

func (this *MemoryController) Cycle() {
	this.serviceInput()
	this.serviceActive()

	this.input_q.Cycle()
	this.ready_q.Cycle()
	this.stat_factory.Increment("rram_cycle", 1)
}

func (this *MemoryController) serviceInput() {
	if this.active_command != nil {
		return
	}

	if this.input_q.CanPop(1) {
		this.active_command = this.input_q.Pop()
		this.remaining_cycle = this.latencyFor(this.active_command)
	}
}

func (this *MemoryController) serviceActive() {
	if this.active_command == nil {
		return
	}

	this.remaining_cycle--
	if this.remaining_cycle > 0 {
		return
	}

	this.completeActiveCommand()
}

func (this *MemoryController) completeActiveCommand() {
	if this.active_command == nil {
		return
	}

	switch this.active_command.MemoryOperation() {
	case dram.READ:
		mram_address := this.active_command.MramAddress()
		size := this.active_command.Size()
		byte_stream := this.array.Read(mram_address, size)

		this.active_command.SetByteStream(mram_address, size, byte_stream)
		this.active_command.SetAck(mram_address, size)
		this.stat_factory.Increment("rram_read_ops", 1)
	case dram.WRITE:
		mram_address := this.active_command.MramAddress()
		size := this.active_command.Size()
		byte_stream := this.active_command.ByteStream(mram_address, size)

		this.array.Write(mram_address, byte_stream)
		this.active_command.SetAck(mram_address, size)
		this.stat_factory.Increment("rram_write_ops", 1)
	default:
		panic(fmt.Errorf("RRAM memory operation not supported"))
	}

	this.ready_q.Push(this.active_command)
	this.active_command = nil
	this.remaining_cycle = 0
}

func (this *MemoryController) latencyFor(dma_command *dram.DmaCommand) int {
	if dma_command.MemoryOperation() == dram.READ {
		return this.read_latency
	}

	return this.write_latency
}
func (this *MemoryController) StageActivations(values []float32) {
	this.activationBuffer = make([]float32, len(values))
	copy(this.activationBuffer, values)
	this.stat_factory.Increment("rram_activation_stage_ops", 1)
}

func (this *MemoryController) ExecuteCim(column int) float32 {
	if this.array == nil {
		err := fmt.Errorf("RRAM array is not connected")
		panic(err)
	}

	if this.activationBuffer == nil {
		err := fmt.Errorf("RRAM activation buffer is empty")
		panic(err)
	}

	rows := this.array.NumRows()
	if len(this.activationBuffer) < rows {
		err := fmt.Errorf("RRAM activation buffer size (%d) < rows (%d)", len(this.activationBuffer), rows)
		panic(err)
	}

	if column < 0 || column+1 >= this.array.NumCols() {
		err := fmt.Errorf("RRAM column %d is out of range", column)
		panic(err)
	}

	var sum float32
	for row := 0; row < rows; row++ {
		msb := this.array.ColumnValue(column, row) & 0x3
		lsb := this.array.ColumnValue(column+1, row) & 0x3
		mapped := (int(msb) << 2) | int(lsb)
		weight := float32(mapped) - 8.0
		activation := this.activationBuffer[row]
		sum += activation * weight
	}

	this.stat_factory.Increment("rram_cim_mac_ops", 1)
	return sum
}
