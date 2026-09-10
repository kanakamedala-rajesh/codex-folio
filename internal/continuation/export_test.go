package continuation

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPortableCheckpointExportAuthenticatedRoundTrip(t *testing.T) {
	exported := CheckpointExport{
		FormatVersion: CheckpointExportVersion,
		ExportedAt:    time.Date(2026, 9, 10, 13, 30, 0, 0, time.UTC),
		Checkpoint: Checkpoint{
			ID: "checkpoint-1", Status: StatusApproved,
			Project: Project{ID: "project-1", Alias: "folio", Basename: "codex-folio"},
			Fields:  CheckpointFields{Goal: userField("portable secret context")},
		},
	}
	random := bytes.NewReader(append(bytes.Repeat([]byte{0x31}, portableSaltSize), bytes.Repeat([]byte{0x42}, portableNonceSize)...))
	artifact, err := SealCheckpointExport(exported, "correct horse battery staple", random)
	if err != nil {
		t.Fatalf("SealCheckpointExport() error = %v", err)
	}
	if bytes.Contains(artifact, []byte("portable secret context")) || bytes.Contains(artifact, []byte("correct horse battery staple")) {
		t.Fatalf("encrypted artifact contains protected content: %s", artifact)
	}
	var container encryptedCheckpointExport
	if err := json.Unmarshal(artifact, &container); err != nil {
		t.Fatal(err)
	}
	if container.FormatVersion != PortableCheckpointVersion || container.KDF != portableKDF || container.Cipher != portableCipher {
		t.Fatalf("container metadata = %#v", container)
	}
	opened, err := OpenCheckpointExport(artifact, "correct horse battery staple")
	if err != nil || opened.FormatVersion != CheckpointExportVersion || opened.Checkpoint.Fields.Goal.Value != "portable secret context" {
		t.Fatalf("OpenCheckpointExport() = %#v, %v", opened, err)
	}
	if _, err := OpenCheckpointExport(artifact, "wrong passphrase"); !errors.Is(err, ErrCheckpointExportAuthentication) {
		t.Fatalf("wrong passphrase error = %v", err)
	}

	ciphertext, err := base64.RawStdEncoding.DecodeString(container.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext[len(ciphertext)-1] ^= 1
	container.Ciphertext = base64.RawStdEncoding.EncodeToString(ciphertext)
	tampered, err := json.Marshal(container)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenCheckpointExport(tampered, "correct horse battery staple"); !errors.Is(err, ErrCheckpointExportAuthentication) {
		t.Fatalf("tampered error = %v", err)
	}
	container.FormatVersion = "codex-folio.cfolio.v2"
	unsupported, err := json.Marshal(container)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenCheckpointExport(unsupported, "correct horse battery staple"); !errors.Is(err, ErrCheckpointExportUnsupported) {
		t.Fatalf("unsupported error = %v", err)
	}
	if _, err := SealCheckpointExport(exported, "", strings.NewReader("random")); !errors.Is(err, ErrCheckpointExportInvalid) {
		t.Fatalf("empty passphrase error = %v", err)
	}
}
