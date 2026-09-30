package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/store"
	"venkatasudha.com/codex-folio/internal/vault"
)

type trustBrowserClock struct{ unix atomic.Int64 }

func (clock *trustBrowserClock) Now() time.Time { return time.Unix(clock.unix.Load(), 0).UTC() }

// runTrustedBrowserRestart uses an actual persisted Chromium profile and the
// service-owned SQLite database. The listener is replaced between browser runs.
func runTrustedBrowserRestart(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	database := filepath.Join(root, "state.sqlite3")
	profile := filepath.Join(root, "browser")
	clock := &trustBrowserClock{}
	clock.unix.Store(time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC).Unix())
	secureVault, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x91}, 32), "browser-trust-https-test")
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.OpenWithVault(database, secureVault)
	if err != nil {
		t.Fatalf("open first trust store: %v", err)
	}
	identity, err := state.LoadOrCreateDashboardTLSIdentity(context.Background())
	if err != nil {
		t.Fatalf("create dashboard HTTPS identity: %v", err)
	}
	start := func() *httpapi.Server {
		t.Helper()
		server, err := httpapi.NewServer(httpapi.Options{BrowserTrust: state, Clock: clock, SessionTTL: time.Minute, TLSCertificate: &identity.Certificate})
		if err != nil {
			t.Fatalf("create first server: %v", err)
		}
		listener, err := server.Listen()
		if err != nil {
			t.Fatalf("bind first listener: %v", err)
		}
		go func() { _ = server.Serve(listener) }()
		return server
	}
	runBrowser := func(phase, link string) {
		t.Helper()
		command := exec.Command("node", filepath.Join("..", "..", "web", "scripts", "trust-browser-restart.mjs"), phase, link, profile)
		command.Env = append(os.Environ(), "CODEX_FOLIO_TEST_SPKI="+identity.LeafSPKISHA256Base64)
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			t.Fatalf("trusted browser %s journey: %v", phase, err)
		}
	}
	first := start()
	port := first.Address()
	trustedOrigin := first.Origin()
	runBrowser("grant", first.BootstrapURL())
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	rogueCertificate, err := store.NewEphemeralDashboardTLSCertificate()
	if err != nil {
		t.Fatal(err)
	}
	rogueListener, err := net.Listen("tcp4", port)
	if err != nil {
		t.Fatalf("bind rogue same-origin listener: %v", err)
	}
	rogue := &http.Server{Handler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`<script>document.body.textContent = localStorage.getItem("codex-folio.browser-trust.v1")</script>`))
	})}
	go func() {
		_ = rogue.Serve(tls.NewListener(rogueListener, &tls.Config{Certificates: []tls.Certificate{rogueCertificate}, MinVersion: tls.VersionTLS12}))
	}()
	runBrowser("attack", trustedOrigin)
	if err := rogue.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	clock.unix.Add(int64((2 * time.Minute).Seconds()))
	state, err = store.OpenWithVault(database, secureVault)
	if err != nil {
		t.Fatalf("reopen trust store: %v", err)
	}
	defer state.Close()
	identityAfterRestart, err := state.LoadOrCreateDashboardTLSIdentity(context.Background())
	if err != nil {
		t.Fatalf("reopen dashboard HTTPS identity: %v", err)
	}
	if identityAfterRestart.LeafFingerprintSHA256 != identity.LeafFingerprintSHA256 {
		t.Fatal("dashboard HTTPS identity changed across restart")
	}
	second, err := httpapi.NewServer(httpapi.Options{BrowserTrust: state, Clock: clock, SessionTTL: time.Minute, TLSCertificate: &identityAfterRestart.Certificate})
	if err != nil {
		t.Fatalf("create restart server: %v", err)
	}
	listener, err := second.ListenPort(mustPort(t, port))
	if err != nil {
		t.Fatalf("restart listener on %s: %v", port, err)
	}
	go func() { _ = second.Serve(listener) }()
	defer second.Close()
	runBrowser("reopen", second.Origin())
}

func mustPort(t *testing.T, address string) int {
	t.Helper()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return number
}
