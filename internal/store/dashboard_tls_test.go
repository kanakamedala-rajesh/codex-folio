package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"venkatasudha.com/codex-folio/internal/vault"
)

func TestDashboardTLSIdentityReusesCertificateAndProtectsKey(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	secureVault, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x71}, 32), "dashboard-tls-test")
	if err != nil {
		t.Fatal(err)
	}
	state, err := OpenWithVault(path, secureVault)
	if err != nil {
		t.Fatal(err)
	}
	first, err := state.LoadOrCreateDashboardTLSIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Certificate.Certificate) != 2 {
		t.Fatalf("certificate chain length = %d, want 2", len(first.Certificate.Certificate))
	}
	leaf, err := x509.ParseCertificate(first.Certificate.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(first.Certificate.Certificate[1])
	if err != nil {
		t.Fatal(err)
	}
	if !root.IsCA || !root.MaxPathLenZero {
		t.Fatal("root is not a path-length-zero CA")
	}
	if err := leaf.VerifyHostname("127.0.0.1"); err != nil {
		t.Fatalf("leaf IP SAN: %v", err)
	}
	if err := leaf.CheckSignatureFrom(root); err != nil {
		t.Fatalf("leaf chain: %v", err)
	}
	rootHash := sha256.Sum256(root.Raw)
	leafHash := sha256.Sum256(leaf.Raw)
	if first.RootFingerprintSHA256 != hex.EncodeToString(rootHash[:]) || first.LeafFingerprintSHA256 != hex.EncodeToString(leafHash[:]) {
		t.Fatal("fingerprints do not match certificate DER")
	}
	var storedRoot, storedLeaf, ciphertext []byte
	if err := state.db.QueryRowContext(ctx, `SELECT root_certificate_pem, server_certificate_pem, server_key_ciphertext FROM dashboard_tls_identity`).Scan(&storedRoot, &storedLeaf, &ciphertext); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(storedRoot, first.RootCertificatePEM) || len(storedLeaf) == 0 || bytes.Contains(ciphertext, []byte("PRIVATE KEY")) {
		t.Fatal("unexpected plaintext or mismatched public certificate in SQLite")
	}
	plaintext, err := secureVault.Decrypt(ctx, ciphertext, dashboardTLSKeyAAD(dashboardTLSIdentityID))
	if err != nil {
		t.Fatal(err)
	}
	if len(plaintext) == 0 || bytes.Contains(ciphertext, plaintext) {
		t.Fatal("server private key not encrypted at rest")
	}
	clear(plaintext)
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = OpenWithVault(path, secureVault)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.Close() }()
	second, err := state.LoadOrCreateDashboardTLSIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Certificate.Certificate[0], second.Certificate.Certificate[0]) || first.RootFingerprintSHA256 != second.RootFingerprintSHA256 {
		t.Fatal("dashboard TLS identity changed across database reopen")
	}
}

func TestDashboardTLSIdentityRequiresVaultAndResetsWithDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.LoadOrCreateDashboardTLSIdentity(ctx); err == nil {
		t.Fatal("identity created without vault")
	}
	var count int
	if err := state.db.QueryRowContext(ctx, `SELECT count(*) FROM dashboard_tls_identity`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("locked creation left %d rows: %v", count, err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	secureVault, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x72}, 32), "dashboard-reset-test")
	if err != nil {
		t.Fatal(err)
	}
	state, err = OpenWithVault(path, secureVault)
	if err != nil {
		t.Fatal(err)
	}
	first, err := state.LoadOrCreateDashboardTLSIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	state, err = OpenWithVault(path, secureVault)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.Close() }()
	second, err := state.LoadOrCreateDashboardTLSIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Certificate.Certificate[0], second.Certificate.Certificate[0]) {
		t.Fatal("fresh database reused the old TLS identity")
	}
}

func TestDashboardTLSIdentityReprotectsWithVaultMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), DatabaseFileName)
	source, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x73}, 32), "dashboard-source")
	if err != nil {
		t.Fatal(err)
	}
	destination, err := vault.NewInMemoryVault(bytes.Repeat([]byte{0x74}, 32), "dashboard-destination")
	if err != nil {
		t.Fatal(err)
	}
	state, err := OpenWithVault(path, source)
	if err != nil {
		t.Fatal(err)
	}
	first, err := state.LoadOrCreateDashboardTLSIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.ReprotectVaultState(ctx, destination); err != nil {
		t.Fatal(err)
	}
	if err := state.VerifyProtectedState(ctx); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = OpenWithVault(path, destination)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.Close() }()
	second, err := state.LoadOrCreateDashboardTLSIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Certificate.Certificate[0], second.Certificate.Certificate[0]) {
		t.Fatal("vault migration changed dashboard TLS identity")
	}
}

func TestEphemeralDashboardTLSCertificateIsFresh(t *testing.T) {
	first, err := NewEphemeralDashboardTLSCertificate()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewEphemeralDashboardTLSCertificate()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Certificate[0], second.Certificate[0]) {
		t.Fatal("ephemeral certificate reused across generations")
	}
}
