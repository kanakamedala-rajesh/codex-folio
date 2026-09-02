//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

func foregroundSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT}
}

func forwardForegroundSignals(foregroundProcess) func() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, foregroundSignals()...)
	return func() {
		signal.Stop(signals)
	}
}
