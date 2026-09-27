//go:build windows

package main

import (
	"fmt"
	"testing"
)

func TestDashboardPortAvoidsWindowsDynamicRange(t *testing.T) {
	for index := range 1000 {
		port := dashboardPort(fmt.Sprintf(`C:\Users\Example\AppData\Local\codex-folio-%d`, index))
		if port < 20000 || port >= 40000 {
			t.Fatalf("dashboardPort() = %d, want 20000 <= port < 40000", port)
		}
	}
}
