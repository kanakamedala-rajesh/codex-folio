package continuation

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	CheckpointExportVersion   = "codex-folio.checkpoint.v1"
	PortableCheckpointVersion = "codex-folio.cfolio.v1"
	portableKDF               = "argon2id"
	portableCipher            = "aes-256-gcm"
	portableSaltSize          = 16
	portableNonceSize         = 12
	portableKeySize           = 32
	portableMaxArtifactSize   = 128 * 1024
)

var (
	ErrCheckpointExportInvalid        = errors.New("checkpoint export is invalid")
	ErrCheckpointExportUnsupported    = errors.New("checkpoint export version is unsupported")
	ErrCheckpointExportAuthentication = errors.New("checkpoint export authentication failed")
)

type CheckpointExport struct {
	FormatVersion string     `json:"format_version"`
	ExportedAt    time.Time  `json:"exported_at"`
	Checkpoint    Checkpoint `json:"checkpoint"`
}

type encryptedCheckpointExport struct {
	FormatVersion string `json:"format_version"`
	KDF           string `json:"kdf"`
	Cipher        string `json:"cipher"`
	Salt          string `json:"salt"`
	Nonce         string `json:"nonce"`
	Ciphertext    string `json:"ciphertext"`
}

func (service *Service) Export(ctx context.Context, id string) (CheckpointExport, error) {
	service.mutationMu.Lock()
	defer service.mutationMu.Unlock()
	checkpoint, err := service.show(ctx, id)
	if err != nil {
		return CheckpointExport{}, err
	}
	if checkpoint.Status != StatusApproved && checkpoint.Status != StatusCompleted {
		return CheckpointExport{}, ErrHandoffNotReady
	}
	return CheckpointExport{FormatVersion: CheckpointExportVersion, ExportedAt: service.now().UTC(), Checkpoint: checkpoint}, nil
}

func SealCheckpointExport(export CheckpointExport, passphrase string, random io.Reader) ([]byte, error) {
	if passphrase == "" || export.FormatVersion != CheckpointExportVersion || export.Checkpoint.Status != StatusApproved && export.Checkpoint.Status != StatusCompleted {
		return nil, ErrCheckpointExportInvalid
	}
	if random == nil {
		random = rand.Reader
	}
	salt := make([]byte, portableSaltSize)
	nonce := make([]byte, portableNonceSize)
	if _, err := io.ReadFull(random, salt); err != nil {
		return nil, errors.Join(ErrCheckpointExportInvalid, err)
	}
	if _, err := io.ReadFull(random, nonce); err != nil {
		return nil, errors.Join(ErrCheckpointExportInvalid, err)
	}
	plaintext, err := json.Marshal(export)
	if err != nil {
		return nil, errors.Join(ErrCheckpointExportInvalid, err)
	}
	key := derivePortableKey(passphrase, salt)
	defer clear(key)
	gcm, err := portableAEAD(key)
	if err != nil {
		return nil, errors.Join(ErrCheckpointExportInvalid, err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, portableAAD())
	container := encryptedCheckpointExport{
		FormatVersion: PortableCheckpointVersion, KDF: portableKDF, Cipher: portableCipher,
		Salt: base64.RawStdEncoding.EncodeToString(salt), Nonce: base64.RawStdEncoding.EncodeToString(nonce), Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext),
	}
	encoded, err := json.Marshal(container)
	if err != nil {
		return nil, errors.Join(ErrCheckpointExportInvalid, err)
	}
	return append(encoded, '\n'), nil
}

// OpenCheckpointExport authenticates an artifact for format qualification. It
// does not persist it or provide a checkpoint import workflow.
func OpenCheckpointExport(artifact []byte, passphrase string) (CheckpointExport, error) {
	if passphrase == "" || len(artifact) == 0 || len(artifact) > portableMaxArtifactSize {
		return CheckpointExport{}, ErrCheckpointExportInvalid
	}
	var container encryptedCheckpointExport
	decoder := json.NewDecoder(bytes.NewReader(artifact))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&container); err != nil {
		return CheckpointExport{}, errors.Join(ErrCheckpointExportInvalid, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return CheckpointExport{}, ErrCheckpointExportInvalid
	}
	if container.FormatVersion != PortableCheckpointVersion || container.KDF != portableKDF || container.Cipher != portableCipher {
		return CheckpointExport{}, ErrCheckpointExportUnsupported
	}
	salt, err := base64.RawStdEncoding.DecodeString(container.Salt)
	if err != nil || len(salt) != portableSaltSize {
		return CheckpointExport{}, ErrCheckpointExportInvalid
	}
	nonce, err := base64.RawStdEncoding.DecodeString(container.Nonce)
	if err != nil || len(nonce) != portableNonceSize {
		return CheckpointExport{}, ErrCheckpointExportInvalid
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(container.Ciphertext)
	if err != nil || len(ciphertext) < 16 {
		return CheckpointExport{}, ErrCheckpointExportInvalid
	}
	key := derivePortableKey(passphrase, salt)
	defer clear(key)
	gcm, err := portableAEAD(key)
	if err != nil {
		return CheckpointExport{}, errors.Join(ErrCheckpointExportInvalid, err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, portableAAD())
	if err != nil {
		return CheckpointExport{}, errors.Join(ErrCheckpointExportAuthentication, err)
	}
	var export CheckpointExport
	decoder = json.NewDecoder(bytes.NewReader(plaintext))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&export); err != nil || export.FormatVersion != CheckpointExportVersion || export.Checkpoint.Status != StatusApproved && export.Checkpoint.Status != StatusCompleted {
		return CheckpointExport{}, ErrCheckpointExportInvalid
	}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return CheckpointExport{}, ErrCheckpointExportInvalid
	}
	return export, nil
}

func derivePortableKey(passphrase string, salt []byte) []byte {
	return argon2.IDKey([]byte(passphrase), salt, 3, 64*1024, 2, portableKeySize)
}

func portableAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func portableAAD() []byte {
	return []byte(PortableCheckpointVersion + "/" + portableKDF + "/" + portableCipher)
}
