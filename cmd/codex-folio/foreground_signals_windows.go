//go:build windows

package main

import (
	"os"
	"os/signal"
)

func foregroundSignals() []os.Signal { return []os.Signal{os.Interrupt} }

func forwardForegroundSignals(process foregroundProcess) func() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, foregroundSignals()...)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case value := <-signals:
				_ = process.Signal(value)
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(signals)
		close(done)
	}
}
