package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"venkatasudha.com/codex-folio/internal/launch"
)

type stopLaunchFixture struct {
	count  int
	failed bool
}

func (f *stopLaunchFixture) ActiveManagedLaunchCount(context.Context) (int, error) {
	if f.failed {
		return 0, context.DeadlineExceeded
	}
	return f.count, nil
}
func (f *stopLaunchFixture) Prepare(context.Context, launch.PrepareRequest, string) (launch.Plan, string, error) {
	f.count++
	return launch.Plan{}, "", nil
}
func (f *stopLaunchFixture) PrepareHandoff(context.Context, launch.PrepareRequest, string, string, string) (launch.Plan, error) {
	f.count++
	return launch.Plan{}, nil
}
func (f *stopLaunchFixture) MarkStarted(context.Context, string, int) error { return nil }
func (f *stopLaunchFixture) MarkExited(context.Context, string, int, string, string) (*launch.SafeContinuationOffer, error) {
	f.count--
	return nil, nil
}
func (f *stopLaunchFixture) MarkAbandoned(context.Context, string) error { f.count--; return nil }

func stopRequest(t *testing.T, server *Server, action string) CommandStopResponse {
	t.Helper()
	data, _ := json.Marshal(map[string]string{"action": action})
	response := httptest.NewRecorder()
	server.commandStop(response, httptest.NewRequest(http.MethodPost, CommandStopPath, bytes.NewReader(data)))
	if response.Code != http.StatusOK {
		t.Fatalf("stop %s: %d %s", action, response.Code, response.Body.String())
	}
	var result CommandStopResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCompanionStopTransitions(t *testing.T) {
	launches := &stopLaunchFixture{count: 1}
	server, err := NewServer(Options{Launches: launches})
	if err != nil {
		t.Fatal(err)
	}
	if result := stopRequest(t, server, "request"); result.State != "running" || result.ActiveLaunches != 1 {
		t.Fatalf("refusal offer: %+v", result)
	}
	if result := stopRequest(t, server, "defer"); result.State != "pending" {
		t.Fatalf("deferred: %+v", result)
	}
	if result := stopRequest(t, server, "cancel"); result.State != "running" {
		t.Fatalf("cancelled: %+v", result)
	}
	stopRequest(t, server, "defer")
	prepared := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, CommandLaunchPath, bytes.NewBufferString(`{"action":"prepare"}`))
	request.Header.Set("Content-Type", "application/json")
	server.commandLaunch(prepared, request)
	if prepared.Code != http.StatusOK {
		t.Fatalf("new prepare = %d: %s", prepared.Code, prepared.Body.String())
	}
	if result := stopRequest(t, server, "status"); result.State != "running" || result.ActiveLaunches != 2 {
		t.Fatalf("new launch: %+v", result)
	}
	stopRequest(t, server, "defer")
	for range 2 {
		exited := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, CommandLaunchPath, bytes.NewBufferString(`{"action":"exited"}`))
		request.Header.Set("Content-Type", "application/json")
		server.commandLaunch(exited, request)
		if exited.Code != http.StatusOK {
			t.Fatalf("exit = %d: %s", exited.Code, exited.Body.String())
		}
	}
	select {
	case <-server.StopReady():
	default:
		t.Fatal("deferred stop did not commit")
	}
}

func TestCompanionStopNeverAssumesIdleOnReadFailure(t *testing.T) {
	launches := &stopLaunchFixture{failed: true}
	server, err := NewServer(Options{Launches: launches})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.commandStop(response, httptest.NewRequest(http.MethodPost, CommandStopPath, bytes.NewBufferString(`{"action":"defer"}`)))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
	select {
	case <-server.StopReady():
		t.Fatal("uncertain state stopped service")
	default:
	}
}

func TestCompanionConcurrentStopRequestsCommitOnce(t *testing.T) {
	server, err := NewServer(Options{Launches: &stopLaunchFixture{}})
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() { defer wait.Done(); stopRequest(t, server, "request") }()
	}
	wait.Wait()
	select {
	case <-server.StopReady():
	default:
		t.Fatal("idle stop did not commit")
	}
}
