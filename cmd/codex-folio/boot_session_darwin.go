//go:build darwin

package main

import (
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
)

func (foregroundProcessInspector) BootSessionID() (string, error) {
	value, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return "", err
	}
	bootSessionID := strings.TrimSpace(value)
	if bootSessionID == "" {
		return "", fmt.Errorf("system boot session is unavailable")
	}
	return bootSessionID, nil
}
