package workeradapter

import (
	"errors"
	"github.com/cryguy/hostedat/internal/environment"
	"github.com/cryguy/hostedat/internal/models"
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
	pd := &PlatformDispatcher{EnvFactory: func(string) (*worker.Env, error) { return nil, errors.New("sensitive storage detail") }}
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
	cr := &CronRunner{db: db, envFactory: func(string) (*worker.Env, error) { called = true; return nil, environment.ErrStorage }}
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
