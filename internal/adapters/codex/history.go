package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"venkatasudha.com/codex-folio/internal/continuation"
)

const (
	maxHistoryResponseBytes = 64 * 1024
	maxHistoryFactBytes     = 2 * 1024
)

type HistoryReader struct {
	appServer appServerRunner
}

func NewHistoryReader() *HistoryReader {
	return &HistoryReader{appServer: runAppServer}
}

func NewHistoryReaderWithCommandRunner(run CommandRunner) *HistoryReader {
	if run == nil {
		run = runCommand
	}
	return &HistoryReader{appServer: appServerWithCommandRunner(run)}
}

func (reader *HistoryReader) Read(ctx context.Context, request continuation.HistoryReadRequest) (continuation.CheckpointFields, error) {
	if reader == nil || reader.appServer == nil || !filepath.IsAbs(request.Executable) || !filepath.IsAbs(request.IdentityHome) || !validUUID(request.ThreadID) {
		return continuation.CheckpointFields{}, continuation.ErrHistoryUnavailable
	}
	payload, err := json.Marshal(struct {
		Method string `json:"method"`
		ID     int    `json:"id"`
		Params struct {
			ThreadID     string `json:"threadId"`
			IncludeTurns bool   `json:"includeTurns"`
		} `json:"params"`
	}{Method: "thread/read", ID: 4, Params: struct {
		ThreadID     string `json:"threadId"`
		IncludeTurns bool   `json:"includeTurns"`
	}{ThreadID: strings.ToLower(request.ThreadID), IncludeTurns: true}})
	if err != nil {
		return continuation.CheckpointFields{}, continuation.ErrHistoryUnavailable
	}
	output, err := reader.appServer(contextOrBackground(ctx), request.Executable, codexEnvironment(request.IdentityHome), string(payload), 4)
	if err != nil || len(output) > maxHistoryResponseBytes {
		return continuation.CheckpointFields{}, continuation.ErrHistoryUnavailable
	}
	return historyCandidates(output, request.ThreadID)
}

func historyCandidates(output []byte, threadID string) (continuation.CheckpointFields, error) {
	var response struct {
		ID     json.RawMessage `json:"id"`
		Error  json.RawMessage `json:"error"`
		Result *struct {
			Thread struct {
				ID     string `json:"id"`
				Status struct {
					Type string `json:"type"`
				} `json:"status"`
				Turns []struct {
					Items []struct {
						Type    string `json:"type"`
						Text    string `json:"text"`
						Content []struct {
							Type string `json:"type"`
							Text string `json:"text"`
						} `json:"content"`
					} `json:"items"`
				} `json:"turns"`
			} `json:"thread"`
		} `json:"result"`
	}
	trimmed := bytes.TrimSpace(output)
	if json.Unmarshal(trimmed, &response) != nil || !bytes.Equal(bytes.TrimSpace(response.ID), []byte("4")) || response.Result == nil || (len(response.Error) != 0 && !bytes.Equal(bytes.TrimSpace(response.Error), []byte("null"))) {
		return continuation.CheckpointFields{}, continuation.ErrHistoryUnavailable
	}
	thread := response.Result.Thread
	if !strings.EqualFold(thread.ID, threadID) || (thread.Status.Type != "notLoaded" && thread.Status.Type != "idle") {
		return continuation.CheckpointFields{}, continuation.ErrHistoryUnavailable
	}
	values := map[string]continuation.Evidence[string]{}
	for _, turn := range thread.Turns {
		for _, item := range turn.Items {
			switch item.Type {
			case "userMessage":
				for _, content := range item.Content {
					if content.Type == "text" {
						extractHistoryFacts(values, content.Text, continuation.ProvenanceUserConfirmed)
					}
				}
			case "agentMessage":
				extractHistoryFacts(values, item.Text, continuation.ProvenanceModelDerived)
			}
		}
	}
	if len(values) == 0 {
		return continuation.CheckpointFields{}, continuation.ErrHistoryUnavailable
	}
	return continuation.CheckpointFields{
		Goal: values["goal"], CompletedWork: values["completed work"], PendingWork: values["pending work"],
		Risks: values["risks"], NextAction: values["next action"],
	}, nil
}

func extractHistoryFacts(values map[string]continuation.Evidence[string], text, provenance string) {
	for _, line := range strings.Split(text, "\n") {
		label, value, found := strings.Cut(line, ":")
		label, value = strings.ToLower(strings.TrimSpace(label)), strings.TrimSpace(value)
		if !found || value == "" || len(value) > maxHistoryFactBytes {
			continue
		}
		switch label {
		case "goal", "completed work", "pending work", "risks", "next action":
			values[label] = continuation.Evidence[string]{Value: value, Provenance: provenance, Completeness: continuation.CompletenessPartial}
		}
	}
}
