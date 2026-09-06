//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

func TestForegroundSignalsForwardToChildUnix(t *testing.T) {
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
	deadline := time.Now().Add(time.Second)
	for len(process.signals) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(process.signals) != 1 || process.signals[0] != syscall.SIGTERM {
		t.Fatalf("forwarded signals = %v, want SIGTERM", process.signals)
	}
}

func TestForegroundSignalsDoNotRelayTerminalGroupSignalsUnix(t *testing.T) {
	for _, candidate := range []os.Signal{os.Interrupt, syscall.SIGHUP, syscall.SIGQUIT} {
		for _, relayed := range foregroundSignals() {
			if relayed == candidate {
				t.Fatalf("foregroundSignals() = %v, must not relay %v already delivered to the foreground process group", foregroundSignals(), candidate)
			}
		}
	}
}
