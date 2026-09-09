package codex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/continuation"
)

func TestHistoryReaderExtractsOnlyBoundedLabelledCandidates(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "codex")
	home := filepath.Join(t.TempDir(), "source-home")
	threadID := "018f4f70-6f77-7c3f-9b77-93aa087dfc4d"
	var input string
	reader := NewHistoryReaderWithCommandRunner(func(_ context.Context, gotExecutable string, args, environment []string, stdin io.Reader, stdout, _ io.Writer) error {
		if gotExecutable != executable || strings.Join(args, " ") != "app-server --stdio" || environmentValue(environment, "CODEX_HOME") != filepath.Clean(home) {
			t.Fatalf("command = %q %v, CODEX_HOME %q", gotExecutable, args, environmentValue(environment, "CODEX_HOME"))
		}
		got, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		input = string(got)
		_, err = io.WriteString(stdout, `{"id":4,"result":{"thread":{"id":"018f4f70-6f77-7c3f-9b77-93aa087dfc4d","status":{"type":"notLoaded"},"turns":[{"items":[{"type":"userMessage","content":[{"type":"text","text":"raw prompt sentinel\nGoal: finish ticket 52\nNext action: run focused tests"}]},{"type":"commandExecution","aggregatedOutput":"credential sentinel"},{"type":"agentMessage","text":"raw reply sentinel\nCompleted work: adapter contract\nPending work: CLI approval\nRisks: history may be unavailable"}]}]}}}`+"\n")
		return err
	})

	fields, err := reader.Read(context.Background(), continuation.HistoryReadRequest{Executable: executable, IdentityHome: home, ThreadID: threadID})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if fields.Goal.Value != "finish ticket 52" || fields.CompletedWork.Value != "adapter contract" || fields.PendingWork.Value != "CLI approval" || fields.Risks.Value != "history may be unavailable" || fields.NextAction.Value != "run focused tests" {
		t.Fatalf("candidates = %#v", fields)
	}
	if fields.Goal.Provenance != continuation.ProvenanceUserConfirmed || fields.CompletedWork.Provenance != continuation.ProvenanceModelDerived || fields.Goal.Completeness != continuation.CompletenessPartial || fields.CompletedWork.Completeness != continuation.CompletenessPartial {
		t.Fatalf("candidate evidence = %#v", fields)
	}
	encoded := string(mustMarshalHistory(t, fields))
	for _, forbidden := range []string{"raw prompt sentinel", "raw reply sentinel", "credential sentinel"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("candidate facts contain %q: %s", forbidden, encoded)
		}
	}
	if !strings.Contains(input, `"method":"thread/read"`) || !strings.Contains(input, `"threadId":"`+threadID+`"`) || !strings.Contains(input, `"includeTurns":true`) {
		t.Fatalf("thread/read request = %q", input)
	}
}

func TestHistoryReaderRejectsUnsafeOrUnavailableHistory(t *testing.T) {
	threadID := "018f4f70-6f77-7c3f-9b77-93aa087dfc4d"
	for _, test := range []struct {
		name, output string
		readErr      error
	}{
		{name: "missing", output: `{"id":4,"error":{"code":-32602,"message":"thread not found"}}`},
		{name: "unsupported", output: `{"id":4,"error":{"code":-32601,"message":"method not found"}}`},
		{name: "active", output: `{"id":4,"result":{"thread":{"id":"018f4f70-6f77-7c3f-9b77-93aa087dfc4d","status":{"type":"active"},"turns":[]}}}`},
		{name: "malformed", output: `{"id":4,"result":`},
		{name: "unlabelled", output: `{"id":4,"result":{"thread":{"id":"018f4f70-6f77-7c3f-9b77-93aa087dfc4d","status":{"type":"idle"},"turns":[{"items":[{"type":"agentMessage","text":"raw reply only"},{"type":"commandExecution","aggregatedOutput":"Goal: unsafe tool output"}]}]}}}`},
		{name: "oversized", output: `{"id":4,"result":{"thread":{"id":"018f4f70-6f77-7c3f-9b77-93aa087dfc4d","status":{"type":"idle"},"turns":[]}}}` + strings.Repeat(" ", maxHistoryResponseBytes)},
		{name: "workspace policy disallowed", readErr: errors.New("workspace policy disallowed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := NewHistoryReaderWithCommandRunner(func(_ context.Context, _ string, _ []string, _ []string, _ io.Reader, stdout, _ io.Writer) error {
				if test.readErr != nil {
					return test.readErr
				}
				_, err := io.WriteString(stdout, test.output)
				return err
			})
			fields, err := reader.Read(context.Background(), continuation.HistoryReadRequest{
				Executable: filepath.Join(t.TempDir(), "codex"), IdentityHome: filepath.Join(t.TempDir(), "home"), ThreadID: threadID,
			})
			if !errors.Is(err, continuation.ErrHistoryUnavailable) || !reflect.DeepEqual(fields, continuation.CheckpointFields{}) {
				t.Fatalf("Read() = %#v, %v; want unavailable with no candidates", fields, err)
			}
		})
	}
}

func mustMarshalHistory(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
