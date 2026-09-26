//go:build linux

package platform

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

func TestWSLDPAPIProviderRoundTripAcrossInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "wsl.vault")
	random := bytes.NewReader(bytes.Repeat([]byte{0x2a}, 48))
	var protected []byte
	runner := func(_ context.Context, helper string, request []byte) ([]byte, error) {
		if helper != "/configured/helper.exe" {
			t.Fatalf("helper = %q", helper)
		}
		decoded, err := decodeWSLHelperRequest(bytes.NewReader(request))
		if err != nil {
			return nil, err
		}
		if decoded.operation == wslOperationProtect {
			protected = make([]byte, len(decoded.payload))
			for index := range decoded.payload {
				protected[index] = decoded.payload[index] ^ 0xff
			}
			return bytes.Clone(protected), nil
		}
		if !bytes.Equal(decoded.payload, protected) {
			return nil, errors.New("wrong protected blob")
		}
		plaintext := make([]byte, len(decoded.payload))
		for index := range decoded.payload {
			plaintext[index] = decoded.payload[index] ^ 0xff
		}
		return plaintext, nil
	}
	first, err := NewWSLDPAPIKeyProviderWithOptions(path, WSLDPAPIOptions{AllowCreate: true, Random: random, HelperPath: "/configured/helper.exe", run: runner})
	if err != nil {
		t.Fatal(err)
	}
	firstMaterial, err := first.LoadOrCreate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer firstMaterial.Clear()
	second, err := NewWSLDPAPIKeyProviderWithOptions(path, WSLDPAPIOptions{AllowCreate: false, HelperPath: "/configured/helper.exe", run: runner})
	if err != nil {
		t.Fatal(err)
	}
	secondMaterial, err := second.LoadOrCreate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer secondMaterial.Clear()
	record, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(record, bytes.Repeat([]byte{0x2a}, 32)) {
		t.Fatal("plaintext envelope key was written to the WSL vault file")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("vault permissions = %v, %v", info, err)
	}
}

func TestWSLDPAPIProviderFailsClosedWithoutProtectedMaterial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "wsl.vault")
	provider, err := NewWSLDPAPIKeyProviderWithOptions(path, WSLDPAPIOptions{AllowCreate: false, HelperPath: "/helper.exe", run: func(context.Context, string, []byte) ([]byte, error) { t.Fatal("helper must not run"); return nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.LoadOrCreate(context.Background()); apperrors.Code(err) != apperrors.VaultUnavailable {
		t.Fatalf("missing material error = %v", err)
	}
}

func TestWSLDPAPIProviderWrongWindowsContextDoesNotReplaceMaterial(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state, "wsl.vault")
	if err := os.WriteFile(path, encodeWSLVaultRecord("0123456789abcdef0123456789abcdef", []byte("protected-for-another-user")), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	provider, err := NewWSLDPAPIKeyProviderWithOptions(path, WSLDPAPIOptions{AllowCreate: true, HelperPath: "/helper.exe", run: func(context.Context, string, []byte) ([]byte, error) { return nil, errWSLProtocol }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.LoadOrCreate(context.Background()); apperrors.Code(err) != apperrors.VaultKeyInvalid {
		t.Fatalf("wrong-context error = %v", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("wrong-context failure replaced protected material")
	}
}

func TestWSLHelperConfigurationAndEnvironmentAreNonSecretAndBounded(t *testing.T) {
	t.Setenv(WSLVaultHelperEnvironment, "relative.exe")
	if _, err := NewWSLDPAPIKeyProviderWithOptions(filepath.Join(t.TempDir(), "vault"), WSLDPAPIOptions{}); apperrors.Code(err) != apperrors.VaultUnavailable {
		t.Fatalf("relative helper error = %v", err)
	}
	t.Setenv("WSL_INTEROP", "/run/WSL/1_interop")
	t.Setenv("WSLENV", "SAFE/u")
	t.Setenv("PRIVATE_TOKEN", "must-not-pass")
	environment := strings.Join(wslInteropEnvironment(), "\n")
	if !strings.Contains(environment, "WSL_INTEROP=") || !strings.Contains(environment, "WSLENV=") || strings.Contains(environment, "PRIVATE_TOKEN") {
		t.Fatalf("helper environment = %q", environment)
	}
}

func TestWSL2KernelDetection(t *testing.T) {
	if !isWSL2KernelRelease("6.6.87.2-microsoft-standard-WSL2") {
		t.Fatal("WSL2 kernel was not detected")
	}
	if isWSL2KernelRelease("5.15.0-generic") || isWSL2KernelRelease("4.19.128-microsoft-standard") {
		t.Fatal("non-WSL2 kernel was detected")
	}
}

func TestWSLDPAPIProviderNativeHelperRoundTrip(t *testing.T) {
	if os.Getenv("CODEX_FOLIO_WSL_VAULT_NATIVE_TEST") != "1" {
		t.Skip("set CODEX_FOLIO_WSL_VAULT_NATIVE_TEST=1 for the native WSL/Windows-user DPAPI probe")
	}
	if !IsWSL2() {
		t.Skip("native helper probe requires WSL2")
	}
	path := filepath.Join(t.TempDir(), "state", "wsl.vault")
	first, err := NewWSLDPAPIVaultWithOptions(path, WSLDPAPIOptions{AllowCreate: true})
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := first.Encrypt(context.Background(), []byte("native WSL DPAPI roundtrip"), []byte("issue-87"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewWSLDPAPIVaultWithOptions(path, WSLDPAPIOptions{AllowCreate: false})
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := second.Decrypt(context.Background(), ciphertext, []byte("issue-87"))
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != "native WSL DPAPI roundtrip" {
		t.Fatalf("plaintext = %q", plaintext)
	}
}

func TestWSLDPAPIAtomicPublicationPreservesWinnerAndIgnoresInterruptedTemporary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "wsl.vault")
	if err := os.Mkdir(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), ".wsl-vault-interrupted.tmp"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	winnerGeneration := strings.Repeat("a", 32)
	winnerKey := bytes.Repeat([]byte{0x77}, 32)
	runner := func(_ context.Context, _ string, request []byte) ([]byte, error) {
		decoded, err := decodeWSLHelperRequest(bytes.NewReader(request))
		if err != nil {
			return nil, err
		}
		if decoded.operation == wslOperationProtect {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("final record published before complete protection: %v", err)
			}
			// Another creator wins between our initial existence check and publication.
			if err := os.WriteFile(path, encodeWSLVaultRecord(winnerGeneration, winnerKey), 0o600); err != nil {
				return nil, err
			}
		}
		return bytes.Clone(decoded.payload), nil
	}
	provider, err := NewWSLDPAPIKeyProviderWithOptions(path, WSLDPAPIOptions{AllowCreate: true, HelperPath: "/helper.exe", run: runner})
	if err != nil {
		t.Fatal(err)
	}
	material, err := provider.LoadOrCreate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	material.Clear()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	generation, protected, err := decodeWSLVaultRecord(data)
	if err != nil || generation != winnerGeneration || !bytes.Equal(protected, winnerKey) {
		t.Fatalf("winning record changed: generation:%q err:%v", generation, err)
	}
}
