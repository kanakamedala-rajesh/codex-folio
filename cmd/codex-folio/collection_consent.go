package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"golang.org/x/term"

	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
)

func offerCollectionConsent(paths platform.Paths, input io.Reader, stdout, stderr io.Writer) int {
	connection, err := platform.DiscoverServiceClient(paths, platform.OwnerOptions{})
	if err != nil {
		return continueAfterCollectionConsentError(stderr, err)
	}
	client, err := newServiceCommandClient(connection)
	if err != nil {
		return continueAfterCollectionConsentError(stderr, err)
	}
	promptInput, interactive := input.(*foregroundPromptInput)
	interactive = interactive && promptInput.terminal != nil && term.IsTerminal(int(promptInput.terminal.Fd()))
	return offerCollectionConsentWithClient(client, input, stdout, stderr, interactive)
}

func offerCollectionConsentWithClient(client *httpapi.CommandClient, input io.Reader, stdout, stderr io.Writer, interactive bool) int {
	current, err := client.CollectionSettings(context.Background())
	if err != nil {
		return continueAfterCollectionConsentError(stderr, err)
	}
	if current.Consent != "undecided" {
		return exitSuccess
	}
	if !interactive {
		return exitSuccess
	}
	_, _ = io.WriteString(stdout, "Optional background collection reads supported Codex usage metadata while this companion is running. It stores bounded local snapshots, not conversation text or credentials. Declining keeps launches and on-demand dashboard refresh available. OS-login enrollment is a separate choice. Enable periodic collection? [y/N]: ")
	answer, err := readCompanionSetupLine(input)
	if err == io.EOF {
		_, _ = io.WriteString(stdout, "Collection choice deferred; on-demand use remains available.\n")
		return exitSuccess
	}
	if err != nil {
		return continueAfterCollectionConsentError(stderr, err)
	}
	choice := "declined"
	if strings.EqualFold(strings.TrimSpace(answer), "y") || strings.EqualFold(strings.TrimSpace(answer), "yes") {
		choice = "accepted"
	} else if answer != "" && !strings.EqualFold(strings.TrimSpace(answer), "n") && !strings.EqualFold(strings.TrimSpace(answer), "no") {
		_, _ = io.WriteString(stdout, "Collection choice deferred; on-demand use remains available.\n")
		return exitSuccess
	}
	_, err = client.SetCollectionSettings(context.Background(), httpapi.CollectionSettingsRequest{
		ActiveIntervalSeconds: current.ActiveIntervalSeconds,
		IdleIntervalSeconds:   current.IdleIntervalSeconds,
		Consent:               &choice,
	})
	if err != nil {
		return continueAfterCollectionConsentError(stderr, err)
	}
	_, _ = fmt.Fprintf(stdout, "Background collection %s. You can change this in Dashboard Settings.\n", choice)
	return exitSuccess
}

func continueAfterCollectionConsentError(stderr io.Writer, err error) int {
	_, _ = io.WriteString(stderr, "Background collection setup could not complete; continuing to profile selection.\n")
	_ = writeServiceError(stderr, err)
	return exitSuccess
}
