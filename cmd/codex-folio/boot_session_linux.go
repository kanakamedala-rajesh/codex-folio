//go:build linux

package main

import (
	"fmt"
	"os"
	"strings"
)

func (foregroundProcessInspector) BootSessionID() (string, error) {
	value, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	bootSessionID := strings.TrimSpace(string(value))
	if bootSessionID == "" {
		return "", fmt.Errorf("system boot session is unavailable")
	}
	return bootSessionID, nil
}
