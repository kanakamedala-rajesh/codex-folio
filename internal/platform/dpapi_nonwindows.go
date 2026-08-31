//go:build !windows

package platform

import (
	"context"

	"venkatasudha.com/codex-folio/internal/vault"
)

var _ vault.KeyProvider = (*DPAPIKeyProvider)(nil)

func (provider *DPAPIKeyProvider) LoadOrCreate(ctx context.Context) (vault.KeyMaterial, error) {
	if err := contextOrBackground(ctx).Err(); err != nil {
		return vault.KeyMaterial{}, unavailableDPAPI(err)
	}
	return vault.KeyMaterial{}, unavailableDPAPI(nil)
}
