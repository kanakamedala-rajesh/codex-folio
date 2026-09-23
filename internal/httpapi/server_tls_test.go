package httpapi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testLoopbackTLSCertificate(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, hex.EncodeToString(fingerprint[:])
}

func startTLSTestServer(t *testing.T, certificate tls.Certificate, port int) (*Server, <-chan error) {
	t.Helper()
	server, err := NewServer(Options{TLSCertificate: &certificate, CommandToken: "test-command"})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := server.ListenPort(port)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close(); <-finished })
	return server, finished
}

func TestPinnedCommandClientAuthenticatesHTTPSAndRejectsWrongIdentity(t *testing.T) {
	certificate, fingerprint := testLoopbackTLSCertificate(t)
	server, _ := startTLSTestServer(t, certificate, 0)
	if !strings.HasPrefix(server.Origin(), "https://127.0.0.1:") {
		t.Fatalf("TLS origin = %q", server.Origin())
	}
	client, err := NewPinnedCommandClient(server.Origin(), "test-command", fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ServiceHealth(context.Background()); err != nil {
		t.Fatalf("pinned ServiceHealth() error = %v", err)
	}
	wrong, err := NewPinnedCommandClient(server.Origin(), "test-command", strings.Repeat("0", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.ServiceHealth(context.Background()); err == nil {
		t.Fatal("wrong certificate fingerprint authenticated")
	}
	if _, err := NewPinnedCommandClient(server.Origin(), "test-command", ""); err == nil {
		t.Fatal("missing certificate fingerprint accepted")
	}
	if _, err := NewCommandClient(server.Origin(), "test-command", nil).ServiceHealth(context.Background()); err == nil {
		t.Fatal("unpinned HTTPS command client authenticated")
	}
}

func TestHTTPSLockedServiceRequiresCLIUnlockBeforeBrowser(t *testing.T) {
	certificate, fingerprint := testLoopbackTLSCertificate(t)
	server, err := NewServer(Options{TLSCertificate: &certificate, CommandToken: "test-command", StartLocked: true})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	defer func() { _ = server.Close(); <-finished }()
	client, err := NewPinnedCommandClient(server.Origin(), "test-command", fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if health, err := client.ServiceHealth(context.Background()); err != nil || health.ServiceState != ServiceStateLocked {
		t.Fatalf("pinned locked health = %#v, %v", health, err)
	}
	request, err := http.NewRequest(http.MethodGet, server.Origin()+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.httpDoer().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusLocked {
		t.Fatalf("locked browser page = %d, want 423", response.StatusCode)
	}
}

func TestHTTPSOriginAndCertificateRotation(t *testing.T) {
	first, firstFingerprint := testLoopbackTLSCertificate(t)
	server, _ := startTLSTestServer(t, first, 0)
	client, err := NewPinnedCommandClient(server.Origin(), "test-command", firstFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, server.Origin()+CommandDashboardPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", "http://"+server.Address())
	request.Header.Set(CommandTokenHeader, "test-command")
	response, err := client.httpDoer().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("HTTP Origin on HTTPS listener = %d, want 403", response.StatusCode)
	}
	second, secondFingerprint := testLoopbackTLSCertificate(t)
	if err := server.SetTLSCertificate(second); err != nil {
		t.Fatal(err)
	}
	oldPin, err := NewPinnedCommandClient(server.Origin(), "test-command", firstFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldPin.ServiceHealth(context.Background()); err == nil {
		t.Fatal("old fingerprint authenticated after TLS rotation")
	}
	rotated, err := NewPinnedCommandClient(server.Origin(), "test-command", secondFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rotated.ServiceHealth(context.Background()); err != nil {
		t.Fatalf("rotated TLS identity rejected: %v", err)
	}
}

func TestHTTPCommandClientCompatibility(t *testing.T) {
	server, _, _ := startTestServer(t, Options{CommandToken: "test-command"})
	if _, err := NewCommandClient(server.Origin(), "test-command", nil).ServiceHealth(context.Background()); err != nil {
		t.Fatalf("HTTP fixture command client error = %v", err)
	}
}

func TestPinnedCommandClientRejectsSamePortReplacement(t *testing.T) {
	first, fingerprint := testLoopbackTLSCertificate(t)
	server, _ := startTLSTestServer(t, first, 0)
	origin := server.Origin()
	address := server.Address()
	client, err := NewPinnedCommandClient(origin, "test-command", fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ServiceHealth(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	_, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := testLoopbackTLSCertificate(t)
	_, _ = startTLSTestServer(t, second, port)
	if _, err := client.ServiceHealth(context.Background()); err == nil {
		t.Fatal("same-port replacement authenticated with old certificate pin")
	}
}
