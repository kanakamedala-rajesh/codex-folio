package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	"venkatasudha.com/codex-folio/internal/activity"
	"venkatasudha.com/codex-folio/internal/apperrors"
	"venkatasudha.com/codex-folio/internal/diagnostics"
)

type CommandProjectRequest struct {
	Action string `json:"action"`
	ID     string `json:"id,omitempty"`
	Alias  string `json:"alias,omitempty"`
	Path   string `json:"path,omitempty"`
}

type CommandProjectResponse struct {
	Project  *activity.ProjectIdentity  `json:"project,omitempty"`
	Projects []activity.ProjectIdentity `json:"projects,omitempty"`
	Path     string                     `json:"path,omitempty"`
}

func (client *CommandClient) Project(ctx context.Context, input CommandProjectRequest) (CommandProjectResponse, error) {
	var result CommandProjectResponse
	body, err := json.Marshal(input)
	if err != nil {
		return result, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.origin+CommandProjectsPath, bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", client.origin)
	request.Header.Set(CommandTokenHeader, client.token)
	response, err := client.httpDoer().Do(request)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var failure struct {
			Code string `json:"code"`
		}
		if json.NewDecoder(response.Body).Decode(&failure) == nil && failure.Code != "" {
			return result, apperrors.New(failure.Code, fmt.Errorf("POST %s returned HTTP %d", CommandProjectsPath, response.StatusCode))
		}
		return result, fmt.Errorf("POST %s returned HTTP %d", CommandProjectsPath, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return result, err
	}
	return result, nil
}

func (server *Server) commandProjects(response http.ResponseWriter, request *http.Request) {
	if server.projects == nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	if request.Method != http.MethodPost {
		server.writeMethodError(response, http.MethodPost)
		return
	}
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || request.ContentLength > maxSelectionBodySize {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ProjectIdentityInvalid)
		return
	}
	var input CommandProjectRequest
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxSelectionBodySize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ProjectIdentityInvalid)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ProjectIdentityInvalid)
		return
	}
	result := CommandProjectResponse{}
	switch input.Action {
	case "resolve":
		project, callErr := server.projects.Resolve(request.Context(), input.Path, input.Alias)
		result.Project, err = &project, callErr
	case "list":
		result.Projects, err = server.projects.List(request.Context())
	case "edit":
		project, callErr := server.projects.EditAlias(request.Context(), input.ID, input.Alias)
		result.Project, err = &project, callErr
	case "reconcile":
		project, callErr := server.projects.Reconcile(request.Context(), input.ID, input.Path)
		result.Project, err = &project, callErr
	case "locate":
		result.Path, err = server.projects.CanonicalLocation(request.Context(), input.ID)
	default:
		err = apperrors.New(apperrors.ProjectIdentityInvalid, activity.ErrProjectInvalid)
	}
	if err != nil {
		server.writeAPIError(response, http.StatusConflict, diagnostics.CodeFor(err, apperrors.ProjectIdentityInvalid))
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (server *Server) getProjects(response http.ResponseWriter, request *http.Request) {
	if server.projects == nil {
		server.writeAPIError(response, http.StatusServiceUnavailable, apperrors.HTTPAPIServiceUnavailable)
		return
	}
	projects, err := server.projects.List(request.Context())
	if err != nil {
		server.writeAPIError(response, http.StatusInternalServerError, diagnostics.CodeFor(err, apperrors.StoreReadFailed))
		return
	}
	result := ProjectsResponse{Projects: make([]ProjectIdentity, 0, len(projects))}
	for _, project := range projects {
		result.Projects = append(result.Projects, ProjectIdentity{
			ProjectId: project.ID, Alias: project.Alias, Basename: project.Basename,
			CreatedAt: project.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
			UpdatedAt: project.UpdatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		})
	}
	writeJSON(response, http.StatusOK, result)
}

func (server *Server) editProject(response http.ResponseWriter, request *http.Request) {
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || request.ContentLength > maxSelectionBodySize {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ProjectIdentityInvalid)
		return
	}
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxSelectionBodySize))
	decoder.DisallowUnknownFields()
	var input ProjectEditRequest
	if err := decoder.Decode(&input); err != nil {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ProjectIdentityInvalid)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		server.writeAPIError(response, http.StatusBadRequest, apperrors.ProjectIdentityInvalid)
		return
	}
	if _, err := server.projects.EditAlias(request.Context(), input.ProjectId, input.Alias); err != nil {
		server.writeAPIError(response, http.StatusConflict, diagnostics.CodeFor(err, apperrors.ProjectIdentityInvalid))
		return
	}
	server.getProjects(response, request)
}
