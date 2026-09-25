package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/launch"
	"venkatasudha.com/codex-folio/internal/platform"
)

func TestServiceStopDefersWithoutChangingForegroundExitAndPlainStartReusesState(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	fixture, err := startEverydayCompanionFixture(paths, secureVault)
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.Close()
	ctx := context.Background()
	plan, err := fixture.stateOwner.store.PrepareLaunch(ctx, launch.PrepareRequest{Alias: "Work", Executable: filepath.Join(paths.Root, "fake-codex"), WorkingDirectory: paths.Root})
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.stateOwner.store.MarkManagedLaunchStarted(ctx, plan.LeaseID, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	ownerDone := make(chan int, 1)
	go func() {
		ownerDone <- waitForServiceStop(fixture.owner, fixture.stateOwner, fixture.server, serviceOptions{}, &bytes.Buffer{}, &bytes.Buffer{}, false, nil)
	}()
	var output, diagnostics bytes.Buffer
	if code := runServiceStop(paths, serviceOptions{}, strings.NewReader("n\n"), &output, &diagnostics); code != exitSuccess || !strings.Contains(output.String(), "remains running") {
		t.Fatalf("refused stop = %d %q %q", code, output.String(), diagnostics.String())
	}
	select {
	case <-fixture.server.StopReady():
		t.Fatal("refused stop committed")
	default:
	}
	output.Reset()
	if code := runServiceStop(paths, serviceOptions{wait: true}, nil, &output, &diagnostics); code != exitSuccess || !strings.Contains(output.String(), "pending") {
		t.Fatalf("deferred stop = %d %q %q", code, output.String(), diagnostics.String())
	}
	select {
	case code := <-ownerDone:
		t.Fatalf("owner exited during deferred stop: %d", code)
	default:
	}
	if code := runServiceStop(paths, serviceOptions{cancelStop: true}, nil, &output, &diagnostics); code != exitSuccess {
		t.Fatalf("cancel = %d %q", code, diagnostics.String())
	}
	select {
	case <-fixture.server.StopReady():
		t.Fatal("cancelled stop committed")
	default:
	}
	if code := runServiceStop(paths, serviceOptions{wait: true}, nil, &output, &diagnostics); code != exitSuccess {
		t.Fatalf("second deferral = %d %q", code, diagnostics.String())
	}
	client := httpapi.NewCommandClient(fixture.server.Origin(), "everyday-companion-command", nil)
	if _, err := client.Launch(ctx, httpapi.CommandLaunchRequest{Action: "exited", LeaseID: plan.LeaseID, ExitStatus: 23}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fixture.server.StopReady():
	default:
		t.Fatal("completed launch did not release deferred stop")
	}
	select {
	case code := <-ownerDone:
		if code != exitSuccess {
			t.Fatalf("owner shutdown = %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owner did not complete deferred shutdown")
	}
	var restarted *everydayCompanionFixture
	starts := 0
	code, _, startupDiagnostics := runPlainCompanionTest(paths, "q\n", func(startPaths platform.Paths, _ serviceOptions) error {
		starts++
		var startErr error
		restarted, startErr = startEverydayCompanionFixture(startPaths, secureVault)
		return startErr
	})
	if code != exitSuccess || starts != 1 || restarted == nil {
		t.Fatalf("plain restart = code:%d starts:%d stderr:%q", code, starts, startupDiagnostics)
	}
	defer restarted.Close()
	record, err := restarted.stateOwner.store.GetManagedLaunch(ctx, plan.LeaseID)
	if err != nil || record.ExitStatus == nil || *record.ExitStatus != 23 {
		t.Fatalf("plain restart exit record = %+v, %v", record, err)
	}
}

func TestServiceStopIdleCompanion(t *testing.T) {
	paths := launchTestPaths(t)
	secureVault := seedReadyLaunchProfile(t, paths)
	fixture, err := startEverydayCompanionFixture(paths, secureVault)
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.Close()
	ownerDone := make(chan int, 1)
	go func() {
		ownerDone <- waitForServiceStop(fixture.owner, fixture.stateOwner, fixture.server, serviceOptions{}, &bytes.Buffer{}, &bytes.Buffer{}, false, nil)
	}()
	var output, diagnostics bytes.Buffer
	if code := runServiceStop(paths, serviceOptions{}, nil, &output, &diagnostics); code != exitSuccess || !strings.Contains(output.String(), "companion stopping") {
		t.Fatalf("idle stop = %d %q %q", code, output.String(), diagnostics.String())
	}
	select {
	case <-fixture.server.StopReady():
	default:
		t.Fatal("idle stop did not commit")
	}
	select {
	case code := <-ownerDone:
		if code != exitSuccess {
			t.Fatalf("idle owner shutdown = %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("idle owner did not shut down")
	}
}

func TestServiceStopDeferredRequestYieldsToAcceptedLaunch(t *testing.T) {
	paths := launchTestPaths(t)
	fixture, err := startEverydayCompanionFixture(paths, seedReadyLaunchProfile(t, paths))
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.Close()
	ctx := context.Background()
	client := httpapi.NewCommandClient(fixture.server.Origin(), "everyday-companion-command", nil)
	request := httpapi.CommandLaunchRequest{Action: "prepare", Alias: "Work", Executable: filepath.Join(paths.Root, "fake-codex"), WorkingDirectory: paths.Root}
	first, err := client.Launch(ctx, request)
	if err != nil || first.Plan == nil {
		t.Fatalf("first prepare = %+v, %v", first, err)
	}
	if _, err := client.Launch(ctx, httpapi.CommandLaunchRequest{Action: "started", LeaseID: first.Plan.LeaseID, ProcessID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	ownerDone := make(chan int, 1)
	go func() {
		ownerDone <- waitForServiceStop(fixture.owner, fixture.stateOwner, fixture.server, serviceOptions{}, &bytes.Buffer{}, &bytes.Buffer{}, false, nil)
	}()
	if result, err := client.Stop(ctx, "defer"); err != nil || result.State != "pending" {
		t.Fatalf("deferred stop = %+v, %v", result, err)
	}
	second, err := client.Launch(ctx, request)
	if err != nil || second.Plan == nil {
		t.Fatalf("concurrent accepted prepare = %+v, %v", second, err)
	}
	if _, err := client.Launch(ctx, httpapi.CommandLaunchRequest{Action: "started", LeaseID: second.Plan.LeaseID, ProcessID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Launch(ctx, httpapi.CommandLaunchRequest{Action: "exited", LeaseID: first.Plan.LeaseID, ExitStatus: 0}); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-ownerDone:
		t.Fatalf("accepted launch lost owner: %d", code)
	default:
	}
	if result, err := client.Stop(ctx, "status"); err != nil || result.State != "running" {
		t.Fatalf("stop after accepted launch = %+v, %v", result, err)
	}
	if result, err := client.Stop(ctx, "defer"); err != nil || result.State != "pending" {
		t.Fatalf("second deferred stop = %+v, %v", result, err)
	}
	if _, err := client.Launch(ctx, httpapi.CommandLaunchRequest{Action: "exited", LeaseID: second.Plan.LeaseID, ExitStatus: 0}); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-ownerDone:
		if code != exitSuccess {
			t.Fatalf("owner shutdown = %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owner did not stop after accepted launch finished")
	}
}
