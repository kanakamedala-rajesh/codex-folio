package diagnostics

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

type fixedClock struct {
	now time.Time
}

func (clock *fixedClock) Now() time.Time { return clock.now }

func TestNewEventOnlyAcceptsTheAllowlistedRedactedShape(t *testing.T) {
	at := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	event, err := NewEvent(at, SeverityWarning, ComponentStore, apperrors.StoreIntegrityFailed, Context{
		Operation:     OperationIntegrityCheck,
		State:         StateFailed,
		SchemaVersion: 1,
	})
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, forbidden := range []string{
		"message",
		"cause",
		"canonical_path",
		"bootstrap_token",
		"csrf",
		"ciphertext",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("encoded event %q contains forbidden field %q", encoded, forbidden)
		}
	}

	for _, context := range []Context{
		{Operation: "login-identity-sentinel"},
		{Operation: "/home/user/repository"},
		{State: "raw provider payload"},
		{VaultTier: "passphrase-sentinel"},
	} {
		if _, err := NewEvent(at, SeverityError, ComponentStore, apperrors.StoreReadFailed, context); err == nil || apperrors.Code(err) != apperrors.DiagnosticsEventInvalid {
			t.Fatalf("NewEvent(%#v) error = %v, want diagnostics event-invalid", context, err)
		}
	}
}

func TestRecorderBoundsRepeatedErrorsByCountBytesAndRetention(t *testing.T) {
	clock := &fixedClock{now: time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)}
	recorder, err := NewRecorder(RecorderOptions{
		Clock:           clock,
		MaxEvents:       2,
		MaxEncodedBytes: 4096,
		Retention:       time.Hour,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}

	old, err := NewEvent(clock.now.Add(-2*time.Hour), SeverityError, ComponentStore, apperrors.StoreReadFailed, Context{Operation: OperationOpenStore})
	if err != nil {
		t.Fatalf("old NewEvent() error = %v", err)
	}
	if err := recorder.Record(old); err != nil {
		t.Fatalf("Record(old) error = %v", err)
	}
	if recorder.Len() != 0 {
		t.Fatalf("Len() after expired event = %d, want 0", recorder.Len())
	}

	for index := 0; index < 20; index++ {
		event, eventErr := NewEvent(clock.now.Add(time.Duration(index)*time.Second), SeverityError, ComponentStore, apperrors.StoreReadFailed, Context{Operation: OperationOpenStore})
		if eventErr != nil {
			t.Fatalf("NewEvent(%d) error = %v", index, eventErr)
		}
		if err := recorder.Record(event); err != nil {
			t.Fatalf("Record(%d) error = %v", index, err)
		}
	}
	if recorder.Len() != 2 {
		t.Fatalf("Len() = %d, want bounded length 2", recorder.Len())
	}
	if recorder.EncodedBytes() > 4096 {
		t.Fatalf("EncodedBytes() = %d, want <= 4096", recorder.EncodedBytes())
	}
	if got := recorder.Events()[0].Time; !got.Equal(clock.now.Add(18 * time.Second)) {
		t.Fatalf("oldest retained event = %s, want the newest bounded window", got)
	}

	clock.now = clock.now.Add(2 * time.Hour)
	if events := recorder.Events(); len(events) != 0 {
		t.Fatalf("Events() after read-time expiry = %#v, want empty", events)
	}
	if recorder.Len() != 0 {
		t.Fatalf("Len() after read-time expiry = %d, want 0", recorder.Len())
	}
	if recorder.EncodedBytes() != 0 {
		t.Fatalf("EncodedBytes() after read-time expiry = %d, want 0", recorder.EncodedBytes())
	}

	event, err := NewEvent(clock.now, SeverityWarning, ComponentPlatform, apperrors.PlatformPermissionDenied, Context{Operation: OperationAcquireOwner})
	if err != nil {
		t.Fatalf("new-time NewEvent() error = %v", err)
	}
	if err := recorder.Record(event); err != nil {
		t.Fatalf("Record(new-time) error = %v", err)
	}
	if recorder.Len() != 1 || !recorder.Events()[0].Time.Equal(clock.now) {
		t.Fatalf("retained events after expiry = %#v, want only the current event", recorder.Events())
	}
}

func TestRecordErrorDropsTheUnderlyingCause(t *testing.T) {
	clock := &fixedClock{now: time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)}
	recorder, err := NewRecorder(RecorderOptions{Clock: clock})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	sentinel := errors.New("login identity, repository path, cookie, and key sentinel")
	if err := recorder.RecordError(ComponentVault, OperationVault, SeverityError, apperrors.New(apperrors.VaultLocked, sentinel)); err != nil {
		t.Fatalf("RecordError() error = %v", err)
	}
	encoded, err := json.Marshal(recorder.Events())
	if err != nil {
		t.Fatalf("json.Marshal(events) error = %v", err)
	}
	if strings.Contains(string(encoded), sentinel.Error()) {
		t.Fatalf("events contain the underlying cause: %s", encoded)
	}
	if recorder.Events()[0].ErrorCode != apperrors.VaultLocked {
		t.Fatalf("event error code = %q, want %q", recorder.Events()[0].ErrorCode, apperrors.VaultLocked)
	}
}

func TestRecorderRejectsUnboundedConfiguration(t *testing.T) {
	if _, err := NewRecorder(RecorderOptions{MaxEvents: MaxRecorderEvents + 1}); err == nil || apperrors.Code(err) != apperrors.DiagnosticsConfigurationInvalid {
		t.Fatalf("NewRecorder() error = %v, want diagnostics configuration-invalid", err)
	}
}
