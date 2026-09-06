package httpapi

import (
	"encoding/json"
	"testing"

	"venkatasudha.com/codex-folio/internal/launch"
)

func TestCommandLaunchResponseKeepsWarningOutsidePlan(t *testing.T) {
	encoded, err := json.Marshal(CommandLaunchResponse{
		Plan:    &launch.Plan{LeaseID: "lease-1"},
		Warning: "authentication metadata unavailable",
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded struct {
		Plan    map[string]any `json:"plan"`
		Warning string         `json:"warning"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.Warning != "authentication metadata unavailable" {
		t.Fatalf("warning = %q, want compatibility warning", decoded.Warning)
	}
	if _, exists := decoded.Plan["warning"]; exists {
		t.Fatalf("plan = %#v, must not carry presentation warning", decoded.Plan)
	}
}
