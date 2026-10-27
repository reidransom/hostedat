package api

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cryguy/hostedat/internal/environment"
	"github.com/cryguy/hostedat/internal/models"
	"github.com/cryguy/hostedat/internal/storage"
)

func TestDeclarationDeployRollback(t *testing.T) {
	env := setupTestEnv(t)
	user, token := env.createTestUser(t, "declaration@test.local", "password123", "user")
	site := models.Site{UserID: user.ID, Name: "Declarations", SubdomainSlug: "declarations"}
	if err := env.db.Create(&site).Error; err != nil {
		t.Fatal(err)
	}
	upload := func(schema *string, want int) {
		t.Helper()
		files := map[string]string{"index.html": "safe"}
		if schema != nil {
			files["_hostedat.env.json"] = *schema
		}
		rec := uploadPolicyZip(t, env, token, site.ID, createTestZip(t, files).Bytes())
		if rec.Code != want {
			t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
		}
	}
	rollback := func(version, want int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/sites/%s/deployments/%d/rollback", site.ID, version), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := env.doRequest(req)
		if rec.Code != want {
			t.Fatalf("rollback: %d %s", rec.Code, rec.Body.String())
		}
	}
	service := testEnvironmentService(t, env.db)
	set := func(value string, secret bool, want bool) {
		t.Helper()
		_, err := service.Set(site.ID, environment.Write{Name: "COUNT", Value: &value, Secret: &secret})
		if (err == nil) != want {
			t.Fatalf("set success=%v expected=%v", err == nil, want)
		}
	}
	upload(nil, 201) // Legacy v1.
	schema := `{"COUNT":{"type":"number"}}`
	upload(&schema, 400)
	set("3", false, true)
	upload(&schema, 400) // A plaintext binding cannot satisfy a declaration.
	set("not-a-number", true, true)
	upload(&schema, 400)
	set("3", true, true)
	upload(&schema, 201) // Enforced v2.
	var current models.Site
	if err := env.db.First(&current, "id = ?", site.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !current.EnvDeclarationsEnforced || *current.ActiveVersion != 2 {
		t.Fatal("enforcement not activated")
	}
	upload(nil, 400)
	rollback(1, 400)
	set("bad", true, false)
	empty := `{}`
	upload(&empty, 201) // Explicit empty v3 removes requirements, not enforcement.
	set("changed-invalid", true, true)
	rollback(2, 400)
	set("7", true, true)
	rollback(2, 200)
	_, secrets, err := service.Load(site.ID)
	if err != nil || secrets["COUNT"] != "7" {
		t.Fatal("rollback did not retain current secret")
	}
	entries, err := os.ReadDir(filepath.Join(env.store.BasePath, site.ID))
	if err != nil || len(entries) != 3 {
		t.Fatalf("failed activations left assets: %v count=%d", err, len(entries))
	}
	var count int64
	if err := env.db.Model(&models.Deployment{}).Where("site_id = ?", site.ID).Count(&count).Error; err != nil || count != 3 {
		t.Fatal("failed activation changed deployment history")
	}
}

type declarationRaceCompiler struct {
	compile     func()
	invalidated []string
}

func (e *declarationRaceCompiler) CompileAndCache(_, _ string, _ string) ([]byte, error) {
	e.compile()
	return []byte("compiled"), nil
}
func (e *declarationRaceCompiler) InvalidatePool(_, id string) {
	e.invalidated = append(e.invalidated, id)
}

// The second connection commits after preflight/compilation and before final
// activation. The server must reject it and retire staged bytecode and pools.
func TestDeclarationActivationRechecksConcurrentMutation(t *testing.T) {
	for _, operation := range []string{"delete", "update", "binding"} {
		t.Run(operation, func(t *testing.T) {
			a, b, site := coordinatedHandlerDBs(t)
			service := testEnvironmentService(t, a)
			other := &environment.Service{DB: b, Keys: service.Keys}
			value, secret := "4", true
			row, err := service.Set(site.ID, environment.Write{Name: "COUNT", Value: &value, Secret: &secret})
			if err != nil {
				t.Fatal(err)
			}
			store := storage.NewManager(t.TempDir())
			engine := &declarationRaceCompiler{compile: func() {
				var err error
				switch operation {
				case "delete":
					_, err = other.Delete(site.ID, row.ID)
				case "update":
					v := "bad"
					_, err = other.Set(site.ID, environment.Write{Name: "COUNT", Value: &v})
				case "binding":
					_, err = other.Delete(site.ID, row.ID)
					if err == nil {
						err = b.Create(&models.KVNamespace{SiteID: site.ID, Name: "COUNT"}).Error
					}
				}
				if err != nil {
					t.Fatal(err)
				}
			}}
			archive := createTestZip(t, map[string]string{"_worker.js": "export default {fetch(){return new Response('safe')}}", "_hostedat.env.json": `{"COUNT":{"type":"number"}}`})
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, err := writer.CreateFormFile("file", "worker.zip")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write(archive.Bytes()); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/", &body)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			rec := httptest.NewRecorder()
			if err := (&DeployHandler{DB: a, Storage: store, Environment: service, WorkerEngine: engine}).Deploy(coordinatedContext(site, req, rec)); err != nil {
				t.Fatal(err)
			}
			if rec.Code != 400 {
				t.Fatalf("racing %s activated: %d %s", operation, rec.Code, rec.Body.String())
			}
			var fresh models.Site
			a.First(&fresh, "id = ?", site.ID)
			if fresh.ActiveDeployID != nil || fresh.EnvDeclarationsEnforced {
				t.Fatal("failed activation changed site")
			}
			if len(engine.invalidated) != 1 {
				t.Fatal("compiled pool not retired")
			}
			if _, err := os.Stat(store.GetDeploymentPath(site.ID, engine.invalidated[0])); !os.IsNotExist(err) {
				t.Fatal("assets/bytecode not removed")
			}
		})
	}
}

func TestDeclarationInvocationRealEngine(t *testing.T) {
	env := setupTestEnvWithWorker(t)
	user, token := env.createTestUser(t, "invocation@test.local", "password123", "user")
	site := models.Site{UserID: user.ID, Name: "Invocation", SubdomainSlug: "invocation"}
	if err := env.db.Create(&site).Error; err != nil {
		t.Fatal(err)
	}
	service := testEnvironmentService(t, env.db)
	value, secret := "5", true
	if _, err := service.Set(site.ID, environment.Write{Name: "COUNT", Value: &value, Secret: &secret}); err != nil {
		t.Fatal(err)
	}
	rec := uploadPolicyZip(t, env, token, site.ID, createTestZip(t, map[string]string{"_worker.js": `export default {fetch(request,env){return new Response(typeof env.COUNT === 'string' && env.COUNT === request.headers.get('x-count') ? 'binding-ok' : 'binding-bad')}}`, "_hostedat.env.json": `{"COUNT":{"type":"number"}}`}).Bytes())
	if rec.Code != 201 {
		t.Fatalf("deploy: %d %s", rec.Code, rec.Body.String())
	}
	fetch := func(want int) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = "invocation.test.local"
		req.Header.Set("x-count", "5")
		rec := env.doRequest(req)
		if rec.Code != want || (want == 200 && rec.Body.String() != "binding-ok") || strings.Contains(rec.Body.String(), "private-marker") {
			t.Fatalf("fetch: %d %s", rec.Code, rec.Body.String())
		}
	}
	fetch(200)
	// Simulate out-of-band operator damage, bypassing the guarded write API.
	if err := env.db.Where("site_id = ?", site.ID).Delete(&models.WorkerEnvVar{}).Error; err != nil {
		t.Fatal(err)
	}
	fetch(500)
	if err := env.db.Create(&models.WorkerEnvVar{SiteID: site.ID, Name: "COUNT", Value: "private-marker", Secret: false}).Error; err != nil {
		t.Fatal(err)
	}
	fetch(500)
	if _, err := service.Set(site.ID, environment.Write{Name: "COUNT", Value: &value, Secret: &secret}); err != nil {
		t.Fatal(err)
	}
	fetch(200)
}
