package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cryguy/hostedat/internal/environment"
	"github.com/spf13/cobra"
)

func executeSecret(args []string, input io.Reader) (string, string, error) {
	cmd := secretCmd()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetIn(input)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), stderr.String(), err
}

func TestSecretInputBoundaries(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"  token \t\n", "  token \t"}, {"a\r\nb\r\n", "a\r\nb"}, {"a\n\n", "a\n"}, {"a\r", "a\r"}, {"", ""},
		{strings.Repeat("x", environment.MaxValueBytes) + "\r\n", strings.Repeat("x", environment.MaxValueBytes)},
	} {
		cmd := &cobra.Command{}
		cmd.SetIn(strings.NewReader(tc.input))
		got, err := readSecret(cmd, true)
		if err != nil || got != tc.want {
			t.Fatal("stdin delimiter or whitespace contract failed")
		}
	}
	cmd := &cobra.Command{}
	cmd.SetIn(strings.NewReader(strings.Repeat("x", environment.MaxValueBytes+1)))
	if _, err := readSecret(cmd, true); err == nil {
		t.Fatal("unbounded secret accepted")
	}
	cmd.SetIn(strings.NewReader("not-a-terminal"))
	if _, err := readSecret(cmd, false); err == nil || !strings.Contains(err.Error(), "--stdin") {
		t.Fatal("non-TTY must explicitly opt in")
	}
}

func TestSecretCommands(t *testing.T) {
	t.Chdir(t.TempDir())
	const marker = "private-marker-not-for-output"
	entries := map[string]map[string]any{
		"ZED":   {"id": "z", "site_id": "site1", "name": "ZED", "secret": true, "value": marker},
		"ALPHA": {"id": "a", "site_id": "site1", "name": "ALPHA", "secret": true, "value": marker},
		"PLAIN": {"id": "p", "site_id": "site1", "name": "PLAIN", "secret": false, "value": marker},
	}
	var writes, deletes int
	var last map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/version":
			fmt.Fprint(w, `{"capabilities":["secrets-env-v1"]}`)
		case r.URL.Path == "/api/v1/sites":
			if r.Header.Get("Authorization") != "Bearer test-key" {
				t.Error("credential resolution was not reused")
			}
			fmt.Fprint(w, `[{"id":"site1","name":"demo","subdomain_slug":"demo"}]`)
		case r.Method == "GET" && r.URL.Path == "/api/v1/sites/site1/worker/env":
			values := make([]map[string]any, 0, len(entries))
			for _, entry := range entries {
				values = append(values, entry)
			}
			json.NewEncoder(w).Encode(values)
		case r.Method == "POST":
			writes++
			json.NewDecoder(r.Body).Decode(&last)
			if strings.HasSuffix(r.URL.Path, "/bulk") {
				fmt.Fprint(w, `[]`)
			} else {
				fmt.Fprintf(w, `{"id":"a","name":"ALPHA","secret":true,"value":%q}`, marker)
			}
		case r.Method == "DELETE":
			deletes++
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	t.Setenv("HOSTEDAT_API_KEY", "test-key")
	t.Setenv("HOSTEDAT_SERVER", server.URL)
	flagAPIKey, flagServer = "", ""
	run := func(args []string, input string) (string, error) {
		t.Helper()
		out, stderr, err := executeSecret(append(args, "--allow-insecure-loopback"), strings.NewReader(input))
		if strings.Contains(out+stderr, marker) || (err != nil && strings.Contains(err.Error(), marker)) {
			t.Fatal("secret leaked")
		}
		return out, err
	}
	if _, err := run([]string{"put", "demo", "ALPHA", "--stdin"}, "  "+marker+"\n\n"); err != nil {
		t.Fatal(err)
	}
	if last["value"] != "  "+marker+"\n" || last["secret"] != true || last["name"] != "ALPHA" {
		t.Fatal("put changed value or classification")
	}
	if _, err := run([]string{"put", "demo", "ALPHA"}, marker); err == nil {
		t.Fatal("non-TTY prompted")
	}
	out, err := run([]string{"list", "demo", "--json"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var metadata []map[string]any
	if err := json.Unmarshal([]byte(out), &metadata); err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 2 || metadata[0]["name"] != "ALPHA" || metadata[1]["name"] != "ZED" {
		t.Fatal("list not secret-only sorted")
	}
	for _, entry := range metadata {
		if _, ok := entry["value"]; ok {
			t.Fatal("value in JSON metadata")
		}
	}
	before := writes
	if err := os.WriteFile("env.json", []byte(`{"ALPHA":{"type":"string"},"COUNT":{"type":"number"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("production.txt", []byte("ALPHA=ok\nCOUNT=bad\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := run([]string{"sync", "demo", "production.txt"}, ""); err == nil || writes != before {
		t.Fatal("invalid sync mutated")
	}
	if err := os.WriteFile("production.txt", []byte("ALPHA=ok\nCOUNT=7\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := run([]string{"sync", "demo", "production.txt"}, ""); err != nil {
		t.Fatal(err)
	}
	if writes != before+1 || len(last["entries"].([]any)) != 2 || last["schema"] == nil {
		t.Fatal("sync must submit one schema-backed batch")
	}
	if _, err := run([]string{"sync", "demo", ".env"}, ""); err == nil {
		t.Fatal("local dotenv accepted")
	}
	if _, err := run([]string{"delete", "demo", "PLAIN", "--yes"}, ""); err == nil || deletes != 0 {
		t.Fatal("plaintext deletion allowed")
	}
	if out, err := run([]string{"delete", "demo", "ALPHA"}, "no\n"); err != nil || !strings.Contains(out, "Aborted") || deletes != 0 {
		t.Fatal("confirmation refusal ignored")
	}
	if _, err := run([]string{"delete", "demo", "ALPHA"}, "Y\n"); err != nil || deletes != 1 {
		t.Fatal("confirmation not accepted")
	}
	if _, err := run([]string{"delete", "demo", "ZED", "--yes"}, ""); err != nil || deletes != 2 {
		t.Fatal("yes flag failed")
	}
	if _, err := run([]string{"put", "absent", "ALPHA", "--stdin"}, marker); err == nil {
		t.Fatal("missing site accepted")
	}
	t.Setenv("HOME", filepath.Join(t.TempDir(), "empty-home"))
	t.Setenv("HOSTEDAT_API_KEY", "")
	if _, err := run([]string{"put", "demo", "ALPHA", "--stdin"}, marker); err == nil || !strings.Contains(err.Error(), "not authenticated") {
		t.Fatal("missing auth not diagnosed")
	}
}

func TestSecretSourceValidationBeforeNetwork(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("HOSTEDAT_API_KEY", "test")
	t.Setenv("HOSTEDAT_SERVER", "https://unreachable.invalid")
	if err := os.WriteFile("env.yml", []byte("TOKEN:\n  type: string\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"TOKEN=one\nTOKEN=two\n", "TOKEN=ok\nUNDECLARED=bad\n", "ASSETS=bad\n", "TOKEN='unterminated\n", ""} {
		if err := os.WriteFile("production.txt", []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		_, _, err := executeSecret([]string{"sync", "demo", "production.txt"}, strings.NewReader(""))
		if err == nil || strings.Contains(err.Error(), "support") {
			t.Fatal("source validation did not finish before network")
		}
	}
}
