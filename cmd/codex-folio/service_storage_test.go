package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"testing"

	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
)

func TestServiceInstallInheritsRememberedVaultMode(t *testing.T) {
	for _, mode := range []platform.VaultMode{platform.VaultModePassphrase, platform.VaultModeWSLDPAPI} {
		t.Run(string(mode), func(t *testing.T) {
			paths := launchTestPaths(t)
			if err := rememberEverydaySecureStorage(paths, mode); err != nil {
				t.Fatal(err)
			}
			var got platform.VaultMode
			factory := func(_ platform.Paths, options serviceOptions) (nativeServiceEnrollment, error) {
				got = options.vaultMode
				return &fakeNativeServiceEnrollment{}, nil
			}
			var stderr bytes.Buffer
			code := runServiceWithEnrollment([]string{"install"}, bytes.NewReader(nil), io.Discard, &stderr,
				func(*string) (platform.Paths, error) { return paths, nil }, nil, factory)
			if code != exitSuccess || got != mode {
				t.Fatalf("exit=%d mode=%q stderr=%q", code, got, stderr.String())
			}
		})
	}
}

func TestServiceStartInheritsRememberedVaultMode(t *testing.T) {
	for _, mode := range []platform.VaultMode{platform.VaultModePassphrase, platform.VaultModeWSLDPAPI} {
		t.Run(string(mode), func(t *testing.T) {
			paths := launchTestPaths(t)
			secureVault := seedReadyLaunchProfile(t, paths)
			if err := rememberEverydaySecureStorage(paths, mode); err != nil {
				t.Fatal(err)
			}
			opened := false
			open := func(paths platform.Paths, actual platform.VaultMode, _ string) (*store.Store, error) {
				opened = true
				if actual != mode {
					t.Errorf("opened mode=%q, want %q", actual, mode)
				}
				return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
			}
			wait := func(owner *platform.Owner, state interface{ Close() error }, server *httpapi.Server, options serviceOptions, _, _ io.Writer, _ bool, _ <-chan error, _ diagnostics.Sink) int {
				defer owner.Close()
				defer state.Close()
				defer server.Close()
				if options.vaultMode != mode {
					t.Errorf("service mode=%q, want %q", options.vaultMode, mode)
				}
				return exitSuccess
			}
			var stderr bytes.Buffer
			code := runServiceStartWithDependencies(paths, serviceOptions{}, io.Discard, &stderr, nil, open, wait)
			if code != exitSuccess || opened != (mode != platform.VaultModePassphrase) {
				t.Fatalf("exit=%d opened=%t stderr=%q", code, opened, stderr.String())
			}
		})
	}
}

func TestFailedServiceListenDoesNotRememberSecureStorage(t *testing.T) {
	for _, mode := range []platform.VaultMode{platform.VaultModePassphrase, platform.VaultModeWSLDPAPI} {
		t.Run(string(mode), func(t *testing.T) {
			paths := launchTestPaths(t)
			secureVault := seedReadyLaunchProfile(t, paths)
			listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", dashboardPort(paths.Root)))
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			open := func(paths platform.Paths, _ platform.VaultMode, _ string) (*store.Store, error) {
				return store.OpenWithOptions(store.Options{Path: paths.DatabaseFile, Vault: secureVault})
			}
			code := runServiceStartWithDependencies(paths, serviceOptions{vaultMode: mode}, io.Discard, io.Discard, nil, open, nil)
			if code == exitSuccess {
				t.Fatal("startup unexpectedly succeeded with occupied port")
			}
			if _, err := os.Stat(paths.SecureStorageFile); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("storage selection persisted before readiness: %v", err)
			}
		})
	}
}
