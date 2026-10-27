package client

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/cryguy/hostedat/internal/envschema"
)

// VersionInfo preserves the existing version fields. Missing capabilities denote
// an older server, not support inferred from its version string.
type VersionInfo struct {
	Version       string   `json:"version"`
	MinCLIVersion string   `json:"min_cli_version"`
	Capabilities  []string `json:"capabilities"`
}

// ServerVersion uses the public endpoint without sending credentials.
func (c *Client) ServerVersion() (*VersionInfo, error) {
	if _, err := c.serverURL(); err != nil {
		return nil, err
	}
	public := *c
	public.Token = ""
	public.safeErrors = true
	var info VersionInfo
	if err := public.get("/api/v1/version", &info); err != nil {
		return nil, safeClientError(err)
	}
	return &info, nil
}

// RequireSecretsCapability must precede secret mutations and manifest uploads.
// It intentionally does not cache support across operations/server changes.
func (c *Client) RequireSecretsCapability() error {
	if err := c.requireSecureTransport(); err != nil {
		return err
	}
	info, err := c.ServerVersion()
	if err != nil {
		return errors.New("cannot verify secret/environment support; upgrade the server and use its final HTTPS URL")
	}
	for _, capability := range info.Capabilities {
		if capability == envschema.SecretsCapability {
			return nil
		}
	}
	return errors.New("server lacks secrets-env-v1; upgrade the hostedat server before managing secrets or uploading environment declarations")
}

func (c *Client) serverURL() (*url.URL, error) {
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("invalid server URL; use an HTTPS server URL without credentials, query or fragment")
	}
	return u, nil
}

func (c *Client) requireSecureTransport() error {
	u, err := c.serverURL()
	if err != nil {
		return err
	}
	if u.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	loopback := strings.EqualFold(u.Hostname(), "localhost") || (ip != nil && ip.IsLoopback())
	if u.Scheme == "http" && c.AllowInsecureLoopback && loopback {
		return nil
	}
	return errors.New("secret/environment operations require HTTPS; HTTP requires explicit AllowInsecureLoopback development opt-in and a loopback host")
}

// EnvVar is a public management DTO, never an encrypted storage model. Value is
// present only for plaintext bindings (including an explicitly empty string).
type EnvVar struct {
	ID     string  `json:"id"`
	SiteID string  `json:"site_id"`
	Name   string  `json:"name"`
	Secret bool    `json:"secret"`
	Value  *string `json:"value,omitempty"`
}

// UnmarshalJSON also discards a secret value from a nonconforming server, so a
// caller cannot accidentally print or marshal a legacy disclosure.
func (v *EnvVar) UnmarshalJSON(data []byte) error {
	type wire EnvVar
	var result wire
	if err := json.Unmarshal(data, &result); err != nil {
		return err
	}
	if result.Secret {
		result.Value = nil
	}
	*v = EnvVar(result)
	return nil
}

// EnvWrite always sends value and classification, including empty/false. Secret
// commands must set Secret true; there is no implicit per-name bulk fallback.
type EnvWrite struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

type EnvValidation struct {
	Valid bool     `json:"valid"`
	Names []string `json:"names"`
}

func envPath(siteID string) (string, error) {
	if siteID == "" || siteID == "." || siteID == ".." || strings.ContainsAny(siteID, "/\\?#") {
		return "", errors.New("invalid site ID")
	}
	return "/api/v1/sites/" + url.PathEscape(siteID) + "/worker/env", nil
}

func candidateDocument(schema envschema.Schema) (json.RawMessage, error) {
	if schema == nil {
		return nil, nil
	}
	return schema.CanonicalJSON()
}

// safeClientError never retains parser text, request/response bodies, URLs, or
// transport diagnostics that may contain credentials or an echoed secret.
func safeClientError(err error) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return &APIError{StatusCode: apiErr.StatusCode, Message: http.StatusText(apiErr.StatusCode)}
	}
	return errors.New("server request failed or returned an invalid response")
}

func (c *Client) envJSON(method, path string, request, response any, mutate bool) error {
	if mutate {
		if err := c.RequireSecretsCapability(); err != nil {
			return err
		}
	} else if err := c.requireSecureTransport(); err != nil {
		return err
	}
	secure := *c
	secure.safeErrors = true
	if err := secure.doJSON(method, path, request, response); err != nil {
		return safeClientError(err)
	}
	return nil
}

func (c *Client) SetEnvVar(siteID string, entry EnvWrite, schema envschema.Schema) (*EnvVar, error) {
	path, err := envPath(siteID)
	if err != nil {
		return nil, err
	}
	document, err := candidateDocument(schema)
	if err != nil {
		return nil, err
	}
	request := struct {
		EnvWrite
		Schema json.RawMessage `json:"schema,omitempty"`
	}{entry, document}
	var result EnvVar
	if err := c.envJSON(http.MethodPost, path, request, &result, true); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) BulkSetEnvVars(siteID string, entries []EnvWrite, schema envschema.Schema) ([]EnvVar, error) {
	path, err := envPath(siteID)
	if err != nil {
		return nil, err
	}
	document, err := candidateDocument(schema)
	if err != nil {
		return nil, err
	}
	request := struct {
		Entries []EnvWrite      `json:"entries"`
		Schema  json.RawMessage `json:"schema,omitempty"`
	}{entries, document}
	var result []EnvVar
	if err := c.envJSON(http.MethodPost, path+"/bulk", request, &result, true); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) ListEnvVars(siteID string) ([]EnvVar, error) {
	path, err := envPath(siteID)
	if err != nil {
		return nil, err
	}
	var result []EnvVar
	if err := c.envJSON(http.MethodGet, path, nil, &result, false); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) DeleteEnvVar(siteID, varID string) error {
	path, err := envPath(siteID)
	if err != nil {
		return err
	}
	if varID == "" || varID == "." || varID == ".." || strings.ContainsAny(varID, "/\\?#") {
		return errors.New("invalid environment variable ID")
	}
	return c.envJSON(http.MethodDelete, path+"/"+url.PathEscape(varID), nil, nil, true)
}

// ValidateEnvVars sends declarations only, never local binding values. Nil is
// rejected; an empty non-nil schema explicitly validates an empty declaration.
func (c *Client) ValidateEnvVars(siteID string, schema envschema.Schema) (*EnvValidation, error) {
	path, err := envPath(siteID)
	if err != nil {
		return nil, err
	}
	if schema == nil {
		return nil, errors.New("environment declaration is required")
	}
	document, err := candidateDocument(schema)
	if err != nil {
		return nil, err
	}
	request := struct {
		Schema json.RawMessage `json:"schema"`
	}{document}
	var result EnvValidation
	// Validation is non-mutating, but cannot use an older endpoint contract.
	if err := c.envJSON(http.MethodPost, path+"/validate", request, &result, true); err != nil {
		return nil, err
	}
	return &result, nil
}
