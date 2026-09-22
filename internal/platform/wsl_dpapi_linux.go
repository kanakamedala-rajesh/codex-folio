//go:build linux

package platform

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/vault"
)

const (
	wslVaultMagic                  = "CFWD"
	wslVaultVersion           byte = 1
	wslVaultHeaderSize             = 11
	wslVaultMaxRecordSize          = 64 * 1024
	WSLVaultHelperEnvironment      = "CODEX_FOLIO_WSL_VAULT_HELPER"
)

var ErrWSLDPAPIUnavailable = errors.New("Windows-backed WSL secure storage is unavailable")
var ErrWSLDPAPIProtectedMaterial = errors.New("Windows-backed WSL protected material is invalid")

type WSLDPAPIOptions struct {
	AllowCreate bool
	Random      io.Reader
	HelperPath  string
	run         func(context.Context, string, []byte) ([]byte, error)
}

type WSLDPAPIKeyProvider struct {
	path        string
	allowCreate bool
	random      io.Reader
	helperPath  string
	run         func(context.Context, string, []byte) ([]byte, error)
	mu          sync.Mutex
	initialized bool
}

var _ vault.KeyProvider = (*WSLDPAPIKeyProvider)(nil)

func NewWSLDPAPIVaultWithOptions(path string, options WSLDPAPIOptions) (*vault.EnvelopeVault, error) {
	provider, err := NewWSLDPAPIKeyProviderWithOptions(path, options)
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

func NewWSLDPAPIKeyProviderWithOptions(path string, options WSLDPAPIOptions) (*WSLDPAPIKeyProvider, error) {
	clean := filepath.Clean(strings.TrimSpace(path))
	if path == "" || !filepath.IsAbs(clean) || isFilesystemRoot(clean) || isFilesystemRoot(filepath.Dir(clean)) {
		return nil, unavailableWSLDPAPI(errors.New("WSL vault path is invalid"))
	}
	helper := strings.TrimSpace(options.HelperPath)
	if helper == "" {
		helper = strings.TrimSpace(os.Getenv(WSLVaultHelperEnvironment))
	}
	if helper != "" && !filepath.IsAbs(helper) {
		return nil, unavailableWSLDPAPI(errors.New("configured WSL vault helper path must be absolute"))
	}
	if helper == "" {
		var err error
		helper, err = discoverWSLVaultHelper()
		if err != nil {
			return nil, unavailableWSLDPAPI(err)
		}
	}
	randomReader := options.Random
	if randomReader == nil {
		randomReader = rand.Reader
	}
	runner := options.run
	if runner == nil {
		runner = runWSLVaultHelper
	}
	return &WSLDPAPIKeyProvider{path: clean, allowCreate: options.AllowCreate, random: randomReader, helperPath: helper, run: runner}, nil
}

func discoverWSLVaultHelper() (string, error) {
	if executable, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(executable), wslHelperName)
		if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	path, err := exec.LookPath(wslHelperName)
	if err != nil {
		return "", errors.New("codex-folio-wsl-vault.exe is missing; reinstall CodexFolio or configure CODEX_FOLIO_WSL_VAULT_HELPER")
	}
	return path, nil
}

func (provider *WSLDPAPIKeyProvider) LoadOrCreate(ctx context.Context) (vault.KeyMaterial, error) {
	if provider == nil || provider.run == nil {
		return vault.KeyMaterial{}, unavailableWSLDPAPI(nil)
	}
	ctx = contextOrBackground(ctx)
	if err := ctx.Err(); err != nil {
		return vault.KeyMaterial{}, unavailableWSLDPAPI(err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.initialized {
		return provider.load(ctx)
	}
	if err := ensurePrivateDirectory(osFileSystem{}, filepath.Dir(provider.path)); err != nil {
		return vault.KeyMaterial{}, unavailableWSLDPAPI(err)
	}
	info, err := os.Lstat(provider.path)
	switch {
	case err == nil:
		material, loadErr := provider.loadInfo(ctx, info)
		if loadErr == nil {
			provider.initialized = true
		}
		return material, loadErr
	case !errors.Is(err, os.ErrNotExist):
		return vault.KeyMaterial{}, unavailableWSLDPAPI(os.ErrPermission)
	case !provider.allowCreate:
		return vault.KeyMaterial{}, unavailableWSLDPAPI(os.ErrNotExist)
	default:
		material, createErr := provider.create(ctx)
		if createErr == nil {
			provider.initialized = true
		}
		return material, createErr
	}
}

func (provider *WSLDPAPIKeyProvider) load(ctx context.Context) (vault.KeyMaterial, error) {
	info, err := os.Lstat(provider.path)
	if err != nil {
		return vault.KeyMaterial{}, unavailableWSLDPAPI(err)
	}
	return provider.loadInfo(ctx, info)
}

func (provider *WSLDPAPIKeyProvider) loadInfo(ctx context.Context, info os.FileInfo) (vault.KeyMaterial, error) {
	if info.Mode()&os.ModeSymlink != 0 {
		return vault.KeyMaterial{}, apperrors.New(apperrors.PlatformStatePathUnsafe, errors.New("WSL vault path cannot be a symbolic link"))
	}
	if !info.Mode().IsRegular() {
		return vault.KeyMaterial{}, invalidWSLDPAPI(nil)
	}
	if err := enforcePrivatePermissions(provider.path); err != nil {
		return vault.KeyMaterial{}, unavailableWSLDPAPI(err)
	}
	file, err := os.Open(provider.path)
	if err != nil {
		return vault.KeyMaterial{}, unavailableWSLDPAPI(err)
	}
	record, readErr := io.ReadAll(io.LimitReader(file, wslVaultMaxRecordSize+1))
	err = errors.Join(readErr, file.Close())
	if err != nil || len(record) > wslVaultMaxRecordSize {
		return vault.KeyMaterial{}, invalidWSLDPAPI(err)
	}
	generation, protected, err := decodeWSLVaultRecord(record)
	clear(record)
	if err != nil {
		return vault.KeyMaterial{}, invalidWSLDPAPI(err)
	}
	request, _ := encodeWSLHelperRequest(wslOperationUnprotect, generation, protected)
	clear(protected)
	plaintext, err := provider.run(ctx, provider.helperPath, request)
	clear(request)
	if err != nil || len(plaintext) != dpapiEnvelopeKeySize {
		clear(plaintext)
		return vault.KeyMaterial{}, invalidWSLDPAPI(err)
	}
	defer clear(plaintext)
	return vault.NewKeyMaterial(generation, plaintext)
}

func (provider *WSLDPAPIKeyProvider) create(ctx context.Context) (vault.KeyMaterial, error) {
	key, generationBytes := make([]byte, dpapiEnvelopeKeySize), make([]byte, 16)
	defer clear(key)
	defer clear(generationBytes)
	if _, err := io.ReadFull(provider.random, key); err != nil {
		return vault.KeyMaterial{}, unavailableWSLDPAPI(err)
	}
	if _, err := io.ReadFull(provider.random, generationBytes); err != nil {
		return vault.KeyMaterial{}, unavailableWSLDPAPI(err)
	}
	generation := hex.EncodeToString(generationBytes)
	request, _ := encodeWSLHelperRequest(wslOperationProtect, generation, key)
	protected, err := provider.run(ctx, provider.helperPath, request)
	clear(request)
	if err != nil || len(protected) == 0 {
		clear(protected)
		return vault.KeyMaterial{}, unavailableWSLDPAPI(err)
	}
	record := encodeWSLVaultRecord(generation, protected)
	clear(protected)
	defer clear(record)
	file, err := os.OpenFile(provider.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return provider.load(ctx)
	}
	if err != nil {
		return vault.KeyMaterial{}, unavailableWSLDPAPI(err)
	}
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(provider.path)
		}
	}()
	if _, err = file.Write(record); err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return vault.KeyMaterial{}, unavailableWSLDPAPI(err)
	}
	remove = false
	return vault.NewKeyMaterial(generation, key)
}

func encodeWSLVaultRecord(generation string, protected []byte) []byte {
	record := make([]byte, wslVaultHeaderSize+len(generation)+len(protected))
	copy(record, wslVaultMagic)
	record[4] = wslVaultVersion
	binary.BigEndian.PutUint16(record[5:7], uint16(len(generation)))
	binary.BigEndian.PutUint32(record[7:11], uint32(len(protected)))
	copy(record[11:], generation)
	copy(record[11+len(generation):], protected)
	return record
}

func decodeWSLVaultRecord(record []byte) (string, []byte, error) {
	if len(record) < wslVaultHeaderSize || !bytes.Equal(record[:4], []byte(wslVaultMagic)) || record[4] != wslVaultVersion {
		return "", nil, ErrWSLDPAPIProtectedMaterial
	}
	gs, ps := int(binary.BigEndian.Uint16(record[5:7])), int(binary.BigEndian.Uint32(record[7:11]))
	if gs != 32 || ps == 0 || ps > wslProtocolMaxPayload || wslVaultHeaderSize+gs+ps != len(record) {
		return "", nil, ErrWSLDPAPIProtectedMaterial
	}
	generation := string(record[11 : 11+gs])
	if !validWSLGeneration(generation) {
		return "", nil, ErrWSLDPAPIProtectedMaterial
	}
	return generation, bytes.Clone(record[11+gs:]), nil
}

func runWSLVaultHelper(ctx context.Context, helper string, request []byte) ([]byte, error) {
	timeoutContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(timeoutContext, helper)
	command.Stdin = bytes.NewReader(request)
	var stdout bytes.Buffer
	command.Stdout = &limitedWriter{writer: &stdout, remaining: wslProtocolMaxPayload + 10}
	command.Stderr = io.Discard
	command.Env = wslInteropEnvironment()
	if err := command.Run(); err != nil {
		return nil, unavailableWSLDPAPI(errors.New("Windows interop or the Windows vault helper is unavailable"))
	}
	return decodeWSLHelperResponse(stdout.Bytes())
}

type limitedWriter struct {
	writer    io.Writer
	remaining int
}

func (writer *limitedWriter) Write(data []byte) (int, error) {
	if len(data) > writer.remaining {
		return 0, errors.New("helper response exceeds limit")
	}
	count, err := writer.writer.Write(data)
	writer.remaining -= count
	return count, err
}

func wslInteropEnvironment() []string {
	result := make([]string, 0, 2)
	for _, name := range []string{"WSL_INTEROP", "WSLENV"} {
		if value, ok := os.LookupEnv(name); ok {
			result = append(result, name+"="+value)
		}
	}
	return result
}

func unavailableWSLDPAPI(cause error) error {
	if cause == nil {
		cause = ErrWSLDPAPIUnavailable
	}
	return apperrors.New(apperrors.VaultUnavailable, errors.Join(vault.ErrUnavailable, ErrWSLDPAPIUnavailable, cause))
}
func invalidWSLDPAPI(cause error) error {
	if cause == nil {
		cause = ErrWSLDPAPIProtectedMaterial
	}
	return apperrors.New(apperrors.VaultKeyInvalid, errors.Join(vault.ErrInvalidKey, ErrWSLDPAPIProtectedMaterial, cause))
}
