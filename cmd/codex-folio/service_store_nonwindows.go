//go:build !windows && !darwin

package main

import (
	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
)

func openServiceStore(paths platform.Paths) (*store.Store, error) {
	return store.Open(paths.DatabaseFile)
}
