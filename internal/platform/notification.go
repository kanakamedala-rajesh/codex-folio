package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"venkatasudha.com/codex-folio/internal/alerts"
)

const (
	linuxNotificationMechanism   = "freedesktop-notify"
	darwinNotificationMechanism  = "notification-center"
	windowsNotificationMechanism = "windows-toast"

	darwinNotificationScript = `on run argv
display notification (item 2 of argv) with title (item 1 of argv)
end run`
	windowsNotificationScript = `$title=$args[0]; $body=$args[1]; [Windows.UI.Notifications.ToastNotificationManager,Windows.UI.Notifications,ContentType=WindowsRuntime] > $null; [Windows.UI.Notifications.ToastNotification,Windows.UI.Notifications,ContentType=WindowsRuntime] > $null; $xml=[Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02); $nodes=$xml.GetElementsByTagName('text'); $nodes.Item(0).AppendChild($xml.CreateTextNode($title)) > $null; $nodes.Item(1).AppendChild($xml.CreateTextNode($body)) > $null; $toast=[Windows.UI.Notifications.ToastNotification]::new($xml); [Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('CodexFolio').Show($toast)`
)

// NotificationRunner is the bounded native-process seam for user-session
// notifications. Every command is invoked directly with an argument slice;
// notification content is never interpolated into shell or script source.
type NotificationRunner interface {
	Available(string) bool
	Run(context.Context, string, ...string) error
}

type nativeNotificationRunner struct{}

func (nativeNotificationRunner) Available(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func (nativeNotificationRunner) Run(ctx context.Context, name string, arguments ...string) error {
	command := exec.CommandContext(ctx, name, arguments...)
	return command.Run()
}

type NotificationOptions struct {
	Platform    Platform
	Runner      NotificationRunner
	Environment map[string]string
}

type NotificationAdapter struct {
	platform    Platform
	runner      NotificationRunner
	environment map[string]string
}

func NewNotificationAdapter(options NotificationOptions) (*NotificationAdapter, error) {
	platform := options.Platform
	if platform == "" {
		platform = Platform(runtime.GOOS)
	}
	if platform != PlatformLinux && platform != PlatformDarwin && platform != PlatformWindows {
		return nil, fmt.Errorf("unsupported notification platform %q", platform)
	}
	runner := options.Runner
	if runner == nil {
		runner = nativeNotificationRunner{}
	}
	return &NotificationAdapter{platform: platform, runner: runner, environment: options.Environment}, nil
}

func (adapter *NotificationAdapter) Health(context.Context) alerts.AdapterHealth {
	if adapter == nil || adapter.runner == nil {
		return alerts.AdapterHealth{Detail: "Native notification delivery is unavailable. Alerts remain available in CodexFolio."}
	}
	mechanism, command := adapter.mechanismAndCommand()
	health := alerts.AdapterHealth{Mechanism: mechanism, Detail: "Native notification delivery is available for this enrolled user session."}
	if !adapter.runner.Available(command) {
		health.Detail = "The native notification facility is unavailable. Alerts remain available in CodexFolio."
		return health
	}
	if adapter.platform == PlatformLinux && adapter.environmentValue("DBUS_SESSION_BUS_ADDRESS") == "" && adapter.environmentValue("DISPLAY") == "" && adapter.environmentValue("WAYLAND_DISPLAY") == "" {
		health.Detail = "No Linux desktop notification session is available. Alerts remain available in CodexFolio."
		return health
	}
	health.Available = true
	return health
}

func (adapter *NotificationAdapter) Deliver(ctx context.Context, notification alerts.Notification) error {
	if adapter == nil || strings.TrimSpace(notification.Title) == "" || strings.TrimSpace(notification.Body) == "" {
		return errors.New("notification content is invalid")
	}
	health := adapter.Health(ctx)
	if !health.Available {
		return errors.New("native notification facility is unavailable")
	}
	name := ""
	arguments := []string{}
	switch adapter.platform {
	case PlatformLinux:
		name = "notify-send"
		arguments = []string{"--app-name=CodexFolio", "--urgency=normal", "--expire-time=10000", "--", notification.Title, notification.Body}
	case PlatformDarwin:
		name = "osascript"
		arguments = []string{"-e", darwinNotificationScript, notification.Title, notification.Body}
	case PlatformWindows:
		name = "powershell.exe"
		arguments = []string{"-NoProfile", "-NonInteractive", "-Command", windowsNotificationScript, notification.Title, notification.Body}
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return adapter.runner.Run(bounded, name, arguments...)
}

func (adapter *NotificationAdapter) mechanismAndCommand() (string, string) {
	switch adapter.platform {
	case PlatformWindows:
		return windowsNotificationMechanism, "powershell.exe"
	case PlatformDarwin:
		return darwinNotificationMechanism, "osascript"
	default:
		return linuxNotificationMechanism, "notify-send"
	}
}

func (adapter *NotificationAdapter) environmentValue(name string) string {
	if adapter.environment == nil {
		return os.Getenv(name)
	}
	return adapter.environment[name]
}

var _ alerts.NotificationAdapter = (*NotificationAdapter)(nil)
