package platform

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"
	"unicode/utf8"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/vault"
)

const (
	// DefaultKeychainService is the stable app-scoped Keychain service identity.
	// Keychain already scopes the item to the current macOS user's keychain.
	DefaultKeychainService = "venkatasudha.com.codex-folio.envelope"
	DefaultKeychainAccount = "envelope-key"

	keychainRecordMagic       = "CFKC"
	keychainRecordVersion     = 1
	keychainRecordHeaderSize  = 7
	keychainGenerationBytes   = 16
	keychainGenerationHexSize = keychainGenerationBytes * 2
	keychainEnvelopeKeySize   = 32
	keychainRecordSize        = keychainRecordHeaderSize + keychainGenerationHexSize + keychainEnvelopeKeySize
	maxKeychainAttributeSize  = 255
)

var (
	ErrKeychainServiceInvalid    = errors.New("Keychain service identity is invalid")
	ErrKeychainUnavailable       = errors.New("macOS Keychain is unavailable")
	ErrKeychainLocked            = errors.New("macOS Keychain is locked")
	ErrKeychainAccessDenied      = errors.New("macOS Keychain access was denied")
	ErrKeychainItemNotFound      = errors.New("macOS Keychain item is missing")
	ErrKeychainItemExists        = errors.New("macOS Keychain item already exists")
	ErrKeychainProtectedMaterial = errors.New("macOS Keychain material is invalid")
)

// KeychainOptions configures the current-user macOS Keychain adapter. Service
// and Account default to the stable CodexFolio identities when empty. Creation
// is explicit so an established state cannot silently receive a new key.
type KeychainOptions struct {
	Service     string
	Account     string
	AllowCreate bool
	Random      io.Reader
}

// KeychainKeyProvider loads an envelope key from a user-scoped macOS
// Keychain item. It exposes only vault.KeyMaterial to the shared envelope
// implementation; callers never receive the unwrapped key bytes.
type KeychainKeyProvider struct {
	service     string
	account     string
	allowCreate bool
	random      io.Reader
	backend     keychainBackend

	mu          sync.Mutex
	initialized bool
}

var _ vault.KeyProvider = (*KeychainKeyProvider)(nil)

// keychainBackend is the platform boundary for Keychain operations. The
// native implementation is backed by Security.framework; tests can inject a
// deterministic backend without weakening the production contract.
type keychainBackend interface {
	find(service, account string) ([]byte, error)
	add(service, account string, record []byte) error
	delete(service, account string) error
}

type systemKeychainBackend struct{}

// NewKeychainKeyProvider creates a provider using the stable default service
// identity, or an explicit service identity for an isolated test/application
// instance. At most one service value may be supplied.
func NewKeychainKeyProvider(service ...string) (*KeychainKeyProvider, error) {
	if len(service) > 1 {
		return nil, invalidKeychainService()
	}
	options := KeychainOptions{AllowCreate: true}
	if len(service) == 1 {
		options.Service = service[0]
	}
	return NewKeychainKeyProviderWithOptions(options)
}

// NewKeychainKeyProviderWithOptions creates a provider with explicit
// initialization and identity settings.
func NewKeychainKeyProviderWithOptions(options KeychainOptions) (*KeychainKeyProvider, error) {
	return newKeychainKeyProviderWithBackend(options, newSystemKeychainBackend())
}

// NewKeychainVault creates an authenticated envelope vault backed by the
// current user's macOS Keychain. The stable default service is used when no
// explicit service is supplied.
func NewKeychainVault(service ...string) (*vault.EnvelopeVault, error) {
	if len(service) > 1 {
		return nil, invalidKeychainService()
	}
	options := KeychainOptions{AllowCreate: true}
	if len(service) == 1 {
		options.Service = service[0]
	}
	return NewKeychainVaultWithOptions(options)
}

// NewKeychainVaultWithOptions creates a Keychain-backed authenticated
// envelope vault and verifies that the protected key can be loaded or
// initialized before returning.
func NewKeychainVaultWithOptions(options KeychainOptions) (*vault.EnvelopeVault, error) {
	provider, err := NewKeychainKeyProviderWithOptions(options)
	if err != nil {
		return nil, err
	}
	if _, err := provider.LoadOrCreate(context.Background()); err != nil {
		return nil, err
	}
	return vault.NewEnvelopeVault(provider, vault.EnvelopeOptions{})
}

func newKeychainKeyProviderWithBackend(options KeychainOptions, backend keychainBackend) (*KeychainKeyProvider, error) {
	service, err := normalizeKeychainAttribute(options.Service, DefaultKeychainService)
	if err != nil {
		return nil, err
	}
	account, err := normalizeKeychainAttribute(options.Account, DefaultKeychainAccount)
	if err != nil {
		return nil, err
	}
	if backend == nil {
		return nil, apperrors.New(apperrors.VaultUnavailable, ErrKeychainUnavailable)
	}
	randomReader := options.Random
	if randomReader == nil {
		randomReader = rand.Reader
	}
	return &KeychainKeyProvider{
		service:     service,
		account:     account,
		allowCreate: options.AllowCreate,
		random:      randomReader,
		backend:     backend,
	}, nil
}

func (provider *KeychainKeyProvider) LoadOrCreate(ctx context.Context) (vault.KeyMaterial, error) {
	if provider == nil || provider.backend == nil {
		return vault.KeyMaterial{}, unavailableKeychain()
	}
	ctx = contextOrBackground(ctx)
	if err := ctx.Err(); err != nil {
		return vault.KeyMaterial{}, unavailableKeychain()
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()

	if provider.initialized {
		return provider.loadExisting()
	}
	record, err := provider.backend.find(provider.service, provider.account)
	switch {
	case err == nil:
		material, loadErr := keyMaterialFromKeychainRecord(record)
		clear(record)
		if loadErr == nil {
			provider.initialized = true
		}
		return material, loadErr
	case errors.Is(err, ErrKeychainItemNotFound) && provider.allowCreate:
		material, createErr := provider.create(ctx)
		if createErr == nil {
			provider.initialized = true
		}
		return material, createErr
	default:
		clear(record)
		return vault.KeyMaterial{}, normalizeKeychainError(err)
	}
}

func (provider *KeychainKeyProvider) loadExisting() (vault.KeyMaterial, error) {
	record, err := provider.backend.find(provider.service, provider.account)
	if err != nil {
		return vault.KeyMaterial{}, normalizeKeychainError(err)
	}
	defer clear(record)
	return keyMaterialFromKeychainRecord(record)
}

func (provider *KeychainKeyProvider) create(ctx context.Context) (vault.KeyMaterial, error) {
	key := make([]byte, keychainEnvelopeKeySize)
	generationBytes := make([]byte, keychainGenerationBytes)
	var record []byte
	defer func() {
		clear(key)
		clear(generationBytes)
		clear(record)
	}()

	if err := ctx.Err(); err != nil {
		return vault.KeyMaterial{}, unavailableKeychain()
	}
	if _, err := io.ReadFull(provider.random, key); err != nil {
		return vault.KeyMaterial{}, unavailableKeychain()
	}
	if _, err := io.ReadFull(provider.random, generationBytes); err != nil {
		return vault.KeyMaterial{}, unavailableKeychain()
	}
	generation := hex.EncodeToString(generationBytes)
	record = encodeKeychainRecord(generation, key)
	if err := provider.backend.add(provider.service, provider.account, record); err != nil {
		if errors.Is(err, ErrKeychainItemExists) {
			return provider.loadExisting()
		}
		return vault.KeyMaterial{}, normalizeKeychainError(err)
	}
	return vault.NewKeyMaterial(generation, key)
}

func encodeKeychainRecord(generation string, key []byte) []byte {
	record := make([]byte, keychainRecordHeaderSize+len(generation)+len(key))
	copy(record[:4], keychainRecordMagic)
	record[4] = keychainRecordVersion
	record[5] = byte(len(generation))
	record[6] = byte(len(key))
	position := keychainRecordHeaderSize
	position += copy(record[position:], generation)
	copy(record[position:], key)
	return record
}

func keyMaterialFromKeychainRecord(record []byte) (vault.KeyMaterial, error) {
	generation, key, err := decodeKeychainRecord(record)
	if err != nil {
		return vault.KeyMaterial{}, invalidKeychainMaterial()
	}
	defer clear(key)
	return vault.NewKeyMaterial(generation, key)
}

func decodeKeychainRecord(record []byte) (string, []byte, error) {
	if len(record) != keychainRecordSize || !bytes.Equal(record[:4], []byte(keychainRecordMagic)) {
		return "", nil, ErrKeychainProtectedMaterial
	}
	if record[4] != keychainRecordVersion || int(record[5]) != keychainGenerationHexSize || int(record[6]) != keychainEnvelopeKeySize {
		return "", nil, ErrKeychainProtectedMaterial
	}
	generationStart := keychainRecordHeaderSize
	keyStart := generationStart + keychainGenerationHexSize
	generation := string(record[generationStart:keyStart])
	if _, err := hex.DecodeString(generation); err != nil {
		return "", nil, ErrKeychainProtectedMaterial
	}
	return generation, bytes.Clone(record[keyStart:]), nil
}

func normalizeKeychainAttribute(value, fallback string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	if len(value) > maxKeychainAttributeSize || strings.IndexByte(value, 0) >= 0 || !utf8.ValidString(value) {
		return "", invalidKeychainService()
	}
	return value, nil
}

func invalidKeychainService() error {
	return apperrors.New(apperrors.VaultUnavailable, ErrKeychainServiceInvalid)
}

func unavailableKeychain() error {
	return apperrors.New(apperrors.VaultUnavailable, errors.Join(vault.ErrUnavailable, ErrKeychainUnavailable))
}

func lockedKeychain() error {
	return apperrors.New(apperrors.VaultLocked, errors.Join(vault.ErrLocked, ErrKeychainLocked))
}

func deniedKeychain() error {
	return apperrors.New(apperrors.VaultUnavailable, errors.Join(vault.ErrUnavailable, ErrKeychainAccessDenied))
}

func missingKeychain() error {
	return apperrors.New(apperrors.VaultUnavailable, errors.Join(vault.ErrUnavailable, ErrKeychainItemNotFound))
}

func invalidKeychainMaterial() error {
	return apperrors.New(apperrors.VaultKeyInvalid, errors.Join(vault.ErrInvalidKey, ErrKeychainProtectedMaterial))
}

func normalizeKeychainError(err error) error {
	if apperrors.Code(err) != "" {
		return err
	}
	switch {
	case errors.Is(err, ErrKeychainLocked):
		return lockedKeychain()
	case errors.Is(err, ErrKeychainAccessDenied):
		return deniedKeychain()
	case errors.Is(err, ErrKeychainItemNotFound):
		return missingKeychain()
	case errors.Is(err, ErrKeychainProtectedMaterial):
		return invalidKeychainMaterial()
	default:
		return unavailableKeychain()
	}
}
