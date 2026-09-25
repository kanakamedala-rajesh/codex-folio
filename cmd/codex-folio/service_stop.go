package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"venkatasudha.com/codex-folio/internal/platform"
)

func runServiceStop(paths platform.Paths, options serviceOptions, input io.Reader, stdout, stderr io.Writer) int {
	status, err := platform.Discover(paths, platform.OwnerOptions{})
	if err != nil {
		return writeServiceError(stderr, err)
	}
	if !status.Running {
		if options.json {
			_ = writeServiceJSON(stdout, serviceOutput{Status: "stopped", ServiceState: "stopped"})
		} else {
			_, _ = io.WriteString(stdout, "companion already stopped\n")
		}
		return exitSuccess
	}
	connection, err := platform.DiscoverServiceClient(paths, platform.OwnerOptions{})
	if err != nil {
		return writeServiceError(stderr, err)
	}
	client, err := newServiceCommandClient(connection)
	if err != nil {
		return writeServiceError(stderr, err)
	}
	action := "request"
	if options.cancelStop {
		action = "cancel"
	} else if options.wait {
		action = "defer"
	}
	result, err := client.Stop(context.Background(), action)
	if err != nil {
		return writeServiceError(stderr, err)
	}
	if action == "request" && result.ActiveLaunches > 0 && result.State == "running" && !options.json {
		_, _ = fmt.Fprintf(stdout, "%d Managed Launch(es) still active. Stop after they finish? [y/N]: ", result.ActiveLaunches)
		if input != nil {
			line, _ := bufio.NewReader(input).ReadString('\n')
			if strings.EqualFold(strings.TrimSpace(line), "y") || strings.EqualFold(strings.TrimSpace(line), "yes") {
				result, err = client.Stop(context.Background(), "defer")
				if err != nil {
					return writeServiceError(stderr, err)
				}
			}
		}
	}
	if options.json {
		_, err = fmt.Fprintf(stdout, "{\"state\":%q,\"active_launches\":%d}\n", result.State, result.ActiveLaunches)
	} else {
		switch result.State {
		case "stopping":
			_, err = io.WriteString(stdout, "companion stopping; Codex processes are untouched\n")
		case "pending":
			_, err = io.WriteString(stdout, "companion stop pending until Managed Launches finish; use 'service stop --cancel' to cancel\n")
		default:
			_, err = io.WriteString(stdout, "companion remains running; Codex processes are untouched\n")
		}
	}
	if err != nil {
		return writeServiceError(stderr, err)
	}
	return exitSuccess
}
