//go:build linux

package platform

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
)

type systemSecretServiceBackend struct{}

func newSystemSecretServiceBackend() secretServiceBackend {
	return systemSecretServiceBackend{}
}

func (systemSecretServiceBackend) lookup(ctx context.Context, attributes map[string]string) ([]byte, error) {
	args, err := secretToolAttributes("lookup", attributes)
	if err != nil {
		return nil, err
	}
	output, runErr := runSecretTool(ctx, args, nil)
	if runErr != nil {
		return nil, runErr
	}
	if len(output) == 0 {
		return nil, ErrSecretServiceItemNotFound
	}
	return parseSecretServiceRecordText(output)
}

func (systemSecretServiceBackend) store(ctx context.Context, label string, attributes map[string]string, record []byte) error {
	args, err := secretToolAttributes("store", attributes)
	if err != nil {
		return err
	}
	args = append([]string{"store", "--label=" + label}, args[1:]...)
	_, err = runSecretTool(ctx, args, []byte(secretServiceRecordText(record)))
	return err
}

func secretToolAttributes(operation string, attributes map[string]string) ([]string, error) {
	if operation != "lookup" && operation != "store" {
		return nil, ErrSecretServiceUnavailable
	}
	orderedKeys := []string{"application", "purpose"}
	args := []string{operation}
	for _, key := range orderedKeys {
		value, ok := attributes[key]
		if !ok || strings.TrimSpace(value) == "" || strings.IndexByte(value, 0) >= 0 || strings.HasPrefix(value, "-") {
			return nil, ErrSecretServiceAttributeInvalid
		}
		args = append(args, key, value)
	}
	return args, nil
}

func runSecretTool(ctx context.Context, args []string, input []byte) ([]byte, error) {
	if _, err := exec.LookPath("secret-tool"); err != nil {
		return nil, ErrSecretServiceUnavailable
	}
	ctx = contextOrBackground(ctx)
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(ErrSecretServiceUnavailable, err)
	}
	command := exec.CommandContext(ctx, "secret-tool", args...)
	command.Stdin = bytes.NewReader(input)
	var output, diagnostics bytes.Buffer
	command.Stdout = &output
	command.Stderr = &diagnostics
	if err := command.Run(); err != nil {
		return nil, classifySecretToolError(diagnostics.Bytes())
	}
	return output.Bytes(), nil
}

func classifySecretToolError(diagnostics []byte) error {
	message := strings.ToLower(string(diagnostics))
	switch {
	case strings.Contains(message, "no such secret"), strings.Contains(message, "not found"):
		return ErrSecretServiceItemNotFound
	case strings.Contains(message, "locked"), strings.Contains(message, "unlock"), strings.Contains(message, "interaction"):
		return ErrSecretServiceLocked
	case strings.Contains(message, "denied"), strings.Contains(message, "permission"):
		return ErrSecretServiceAccessDenied
	default:
		return ErrSecretServiceUnavailable
	}
}
