package platform

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/vault"
)

const dpapiEnvelopeKeySize = 32

var (
	ErrDPAPIPathInvalid       = errors.New("DPAPI vault path is invalid")
	ErrDPAPIUnavailable       = errors.New("Windows DPAPI is unavailable")
	ErrDPAPIProtectedMaterial = errors.New("DPAPI protected vault material is invalid")
)

// DPAPIOptions configures the Windows user-scoped protected-key adapter.
// AllowCreate is intended for first-use initialization; callers opening an
// already established state root can disable it so missing material fails
// closed instead of creating a new generation.
type DPAPIOptions struct {
	AllowCreate bool
	Random      io.Reader
}

// DPAPIKeyProvider loads the opaque envelope key protected by the current
// Windows user through DPAPI. The key is never returned directly; callers
// receive vault.KeyMaterial, whose key bytes are private to the vault
// package.
type DPAPIKeyProvider struct {
	path        string
	allowCreate bool
	random      io.Reader

	mu          sync.Mutex
	initialized bool
}

// NewDPAPIKeyProvider creates a provider that initializes protected material
// when the app-local vault file is absent.
func NewDPAPIKeyProvider(path string) (*DPAPIKeyProvider, error) {
	return NewDPAPIKeyProviderWithOptions(path, DPAPIOptions{AllowCreate: true})
}

// NewDPAPIKeyProviderWithOptions creates a provider with explicit first-use
// initialization behavior.
func NewDPAPIKeyProviderWithOptions(path string, options DPAPIOptions) (*DPAPIKeyProvider, error) {
	path = strings.TrimSpace(path)
	cleanPath := filepath.Clean(path)
	if path == "" || !filepath.IsAbs(path) || isFilesystemRoot(cleanPath) || isFilesystemRoot(filepath.Dir(cleanPath)) {
		return nil, apperrors.New(apperrors.VaultUnavailable, ErrDPAPIPathInvalid)
	}
	randomReader := options.Random
	if randomReader == nil {
		randomReader = rand.Reader
	}
	return &DPAPIKeyProvider{
		path:        cleanPath,
		allowCreate: options.AllowCreate,
		random:      randomReader,
	}, nil
}

// NewDPAPIVault creates an authenticated envelope vault backed by a
// user-scoped Windows DPAPI protected key. The key file is initialized on
// first use and reused across provider instances for the same OS user.
func NewDPAPIVault(path string) (*vault.EnvelopeVault, error) {
	return NewDPAPIVaultWithOptions(path, DPAPIOptions{AllowCreate: true})
}

// NewDPAPIVaultWithOptions creates a DPAPI-backed envelope vault with
// explicit first-use initialization behavior.
func NewDPAPIVaultWithOptions(path string, options DPAPIOptions) (*vault.EnvelopeVault, error) {
	provider, err := NewDPAPIKeyProviderWithOptions(path, options)
	if err != nil {
		return nil, err
	}
	if _, err := provider.LoadOrCreate(context.Background()); err != nil {
		return nil, err
	}
	return vault.NewEnvelopeVault(provider, vault.EnvelopeOptions{})
}

func unavailableDPAPI(cause error) error {
	if cause == nil {
		cause = ErrDPAPIUnavailable
	}
	return apperrors.New(apperrors.VaultUnavailable, errors.Join(vault.ErrUnavailable, ErrDPAPIUnavailable, cause))
}

func invalidDPAPI(cause error) error {
	if cause == nil {
		cause = ErrDPAPIProtectedMaterial
	}
	return apperrors.New(apperrors.VaultKeyInvalid, errors.Join(vault.ErrInvalidKey, ErrDPAPIProtectedMaterial, cause))
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
