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
	"sync"
	"testing"
	"time"

	"github.com/cryguy/hostedat/internal/config"
	"github.com/cryguy/hostedat/internal/environment"
	"github.com/cryguy/hostedat/internal/models"
	"github.com/cryguy/hostedat/internal/storage"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

func coordinatedHandlerDBs(t *testing.T) (*gorm.DB, *gorm.DB, models.Site) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "handlers.db")
	open := func() *gorm.DB {
		db, err := models.InitDB(config.DBConfig{Driver: "sqlite", DSN: path})
		if err != nil {
			t.Fatal(err)
		}
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = sqlDB.Close() })
		return db
	}
	a, b := open(), open()
	user := models.User{Email: "coordination@example.test", PasswordHash: "unused"}
	if err := a.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	site := models.Site{UserID: user.ID, SubdomainSlug: "coordination", Name: "Coordination"}
	if err := a.Create(&site).Error; err != nil {
		t.Fatal(err)
	}
	return a, b, site
}

func coordinatedContext(site models.Site, req *http.Request, rec *httptest.ResponseRecorder) echo.Context {
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(site.ID)
	c.Set(contextKeyUserID, site.UserID)
	c.Set(contextKeyRole, "user")
	return c
}

func TestSiteTransactionConcurrentEnvUpserts(t *testing.T) {
	a, b, site := coordinatedHandlerDBs(t)
	if err := a.Transaction(models.InstallWorkerEnvUniqueness); err != nil {
		t.Fatal(err)
	}
	const writers = 16
	start := make(chan struct{})
	results := make(chan error, writers)
	var wg sync.WaitGroup
	service := testEnvironmentService(t, a)
	for i := range writers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			db := a
			if i%2 != 0 {
				db = b
			}
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(fmt.Sprintf(`{"name":"SAME","value":"disposable-%d","secret":true}`, i)))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			err := (&WorkerHandler{DB: db, Environment: &environment.Service{DB: db, Keys: service.Keys}}).SetEnvVar(coordinatedContext(site, req, rec))
			if err == nil && rec.Code != http.StatusOK {
				err = fmt.Errorf("status %d: %s", rec.Code, rec.Body.String())
			}
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	for range writers {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var rows []models.WorkerEnvVar
	if err := a.Where("site_id = ? AND name = ?", site.ID, "SAME").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].Secret {
		t.Fatalf("concurrent upserts: %+v", rows)
	}
}

func TestSiteTransactionHandlersWaitForWriter(t *testing.T) {
	for _, kind := range []string{"set", "delete", "kv", "d1", "do", "storage", "deploy", "rollback"} {
		t.Run(kind, func(t *testing.T) {
			a, b, site := coordinatedHandlerDBs(t)
			env := models.WorkerEnvVar{SiteID: site.ID, Name: "OLD", Value: "disposable"}
			if err := a.Create(&env).Error; err != nil {
				t.Fatal(err)
			}
			dep := models.Deployment{SiteID: site.ID, Version: 1, FileHash: "disposable"}
			if err := a.Create(&dep).Error; err != nil {
				t.Fatal(err)
			}
			store := storage.NewManager(t.TempDir())
			wh, dh := &WorkerHandler{DB: b}, &DeployHandler{DB: b, Storage: store}
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"NEW","value":"disposable"}`))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := coordinatedContext(site, req, rec)
			var handler echo.HandlerFunc
			want := http.StatusCreated
			switch kind {
			case "set":
				handler, want = wh.SetEnvVar, http.StatusOK
			case "delete":
				handler, want = wh.DeleteEnvVar, http.StatusNoContent
				c.SetParamNames("id", "varId")
				c.SetParamValues(site.ID, env.ID)
			case "kv":
				handler = wh.CreateKVNamespace
			case "d1":
				handler = wh.CreateD1Database
			case "do":
				handler = wh.CreateDurableObjectNamespace
			case "storage":
				handler = (&StorageHandler{DB: b, S3Client: &fakeBucketClient{}}).CreateBucket
				req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(fmt.Sprintf(`{"name":"IMAGES","bucket_name":"%s-images"}`, site.ID)))
				req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
				c = coordinatedContext(site, req, rec)
			case "rollback":
				handler, want = dh.Rollback, http.StatusOK
				c.SetParamNames("id", "version")
				c.SetParamValues(site.ID, "1")
			case "deploy":
				handler = dh.Deploy
				body := new(bytes.Buffer)
				form := multipart.NewWriter(body)
				part, err := form.CreateFormFile("file", "site.zip")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := part.Write(createTestZip(t, map[string]string{"index.html": "disposable"}).Bytes()); err != nil {
					t.Fatal(err)
				}
				if err := form.Close(); err != nil {
					t.Fatal(err)
				}
				req = httptest.NewRequest(http.MethodPost, "/", body)
				req.Header.Set(echo.HeaderContentType, form.FormDataContentType())
				c = coordinatedContext(site, req, rec)
			}
			locked, release := make(chan struct{}), make(chan struct{})
			first := make(chan error, 1)
			go func() {
				first <- models.WithSiteTransaction(a, site.ID, func(_ *gorm.DB, _ *models.Site) error { close(locked); <-release; return nil })
			}()
			<-locked
			done := make(chan error, 1)
			go func() { done <- handler(c) }()
			select {
			case err := <-done:
				close(release)
				t.Fatalf("handler bypassed writer lock: %v, status %d", err, rec.Code)
			case <-time.After(100 * time.Millisecond):
			}
			close(release)
			if err := <-first; err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if rec.Code != want {
				t.Fatalf("status %d, want %d: %s", rec.Code, want, rec.Body.String())
			}
		})
	}
}

func TestSiteTransactionFailedDeployLeavesPriorState(t *testing.T) {
	a, _, site := coordinatedHandlerDBs(t)
	prior := models.Deployment{SiteID: site.ID, Version: 1, FileHash: "prior"}
	if err := a.Create(&prior).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.Model(&site).Updates(map[string]interface{}{"active_deploy_id": prior.ID, "active_version": 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.Exec(`CREATE TRIGGER reject_site_activation BEFORE UPDATE OF active_deploy_id ON sites BEGIN SELECT RAISE(ABORT, 'disposable storage failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	body := new(bytes.Buffer)
	form := multipart.NewWriter(body)
	part, err := form.CreateFormFile("file", "site.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(createTestZip(t, map[string]string{"index.html": "new"}).Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set(echo.HeaderContentType, form.FormDataContentType())
	rec := httptest.NewRecorder()
	if err := (&DeployHandler{DB: a, Storage: storage.NewManager(root)}).Deploy(coordinatedContext(site, req, rec)); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
	var current models.Site
	if err := a.First(&current, "id = ?", site.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.ActiveDeployID == nil || *current.ActiveDeployID != prior.ID {
		t.Fatal("failed deploy changed active ID")
	}
	var count int64
	if err := a.Model(&models.Deployment{}).Where("site_id = ?", site.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("partial deployment: %d %v", count, err)
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			return fmt.Errorf("failed deploy left artifact: %s", path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
