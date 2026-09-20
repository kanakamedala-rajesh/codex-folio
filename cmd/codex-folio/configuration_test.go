package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/configbundle"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
)

func TestConfigurationCLIUsesRunningCommandServiceAndCreatesExclusivePrivateExport(t *testing.T) {
	paths := launchTestPaths(t)
	owner, err := platform.Acquire(paths, platform.OwnerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(paths.DatabaseFile)
	if err != nil {
		t.Fatal(err)
	}
	service, err := configbundle.NewService(state)
	if err != nil {
		t.Fatal(err)
	}
	const token = "configuration-running-owner-token"
	server, err := httpapi.NewServer(httpapi.Options{ConfigurationBundles: service, CommandToken: token})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatal(err)
	}
	if err = owner.PublishClient(platform.ServiceClient{Origin: server.Origin(), Token: token}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close(); _ = state.Close(); _ = owner.Close() })
	destination := filepath.Join(t.TempDir(), "portable.json")
	var stdout, stderr bytes.Buffer
	code := runConfiguration([]string{"export", "--json"}, &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil })
	if code != exitSuccess || stderr.Len() != 0 {
		t.Fatalf("preview=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var preview httpapi.ConfigurationBundleResponse
	if err := json.Unmarshal(stdout.Bytes(), &preview); err != nil || preview.Preview == nil {
		t.Fatalf("preview=%#v err=%v", preview, err)
	}
	stdout.Reset()
	stderr.Reset()
	code = runConfiguration([]string{"export", "--write", "--output", destination, "--confirmation-digest", strings.Repeat("0", 64), "--json"}, &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil })
	if code == exitSuccess || stdout.Len() != 0 {
		t.Fatalf("stale confirmation=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = runConfiguration([]string{"export", "--write", "--output", destination, "--confirmation-digest", preview.Preview.ConfirmationDigest}, &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil })
	if code != exitSuccess || stderr.Len() != 0 {
		t.Fatalf("export=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	source, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := configbundle.Decode(source)
	if err != nil || bundle.SchemaVersion != 1 {
		t.Fatalf("bundle=%#v err=%v", bundle, err)
	}
	stdout.Reset()
	stderr.Reset()
	if code = runConfiguration([]string{"export", "--write", "--output", destination, "--confirmation-digest", preview.Preview.ConfirmationDigest, "--json"}, &stdout, &stderr, func(*string) (platform.Paths, error) { return paths, nil }); code == exitSuccess || stdout.Len() != 0 {
		t.Fatalf("existing destination=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestConfigurationImportApplyRequiresExactReview(t *testing.T) {
	if _, err := parseConfigurationOptions([]string{"import", "--input", "bundle.json", "--apply"}); err == nil {
		t.Fatal("apply accepted without review and digest")
	}
	if _, err := parseConfigurationOptions([]string{"import", "--input", "bundle.json", "--reviewed"}); err == nil {
		t.Fatal("preview accepted review flag")
	}
	options, err := parseConfigurationOptions([]string{"import", "--input", "bundle.json", "--apply", "--reviewed", "--confirmation-digest", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "--resolve", "profile:work=keep_local"})
	if err != nil || options.resolutions["profile:work"] != "keep_local" {
		t.Fatalf("options=%#v err=%v", options, err)
	}
}
