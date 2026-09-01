//go:build darwin && !cgo

package platform

func newSystemKeychainBackend() keychainBackend {
	return systemKeychainBackend{}
}

func (systemKeychainBackend) find(_, _ string) ([]byte, error) {
	return nil, ErrKeychainUnavailable
}

func (systemKeychainBackend) add(_, _ string, _ []byte) error {
	return ErrKeychainUnavailable
}

func (systemKeychainBackend) delete(_, _ string) error {
	return ErrKeychainUnavailable
}
