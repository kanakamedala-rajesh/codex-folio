package platform

import (
	"os"
	"strings"
)

const wslKernelReleasePath = "/proc/sys/kernel/osrelease"

// IsWSL2 reports whether the running Linux kernel identifies itself as WSL2.
// It deliberately does not rely on Windows drives or an inherited Windows PATH.
func IsWSL2() bool {
	data, err := os.ReadFile(wslKernelReleasePath)
	return err == nil && isWSL2KernelRelease(string(data))
}

func isWSL2KernelRelease(release string) bool {
	release = strings.ToLower(strings.TrimSpace(release))
	return strings.Contains(release, "microsoft") && strings.Contains(release, "wsl2")
}
