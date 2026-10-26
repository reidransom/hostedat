package api

import (
	"github.com/cryguy/hostedat/internal/environment"
	"github.com/cryguy/hostedat/internal/models"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testEnvironmentService(t *testing.T, db *gorm.DB) *environment.Service {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, []byte(`{"active":"test","keys":{"test":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}}`), 0600); err != nil {
		t.Fatal(err)
	}
	keys, err := environment.LoadKeys(path)
	if err != nil {
		t.Fatal(err)
	}
	return &environment.Service{DB: db, Keys: keys}
}

func migrateTestEnvironment(t *testing.T, service *environment.Service) {
	t.Helper()
	if _, err := environment.Maintain(service.DB, service.Keys, service.Keys, false); err != nil {
		t.Fatal(err)
	}
}

func TestServingEncryptedEnvironmentFailsClosed(t *testing.T) {
	env := setupTestEnvWithWorker(t)
	user, token := env.createTestUser(t, "encrypted@test.local", "password123", "user")
	version, deploy := 1, "encrypted-deploy"
	site := models.Site{UserID: user.ID, SubdomainSlug: "encrypted", Name: "Encrypted", HasWorker: true, ActiveVersion: &version, ActiveDeployID: &deploy}
	if err := env.db.Create(&site).Error; err != nil {
		t.Fatal(err)
	}
	env.deployWorkerSite(t, site.ID, deploy, "encrypted", `export default { fetch(request, env) { return new Response(typeof env.TOKEN === "string" && env.TOKEN === request.headers.get("x-expected") ? "binding-ok" : "binding-bad"); } }`)
	base := "/api/v1/sites/" + site.ID + "/worker/env"
	put := func(body string, intended int) {
		req := httptest.NewRequest(http.MethodPost, base, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := env.doRequest(req)
		if rec.Code != intended || strings.Contains(rec.Body.String(), "disposable-marker") {
			t.Fatalf("write status=%d", rec.Code)
		}
	}
	fetch := func(expected string, intended int) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = "encrypted.test.local"
		req.Header.Set("x-expected", expected)
		rec := env.doRequest(req)
		if rec.Code != intended || (intended == 200 && rec.Body.String() != "binding-ok") {
			t.Fatalf("fetch status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
	put(`{"name":"TOKEN","secret":true,"value":"disposable-marker-one"}`, 200)
	fetch("disposable-marker-one", 200)
	put(`{"name":"TOKEN","value":"disposable-marker-two"}`, 200)
	fetch("disposable-marker-two", 200)
	put(`{"name":"TOKEN"}`, 400)
	var row models.WorkerEnvVar
	env.db.Where("site_id = ?", site.ID).First(&row)
	if !row.Secret || row.Value != "" || row.SecretKeyID == "" {
		t.Fatal("secret was downgraded or left plaintext")
	}
	if err := env.db.Model(&row).Update("ciphertext", []byte("tampered")).Error; err != nil {
		t.Fatal(err)
	}
	fetch("disposable-marker-two", 500)
}
