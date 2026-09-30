//go:build linux

package main

import "venkatasudha.com/codex-folio/internal/platform"

func commitNativeVaultMigration(platform.Paths, platform.VaultMode) error { return nil }

func restoreNativeVaultMigration(platform.Paths) error { return nil }
