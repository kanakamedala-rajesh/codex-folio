//go:build windows

package main

import "os"

func serviceStopSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
