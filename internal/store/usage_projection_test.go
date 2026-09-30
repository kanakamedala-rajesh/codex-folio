package store

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/usage"
)

type measuredUsageVault struct {
	Vault
	decrypts int
	delay    time.Duration
}

func (v *measuredUsageVault) Decrypt(ctx context.Context, ciphertext, aad []byte) ([]byte, error) {
	v.decrypts++
	if v.delay > 0 {
		time.Sleep(v.delay)
	}
	return v.Vault.Decrypt(ctx, ciphertext, aad)
}

func TestRecentUsageMetricProjectionAvoidsScopeDecryption(t *testing.T) {
	state, err := openProfileTestStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	targets := make([]usage.ProfileTarget, 0, 3)
	for p := 0; p < 3; p++ {
		alias := fmt.Sprintf("Profile%d", p)
		addReadyProfile(t, state, fmt.Sprintf("profile-%d", p), alias)
		target, err := state.ResolveUsageProfile(ctx, alias)
		if err != nil {
			t.Fatal(err)
		}
		target.LoginIdentity, target.Workspace = "synthetic-login", "synthetic-workspace"
		targets = append(targets, target)
		for i := 0; i < 14; i++ {
			at := time.Date(2026, 9, 11, 12, i, 0, 0, time.UTC)
			snapshot := usage.NewUnavailableSnapshot("0.153.4", at, usage.AvailabilityTemporarilyUnavailable, usage.ReasonCollectionFailed)
			snapshot.TriggerReason = usage.TriggerDashboardRefresh
			if i != 12 {
				snapshot.Status = usage.AvailabilityPartial
				snapshot.Availability[0].State, snapshot.Availability[0].Reason = usage.AvailabilityAvailable, ""
				snapshot.Observations = []usage.Observation{{Metric: usage.Registry()[0], Value: float64(i), ObservedAt: at, CapturedAt: at, Source: usage.SourceCodexAppServer, SourceVersion: "0.153.4", Provenance: usage.ProvenanceProvider, Freshness: usage.FreshnessFresh, Availability: usage.AvailabilityAvailable}}
			}
			if _, err := state.SaveUsageSnapshot(ctx, target, snapshot); err != nil {
				t.Fatal(err)
			}
		}
	}
	measured := &measuredUsageVault{Vault: state.vault, delay: time.Millisecond}
	state.vault = measured
	want := make([][]usage.Snapshot, len(targets))
	for index, target := range targets {
		want[index], err = state.RecentUsageSnapshots(ctx, target)
		if err != nil {
			t.Fatal(err)
		}
		if len(want[index]) != 12 || len(want[index][10].Observations) != 0 {
			t.Fatal("fixture lost bounded history or failure gap")
		}
		for capture := range want[index] {
			snapshot := &want[index][capture]
			if snapshot.LoginIdentity != target.LoginIdentity || snapshot.Workspace != target.Workspace {
				t.Fatal("full history lost identity scope")
			}
			snapshot.LoginIdentity, snapshot.Workspace = "", ""
		}
	}
	if measured.decrypts != 72 {
		t.Fatalf("full history decrypts = %d, want 72", measured.decrypts)
	}
	measured.decrypts = 0
	started := time.Now()
	for index, target := range targets {
		got, err := state.RecentUsageMetricSnapshots(ctx, target)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want[index]) {
			t.Fatal("metric projection changed normalized evidence")
		}
	}
	t.Logf("three-profile recent metrics: %d decrypts, %s", measured.decrypts, time.Since(started))
	if measured.decrypts != 0 {
		t.Fatalf("metric history performed %d scope decrypts", measured.decrypts)
	}
	started = time.Now()
	evidence, err := state.AlertEvidence(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("three-profile alerts: %d decrypts, %s", measured.decrypts, time.Since(started))
	if measured.decrypts != 0 {
		t.Fatalf("alert evidence performed %d scope decrypts", measured.decrypts)
	}
	if len(evidence) != len(targets) {
		t.Fatalf("alert evidence count = %d", len(evidence))
	}
	for index, item := range evidence {
		if !reflect.DeepEqual(item.Snapshot, want[index][11]) || item.PreviousSourceVersion != "0.153.4" || item.ConsecutiveFailures != 0 {
			t.Fatal("alert projection changed latest evidence, failure count, or compatibility evidence")
		}
	}
	latest, err := state.LatestUsageSnapshot(ctx, targets[0])
	if err != nil {
		t.Fatal(err)
	}
	if measured.decrypts != 2 || latest.LoginIdentity != targets[0].LoginIdentity || latest.Workspace != targets[0].Workspace {
		t.Fatal("latest snapshot lost authenticated identity scope used by ranking")
	}
}
