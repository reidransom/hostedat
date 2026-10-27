package workeradapter

import (
	"errors"
	"github.com/cryguy/hostedat/internal/environment"
	"github.com/cryguy/hostedat/internal/models"
	"github.com/cryguy/hostedat/internal/storage"
	"github.com/cryguy/worker/v2"
	"gorm.io/gorm"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func encryptedTestService(t *testing.T, db *gorm.DB) *environment.Service {
	t.Helper()
	p := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(p, []byte(`{"active":"test","keys":{"test":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}}`), 0600); err != nil {
		t.Fatal(err)
	}
	keys, err := environment.LoadKeys(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := environment.Maintain(db, keys, keys, false); err != nil {
		t.Fatal(err)
	}
	return &environment.Service{DB: db, Keys: keys}
}
func TestEnvironmentFailuresStopDispatch(t *testing.T) {
	pd := &PlatformDispatcher{EnvFactory: func(string, string) (*worker.Env, error) { return nil, errors.New("sensitive storage detail") }}
	if r := pd.Execute("site", "deploy", nil, &worker.WorkerRequest{}); r.Error == nil || r.Error.Error() != "worker environment configuration failed" {
		t.Fatal("dispatcher did not fail closed")
	}
	db := setupFullEnvTestDB(t)
	if err := db.AutoMigrate(&models.Site{}, &models.CronSchedule{}); err != nil {
		t.Fatal(err)
	}
	deploy := "deploy"
	db.Create(&models.Site{ID: "site", UserID: "user", SubdomainSlug: "slug", Name: "site", HasWorker: true, ActiveDeployID: &deploy})
	sched := models.CronSchedule{SiteID: "site", Cron: "* * * * *", Enabled: true}
	db.Create(&sched)
	called := false
	cr := &CronRunner{db: db, envFactory: func(string, string) (*worker.Env, error) { called = true; return nil, environment.ErrStorage }}
	// No engine: calling it would panic, so a rejected environment must skip source and execution.
	cr.tick(time.Now())
	if !called {
		t.Fatal("factory was not called")
	}
	db.First(&sched, "id = ?", sched.ID)
	if sched.LastRunAt != nil {
		t.Fatal("failed invocation reported as run")
	}
}

func TestCronDeclarationRealEngine(t *testing.T) {
	db := setupFullEnvTestDB(t)
	if err := db.AutoMigrate(&models.CronSchedule{}, &models.WorkerLog{}); err != nil {
		t.Fatal(err)
	}
	service := encryptedTestService(t, db)
	deploy, declaration := "cron-deployment", `{"COUNT":{"type":"number"}}`
	site := models.Site{ID: "cron-site", UserID: "user", Name: "Cron", SubdomainSlug: "cron", HasWorker: true, ActiveDeployID: &deploy, EnvDeclarationsEnforced: true}
	if err := db.Create(&site).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Deployment{ID: deploy, SiteID: site.ID, Version: 1, FileHash: "test", HasWorker: true, EnvDeclaration: &declaration}).Error; err != nil {
		t.Fatal(err)
	}
	value, secret := "5", true
	row, err := service.Set(site.ID, environment.Write{Name: "COUNT", Value: &value, Secret: &secret})
	if err != nil {
		t.Fatal(err)
	}
	store := storage.NewManager(t.TempDir())
	root := store.GetDeploymentPath(site.ID, deploy)
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "_worker.js"), []byte(`export default {scheduled(event, env) { if (typeof env.COUNT !== "string" || env.COUNT !== "5") throw new Error("binding invalid"); console.log("cron-binding-ok"); }}`), 0644); err != nil {
		t.Fatal(err)
	}
	engine := worker.NewEngine(worker.EngineConfig{PoolSize: 1, MemoryLimitMB: 64, ExecutionTimeout: 5}, store)
	defer engine.Shutdown()
	sched := models.CronSchedule{SiteID: site.ID, Cron: "* * * * *", Enabled: true}
	if err := db.Create(&sched).Error; err != nil {
		t.Fatal(err)
	}
	cr := &CronRunner{db: db, engine: engine, envFactory: func(siteID, deployID string) (*worker.Env, error) {
		return BuildEnvFromDB(BuildEnvOptions{DB: db, Environment: service, DeploymentID: deployID}, siteID, nil)
	}}
	cr.tick(time.Now())
	deadline := time.Now().Add(5 * time.Second)
	for {
		var count int64
		db.Model(&models.WorkerLog{}).Where("site_id = ? AND message = ?", site.ID, "cron-binding-ok").Count(&count)
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("real scheduled invocation did not receive decrypted binding")
		}
		time.Sleep(10 * time.Millisecond)
	}
	db.First(&sched, "id = ?", sched.ID)
	if sched.LastRunAt == nil {
		t.Fatal("successful cron did not run")
	}
	last := *sched.LastRunAt
	// Bypass the guarded service to model corrupt/out-of-band required values.
	if err := db.Delete(&row).Error; err != nil {
		t.Fatal(err)
	}
	cr.tick(time.Now())
	db.First(&sched, "id = ?", sched.ID)
	if sched.LastRunAt == nil || !sched.LastRunAt.Equal(last) {
		t.Fatal("invalid required cron environment reported a run")
	}
}
