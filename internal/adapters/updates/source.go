// Package updatesadapter is the outward HTTPS adapter for update metadata.
// It retrieves only a bounded manifest and never fetches the download URL.
package updatesadapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"venkatasudha.com/codex-folio/internal/updates"
)

const (
	manifestSchemaVersion = 1
	maximumManifestBytes  = 64 * 1024
	defaultAttemptTimeout = 15 * time.Second
)

type disabledSource struct{}

func DisabledSource() updates.Source { return disabledSource{} }

func (disabledSource) Configured() bool { return false }

func (disabledSource) Check(context.Context) (updates.Evidence, error) {
	return updates.Evidence{}, updates.ErrSourceUnconfigured
}

type HTTPSOptions struct {
	ManifestURL           string
	AllowedDownloadPrefix string
	Client                *http.Client
	AttemptTimeout        time.Duration
}

type HTTPSSource struct {
	manifestURL    *url.URL
	downloadBase   *url.URL
	client         *http.Client
	attemptTimeout time.Duration
}

func NewHTTPSSource(options HTTPSOptions) (*HTTPSSource, error) {
	manifestURL, err := parseTrustedURL(options.ManifestURL)
	if err != nil || manifestURL.RawQuery != "" {
		return nil, updates.ErrInvalid
	}
	downloadBase, err := parseTrustedURL(options.AllowedDownloadPrefix)
	if err != nil || downloadBase.RawQuery != "" || !strings.HasSuffix(downloadBase.Path, "/") {
		return nil, updates.ErrInvalid
	}
	attemptTimeout := options.AttemptTimeout
	if attemptTimeout == 0 {
		attemptTimeout = defaultAttemptTimeout
	}
	if attemptTimeout < 0 {
		return nil, updates.ErrInvalid
	}
	client := secureClient(options.Client, attemptTimeout)
	return &HTTPSSource{manifestURL: manifestURL, downloadBase: downloadBase, client: client, attemptTimeout: attemptTimeout}, nil
}

func (source *HTTPSSource) Configured() bool {
	return source != nil && source.manifestURL != nil && source.downloadBase != nil && source.client != nil && source.attemptTimeout > 0
}

func (source *HTTPSSource) Check(ctx context.Context) (updates.Evidence, error) {
	if !source.Configured() {
		return updates.Evidence{}, updates.ErrSourceUnconfigured
	}
	attemptCtx, cancel := context.WithTimeout(ctx, source.attemptTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, source.manifestURL.String(), nil)
	if err != nil {
		return updates.Evidence{}, updates.ErrSourceUnconfigured
	}
	request.Header.Set("Accept", "application/json")
	response, err := source.client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return updates.Evidence{}, ctxErr
		}
		if attemptCtx.Err() != nil {
			return updates.Evidence{}, updates.ErrSourceUnavailable
		}
		if timedOut(err) {
			return updates.Evidence{}, updates.ErrSourceUnavailable
		}
		return updates.Evidence{}, updates.ErrSourceOffline
	}
	if response == nil || response.Body == nil {
		return updates.Evidence{}, updates.ErrSourceUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.Request != nil && response.Request.URL != nil && response.Request.URL.String() != source.manifestURL.String() {
		return updates.Evidence{}, updates.ErrSourceUnavailable
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return updates.Evidence{}, updates.ErrSourceUnavailable
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return updates.Evidence{}, updates.ErrSourceMalformed
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maximumManifestBytes+1))
	if ctxErr := ctx.Err(); ctxErr != nil {
		return updates.Evidence{}, ctxErr
	}
	if attemptCtx.Err() != nil {
		return updates.Evidence{}, updates.ErrSourceUnavailable
	}
	if timedOut(err) {
		return updates.Evidence{}, updates.ErrSourceUnavailable
	}
	if err != nil || len(payload) > maximumManifestBytes {
		return updates.Evidence{}, updates.ErrSourceMalformed
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	var manifest manifestV1
	if err := decoder.Decode(&manifest); err != nil {
		return updates.Evidence{}, updates.ErrSourceMalformed
	}
	if err := rejectTrailing(decoder); err != nil {
		return updates.Evidence{}, updates.ErrSourceMalformed
	}
	if manifest.SchemaVersion != manifestSchemaVersion {
		return updates.Evidence{}, updates.ErrSourceMalformed
	}
	evidence := updates.Evidence{
		Version:           manifest.Version,
		ReleaseNotes:      manifest.ReleaseNotes,
		DownloadURL:       manifest.DownloadURL,
		InstallerGuidance: manifest.InstallerGuidance,
	}
	if err := updates.ValidateEvidence(evidence); err != nil || !source.allowedDownload(evidence.DownloadURL) {
		return updates.Evidence{}, updates.ErrSourceMalformed
	}
	return evidence, nil
}

func timedOut(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}

type manifestV1 struct {
	SchemaVersion     int    `json:"schema_version"`
	Version           string `json:"version"`
	ReleaseNotes      string `json:"release_notes"`
	DownloadURL       string `json:"download_url"`
	InstallerGuidance string `json:"installer_guidance"`
}

func parseTrustedURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" {
		return nil, updates.ErrInvalid
	}
	if parsed.Hostname() == "" || parsed.Path == "" || strings.Contains(parsed.Path, "\\") || strings.Contains(parsed.EscapedPath(), "%2f") || strings.Contains(parsed.EscapedPath(), "%2F") || strings.Contains(parsed.EscapedPath(), "%5c") || strings.Contains(parsed.EscapedPath(), "%5C") {
		return nil, updates.ErrInvalid
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return nil, updates.ErrInvalid
		}
	}
	return parsed, nil
}

func secureClient(candidate *http.Client, attemptTimeout time.Duration) *http.Client {
	if candidate == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		candidate = &http.Client{Transport: transport}
	}
	clone := *candidate
	clone.Jar = nil
	clone.Timeout = attemptTimeout
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &clone
}

func (source *HTTPSSource) allowedDownload(raw string) bool {
	download, err := parseTrustedURL(raw)
	if err != nil || download.RawQuery != "" {
		return false
	}
	if !strings.EqualFold(download.Scheme, source.downloadBase.Scheme) || !strings.EqualFold(download.Host, source.downloadBase.Host) {
		return false
	}
	cleaned := path.Clean(download.Path)
	if (download.Path != cleaned && download.Path != cleaned+"/") || strings.Contains(download.Path, `\`) {
		return false
	}
	return strings.HasPrefix(download.Path, source.downloadBase.Path)
}

func rejectTrailing(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return updates.ErrSourceMalformed
		}
		return err
	}
	return nil
}
