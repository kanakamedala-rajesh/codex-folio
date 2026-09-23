package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

const TrustHeaderName = "X-CodexFolio-Trust"

// BrowserTrustStore persists only a digest, never the browser credential.
type BrowserTrustStore interface {
	GrantBrowserTrust(context.Context, []byte, time.Time) error
	BrowserTrusted(context.Context, []byte) (bool, error)
	ForgetBrowser(context.Context, []byte) error
	RevokeAllBrowsers(context.Context) error
}

func (server *Server) issueSession(response http.ResponseWriter, now time.Time, trustDigest []byte) (string, error) {
	sessionBytes, err := server.randomBytes(randomTokenSize)
	if err != nil {
		return "", err
	}
	csrfBytes, err := server.randomBytes(randomTokenSize)
	if err != nil {
		return "", err
	}
	sessionID := base64.RawURLEncoding.EncodeToString(sessionBytes)
	csrfToken := base64.RawURLEncoding.EncodeToString(csrfBytes)
	current := session{csrfDigest: sha256.Sum256([]byte(csrfToken)), expiresAt: now.Add(server.sessionTTL)}
	if len(trustDigest) == sha256.Size {
		copy(current.trustDigest[:], trustDigest)
		current.trusted = true
	}
	server.mu.Lock()
	server.sessions[sha256.Sum256([]byte(sessionID))] = current
	server.mu.Unlock()
	http.SetCookie(response, &http.Cookie{Name: SessionCookieName, Value: sessionID, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(server.Origin(), "https://"), SameSite: http.SameSiteStrictMode, MaxAge: maxAge(server.sessionTTL)})
	return csrfToken, nil
}

func (server *Server) browserTrustHandler(response http.ResponseWriter, request *http.Request) {
	if server.browserTrust == nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	if request.Method == http.MethodGet {
		if !server.authorize(response, request) {
			return
		}
		trusted, err := server.requestTrusted(request)
		if err != nil {
			server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
			return
		}
		writeJSON(response, http.StatusOK, BrowserTrustResponse{Trusted: trusted})
		return
	}
	if request.Method != http.MethodPost {
		server.writeMethodError(response, "GET, POST")
		return
	}
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || request.ContentLength > maxBootstrapBodySize {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.HTTPAPIBootstrapInvalid)
		return
	}
	var input BrowserTrustRequest
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxBootstrapBodySize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.HTTPAPIBootstrapInvalid)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.HTTPAPIBootstrapInvalid)
		return
	}

	if input.Action == "renew" {
		// A custom header plus the exact Origin/Host checks excludes simple cross-site
		// requests. There is no prior session CSRF value after a service restart.
		if len(request.Header.Values("X-CodexFolio-Renew")) != 1 || request.Header.Get("X-CodexFolio-Renew") != "1" || len(request.Header.Values("Origin")) != 1 || request.Header.Get("Origin") != server.Origin() {
			server.writeAPIError(response, http.StatusForbidden, apperrors.HTTPAPICSRFInvalid)
			return
		}
		digest, valid, err := server.trustDigest(request)
		if err != nil {
			server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
			return
		}
		if !valid {
			server.writeAPIError(response, http.StatusUnauthorized, apperrors.HTTPAPISessionInvalid)
			return
		}
		csrf, err := server.issueSession(response, server.clock.Now().UTC(), digest)
		if err != nil {
			server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
			return
		}
		writeJSON(response, http.StatusOK, BrowserTrustResponse{Trusted: true, CSRFToken: &csrf})
		return
	}
	if !server.authorize(response, request) {
		return
	}
	if !server.validCSRF(request) {
		server.writeAPIError(response, http.StatusForbidden, apperrors.HTTPAPICSRFInvalid)
		return
	}
	switch input.Action {
	case "grant":
		digest, valid, err := server.trustDigest(request)
		if err != nil {
			server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
			return
		}
		if !valid {
			credential, randomErr := server.randomBytes(randomTokenSize)
			if randomErr != nil {
				server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
				return
			}
			digestValue := sha256.Sum256([]byte(base64.RawURLEncoding.EncodeToString(credential)))
			if err := server.browserTrust.GrantBrowserTrust(request.Context(), digestValue[:], server.clock.Now().UTC()); err != nil {
				server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
				return
			}
			digest = digestValue[:]
			value := base64.RawURLEncoding.EncodeToString(credential)
			server.bindSessionTrust(request, digest)
			writeJSON(response, http.StatusOK, BrowserTrustResponse{Trusted: true, Credential: &value})
			return
		}
		server.bindSessionTrust(request, digest)
		writeJSON(response, http.StatusOK, BrowserTrustResponse{Trusted: true})
	case "forget":
		digest, valid, err := server.trustDigest(request)
		if err != nil {
			server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
			return
		}
		if valid {
			if err := server.browserTrust.ForgetBrowser(request.Context(), digest); err != nil {
				server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
				return
			}
			server.invalidateTrustSessions(digest)
		}
		server.invalidateCurrentSession(request)
		server.clearSessionCookie(response)
		writeJSON(response, http.StatusOK, BrowserTrustResponse{Trusted: false})
	case "revoke_all":
		if err := server.browserTrust.RevokeAllBrowsers(request.Context()); err != nil {
			server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
			return
		}
		server.mu.Lock()
		server.sessions = make(map[[sha256.Size]byte]session)
		server.mu.Unlock()
		server.clearSessionCookie(response)
		writeJSON(response, http.StatusOK, BrowserTrustResponse{Trusted: false})
	default:
		server.writeAPIError(response, http.StatusBadRequest, apperrors.HTTPAPIBootstrapInvalid)
	}
}

func (server *Server) trustDigest(request *http.Request) ([]byte, bool, error) {
	values := request.Header.Values(TrustHeaderName)
	if len(values) != 1 {
		return nil, false, nil
	}
	value := values[0]
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != randomTokenSize {
		return nil, false, nil
	}
	digest := sha256.Sum256([]byte(value))
	valid, err := server.browserTrust.BrowserTrusted(request.Context(), digest[:])
	return digest[:], valid, err
}

func (server *Server) requestTrusted(request *http.Request) (bool, error) {
	_, trusted, err := server.trustDigest(request)
	return trusted, err
}

func (server *Server) bindSessionTrust(request *http.Request, digest []byte) {
	cookie, err := request.Cookie(SessionCookieName)
	if err != nil {
		return
	}
	key := sha256.Sum256([]byte(cookie.Value))
	server.mu.Lock()
	current, ok := server.sessions[key]
	if ok {
		copy(current.trustDigest[:], digest)
		current.trusted = true
		server.sessions[key] = current
	}
	server.mu.Unlock()
}

func (server *Server) invalidateTrustSessions(digest []byte) {
	server.mu.Lock()
	for key, current := range server.sessions {
		if current.trusted && string(current.trustDigest[:]) == string(digest) {
			delete(server.sessions, key)
		}
	}
	server.mu.Unlock()
}

func (server *Server) invalidateCurrentSession(request *http.Request) {
	cookie, err := request.Cookie(SessionCookieName)
	if err != nil {
		return
	}
	server.mu.Lock()
	delete(server.sessions, sha256.Sum256([]byte(cookie.Value)))
	server.mu.Unlock()
}

func (server *Server) clearSessionCookie(response http.ResponseWriter) {
	http.SetCookie(response, &http.Cookie{Name: SessionCookieName, Path: "/", HttpOnly: true, Secure: strings.HasPrefix(server.Origin(), "https://"), SameSite: http.SameSiteStrictMode, MaxAge: -1})
}
