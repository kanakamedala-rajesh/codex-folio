package main

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"venkatasudha.com/codex-folio/internal/platform"
)

const dashboardRootCertificateFile = "dashboard-root-ca.pem"

func runServiceCertificate(paths platform.Paths, jsonOutput bool, stdout, stderr io.Writer) int {
	path := filepath.Join(paths.Root, dashboardRootCertificateFile)
	info, err := os.Lstat(path)
	if err != nil {
		return writeServiceError(stderr, fmt.Errorf("dashboard certificate is unavailable; start and unlock the service first: %w", err))
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return writeServiceError(stderr, errors.New("dashboard certificate file is not a regular file"))
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return writeServiceError(stderr, err)
	}
	block, rest := pem.Decode(contents)
	if block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
		return writeServiceError(stderr, errors.New("dashboard certificate file is invalid"))
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !certificate.IsCA {
		return writeServiceError(stderr, errors.New("dashboard certificate authority is invalid"))
	}
	fingerprint := sha256.Sum256(certificate.Raw)
	result := struct {
		Path              string `json:"path"`
		SHA256Fingerprint string `json:"sha256_fingerprint"`
	}{Path: path, SHA256Fingerprint: hex.EncodeToString(fingerprint[:])}
	if jsonOutput {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			return writeServiceError(stderr, err)
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintf(stdout, "Dashboard certificate authority: %s\nSHA-256 fingerprint: %s\nImport this certificate into the browser's trusted certificate authorities before opening the HTTPS dashboard.\n", result.Path, result.SHA256Fingerprint); err != nil {
		return writeServiceError(stderr, err)
	}
	return exitSuccess
}
