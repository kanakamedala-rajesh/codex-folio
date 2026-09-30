package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
)

func TestServiceCertificatePrintsOnlyPublicRootAndFingerprint(t *testing.T) {
	root := t.TempDir()
	certificate, err := store.NewEphemeralDashboardTLSCertificate()
	if err != nil {
		t.Fatal(err)
	}
	publicRoot := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[1]})
	if err := writeDashboardRootCertificate(root, publicRoot); err != nil {
		t.Fatal(err)
	}
	if err := writeDashboardRootCertificate(root, publicRoot); err != nil {
		t.Fatalf("same certificate should be idempotent: %v", err)
	}
	var output, errors bytes.Buffer
	if code := runServiceCertificate(platform.Paths{Root: root}, true, &output, &errors); code != exitSuccess {
		t.Fatalf("certificate command = %d: %s", code, errors.String())
	}
	var result struct {
		Path              string `json:"path"`
		SHA256Fingerprint string `json:"sha256_fingerprint"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256(certificate.Certificate[1])
	if result.Path != filepath.Join(root, dashboardRootCertificateFile) || result.SHA256Fingerprint != hex.EncodeToString(fingerprint[:]) {
		t.Fatalf("certificate metadata mismatch: %#v", result)
	}
	if strings.Contains(output.String(), "PRIVATE KEY") {
		t.Fatal("certificate command exposed private key")
	}
	if err := writeDashboardRootCertificate(root, []byte("different")); err != nil {
		t.Fatalf("identity reset did not replace the old public root: %v", err)
	}
	if updated, err := os.ReadFile(result.Path); err != nil || string(updated) != "different" {
		t.Fatalf("replacement root = %q, %v", updated, err)
	}
	if err := os.Remove(result.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(result.Path, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	errors.Reset()
	if code := runServiceCertificate(platform.Paths{Root: root}, false, &output, &errors); code == exitSuccess {
		t.Fatal("invalid certificate file was accepted")
	}
}
