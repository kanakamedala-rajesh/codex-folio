//go:build windows

package main

import (
	"os"

	"venkatasudha.com/codex-folio/internal/platform"
)

func main() {
	if err := platform.RunWSLDPAPIHelper(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}
