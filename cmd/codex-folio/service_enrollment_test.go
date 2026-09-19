package main

import (
	"bytes"
	"encoding/json"
	"net/url"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"venkatasudha.com/codex-folio/internal/buildinfo"
	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
)

type fakeNativeServiceEnrollment struct {
	status          platform.ServiceEnrollmentStatus
	installResult   platform.ServiceEnrollmentResult
	uninstallResult platform.ServiceEnrollmentResult
}

func (fake *fakeNativeServiceEnrollment) Mechanism() string { return fake.status.Mechanism }
func (fake *fakeNativeServiceEnrollment) Status() (platform.ServiceEnrollmentStatus, error) {
	return fake.status, nil
}
func (fake *fakeNativeServiceEnrollment) Install() (platform.ServiceEnrollmentResult, error) {
	return fake.installResult, nil
}
func (fake *fakeNativeServiceEnrollment) Uninstall() (platform.ServiceEnrollmentResult, error) {
	return fake.uninstallResult, nil
}

func TestServiceInstallAndUninstallReportNativeEnrollmentWithoutChangingState(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	paths := platform.Paths{Root: root, Runtime: filepath.Join(root, "runtime")}
	resolve := func(*string) (platform.Paths, error) { return paths, nil }
	fake := &fakeNativeServiceEnrollment{
		status:          platform.ServiceEnrollmentStatus{Mechanism: "systemd-user", State: platform.EnrollmentActive, Available: true, Installed: true, Active: true},
		installResult:   platform.ServiceEnrollmentResult{ServiceEnrollmentStatus: platform.ServiceEnrollmentStatus{Mechanism: "systemd-user", State: platform.EnrollmentActive, Available: true, Installed: true, Active: true}, Changed: true},
		uninstallResult: platform.ServiceEnrollmentResult{ServiceEnrollmentStatus: platform.ServiceEnrollmentStatus{Mechanism: "systemd-user", State: platform.EnrollmentNotInstalled, Available: true}, Changed: true},
	}
	factory := func(platform.Paths, serviceOptions) (nativeServiceEnrollment, error) { return fake, nil }

	for _, test := range []struct{ command, state string }{{"install", platform.EnrollmentActive}, {"uninstall", platform.EnrollmentNotInstalled}} {
		var stdout, stderr bytes.Buffer
		code := runServiceWithEnrollment([]string{test.command, "--json"}, bytes.NewReader(nil), &stdout, &stderr, resolve, nil, factory)
		if code != exitSuccess {
			t.Fatalf("%s exit = %d, stderr = %s", test.command, code, stderr.String())
		}
		var output platform.ServiceEnrollmentResult
		if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
			t.Fatalf("%s output = %q: %v", test.command, stdout.String(), err)
		}
		if output.State != test.state || output.Mechanism != "systemd-user" || !output.Changed {
			t.Fatalf("%s output = %#v", test.command, output)
		}
	}
}

func TestServiceEnrollmentCommandsRejectRecoveryOnlyCandidate(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := runServiceWithEnrollment([]string{"install", "--candidate", "backup"}, bytes.NewReader(nil), &stdout, &stderr, nil, nil, nil)
	if code != exitUsage || !bytes.Contains(stderr.Bytes(), []byte("--candidate is only valid")) {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
}

func TestEnrolledServiceStartReusesOwnerWithoutEmittingDashboardCredential(t *testing.T) {
	home := testServiceTempDir(t)
	t.Setenv("HOME", home)
	stateRoot := filepath.Join(testServiceTempDir(t), "state")
	override := stateRoot
	paths, err := platform.ResolvePaths(platform.PathOptions{
		Platform: platform.Platform(runtime.GOOS), HomeDir: home, OwnerHomeDir: home,
		Environment: map[string]string{}, StateRootOverride: &override,
	})
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	owner, err := platform.Acquire(paths, platform.OwnerOptions{ProcessID: func() int { return 7778 }})
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer func() { _ = owner.Close() }()

	server, err := httpapi.NewServer(httpapi.Options{CommandToken: "enrolled-reuse-command"})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := server.Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err := owner.PublishClient(platform.ServiceClient{Origin: server.Origin(), Token: "enrolled-reuse-command"}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()

	entered, release := make(chan struct{}), make(chan struct{})
	originalWaiter := waitForServiceOwnerRelease
	waitForServiceOwnerRelease = func(platform.Paths) bool {
		close(entered)
		<-release
		return false
	}
	t.Cleanup(func() { waitForServiceOwnerRelease = originalWaiter })

	var stdout, stderr bytes.Buffer
	result := make(chan int, 1)
	go func() {
		result <- runWithServicePathResolver(
			[]string{"service", "start", "--enrolled", "--state-root", stateRoot},
			&stdout, &stderr, buildinfo.Metadata{}, testServicePathResolver(home),
		)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("enrolled start did not wait for the foreground owner")
	}
	select {
	case code := <-result:
		t.Fatalf("enrolled start exited early with %d", code)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	code := <-result
	if code != exitSuccess || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("enrolled start = %d, stdout = %q, stderr = %q; want silent reuse", code, stdout.String(), stderr.String())
	}
	bootstrap, err := url.Parse(server.BootstrapURL())
	if err != nil || bootstrap.Query().Get("bootstrap") == "" {
		t.Fatal("enrolled reuse altered the owner's existing bootstrap credential")
	}
}

func TestEnrolledOptionIsValidOnlyForServiceStart(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"status", "install", "uninstall"} {
		var stdout, stderr bytes.Buffer
		code := runServiceWithEnrollment([]string{command, "--enrolled"}, bytes.NewReader(nil), &stdout, &stderr, nil, nil, nil)
		if code != exitUsage || !bytes.Contains(stderr.Bytes(), []byte("only valid for service start")) {
			t.Fatalf("%s exit = %d, stderr = %q", command, code, stderr.String())
		}
	}
}
