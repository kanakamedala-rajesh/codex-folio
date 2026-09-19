package platform

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"venkatasudha.com/codex-folio/internal/alerts"
)

type recordingNotificationRunner struct {
	available map[string]bool
	name      string
	arguments []string
	err       error
}

func (runner *recordingNotificationRunner) Available(name string) bool {
	return runner.available[name]
}

func (runner *recordingNotificationRunner) Run(_ context.Context, name string, arguments ...string) error {
	runner.name = name
	runner.arguments = append([]string(nil), arguments...)
	return runner.err
}

func TestNotificationAdapterUsesNativeCommandsWithoutInterpolatingContent(t *testing.T) {
	notification := alerts.Notification{Title: `Title " $(unsafe)`, Body: `Body '; rm -rf example`}
	tests := []struct {
		name        string
		platform    Platform
		command     string
		environment map[string]string
		wantPrefix  []string
	}{
		{name: "linux", platform: PlatformLinux, command: "notify-send", environment: map[string]string{"DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus"}, wantPrefix: []string{"--app-name=CodexFolio", "--urgency=normal", "--expire-time=10000", "--"}},
		{name: "macos", platform: PlatformDarwin, command: "osascript", wantPrefix: []string{"-e", darwinNotificationScript}},
		{name: "windows", platform: PlatformWindows, command: "powershell.exe", wantPrefix: []string{"-NoProfile", "-NonInteractive", "-Command", windowsNotificationScript}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &recordingNotificationRunner{available: map[string]bool{test.command: true}}
			adapter, err := NewNotificationAdapter(NotificationOptions{Platform: test.platform, Runner: runner, Environment: test.environment})
			if err != nil {
				t.Fatal(err)
			}
			if health := adapter.Health(context.Background()); !health.Available || health.Mechanism == "" {
				t.Fatalf("Health() = %#v", health)
			}
			if err := adapter.Deliver(context.Background(), notification); err != nil {
				t.Fatal(err)
			}
			want := append(append([]string(nil), test.wantPrefix...), notification.Title, notification.Body)
			if runner.name != test.command || !reflect.DeepEqual(runner.arguments, want) {
				t.Fatalf("command = %q %#v, want %q %#v", runner.name, runner.arguments, test.command, want)
			}
		})
	}
}

func TestNotificationAdapterReportsUnavailableSessionsAndDeliveryFailures(t *testing.T) {
	missing := &recordingNotificationRunner{available: map[string]bool{}}
	linux, err := NewNotificationAdapter(NotificationOptions{Platform: PlatformLinux, Runner: missing, Environment: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if health := linux.Health(context.Background()); health.Available || health.Mechanism != linuxNotificationMechanism {
		t.Fatalf("missing command health = %#v", health)
	}

	headless := &recordingNotificationRunner{available: map[string]bool{"notify-send": true}}
	linux, _ = NewNotificationAdapter(NotificationOptions{Platform: PlatformLinux, Runner: headless, Environment: map[string]string{}})
	if health := linux.Health(context.Background()); health.Available {
		t.Fatalf("headless health = %#v", health)
	}

	injected := errors.New("native notification rejected")
	failing := &recordingNotificationRunner{available: map[string]bool{"osascript": true}, err: injected}
	mac, _ := NewNotificationAdapter(NotificationOptions{Platform: PlatformDarwin, Runner: failing})
	if err := mac.Deliver(context.Background(), alerts.Notification{Title: "CodexFolio", Body: "Review an alert"}); !errors.Is(err, injected) {
		t.Fatalf("Deliver() error = %v", err)
	}
}
