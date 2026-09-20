// Package telemetryadapter contains outward telemetry adapters. This ticket
// intentionally provides no production network endpoint.
package telemetryadapter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"

	"venkatasudha.com/codex-folio/internal/telemetry"
)

type DisabledTransport struct{}

func (DisabledTransport) Send(context.Context, telemetry.EventV1) error {
	return telemetry.ErrTransportDisabled
}

func (DisabledTransport) DeleteInstallation(context.Context, string) error {
	return telemetry.ErrTransportDisabled
}

// DisabledPrerequisites is the production-safe provider until every privacy
// and operations prerequisite has deployable evidence.
type DisabledPrerequisites struct{}

func (DisabledPrerequisites) TelemetryPrerequisites(context.Context) (telemetry.Prerequisites, error) {
	return telemetry.Prerequisites{}, nil
}

type RandomIDGenerator struct{}

func (RandomIDGenerator) NewInstallationID() (string, error) {
	value := make([]byte, telemetry.InstallationIDBytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

// RecordingTransport is a deterministic test adapter. Failure is returned
// after recording so callers can prove attempted delivery and isolation.
type RecordingTransport struct {
	mu        sync.Mutex
	events    []telemetry.EventV1
	deletions []string
	err       error
}

func (transport *RecordingTransport) DeleteInstallation(ctx context.Context, installationID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	transport.mu.Lock()
	transport.deletions = append(transport.deletions, installationID)
	err := transport.err
	transport.mu.Unlock()
	return err
}

func NewRecordingTransport(err error) *RecordingTransport {
	return &RecordingTransport{err: err}
}

func (transport *RecordingTransport) Send(ctx context.Context, event telemetry.EventV1) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	transport.mu.Lock()
	transport.events = append(transport.events, event)
	err := transport.err
	transport.mu.Unlock()
	return err
}

func (transport *RecordingTransport) Events() []telemetry.EventV1 {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return append([]telemetry.EventV1(nil), transport.events...)
}

func (transport *RecordingTransport) Deletions() []string {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return append([]string(nil), transport.deletions...)
}
