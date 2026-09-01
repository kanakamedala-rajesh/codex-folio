package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/buildinfo"
	"venkatasudha.com/codex-folio/internal/diagnostics"
)

const (
	DefaultBootstrapTTL = 5 * time.Minute
	DefaultSessionTTL   = 15 * time.Minute

	SessionCookieName    = "codexfolio_session"
	CSRFHeaderName       = "X-CodexFolio-CSRF"
	BootstrapPathName    = "/bootstrap"
	BootstrapQueryName   = "bootstrap"
	maxBootstrapBodySize = 4096
	randomTokenSize      = 32
)

const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"

// The browser shell is built ahead of time and kept beside the transport
// adapter so the executable never needs a network or a frontend toolchain at
// runtime.
//
//go:embed assets/index.html assets/app.js assets/styles.css
var embeddedAssets embed.FS

// Clock is the time seam for session and bootstrap expiry tests.
type Clock interface {
	Now() time.Time
}

// Options configures the local browser service. Random is used only for
// in-memory bootstrap, session, and CSRF material; none of those values are
// persisted by this package.
type Options struct {
	Product      string
	BootstrapTTL time.Duration
	SessionTTL   time.Duration
	Random       io.Reader
	Clock        Clock
	Diagnostics  diagnostics.Sink
}

// ServerOptions is retained as a descriptive alias for callers composing the
// service alongside other platform adapters.
type ServerOptions = Options

// Server is the authorization-protected loopback HTTP service for the
// embedded placeholder SPA. It has no durable session or bootstrap state.
type Server struct {
	mu sync.Mutex

	product      string
	bootstrapTTL time.Duration
	sessionTTL   time.Duration
	random       io.Reader
	randomMu     sync.Mutex
	clock        Clock
	diagnostics  diagnostics.Sink

	bootstrapToken     []byte
	bootstrapDigest    [sha256.Size]byte
	bootstrapExpiresAt time.Time
	bootstrapAvailable bool
	sessions           map[[sha256.Size]byte]session

	listener    net.Listener
	httpServer  *http.Server
	closed      bool
	address     string
	allowedHost string
	origin      string
}

type session struct {
	csrfDigest [sha256.Size]byte
	expiresAt  time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now()
}

// NewServer creates a service with a fresh one-time bootstrap secret. The
// caller obtains the launcher URL only after Listen has selected the port.
func NewServer(options Options) (*Server, error) {
	bootstrapTTL := options.BootstrapTTL
	if bootstrapTTL <= 0 {
		bootstrapTTL = DefaultBootstrapTTL
	}
	sessionTTL := options.SessionTTL
	if sessionTTL <= 0 {
		sessionTTL = DefaultSessionTTL
	}
	randomReader := options.Random
	if randomReader == nil {
		randomReader = rand.Reader
	}
	clock := options.Clock
	if clock == nil {
		clock = systemClock{}
	}
	product := options.Product
	if strings.TrimSpace(product) == "" {
		product = buildinfo.ProductName
	}

	token, err := readRandom(randomReader, randomTokenSize)
	if err != nil {
		recordDiagnostic(options.Diagnostics, clock.Now(), diagnostics.ComponentHTTPAPI, diagnostics.OperationHTTPListen, diagnostics.SeverityError, apperrors.HTTPAPIServiceUnavailable, diagnostics.Context{State: diagnostics.StateUnavailable})
		return nil, apperrors.New(apperrors.HTTPAPIServiceUnavailable, err)
	}
	encodedToken := base64.RawURLEncoding.EncodeToString(token)
	now := clock.Now().UTC()
	return &Server{
		product:            product,
		bootstrapTTL:       bootstrapTTL,
		sessionTTL:         sessionTTL,
		random:             randomReader,
		clock:              clock,
		diagnostics:        options.Diagnostics,
		bootstrapToken:     token,
		bootstrapDigest:    sha256.Sum256([]byte(encodedToken)),
		bootstrapExpiresAt: now.Add(bootstrapTTL),
		bootstrapAvailable: true,
		sessions:           make(map[[sha256.Size]byte]session),
	}, nil
}

// Listen binds exclusively to an IPv4 loopback address. It deliberately does
// not accept a caller-supplied bind address so a future CLI flag cannot turn
// the MVP service into a LAN listener by accident.
func (server *Server) Listen() (net.Listener, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		server.recordDiagnostic(diagnostics.OperationHTTPListen, diagnostics.SeverityError, apperrors.HTTPAPIServiceUnavailable, diagnostics.Context{State: diagnostics.StateUnavailable})
		return nil, apperrors.New(apperrors.HTTPAPIServiceUnavailable, err)
	}
	if err := server.attachListener(listener); err != nil {
		_ = listener.Close()
		server.recordDiagnostic(diagnostics.OperationHTTPListen, diagnostics.SeverityError, diagnostics.CodeFor(err, apperrors.HTTPAPIServiceUnavailable), diagnostics.Context{State: diagnostics.StateInvalid})
		return nil, err
	}
	return listener, nil
}

// Serve runs the configured listener until it is closed. Call Close to stop
// it and invalidate all in-memory sessions.
func (server *Server) Serve(listener net.Listener) error {
	if listener == nil {
		server.recordDiagnostic(diagnostics.OperationHTTPListen, diagnostics.SeverityError, apperrors.HTTPAPIServiceUnavailable, diagnostics.Context{State: diagnostics.StateInvalid})
		return apperrors.New(apperrors.HTTPAPIServiceUnavailable, errors.New("listener is required"))
	}
	server.mu.Lock()
	closed := server.closed
	server.mu.Unlock()
	if closed {
		_ = listener.Close()
		return nil
	}
	if err := server.attachListener(listener); err != nil {
		server.recordDiagnostic(diagnostics.OperationHTTPListen, diagnostics.SeverityError, diagnostics.CodeFor(err, apperrors.HTTPAPIServiceUnavailable), diagnostics.Context{State: diagnostics.StateInvalid})
		return err
	}

	server.mu.Lock()
	if server.closed {
		server.mu.Unlock()
		_ = listener.Close()
		return nil
	}
	if server.httpServer != nil {
		server.mu.Unlock()
		server.recordDiagnostic(diagnostics.OperationHTTPListen, diagnostics.SeverityError, apperrors.HTTPAPIServiceUnavailable, diagnostics.Context{State: diagnostics.StateRejected})
		return apperrors.New(apperrors.HTTPAPIServiceUnavailable, errors.New("server is already serving"))
	}
	httpServer := &http.Server{
		Handler:           server,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 * 1024,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	server.httpServer = httpServer
	server.mu.Unlock()

	err := httpServer.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	server.recordDiagnostic(diagnostics.OperationHTTPListen, diagnostics.SeverityError, apperrors.HTTPAPIServiceUnavailable, diagnostics.Context{State: diagnostics.StateUnavailable})
	return apperrors.New(apperrors.HTTPAPIServiceUnavailable, err)
}

// Handler exposes the same secured handler used by Serve for focused tests
// and composition. Production callers should use Listen and Serve together.
func (server *Server) Handler() http.Handler {
	return server
}

// Close stops the HTTP server, releases the listener, and invalidates every
// browser session. Bootstrap material is also cleared so a restart cannot
// reuse it.
func (server *Server) Close() error {
	server.mu.Lock()
	httpServer := server.httpServer
	listener := server.listener
	server.closed = true
	server.httpServer = nil
	server.listener = nil
	server.address = ""
	server.allowedHost = ""
	server.origin = ""
	server.invalidateLocked()
	server.mu.Unlock()

	if httpServer != nil {
		if err := httpServer.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			server.recordDiagnostic(diagnostics.OperationShutdown, diagnostics.SeverityError, apperrors.HTTPAPIServiceUnavailable, diagnostics.Context{State: diagnostics.StateFailed})
			return apperrors.New(apperrors.HTTPAPIServiceUnavailable, err)
		}
		return nil
	}
	if listener != nil {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			server.recordDiagnostic(diagnostics.OperationShutdown, diagnostics.SeverityError, apperrors.HTTPAPIServiceUnavailable, diagnostics.Context{State: diagnostics.StateFailed})
			return apperrors.New(apperrors.HTTPAPIServiceUnavailable, err)
		}
	}
	return nil
}

// Origin returns the exact same-origin URL selected for the listener.
func (server *Server) Origin() string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.origin
}

// Address returns the selected loopback host and port.
func (server *Server) Address() string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.address
}

// BootstrapURL returns the one-time launcher URL. It is empty until Listen
// has selected a port or after the bootstrap secret has been consumed.
func (server *Server) BootstrapURL() string {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.origin == "" || !server.bootstrapAvailable || len(server.bootstrapToken) == 0 {
		return ""
	}
	query := url.Values{}
	query.Set(BootstrapQueryName, base64.RawURLEncoding.EncodeToString(server.bootstrapToken))
	return server.origin + BootstrapPathName + "?" + query.Encode()
}

func (server *Server) attachListener(listener net.Listener) error {
	address, host, origin, err := loopbackAddress(listener)
	if err != nil {
		return apperrors.New(apperrors.HTTPAPIServiceUnavailable, err)
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	if server.closed {
		return apperrors.New(apperrors.HTTPAPIServiceUnavailable, errors.New("server is closed"))
	}
	if server.listener != nil && server.listener.Addr().String() != listener.Addr().String() {
		return apperrors.New(apperrors.HTTPAPIServiceUnavailable, errors.New("server listener is already configured"))
	}
	server.listener = listener
	server.address = address
	server.allowedHost = host
	server.origin = origin
	return nil
}

func loopbackAddress(listener net.Listener) (address, host, origin string, err error) {
	tcpAddress, ok := listener.Addr().(*net.TCPAddr)
	if !ok || tcpAddress == nil || tcpAddress.IP == nil || tcpAddress.Port <= 0 || !tcpAddress.IP.IsLoopback() {
		return "", "", "", errors.New("service listener must be a bound loopback TCP address")
	}
	ip := tcpAddress.IP
	if ipv4 := ip.To4(); ipv4 != nil {
		ip = ipv4
	}
	host = net.JoinHostPort(ip.String(), strconv.Itoa(tcpAddress.Port))
	return host, host, "http://" + host, nil
}

func (server *Server) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	setSecurityHeaders(response)

	if !server.validHost(request.Host) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.HTTPAPIHostInvalid)
		return
	}
	if !server.validOrigin(request) {
		server.writeAPIError(response, http.StatusForbidden, apperrors.HTTPAPIOriginInvalid)
		return
	}

	switch request.URL.Path {
	case BootstrapPathName, "/", "/index.html":
		if !isReadMethod(request.Method) {
			server.writeMethodError(response, http.MethodGet)
			return
		}
		server.serveAsset(response, request, "index.html")
	case "/assets/app.js":
		if !isReadMethod(request.Method) {
			server.writeMethodError(response, http.MethodGet)
			return
		}
		server.serveAsset(response, request, "app.js")
	case "/assets/styles.css":
		if !isReadMethod(request.Method) {
			server.writeMethodError(response, http.MethodGet)
			return
		}
		server.serveAsset(response, request, "styles.css")
	case BootstrapPath:
		if request.Method != http.MethodPost {
			server.writeMethodError(response, http.MethodPost)
			return
		}
		server.exchangeBootstrap(response, request)
	case MetadataPath:
		if !server.authorize(response, request) {
			return
		}
		if !isReadMethod(request.Method) {
			if !server.validCSRF(request) {
				server.writeAPIError(response, http.StatusForbidden, apperrors.HTTPAPICSRFInvalid)
				return
			}
			server.writeMethodError(response, http.MethodGet)
			return
		}
		server.writeMetadata(response, request)
	default:
		if strings.HasPrefix(request.URL.Path, "/api/") {
			if !server.authorize(response, request) {
				return
			}
			if !isReadMethod(request.Method) && !server.validCSRF(request) {
				server.writeAPIError(response, http.StatusForbidden, apperrors.HTTPAPICSRFInvalid)
				return
			}
		}
		server.writeAPIError(response, http.StatusNotFound, apperrors.HTTPAPIRouteNotFound)
	}
}

func (server *Server) exchangeBootstrap(response http.ResponseWriter, request *http.Request) {
	contentType, _, contentTypeErr := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if contentTypeErr != nil || contentType != "application/json" {
		server.writeAPIError(response, http.StatusUnauthorized, apperrors.HTTPAPIBootstrapInvalid)
		return
	}
	if request.ContentLength > maxBootstrapBodySize {
		server.writeAPIError(response, http.StatusUnauthorized, apperrors.HTTPAPIBootstrapInvalid)
		return
	}

	decoder := json.NewDecoder(io.LimitReader(request.Body, maxBootstrapBodySize))
	decoder.DisallowUnknownFields()
	var input BootstrapRequest
	if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.BootstrapToken) == "" {
		server.writeAPIError(response, http.StatusUnauthorized, apperrors.HTTPAPIBootstrapInvalid)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		server.writeAPIError(response, http.StatusUnauthorized, apperrors.HTTPAPIBootstrapInvalid)
		return
	}

	digest := sha256.Sum256([]byte(input.BootstrapToken))
	now := server.clock.Now().UTC()
	server.mu.Lock()
	if server.bootstrapAvailable && !now.Before(server.bootstrapExpiresAt) {
		server.bootstrapAvailable = false
		server.bootstrapToken = nil
	}
	valid := server.bootstrapAvailable && subtle.ConstantTimeCompare(digest[:], server.bootstrapDigest[:]) == 1
	if valid {
		server.bootstrapAvailable = false
		server.bootstrapToken = nil
	}
	server.mu.Unlock()
	if !valid {
		server.writeAPIError(response, http.StatusUnauthorized, apperrors.HTTPAPIBootstrapInvalid)
		return
	}

	sessionBytes, err := server.randomBytes(randomTokenSize)
	if err != nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	csrfBytes, err := server.randomBytes(randomTokenSize)
	if err != nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	sessionID := base64.RawURLEncoding.EncodeToString(sessionBytes)
	csrfToken := base64.RawURLEncoding.EncodeToString(csrfBytes)
	expiresAt := now.Add(server.sessionTTL)
	sessionDigest := sha256.Sum256([]byte(sessionID))
	server.mu.Lock()
	server.sessions[sessionDigest] = session{csrfDigest: sha256.Sum256([]byte(csrfToken)), expiresAt: expiresAt}
	server.mu.Unlock()

	http.SetCookie(response, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge(server.sessionTTL),
	})
	writeJSON(response, http.StatusOK, BootstrapResponse{CSRFToken: csrfToken})
}

func (server *Server) authorize(response http.ResponseWriter, request *http.Request) bool {
	cookies := request.Cookies()
	matching := make([]*http.Cookie, 0, 1)
	for _, cookie := range cookies {
		if cookie.Name == SessionCookieName {
			matching = append(matching, cookie)
		}
	}
	if len(matching) != 1 || matching[0].Value == "" {
		server.writeAPIError(response, http.StatusUnauthorized, apperrors.HTTPAPISessionInvalid)
		return false
	}

	digest := sha256.Sum256([]byte(matching[0].Value))
	now := server.clock.Now().UTC()
	server.mu.Lock()
	current, ok := server.sessions[digest]
	if ok && !now.Before(current.expiresAt) {
		delete(server.sessions, digest)
		ok = false
	}
	server.mu.Unlock()
	if !ok {
		if current.expiresAt.IsZero() {
			server.writeAPIError(response, http.StatusUnauthorized, apperrors.HTTPAPISessionInvalid)
		} else {
			server.writeAPIError(response, http.StatusUnauthorized, apperrors.HTTPAPISessionExpired)
		}
		return false
	}
	return true
}

func (server *Server) validCSRF(request *http.Request) bool {
	values := request.Header.Values(CSRFHeaderName)
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return false
	}
	digest := sha256.Sum256([]byte(values[0]))

	cookies := request.Cookies()
	if len(cookies) == 0 {
		return false
	}
	var sessionCookie *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name != SessionCookieName {
			continue
		}
		if sessionCookie != nil {
			return false
		}
		sessionCookie = cookie
	}
	if sessionCookie == nil {
		return false
	}
	sessionDigest := sha256.Sum256([]byte(sessionCookie.Value))
	server.mu.Lock()
	current, ok := server.sessions[sessionDigest]
	server.mu.Unlock()
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare(digest[:], current.csrfDigest[:]) == 1
}

func (server *Server) writeMetadata(response http.ResponseWriter, request *http.Request) {
	writeJSON(response, http.StatusOK, MetadataResponse{
		APIVersion:      APIVersion,
		ContractVersion: ContractVersion,
		Product:         server.product,
	})
}

func (server *Server) writeAPIError(response http.ResponseWriter, status int, code string) {
	context := diagnostics.Context{
		Operation:  httpDiagnosticOperation(code),
		State:      httpDiagnosticState(code),
		HTTPStatus: status,
	}
	server.recordDiagnostic(context.Operation, httpDiagnosticSeverity(code), code, context)
	writeAPIError(response, status, code)
}

func (server *Server) writeMethodError(response http.ResponseWriter, allowed string) {
	response.Header().Set("Allow", allowed)
	server.writeAPIError(response, http.StatusMethodNotAllowed, apperrors.HTTPAPIMethodNotAllowed)
}

func (server *Server) recordDiagnostic(operation string, severity diagnostics.Severity, code string, context diagnostics.Context) {
	if server == nil || server.diagnostics == nil {
		return
	}
	if context.Operation == "" {
		context.Operation = operation
	}
	recordDiagnostic(server.diagnostics, server.clock.Now(), diagnostics.ComponentHTTPAPI, operation, severity, code, context)
}

func (server *Server) serveAsset(response http.ResponseWriter, request *http.Request, name string) {
	asset, err := embeddedAssets.ReadFile("assets/" + name)
	if err != nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	contentType := map[string]string{
		"index.html": "text/html; charset=utf-8",
		"app.js":     "text/javascript; charset=utf-8",
		"styles.css": "text/css; charset=utf-8",
	}[name]
	response.Header().Set("Content-Type", contentType)
	response.Header().Set("Content-Length", strconv.Itoa(len(asset)))
	if request.Method != http.MethodHead {
		_, _ = response.Write(asset)
	}
}

func (server *Server) validHost(host string) bool {
	server.mu.Lock()
	want := server.allowedHost
	server.mu.Unlock()
	return want != "" && strings.EqualFold(host, want)
}

func (server *Server) validOrigin(request *http.Request) bool {
	values := request.Header.Values("Origin")
	if len(values) > 1 {
		return false
	}
	if len(values) == 0 || strings.TrimSpace(values[0]) == "" {
		return isReadMethod(request.Method)
	}
	server.mu.Lock()
	want := server.origin
	server.mu.Unlock()
	return want != "" && values[0] == want
}

func (server *Server) randomBytes(size int) ([]byte, error) {
	server.randomMu.Lock()
	defer server.randomMu.Unlock()
	return readRandom(server.random, size)
}

func (server *Server) invalidateLocked() {
	server.sessions = make(map[[sha256.Size]byte]session)
	server.bootstrapAvailable = false
	server.bootstrapToken = nil
}

func setSecurityHeaders(response http.ResponseWriter) {
	response.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("X-Frame-Options", "DENY")
	response.Header().Set("Referrer-Policy", "no-referrer")
	response.Header().Set("Cache-Control", "no-store")
}

func writeAPIError(response http.ResponseWriter, status int, code string) {
	if !apperrors.IsRegistered(code) {
		code = apperrors.HTTPAPIServiceUnavailable
	}
	writeJSON(response, status, map[string]string{
		"code":    code,
		"message": safeMessage(code),
	})
}

func recordDiagnostic(sink diagnostics.Sink, at time.Time, component, operation string, severity diagnostics.Severity, code string, context diagnostics.Context) {
	if sink == nil {
		return
	}
	if context.Operation == "" {
		context.Operation = operation
	}
	event, err := diagnostics.NewEvent(at, severity, component, code, context)
	if err != nil {
		return
	}
	_ = sink.Record(event)
}

func writeMethodError(response http.ResponseWriter, allowed string) {
	response.Header().Set("Allow", allowed)
	writeAPIError(response, http.StatusMethodNotAllowed, apperrors.HTTPAPIMethodNotAllowed)
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func safeMessage(code string) string {
	switch code {
	case apperrors.HTTPAPIHostInvalid, apperrors.HTTPAPIOriginInvalid:
		return "The local dashboard request is not authorized."
	case apperrors.HTTPAPIBootstrapInvalid:
		return "This dashboard link is no longer valid. Relaunch CodexFolio."
	case apperrors.HTTPAPISessionInvalid, apperrors.HTTPAPISessionExpired:
		return "This dashboard session is no longer valid. Relaunch CodexFolio."
	case apperrors.HTTPAPICSRFInvalid:
		return "This dashboard action is not authorized. Relaunch CodexFolio."
	case apperrors.HTTPAPIMethodNotAllowed:
		return "This dashboard request method is not available."
	case apperrors.HTTPAPIRouteNotFound:
		return "The requested dashboard resource was not found."
	case apperrors.HTTPAPIServiceUnavailable:
		return "The local dashboard is temporarily unavailable. Relaunch CodexFolio."
	default:
		return "The local dashboard could not complete the request."
	}
}

func httpDiagnosticOperation(code string) string {
	switch code {
	case apperrors.HTTPAPIBootstrapInvalid:
		return diagnostics.OperationHTTPBootstrap
	case apperrors.HTTPAPISessionInvalid, apperrors.HTTPAPISessionExpired:
		return diagnostics.OperationHTTPSession
	case apperrors.HTTPAPIHostInvalid, apperrors.HTTPAPIOriginInvalid, apperrors.HTTPAPICSRFInvalid:
		return diagnostics.OperationHTTPAuthorization
	case apperrors.HTTPAPIServiceUnavailable:
		return diagnostics.OperationHTTPListen
	default:
		return diagnostics.OperationHTTPAuthorization
	}
}

func httpDiagnosticState(code string) string {
	switch code {
	case apperrors.HTTPAPISessionExpired:
		return diagnostics.StateExpired
	case apperrors.HTTPAPISessionInvalid, apperrors.HTTPAPIBootstrapInvalid, apperrors.HTTPAPICSRFInvalid, apperrors.HTTPAPIHostInvalid, apperrors.HTTPAPIOriginInvalid, apperrors.HTTPAPIMethodNotAllowed, apperrors.HTTPAPIRouteNotFound:
		return diagnostics.StateRejected
	case apperrors.HTTPAPIServiceUnavailable:
		return diagnostics.StateUnavailable
	default:
		return diagnostics.StateFailed
	}
}

func httpDiagnosticSeverity(code string) diagnostics.Severity {
	if code == apperrors.HTTPAPIServiceUnavailable {
		return diagnostics.SeverityError
	}
	if code == apperrors.HTTPAPIRouteNotFound || code == apperrors.HTTPAPIMethodNotAllowed {
		return diagnostics.SeverityInfo
	}
	return diagnostics.SeverityWarning
}

func isReadMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

func maxAge(duration time.Duration) int {
	seconds := int(duration / time.Second)
	if duration%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		return 1
	}
	return seconds
}

func readRandom(reader io.Reader, size int) ([]byte, error) {
	value := make([]byte, size)
	if _, err := io.ReadFull(reader, value); err != nil {
		return nil, err
	}
	return value, nil
}
