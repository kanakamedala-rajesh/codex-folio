package main

import (
	"errors"
	"net/url"

	"venkatasudha.com/codex-folio/internal/httpapi"
	"venkatasudha.com/codex-folio/internal/platform"
)

// newServiceCommandClient uses the certificate bound to the private service
// descriptor when connecting to an HTTPS owner. HTTP remains available for
// in-process fixtures that do not publish a certificate.
func newServiceCommandClient(connection platform.ServiceClient) (*httpapi.CommandClient, error) {
	origin, err := url.Parse(connection.Origin)
	if err != nil {
		return nil, err
	}
	switch origin.Scheme {
	case "https":
		return httpapi.NewPinnedCommandClient(connection.Origin, connection.Token, connection.CertificateSHA256)
	case "http":
		return httpapi.NewCommandClient(connection.Origin, connection.Token, nil), nil
	default:
		return nil, errors.New("unsupported service origin scheme")
	}
}
