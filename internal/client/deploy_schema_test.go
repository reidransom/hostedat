package client

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cryguy/hostedat/internal/artifactpolicy"
	"github.com/cryguy/hostedat/internal/envschema"
)

func TestDeploymentSchemaPackaging(t *testing.T) {
	for _, source := range []struct{ name, body string }{
		{"env.json", `{"TOKEN":{"type":"string"}}`},
		{"env.yml", "TOKEN:\n  type: string\n"},
		{"_hostedat.env.json", `{"TOKEN":{"type":"string"}}`},
	} {
		t.Run(source.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, source.name)
			if err := os.WriteFile(path, []byte(source.body), 0600); err != nil {
				t.Fatal(err)
			}
			project, err := envschema.Discover(dir, source.name)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Link(path, filepath.Join(dir, "source-alias")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(path, filepath.Join(dir, "symlink")); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{".env", ".ENV.production", "index.html"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("private-marker"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			// Packaging must use the parsed snapshot, never reparse changed source bytes.
			if err := os.WriteFile(path, []byte("private-source-marker"), 0600); err != nil {
				t.Fatal(err)
			}
			data, err := zipDirectory(dir, project)
			if err != nil {
				t.Fatal(err)
			}
			archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			if len(archive.File) != 2 {
				t.Fatalf("unexpected packaged entries: %d", len(archive.File))
			}
			found := false
			for _, f := range archive.File {
				if f.Name == "index.html" {
					continue
				}
				if f.Name != artifactpolicy.EnvManifest {
					t.Fatalf("source or excluded file uploaded: %s", f.Name)
				}
				r, err := f.Open()
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(r)
				_ = r.Close()
				if err != nil || string(body) != `{"TOKEN":{"type":"string","required":true}}` {
					t.Fatalf("not canonical declarations: %q %v", body, err)
				}
				found = true
			}
			if !found {
				t.Fatal("missing canonical manifest")
			}
		})
	}
}

func TestDeploymentRejectsSeparateCanonicalMetadata(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "env.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	project, err := envschema.Discover(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"_hostedat.env.json", "_HOSTEDAT.ENV.JSON", "nested/_hostedat.env.json"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := zipDirectory(dir, project); err == nil {
			t.Fatal("separate canonical manifest accepted")
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestManifestPreflightRefusesUpload(t *testing.T) {
	for _, response := range []struct {
		status int
		body   string
	}{
		{400, `{"error":"private-marker"}`}, {200, `{"valid":false}`}, {200, `invalid-private-marker`},
	} {
		uploads := 0
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/version":
				capabilityResponse(w)
			case "/api/v1/sites/site/worker/env/validate":
				w.WriteHeader(response.status)
				_, _ = io.WriteString(w, response.body)
			default:
				uploads++
				_, _ = io.WriteString(w, `{"id":"deploy"}`)
			}
		}))
		c := New(srv.URL, "token")
		c.HTTPClient = srv.Client()
		_, err := c.DeployArchive("site", manifestZIP(t, "_hostedat.env.json"))
		srv.Close()
		if err == nil || strings.Contains(err.Error(), "private-marker") || uploads != 0 {
			t.Fatalf("unsafe/ignored preflight: %v uploads=%d", err, uploads)
		}
	}
}
