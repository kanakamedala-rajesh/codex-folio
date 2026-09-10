// Package git inspects repository metadata without reading file or diff content.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"venkatasudha.com/codex-folio/internal/continuation"
)

type runCommand func(context.Context, string, ...string) ([]byte, error)

type Inspector struct{ run runCommand }

func NewInspector() *Inspector { return &Inspector{run: runGit} }

func (inspector *Inspector) Inspect(ctx context.Context, path string) (continuation.RepositoryInventory, error) {
	if inspector == nil || inspector.run == nil {
		return continuation.RepositoryInventory{}, continuation.ErrRepositoryInspection
	}
	branch, _ := inspector.run(ctx, path, "symbolic-ref", "--quiet", "--short", "HEAD")
	head, _ := inspector.run(ctx, path, "rev-parse", "--verify", "HEAD")
	status, err := inspector.run(ctx, path, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames")
	if err != nil {
		return continuation.RepositoryInventory{}, errors.Join(continuation.ErrRepositoryInspection, err)
	}
	inventory := continuation.RepositoryInventory{Branch: strings.TrimSpace(string(branch)), Head: strings.TrimSpace(string(head))}
	if err := parseStatus(status, &inventory); err != nil {
		return continuation.RepositoryInventory{}, errors.Join(continuation.ErrRepositoryInspection, err)
	}
	if _, err := inspector.run(ctx, path, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err == nil {
		divergence, divergenceErr := inspector.run(ctx, path, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
		if divergenceErr != nil {
			return continuation.RepositoryInventory{}, errors.Join(continuation.ErrRepositoryInspection, divergenceErr)
		}
		inventory.Upstream, err = parseDivergence(divergence)
		if err != nil {
			return continuation.RepositoryInventory{}, errors.Join(continuation.ErrRepositoryInspection, err)
		}
	}
	files := map[string]struct{}{}
	diffCommands := [][]string{{"diff", "--no-ext-diff", "HEAD", "--numstat", "--"}}
	if inventory.Head == "" {
		emptyTree, runErr := inspector.run(ctx, path, "hash-object", "-t", "tree", "--stdin")
		if runErr != nil || strings.TrimSpace(string(emptyTree)) == "" {
			return continuation.RepositoryInventory{}, errors.Join(continuation.ErrRepositoryInspection, runErr)
		}
		diffCommands = [][]string{{"diff", "--no-ext-diff", strings.TrimSpace(string(emptyTree)), "--numstat", "--"}}
	}
	for _, args := range diffCommands {
		output, runErr := inspector.run(ctx, path, args...)
		if runErr != nil {
			return continuation.RepositoryInventory{}, errors.Join(continuation.ErrRepositoryInspection, runErr)
		}
		if err := parseNumstat(output, files, &inventory.Diff); err != nil {
			return continuation.RepositoryInventory{}, errors.Join(continuation.ErrRepositoryInspection, err)
		}
	}
	inventory.Diff.FilesChanged = len(files)
	return inventory, nil
}

func runGit(ctx context.Context, path string, args ...string) ([]byte, error) {
	commandArgs := append([]string{"-c", "core.fsmonitor=false", "-C", path}, args...)
	command := exec.CommandContext(ctx, "git", commandArgs...)
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("git metadata command failed: %w", err)
	}
	return output, nil
}

func parseStatus(output []byte, inventory *continuation.RepositoryInventory) error {
	for _, entry := range bytes.Split(output, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		if len(entry) < 4 || entry[2] != ' ' {
			return errors.New("git status output is invalid")
		}
		path := string(entry[3:])
		switch {
		case entry[0] == '?' && entry[1] == '?':
			inventory.Untracked = append(inventory.Untracked, path)
		default:
			if entry[0] != ' ' {
				inventory.Staged = append(inventory.Staged, path)
			}
			if entry[1] != ' ' {
				inventory.Modified = append(inventory.Modified, path)
			}
		}
	}
	return nil
}

func parseDivergence(output []byte) (*continuation.UpstreamDivergence, error) {
	parts := strings.Fields(string(output))
	if len(parts) != 2 {
		return nil, errors.New("git divergence output is invalid")
	}
	ahead, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, errors.New("git divergence output is invalid")
	}
	behind, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, errors.New("git divergence output is invalid")
	}
	return &continuation.UpstreamDivergence{Ahead: ahead, Behind: behind}, nil
}

func parseNumstat(output []byte, files map[string]struct{}, statistics *continuation.DiffStatistics) error {
	for _, line := range bytes.Split(bytes.TrimSpace(output), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		parts := bytes.SplitN(line, []byte{'\t'}, 3)
		if len(parts) != 3 || len(parts[2]) == 0 {
			return errors.New("git numstat output is invalid")
		}
		files[string(parts[2])] = struct{}{}
		if string(parts[0]) == "-" && string(parts[1]) == "-" {
			statistics.BinaryFiles++
			continue
		}
		insertions, insertErr := strconv.Atoi(string(parts[0]))
		deletions, deleteErr := strconv.Atoi(string(parts[1]))
		if insertErr != nil || deleteErr != nil {
			return errors.New("git numstat output is invalid")
		}
		statistics.Insertions += insertions
		statistics.Deletions += deletions
	}
	return nil
}
