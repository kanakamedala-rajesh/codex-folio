package store

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

const dashboardTLSIdentityID = "dashboard"

// DashboardTLSIdentity is the stable, installation-local HTTPS identity. Only
// the public root may be exported for browser trust enrollment; the root key is
// discarded at creation and the server key is vault-protected at rest.
type DashboardTLSIdentity struct {
	Certificate           tls.Certificate
	RootCertificatePEM    []byte
	RootFingerprintSHA256 string
	LeafFingerprintSHA256 string
	LeafSPKISHA256Base64  string
}

// LoadOrCreateDashboardTLSIdentity requires an unlocked vault and returns the
// same identity across service restarts. A damaged stored identity fails closed.
func (store *Store) LoadOrCreateDashboardTLSIdentity(ctx context.Context) (DashboardTLSIdentity, error) {
	if store == nil || store.db == nil {
		return DashboardTLSIdentity{}, coded(apperrors.StoreOpenFailed, ErrDatabaseOpen)
	}
	ctx = contextOrBackground(ctx)
	store.operationMu.Lock()
	defer store.operationMu.Unlock()
	secureVault, err := store.requireVaultLocked()
	if err != nil {
		return DashboardTLSIdentity{}, err
	}
	var rootPEM, leafPEM, encryptedKey []byte
	err = store.db.QueryRowContext(ctx, `SELECT root_certificate_pem, server_certificate_pem, server_key_ciphertext FROM dashboard_tls_identity WHERE identity_id = ?`, dashboardTLSIdentityID).Scan(&rootPEM, &leafPEM, &encryptedKey)
	if errors.Is(err, sql.ErrNoRows) {
		var keyDER []byte
		rootPEM, leafPEM, keyDER, err = createDashboardTLSIdentity()
		if err != nil {
			return DashboardTLSIdentity{}, err
		}
		defer clear(keyDER)
		encryptedKey, err = encryptField(ctx, secureVault, keyDER, dashboardTLSKeyAAD(dashboardTLSIdentityID))
		if err != nil {
			return DashboardTLSIdentity{}, err
		}
		tx, txErr := store.db.BeginTx(ctx, nil)
		if txErr != nil {
			return DashboardTLSIdentity{}, coded(apperrors.StoreWriteFailed, ErrSensitiveWrite)
		}
		_, txErr = tx.ExecContext(ctx, `INSERT INTO dashboard_tls_identity (identity_id, root_certificate_pem, server_certificate_pem, server_key_ciphertext) VALUES (?, ?, ?, ?)`, dashboardTLSIdentityID, rootPEM, leafPEM, encryptedKey)
		if txErr != nil {
			_ = tx.Rollback()
			return DashboardTLSIdentity{}, coded(apperrors.StoreWriteFailed, ErrSensitiveWrite)
		}
		if txErr = tx.Commit(); txErr != nil {
			return DashboardTLSIdentity{}, coded(apperrors.StoreWriteFailed, ErrSensitiveWrite)
		}
	} else if err != nil {
		return DashboardTLSIdentity{}, coded(apperrors.StoreReadFailed, ErrSensitiveRead)
	}
	keyDER, err := secureVault.Decrypt(ctx, encryptedKey, dashboardTLSKeyAAD(dashboardTLSIdentityID))
	if err != nil {
		return DashboardTLSIdentity{}, normalizeVaultMigrationError(err)
	}
	defer clear(keyDER)
	return parseDashboardTLSIdentity(rootPEM, leafPEM, keyDER)
}

func dashboardTLSKeyAAD(id string) []byte {
	return []byte("codex-folio/dashboard-tls/" + id + "/server-key")
}

// NewEphemeralDashboardTLSCertificate supplies a process-local certificate
// until a locked vault can load its persistent identity. It is never stored.
func NewEphemeralDashboardTLSCertificate() (tls.Certificate, error) {
	rootPEM, leafPEM, keyDER, err := createDashboardTLSIdentity()
	if err != nil {
		return tls.Certificate{}, err
	}
	defer clear(keyDER)
	identity, err := parseDashboardTLSIdentity(rootPEM, leafPEM, keyDER)
	if err != nil {
		return tls.Certificate{}, err
	}
	return identity.Certificate, nil
}

func createDashboardTLSIdentity() (rootPEM, leafPEM, keyDER []byte, err error) {
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, coded(apperrors.StoreWriteFailed, ErrSensitiveWrite)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, coded(apperrors.StoreWriteFailed, ErrSensitiveWrite)
	}
	now := time.Now().UTC()
	rootSerial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, coded(apperrors.StoreWriteFailed, ErrSensitiveWrite)
	}
	root := &x509.Certificate{
		SerialNumber: rootSerial, Subject: pkix.Name{CommonName: "CodexFolio local dashboard root"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true, IsCA: true, MaxPathLenZero: true,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &rootKey.PublicKey, rootKey)
	if err != nil {
		return nil, nil, nil, coded(apperrors.StoreWriteFailed, ErrSensitiveWrite)
	}
	rootCert, err := x509.ParseCertificate(rootDER)
	if err != nil {
		return nil, nil, nil, coded(apperrors.StoreWriteFailed, ErrSensitiveWrite)
	}
	leafSerial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, coded(apperrors.StoreWriteFailed, ErrSensitiveWrite)
	}
	leaf := &x509.Certificate{
		SerialNumber: leafSerial, Subject: pkix.Name{CommonName: "CodexFolio local dashboard"},
		NotBefore: root.NotBefore, NotAfter: root.NotAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, rootCert, &leafKey.PublicKey, rootKey)
	if err != nil {
		return nil, nil, nil, coded(apperrors.StoreWriteFailed, ErrSensitiveWrite)
	}
	keyDER, err = x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return nil, nil, nil, coded(apperrors.StoreWriteFailed, ErrSensitiveWrite)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}), keyDER, nil
}

func parseDashboardTLSIdentity(rootPEM, leafPEM, keyDER []byte) (DashboardTLSIdentity, error) {
	rootBlock, rest := pem.Decode(rootPEM)
	if rootBlock == nil || len(rest) != 0 || rootBlock.Type != "CERTIFICATE" {
		return DashboardTLSIdentity{}, coded(apperrors.StoreReadFailed, ErrSensitiveRead)
	}
	root, err := x509.ParseCertificate(rootBlock.Bytes)
	if err != nil || !root.IsCA || !root.MaxPathLenZero {
		return DashboardTLSIdentity{}, coded(apperrors.StoreReadFailed, ErrSensitiveRead)
	}
	leafBlock, rest := pem.Decode(leafPEM)
	if leafBlock == nil || len(rest) != 0 || leafBlock.Type != "CERTIFICATE" {
		return DashboardTLSIdentity{}, coded(apperrors.StoreReadFailed, ErrSensitiveRead)
	}
	leaf, err := x509.ParseCertificate(leafBlock.Bytes)
	if err != nil {
		return DashboardTLSIdentity{}, coded(apperrors.StoreReadFailed, ErrSensitiveRead)
	}
	rootPool := x509.NewCertPool()
	rootPool.AddCert(root)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: rootPool, DNSName: "127.0.0.1", KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return DashboardTLSIdentity{}, coded(apperrors.StoreReadFailed, ErrSensitiveRead)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	defer clear(keyPEM)
	certificate, err := tls.X509KeyPair(append(append([]byte(nil), leafPEM...), rootPEM...), keyPEM)
	if err != nil {
		return DashboardTLSIdentity{}, coded(apperrors.StoreReadFailed, ErrSensitiveRead)
	}
	certificate.Leaf = leaf
	rootFingerprint := sha256.Sum256(root.Raw)
	leafFingerprint := sha256.Sum256(leaf.Raw)
	spkiHash := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	return DashboardTLSIdentity{
		Certificate: certificate, RootCertificatePEM: append([]byte(nil), rootPEM...),
		RootFingerprintSHA256: hex.EncodeToString(rootFingerprint[:]),
		LeafFingerprintSHA256: hex.EncodeToString(leafFingerprint[:]),
		LeafSPKISHA256Base64:  base64.StdEncoding.EncodeToString(spkiHash[:]),
	}, nil
}
