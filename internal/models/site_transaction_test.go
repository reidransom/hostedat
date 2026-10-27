package models

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cryguy/hostedat/internal/config"
	"gorm.io/gorm"
)

func openSiteTransactionDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := InitDB(config.DBConfig{Driver: "sqlite", DSN: path})
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

func createTransactionSite(t *testing.T, db *gorm.DB) Site {
	t.Helper()
	user := User{Email: "transaction@example.test", PasswordHash: "unused"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	site := Site{UserID: user.ID, SubdomainSlug: "transaction", Name: "Transaction"}
	if err := db.Create(&site).Error; err != nil {
		t.Fatal(err)
	}
	return site
}

func TestSiteTransactionSeparateConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coordination.db")
	a := openSiteTransactionDB(t, path+"?cache=private")
	b := openSiteTransactionDB(t, path+"?cache=private")
	site := createTransactionSite(t, a)
	locked, release := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- WithSiteTransaction(a, site.ID, func(tx *gorm.DB, site *Site) error {
			close(locked)
			<-release
			return tx.Model(site).Update("env_declarations_enforced", true).Error
		})
	}()
	<-locked
	entered := make(chan bool, 1)
	second := make(chan error, 1)
	go func() {
		second <- WithSiteTransaction(b, site.ID, func(_ *gorm.DB, site *Site) error {
			entered <- site.EnvDeclarationsEnforced
			return nil
		})
	}()
	select {
	case <-entered:
		close(release)
		t.Fatal("second connection entered before first transaction committed")
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if !<-entered {
		t.Fatal("second transaction used stale pre-lock snapshot")
	}
}

func TestSiteTransactionRollbackAndSnapshots(t *testing.T) {
	db := openSiteTransactionDB(t, filepath.Join(t.TempDir(), "snapshots.db"))
	site := createTransactionSite(t, db)
	empty := "{}"
	for i, declaration := range []*string{nil, &empty} {
		if err := db.Create(&Deployment{SiteID: site.ID, Version: i + 1, FileHash: "disposable", EnvDeclaration: declaration}).Error; err != nil {
			t.Fatal(err)
		}
	}
	var deployments []Deployment
	if err := db.Order("version").Find(&deployments).Error; err != nil {
		t.Fatal(err)
	}
	if len(deployments) != 2 || deployments[0].EnvDeclaration != nil || deployments[1].EnvDeclaration == nil || *deployments[1].EnvDeclaration != "{}" {
		t.Fatalf("absent and empty declaration were not preserved: %+v", deployments)
	}
	abort := errors.New("abort transaction")
	err := WithSiteTransaction(db, site.ID, func(tx *gorm.DB, site *Site) error {
		if err := tx.Model(site).Updates(map[string]interface{}{"env_declarations_enforced": true, "active_deploy_id": deployments[1].ID, "active_version": 2}).Error; err != nil {
			return err
		}
		if err := tx.Create(&WorkerEnvVar{SiteID: site.ID, Name: "TEMP", Value: "disposable"}).Error; err != nil {
			return err
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatalf("rollback error = %v", err)
	}
	if err := db.First(&site, "id = ?", site.ID).Error; err != nil {
		t.Fatal(err)
	}
	if site.ActiveDeployID != nil || site.ActiveVersion != nil || site.EnvDeclarationsEnforced {
		t.Fatal("failed transaction changed site")
	}
	var count int64
	if err := db.Model(&WorkerEnvVar{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed transaction retained environment write: %d, %v", count, err)
	}
	if err := WithSiteTransaction(db, "missing", func(_ *gorm.DB, _ *Site) error { t.Fatal("missing site entered transaction callback"); return nil }); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing site = %v", err)
	}
}

func TestWorkerEnvDuplicateMaintenance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duplicates.db")
	db := openSiteTransactionDB(t, path)
	site := createTransactionSite(t, db)
	rows := []WorkerEnvVar{{SiteID: site.ID, Name: "TOKEN", Value: "disposable-one"}, {SiteID: site.ID, Name: "TOKEN", Value: "disposable-two"}}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := FindWorkerEnvVar(db, site.ID, "TOKEN"); !errors.Is(err, ErrDuplicateWorkerEnv) {
		t.Fatalf("ambiguous lookup = %v", err)
	}
	if err := CheckWorkerEnvDuplicates(db); !errors.Is(err, ErrDuplicateWorkerEnv) {
		t.Fatalf("duplicate check = %v", err)
	}
	if err := db.Transaction(InstallWorkerEnvUniqueness); !errors.Is(err, ErrDuplicateWorkerEnv) {
		t.Fatalf("duplicate index installation = %v", err)
	}
	if _, err := InitDB(config.DBConfig{Driver: "sqlite", DSN: path}); !errors.Is(err, ErrDuplicateWorkerEnv) || strings.Contains(err.Error(), "disposable") {
		t.Fatalf("duplicate startup = %v", err)
	}
	// An operator explicitly chooses a row to remove; maintenance never does.
	if err := db.Delete(&rows[1]).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(InstallWorkerEnvUniqueness); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&WorkerEnvVar{SiteID: site.ID, Name: "TOKEN", Value: "disposable-three"}).Error; err == nil {
		t.Fatal("unique index allowed duplicate")
	}
	if _, err := FindWorkerEnvVar(db, site.ID, "TOKEN"); err != nil {
		t.Fatal(err)
	}
}

func TestSiteTransactionConnectionPragmas(t *testing.T) {
	db := openSiteTransactionDB(t, filepath.Join(t.TempDir(), "pragmas.db")+"?cache=private")
	// Hold one physical connection while checking a second pooled connection.
	if err := db.Connection(func(first *gorm.DB) error {
		return db.Connection(func(second *gorm.DB) error {
			for _, connection := range []*gorm.DB{first, second} {
				var mode string
				var timeout int
				if err := connection.Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
					return err
				}
				if err := connection.Raw("PRAGMA busy_timeout").Scan(&timeout).Error; err != nil {
					return err
				}
				if mode != "wal" || timeout != 5000 {
					t.Fatalf("connection pragmas: %s, %d", mode, timeout)
				}
			}
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
}
