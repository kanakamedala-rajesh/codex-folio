package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/configpack"
	"venkatasudha.com/codex-folio/internal/diagnostics"
)

type CommandConfigurationPackRequest struct {
	Action   string            `json:"action"`
	PackID   string            `json:"pack_id,omitempty"`
	Version  string            `json:"version,omitempty"`
	Alias    string            `json:"alias,omitempty"`
	Path     string            `json:"path,omitempty"`
	Content  string            `json:"content,omitempty"`
	Files    map[string]string `json:"files,omitempty"`
	Reviewed bool              `json:"reviewed,omitempty"`
}

type CommandConfigurationPackResponse struct {
	Pack       *configpack.Pack             `json:"pack,omitempty"`
	Assignment *configpack.Assignment       `json:"assignment,omitempty"`
	Plan       *configpack.ProjectionPlan   `json:"plan,omitempty"`
	Projection *configpack.ProjectionResult `json:"projection,omitempty"`
	Promotion  *configpack.PromotionPreview `json:"promotion_preview,omitempty"`
}

func (server *Server) commandConfigurationPack(response http.ResponseWriter, request *http.Request) {
	if server.configurationPacks == nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	if request.Method != http.MethodPost {
		server.writeMethodError(response, http.MethodPost)
		return
	}
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || request.ContentLength > maxConfigPackBodySize {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ConfigurationPackInvalid)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maxConfigPackBodySize+1))
	if err != nil || len(body) > maxConfigPackBodySize {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ConfigurationPackInvalid)
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var input CommandConfigurationPackRequest
	if err := decoder.Decode(&input); err != nil {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ConfigurationPackInvalid)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ConfigurationPackInvalid)
		return
	}

	result, err := server.applyConfigurationPack(request.Context(), input)
	if err != nil {
		server.writeAPIError(response, configurationPackHTTPStatus(err), diagnostics.CodeFor(err, apperrors.ConfigurationPackInvalid))
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (server *Server) applyConfigurationPack(ctx context.Context, input CommandConfigurationPackRequest) (CommandConfigurationPackResponse, error) {
	service := server.configurationPacks
	switch strings.TrimSpace(input.Action) {
	case "create":
		pack, err := service.CreateDraft(ctx, input.PackID, input.Version, input.Files)
		return CommandConfigurationPackResponse{Pack: packPointer(pack, err)}, err
	case "approve":
		pack, err := service.Approve(ctx, input.PackID, input.Version)
		return CommandConfigurationPackResponse{Pack: packPointer(pack, err)}, err
	case "assign":
		assignment, err := service.Assign(ctx, input.Alias, input.PackID, input.Version)
		return CommandConfigurationPackResponse{Assignment: assignmentPointer(assignment, err)}, err
	case "override":
		return CommandConfigurationPackResponse{}, service.SetOverride(ctx, input.Alias, input.Path, input.Content)
	case "preview":
		plan, err := service.Preview(ctx, input.Alias)
		return CommandConfigurationPackResponse{Plan: planPointer(plan, err)}, err
	case "project":
		projection, err := service.Project(ctx, input.Alias)
		return CommandConfigurationPackResponse{Projection: projectionPointer(projection, err)}, err
	case "promotion-preview":
		promotion, err := service.PreviewPromotion(ctx, input.Alias, input.Version)
		return CommandConfigurationPackResponse{Promotion: promotionPointer(promotion, err)}, err
	case "promote":
		pack, err := service.Promote(ctx, input.Alias, input.Version, input.Reviewed)
		return CommandConfigurationPackResponse{Pack: packPointer(pack, err)}, err
	default:
		return CommandConfigurationPackResponse{}, apperrors.New(apperrors.ConfigurationPackInvalid, configpack.ErrInvalid)
	}
}

func configurationPackHTTPStatus(err error) int {
	if apperrors.Code(err) == apperrors.ConfigurationPackProjectionFailed {
		return http.StatusInternalServerError
	}
	if apperrors.Code(err) == apperrors.StoreReadFailed || apperrors.Code(err) == apperrors.StoreWriteFailed {
		return http.StatusInternalServerError
	}
	return http.StatusConflict
}

func packPointer(value configpack.Pack, err error) *configpack.Pack {
	if err != nil {
		return nil
	}
	return &value
}

func assignmentPointer(value configpack.Assignment, err error) *configpack.Assignment {
	if err != nil {
		return nil
	}
	return &value
}

func planPointer(value configpack.ProjectionPlan, err error) *configpack.ProjectionPlan {
	if err != nil {
		return nil
	}
	return &value
}

func projectionPointer(value configpack.ProjectionResult, err error) *configpack.ProjectionResult {
	if err != nil {
		return nil
	}
	return &value
}

func promotionPointer(value configpack.PromotionPreview, err error) *configpack.PromotionPreview {
	if err != nil {
		return nil
	}
	return &value
}
