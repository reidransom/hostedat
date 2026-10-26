package storage

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cryguy/hostedat/internal/artifactpolicy"
)

func policyZip(t *testing.T, names []string, mode os.FileMode) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, name := range names {
		h := &zip.FileHeader{Name: name, Method: zip.Store}
		h.SetMode(mode)
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte("artifact-marker")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestArtifactArchivePreflight(t *testing.T) {
	for _, names := range [][]string{
		{".env/visible.txt"}, {".ENV.local/visible.txt"}, {"dist/.EnV"},
		{"index.html", "assets/.env.production"}, {"../escape.txt"}, {"dist/../escape.txt"},
		{"/absolute.txt"}, {"C:/drive.txt"}, {"C:relative.txt"}, {"//server/share.txt"},
		{"dir\\file.txt"}, {"null\x00file.txt"},
		{"dist/file.txt", "dist/file.txt"}, {"dist/file.txt", "dist/./file.txt"},
		{"dist/a//b.txt", "dist/a/b.txt"}, {"dist", "dist/file.txt"},
		{"index.html", "dir/_hostedat.env.json"}, {"_HOSTEDAT.ENV.JSON"},
		{"_hostedat.env.json/visible.txt"},
		{"_hostedat.env.json", "_hostedat.env.json"},
	} {
		t.Run(strings.Join(names, "+"), func(t *testing.T) {
			m := NewManager(t.TempDir())
			data := policyZip(t, names, 0644)
			if _, err := m.ExtractZip("site", "deploy", bytes.NewReader(data), int64(len(data))); err == nil {
				t.Fatal("unsafe archive accepted")
			}
			if _, err := os.Stat(filepath.Join(m.BasePath, "site")); !os.IsNotExist(err) {
				t.Fatal("preflight wrote deployment files")
			}
		})
	}
	for _, mode := range []os.FileMode{os.ModeSymlink | 0777, os.ModeNamedPipe | 0600, os.ModeDevice | 0600} {
		m := NewManager(t.TempDir())
		data := policyZip(t, []string{"bad"}, mode)
		if _, err := m.ExtractZip("site", "deploy", bytes.NewReader(data), int64(len(data))); err == nil {
			t.Fatal("unsupported file type accepted")
		}
	}
}

func TestArtifactManifestIsPrivateCanonicalMetadata(t *testing.T) {
	for _, prefix := range []string{"", "dist/", "./"} {
		m := NewManager(t.TempDir())
		data := createTestZip(t, map[string]string{
			prefix + "index.html":               "safe",
			prefix + artifactpolicy.EnvManifest: `{"TOKEN":{"type":"string","required":false}}`,
		})
		declaration, err := m.ExtractZip("site", "deploy", bytes.NewReader(data.Bytes()), int64(data.Len()))
		if err != nil || declaration == nil || *declaration != `{"TOKEN":{"type":"string","required":false}}` {
			t.Fatalf("canonical metadata missing: %v", err)
		}
		if _, err := os.Stat(filepath.Join(m.GetDeploymentPath("site", "deploy"), artifactpolicy.EnvManifest)); !os.IsNotExist(err) {
			t.Fatal("metadata extracted as a public asset")
		}
		if _, ok := m.ResolveFile(m.GetDeploymentPath("site", "deploy"), "/"); !ok {
			t.Fatal("safe asset missing")
		}
	}
	for _, source := range []string{`{"TOKEN":{"type":"MARKER"}}`, `{"TOKEN":{},"TOKEN":{}}`, strings.Repeat(" ", (1<<20)+1)} {
		m := NewManager(t.TempDir())
		data := createTestZip(t, map[string]string{"index.html": "safe", artifactpolicy.EnvManifest: source})
		if _, err := m.ExtractZip("site", "deploy", bytes.NewReader(data.Bytes()), int64(data.Len())); err == nil || strings.Contains(err.Error(), "MARKER") {
			t.Fatal("invalid metadata was not rejected safely")
		}
		if _, err := os.Stat(m.GetDeploymentPath("site", "deploy")); !os.IsNotExist(err) {
			t.Fatal("metadata failure wrote files")
		}
	}
	for _, source := range []*string{nil, new(string)} {
		files := map[string]string{"index.html": "safe"}
		if source != nil {
			files[artifactpolicy.EnvManifest] = "{}"
		}
		data := createTestZip(t, files)
		m := NewManager(t.TempDir())
		got, err := m.ExtractZip("site", "deploy", bytes.NewReader(data.Bytes()), int64(data.Len()))
		if err != nil || (got == nil) != (source == nil) || (got != nil && *got != "{}") {
			t.Fatal("absent/empty distinction lost")
		}
	}
}

func TestArtifactExtractionFailureCleanup(t *testing.T) {
	m := NewManager(t.TempDir())
	data := policyZip(t, []string{"first.txt", "second.txt"}, 0644)
	// Corrupt the last payload, preserving the directory/header so extraction
	// writes the first file before the second file's real CRC check fails.
	pos := bytes.LastIndex(data, []byte("artifact-marker"))
	data[pos] ^= 1
	if _, err := m.ExtractZip("site", "deploy", bytes.NewReader(data), int64(len(data))); err == nil {
		t.Fatal("corrupt payload accepted")
	}
	if _, err := os.Stat(m.GetDeploymentPath("site", "deploy")); !os.IsNotExist(err) {
		t.Fatal("partial files survived")
	}

	// Header-reported oversize is rejected before any writes.
	data = policyZip(t, []string{"large.txt"}, 0644)
	pos = bytes.Index(data, []byte{'P', 'K', 1, 2})
	binary.LittleEndian.PutUint32(data[pos+24:pos+28], maxFileSize+1)
	if _, err := m.ExtractZip("site", "large", bytes.NewReader(data), int64(len(data))); err == nil {
		t.Fatal("oversize accepted")
	}
	if _, err := os.Stat(m.GetDeploymentPath("site", "large")); !os.IsNotExist(err) {
		t.Fatal("oversize wrote files")
	}
}

func TestArtifactResolvedHistoricPolicy(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{".env", ".ENV.local", "nested/.env.production", "_hostedat.env.json", "nested/_HOSTEDAT.ENV.JSON", "index.html", "about.html"} {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("historic-marker"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(".env", filepath.Join(dir, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	m := NewManager("")
	for _, name := range []string{"/.env", "/.ENV.local", "/nested/.env.production", "/_hostedat.env.json", "/nested/_HOSTEDAT.ENV.JSON", "/safe/../.env", "//.env", "/./_hostedat.env.json", "/alias.txt"} {
		if _, ok := m.ResolveFile(dir, name); ok {
			t.Errorf("historic asset resolved: %s", name)
		}
	}
	for _, name := range []string{"/", "/about", "/safe/../about.html"} {
		if _, ok := m.ResolveFile(dir, name); !ok {
			t.Errorf("safe legacy path blocked: %s", name)
		}
	}
}

func TestArtifactArchiveAggregateAndCountLimits(t *testing.T) {
	for _, count := range []int{11, maxFileCount + 1} {
		names := make([]string, count)
		for i := range names {
			names[i] = fmt.Sprintf("file-%d", i)
		}
		data := policyZip(t, names, 0644)
		if count == 11 {
			for offset := 0; ; {
				pos := bytes.Index(data[offset:], []byte{'P', 'K', 1, 2})
				if pos < 0 {
					break
				}
				pos += offset
				binary.LittleEndian.PutUint32(data[pos+24:pos+28], maxFileSize)
				offset = pos + 46
			}
		}
		m := NewManager(t.TempDir())
		if _, err := m.ExtractZip("site", "deploy", bytes.NewReader(data), int64(len(data))); err == nil {
			t.Fatal("archive bound accepted")
		}
		if _, err := os.Stat(filepath.Join(m.BasePath, "site")); !os.IsNotExist(err) {
			t.Fatal("archive bound failure wrote files")
		}
	}
}
