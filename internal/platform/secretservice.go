package platform

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
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
	// DefaultSecretServiceApplication is the stable Secret Service attribute
	// used to keep CodexFolio's item separate from unrelated application items.
	DefaultSecretServiceApplication = "venkatasudha.com.codex-folio"
	DefaultSecretServicePurpose     = "envelope-key"
	DefaultSecretServiceLabel       = "CodexFolio envelope key"

	secretServiceRecordMagic       = "CFSS"
	secretServiceRecordVersion     = 1
	secretServiceRecordHeaderSize  = 7
	secretServiceGenerationBytes   = 16
	secretServiceGenerationHexSize = secretServiceGenerationBytes * 2
	secretServiceEnvelopeKeySize   = 32
	secretServiceRecordSize        = secretServiceRecordHeaderSize + secretServiceGenerationHexSize + secretServiceEnvelopeKeySize
	maxSecretServiceAttributeSize  = 255
)

var (
	ErrSecretServiceUnavailable       = errors.New("Linux Secret Service is unavailable")
	ErrSecretServiceLocked            = errors.New("Linux Secret Service is locked")
	ErrSecretServiceAccessDenied      = errors.New("Linux Secret Service access was denied")
	ErrSecretServiceItemNotFound      = errors.New("Linux Secret Service item is missing")
	ErrSecretServiceItemExists        = errors.New("Linux Secret Service item already exists")
	ErrSecretServiceProtectedMaterial = errors.New("Linux Secret Service material is invalid")
	ErrSecretServiceAttributeInvalid  = errors.New("Linux Secret Service attribute is invalid")
)

// SecretServiceOptions configures the current-user Secret Service item. The
// collection itself is selected by Secret Service's default collection; the
// adapter never creates a parallel plaintext file or stores key bytes in the
// process environment.
type SecretServiceOptions struct {
	Application string
	Purpose     string
	Label       string
	AllowCreate bool
	Random      io.Reader
}

// SecretServiceKeyProvider loads an envelope key from a Secret Service item.
// Only opaque vault.KeyMaterial crosses into the shared envelope package.
type SecretServiceKeyProvider struct {
	application string
	purpose     string
	label       string
	allowCreate bool
	random      io.Reader
	backend     secretServiceBackend

	mu          sync.Mutex
	initialized bool
}

var _ vault.KeyProvider = (*SecretServiceKeyProvider)(nil)

// secretServiceBackend is the platform boundary for the Secret Service
// collection. The Linux production implementation uses the libsecret
// secret-tool client; tests inject deterministic collection behavior here.
type secretServiceBackend interface {
	lookup(context.Context, map[string]string) ([]byte, error)
	store(context.Context, string, map[string]string, []byte) error
}

// NewSecretServiceKeyProvider creates a provider using the stable CodexFolio
// Secret Service identity and allows explicit first-use initialization.
func NewSecretServiceKeyProvider() (*SecretServiceKeyProvider, error) {
	return NewSecretServiceKeyProviderWithOptions(SecretServiceOptions{AllowCreate: true})
}

// NewSecretServiceKeyProviderWithOptions creates a provider with explicit
// initialization behavior and item attributes.
func NewSecretServiceKeyProviderWithOptions(options SecretServiceOptions) (*SecretServiceKeyProvider, error) {
	return newSecretServiceKeyProviderWithBackend(options, newSystemSecretServiceBackend())
}

// NewSecretServiceVault creates an authenticated envelope vault backed by the
// current user's Secret Service collection.
func NewSecretServiceVault() (*vault.EnvelopeVault, error) {
	return NewSecretServiceVaultWithOptions(SecretServiceOptions{AllowCreate: true})
}

// NewSecretServiceVaultWithOptions creates and verifies a Secret Service
// backed envelope vault.
func NewSecretServiceVaultWithOptions(options SecretServiceOptions) (*vault.EnvelopeVault, error) {
	provider, err := NewSecretServiceKeyProviderWithOptions(options)
	if err != nil {
		return nil, err
	}
	material, err := provider.LoadOrCreate(context.Background())
	if err != nil {
		return nil, err
	}
	material.Clear()
	return vault.NewEnvelopeVault(provider, vault.EnvelopeOptions{})
}

func newSecretServiceKeyProviderWithBackend(options SecretServiceOptions, backend secretServiceBackend) (*SecretServiceKeyProvider, error) {
	application, err := normalizeSecretServiceAttribute(options.Application, DefaultSecretServiceApplication)
	if err != nil {
		return nil, err
	}
	purpose, err := normalizeSecretServiceAttribute(options.Purpose, DefaultSecretServicePurpose)
	if err != nil {
		return nil, err
	}
	label, err := normalizeSecretServiceAttribute(options.Label, DefaultSecretServiceLabel)
	if err != nil {
		return nil, err
	}
	if backend == nil {
		return nil, unavailableSecretService()
	}
	randomReader := options.Random
	if randomReader == nil {
		randomReader = rand.Reader
	}
	return &SecretServiceKeyProvider{
		application: application,
		purpose:     purpose,
		label:       label,
		allowCreate: options.AllowCreate,
		random:      randomReader,
		backend:     backend,
	}, nil
}

func (provider *SecretServiceKeyProvider) LoadOrCreate(ctx context.Context) (vault.KeyMaterial, error) {
	if provider == nil || provider.backend == nil {
		return vault.KeyMaterial{}, unavailableSecretService()
	}
	ctx = contextOrBackground(ctx)
	if err := ctx.Err(); err != nil {
		return vault.KeyMaterial{}, unavailableSecretService()
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	attributes := provider.attributes()
	if provider.initialized {
		return provider.loadExisting(ctx, attributes)
	}
	record, err := provider.backend.lookup(ctx, attributes)
	switch {
	case err == nil:
		material, loadErr := secretServiceKeyMaterial(record)
		clear(record)
		if loadErr == nil {
			provider.initialized = true
		}
		return material, loadErr
	case errors.Is(err, ErrSecretServiceItemNotFound) && provider.allowCreate:
		return provider.create(ctx, attributes)
	default:
		clear(record)
		return vault.KeyMaterial{}, normalizeSecretServiceError(err)
	}
}

func (provider *SecretServiceKeyProvider) loadExisting(ctx context.Context, attributes map[string]string) (vault.KeyMaterial, error) {
	record, err := provider.backend.lookup(ctx, attributes)
	if err != nil {
		return vault.KeyMaterial{}, normalizeSecretServiceError(err)
	}
	defer clear(record)
	return secretServiceKeyMaterial(record)
}

func (provider *SecretServiceKeyProvider) create(ctx context.Context, attributes map[string]string) (vault.KeyMaterial, error) {
	key := make([]byte, secretServiceEnvelopeKeySize)
	generationBytes := make([]byte, secretServiceGenerationBytes)
	var record []byte
	defer func() {
		clear(key)
		clear(generationBytes)
		clear(record)
	}()

	if err := ctx.Err(); err != nil {
		return vault.KeyMaterial{}, unavailableSecretService()
	}
	if _, err := io.ReadFull(provider.random, key); err != nil {
		return vault.KeyMaterial{}, unavailableSecretService()
	}
	if _, err := io.ReadFull(provider.random, generationBytes); err != nil {
		return vault.KeyMaterial{}, unavailableSecretService()
	}
	generation := hex.EncodeToString(generationBytes)
	record = encodeSecretServiceRecord(generation, key)
	if err := provider.backend.store(ctx, provider.label, attributes, record); err != nil {
		if errors.Is(err, ErrSecretServiceItemExists) {
			return provider.loadExisting(ctx, attributes)
		}
		return vault.KeyMaterial{}, normalizeSecretServiceError(err)
	}
	return vault.NewKeyMaterial(generation, key)
}

func (provider *SecretServiceKeyProvider) attributes() map[string]string {
	return map[string]string{
		"application": provider.application,
		"purpose":     provider.purpose,
	}
}

func encodeSecretServiceRecord(generation string, key []byte) []byte {
	record := make([]byte, secretServiceRecordHeaderSize+len(generation)+len(key))
	copy(record[:4], secretServiceRecordMagic)
	record[4] = secretServiceRecordVersion
	record[5] = byte(len(generation))
	record[6] = byte(len(key))
	position := secretServiceRecordHeaderSize
	position += copy(record[position:], generation)
	copy(record[position:], key)
	return record
}

func secretServiceKeyMaterial(record []byte) (vault.KeyMaterial, error) {
	generation, key, err := decodeSecretServiceRecord(record)
	if err != nil {
		return vault.KeyMaterial{}, invalidSecretServiceMaterial()
	}
	defer clear(key)
	return vault.NewKeyMaterial(generation, key)
}

func decodeSecretServiceRecord(record []byte) (string, []byte, error) {
	if len(record) != secretServiceRecordSize {
		return "", nil, ErrSecretServiceProtectedMaterial
	}
	if !bytes.Equal(record[:4], []byte(secretServiceRecordMagic)) {
		return "", nil, ErrSecretServiceProtectedMaterial
	}
	if record[4] != secretServiceRecordVersion || int(record[5]) != secretServiceGenerationHexSize || int(record[6]) != secretServiceEnvelopeKeySize {
		return "", nil, ErrSecretServiceProtectedMaterial
	}
	generationStart := secretServiceRecordHeaderSize
	keyStart := generationStart + secretServiceGenerationHexSize
	generation := string(record[generationStart:keyStart])
	if _, err := hex.DecodeString(generation); err != nil {
		return "", nil, ErrSecretServiceProtectedMaterial
	}
	return generation, bytes.Clone(record[keyStart:]), nil
}

func normalizeSecretServiceAttribute(value, fallback string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	if len(value) > maxSecretServiceAttributeSize || strings.IndexByte(value, 0) >= 0 || strings.HasPrefix(value, "-") || !utf8.ValidString(value) {
		return "", apperrors.New(apperrors.VaultUnavailable, ErrSecretServiceAttributeInvalid)
	}
	return value, nil
}

func unavailableSecretService() error {
	return apperrors.New(apperrors.VaultUnavailable, errors.Join(vault.ErrUnavailable, ErrSecretServiceUnavailable))
}

func lockedSecretService() error {
	return apperrors.New(apperrors.VaultLocked, errors.Join(vault.ErrLocked, ErrSecretServiceLocked))
}

func deniedSecretService() error {
	return apperrors.New(apperrors.VaultUnavailable, errors.Join(vault.ErrUnavailable, ErrSecretServiceAccessDenied))
}

func missingSecretService() error {
	return apperrors.New(apperrors.VaultUnavailable, errors.Join(vault.ErrUnavailable, ErrSecretServiceItemNotFound))
}

func invalidSecretServiceMaterial() error {
	return apperrors.New(apperrors.VaultKeyInvalid, errors.Join(vault.ErrInvalidKey, ErrSecretServiceProtectedMaterial))
}

func normalizeSecretServiceError(err error) error {
	if apperrors.Code(err) != "" {
		return err
	}
	switch {
	case errors.Is(err, ErrSecretServiceLocked):
		return lockedSecretService()
	case errors.Is(err, ErrSecretServiceAccessDenied):
		return deniedSecretService()
	case errors.Is(err, ErrSecretServiceItemNotFound):
		return missingSecretService()
	case errors.Is(err, ErrSecretServiceProtectedMaterial):
		return invalidSecretServiceMaterial()
	default:
		return unavailableSecretService()
	}
}

// secretServiceRecordText keeps the system Secret Service command boundary
// textual. The encoded value is still protected by the collection; base64
// prevents binary key material from being altered by command-line wrappers.
func secretServiceRecordText(record []byte) string {
	return base64.RawStdEncoding.EncodeToString(record)
}

func parseSecretServiceRecordText(value []byte) ([]byte, error) {
	value = bytes.TrimSpace(value)
	if len(value) == 0 {
		return nil, ErrSecretServiceProtectedMaterial
	}
	decoded, err := base64.RawStdEncoding.DecodeString(string(value))
	if err != nil {
		return nil, ErrSecretServiceProtectedMaterial
	}
	return decoded, nil
}
