package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"sort"
	"strings"
	"time"

	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/configpack"
	"venkatasudha.com/codex-folio/internal/diagnostics"
)

type BrowserConfigurationDocument struct {
	Kind    string `json:"kind"`
	Content string `json:"content"`
}

type BrowserConfigurationPackRequest struct {
	Action         string                         `json:"action"`
	PackID         string                         `json:"pack_id,omitempty"`
	Version        string                         `json:"version,omitempty"`
	Alias          string                         `json:"alias,omitempty"`
	Documents      []BrowserConfigurationDocument `json:"documents,omitempty"`
	Reviewed       bool                           `json:"reviewed,omitempty"`
	ExpectedDigest string                         `json:"expected_digest,omitempty"`
}

type BrowserConfigurationPack struct {
	ID        string           `json:"id"`
	Version   string           `json:"version"`
	State     configpack.State `json:"state"`
	Digest    string           `json:"digest"`
	Files     []string         `json:"files"`
	CreatedAt string           `json:"created_at"`
}

type BrowserConfigurationPackResponse struct {
	Packs      []BrowserConfigurationPack   `json:"packs"`
	Pack       *BrowserConfigurationPack    `json:"pack,omitempty"`
	Assignment *configpack.Assignment       `json:"assignment,omitempty"`
	Plan       *configpack.ProjectionPlan   `json:"plan,omitempty"`
	Projection *configpack.ProjectionResult `json:"projection,omitempty"`
	Promotion  *configpack.PromotionPreview `json:"promotion_preview,omitempty"`
}

func (server *Server) browserConfigurationPacks(response http.ResponseWriter, request *http.Request) {
	if server.configurationPacks == nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	if request.Method == http.MethodGet {
		packs, err := server.configurationPacks.Packs(request.Context())
		if err != nil {
			server.writeAPIError(response, http.StatusInternalServerError, diagnostics.CodeFor(err, apperrors.StoreReadFailed))
			return
		}
		result := BrowserConfigurationPackResponse{Packs: make([]BrowserConfigurationPack, 0, len(packs))}
		for _, pack := range packs {
			result.Packs = append(result.Packs, browserConfigurationPack(pack))
		}
		writeJSON(response, http.StatusOK, result)
		return
	}
	if request.Method != http.MethodPost {
		server.writeMethodError(response, http.MethodGet+", "+http.MethodPost)
		return
	}
	input, ok := server.decodeBrowserConfigurationPack(response, request)
	if !ok {
		return
	}
	result, err := server.applyBrowserConfigurationPack(request, input)
	if err != nil {
		status := configurationPackHTTPStatus(err)
		if status == http.StatusConflict && apperrors.Code(err) == apperrors.ConfigurationPackInvalid {
			status = http.StatusBadRequest
		}
		server.writeAPIError(response, status, diagnostics.CodeFor(err, apperrors.ConfigurationPackInvalid))
		return
	}
	result.Packs = []BrowserConfigurationPack{}
	writeJSON(response, http.StatusOK, result)
}

func (server *Server) decodeBrowserConfigurationPack(response http.ResponseWriter, request *http.Request) (BrowserConfigurationPackRequest, bool) {
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || request.ContentLength > maxConfigPackBodySize {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ConfigurationPackInvalid)
		return BrowserConfigurationPackRequest{}, false
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maxConfigPackBodySize+1))
	if err != nil || len(body) > maxConfigPackBodySize {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ConfigurationPackInvalid)
		return BrowserConfigurationPackRequest{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var input BrowserConfigurationPackRequest
	if err := decoder.Decode(&input); err != nil {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ConfigurationPackInvalid)
		return BrowserConfigurationPackRequest{}, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ConfigurationPackInvalid)
		return BrowserConfigurationPackRequest{}, false
	}
	return input, true
}

func (server *Server) applyBrowserConfigurationPack(request *http.Request, input BrowserConfigurationPackRequest) (BrowserConfigurationPackResponse, error) {
	service := server.configurationPacks
	switch strings.TrimSpace(input.Action) {
	case "create":
		files, err := browserConfigurationFiles(input.Documents)
		if err != nil {
			return BrowserConfigurationPackResponse{}, err
		}
		pack, err := service.CreateDraft(request.Context(), input.PackID, input.Version, files)
		return browserPackResponse(pack, err), err
	case "approve":
		if !input.Reviewed {
			return BrowserConfigurationPackResponse{}, apperrors.New(apperrors.ConfigurationPackInvalid, configpack.ErrInvalid)
		}
		pack, err := service.Approve(request.Context(), input.PackID, input.Version)
		return browserPackResponse(pack, err), err
	case "assign":
		if !input.Reviewed {
			return BrowserConfigurationPackResponse{}, apperrors.New(apperrors.ConfigurationPackInvalid, configpack.ErrInvalid)
		}
		assignment, err := service.Assign(request.Context(), input.Alias, input.PackID, input.Version)
		return BrowserConfigurationPackResponse{Assignment: assignmentPointer(assignment, err)}, err
	case "preview":
		plan, err := service.Preview(request.Context(), input.Alias)
		return BrowserConfigurationPackResponse{Plan: planPointer(plan, err)}, err
	case "apply":
		projection, err := service.ApplyReviewed(request.Context(), input.Alias, input.ExpectedDigest, input.Reviewed)
		return BrowserConfigurationPackResponse{Projection: projectionPointer(projection, err)}, err
	case "promotion-preview":
		promotion, err := service.PreviewPromotion(request.Context(), input.Alias, input.Version)
		return BrowserConfigurationPackResponse{Promotion: promotionPointer(promotion, err)}, err
	case "promote":
		pack, err := service.PromoteReviewed(request.Context(), input.Alias, input.Version, input.ExpectedDigest, input.Reviewed)
		return browserPackResponse(pack, err), err
	default:
		return BrowserConfigurationPackResponse{}, apperrors.New(apperrors.ConfigurationPackInvalid, configpack.ErrInvalid)
	}
}

func browserConfigurationFiles(documents []BrowserConfigurationDocument) (map[string]string, error) {
	files := make(map[string]string, len(documents))
	paths := map[string]string{"config": "config.toml", "agents": "AGENTS.md", "plugins": "plugins.lock"}
	for _, document := range documents {
		path, ok := paths[document.Kind]
		if !ok {
			return nil, apperrors.New(apperrors.ConfigurationPackInvalid, configpack.ErrInvalid)
		}
		if _, exists := files[path]; exists {
			return nil, apperrors.New(apperrors.ConfigurationPackInvalid, configpack.ErrInvalid)
		}
		files[path] = document.Content
	}
	if err := configpack.ValidateFiles(files); err != nil {
		return nil, err
	}
	return files, nil
}

func browserPackResponse(pack configpack.Pack, err error) BrowserConfigurationPackResponse {
	if err != nil {
		return BrowserConfigurationPackResponse{}
	}
	projected := browserConfigurationPack(pack)
	return BrowserConfigurationPackResponse{Pack: &projected}
}

func browserConfigurationPack(pack configpack.Pack) BrowserConfigurationPack {
	createdAt := ""
	if !pack.CreatedAt.IsZero() {
		createdAt = pack.CreatedAt.UTC().Format(time.RFC3339)
	}
	files := make([]string, 0, len(pack.Files))
	for path := range pack.Files {
		files = append(files, path)
	}
	sort.Strings(files)
	return BrowserConfigurationPack{ID: pack.ID, Version: pack.Version, State: pack.State, Digest: pack.Digest, Files: files, CreatedAt: createdAt}
}
