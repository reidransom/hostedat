package environment

import (
	"strings"
	"testing"

	"github.com/cryguy/hostedat/internal/envschema"
	"github.com/cryguy/hostedat/internal/models"
)

func TestBindingDeclarationsAndLegacyLoading(t *testing.T) {
	db := testDB(t)
	s := &Service{DB: db, Keys: testKeys(t, "test", 1)}
	for _, name := range []string{"ASSETS", "assets", "__proto__", "CONSTRUCTOR", "Prototype"} {
		for _, format := range []envschema.Format{envschema.JSON, envschema.YAML} {
			input := name + ": {type: string}"
			if format == envschema.JSON {
				input = `{"` + name + `":{"type":"string"}}`
			}
			if _, err := envschema.Parse(strings.NewReader(input), format); err == nil {
				t.Fatalf("reserved declaration accepted: %s", name)
			}
		}
		value := "disposable"
		if _, err := s.Set("site", Write{Name: name, Value: &value}); err == nil {
			t.Fatalf("reserved write accepted: %s", name)
		}
	}
	if err := db.Create(&models.KVNamespace{SiteID: "site", Name: "TAKEN"}).Error; err != nil {
		t.Fatal(err)
	}
	schema := envschema.Schema{"TAKEN": {Type: envschema.String, Required: false}}
	if err := s.Validate("site", schema); err == nil {
		t.Fatal("absent optional declaration shadowed binding")
	}
	// Persisted legacy collisions fail closed, without decrypting or selecting a winner.
	if err := db.Create(&models.WorkerEnvVar{SiteID: "site", Name: "TAKEN", Value: "legacy"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Load("site"); err == nil {
		t.Fatal("legacy collision was loaded")
	}
	// Type labels are not reserved: actual names, not namespace kinds, matter.
	if err := s.Validate("site", envschema.Schema{"KV": {Type: envschema.String, Required: false}, "D1": {Type: envschema.String, Required: false}, "CACHE": {Type: envschema.String, Required: false}}); err != nil {
		t.Fatal(err)
	}
}
