//go:build windows

package platform

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/vault"
)

const (
	dpapiRecordMagic        = "CFDP"
	dpapiRecordVersion      = 1
	dpapiRecordHeaderSize   = 11
	dpapiGenerationBytes    = 16
	dpapiGenerationHexSize  = dpapiGenerationBytes * 2
	dpapiMaxRecordSize      = 64 * 1024
	dpapiMaxBlobSize        = dpapiMaxRecordSize - dpapiRecordHeaderSize
	cryptProtectUIForbidden = 0x1
)

type dpapiDataBlob struct {
	cbData uint32
	pbData *byte
}

var (
	crypt32                = syscall.NewLazyDLL("crypt32.dll")
	cryptProtectDataProc   = crypt32.NewProc("CryptProtectData")
	cryptUnprotectDataProc = crypt32.NewProc("CryptUnprotectData")
)

var _ vault.KeyProvider = (*DPAPIKeyProvider)(nil)

func (provider *DPAPIKeyProvider) LoadOrCreate(ctx context.Context) (vault.KeyMaterial, error) {
	if provider == nil {
		return vault.KeyMaterial{}, unavailableDPAPI(nil)
	}
	ctx = contextOrBackground(ctx)
	if err := ctx.Err(); err != nil {
		return vault.KeyMaterial{}, unavailableDPAPI(err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()

	if provider.initialized {
		return provider.loadExisting()
	}
	if err := ensurePrivateDirectory(osFileSystem{}, filepath.Dir(provider.path)); err != nil {
		return vault.KeyMaterial{}, normalizeDPAPIFileError(err)
	}

	info, err := os.Lstat(provider.path)
	switch {
	case err == nil:
		material, loadErr := provider.loadExistingInfo(info)
		if loadErr == nil {
			provider.initialized = true
		}
		return material, loadErr
	case !errors.Is(err, os.ErrNotExist):
		return vault.KeyMaterial{}, unavailableDPAPI(os.ErrPermission)
	case !provider.allowCreate:
		return vault.KeyMaterial{}, unavailableDPAPI(os.ErrNotExist)
	default:
		material, createErr := provider.create(ctx)
		if createErr == nil {
			provider.initialized = true
		}
		return material, createErr
	}
}

func (provider *DPAPIKeyProvider) loadExisting() (vault.KeyMaterial, error) {
	info, err := os.Lstat(provider.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return vault.KeyMaterial{}, unavailableDPAPI(os.ErrNotExist)
		}
		return vault.KeyMaterial{}, unavailableDPAPI(os.ErrPermission)
	}
	return provider.loadExistingInfo(info)
}

func (provider *DPAPIKeyProvider) loadExistingInfo(info os.FileInfo) (vault.KeyMaterial, error) {
	if info.Mode()&os.ModeSymlink != 0 {
		return vault.KeyMaterial{}, apperrors.New(apperrors.PlatformStatePathUnsafe, errors.New("vault path cannot be a symbolic link"))
	}
	if !info.Mode().IsRegular() {
		return vault.KeyMaterial{}, invalidDPAPI(nil)
	}
	if err := enforcePrivatePermissions(provider.path); err != nil {
		return vault.KeyMaterial{}, normalizeDPAPIFileError(err)
	}

	record, err := readDPAPIRecord(provider.path)
	if err != nil {
		if errors.Is(err, ErrDPAPIProtectedMaterial) {
			return vault.KeyMaterial{}, invalidDPAPI(err)
		}
		if errors.Is(err, os.ErrNotExist) {
			return vault.KeyMaterial{}, unavailableDPAPI(os.ErrNotExist)
		}
		if errors.Is(err, os.ErrPermission) {
			return vault.KeyMaterial{}, unavailableDPAPI(os.ErrPermission)
		}
		return vault.KeyMaterial{}, unavailableDPAPI(nil)
	}
	return keyMaterialFromRecord(record)
}

func (provider *DPAPIKeyProvider) create(ctx context.Context) (vault.KeyMaterial, error) {
	key := make([]byte, dpapiEnvelopeKeySize)
	keyID := make([]byte, dpapiGenerationBytes)
	var record []byte
	defer func() {
		clear(key)
		clear(keyID)
		clear(record)
	}()

	if err := ctx.Err(); err != nil {
		return vault.KeyMaterial{}, unavailableDPAPI(err)
	}
	if _, err := io.ReadFull(provider.random, key); err != nil {
		return vault.KeyMaterial{}, unavailableDPAPI(err)
	}
	if _, err := io.ReadFull(provider.random, keyID); err != nil {
		return vault.KeyMaterial{}, unavailableDPAPI(err)
	}
	generation := hexGeneration(keyID)
	protected, err := protectDPAPI(key, []byte(generation))
	if err != nil {
		return vault.KeyMaterial{}, unavailableDPAPI(nil)
	}
	record = encodeDPAPIRecord(generation, protected)
	clear(protected)

	file, err := os.OpenFile(provider.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return provider.loadExisting()
		}
		return vault.KeyMaterial{}, unavailableDPAPI(os.ErrPermission)
	}
	removeFile := true
	defer func() {
		if removeFile {
			_ = os.Remove(provider.path)
		}
	}()

	if err := enforcePrivatePermissions(provider.path); err != nil {
		_ = file.Close()
		return vault.KeyMaterial{}, normalizeDPAPIFileError(err)
	}
	if _, err := file.Write(record); err != nil {
		_ = file.Close()
		return vault.KeyMaterial{}, unavailableDPAPI(os.ErrPermission)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return vault.KeyMaterial{}, unavailableDPAPI(os.ErrPermission)
	}
	if err := file.Close(); err != nil {
		return vault.KeyMaterial{}, unavailableDPAPI(os.ErrPermission)
	}
	removeFile = false

	material, err := vault.NewKeyMaterial(generation, key)
	if err != nil {
		return vault.KeyMaterial{}, err
	}
	return material, nil
}

type dpapiRecord struct {
	generation string
	protected  []byte
}

func readDPAPIRecord(path string) (dpapiRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return dpapiRecord{}, err
	}
	defer func() { _ = file.Close() }()

	record, err := io.ReadAll(io.LimitReader(file, dpapiMaxRecordSize+1))
	if err != nil {
		return dpapiRecord{}, err
	}
	if len(record) > dpapiMaxRecordSize {
		return dpapiRecord{}, ErrDPAPIProtectedMaterial
	}
	return decodeDPAPIRecord(record)
}

func encodeDPAPIRecord(generation string, protected []byte) []byte {
	record := make([]byte, dpapiRecordHeaderSize+len(generation)+len(protected))
	copy(record[:4], dpapiRecordMagic)
	record[4] = dpapiRecordVersion
	binary.BigEndian.PutUint16(record[5:7], uint16(len(generation)))
	binary.BigEndian.PutUint32(record[7:11], uint32(len(protected)))
	position := dpapiRecordHeaderSize
	position += copy(record[position:], generation)
	copy(record[position:], protected)
	return record
}

func decodeDPAPIRecord(record []byte) (dpapiRecord, error) {
	if len(record) < dpapiRecordHeaderSize || !bytes.Equal(record[:4], []byte(dpapiRecordMagic)) {
		return dpapiRecord{}, ErrDPAPIProtectedMaterial
	}
	if record[4] != dpapiRecordVersion {
		return dpapiRecord{}, ErrDPAPIProtectedMaterial
	}
	generationSize := int(binary.BigEndian.Uint16(record[5:7]))
	protectedSize := int(binary.BigEndian.Uint32(record[7:11]))
	if generationSize != dpapiGenerationHexSize || protectedSize == 0 || protectedSize > dpapiMaxBlobSize {
		return dpapiRecord{}, ErrDPAPIProtectedMaterial
	}
	if dpapiRecordHeaderSize+generationSize+protectedSize != len(record) {
		return dpapiRecord{}, ErrDPAPIProtectedMaterial
	}
	generation := string(record[dpapiRecordHeaderSize : dpapiRecordHeaderSize+generationSize])
	if !validHexGeneration(generation) {
		return dpapiRecord{}, ErrDPAPIProtectedMaterial
	}
	protectedStart := dpapiRecordHeaderSize + generationSize
	return dpapiRecord{
		generation: generation,
		protected:  bytes.Clone(record[protectedStart:]),
	}, nil
}

func keyMaterialFromRecord(record dpapiRecord) (vault.KeyMaterial, error) {
	decrypted, err := unprotectDPAPI(record.protected, []byte(record.generation))
	clear(record.protected)
	if err != nil {
		return vault.KeyMaterial{}, invalidDPAPI(nil)
	}
	defer clear(decrypted)
	if len(decrypted) != dpapiEnvelopeKeySize {
		return vault.KeyMaterial{}, invalidDPAPI(nil)
	}
	return vault.NewKeyMaterial(record.generation, decrypted)
}

func protectDPAPI(plaintext, optionalEntropy []byte) ([]byte, error) {
	if len(plaintext) == 0 || len(optionalEntropy) == 0 {
		return nil, ErrDPAPIProtectedMaterial
	}
	input := dpapiDataBlob{cbData: uint32(len(plaintext)), pbData: &plaintext[0]}
	entropy := dpapiDataBlob{cbData: uint32(len(optionalEntropy)), pbData: &optionalEntropy[0]}
	var output dpapiDataBlob
	result, _, _ := cryptProtectDataProc.Call(
		uintptr(unsafe.Pointer(&input)),
		0,
		uintptr(unsafe.Pointer(&entropy)),
		0,
		0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&output)),
	)
	if result == 0 || output.pbData == nil || output.cbData == 0 || output.cbData > dpapiMaxBlobSize {
		freeDPAPIData(&output)
		return nil, ErrDPAPIUnavailable
	}
	protected := bytes.Clone(unsafe.Slice(output.pbData, int(output.cbData)))
	freeDPAPIData(&output)
	return protected, nil
}

func unprotectDPAPI(protected, optionalEntropy []byte) ([]byte, error) {
	if len(protected) == 0 || len(optionalEntropy) == 0 {
		return nil, ErrDPAPIProtectedMaterial
	}
	input := dpapiDataBlob{cbData: uint32(len(protected)), pbData: &protected[0]}
	entropy := dpapiDataBlob{cbData: uint32(len(optionalEntropy)), pbData: &optionalEntropy[0]}
	var output dpapiDataBlob
	result, _, _ := cryptUnprotectDataProc.Call(
		uintptr(unsafe.Pointer(&input)),
		0,
		uintptr(unsafe.Pointer(&entropy)),
		0,
		0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&output)),
	)
	if result == 0 || output.pbData == nil || output.cbData == 0 || output.cbData > dpapiEnvelopeKeySize {
		freeDPAPIData(&output)
		return nil, ErrDPAPIProtectedMaterial
	}
	plaintext := bytes.Clone(unsafe.Slice(output.pbData, int(output.cbData)))
	freeDPAPIData(&output)
	return plaintext, nil
}

func freeDPAPIData(data *dpapiDataBlob) {
	if data == nil || data.pbData == nil {
		return
	}
	_, _ = syscall.LocalFree(syscall.Handle(unsafe.Pointer(data.pbData)))
	data.pbData = nil
	data.cbData = 0
}

func hexGeneration(value []byte) string {
	const hex = "0123456789abcdef"
	result := make([]byte, len(value)*2)
	for index, current := range value {
		result[index*2] = hex[current>>4]
		result[index*2+1] = hex[current&0x0f]
	}
	return string(result)
}

func validHexGeneration(value string) bool {
	if len(value) != dpapiGenerationHexSize {
		return false
	}
	for _, current := range value {
		if !(current >= '0' && current <= '9') && !(current >= 'a' && current <= 'f') {
			return false
		}
	}
	return true
}

func normalizeDPAPIFileError(err error) error {
	if apperrors.Code(err) != "" {
		return err
	}
	return unavailableDPAPI(err)
}
