package environment

import (
	"testing"

	"github.com/cryguy/hostedat/internal/models"
)

func TestInvocationValidatesSelectedDeclaration(t *testing.T) {
	db := testDB(t)
	service := &Service{DB: db, Keys: testKeys(t, "key", 9)}
	required, empty := `{"COUNT":{"type":"number"},"OPTIONAL":{"type":"boolean","required":false}}`, `{}`
	old := models.Deployment{ID: "old", SiteID: "site", Version: 1, FileHash: "old", EnvDeclaration: &required}
	current := models.Deployment{ID: "current", SiteID: "site", Version: 2, FileHash: "new", EnvDeclaration: &empty}
	for _, dep := range []*models.Deployment{&old, &current} {
		if err := db.Create(dep).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&models.Site{}).Where("id = ?", "site").Updates(map[string]any{"active_deploy_id": current.ID, "env_declarations_enforced": true}).Error; err != nil {
		t.Fatal(err)
	}
	secret := true
	put := func(name, value string) {
		t.Helper()
		if _, err := service.Set("site", Write{Name: name, Value: &value, Secret: &secret}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := service.LoadForDeployment("site", old.ID); err == nil {
		t.Fatal("selected historical required binding ignored")
	}
	put("COUNT", "invalid-private-marker")
	if _, _, err := service.LoadForDeployment("site", old.ID); err == nil {
		t.Fatal("invalid decrypted type accepted")
	}
	put("COUNT", "7")
	_, values, err := service.LoadForDeployment("site", old.ID)
	if err != nil || values["COUNT"] != "7" {
		t.Fatal("valid selected declaration failed")
	}
	put("OPTIONAL", "invalid-private-marker")
	if _, _, err := service.LoadForDeployment("site", old.ID); err == nil {
		t.Fatal("invalid optional value accepted")
	}
	put("OPTIONAL", "true")
	_, values, err = service.LoadForDeployment("site", old.ID)
	if err != nil || values["OPTIONAL"] != "true" {
		t.Fatal("valid optional rejected")
	}
	// No values are cached with the parsed snapshot.
	put("COUNT", "9")
	_, values, err = service.LoadForDeployment("site", old.ID)
	if err != nil || values["COUNT"] != "9" {
		t.Fatal("cached declaration retained stale values")
	}
}
