package main

import (
	"strings"
	"testing"

	"venkatasudha.com/codex-folio/internal/platform"
)

func TestNewServiceCommandClient(t *testing.T) {
	t.Run("pinned HTTPS descriptor", func(t *testing.T) {
		client, err := newServiceCommandClient(platform.ServiceClient{
			Origin:            "https://127.0.0.1:31000",
			Token:             "test-token",
			CertificateSHA256: strings.Repeat("a", 64),
		})
		if err != nil || client == nil {
			t.Fatalf("pinned client = %v, %v", client, err)
		}
	})
	t.Run("HTTPS requires certificate fingerprint", func(t *testing.T) {
		if _, err := newServiceCommandClient(platform.ServiceClient{Origin: "https://127.0.0.1:31000", Token: "test-token"}); err == nil {
			t.Fatal("HTTPS descriptor without fingerprint was accepted")
		}
	})
	t.Run("HTTP fixture", func(t *testing.T) {
		client, err := newServiceCommandClient(platform.ServiceClient{Origin: "http://127.0.0.1:31000", Token: "test-token"})
		if err != nil || client == nil {
			t.Fatalf("fixture client = %v, %v", client, err)
		}
	})
}
