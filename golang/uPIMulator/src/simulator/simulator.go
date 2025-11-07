package simulator

import "uPIMulator/src/misc"

type Simulator struct {
	mode     misc.PlatformMode
	platform Platform
}

func (this *Simulator) Init(command_line_parser *misc.CommandLineParser) {
	this.mode = misc.RuntimePlatformMode()

	platform := newPlatformForMode(this.mode)
	platform.Init(command_line_parser)

	this.platform = platform
}

func (this *Simulator) Fini() {
	if this.platform != nil {
		this.platform.Fini()
	}
}

func (this *Simulator) IsFinished() bool {
	if this.platform == nil {
		return true
	}

	return this.platform.IsFinished()
}

func (this *Simulator) Cycle() {
	if this.platform != nil {
		this.platform.Cycle()
	}
}

func (this *Simulator) Dump() {
	if this.platform != nil {
		this.platform.Dump()
	}
}
