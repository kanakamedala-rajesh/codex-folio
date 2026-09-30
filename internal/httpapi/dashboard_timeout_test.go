package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

func TestDashboardReadDeadlineReturnsSafeJSONOverTLS(t *testing.T) {
	cancelled := make(chan error, 2)
	handler := dashboardReadHandler(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		cancelled <- r.Context().Err()
		// A late result must never replace the timeout response.
		_, _ = w.Write([]byte("private-late-result"))
	}, 50*time.Millisecond)
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	results := make(chan error, 2)
	for _, path := range []string{"/api/v1/analytics?scope=combined_identity", "/api/v1/alerts"} {
		go func() {
			response, err := server.Client().Get(server.URL + path)
			if err != nil {
				results <- err
				return
			}
			defer response.Body.Close()
			var body map[string]string
			err = json.NewDecoder(response.Body).Decode(&body)
			if err == nil && (response.StatusCode != http.StatusServiceUnavailable || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") || body["code"] != apperrors.HTTPAPIServiceUnavailable || body["message"] != safeMessage(apperrors.HTTPAPIServiceUnavailable)) {
				err = errors.New("timeout response did not preserve the safe JSON API contract")
			}
			results <- err
		}()
	}
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent reads did not return bounded TLS responses")
		}
		if err := <-cancelled; !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("handler cancellation = %v", err)
		}
	}
}

func TestDashboardReadDeadlinePreservesSuccessfulResponse(t *testing.T) {
	response := httptest.NewRecorder()
	dashboardReadHandler(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Context().Deadline(); !ok {
			t.Error("missing request deadline")
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}, dashboardReadTimeout).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ready"`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}
