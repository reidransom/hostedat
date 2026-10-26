package environment

import (
	"bytes"
	"encoding/base64"
	"errors"
	"github.com/cryguy/hostedat/internal/config"
	"github.com/cryguy/hostedat/internal/models"
	"gorm.io/gorm"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testKeys(t *testing.T, id string, fill byte) *Keys {
	t.Helper()
	p := filepath.Join(t.TempDir(), "keys.json")
	data := `{"active":"` + id + `","keys":{"` + id + `":"` + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32)) + `"}}`
	if err := os.WriteFile(p, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	k, err := LoadKeys(p)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := models.InitDB(config.DBConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "db.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	t.Cleanup(func() { sql.Close() })
	if err := db.Create(&models.User{ID: "user", Email: "test@example.test", PasswordHash: "unused"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Site{ID: "site", UserID: "user", SubdomainSlug: "test", Name: "test"}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}
func TestOfflineMigrationRotationAndSnapshots(t *testing.T) {
	db := testDB(t)
	old, newKeys := testKeys(t, "old", 1), testKeys(t, "new", 2)
	marker := "disposable-marker-never-persist-after-migration"
	rows := []models.WorkerEnvVar{{ID: "a", SiteID: "site", Name: "TOKEN", Value: marker, Secret: true}, {ID: "b", SiteID: "site", Name: "EMPTY", Value: "", Secret: true}, {ID: "c", SiteID: "site", Name: "PUBLIC", Value: "", Secret: false}}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	s := &Service{DB: db, Keys: old}
	if s.ValidateStartup() == nil {
		t.Fatal("legacy startup allowed")
	}
	if n, err := Maintain(db, old, old, false); err != nil || n != 2 {
		t.Fatalf("migration count=%d error=%v", n, err)
	}
	if err := s.ValidateStartup(); err != nil {
		t.Fatal(err)
	}
	var saved models.WorkerEnvVar
	db.First(&saved, "id = ?", "a")
	if saved.Value != "" || saved.SecretVersion != 1 || saved.SecretKeyID != "old" || bytes.Contains(saved.Ciphertext, []byte(marker)) {
		t.Fatal("plaintext persisted")
	}
	_, snapshot, err := s.Load("site")
	if err != nil || snapshot["TOKEN"] != marker {
		t.Fatal("migration lost value")
	}
	replacement := "next-value"
	row, err := s.Set("site", Write{Name: "TOKEN", Value: &replacement})
	if err != nil || !row.Secret || row.ID != "a" {
		t.Fatal("omission downgraded secret or changed ID")
	}
	_, next, err := s.Load("site")
	if err != nil || next["TOKEN"] != replacement || snapshot["TOKEN"] != marker {
		t.Fatal("snapshot not isolated")
	}
	if _, err := s.Set("site", Write{Name: "TOKEN"}); !errors.Is(err, ErrInput) {
		t.Fatal("missing value accepted")
	}
	if n, err := Maintain(db, old, newKeys, true); err != nil || n != 2 {
		t.Fatalf("rotation count=%d error=%v", n, err)
	}
	if s.ValidateStartup() == nil {
		t.Fatal("old key worked after rotation")
	}
	s.Keys = newKeys
	if err := s.ValidateStartup(); err != nil {
		t.Fatal(err)
	}
	_, next, err = s.Load("site")
	if err != nil || next["TOKEN"] != replacement {
		t.Fatal("rotation lost data")
	}
}

func TestCiphertextAuthenticationAndMetadata(t *testing.T) {
	db := testDB(t)
	keys := testKeys(t, "key", 3)
	if _, err := Maintain(db, keys, keys, false); err != nil {
		t.Fatal(err)
	}
	s := &Service{DB: db, Keys: keys}
	v := "unique-value"
	secret := true
	row, err := s.Set("site", Write{Name: "TOKEN", Value: &v, Secret: &secret})
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Set("site", Write{Name: "TOKEN", Value: &v})
	if err != nil || bytes.Equal(row.SecretNonce, again.SecretNonce) {
		t.Fatal("nonce reused")
	}
	for _, modify := range []func(*models.WorkerEnvVar){
		func(r *models.WorkerEnvVar) { r.Ciphertext[0] ^= 1 }, func(r *models.WorkerEnvVar) { r.Name = "OTHER" }, func(r *models.WorkerEnvVar) { r.SiteID = "elsewhere" }, func(r *models.WorkerEnvVar) { r.SecretVersion++ }, func(r *models.WorkerEnvVar) { r.SecretKeyID = "unknown" }, func(r *models.WorkerEnvVar) { r.Value = v },
	} {
		copy := again
		copy.Ciphertext = append([]byte(nil), again.Ciphertext...)
		modify(&copy)
		if _, err := s.value(copy); err == nil {
			t.Fatal("invalid ciphertext accepted")
		}
	}
	wrong := &Service{DB: db, Keys: testKeys(t, "key", 4)}
	if wrong.ValidateStartup() == nil {
		t.Fatal("wrong key accepted")
	}
	missing := &Service{DB: db}
	if _, _, err := missing.Load("site"); err == nil {
		t.Fatal("missing key accepted")
	}
	if err := db.Model(&models.WorkerEnvVar{}).Where("id = ?", again.ID).Update("ciphertext", []byte("tampered")).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Load("site"); err == nil {
		t.Fatal("load did not fail closed")
	}
	listed, err := missing.List("site")
	if err != nil || len(listed) != 1 || listed[0].Value != "" || len(listed[0].Ciphertext) != 0 {
		t.Fatal("metadata read decrypted or disclosed")
	}
	if _, err := missing.Delete("site", again.ID); err != nil {
		t.Fatal("metadata delete required decryption")
	}
}

func TestMaintenanceFailureIsAtomic(t *testing.T) {
	db := testDB(t)
	k := testKeys(t, "key", 5)
	rows := []models.WorkerEnvVar{{ID: "a", SiteID: "site", Name: "A", Value: "marker-a", Secret: true}, {ID: "b", SiteID: "site", Name: "B", Value: "marker-b", Secret: true}}
	db.Create(&rows)
	if err := db.Exec("CREATE TRIGGER fail_second BEFORE UPDATE ON worker_env_vars WHEN NEW.id = 'b' BEGIN SELECT RAISE(ABORT, 'blocked'); END").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := Maintain(db, k, k, false); err == nil {
		t.Fatal("maintenance succeeded despite failure")
	}
	var got []models.WorkerEnvVar
	db.Order("id").Find(&got)
	if len(got) != 2 || got[0].Value != "marker-a" || got[0].SecretVersion != 0 || got[1].Value != "marker-b" || db.Migrator().HasIndex(&models.WorkerEnvVar{}, "idx_worker_env_site_name") {
		t.Fatal("failed maintenance left partial writes")
	}
	db.Exec("DROP TRIGGER fail_second")
	db.Create(&models.WorkerEnvVar{SiteID: "site", Name: "A", Value: "duplicate", Secret: true})
	if _, err := Maintain(db, k, k, false); !errors.Is(err, models.ErrDuplicateWorkerEnv) {
		t.Fatal("duplicate maintenance allowed")
	}
}

func TestKeyFileValidation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "keys")
	good := `{"active":"key","keys":{"key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}}`
	for _, data := range []string{"", `{"active":"missing","keys":{}}`, strings.Replace(good, "AAA=", "AA=", 1), `{"active":"key","active":"key","keys":{}}`, good + " {}", strings.Replace(good, `"active"`, `"unknown"`, 1)} {
		os.WriteFile(p, []byte(data), 0600)
		if _, err := LoadKeys(p); err == nil {
			t.Fatal("bad key file accepted")
		}
	}
	os.WriteFile(p, []byte(good), 0600)
	os.Chmod(p, 0644)
	if _, err := LoadKeys(p); err == nil {
		t.Fatal("public file accepted")
	}
	os.Chmod(p, 0600)
	link := filepath.Join(dir, "link")
	os.Symlink(p, link)
	if _, err := LoadKeys(link); err == nil {
		t.Fatal("symlink accepted")
	}
	if _, err := LoadKeys(dir); err == nil {
		t.Fatal("directory accepted")
	}
	if _, err := LoadKeys(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing file accepted")
	}
}
