package platform

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/vault"
)

const (
	passphraseRecordMagic      = "CFPV"
	passphraseRecordVersion    = 1
	passphraseKDFArgon2ID      = 1
	passphraseCipherAES256GCM  = 1
	passphraseRecordHeaderSize = 13
	passphraseSaltSize         = 16
	passphraseNonceSize        = 12
	passphraseGenerationSize   = 16
	passphraseEnvelopeKeySize  = 32
	passphraseMaxRecordSize    = 64 * 1024
	passphraseArgon2Time       = 3
	passphraseArgon2Memory     = 64 * 1024
	passphraseArgon2Threads    = 2
	passphraseDerivedKeySize   = 32
)

var (
	ErrPassphrasePathInvalid       = errors.New("passphrase vault path is invalid")
	ErrPassphraseUnavailable       = errors.New("passphrase vault is unavailable")
	ErrPassphraseLocked            = errors.New("passphrase vault is locked")
	ErrPassphraseInvalid           = errors.New("passphrase does not unlock the vault")
	ErrPassphraseProtectedMaterial = errors.New("passphrase vault material is invalid")
	ErrPassphraseUnsupported       = errors.New("passphrase vault format is unsupported")
)

// PassphraseOptions configures the app-local passphrase vault. The file is
// never initialized merely by selecting this mode; initialization requires an
// explicit Unlock call with a passphrase.
type PassphraseOptions struct {
	AllowCreate bool
	Random      io.Reader
}

// PassphraseKeyProvider protects the envelope key with a passphrase-derived
// wrapping key. It starts locked for every provider instance, including when a
// valid protected file already exists. The unlocked key is held only in memory
// for the lifetime of this provider session.
type PassphraseKeyProvider struct {
	path        string
	allowCreate bool
	random      io.Reader

	mu          sync.RWMutex
	material    vault.KeyMaterial
	initialized bool
	unlocked    bool
}

var _ vault.KeyProvider = (*PassphraseKeyProvider)(nil)

// NewPassphraseKeyProvider creates a locked provider that may initialize a
// missing vault file after an explicit Unlock call.
func NewPassphraseKeyProvider(path string) (*PassphraseKeyProvider, error) {
	return NewPassphraseKeyProviderWithOptions(path, PassphraseOptions{AllowCreate: true})
}

// NewPassphraseKeyProviderWithOptions creates a locked provider with explicit
// first-use behavior. Disabling AllowCreate makes missing established state
// fail closed instead of generating a new key generation.
func NewPassphraseKeyProviderWithOptions(path string, options PassphraseOptions) (*PassphraseKeyProvider, error) {
	cleanPath, err := normalizePassphrasePath(path)
	if err != nil {
		return nil, apperrors.New(apperrors.VaultUnavailable, err)
	}
	randomReader := options.Random
	if randomReader == nil {
		randomReader = rand.Reader
	}
	return &PassphraseKeyProvider{
		path:        cleanPath,
		allowCreate: options.AllowCreate,
		random:      randomReader,
	}, nil
}

// NewPassphraseVault creates a locked authenticated envelope vault backed by a
// passphrase-protected app-local file.
func NewPassphraseVault(path string) (*PassphraseVault, error) {
	return NewPassphraseVaultWithOptions(path, PassphraseOptions{AllowCreate: true})
}

// NewPassphraseVaultWithOptions creates a locked passphrase-backed envelope
// vault. Call Unlock before sensitive operations; selecting this constructor is
// never treated as consent to plaintext storage.
func NewPassphraseVaultWithOptions(path string, options PassphraseOptions) (*PassphraseVault, error) {
	provider, err := NewPassphraseKeyProviderWithOptions(path, options)
	if err != nil {
		return nil, err
	}
	envelope, err := vault.NewEnvelopeVault(provider, vault.EnvelopeOptions{})
	if err != nil {
		return nil, err
	}
	return &PassphraseVault{provider: provider, envelope: envelope}, nil
}

// PassphraseVault is the explicit session-controlled vault used by WSL and
// headless Linux operation.
type PassphraseVault struct {
	provider *PassphraseKeyProvider
	envelope *vault.EnvelopeVault
}

var _ vault.Vault = (*PassphraseVault)(nil)

// Unlock initializes or opens the protected vault file and keeps its envelope
// key in memory for this provider session only. The passphrase is never stored
// in the provider, file, process arguments, or environment.
func (secureVault *PassphraseVault) Unlock(ctx context.Context, passphrase string) error {
	if secureVault == nil || secureVault.provider == nil {
		return unavailablePassphrase(nil)
	}
	return secureVault.provider.Unlock(ctx, passphrase)
}

// Lock clears the in-memory envelope key and requires another explicit Unlock
// before sensitive operations can continue.
func (secureVault *PassphraseVault) Lock() {
	if secureVault != nil && secureVault.provider != nil {
		secureVault.provider.Lock()
	}
}

// IsUnlocked reports only session state and never returns key material.
func (secureVault *PassphraseVault) IsUnlocked() bool {
	return secureVault != nil && secureVault.provider != nil && secureVault.provider.IsUnlocked()
}

func (secureVault *PassphraseVault) Encrypt(ctx context.Context, plaintext, associatedData []byte) ([]byte, error) {
	if secureVault == nil || secureVault.envelope == nil {
		return nil, unavailablePassphrase(nil)
	}
	return secureVault.envelope.Encrypt(ctx, plaintext, associatedData)
}

func (secureVault *PassphraseVault) Decrypt(ctx context.Context, envelope, associatedData []byte) ([]byte, error) {
	if secureVault == nil || secureVault.envelope == nil {
		return nil, unavailablePassphrase(nil)
	}
	return secureVault.envelope.Decrypt(ctx, envelope, associatedData)
}

func (secureVault *PassphraseVault) Seal(ctx context.Context, plaintext, associatedData []byte) ([]byte, error) {
	return secureVault.Encrypt(ctx, plaintext, associatedData)
}

func (secureVault *PassphraseVault) Open(ctx context.Context, envelope, associatedData []byte) ([]byte, error) {
	return secureVault.Decrypt(ctx, envelope, associatedData)
}

// Unlock opens the provider for the current process session. A missing file is
// initialized only when AllowCreate was explicitly enabled by the composition
// root and the caller supplies a passphrase.
func (provider *PassphraseKeyProvider) Unlock(ctx context.Context, passphrase string) error {
	if provider == nil {
		return unavailablePassphrase(nil)
	}
	ctx = contextOrBackground(ctx)
	if err := ctx.Err(); err != nil {
		return unavailablePassphrase(err)
	}
	if passphrase == "" {
		return invalidPassphrase()
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.unlocked {
		return nil
	}

	if err := ensurePrivateDirectory(osFileSystem{}, filepath.Dir(provider.path)); err != nil {
		return normalizePassphraseFileError(err)
	}
	info, err := os.Lstat(provider.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if provider.initialized {
			return unavailablePassphrase(os.ErrNotExist)
		}
		if !provider.allowCreate {
			return unavailablePassphrase(os.ErrNotExist)
		}
		return provider.createAndUnlock(ctx, passphrase)
	case err != nil:
		return unavailablePassphrase(os.ErrPermission)
	default:
		return provider.openAndUnlock(ctx, passphrase, info)
	}
}

// LoadOrCreate deliberately returns locked until Unlock has been called. This
// keeps the shared vault contract fail closed for all sensitive reads/writes.
func (provider *PassphraseKeyProvider) LoadOrCreate(ctx context.Context) (vault.KeyMaterial, error) {
	if provider == nil {
		return vault.KeyMaterial{}, unavailablePassphrase(nil)
	}
	ctx = contextOrBackground(ctx)
	if err := ctx.Err(); err != nil {
		return vault.KeyMaterial{}, unavailablePassphrase(err)
	}
	provider.mu.RLock()
	defer provider.mu.RUnlock()
	if !provider.unlocked {
		return vault.KeyMaterial{}, lockedPassphrase()
	}
	return provider.material, nil
}

// Lock ends the in-memory session without deleting or rewriting the protected
// file. The next process or session must provide the passphrase again.
func (provider *PassphraseKeyProvider) Lock() {
	if provider == nil {
		return
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.material.Clear()
	provider.unlocked = false
}

// IsUnlocked reports current in-memory session state.
func (provider *PassphraseKeyProvider) IsUnlocked() bool {
	if provider == nil {
		return false
	}
	provider.mu.RLock()
	defer provider.mu.RUnlock()
	return provider.unlocked
}

func (provider *PassphraseKeyProvider) createAndUnlock(ctx context.Context, passphrase string) error {
	key := make([]byte, passphraseEnvelopeKeySize)
	generationBytes := make([]byte, passphraseGenerationSize)
	salt := make([]byte, passphraseSaltSize)
	nonce := make([]byte, passphraseNonceSize)
	var record []byte
	defer func() {
		clear(key)
		clear(generationBytes)
		clear(salt)
		clear(nonce)
		clear(record)
	}()

	if err := ctx.Err(); err != nil {
		return unavailablePassphrase(err)
	}
	for _, destination := range [][]byte{key, generationBytes, salt, nonce} {
		if _, err := io.ReadFull(provider.random, destination); err != nil {
			return unavailablePassphrase(nil)
		}
	}
	generation := hex.EncodeToString(generationBytes)
	record, err := sealPassphraseRecord(passphrase, generation, key, salt, nonce)
	if err != nil {
		return err
	}
	if err := writePassphraseRecord(provider.path, record); err != nil {
		if errors.Is(err, os.ErrExist) {
			info, statErr := os.Lstat(provider.path)
			if statErr != nil {
				return unavailablePassphrase(os.ErrPermission)
			}
			return provider.openAndUnlock(ctx, passphrase, info)
		}
		return normalizePassphraseFileError(err)
	}
	return provider.acceptKey(generation, key)
}

func (provider *PassphraseKeyProvider) openAndUnlock(ctx context.Context, passphrase string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return apperrors.New(apperrors.PlatformStatePathUnsafe, errors.New("vault path cannot be a symbolic link"))
	}
	if !info.Mode().IsRegular() || !isPrivateFileMode(info.Mode()) {
		return unavailablePassphrase(os.ErrPermission)
	}
	if err := enforcePrivatePermissions(provider.path); err != nil {
		return normalizePassphraseFileError(err)
	}
	record, err := readPassphraseRecord(provider.path)
	if err != nil {
		return normalizePassphraseRecordError(err)
	}
	decrypted, err := openPassphraseRecord(passphrase, record)
	clear(record.salt)
	clear(record.nonce)
	clear(record.ciphertext)
	if err != nil {
		return err
	}
	defer clear(decrypted)
	generation, key, err := decodePassphrasePayload(decrypted)
	if err != nil {
		return invalidPassphraseMaterial(err)
	}
	defer clear(key)
	return provider.acceptKey(generation, key)
}

func (provider *PassphraseKeyProvider) acceptKey(generation string, key []byte) error {
	material, err := vault.NewKeyMaterial(generation, key)
	if err != nil {
		return err
	}
	provider.material.Clear()
	provider.material = material
	provider.initialized = true
	provider.unlocked = true
	return nil
}

type passphraseRecord struct {
	header     []byte
	salt       []byte
	nonce      []byte
	ciphertext []byte
}

func sealPassphraseRecord(passphrase, generation string, key, salt, nonce []byte) ([]byte, error) {
	if len(key) != passphraseEnvelopeKeySize || len(salt) != passphraseSaltSize || len(nonce) != passphraseNonceSize || generation == "" {
		return nil, invalidPassphraseMaterial(nil)
	}
	derived := derivePassphraseKey(passphrase, salt)
	defer clear(derived)
	block, err := aes.NewCipher(derived)
	if err != nil {
		return nil, unavailablePassphrase(nil)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, unavailablePassphrase(nil)
	}
	payload := encodePassphrasePayload(generation, key)
	defer clear(payload)
	header := encodePassphraseHeader(len(payload)+gcm.Overhead(), salt, nonce)
	ciphertext := gcm.Seal(nil, nonce, payload, passphraseAAD(header))
	return joinPassphraseRecord(header, salt, nonce, ciphertext), nil
}

func openPassphraseRecord(passphrase string, record passphraseRecord) ([]byte, error) {
	if len(record.header) != passphraseRecordHeaderSize || len(record.salt) != passphraseSaltSize || len(record.nonce) != passphraseNonceSize {
		return nil, invalidPassphraseMaterial(nil)
	}
	derived := derivePassphraseKey(passphrase, record.salt)
	defer clear(derived)
	block, err := aes.NewCipher(derived)
	if err != nil {
		return nil, invalidPassphraseMaterial(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, invalidPassphraseMaterial(err)
	}
	plaintext, err := gcm.Open(nil, record.nonce, record.ciphertext, passphraseAAD(record.header))
	if err != nil {
		return nil, invalidPassphrase()
	}
	return plaintext, nil
}

func derivePassphraseKey(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, passphraseArgon2Time, passphraseArgon2Memory, passphraseArgon2Threads, passphraseDerivedKeySize)
}

func encodePassphrasePayload(generation string, key []byte) []byte {
	payload := make([]byte, 2+len(generation)+len(key))
	binary.BigEndian.PutUint16(payload[:2], uint16(len(generation)))
	position := 2
	position += copy(payload[position:], generation)
	copy(payload[position:], key)
	return payload
}

func decodePassphrasePayload(payload []byte) (string, []byte, error) {
	if len(payload) < 2+passphraseEnvelopeKeySize {
		return "", nil, ErrPassphraseProtectedMaterial
	}
	generationSize := int(binary.BigEndian.Uint16(payload[:2]))
	if generationSize <= 0 || generationSize > 128 || 2+generationSize+passphraseEnvelopeKeySize != len(payload) {
		return "", nil, ErrPassphraseProtectedMaterial
	}
	generationStart := 2
	keyStart := generationStart + generationSize
	return string(payload[generationStart:keyStart]), bytes.Clone(payload[keyStart:]), nil
}

func encodePassphraseHeader(ciphertextSize int, salt, nonce []byte) []byte {
	header := make([]byte, passphraseRecordHeaderSize)
	copy(header[:4], passphraseRecordMagic)
	header[4] = passphraseRecordVersion
	header[5] = passphraseKDFArgon2ID
	header[6] = passphraseCipherAES256GCM
	header[7] = byte(len(salt))
	header[8] = byte(len(nonce))
	binary.BigEndian.PutUint32(header[9:13], uint32(ciphertextSize))
	return header
}

func joinPassphraseRecord(header, salt, nonce, ciphertext []byte) []byte {
	record := make([]byte, 0, len(header)+len(salt)+len(nonce)+len(ciphertext))
	record = append(record, header...)
	record = append(record, salt...)
	record = append(record, nonce...)
	record = append(record, ciphertext...)
	return record
}

func passphraseAAD(header []byte) []byte {
	aad := make([]byte, 0, len(passphraseRecordMagic)+len(header)+32)
	aad = append(aad, []byte("codex-folio/passphrase-vault/")...)
	aad = append(aad, header...)
	return aad
}

func decodePassphraseRecord(record []byte) (passphraseRecord, error) {
	if len(record) < passphraseRecordHeaderSize || !bytes.Equal(record[:4], []byte(passphraseRecordMagic)) {
		return passphraseRecord{}, ErrPassphraseProtectedMaterial
	}
	if record[4] != passphraseRecordVersion || record[5] != passphraseKDFArgon2ID || record[6] != passphraseCipherAES256GCM {
		return passphraseRecord{}, ErrPassphraseUnsupported
	}
	saltSize := int(record[7])
	nonceSize := int(record[8])
	ciphertextSize := int(binary.BigEndian.Uint32(record[9:13]))
	if saltSize != passphraseSaltSize || nonceSize != passphraseNonceSize || ciphertextSize < 16 || ciphertextSize > passphraseMaxRecordSize || passphraseRecordHeaderSize+saltSize+nonceSize+ciphertextSize != len(record) {
		return passphraseRecord{}, ErrPassphraseProtectedMaterial
	}
	saltStart := passphraseRecordHeaderSize
	nonceStart := saltStart + saltSize
	ciphertextStart := nonceStart + nonceSize
	return passphraseRecord{
		header:     bytes.Clone(record[:passphraseRecordHeaderSize]),
		salt:       bytes.Clone(record[saltStart:nonceStart]),
		nonce:      bytes.Clone(record[nonceStart:ciphertextStart]),
		ciphertext: bytes.Clone(record[ciphertextStart:]),
	}, nil
}

func readPassphraseRecord(path string) (passphraseRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return passphraseRecord{}, err
	}
	defer func() { _ = file.Close() }()
	record, err := io.ReadAll(io.LimitReader(file, passphraseMaxRecordSize+1))
	if err != nil {
		return passphraseRecord{}, err
	}
	if len(record) > passphraseMaxRecordSize {
		return passphraseRecord{}, ErrPassphraseProtectedMaterial
	}
	return decodePassphraseRecord(record)
}

func writePassphraseRecord(path string, record []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	removeFile := true
	defer func() {
		if removeFile {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if err := enforcePrivatePermissions(path); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(record); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	removeFile = false
	return nil
}

func normalizePassphrasePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	cleanPath := filepath.Clean(path)
	if path == "" || !filepath.IsAbs(path) || isFilesystemRoot(cleanPath) || isFilesystemRoot(filepath.Dir(cleanPath)) {
		return "", ErrPassphrasePathInvalid
	}
	return cleanPath, nil
}

func normalizePassphraseFileError(err error) error {
	if apperrors.Code(err) != "" {
		return err
	}
	if errors.Is(err, os.ErrNotExist) {
		return unavailablePassphrase(os.ErrNotExist)
	}
	return unavailablePassphrase(os.ErrPermission)
}

func normalizePassphraseRecordError(err error) error {
	if errors.Is(err, ErrPassphraseUnsupported) {
		return apperrors.New(apperrors.VaultKeyInvalid, errors.Join(vault.ErrInvalidKey, err))
	}
	if errors.Is(err, ErrPassphraseProtectedMaterial) {
		return invalidPassphraseMaterial(err)
	}
	return normalizePassphraseFileError(err)
}

func unavailablePassphrase(cause error) error {
	if cause == nil {
		cause = ErrPassphraseUnavailable
	}
	return apperrors.New(apperrors.VaultUnavailable, errors.Join(vault.ErrUnavailable, ErrPassphraseUnavailable, cause))
}

func lockedPassphrase() error {
	return apperrors.New(apperrors.VaultLocked, errors.Join(vault.ErrLocked, ErrPassphraseLocked))
}

func invalidPassphrase() error {
	return apperrors.New(apperrors.VaultKeyInvalid, errors.Join(vault.ErrInvalidKey, ErrPassphraseInvalid))
}

func invalidPassphraseMaterial(cause error) error {
	if cause == nil {
		cause = ErrPassphraseProtectedMaterial
	}
	return apperrors.New(apperrors.VaultKeyInvalid, errors.Join(vault.ErrInvalidKey, ErrPassphraseProtectedMaterial, cause))
}
