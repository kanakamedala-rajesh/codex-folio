package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

// Leave time to send a safe response before the server's 15-second write
// deadline. TimeoutHandler also cancels queued SQLite and native-helper work
// and prevents a late handler from writing to the expired connection.
const dashboardReadTimeout = 10 * time.Second

func dashboardReadHandler(handler http.HandlerFunc, timeout time.Duration) http.Handler {
	body, _ := json.Marshal(map[string]string{
		"code":    apperrors.HTTPAPIServiceUnavailable,
		"message": safeMessage(apperrors.HTTPAPIServiceUnavailable),
	})
	bounded := http.TimeoutHandler(handler, timeout, string(body))
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		bounded.ServeHTTP(dashboardJSONWriter{response}, request)
	})
}

// The standard timeout handler defaults to HTML; these endpoints, including
// their timeout responses, use the existing JSON API error contract.
type dashboardJSONWriter struct{ http.ResponseWriter }

func (response dashboardJSONWriter) WriteHeader(status int) {
	response.Header().Set("Content-Type", "application/json")
	response.ResponseWriter.WriteHeader(status)
}
