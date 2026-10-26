package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func executeDeploy(args ...string) (string, error) {
	cmd := deployCmd()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestDeployInvocationSchemaAndPreflight(t *testing.T) {
	for _, tc := range []struct {
		name, filename, body string
		explicit             bool
	}{
		{"json", "env.json", `{"TOKEN":{"type":"string"}}`, false},
		{"yaml", "env.yml", "TOKEN:\n  type: string\n", false},
		{"explicit", "rules.yml", "TOKEN:\n  type: string\n", true},
		{"empty", "env.json", `{}`, false},
		{"legacy", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			if err := os.Mkdir("dist", 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile("dist/index.html", []byte("safe-asset"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile("dist/.ENV.production", []byte("private-marker"), 0600); err != nil {
				t.Fatal(err)
			}
			// Neither deploy nor discovery may open the local dotenv.
			if err := os.Mkdir(".env", 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TOKEN", "private-marker")
			if tc.filename != "" {
				if err := os.WriteFile(tc.filename, []byte(tc.body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var events []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/sites":
					fmt.Fprint(w, `[{"id":"site1","name":"demo","subdomain_slug":"demo"}]`)
				case "/api/v1/version":
					events = append(events, "capability")
					fmt.Fprint(w, `{"capabilities":["secrets-env-v1"]}`)
				case "/api/v1/sites/site1/worker/env/validate":
					events = append(events, "preflight")
					body, _ := io.ReadAll(r.Body)
					want := `{"schema":{"TOKEN":{"type":"string","required":true}}}`
					if tc.name == "empty" {
						want = `{"schema":{}}`
					}
					if string(body) != want {
						t.Error("preflight did not contain canonical declarations only")
					}
					fmt.Fprint(w, `{"valid":true}`)
				case "/api/v1/sites/site1/deploy":
					events = append(events, "upload")
					file, _, err := r.FormFile("file")
					if err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					data, err := io.ReadAll(file)
					_ = file.Close()
					if err != nil {
						t.Error(err)
						return
					}
					archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
					if err != nil {
						t.Error(err)
						return
					}
					wantCount := 2
					if tc.name == "legacy" {
						wantCount = 1
					}
					if len(archive.File) != wantCount {
						t.Error("unexpected ZIP entries")
					}
					for _, f := range archive.File {
						if f.Name != "index.html" && f.Name != "_hostedat.env.json" {
							t.Error("unexpected source/value file in ZIP")
						}
						r, _ := f.Open()
						body, _ := io.ReadAll(r)
						_ = r.Close()
						if bytes.Contains(body, []byte("private-marker")) {
							t.Error("local values uploaded")
						}
					}
					fmt.Fprint(w, `{"id":"deploy","version":1}`)
				default:
					t.Error("unexpected deploy request")
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			t.Setenv("HOSTEDAT_SERVER", server.URL)
			t.Setenv("HOSTEDAT_API_KEY", "test-key")
			args := []string{"demo", "dist", "--allow-insecure-loopback"}
			if tc.explicit {
				args = append(args, "--schema", tc.filename)
			}
			out, err := executeDeploy(args...)
			if err != nil || !strings.Contains(out, "Deployed!") {
				t.Fatalf("deploy failed: %s %v", out, err)
			}
			want := "capability,preflight,upload"
			if tc.name == "legacy" {
				want = "upload"
			}
			if strings.Join(events, ",") != want {
				t.Fatalf("incorrect preflight order: %v", events)
			}
		})
	}
}

func TestDeploySchemaSelectionErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HOSTEDAT_API_KEY", "")
	for _, name := range []string{"env.json", "env.yml"} {
		if err := os.WriteFile(name, []byte(`{}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := executeDeploy("demo", "dist"); err == nil || !strings.Contains(err.Error(), "both env.json and env.yml") {
		t.Fatalf("ambiguous schemas accepted: %v", err)
	}
	if _, err := executeDeploy("demo", "dist", "--schema", "missing.json"); err == nil || !strings.Contains(err.Error(), "cannot open schema") {
		t.Fatalf("missing schema accepted: %v", err)
	}
}
