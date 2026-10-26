package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func executeEnv(args ...string) (string, error) {
	cmd := envCmd()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestEnvCheckLocal(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(500)
	}))
	defer server.Close()
	t.Setenv("HOSTEDAT_SERVER", server.URL)
	t.Setenv("HOSTEDAT_API_KEY", "")
	const marker = "unique-local-value-never-output"
	for _, tc := range []struct {
		name, schema, dotenv, shell, wantError string
		setShell                               bool
	}{
		{name: "absent", schema: `{"TASK13_VALUE":{"type":"string"}}`, wantError: "required value is missing"},
		{name: "empty", schema: `{"TASK13_VALUE":{"type":"string"}}`, dotenv: "TASK13_VALUE=\n", wantError: "required value is empty"},
		{name: "invalid", schema: `{"TASK13_VALUE":{"type":"number"}}`, dotenv: "TASK13_VALUE=" + marker, wantError: "number constraint"},
		{name: "optional absent", schema: `{"TASK13_VALUE":{"type":"number","required":false}}`},
		{name: "optional invalid", schema: `{"TASK13_VALUE":{"type":"boolean","required":false}}`, dotenv: "TASK13_VALUE=" + marker, wantError: "boolean constraint"},
		{name: "optional empty string", schema: `{"TASK13_VALUE":{"type":"string","required":false}}`, dotenv: "TASK13_VALUE=\n"},
		{name: "shell precedence", schema: `{"TASK13_VALUE":{"type":"number"}}`, dotenv: "TASK13_VALUE=" + marker, setShell: true, shell: "7"},
		{name: "explicit empty shell", schema: `{"TASK13_VALUE":{"type":"string"}}`, dotenv: "TASK13_VALUE=" + marker, setShell: true, wantError: "required value is empty"},
		{name: "undeclared ignored", schema: `{}`, dotenv: "UNDECLARED=" + marker},
		{name: "quotes crlf multiline literal", schema: `{"TASK13_VALUE":{"type":"enum","values":["  #${NO_EXEC}\nend  "]}}`, dotenv: "TASK13_VALUE=\"  #${NO_EXEC}\nend  \"\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			if err := os.WriteFile("env.json", []byte(tc.schema), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.dotenv != "" {
				if err := os.WriteFile(".env", []byte(tc.dotenv), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.setShell {
				t.Setenv("TASK13_VALUE", tc.shell)
			}
			out, err := executeEnv("check")
			if tc.wantError == "" && err != nil || tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("unexpected validation result: %v", err)
			}
			if strings.Contains(out, marker) || err != nil && strings.Contains(err.Error(), marker) {
				t.Fatal("local value disclosed")
			}
			if tc.wantError == "" && !strings.Contains(out, "Local environment valid") {
				t.Fatal("missing local success")
			}
			wantFiles := 1
			if tc.dotenv != "" {
				wantFiles++
			}
			entries, err := os.ReadDir(".")
			if err != nil || len(entries) != wantFiles {
				t.Fatal("local check wrote a file")
			}
		})
	}
	if requests != 0 {
		t.Fatal("local checks contacted server")
	}
}

func TestEnvCheckSelectionAndMode(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, args := range [][]string{{"check"}, {"check", "--remote"}, {"check", "--site", "demo"}, {"check", "--allow-insecure-loopback"}, {"check", "unexpected"}, {"check", "--schema", "missing.json"}} {
		if _, err := executeEnv(args...); err == nil {
			t.Fatalf("invalid invocation accepted: %v", args)
		}
	}
	if err := os.Mkdir("project", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("project", "rules.yml"), []byte("TASK13_VALUE:\n  type: number\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("project", ".env"), []byte("TASK13_VALUE=7\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := executeEnv("check", "--schema", "project/rules.yml"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"env.json", "env.yml"} {
		if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := executeEnv("check"); err == nil {
		t.Fatal("ambiguous schemas accepted")
	}
	if _, err := executeEnv("check", "--schema", "env.json"); err != nil {
		t.Fatal(err)
	}
}

func TestEnvCheckRemoteDeclarationsOnly(t *testing.T) {
	t.Chdir(t.TempDir())
	const marker = "unique-local-remote-exclusion-marker"
	if err := os.WriteFile("env.yml", []byte("TASK13_VALUE:\n  type: number\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// A local check cannot open this as a regular dotenv; remote must not try.
	if err := os.Mkdir(".env", 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TASK13_VALUE", marker)
	var validations int
	status := 200
	valid := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/version":
			fmt.Fprint(w, `{"capabilities":["secrets-env-v1"]}`)
		case "/api/v1/sites":
			fmt.Fprint(w, `[{"id":"site1","name":"demo","subdomain_slug":"demo"}]`)
		case "/api/v1/sites/site1/worker/env/validate":
			validations++
			body, _ := io.ReadAll(r.Body)
			if r.Method != "POST" || string(body) != `{"schema":{"TASK13_VALUE":{"type":"number","required":true}}}` || r.Header.Get("Authorization") != "Bearer test-key" {
				t.Error("remote validation did not send only canonical declarations with resolved credentials")
			}
			w.WriteHeader(status)
			if status == 200 {
				fmt.Fprintf(w, `{"valid":%t,"names":["TASK13_VALUE"]}`, valid)
			} else {
				fmt.Fprintf(w, `{"error":%q}`, marker)
			}
		default:
			t.Error("unexpected request or mutation")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	t.Setenv("HOSTEDAT_SERVER", server.URL)
	t.Setenv("HOSTEDAT_API_KEY", "test-key")
	flagAPIKey, flagServer = "", ""
	args := []string{"check", "--remote", "--site", "demo", "--allow-insecure-loopback"}
	out, err := executeEnv(args...)
	if err != nil || !strings.Contains(out, "Remote environment valid") || validations != 1 {
		t.Fatalf("remote check failed: %v", err)
	}
	for _, failure := range []int{200, 400} {
		status, valid = failure, false
		out, err := executeEnv(args...)
		if err == nil || strings.Contains(out+err.Error(), marker) {
			t.Fatal("remote rejection accepted or disclosed a value")
		}
	}
	if validations != 3 {
		t.Fatal("missing remote validations")
	}
}
