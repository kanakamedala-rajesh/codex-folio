package configpack

import (
	"errors"
	"testing"
)

func TestNewDraftCreatesDeterministicValidatedPack(t *testing.T) {
	files := map[string]string{
		"config/base.toml":   "model = \"gpt-5\"\n",
		"guidance/AGENTS.md": "Prefer small changes.\n",
	}

	first, err := NewDraft("shared", "1", files)
	if err != nil {
		t.Fatalf("NewDraft() error = %v", err)
	}
	second, err := NewDraft("shared", "1", map[string]string{
		"guidance/AGENTS.md": "Prefer small changes.\n",
		"config/base.toml":   "model = \"gpt-5\"\n",
	})
	if err != nil {
		t.Fatalf("NewDraft() with reordered files error = %v", err)
	}
	if first.Digest == "" || first.Digest != second.Digest {
		t.Fatalf("digests = %q/%q, want equal non-empty deterministic digests", first.Digest, second.Digest)
	}
	if first.State != StateDraft || len(first.Files) != 2 {
		t.Fatalf("draft = %#v, want draft with two files", first)
	}
}

func TestPackRejectsCodexOwnedStateAndLiteralSecrets(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"authentication state": {"auth.json": "{}"},
		"sqlite state":         {"state/threads.sqlite3": "not allowed"},
		"literal token":        {"mcp/server.toml": "token = \"real-token\"\n"},
		"credential field":     {"config/base.toml": "  \"GITHUB_PAT\" = \"secret\"\n"},
		"credential table":     {"config/base.toml": "[credentials]\nvalue = \"actual-secret\"\n"},
		"markdown secret":      {"guidance/AGENTS.md": "token = real-token\n"},
		"unsupported JSON":     {"mcp/server.json": `{"token":"real-token"}`},
		"malformed TOML":       {"config/base.toml": "[models\nname = \"gpt-5\"\n"},
		"Windows path collision": {
			"config/A.toml": "model = \"gpt-5\"\n",
			"config/a.toml": "model = \"gpt-5\"\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewDraft("shared", "1", files); err == nil {
				t.Fatal("NewDraft() error = nil, want validation failure")
			}
		})
	}
	if _, err := NewDraft("shared", "1", map[string]string{"config/base.toml": "token_env = \"GITHUB_PAT\"\n"}); err != nil {
		t.Fatalf("NewDraft() secret reference error = %v, want allowed non-secret reference", err)
	}
}

func TestApprovedPackAndPromotionDoNotRewritePriorVersion(t *testing.T) {
	base, err := NewDraft("shared", "1", map[string]string{"config/base.toml": "model = \"gpt-5\"\n"})
	if err != nil {
		t.Fatalf("NewDraft() error = %v", err)
	}
	approved, err := base.Approve()
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	overrides := map[string]string{"config/base.toml": "model = \"gpt-5-mini\"\n"}
	preview, err := PreviewPromotion(approved, overrides, "2")
	if err != nil {
		t.Fatalf("PreviewPromotion() error = %v", err)
	}
	if len(preview.Changes) != 1 || preview.Changes[0].Path != "config/base.toml" || preview.Changes[0].Kind != ChangeModified {
		t.Fatalf("promotion changes = %#v, want one modified config file", preview.Changes)
	}
	next, err := Promote(approved, overrides, "2", true)
	if err != nil {
		t.Fatalf("Promote() error = %v", err)
	}
	if next.Version != "2" || next.State != StateApproved || next.Files["config/base.toml"] != overrides["config/base.toml"] {
		t.Fatalf("promoted pack = %#v, want approved version 2 with override content", next)
	}
	if approved.Version != "1" || approved.State != StateApproved || approved.Files["config/base.toml"] != "model = \"gpt-5\"\n" {
		t.Fatalf("prior pack changed after promotion: %#v", approved)
	}
}

func TestPromotionRequiresExplicitReview(t *testing.T) {
	pack, err := NewDraft("shared", "1", map[string]string{"config/base.toml": "model = \"gpt-5\"\n"})
	if err != nil {
		t.Fatalf("NewDraft() error = %v", err)
	}
	pack, err = pack.Approve()
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if _, err := Promote(pack, nil, "2", false); err == nil || !errors.Is(err, ErrPromotionReviewNeeded) {
		t.Fatalf("Promote() error = %v, want explicit review error", err)
	}
}
