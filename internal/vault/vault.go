// Package vault owns the key-opaque contract and authenticated envelope
// format used by sensitive local state.
package vault

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"io"
	"sync"

	"venkatasudha.com/codex-folio/internal/apperrors"
)

const (
	// EnvelopeVersion identifies the authenticated field envelope format.
	EnvelopeVersion uint8 = 1

	envelopeAlgorithmAES256GCM uint8 = 1
	envelopeMagic                    = "CFVE"
	envelopeHeaderSize               = 9
	maxGenerationSize                = 128
	keySize                          = 32
)

var (
	ErrUnavailable         = errors.New("secure vault is unavailable")
	ErrLocked              = errors.New("secure vault is locked")
	ErrInvalidKey          = errors.New("vault key material is invalid")
	ErrInvalidEnvelope     = errors.New("vault envelope is invalid")
	ErrUnsupportedEnvelope = errors.New("vault envelope version or algorithm is unsupported")
	ErrGenerationMismatch  = errors.New("vault key generation does not match envelope")
	ErrEncryption          = errors.New("vault encryption failed")
	ErrAuthentication      = errors.New("vault envelope authentication failed")
)

// Vault is the key-opaque boundary used by storage adapters. Implementations
// own key loading, protection, and envelope operations; callers never receive
// the unwrapped envelope key.
type Vault interface {
	Encrypt(context.Context, []byte, []byte) ([]byte, error)
	Decrypt(context.Context, []byte, []byte) ([]byte, error)
}

// KeyMaterial is an internal adapter handoff for an unwrapped key. Its fields
// are deliberately private so storage and browser contracts cannot inspect or
// serialize key bytes. Platform adapters provide it to NewEnvelopeVault from
// their protected key store.
type KeyMaterial struct {
	generation string
	key        []byte
}

// String prevents accidental diagnostic output from exposing the unwrapped
// key when an adapter includes key material in ordinary formatting.
func (KeyMaterial) String() string {
	return "vault key material (opaque)"
}

// GoString keeps %#v formatting opaque for debug tooling as well.
func (KeyMaterial) GoString() string {
	return "vault.KeyMaterial{opaque}"
}

// Clear releases the in-memory key bytes held by material. Platform adapters
// use this when a per-session vault is locked; it does not expose the bytes or
// make them serializable.
func (material *KeyMaterial) Clear() {
	if material == nil {
		return
	}
	clear(material.key)
	material.key = nil
	material.generation = ""
}

// NewKeyMaterial validates and copies a key returned by a platform vault
// adapter. The returned value has no accessor for the key bytes.
func NewKeyMaterial(generation string, key []byte) (KeyMaterial, error) {
	if generation == "" || len(generation) > maxGenerationSize || len(key) != keySize {
		return KeyMaterial{}, apperrors.New(apperrors.VaultKeyInvalid, ErrInvalidKey)
	}
	return KeyMaterial{generation: generation, key: bytes.Clone(key)}, nil
}

// KeyProvider loads the current protected envelope key. Implementations may
// create it on first use, but must preserve the generation for later opens.
type KeyProvider interface {
	LoadOrCreate(context.Context) (KeyMaterial, error)
}

// EnvelopeOptions configures randomness for envelope nonces. Production uses
// crypto/rand; tests can inject a deterministic reader without changing the
// envelope or key boundary.
type EnvelopeOptions struct {
	Random io.Reader
}

// EnvelopeVault combines a protected-key provider with the shared envelope
// construction. The provider is consulted for each operation so locked or
// unavailable platform vaults fail closed immediately.
type EnvelopeVault struct {
	provider KeyProvider
	random   io.Reader
	randomMu sync.Mutex
}

// NewEnvelopeVault creates a key-opaque authenticated envelope vault.
func NewEnvelopeVault(provider KeyProvider, options EnvelopeOptions) (*EnvelopeVault, error) {
	if provider == nil {
		return nil, apperrors.New(apperrors.VaultUnavailable, ErrUnavailable)
	}
	randomReader := options.Random
	if randomReader == nil {
		randomReader = rand.Reader
	}
	return &EnvelopeVault{provider: provider, random: randomReader}, nil
}

// Encrypt authenticates associatedData and encrypts plaintext into a
// versioned envelope containing only non-secret format and key-generation
// metadata.
func (vault *EnvelopeVault) Encrypt(ctx context.Context, plaintext, associatedData []byte) ([]byte, error) {
	material, err := vault.loadMaterial(ctx)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(material.key)
	if err != nil {
		return nil, apperrors.New(apperrors.VaultKeyInvalid, ErrInvalidKey)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, apperrors.New(apperrors.VaultEncryptionFailed, errors.Join(ErrEncryption, err))
	}
	nonce := make([]byte, gcm.NonceSize())
	vault.randomMu.Lock()
	_, randomErr := io.ReadFull(vault.random, nonce)
	vault.randomMu.Unlock()
	if randomErr != nil {
		return nil, apperrors.New(apperrors.VaultEncryptionFailed, errors.Join(ErrEncryption, randomErr))
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, associatedData)
	return encodeEnvelope(material.generation, nonce, ciphertext), nil
}

// Decrypt authenticates and decrypts an envelope only when its format,
// generation, key, and associated data all match the current vault state.
func (vault *EnvelopeVault) Decrypt(ctx context.Context, envelope, associatedData []byte) ([]byte, error) {
	material, err := vault.loadMaterial(ctx)
	if err != nil {
		return nil, err
	}
	metadata, nonce, ciphertext, err := parseEnvelope(envelope)
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(metadata.Generation), []byte(material.generation)) != 1 {
		return nil, apperrors.New(apperrors.VaultKeyGenerationMismatch, ErrGenerationMismatch)
	}

	block, err := aes.NewCipher(material.key)
	if err != nil {
		return nil, apperrors.New(apperrors.VaultKeyInvalid, ErrInvalidKey)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, apperrors.New(apperrors.VaultEnvelopeInvalid, errors.Join(ErrInvalidEnvelope, err))
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, associatedData)
	if err != nil {
		return nil, apperrors.New(apperrors.VaultEnvelopeInvalid, errors.Join(ErrInvalidEnvelope, ErrAuthentication, err))
	}
	return plaintext, nil
}

// Seal is an idiomatic alias for Encrypt on the concrete envelope vault.
func (vault *EnvelopeVault) Seal(ctx context.Context, plaintext, associatedData []byte) ([]byte, error) {
	return vault.Encrypt(ctx, plaintext, associatedData)
}

// Open is an idiomatic alias for Decrypt on the concrete envelope vault.
func (vault *EnvelopeVault) Open(ctx context.Context, envelope, associatedData []byte) ([]byte, error) {
	return vault.Decrypt(ctx, envelope, associatedData)
}

// EnvelopeMetadata contains the non-secret metadata carried by an envelope.
type EnvelopeMetadata struct {
	Version    uint8
	Algorithm  uint8
	Generation string
}

// ParseEnvelopeMetadata validates an envelope and returns only its
// non-secret metadata. It never returns ciphertext or key material.
func ParseEnvelopeMetadata(envelope []byte) (EnvelopeMetadata, error) {
	metadata, _, _, err := parseEnvelope(envelope)
	return metadata, err
}

func (vault *EnvelopeVault) loadMaterial(ctx context.Context) (KeyMaterial, error) {
	if vault == nil || vault.provider == nil {
		return KeyMaterial{}, apperrors.New(apperrors.VaultUnavailable, ErrUnavailable)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return KeyMaterial{}, apperrors.New(apperrors.VaultUnavailable, errors.Join(ErrUnavailable, err))
	}
	material, err := vault.provider.LoadOrCreate(ctx)
	if err != nil {
		if apperrors.Code(err) != "" {
			return KeyMaterial{}, err
		}
		return KeyMaterial{}, apperrors.New(apperrors.VaultUnavailable, errors.Join(ErrUnavailable, err))
	}
	if material.generation == "" || len(material.generation) > maxGenerationSize || len(material.key) != keySize {
		return KeyMaterial{}, apperrors.New(apperrors.VaultKeyInvalid, ErrInvalidKey)
	}
	return material, nil
}

func encodeEnvelope(generation string, nonce, ciphertext []byte) []byte {
	envelope := make([]byte, envelopeHeaderSize+len(generation)+len(nonce)+len(ciphertext))
	copy(envelope[:4], envelopeMagic)
	envelope[4] = EnvelopeVersion
	envelope[5] = envelopeAlgorithmAES256GCM
	binary.BigEndian.PutUint16(envelope[6:8], uint16(len(generation)))
	envelope[8] = uint8(len(nonce))
	position := envelopeHeaderSize
	position += copy(envelope[position:], generation)
	position += copy(envelope[position:], nonce)
	copy(envelope[position:], ciphertext)
	return envelope
}

func parseEnvelope(envelope []byte) (EnvelopeMetadata, []byte, []byte, error) {
	if len(envelope) < envelopeHeaderSize || !bytes.Equal(envelope[:4], []byte(envelopeMagic)) {
		return EnvelopeMetadata{}, nil, nil, apperrors.New(apperrors.VaultEnvelopeInvalid, ErrInvalidEnvelope)
	}
	if envelope[4] != EnvelopeVersion || envelope[5] != envelopeAlgorithmAES256GCM {
		return EnvelopeMetadata{}, nil, nil, apperrors.New(apperrors.VaultEnvelopeUnsupported, ErrUnsupportedEnvelope)
	}
	generationSize := int(binary.BigEndian.Uint16(envelope[6:8]))
	nonceSize := int(envelope[8])
	if generationSize == 0 || generationSize > maxGenerationSize || nonceSize != 12 {
		return EnvelopeMetadata{}, nil, nil, apperrors.New(apperrors.VaultEnvelopeInvalid, ErrInvalidEnvelope)
	}
	nonceStart := envelopeHeaderSize + generationSize
	ciphertextStart := nonceStart + nonceSize
	if ciphertextStart > len(envelope) || len(envelope)-ciphertextStart < 16 {
		return EnvelopeMetadata{}, nil, nil, apperrors.New(apperrors.VaultEnvelopeInvalid, ErrInvalidEnvelope)
	}
	metadata := EnvelopeMetadata{
		Version:    envelope[4],
		Algorithm:  envelope[5],
		Generation: string(envelope[envelopeHeaderSize:nonceStart]),
	}
	return metadata, envelope[nonceStart:ciphertextStart], envelope[ciphertextStart:], nil
}

// MemoryVault is a deterministic, failure-injectable vault for composed
// tests. It intentionally has no key accessor and implements the same public
// Vault boundary as platform adapters.
type MemoryVault struct {
	provider *memoryKeyProvider
	envelope *EnvelopeVault
}

// MemoryVaultOptions configures the in-memory adapter. A nil Key generates a
// fresh 256-bit key; supplied key bytes are copied and never exposed again.
type MemoryVaultOptions struct {
	Key        []byte
	Generation string
	Random     io.Reader
}

// NewMemoryVault creates an in-memory vault suitable for deterministic and
// failure-injection tests.
func NewMemoryVault(options MemoryVaultOptions) (*MemoryVault, error) {
	randomReader := options.Random
	if randomReader == nil {
		randomReader = rand.Reader
	}
	key := options.Key
	if key == nil {
		key = make([]byte, keySize)
		if _, err := io.ReadFull(randomReader, key); err != nil {
			return nil, apperrors.New(apperrors.VaultUnavailable, errors.Join(ErrUnavailable, err))
		}
	}
	generation := options.Generation
	if generation == "" {
		generation = "memory-1"
	}
	material, err := NewKeyMaterial(generation, key)
	if err != nil {
		return nil, err
	}
	provider := &memoryKeyProvider{material: material}
	envelope, err := NewEnvelopeVault(provider, EnvelopeOptions{Random: options.Random})
	if err != nil {
		return nil, err
	}
	return &MemoryVault{provider: provider, envelope: envelope}, nil
}

// NewInMemoryVault is a concise constructor for tests with fixed key and
// generation fixtures.
func NewInMemoryVault(key []byte, generation string) (*MemoryVault, error) {
	return NewMemoryVault(MemoryVaultOptions{Key: key, Generation: generation})
}

func (vault *MemoryVault) Encrypt(ctx context.Context, plaintext, associatedData []byte) ([]byte, error) {
	if vault == nil || vault.envelope == nil {
		return nil, apperrors.New(apperrors.VaultUnavailable, ErrUnavailable)
	}
	return vault.envelope.Encrypt(ctx, plaintext, associatedData)
}

func (vault *MemoryVault) Decrypt(ctx context.Context, envelope, associatedData []byte) ([]byte, error) {
	if vault == nil || vault.envelope == nil {
		return nil, apperrors.New(apperrors.VaultUnavailable, ErrUnavailable)
	}
	return vault.envelope.Decrypt(ctx, envelope, associatedData)
}

func (vault *MemoryVault) Seal(ctx context.Context, plaintext, associatedData []byte) ([]byte, error) {
	return vault.Encrypt(ctx, plaintext, associatedData)
}

func (vault *MemoryVault) Open(ctx context.Context, envelope, associatedData []byte) ([]byte, error) {
	return vault.Decrypt(ctx, envelope, associatedData)
}

// Lock makes future sensitive operations fail with the stable locked error.
func (vault *MemoryVault) Lock() {
	if vault != nil && vault.provider != nil {
		vault.provider.setState(memoryVaultLocked, nil)
	}
}

// SetUnavailable makes future sensitive operations fail with the stable
// unavailable error. The optional cause is retained only for errors.Is-based
// tests and is not included in the public error string.
func (vault *MemoryVault) SetUnavailable(cause error) {
	if vault != nil && vault.provider != nil {
		vault.provider.setState(memoryVaultUnavailable, cause)
	}
}

// SetAvailable restores the in-memory vault after a failure-injection state.
func (vault *MemoryVault) SetAvailable() {
	if vault != nil && vault.provider != nil {
		vault.provider.setState(memoryVaultAvailable, nil)
	}
}

type memoryVaultState uint8

const (
	memoryVaultAvailable memoryVaultState = iota
	memoryVaultLocked
	memoryVaultUnavailable
)

type memoryKeyProvider struct {
	mu          sync.RWMutex
	material    KeyMaterial
	state       memoryVaultState
	unavailable error
}

func (provider *memoryKeyProvider) LoadOrCreate(ctx context.Context) (KeyMaterial, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return KeyMaterial{}, errors.Join(ErrUnavailable, err)
	}
	provider.mu.RLock()
	defer provider.mu.RUnlock()
	switch provider.state {
	case memoryVaultLocked:
		return KeyMaterial{}, apperrors.New(apperrors.VaultLocked, ErrLocked)
	case memoryVaultUnavailable:
		return KeyMaterial{}, apperrors.New(apperrors.VaultUnavailable, errors.Join(ErrUnavailable, provider.unavailable))
	default:
		return provider.material, nil
	}
}

func (provider *memoryKeyProvider) setState(state memoryVaultState, cause error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.state = state
	provider.unavailable = cause
}
