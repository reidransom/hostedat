package client

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cryguy/hostedat/internal/envschema"
)

func capabilityResponse(w http.ResponseWriter) {
	_ = json.NewEncoder(w).Encode(VersionInfo{Version: "1.2.3", MinCLIVersion: "1.0.0", Capabilities: []string{envschema.SecretsCapability}})
}

func manifestZIP(t *testing.T, name string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(f, "{}"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestEnvironmentCapabilityGate(t *testing.T) {
	for _, response := range []string{`{"version":"99.0.0","min_cli_version":""}`, `{"capabilities":["secrets-env-v2"]}`, `not-json`, `{"capabilities":"secrets-env-v1"}`} {
		t.Run(response, func(t *testing.T) {
			mutations := 0
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/version" {
					if r.Header.Get("Authorization") != "" {
						t.Error("public negotiation sent credentials")
					}
					_, _ = io.WriteString(w, response)
					return
				}
				mutations++
				w.WriteHeader(200)
			}))
			defer srv.Close()
			c := New(srv.URL, "disposable-token")
			c.HTTPClient = srv.Client()
			operations := []func() error{
				func() error { _, err := c.SetEnvVar("site", EnvWrite{"TOKEN", "private", true}, nil); return err },
				func() error {
					_, err := c.BulkSetEnvVars("site", []EnvWrite{{"TOKEN", "private", true}}, nil)
					return err
				},
				func() error { return c.DeleteEnvVar("site", "id") },
				func() error { _, err := c.ValidateEnvVars("site", envschema.Schema{}); return err },
				func() error {
					_, err := c.DeployArchive("site", manifestZIP(t, "wrapper/_hostedat.env.json"))
					return err
				},
			}
			for _, op := range operations {
				if err := op(); err == nil || !strings.Contains(err.Error(), "upgrade") {
					t.Fatalf("expected upgrade guidance, got %v", err)
				}
			}
			if mutations != 0 {
				t.Fatalf("sent %d requests before capability support", mutations)
			}
		})
	}
}

func TestEnvironmentTransportPolicy(t *testing.T) {
	for _, tc := range []struct {
		url            string
		optIn, allowed bool
	}{
		{"https://example.com", false, true}, {"http://example.com", true, false},
		{"http://127.0.0.1:8080", false, false}, {"http://127.0.0.1:8080", true, true},
		{"http://127.3.2.1", true, true}, {"http://[::1]:8080", true, true},
		{"http://localhost:8080", true, true}, {"http://localhost.evil.test", true, false},
		{"http://0.0.0.0:8080", true, false}, {"http://192.168.1.2", true, false},
		{"https://user:private@example.com", true, false}, {"https://example.com?private", true, false},
		{"https://example.com#private", true, false}, {"ftp://127.0.0.1", true, false},
	} {
		t.Run(tc.url, func(t *testing.T) {
			c := New(tc.url, "token")
			c.AllowInsecureLoopback = tc.optIn
			err := c.requireSecureTransport()
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v error=%v", tc.allowed, err)
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("URL leaked")
			}
		})
	}
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/api/v1/version" {
			capabilityResponse(w)
			return
		}
		_, _ = io.WriteString(w, `{"id":"id","name":"TOKEN","secret":true}`)
	}))
	defer srv.Close()
	c := New(srv.URL, "token")
	if _, err := c.SetEnvVar("site", EnvWrite{"TOKEN", "private", true}, nil); err == nil || requests != 0 {
		t.Fatal("HTTP was not rejected before network")
	}
	c.AllowInsecureLoopback = true
	if _, err := c.SetEnvVar("site", EnvWrite{"TOKEN", "private", true}, nil); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("requests=%d", requests)
	}
	// Remote rejection is observable without contacting an external host.
	c.BaseURL = "http://example.com"
	if _, err := c.SetEnvVar("site", EnvWrite{"TOKEN", "private", true}, nil); err == nil {
		t.Fatal("remote HTTP accepted")
	}
}

func TestEnvironmentTypedRequests(t *testing.T) {
	bulkCalls, singleCalls := 0, 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/version" {
			capabilityResponse(w)
			return
		}
		if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("X-Hostedat-Version") != "1.1.0" {
			t.Error("missing existing auth/version headers")
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/sites/site/worker/env":
			singleCalls++
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if string(body["value"]) != `""` || string(body["secret"]) != "true" || string(body["schema"]) != "{}" {
				t.Errorf("missing explicit empty/true/schema fields: %v", body)
			}
			_, _ = io.WriteString(w, `{"id":"id","site_id":"site","name":"TOKEN","secret":true,"value":"malicious-server-secret"}`)
		case "POST /api/v1/sites/site/worker/env/bulk":
			bulkCalls++
			var body struct {
				Entries []EnvWrite      `json:"entries"`
				Schema  json.RawMessage `json:"schema"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if len(body.Entries) != 2 || len(body.Schema) != 0 {
				t.Error("batch was split or absent schema changed")
			}
			w.WriteHeader(500)
			_, _ = io.WriteString(w, `{"error":"private"}`)
		case "POST /api/v1/sites/site/worker/env/validate":
			data, _ := io.ReadAll(r.Body)
			if string(data) != `{"schema":{"TOKEN":{"type":"string","required":true}}}` {
				t.Errorf("validation did not send canonical declarations only: %s", data)
			}
			_, _ = io.WriteString(w, `{"valid":true,"names":["TOKEN"]}`)
		case "GET /api/v1/sites/site/worker/env":
			_, _ = io.WriteString(w, `[{"id":"secret","name":"TOKEN","secret":true,"value":"private"},{"id":"plain","name":"EMPTY","secret":false,"value":""}]`)
		case "DELETE /api/v1/sites/site/worker/env/id":
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected path %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "token")
	c.HTTPClient, c.Version = srv.Client(), "1.1.0"
	info, err := c.ServerVersion()
	if err != nil || info.Version != "1.2.3" || info.MinCLIVersion != "1.0.0" {
		t.Fatalf("version compatibility: %+v %v", info, err)
	}
	row, err := c.SetEnvVar("site", EnvWrite{"TOKEN", "", true}, envschema.Schema{})
	if err != nil || row.Value != nil || !row.Secret {
		t.Fatalf("unsafe secret result: %+v %v", row, err)
	}
	_, err = c.BulkSetEnvVars("site", []EnvWrite{{"TOKEN", "private", true}, {"OTHER", "private", true}}, nil)
	if err == nil || strings.Contains(err.Error(), "private") || bulkCalls != 1 || singleCalls != 1 {
		t.Fatalf("unsafe error or sequential fallback: %v bulk=%d single=%d", err, bulkCalls, singleCalls)
	}
	schema := envschema.Schema{"TOKEN": {Type: envschema.String, Required: true}}
	valid, err := c.ValidateEnvVars("site", schema)
	if err != nil || !valid.Valid || len(valid.Names) != 1 {
		t.Fatalf("validation: %+v %v", valid, err)
	}
	rows, err := c.ListEnvVars("site")
	if err != nil || len(rows) != 2 || rows[0].Value != nil || rows[1].Value == nil || *rows[1].Value != "" {
		t.Fatalf("public DTO: %+v %v", rows, err)
	}
	encoded, _ := json.Marshal(rows)
	if strings.Contains(string(encoded), "private") {
		t.Fatal("secret survived reserialization")
	}
	if err := c.DeleteEnvVar("site", "id"); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentRedirectRefusal(t *testing.T) {
	for _, code := range []int{301, 302, 303, 307, 308} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			destinationCalls := 0
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationCalls++ }))
			defer destination.Close()
			for _, target := range []string{destination.URL + "/capture", "/same-origin-capture"} {
				captureCalls := 0
				srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/api/v1/version" {
						capabilityResponse(w)
						return
					}
					if r.URL.Path == "/same-origin-capture" {
						captureCalls++
						return
					}
					http.Redirect(w, r, target+"?private", code)
				}))
				c := New(srv.URL, "private-token")
				c.HTTPClient = srv.Client()
				// A permissive caller redirect policy must not override protection.
				c.HTTPClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return nil }
				_, err := c.SetEnvVar("site", EnvWrite{"TOKEN", "private", true}, nil)
				if err == nil || strings.Contains(err.Error(), "private") || destinationCalls != 0 || captureCalls != 0 {
					t.Fatalf("unsafe write redirect: %v", err)
				}
				_, err = c.ListEnvVars("site")
				if err == nil || destinationCalls != 0 || captureCalls != 0 {
					t.Fatalf("unsafe authorization redirect: %v", err)
				}
				srv.Close()
			}
		})
	}
}

func TestEnvironmentSafeErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{400, `{"error":"private-marker"}`}, {500, `private-marker`},
		{200, `{"secret":"private-marker"}`}, {200, `"private-marker"`}, {200, `private-marker`},
	} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/version" {
				capabilityResponse(w)
				return
			}
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		}))
		c := New(srv.URL, "token")
		c.HTTPClient = srv.Client()
		_, err := c.SetEnvVar("site", EnvWrite{"TOKEN", "private-marker", true}, nil)
		if err == nil || strings.Contains(err.Error(), "private-marker") {
			t.Fatalf("unsafe response error: %v", err)
		}
		if tc.status >= 400 {
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status {
				t.Fatalf("lost API status: %v", err)
			}
		}
		srv.Close()
	}
}

func TestManifestUploadNegotiation(t *testing.T) {
	for _, name := range []string{"_hostedat.env.json", "wrapper/_hostedat.env.json", "wrapper/_HOSTEDAT.ENV.JSON"} {
		uploads, versions := 0, 0
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/version" {
				versions++
				capabilityResponse(w)
				return
			}
			uploads++
			if versions != 1 || r.Header.Get("Authorization") != "Bearer token" {
				t.Error("upload preceded negotiation or lacked authentication")
			}
			_, _ = io.WriteString(w, `{"id":"deploy","version":1}`)
		}))
		c := New(srv.URL, "token")
		c.HTTPClient = srv.Client()
		result, err := c.DeployArchive("site", manifestZIP(t, name))
		if err != nil || result.ID != "deploy" || uploads != 1 {
			t.Fatalf("manifest upload: %+v %v", result, err)
		}
		srv.Close()
	}
	// Legacy schema-less uploads retain their existing server compatibility.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/version" {
			t.Error("legacy deployment unnecessarily negotiated")
		}
		_, _ = io.WriteString(w, `{"id":"legacy"}`)
	}))
	defer srv.Close()
	if _, err := New(srv.URL, "token").DeployArchive("site", manifestZIP(t, "index.html")); err != nil {
		t.Fatal(err)
	}
}
