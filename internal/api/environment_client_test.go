package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cryguy/hostedat/internal/client"
	"github.com/cryguy/hostedat/internal/envschema"
	"github.com/cryguy/hostedat/internal/models"
)

func TestEnvironmentTypedClientAtomicIntegration(t *testing.T) {
	env := setupTestEnv(t)
	owner, token := env.createTestUser(t, "client@test.local", "password123", "user")
	site := models.Site{UserID: owner.ID, SubdomainSlug: "typed-client", Name: "Typed client"}
	if err := env.db.Create(&site).Error; err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(env.e)
	defer srv.Close()
	c := client.New(srv.URL, token)
	c.HTTPClient = srv.Client()
	info, err := c.ServerVersion()
	if err != nil || info.Version != "0.1.0" {
		t.Fatalf("version compatibility: %+v %v", info, err)
	}
	if err := c.RequireSecretsCapability(); err != nil {
		t.Fatal(err)
	}
	rows, err := c.BulkSetEnvVars(site.ID, []client.EnvWrite{{Name: "TOKEN", Value: "private-client-marker", Secret: true}, {Name: "COUNT", Value: "7", Secret: true}}, nil)
	if err != nil || len(rows) != 2 {
		t.Fatalf("bulk: %+v %v", rows, err)
	}
	tokenID := rows[0].ID
	// Real HTTP + real SQLite trigger failure after the first update: no partial
	// commit, no per-name fallback, and no storage diagnostics in client errors.
	if err := env.db.Exec("CREATE TRIGGER fail_client BEFORE INSERT ON worker_env_vars WHEN NEW.name = 'FAIL' BEGIN SELECT RAISE(ABORT, 'private-client-marker'); END").Error; err != nil {
		t.Fatal(err)
	}
	_, err = c.BulkSetEnvVars(site.ID, []client.EnvWrite{{Name: "COUNT", Value: "9", Secret: true}, {Name: "FAIL", Value: "private-client-marker", Secret: true}}, nil)
	if err == nil || strings.Contains(err.Error(), "private-client-marker") {
		t.Fatalf("unsafe storage failure: %v", err)
	}
	schema := envschema.Schema{"COUNT": {Type: envschema.Enum, Required: true, Values: []string{"7"}}, "TOKEN": {Type: envschema.String, Required: true}}
	valid, err := c.ValidateEnvVars(site.ID, schema)
	if err != nil || !valid.Valid {
		t.Fatalf("atomic stored validation: %+v %v", valid, err)
	}
	if _, err := c.BulkSetEnvVars(site.ID, []client.EnvWrite{{Name: "COUNT", Value: "9", Secret: true}, {Name: "bad-name", Value: "private-client-marker", Secret: true}}, nil); err == nil {
		t.Fatal("invalid batch succeeded")
	}
	if _, err := c.ValidateEnvVars(site.ID, schema); err != nil {
		t.Fatal("invalid batch changed COUNT")
	}
	updated, err := c.SetEnvVar(site.ID, client.EnvWrite{Name: "TOKEN", Value: "private-client-marker-updated", Secret: true}, nil)
	if err != nil || updated.ID != tokenID || updated.Value != nil {
		t.Fatalf("upsert: %+v %v", updated, err)
	}
	plain, err := c.SetEnvVar(site.ID, client.EnvWrite{Name: "EMPTY", Value: "", Secret: false}, nil)
	if err != nil || plain.Value == nil || *plain.Value != "" {
		t.Fatalf("empty plaintext: %+v %v", plain, err)
	}
	listed, err := c.ListEnvVars(site.ID)
	if err != nil || len(listed) != 3 {
		t.Fatalf("list: %+v %v", listed, err)
	}
	encoded, _ := json.Marshal(listed)
	if strings.Contains(string(encoded), "private-client-marker") {
		t.Fatal("client exposed secret")
	}
	if err := c.DeleteEnvVar(site.ID, tokenID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ValidateEnvVars(site.ID, schema); err == nil {
		t.Fatal("deleted required secret unexpectedly validates")
	}
}
