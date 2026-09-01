//go:build !linux

package platform

import "context"

type systemSecretServiceBackend struct{}

func newSystemSecretServiceBackend() secretServiceBackend {
	return systemSecretServiceBackend{}
}

func (systemSecretServiceBackend) lookup(context.Context, map[string]string) ([]byte, error) {
	return nil, ErrSecretServiceUnavailable
}

func (systemSecretServiceBackend) store(context.Context, string, map[string]string, []byte) error {
	return ErrSecretServiceUnavailable
}
