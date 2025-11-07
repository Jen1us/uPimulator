package simulator

import (
	"fmt"
	"uPIMulator/src/misc"
)

type Platform interface {
	Init(command_line_parser *misc.CommandLineParser)
	Fini()
	IsFinished() bool
	Cycle()
	Dump()
}

func newPlatformForMode(mode misc.PlatformMode) Platform {
	switch mode {
	case misc.PlatformModeUpmem:
		return new(UpmemPlatform)
	case misc.PlatformModeChiplet:
		return new(ChipletPlatform)
	default:
		panic(fmt.Sprintf("unsupported platform mode: %s", mode))
	}
}
