package main

import (
	"context"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/usage"
)

type consentScheduleFixture struct {
	choice      usage.CollectionConsent
	targetReads int
	target      bool
	onTargets   func()
}

func (fixture *consentScheduleFixture) CollectionSettings(context.Context) (usage.CollectionSettings, error) {
	return usage.DefaultCollectionSettings(), nil
}

func (fixture *consentScheduleFixture) CollectionConsent(context.Context) (usage.CollectionConsent, error) {
	return fixture.choice, nil
}

func (fixture *consentScheduleFixture) CollectionScheduleTargets(context.Context) ([]usage.ScheduleTarget, error) {
	fixture.targetReads++
	if fixture.onTargets != nil {
		fixture.onTargets()
	}
	if fixture.target {
		return []usage.ScheduleTarget{{Profile: usage.ProfileTarget{ID: "work", Alias: "Work"}, Active: true}}, nil
	}
	return nil, nil
}

func TestCollectionDeclineBetweenTargetReadAndRefreshStartsNoSource(t *testing.T) {
	fixture := &consentScheduleFixture{choice: usage.CollectionConsentAccepted, target: true}
	fixture.onTargets = func() { fixture.choice = usage.CollectionConsentDeclined }
	scheduler, err := usage.NewScheduler(collectionConsentStore{ScheduleStore: fixture, consentReader: fixture}, collectionConsentRefresher{reader: fixture, refresher: unusedConsentRefresher{}}, fixedConsentClock{at: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scheduler.Tick(context.Background())
	if err != nil || result.Collected != 0 || fixture.targetReads != 1 {
		t.Fatalf("declined transition tick = %#v, target reads=%d, err=%v", result, fixture.targetReads, err)
	}
}

func (fixture *consentScheduleFixture) SaveCollectionScheduleState(context.Context, string, usage.ScheduleState) error {
	return nil
}

type fixedConsentClock struct{ at time.Time }

func (clock fixedConsentClock) Now() time.Time { return clock.at }

type unusedConsentRefresher struct{}

func (unusedConsentRefresher) Refresh(context.Context, string, string) (usage.Snapshot, error) {
	panic("collection reached a source without a target")
}

func TestCollectionTickReadsNoTargetsUntilExplicitAcceptance(t *testing.T) {
	fixture := &consentScheduleFixture{choice: usage.CollectionConsentUndecided}
	scheduler, err := usage.NewScheduler(collectionConsentStore{ScheduleStore: fixture, consentReader: fixture}, unusedConsentRefresher{}, fixedConsentClock{at: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, choice := range []usage.CollectionConsent{usage.CollectionConsentUndecided, usage.CollectionConsentDeclined} {
		fixture.choice = choice
		if _, err := scheduler.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		if fixture.targetReads != 0 {
			t.Fatalf("choice %s read %d collection targets", choice, fixture.targetReads)
		}
	}
	fixture.choice = usage.CollectionConsentAccepted
	if _, err := scheduler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fixture.targetReads != 1 {
		t.Fatalf("accepted choice read %d collection targets, want one", fixture.targetReads)
	}
	fixture.choice = usage.CollectionConsentDeclined
	if _, err := scheduler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fixture.targetReads != 1 {
		t.Fatalf("decline after acceptance read %d collection targets", fixture.targetReads)
	}
}
