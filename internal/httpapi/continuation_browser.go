package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

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
	PreviewAssisted(context.Context, string, string, continuation.EditRequest) (continuation.Checkpoint, error)
	ApproveAssisted(context.Context, string, string, string, continuation.EditRequest) (continuation.Checkpoint, error)
	List(context.Context) ([]continuation.Checkpoint, error)
	Purge(context.Context, string, string) error
	Retention(context.Context, string, string) (continuation.RetentionPolicy, error)
	Export(context.Context, string) (continuation.CheckpointExport, error)
	Source(context.Context, string) (continuation.SourceLaunch, error)
	PrepareHistory(context.Context, string, string) (continuation.HistorySource, error)
}

// BrowserCheckpointHistory reads bounded candidate fields without exposing the
// installed executable or source Identity Home to the browser contract.
type BrowserCheckpointHistory interface {
	Candidates(context.Context, continuation.HistorySource, string) (continuation.CheckpointFields, error)
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
	if result, handled, managementErr := server.browserCheckpointManagement(request.Context(), service, input); handled {
		if managementErr != nil {
			status, code := checkpointError(managementErr)
			server.writeAPIError(response, status, diagnostics.CodeFor(managementErr, code))
			return
		}
		writeJSON(response, http.StatusOK, HandoffResult{Kind: "management", Management: &result})
		return
	}
	targetAlias := ""
	if input.TargetAlias != nil {
		targetAlias = *input.TargetAlias
	}
	if strings.TrimSpace(targetAlias) == "" {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ContinuationCheckpointInvalid)
		return
	}
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
		var current continuation.Checkpoint
		current, err = service.Show(request.Context(), *input.CheckpointId)
		if err == nil {
			var readiness HandoffResponse
			readiness, err = server.handoffResponse(request.Context(), service, current, targetAlias)
			if err == nil && (readiness.SourceState != string(continuation.SourceExited) || !readiness.TargetEligible) {
				err = continuation.ErrHandoffNotReady
			}
		}
		if err == nil {
			checkpoint, err = service.Approve(request.Context(), *input.CheckpointId, *input.Revision)
		}
	case "assist":
		if input.CheckpointId == nil || input.Revision == nil || input.ThreadId == nil || input.HistoryConsent == nil || !*input.HistoryConsent || strings.TrimSpace(*input.CheckpointId) == "" || strings.TrimSpace(*input.Revision) == "" || strings.TrimSpace(*input.ThreadId) == "" || input.ProjectId != nil || input.Fields != nil || input.PreviewRevision != nil || input.RedactPaths != nil || input.RedactText != nil || server.checkpointHistory == nil {
			err = continuation.ErrCheckpointInvalid
			break
		}
		base, showErr := service.Show(request.Context(), *input.CheckpointId)
		if showErr != nil {
			err = showErr
			break
		}
		source, sourceErr := service.PrepareHistory(request.Context(), *input.CheckpointId, *input.Revision)
		if sourceErr != nil {
			err = sourceErr
			break
		}
		candidates, historyErr := server.checkpointHistory.Candidates(request.Context(), source, *input.ThreadId)
		if historyErr != nil {
			err = historyErr
			break
		}
		candidates = continuation.RedactHistoryIdentityHome(candidates, source.IdentityHome)
		checkpoint, err = service.PreviewAssisted(request.Context(), *input.CheckpointId, *input.Revision, continuation.EditRequest{Fields: continuation.MergeHistoryCandidates(base.Fields, candidates)})
	case "preview-assisted":
		if input.CheckpointId == nil || input.Revision == nil || input.Fields == nil || strings.TrimSpace(*input.CheckpointId) == "" || strings.TrimSpace(*input.Revision) == "" || input.ProjectId != nil || input.PreviewRevision != nil || input.ThreadId != nil || input.HistoryConsent != nil {
			err = continuation.ErrCheckpointInvalid
			break
		}
		checkpoint, err = service.PreviewAssisted(request.Context(), *input.CheckpointId, *input.Revision, continuation.EditRequest{Fields: handoffFields(*input.Fields), RedactPaths: stringSlice(input.RedactPaths), RedactText: stringSlice(input.RedactText)})
	case "approve-assisted":
		if input.CheckpointId == nil || input.Revision == nil || input.PreviewRevision == nil || input.Fields == nil || strings.TrimSpace(*input.CheckpointId) == "" || strings.TrimSpace(*input.Revision) == "" || strings.TrimSpace(*input.PreviewRevision) == "" || input.ProjectId != nil || input.ThreadId != nil || input.HistoryConsent != nil {
			err = continuation.ErrCheckpointInvalid
			break
		}
		var current continuation.Checkpoint
		current, err = service.Show(request.Context(), *input.CheckpointId)
		if err == nil {
			var readiness HandoffResponse
			readiness, err = server.handoffResponse(request.Context(), service, current, targetAlias)
			if err == nil && (readiness.SourceState != string(continuation.SourceExited) || !readiness.TargetEligible) {
				err = continuation.ErrHandoffNotReady
			}
		}
		if err == nil {
			checkpoint, err = service.ApproveAssisted(request.Context(), *input.CheckpointId, *input.Revision, *input.PreviewRevision, continuation.EditRequest{Fields: handoffFields(*input.Fields), RedactPaths: stringSlice(input.RedactPaths), RedactText: stringSlice(input.RedactText)})
		}
	default:
		err = continuation.ErrCheckpointInvalid
	}
	if err != nil {
		status, code := checkpointError(err)
		server.writeAPIError(response, status, diagnostics.CodeFor(err, code))
		return
	}
	result, err := server.handoffResponse(request.Context(), service, checkpoint, targetAlias)
	if err != nil {
		status, code := checkpointError(err)
		server.writeAPIError(response, status, diagnostics.CodeFor(err, code))
		return
	}
	writeJSON(response, http.StatusOK, HandoffResult{Kind: "handoff", Handoff: &result})
}

var checkpointExportFields = []string{"format_version", "exported_at", "checkpoint", "project_alias", "project_basename", "approved_fields", "repository_metadata", "revision", "expiry"}
var checkpointExportExcluded = []string{"credentials", "identity_homes", "canonical_repository_paths", "raw_transcripts", "raw_diffs", "commands_and_tool_output", "analytics", "configuration"}

func (server *Server) browserCheckpointManagement(ctx context.Context, service browserCheckpointService, input HandoffRequest) (CheckpointManagementResponse, bool, error) {
	result := CheckpointManagementResponse{}
	switch input.Action {
	case "list":
		if !managementOnly(input) {
			return result, true, continuation.ErrCheckpointInvalid
		}
		checkpoints, err := service.List(ctx)
		if err != nil {
			return result, true, err
		}
		summaries := make([]HandoffCheckpointSummary, 0, len(checkpoints))
		for _, checkpoint := range checkpoints {
			summaries = append(summaries, checkpointSummary(checkpoint))
		}
		result.Checkpoints = &summaries
		return result, true, nil
	case "retention":
		if input.CheckpointId != nil || input.Revision != nil || input.Confirmation != nil || input.Passphrase != nil || input.PlaintextAcknowledgement != nil || input.ProjectId != nil || input.TargetAlias != nil || input.Fields != nil || input.ThreadId != nil || input.HistoryConsent != nil || input.PreviewRevision != nil || input.RedactPaths != nil || input.RedactText != nil {
			return result, true, continuation.ErrCheckpointInvalid
		}
		source, setting := "", ""
		if input.Source != nil {
			source = *input.Source
		}
		if input.Setting != nil {
			setting = *input.Setting
		}
		policy, err := service.Retention(ctx, source, setting)
		if err != nil {
			return result, true, err
		}
		result.RetentionPolicy = &HandoffRetentionPolicy{RepositoryFirst: policy.RepositoryFirst, TranscriptAssisted: policy.TranscriptAssisted}
		return result, true, nil
	case "purge-preview", "purge", "export-preview", "export-encrypted", "export-plaintext":
		return server.browserCheckpointOperation(ctx, service, input)
	default:
		return result, false, nil
	}
}

func (server *Server) browserCheckpointOperation(ctx context.Context, service browserCheckpointService, input HandoffRequest) (CheckpointManagementResponse, bool, error) {
	result := CheckpointManagementResponse{}
	if input.CheckpointId == nil || input.Revision == nil || strings.TrimSpace(*input.CheckpointId) == "" || strings.TrimSpace(*input.Revision) == "" || input.Source != nil || input.Setting != nil || input.ProjectId != nil || input.TargetAlias != nil || input.Fields != nil || input.ThreadId != nil || input.HistoryConsent != nil || input.PreviewRevision != nil || input.RedactPaths != nil || input.RedactText != nil {
		return result, true, continuation.ErrCheckpointInvalid
	}
	id, revision := *input.CheckpointId, *input.Revision
	checkpoint, err := service.Show(ctx, id)
	if err != nil {
		return result, true, err
	}
	if checkpoint.Revision != revision {
		return result, true, continuation.ErrCheckpointRevisionChanged
	}
	format := "encrypted"
	if input.Action == "export-plaintext" || input.Action == "export-preview" && input.PlaintextAcknowledgement != nil && *input.PlaintextAcknowledgement {
		format = "plaintext"
	}
	confirmation := checkpointConfirmation(input.Action, id, revision, format)
	filename := "checkpoint-" + id + ".cfolio"
	if format == "plaintext" {
		filename = "checkpoint-" + id + ".json"
	}
	preview := HandoffOperationPreview{Kind: strings.TrimSuffix(input.Action, "-preview"), Confirmation: confirmation, Filename: filename, IncludedFields: slices.Clone(checkpointExportFields), ExcludedFields: slices.Clone(checkpointExportExcluded)}
	result.OperationPreview = &preview
	if input.Action == "purge-preview" {
		if checkpoint.Status == continuation.StatusLaunching || input.Confirmation != nil || input.Passphrase != nil || input.PlaintextAcknowledgement != nil {
			return result, true, continuation.ErrHandoffNotReady
		}
		return result, true, nil
	}
	if input.Action == "purge" {
		if input.Confirmation == nil || *input.Confirmation != checkpointConfirmation("purge-preview", id, revision, "encrypted") || input.Passphrase != nil || input.PlaintextAcknowledgement != nil {
			return result, true, continuation.ErrCheckpointRevisionChanged
		}
		if err := service.Purge(ctx, id, revision); err != nil {
			return result, true, err
		}
		applied := true
		result.Applied = &applied
		return result, true, nil
	}
	if checkpoint.Status != continuation.StatusApproved && checkpoint.Status != continuation.StatusCompleted {
		return result, true, continuation.ErrHandoffNotReady
	}
	if input.Action == "export-preview" {
		if input.Confirmation != nil || input.Passphrase != nil {
			return result, true, continuation.ErrCheckpointInvalid
		}
		return result, true, nil
	}
	if input.Confirmation == nil || *input.Confirmation != checkpointConfirmation("export-preview", id, revision, format) {
		return result, true, continuation.ErrCheckpointRevisionChanged
	}
	exported, err := service.Export(ctx, id)
	if err != nil {
		return result, true, err
	}
	var contents []byte
	mediaType := "application/vnd.codex-folio.checkpoint+json"
	if input.Action == "export-encrypted" {
		if input.Passphrase == nil || *input.Passphrase == "" || input.PlaintextAcknowledgement != nil {
			return result, true, continuation.ErrCheckpointExportInvalid
		}
		contents, err = continuation.SealCheckpointExport(exported, *input.Passphrase, nil)
	} else {
		if input.PlaintextAcknowledgement == nil || !*input.PlaintextAcknowledgement || input.Passphrase != nil {
			return result, true, continuation.ErrCheckpointExportInvalid
		}
		contents, err = json.MarshalIndent(exported, "", "  ")
		contents = append(contents, '\n')
		mediaType = "application/json"
	}
	if err != nil {
		return result, true, err
	}
	result.Download = &HandoffDownload{Filename: filename, MediaType: mediaType, ContentBase64: base64.StdEncoding.EncodeToString(contents)}
	return result, true, nil
}

func managementOnly(input HandoffRequest) bool {
	return input.ProjectId == nil && input.TargetAlias == nil && input.CheckpointId == nil && input.Revision == nil && input.PreviewRevision == nil && input.ThreadId == nil && input.HistoryConsent == nil && input.Fields == nil && input.Source == nil && input.Setting == nil && input.Confirmation == nil && input.Passphrase == nil && input.PlaintextAcknowledgement == nil && input.RedactPaths == nil && input.RedactText == nil
}

func checkpointConfirmation(action, id, revision, format string) string {
	digest := sha256.Sum256([]byte(action + "\x00" + id + "\x00" + revision + "\x00" + format))
	return base64.RawURLEncoding.EncodeToString(digest[:18])
}

func checkpointSummary(checkpoint continuation.Checkpoint) HandoffCheckpointSummary {
	expires := ""
	if checkpoint.ExpiresAt != nil {
		expires = checkpoint.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	state := "retained"
	if checkpoint.Status == continuation.StatusApproved {
		state = "recoverable"
	} else if checkpoint.Status == continuation.StatusCompleted {
		state = "completed"
	} else if checkpoint.Status == continuation.StatusExpired {
		state = "expired"
	} else if checkpoint.Status == continuation.StatusLaunching {
		state = "start uncertain"
	}
	return HandoffCheckpointSummary{CheckpointId: checkpoint.ID, Status: checkpoint.Status, State: state, Source: checkpoint.Source, ProjectAlias: checkpoint.Project.Alias, ProjectBasename: checkpoint.Project.Basename, Revision: checkpoint.Revision, CreatedAt: checkpoint.CreatedAt.UTC().Format(time.RFC3339Nano), ExpiresAt: expires, Exportable: checkpoint.Status == continuation.StatusApproved || checkpoint.Status == continuation.StatusCompleted, Purgeable: checkpoint.Status != continuation.StatusLaunching}
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
		result.TerminalCommand = server.terminalCommand("handoff", targetAlias, "--checkpoint", checkpoint.ID, "--revision", checkpoint.Revision)
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
