//go:build !windows

package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
)

type recordingServiceCloser struct {
	name  string
	order *[]string
	err   error
}

func (closer recordingServiceCloser) Close() error {
	*closer.order = append(*closer.order, closer.name)
	return closer.err
}

func TestServiceStopSignalsIncludeInterruptAndSIGTERMUnix(t *testing.T) {
	t.Parallel()
	wanted := []os.Signal{os.Interrupt, syscall.SIGTERM}
	for _, signal := range wanted {
		found := false
		for _, candidate := range serviceStopSignals() {
			if candidate == signal {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("serviceStopSignals() = %v, want %v", serviceStopSignals(), signal)
		}
	}
}

func TestServiceStopCleanupClosesResourcesInOrderWithoutProcessSignal(t *testing.T) {
	t.Parallel()
	order := []string{}
	stderr := &bytes.Buffer{}
	code := closeServiceAfterStop(
		recordingServiceCloser{name: "server", order: &order},
		recordingServiceCloser{name: "state", order: &order},
		recordingServiceCloser{name: "owner", order: &order},
		stderr,
		nil,
	)
	if code != exitSuccess || stderr.Len() != 0 {
		t.Fatalf("closeServiceAfterStop() = %d, stderr = %q", code, stderr.String())
	}
	if got := strings.Join(order, ","); got != "server,state,owner" {
		t.Fatalf("close order = %q, want server,state,owner", got)
	}

	order = nil
	code = closeServiceAfterStop(
		recordingServiceCloser{name: "server", order: &order, err: errors.New("close failed")},
		recordingServiceCloser{name: "state", order: &order},
		recordingServiceCloser{name: "owner", order: &order},
		stderr,
		nil,
	)
	if code != exitFailure || strings.Join(order, ",") != "server,state,owner" {
		t.Fatalf("failed close = %d, order = %v; want failure with full cleanup", code, order)
	}
}
