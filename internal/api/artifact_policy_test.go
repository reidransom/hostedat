package api

import (
	"archive/zip"
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cryguy/hostedat/internal/models"
)

func uploadPolicyZip(t *testing.T, env *testEnv, token, siteID string, archive []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", "artifact.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(part, bytes.NewReader(archive)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sites/"+siteID+"/deploy", &body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return env.doRequest(req)
}

func TestArtifactRawUploads(t *testing.T) {
	env := setupTestEnv(t)
	user, token := env.createTestUser(t, "artifact@test.local", "password123", "user")
	site := models.Site{UserID: user.ID, Name: "Artifact", SubdomainSlug: "artifact"}
	if err := env.db.Create(&site).Error; err != nil {
		t.Fatal(err)
	}
	good := createTestZip(t, map[string]string{"dist/index.html": "legacy-safe", "dist/_hostedat.env.json": "{}"})
	rec := uploadPolicyZip(t, env, token, site.ID, good.Bytes())
	if rec.Code != 201 {
		t.Fatalf("wrapped manifest: %d %s", rec.Code, rec.Body.String())
	}
	var dep models.Deployment
	if err := env.db.First(&dep, "site_id = ?", site.ID).Error; err != nil {
		t.Fatal(err)
	}
	if dep.EnvDeclaration == nil || *dep.EnvDeclaration != "{}" {
		t.Fatal("canonical snapshot lost")
	}
	if _, err := os.Stat(filepath.Join(env.store.GetDeploymentPath(site.ID, dep.ID), "_hostedat.env.json")); !os.IsNotExist(err) {
		t.Fatal("manifest is public")
	}
	for _, names := range [][]string{{".ENV/visible.txt"}, {"dist/.env"}, {"../escape"}, {"/absolute"}, {"C:/drive"}, {"dir\\file"}, {"index.html", "nested/_hostedat.env.json"}, {"dist/a", "dist/./a"}, {"_hostedat.env.json", "_hostedat.env.json"}} {
		var b bytes.Buffer
		w := zip.NewWriter(&b)
		for _, name := range names {
			f, err := w.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write([]byte("{}")); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		rec := uploadPolicyZip(t, env, token, site.ID, b.Bytes())
		if rec.Code != 400 {
			t.Fatalf("attack accepted: %v status=%d", names, rec.Code)
		}
	}
	// A stored entry makes its payload easy to corrupt deterministically.
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	f, err := w.CreateHeader(&zip.FileHeader{Name: "safe.txt", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("payload-to-corrupt")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	corrupt := b.Bytes()
	corrupt[bytes.Index(corrupt, []byte("payload-to-corrupt"))] ^= 1
	if rec := uploadPolicyZip(t, env, token, site.ID, corrupt); rec.Code != 400 {
		t.Fatal("corruption accepted")
	}
	entries, err := os.ReadDir(filepath.Join(env.store.BasePath, site.ID))
	if err != nil || len(entries) != 1 || entries[0].Name() != dep.ID {
		t.Fatal("failed uploads left staged assets")
	}
	var count int64
	if err := env.db.Model(&models.Deployment{}).Where("site_id = ?", site.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatal("failed upload changed deployments")
	}
}

func TestArtifactPublicServing(t *testing.T) {
	env := setupTestEnv(t)
	user, _ := env.createTestUser(t, "historic@test.local", "password123", "user")
	version, key := 1, "historic"
	site := models.Site{UserID: user.ID, Name: "Historic", SubdomainSlug: "historic", SPAMode: true, ActiveVersion: &version, ActiveDeployID: &key}
	if err := env.db.Create(&site).Error; err != nil {
		t.Fatal(err)
	}
	root := env.store.GetDeploymentPath(site.ID, key)
	for name, data := range map[string]string{"index.html": "safe-spa", ".env": "private-marker", "nested/.ENV.production": "private-marker", "_hostedat.env.json": "private-marker", "_redirects": "/rewrite /.env 200\n/meta /nested/../_hostedat.env.json 200\n"} {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/.env", "/%2eenv", "/nested/.ENV.production", "/_hostedat.env.json", "/./_hostedat.env.json", "/a/../.env", "/rewrite", "/meta"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "historic." + env.domain
		rec := env.doRequest(req)
		if rec.Code != 404 || strings.Contains(rec.Body.String(), "private-marker") {
			t.Errorf("public path %s returned %d %s", path, rec.Code, rec.Body.String())
		}
	}
	for _, path := range []string{"/", "/normal-spa"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "historic." + env.domain
		if rec := env.doRequest(req); rec.Code != 200 || rec.Body.String() != "safe-spa" {
			t.Fatal("legacy SPA broken")
		}
	}
}

// This test invokes the actual default engine; run without -race because the
// upstream QuickJS runtime is not compatible with Go checkptr instrumentation.
func TestArtifactWorkerAssetsRealEngine(t *testing.T) {
	env := setupTestEnvWithWorker(t)
	user, token := env.createTestUser(t, "assets@test.local", "password123", "user")
	site := models.Site{UserID: user.ID, Name: "Assets", SubdomainSlug: "assets", SPAMode: true}
	if err := env.db.Create(&site).Error; err != nil {
		t.Fatal(err)
	}
	source := `export default { async fetch(req, env) { const u = new URL(req.url); u.pathname = u.searchParams.get("asset") || "/safe.txt"; u.search = ""; return env.ASSETS.fetch(new Request(u.toString())); } }`
	archive := createTestZip(t, map[string]string{"dist/_worker.js": source, "dist/safe.txt": "assets-safe", "dist/index.html": "safe-spa", "dist/_hostedat.env.json": "{}", "dist/_redirects": "/rewrite /.env 200\n/meta /_hostedat.env.json 200\n"})
	if rec := uploadPolicyZip(t, env, token, site.ID, archive.Bytes()); rec.Code != 201 {
		t.Fatalf("worker deploy: %d %s", rec.Code, rec.Body.String())
	}
	if err := env.db.First(&site, "id = ?", site.ID).Error; err != nil {
		t.Fatal(err)
	}
	root := env.store.GetDeploymentPath(site.ID, *site.ActiveDeployID)
	for _, name := range []string{".env", "_hostedat.env.json"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("historic-private-marker"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, asset := range []string{".env", "%2eenv", "_hostedat.env.json", "./_hostedat.env.json", "rewrite", "meta"} {
		req := httptest.NewRequest(http.MethodGet, "/?asset=/"+asset, nil)
		req.Host = "assets." + env.domain
		rec := env.doRequest(req)
		if rec.Code != 404 || strings.Contains(rec.Body.String(), "historic-private-marker") {
			t.Fatalf("ASSETS %s returned %d %s", asset, rec.Code, rec.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "assets." + env.domain
	if rec := env.doRequest(req); rec.Code != 200 || rec.Body.String() != "assets-safe" {
		t.Fatalf("real ASSETS safe fetch: %d %s", rec.Code, rec.Body.String())
	}
}
