//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

func TestForegroundSignalsKeepParentAliveWithoutForwardingUnix(t *testing.T) {
	process := &launchTestProcess{}
	stop := forwardForegroundSignals(process)
	defer stop()

	received := make(chan os.Signal, 1)
	signal.Notify(received, syscall.SIGTERM)
	defer signal.Stop(received)
	self, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("FindProcess() error = %v", err)
	}
	if err := self.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("Signal() error = %v", err)
	}
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for signal")
	}
	if len(process.signals) != 0 {
		t.Fatalf("forwarded signals = %v, want none", process.signals)
	}
}
