package platform

// VaultMode identifies the explicitly selected platform vault tier. Linux
// desktop uses Secret Service by default; passphrase mode is opt-in for WSL
// and headless sessions and is never inferred from Secret Service failure.
type VaultMode string

const (
	VaultModeSecretService VaultMode = "secret-service"
	VaultModePassphrase    VaultMode = "passphrase"
)
