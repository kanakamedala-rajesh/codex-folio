//go:build !windows

package main

import (
	"os"
	"syscall"
)

func serviceStopSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
