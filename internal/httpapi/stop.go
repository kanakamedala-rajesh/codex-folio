package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

type activeLaunchCounter interface {
	ActiveManagedLaunchCount(context.Context) (int, error)
}

type CommandStopResponse struct {
	State          string `json:"state"`
	ActiveLaunches int    `json:"active_launches"`
}

// StopReady closes only after the owner has accepted a safe shutdown.
func (server *Server) StopReady() <-chan struct{} { return server.stopReady }

// ReconcileDeferredStop observes completed processes even when the foreground
// launcher could not deliver its final state update.
func (server *Server) ReconcileDeferredStop(ctx context.Context) {
	server.stopMu.Lock()
	defer server.stopMu.Unlock()
	server.completeDeferredStop(ctx)
}

func (server *Server) completeDeferredStop(ctx context.Context) {
	if !server.stopRequested || server.stopCommitted {
		return
	}
	counter, ok := server.launches.(activeLaunchCounter)
	if !ok {
		return
	}
	count, err := counter.ActiveManagedLaunchCount(ctx)
	if err == nil && count == 0 {
		server.commitStop()
	}
}

// Caller holds stopMu. Closing the channel is a one-way transition; an
// accepted prepare can cancel only a deferred request, never a committed stop.
func (server *Server) commitStop() {
	server.stopCommitted = true
	close(server.stopReady)
}

func (client *CommandClient) Stop(ctx context.Context, action string) (CommandStopResponse, error) {
	var result CommandStopResponse
	body, _ := json.Marshal(struct {
		Action string `json:"action"`
	}{action})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.origin+CommandStopPath, bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", client.origin)
	request.Header.Set(CommandTokenHeader, client.token)
	response, err := client.httpDoer().Do(request)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Code string `json:"code"`
		}
		if json.NewDecoder(response.Body).Decode(&failure) == nil && failure.Code != "" {
			return result, apperrors.New(failure.Code, fmt.Errorf("stop request returned HTTP %d", response.StatusCode))
		}
		return result, fmt.Errorf("stop request returned HTTP %d", response.StatusCode)
	}
	return result, json.NewDecoder(response.Body).Decode(&result)
}

func (server *Server) commandStop(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		server.writeMethodError(response, http.MethodPost)
		return
	}
	var input struct {
		Action string `json:"action"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || (input.Action != "request" && input.Action != "defer" && input.Action != "cancel" && input.Action != "status") {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.CLIUsage)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.CLIUsage)
		return
	}
	server.stopMu.Lock()
	defer server.stopMu.Unlock()
	result := CommandStopResponse{State: "running"}
	if server.stopCommitted {
		result.State = "stopping"
		writeJSON(response, http.StatusOK, result)
		return
	}
	if input.Action == "cancel" {
		server.stopRequested = false
	}
	counter, ok := server.launches.(activeLaunchCounter)
	if !ok && (server.operational.Load() || server.launches != nil) {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	count := 0
	if ok {
		var err error
		count, err = counter.ActiveManagedLaunchCount(request.Context())
		if err != nil {
			server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
			return
		}
	}
	result.ActiveLaunches = count
	if input.Action == "defer" || input.Action == "request" && count == 0 || server.stopRequested {
		server.stopRequested = true
		if count == 0 {
			server.commitStop()
		}
	}
	if server.stopCommitted {
		result.State = "stopping"
	} else if server.stopRequested {
		result.State = "pending"
	}
	writeJSON(response, http.StatusOK, result)
}
