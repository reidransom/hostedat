package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cryguy/hostedat/internal/environment"
	"github.com/cryguy/hostedat/internal/envschema"
	"github.com/cryguy/hostedat/internal/models"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

func collisionCreate(t *testing.T, db *gorm.DB, site models.Site, kind, name string, s3 *fakeBucketClient) int {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"bucket_name":%q}`, name, site.ID+"-"+strings.ReplaceAll(strings.ToLower(name), "_", "-"))
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := coordinatedContext(site, req, rec)
	wh := &WorkerHandler{DB: db}
	var handler echo.HandlerFunc
	switch kind {
	case "kv":
		handler = wh.CreateKVNamespace
	case "d1":
		handler = wh.CreateD1Database
	case "do":
		handler = wh.CreateDurableObjectNamespace
	case "storage":
		handler = (&StorageHandler{DB: db, S3Client: s3}).CreateBucket
	default:
		t.Fatal("unknown binding kind")
	}
	if err := handler(c); err != nil {
		t.Fatal(err)
	}
	return rec.Code
}

func TestBindingCollisionsBothDirections(t *testing.T) {
	for _, kind := range []string{"kv", "d1", "do", "storage"} {
		t.Run(kind, func(t *testing.T) {
			a, _, site := coordinatedHandlerDBs(t)
			s := testEnvironmentService(t, a)
			value, secret := "disposable-binding-marker", true
			write := func(name string) error {
				_, err := s.Set(site.ID, environment.Write{Name: name, Value: &value, Secret: &secret})
				return err
			}
			s3 := &fakeBucketClient{}
			if err := write("ENV_FIRST"); err != nil {
				t.Fatal(err)
			}
			if got := collisionCreate(t, a, site, kind, "ENV_FIRST", s3); got != 400 {
				t.Fatalf("env first: %d", got)
			}
			if kind == "storage" && len(s3.made) != len(s3.removed) {
				t.Fatal("colliding storage bucket not compensated")
			}
			if got := collisionCreate(t, a, site, kind, "BINDING_FIRST", s3); got != 201 {
				t.Fatalf("binding first: %d", got)
			}
			if err := write("BINDING_FIRST"); err == nil {
				t.Fatal("environment shadowed binding")
			}
			// Entire batch and every candidate name are checked, even unsupplied optional names.
			for _, body := range []string{
				`{"entries":[{"name":"UNRELATED","value":"x","secret":true},{"name":"BINDING_FIRST","value":"x","secret":true}]}`,
				`{"entries":[{"name":"UNRELATED","value":"x","secret":true}],"schema":{"UNRELATED":{"type":"string"},"BINDING_FIRST":{"type":"string","required":false}}}`,
			} {
				req := httptest.NewRequest("POST", "/", strings.NewReader(body))
				req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
				rec := httptest.NewRecorder()
				if err := (&WorkerHandler{DB: a, Environment: s}).BulkEnvVars(coordinatedContext(site, req, rec)); err != nil {
					t.Fatal(err)
				}
				if rec.Code != 400 {
					t.Fatalf("bulk collision: %d %s", rec.Code, rec.Body.String())
				}
			}
			var count int64
			a.Model(&models.WorkerEnvVar{}).Where("site_id = ? AND name = ?", site.ID, "UNRELATED").Count(&count)
			if count != 0 {
				t.Fatal("partial batch committed")
			}
			// An absent optional active secret still owns its name.
			declaration := `{"DECLARED":{"type":"string","required":false}}`
			dep := models.Deployment{SiteID: site.ID, Version: 1, FileHash: "test", EnvDeclaration: &declaration}
			if err := a.Create(&dep).Error; err != nil {
				t.Fatal(err)
			}
			if err := a.Model(&site).Update("active_deploy_id", dep.ID).Error; err != nil {
				t.Fatal(err)
			}
			if got := collisionCreate(t, a, site, kind, "DECLARED", s3); got != 400 {
				t.Fatalf("optional declaration shadowed: %d", got)
			}
			for _, name := range []string{"ASSETS", "CONSTRUCTOR", "PROTOTYPE"} {
				if got := collisionCreate(t, a, site, kind, name, s3); got != 400 {
					t.Fatalf("reserved %s: %d", name, got)
				}
			}
			// A different site and exact case-sensitive namespaces are independent.
			other := models.Site{UserID: site.UserID, SubdomainSlug: "other", Name: "Other"}
			if err := a.Create(&other).Error; err != nil {
				t.Fatal(err)
			}
			if got := collisionCreate(t, a, other, kind, "ENV_FIRST", s3); got != 201 {
				t.Fatalf("cross-site: %d", got)
			}
			if kind != "storage" {
				if got := collisionCreate(t, a, site, kind, "legacy-name", s3); got != 201 {
					t.Fatalf("legacy grammar: %d", got)
				}
				if got := collisionCreate(t, a, site, kind, "env_first", s3); got != 201 {
					t.Fatalf("case-sensitive name: %d", got)
				}
			}
		})
	}
}

func TestBindingCollisionsConcurrentConnections(t *testing.T) {
	for _, kind := range []string{"kv", "d1", "do", "storage"} {
		t.Run(kind, func(t *testing.T) {
			a, b, site := coordinatedHandlerDBs(t)
			s := testEnvironmentService(t, a)
			for i := range 8 {
				name := fmt.Sprintf("RACE%d", i)
				start, result := make(chan struct{}), make(chan int, 2)
				go func() {
					<-start
					value, secret := "disposable", true
					_, err := s.Set(site.ID, environment.Write{Name: name, Value: &value, Secret: &secret})
					if err == nil {
						result <- 200
					} else {
						var diagnostic *envschema.Diagnostic
						if errors.As(err, &diagnostic) {
							result <- 400
						} else {
							result <- 500
						}
					}
				}()
				go func() { <-start; result <- collisionCreate(t, b, site, kind, name, &fakeBucketClient{}) }()
				close(start)
				x, y := <-result, <-result
				if !((x == 200 && y == 400) || (x == 400 && y == 201) || (x == 201 && y == 400) || (x == 400 && y == 200)) {
					t.Fatalf("conflicting creates: %d %d", x, y)
				}
				var envCount int64
				a.Model(&models.WorkerEnvVar{}).Where("site_id = ? AND name = ?", site.ID, name).Count(&envCount)
				for _, model := range []any{&models.KVNamespace{}, &models.D1Database{}, &models.DurableObjectNamespace{}, &models.StorageBucket{}} {
					var bindings int64
					a.Model(model).Where("site_id = ? AND name = ?", site.ID, name).Count(&bindings)
					if envCount != 0 && bindings != 0 {
						t.Fatal("both names committed")
					}
				}
			}
		})
	}
}

func TestBindingCollisionsCrossNamespace(t *testing.T) {
	a, _, site := coordinatedHandlerDBs(t)
	if got := collisionCreate(t, a, site, "kv", "SHARED", nil); got != 201 {
		t.Fatal(got)
	}
	for _, kind := range []string{"d1", "do", "storage"} {
		if got := collisionCreate(t, a, site, kind, "SHARED", &fakeBucketClient{}); got != 400 {
			t.Fatalf("%s shadowed KV: %d", kind, got)
		}
	}
}
