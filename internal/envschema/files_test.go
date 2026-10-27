package envschema

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeInput(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryDefaultsAndConflict(t *testing.T) {
	for _, filename := range []string{"env.json", "env.yml"} {
		t.Run(filename, func(t *testing.T) {
			dir := t.TempDir()
			content := `{"A":{"type":"string"}}`
			format := JSON
			if filename == "env.yml" {
				content, format = "A: {type: string}", YAML
			}
			writeInput(t, filepath.Join(dir, filename), content)
			// Discovery must not read even invalid local dotenv contents.
			writeInput(t, filepath.Join(dir, ".env"), "invalid local dotenv")
			project, err := Discover(dir, "")
			if err != nil {
				t.Fatal(err)
			}
			if project.SchemaPath != filepath.Join(dir, filename) || project.Format != format || project.DotenvPath != filepath.Join(dir, ".env") {
				t.Fatal("incorrect default selection/root")
			}
			if !reflect.DeepEqual(project.Schema, Schema{"A": {Type: String, Required: true}}) {
				t.Fatal("selected declaration was not parsed")
			}
			other := "env.yml"
			if filename == other {
				other = "env.json"
			}
			writeInput(t, filepath.Join(dir, other), "not parsed unless selected")
			if _, err := Discover(dir, ""); err == nil {
				t.Fatal("both defaults did not require explicit selection")
			}
			if _, err := Discover(dir, filename); err != nil {
				t.Fatal("explicit selection did not resolve conflict", err)
			}
		})
	}
}

func TestDiscoveryIsInvocationScopedSnapshot(t *testing.T) {
	parent := t.TempDir()
	writeInput(t, filepath.Join(parent, "env.json"), `{"PARENT":{"type":"string"}}`)
	dir := filepath.Join(parent, "child")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	project, err := Discover(dir, "")
	if err != nil || project.Schema != nil || project.SchemaPath != "" || project.DotenvPath != filepath.Join(dir, ".env") {
		t.Fatal("discovery searched parent or lost absent-schema state")
	}
	if _, err := project.LocalValues(nil); err == nil {
		t.Fatal("schema-less project accepted a local check")
	}
	writeInput(t, filepath.Join(dir, "selected.yml"), "A: {type: string}")
	project, err = Discover(parent, "child/selected.yml")
	if err != nil || project.DotenvPath != filepath.Join(dir, ".env") {
		t.Fatal("explicit relative schema lost associated root", err)
	}
	writeInput(t, filepath.Join(dir, "selected.yml"), "invalid later contents")
	writeInput(t, filepath.Join(dir, "env.json"), "invalid later default")
	writeInput(t, filepath.Join(dir, ".env"), "A=retained")
	t.Chdir(t.TempDir())
	values, err := project.LocalValues(nil)
	if err != nil || project.Schema.Validate(values) != nil {
		t.Fatal("resolved snapshot changed with disk schema or working directory", err)
	}
	project, err = Discover(parent, filepath.Join(parent, "env.json"))
	if err != nil || project.SchemaPath != filepath.Join(parent, "env.json") {
		t.Fatal("absolute schema path failed", err)
	}
}

func TestDiscoveryFailuresAreSafe(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{"INPUT_SECRET_MARKER.json", "INPUT_SECRET_MARKER.yaml", "INPUT_SECRET_MARKER.ts"} {
		_, err := Discover(dir, path)
		if err == nil || strings.Contains(err.Error(), "INPUT_SECRET_MARKER") {
			t.Fatal("explicit missing/unsupported selection was not safely rejected")
		}
	}
	writeInput(t, filepath.Join(dir, "malformed.json"), `{"A":{"type":"INPUT_SECRET_MARKER"}}`)
	if _, err := Discover(dir, "malformed.json"); err == nil || strings.Contains(err.Error(), "INPUT_SECRET_MARKER") {
		t.Fatal("malformed selected declaration was not safely rejected")
	}
	if err := os.Mkdir(filepath.Join(dir, "env.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(dir, ""); err == nil {
		t.Fatal("default directory was accepted as a schema")
	}
	if _, err := Discover(filepath.Join(dir, "missing"), ""); err == nil {
		t.Fatal("missing invocation directory accepted")
	}
	broken := t.TempDir()
	if err := os.Symlink("missing", filepath.Join(broken, "env.yml")); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(broken, ""); err == nil {
		t.Fatal("dangling default schema was silently ignored")
	}
}

func TestLocalOverlayPreservesEmptyAndDoesNotMutate(t *testing.T) {
	dir := t.TempDir()
	writeInput(t, filepath.Join(dir, "env.json"), `{"A":{"type":"string"},"B":{"type":"number","required":false},"C":{"type":"string","required":false}}`)
	project, err := Discover(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	values, err := project.LocalValues(nil)
	if err != nil || len(values) != 0 {
		t.Fatal("missing local dotenv was not treated as absent", err)
	}
	if err := project.Schema.Validate(values); err == nil {
		t.Fatal("missing required local value accepted")
	}
	t.Setenv("A", "")
	t.Setenv("C", "  from shell  ")
	t.Setenv("UNDECLARED", "must-not-overlay")
	const local = "A=from-file\nB=12\nUNDECLARED=from-file\n"
	writeInput(t, project.DotenvPath, local)
	before, err := os.Stat(project.DotenvPath)
	if err != nil {
		t.Fatal(err)
	}
	values, err = project.LocalValues(os.LookupEnv)
	if err != nil || values["A"] != "" || values["C"] != "  from shell  " || values["B"] != "12" || values["UNDECLARED"] != "from-file" {
		t.Fatal("local shell overlay lost explicit emptiness/whitespace or overlaid unrelated values", err)
	}
	if err := project.Schema.Validate(values); err == nil {
		t.Fatal("empty shell override did not invalidate required value")
	}
	if os.Getenv("A") != "" {
		t.Fatal("local helper modified process environment")
	}
	after, err := os.Stat(project.DotenvPath)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(project.DotenvPath)
	if err != nil || string(contents) != local || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("local helper changed dotenv contents/metadata")
	}
	writeInput(t, project.DotenvPath, "A='INPUT_SECRET_MARKER")
	if _, err := project.LocalValues(nil); err == nil || strings.Contains(err.Error(), "INPUT_SECRET_MARKER") {
		t.Fatal("malformed local dotenv was not safely rejected")
	}
}

func TestSyncRejectsLocalDotenvAliases(t *testing.T) {
	dir := t.TempDir()
	project, err := Discover(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	writeInput(t, project.DotenvPath, "A=local-only")
	if err := os.Symlink(project.DotenvPath, filepath.Join(dir, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(project.DotenvPath, filepath.Join(dir, "hardlink.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, filepath.Join(dir, "dir-alias")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{".env", "./.env", "unused/../.env", project.DotenvPath, "alias.txt", "hardlink.txt", "dir-alias/.env"} {
		if got, err := project.ReadSyncSource(path); err == nil || got != nil {
			t.Fatal("local dotenv alias accepted as sync source:", path)
		}
	}
	for _, path := range []string{"", "-", "missing.txt", "."} {
		if _, err := project.ReadSyncSource(path); err == nil {
			t.Fatal("invalid sync source accepted")
		}
	}
	t.Setenv("A", "shell-must-not-overlay")
	writeInput(t, filepath.Join(dir, "production.txt"), "A=explicit-source")
	values, err := project.ReadSyncSource("production.txt")
	if err != nil || values["A"] != "explicit-source" {
		t.Fatal("named sync source was not independent of shell", err)
	}
	if err := os.Remove(project.DotenvPath); err != nil {
		t.Fatal(err)
	}
	// A symlinked local .env must protect its target as well.
	if err := os.Symlink(filepath.Join(dir, "production.txt"), project.DotenvPath); err != nil {
		t.Fatal(err)
	}
	if _, err := project.ReadSyncSource("production.txt"); err == nil {
		t.Fatal("local dotenv symlink target accepted for sync")
	}
}

func TestSchemaSymlinkKeepsNamedRootAndSyncProtection(t *testing.T) {
	target, projectDir := t.TempDir(), t.TempDir()
	writeInput(t, filepath.Join(target, "shared.json"), `{}`)
	if err := os.Symlink(filepath.Join(target, "shared.json"), filepath.Join(projectDir, "env.json")); err != nil {
		t.Fatal(err)
	}
	project, err := Discover(projectDir, "")
	if err != nil || project.Schema == nil || project.DotenvPath != filepath.Join(projectDir, ".env") {
		t.Fatal("schema symlink changed named project root", err)
	}
	writeInput(t, project.DotenvPath, "A=local")
	if _, err := project.ReadSyncSource(project.DotenvPath); err == nil {
		t.Fatal("schema symlink bypassed local dotenv protection")
	}
	if err := os.Remove(project.DotenvPath); err != nil {
		t.Fatal(err)
	}
	values, err := project.LocalValues(nil)
	if err != nil || len(values) != 0 {
		t.Fatal("explicit empty declaration was confused with absence", err)
	}
	if _, err := os.Stat(project.DotenvPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("local helper created missing dotenv")
	}
}
