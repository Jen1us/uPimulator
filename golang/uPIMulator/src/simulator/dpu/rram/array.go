package rram

import (
	"errors"
	"uPIMulator/src/abi/encoding"
	"uPIMulator/src/misc"
)

type Array struct {
	address        int64
	size           int64
	rows           int
	cols           int
	cellPrecision  int
	dataWidthBytes int

	storage []uint8
}

func (this *Array) Init(command_line_parser *misc.CommandLineParser) {
	config_loader := new(misc.ConfigLoader)
	config_loader.Init()

	this.address = config_loader.RramOffset()
	this.size = config_loader.RramSize()
	this.rows = config_loader.RramArrayRows()
	this.cols = config_loader.RramArrayCols()
	this.cellPrecision = config_loader.RramCellPrecision()
	this.dataWidthBytes = config_loader.RramDataWidth() / 8
	if this.dataWidthBytes <= 0 {
		this.dataWidthBytes = 1
	}

	if this.size <= 0 {
		err := errors.New("RRAM size <= 0")
		panic(err)
	}

	if this.size%int64(this.dataWidthBytes) != 0 {
		err := errors.New("RRAM size is not aligned with data width")
		panic(err)
	}

	storageSize := int(this.size)
	if storageSize <= 0 {
		err := errors.New("RRAM storage size overflow")
		panic(err)
	}

	this.storage = make([]uint8, storageSize)
}

func (this *Array) Fini() {
	this.storage = nil
}

func (this *Array) Address() int64 {
	return this.address
}

func (this *Array) Size() int64 {
	return this.size
}

func (this *Array) Read(address int64, size int64) *encoding.ByteStream {
	this.validateRange(address, size)

	byte_stream := new(encoding.ByteStream)
	byte_stream.Init()

	offset := address - this.address
	for i := int64(0); i < size; i++ {
		index := int(offset + i)
		byte_stream.Append(this.storage[index])
	}

	return byte_stream
}

func (this *Array) Write(address int64, byte_stream *encoding.ByteStream) {
	size := byte_stream.Size()
	this.validateRange(address, size)

	offset := address - this.address
	for i := int64(0); i < size; i++ {
		index := int(offset + i)
		this.storage[index] = byte_stream.Get(int(i))
	}
}

func (this *Array) ColumnValue(column int, row int) uint8 {
	if column < 0 || column >= this.cols {
		err := errors.New("RRAM column out of range")
		panic(err)
	}
	if row < 0 || row >= this.rows {
		err := errors.New("RRAM row out of range")
		panic(err)
	}

	index := column*this.rows + row
	if index < 0 || index >= len(this.storage) {
		err := errors.New("RRAM storage index out of range")
		panic(err)
	}

	return this.storage[index]
}

func (this *Array) NumRows() int {
	return this.rows
}

func (this *Array) NumCols() int {
	return this.cols
}

func (this *Array) validateRange(address int64, size int64) {
	if size < 0 {
		err := errors.New("RRAM size < 0")
		panic(err)
	}

	if address < this.address {
		err := errors.New("RRAM address < base address")
		panic(err)
	}

	if address+size > this.address+this.size {
		err := errors.New("RRAM address range overflow")
		panic(err)
	}

	if address%int64(this.dataWidthBytes) != 0 {
		err := errors.New("RRAM address is not aligned with data width")
		panic(err)
	}

	if size%int64(this.dataWidthBytes) != 0 {
		err := errors.New("RRAM size is not aligned with data width")
		panic(err)
	}
}
