package vault

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

func TestMemoryVaultEncryptsAndRoundTripsWithFreshRandomness(t *testing.T) {
	key := bytes.Repeat([]byte{0x4a}, 32)
	vault, err := NewMemoryVault(MemoryVaultOptions{
		Key:        key,
		Generation: "generation-1",
		Random:     bytes.NewReader(append(bytes.Repeat([]byte{0x01}, 12), bytes.Repeat([]byte{0x02}, 12)...)),
	})
	if err != nil {
		t.Fatalf("NewMemoryVault() error = %v", err)
	}

	plaintext := []byte("/Users/example/project")
	associatedData := []byte("project-identities/project-1/canonical-path")
	first, err := vault.Encrypt(context.Background(), plaintext, associatedData)
	if err != nil {
		t.Fatalf("Encrypt() first error = %v", err)
	}
	second, err := vault.Encrypt(context.Background(), plaintext, associatedData)
	if err != nil {
		t.Fatalf("Encrypt() second error = %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("Encrypt() returned deterministic ciphertext for repeated plaintext")
	}
	if bytes.Contains(first, plaintext) {
		t.Fatal("ciphertext contains plaintext")
	}
	if bytes.Contains(first, key) {
		t.Fatal("ciphertext contains envelope key")
	}

	decoded, err := vault.Decrypt(context.Background(), first, associatedData)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if !bytes.Equal(decoded, plaintext) {
		t.Fatalf("Decrypt() = %q, want %q", decoded, plaintext)
	}

	metadata, err := ParseEnvelopeMetadata(first)
	if err != nil {
		t.Fatalf("ParseEnvelopeMetadata() error = %v", err)
	}
	if metadata.Version != EnvelopeVersion {
		t.Fatalf("envelope version = %d, want %d", metadata.Version, EnvelopeVersion)
	}
	if metadata.Algorithm != envelopeAlgorithmAES256GCM {
		t.Fatalf("envelope algorithm = %d, want %d", metadata.Algorithm, envelopeAlgorithmAES256GCM)
	}
	if metadata.Generation != "generation-1" {
		t.Fatalf("envelope generation = %q, want %q", metadata.Generation, "generation-1")
	}
}

func TestMemoryVaultRejectsTamperedWrongGenerationAndUnsupportedEnvelopes(t *testing.T) {
	key := bytes.Repeat([]byte{0x4a}, 32)
	first, err := NewMemoryVault(MemoryVaultOptions{Key: key, Generation: "generation-1"})
	if err != nil {
		t.Fatalf("NewMemoryVault() error = %v", err)
	}
	envelope, err := first.Encrypt(context.Background(), []byte("sentinel"), []byte("checkpoint/checkpoint-1/goal"))
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	tampered := bytes.Clone(envelope)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := first.Decrypt(context.Background(), tampered, []byte("checkpoint/checkpoint-1/goal")); err == nil {
		t.Fatal("Decrypt() accepted tampered ciphertext")
	} else if got := apperrors.Code(err); got != apperrors.VaultEnvelopeInvalid {
		t.Fatalf("tampered error code = %q, want %q", got, apperrors.VaultEnvelopeInvalid)
	} else if errors.Is(err, ErrAuthentication) == false {
		t.Fatalf("tampered error = %v, want authentication cause", err)
	}

	otherGeneration, err := NewMemoryVault(MemoryVaultOptions{Key: key, Generation: "generation-2"})
	if err != nil {
		t.Fatalf("NewMemoryVault() other generation error = %v", err)
	}
	if _, err := otherGeneration.Decrypt(context.Background(), envelope, []byte("checkpoint/checkpoint-1/goal")); err == nil {
		t.Fatal("Decrypt() accepted an envelope from another key generation")
	} else if got := apperrors.Code(err); got != apperrors.VaultKeyGenerationMismatch {
		t.Fatalf("generation mismatch error code = %q, want %q", got, apperrors.VaultKeyGenerationMismatch)
	}

	unsupported := bytes.Clone(envelope)
	unsupported[4]++
	if _, err := first.Decrypt(context.Background(), unsupported, []byte("checkpoint/checkpoint-1/goal")); err == nil {
		t.Fatal("Decrypt() accepted an unsupported envelope version")
	} else if got := apperrors.Code(err); got != apperrors.VaultEnvelopeUnsupported {
		t.Fatalf("unsupported error code = %q, want %q", got, apperrors.VaultEnvelopeUnsupported)
	}

	if _, err := first.Decrypt(context.Background(), envelope, []byte("checkpoint/checkpoint-1/risks")); err == nil {
		t.Fatal("Decrypt() accepted an envelope under a different associated-data purpose")
	} else if got := apperrors.Code(err); got != apperrors.VaultEnvelopeInvalid {
		t.Fatalf("associated-data error code = %q, want %q", got, apperrors.VaultEnvelopeInvalid)
	}
}

func TestMemoryVaultBlocksSensitiveOperationsWhenLockedOrUnavailable(t *testing.T) {
	vault, err := NewMemoryVault(MemoryVaultOptions{Key: bytes.Repeat([]byte{0x4a}, 32), Generation: "generation-1"})
	if err != nil {
		t.Fatalf("NewMemoryVault() error = %v", err)
	}

	vault.Lock()
	if _, err := vault.Encrypt(context.Background(), []byte("sentinel"), nil); err == nil {
		t.Fatal("Encrypt() succeeded while vault was locked")
	} else if got := apperrors.Code(err); got != apperrors.VaultLocked {
		t.Fatalf("locked error code = %q, want %q", got, apperrors.VaultLocked)
	} else if !errors.Is(err, ErrLocked) {
		t.Fatalf("locked error = %v, want ErrLocked cause", err)
	}

	vault.SetAvailable()
	envelope, err := vault.Encrypt(context.Background(), []byte("sentinel"), nil)
	if err != nil {
		t.Fatalf("Encrypt() after unlock error = %v", err)
	}

	sentinel := errors.New("injected vault failure")
	vault.SetUnavailable(sentinel)
	if _, err := vault.Decrypt(context.Background(), envelope, nil); err == nil {
		t.Fatal("Decrypt() succeeded while vault was unavailable")
	} else if got := apperrors.Code(err); got != apperrors.VaultUnavailable {
		t.Fatalf("unavailable error code = %q, want %q", got, apperrors.VaultUnavailable)
	} else if !errors.Is(err, sentinel) {
		t.Fatalf("unavailable error = %v, want injected cause", err)
	}
}

func TestKeyMaterialFormattingDoesNotExposeKeyBytes(t *testing.T) {
	key := bytes.Repeat([]byte{0x7e}, 32)
	material, err := NewKeyMaterial("generation-1", key)
	if err != nil {
		t.Fatalf("NewKeyMaterial() error = %v", err)
	}
	formatted := fmt.Sprintf("%v %#v", material, material)
	if strings.Contains(formatted, "7e") || strings.Contains(formatted, string(key)) {
		t.Fatalf("formatted key material contains key bytes: %q", formatted)
	}
}
