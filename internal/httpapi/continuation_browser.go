package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/continuation"
	"venkatasudha.com/codex-folio/internal/diagnostics"
	"venkatasudha.com/codex-folio/internal/profile"
	"venkatasudha.com/codex-folio/internal/usage"
)

type browserCheckpointService interface {
	CaptureProject(context.Context, string, continuation.CaptureRequest) (continuation.Checkpoint, error)
	Show(context.Context, string) (continuation.Checkpoint, error)
	Edit(context.Context, string, continuation.EditRequest) (continuation.Checkpoint, error)
	Approve(context.Context, string, string) (continuation.Checkpoint, error)
	Source(context.Context, string) (continuation.SourceLaunch, error)
}

func (server *Server) browserHandoff(response http.ResponseWriter, request *http.Request) {
	service, ok := server.checkpoints.(browserCheckpointService)
	if !ok {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	if request.Method != http.MethodPost {
		server.writeMethodError(response, http.MethodPost)
		return
	}
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || request.ContentLength > maxCheckpointBodySize {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ContinuationCheckpointInvalid)
		return
	}
	var input HandoffRequest
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxCheckpointBodySize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ContinuationCheckpointInvalid)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ContinuationCheckpointInvalid)
		return
	}

	var checkpoint continuation.Checkpoint
	switch input.Action {
	case "capture":
		if input.ProjectId == nil || strings.TrimSpace(*input.ProjectId) == "" || input.Fields == nil || input.CheckpointId != nil || input.Revision != nil {
			err = continuation.ErrCheckpointInvalid
			break
		}
		checkpoint, err = service.CaptureProject(request.Context(), *input.ProjectId, captureFromHandoff(input))
	case "show":
		if input.CheckpointId == nil || strings.TrimSpace(*input.CheckpointId) == "" || input.ProjectId != nil || input.Fields != nil || input.Revision != nil || input.RedactPaths != nil || input.RedactText != nil {
			err = continuation.ErrCheckpointInvalid
			break
		}
		checkpoint, err = service.Show(request.Context(), *input.CheckpointId)
	case "edit":
		if input.CheckpointId == nil || strings.TrimSpace(*input.CheckpointId) == "" || input.Fields == nil || input.ProjectId != nil || input.Revision != nil {
			err = continuation.ErrCheckpointInvalid
			break
		}
		checkpoint, err = service.Edit(request.Context(), *input.CheckpointId, continuation.EditRequest{
			Fields: handoffFields(*input.Fields), RedactPaths: stringSlice(input.RedactPaths), RedactText: stringSlice(input.RedactText),
		})
	case "approve":
		if input.CheckpointId == nil || input.Revision == nil || strings.TrimSpace(*input.CheckpointId) == "" || strings.TrimSpace(*input.Revision) == "" || input.ProjectId != nil || input.Fields != nil || input.RedactPaths != nil || input.RedactText != nil {
			err = continuation.ErrCheckpointInvalid
			break
		}
		checkpoint, err = service.Approve(request.Context(), *input.CheckpointId, *input.Revision)
	default:
		err = continuation.ErrCheckpointInvalid
	}
	if err != nil {
		status, code := checkpointError(err)
		server.writeAPIError(response, status, diagnostics.CodeFor(err, code))
		return
	}
	result, err := server.handoffResponse(request.Context(), service, checkpoint, input.TargetAlias)
	if err != nil {
		status, code := checkpointError(err)
		server.writeAPIError(response, status, diagnostics.CodeFor(err, code))
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func captureFromHandoff(input HandoffRequest) continuation.CaptureRequest {
	request := continuation.CaptureRequest{RedactPaths: stringSlice(input.RedactPaths), RedactText: stringSlice(input.RedactText)}
	if input.Fields == nil {
		return request
	}
	request.Goal = input.Fields.Goal
	request.CompletedWork = input.Fields.CompletedWork
	request.PendingWork = input.Fields.PendingWork
	request.Risks = input.Fields.Risks
	request.NextAction = input.Fields.NextAction
	if strings.TrimSpace(input.Fields.KnownValidation) != "" {
		request.Validation = &continuation.ValidationEvidence{Command: input.Fields.KnownValidation, Source: continuation.ProvenanceUserConfirmed, Freshness: continuation.FreshnessUnknown}
	}
	return request
}

func handoffFields(fields HandoffFields) continuation.CheckpointFields {
	result := continuation.CheckpointFields{
		Goal: continuation.Evidence[string]{Value: fields.Goal}, CompletedWork: continuation.Evidence[string]{Value: fields.CompletedWork},
		PendingWork: continuation.Evidence[string]{Value: fields.PendingWork}, Risks: continuation.Evidence[string]{Value: fields.Risks},
		NextAction: continuation.Evidence[string]{Value: fields.NextAction},
	}
	if strings.TrimSpace(fields.KnownValidation) != "" {
		result.Validation.Value = []continuation.ValidationEvidence{{Command: fields.KnownValidation, Source: continuation.ProvenanceUserConfirmed, Freshness: continuation.FreshnessUnknown}}
	}
	return result
}

func stringSlice(values *[]string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), (*values)...)
}

func (server *Server) handoffResponse(ctx context.Context, service browserCheckpointService, checkpoint continuation.Checkpoint, targetAlias string) (HandoffResponse, error) {
	source, err := service.Source(ctx, checkpoint.Project.ID)
	if err != nil {
		return HandoffResponse{}, err
	}
	targetID, sourceAlias, targetEligible := "", "", false
	if server.profiles != nil {
		inventory, inventoryErr := server.profiles.Inventory(ctx)
		if inventoryErr != nil {
			return HandoffResponse{}, inventoryErr
		}
		for _, item := range inventory.Profiles {
			if item.ID == source.ProfileID {
				sourceAlias = item.Alias
			}
			if strings.EqualFold(item.Alias, targetAlias) {
				targetAlias, targetID = item.Alias, item.ID
				targetEligible = item.Status == profile.StatusReady && item.IdentityHomeID != ""
			}
		}
		if targetID == "" {
			return HandoffResponse{}, continuation.ErrHandoffNotReady
		}
	}
	if source.ProfileID != "" && source.ProfileID == targetID {
		targetEligible = false
	}
	caution := "Eligibility is checked again in the terminal before launch."
	if server.usage != nil {
		if view, _, viewErr := server.usage.View(ctx, usage.ScopeCombinedIdentity); viewErr == nil {
			for _, candidate := range view.Candidates {
				if candidate.ProfileID == targetID {
					targetEligible = targetEligible && candidate.Eligible
					caution = "Capacity evidence: " + candidate.CapacityState + ". Eligibility is checked again before launch."
				}
			}
		} else {
			caution = "Capacity evidence is unavailable and did not block this otherwise eligible handoff. Eligibility is checked again before launch."
		}
	}
	result := projectHandoffCheckpoint(checkpoint)
	result.SourceProfileId, result.SourceAlias, result.SourceState = source.ProfileID, sourceAlias, string(source.State)
	result.TargetProfileId, result.TargetAlias, result.TargetEligible, result.TargetCaution = targetID, targetAlias, targetEligible, caution
	if checkpoint.Status == continuation.StatusApproved && targetEligible && source.State == continuation.SourceExited {
		result.TerminalCommand = "codex-folio handoff " + terminalArgument(targetAlias) + " --checkpoint " + terminalArgument(checkpoint.ID) + " --revision " + terminalArgument(checkpoint.Revision)
	}
	return result, nil
}

func projectHandoffCheckpoint(checkpoint continuation.Checkpoint) HandoffResponse {
	expires := ""
	if checkpoint.ExpiresAt != nil {
		expires = checkpoint.ExpiresAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	validations := make([]HandoffValidationEvidence, 0, len(checkpoint.Fields.Validation.Value))
	for _, value := range checkpoint.Fields.Validation.Value {
		var timestamp *string
		if value.Timestamp != nil {
			formatted := value.Timestamp.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
			timestamp = &formatted
		}
		var exitStatus *int64
		if value.ExitStatus != nil {
			status := int64(*value.ExitStatus)
			exitStatus = &status
		}
		validations = append(validations, HandoffValidationEvidence{Command: value.Command, Timestamp: timestamp, ExitStatus: exitStatus, Source: value.Source, Freshness: value.Freshness})
	}
	return HandoffResponse{
		CheckpointId: checkpoint.ID, Status: checkpoint.Status, Revision: checkpoint.Revision, Source: checkpoint.Source,
		ProjectId: checkpoint.Project.ID, ProjectAlias: checkpoint.Project.Alias, ProjectBasename: checkpoint.Project.Basename,
		Fields: HandoffCheckpointFields{
			Goal: fieldEvidence(checkpoint.Fields.Goal), CompletedWork: fieldEvidence(checkpoint.Fields.CompletedWork),
			PendingWork: fieldEvidence(checkpoint.Fields.PendingWork), KnownValidation: validations,
			ValidationProvenance: checkpoint.Fields.Validation.Provenance, ValidationCompleteness: checkpoint.Fields.Validation.Completeness,
			Risks: fieldEvidence(checkpoint.Fields.Risks), NextAction: fieldEvidence(checkpoint.Fields.NextAction),
		},
		Repository: HandoffRepository{
			Branch: checkpoint.Repository.Branch.Value, Head: checkpoint.Repository.Head.Value,
			Staged: browserStrings(checkpoint.Repository.Staged.Value), Modified: browserStrings(checkpoint.Repository.Modified.Value), Untracked: browserStrings(checkpoint.Repository.Untracked.Value),
			FilesChanged: int64(checkpoint.Repository.Diff.Value.FilesChanged), Insertions: int64(checkpoint.Repository.Diff.Value.Insertions),
			Deletions: int64(checkpoint.Repository.Diff.Value.Deletions), BinaryFiles: int64(checkpoint.Repository.Diff.Value.BinaryFiles),
			Provenance: checkpoint.Repository.Diff.Provenance, Completeness: checkpoint.Repository.Diff.Completeness,
		},
		Retention: checkpoint.Retention, CreatedAt: checkpoint.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		ExpiresAt: expires, SizeBytes: int64(checkpoint.SizeBytes), TerminalCommand: "",
	}
}

func browserStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func fieldEvidence(value continuation.Evidence[string]) HandoffFieldEvidence {
	return HandoffFieldEvidence{Value: value.Value, Provenance: value.Provenance, Completeness: value.Completeness}
}
