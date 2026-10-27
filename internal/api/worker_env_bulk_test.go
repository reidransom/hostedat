package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cryguy/hostedat/internal/environment"
	"github.com/cryguy/hostedat/internal/models"
)

func TestWorkerEnvBulkAndValidation(t *testing.T) {
	env := setupTestEnv(t)
	owner, token := env.createTestUser(t, "bulk@test.local", "password123", "user")
	other, otherToken := env.createTestUser(t, "bulk-other@test.local", "password123", "user")
	_, admin := env.createTestUser(t, "bulk-admin@test.local", "password123", "admin")
	site := models.Site{UserID: owner.ID, SubdomainSlug: "bulk", Name: "Bulk"}
	foreign := models.Site{UserID: other.ID, SubdomainSlug: "foreign", Name: "Foreign"}
	for _, s := range []*models.Site{&site, &foreign} {
		if err := env.db.Create(s).Error; err != nil {
			t.Fatal(err)
		}
	}
	base := "/api/v1/sites/" + site.ID + "/worker/env"
	marker := "task06-disposable-value-marker"
	request := func(method, path, body, auth string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+auth)
		rec := env.doRequest(req)
		if rec.Code != status {
			t.Fatalf("%s %s status=%d want=%d body=%s", method, path, rec.Code, status, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "no-store" || strings.Contains(rec.Body.String(), marker) {
			t.Fatal("unsafe management response")
		}
		return rec
	}
	bulk := func(body string, status int) *httptest.ResponseRecorder {
		return request("POST", base+"/bulk", body, token, status)
	}
	body := `{"entries":[{"name":"TOKEN","value":"` + marker + `","secret":true},{"name":"COUNT","value":"12","secret":true},{"name":"LEGACY","value":"plain","secret":false}]}`
	rec := bulk(body, 200)
	var entries []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if _, ok := entry["value"]; ok {
			t.Fatal("bulk returned a value")
		}
	}
	tokenID := entries[0]["id"].(string)
	schema := `{"TOKEN":{"type":"string"},"COUNT":{"type":"number"},"OPTIONAL":{"type":"boolean","required":false}}`
	request("POST", base+"/validate", `{"schema":`+schema+`}`, token, 200)
	request("POST", base+"/validate", `{"schema":{"LEGACY":{"type":"string"}}}`, token, 400)
	request("POST", base+"/validate", `{"schema":{"MISSING":{"type":"string"}}}`, token, 400)
	request("POST", base+"/validate", `{"schema":{"COUNT":{"type":"boolean"}}}`, token, 400)
	request("POST", base+"/validate", `{"schema":{},"values":{"TOKEN":"`+marker+`"}}`, token, 400)
	for _, bad := range []string{
		`{"entries":[]}`, `{}`, `{"entries":[{"name":"X","secret":true}]}`, `{"entries":[{"name":"X","value":null}]}`,
		`{"entries":[{"name":"COUNT","value":"99","secret":true},{"name":"COUNT","value":"1"}]}`,
		`{"entries":[{"name":"COUNT","value":"99","secret":true},{"name":"bad-name","value":"` + marker + `"}]}`,
		`{"entries":[{"name":"COUNT","value":"99","secret":true}],"schema":{"COUNT":{"type":"boolean"}}}`,
		`{"entries":[{"name":"UNDECLARED","value":"x","secret":true}],"schema":{}}`,
		`{"entries":[{"name":"LEGACY","value":"x"}],"schema":{"LEGACY":{"type":"string"}}}`,
		`{"entries":[{"name":"COUNT","value":"99"}],"schema":{"COUNT":{"type":"number","type":"string"}}}`,
		`{"entries":[{"name":"X","value":"` + strings.Repeat("x", environment.MaxValueBytes+1) + `"}]}`,
	} {
		bulk(bad, 400)
	}
	tooMany := make([]environment.Write, environment.MaxBatchEntries+1)
	v := "x"
	for i := range tooMany {
		tooMany[i] = environment.Write{Name: fmt.Sprintf("N%d", i), Value: &v}
	}
	encoded, _ := json.Marshal(map[string]any{"entries": tooMany})
	bulk(string(encoded), 400)
	large := make([]environment.Write, 5)
	v = strings.Repeat("x", environment.MaxValueBytes)
	for i := range large {
		large[i] = environment.Write{Name: fmt.Sprintf("L%d", i), Value: &v}
	}
	encoded, _ = json.Marshal(map[string]any{"entries": large})
	bulk(string(encoded), 400)
	bulk(`{"entries":[],"padding":"`+strings.Repeat("x", environment.MaxRequestBytes)+`"}`, 400)
	// Failure of the second SQL write rolls back the first and emits no success audit.
	var auditBefore int64
	env.db.Model(&models.AuditLog{}).Where("action = ?", "worker.env.bulk").Count(&auditBefore)
	if err := env.db.Exec("CREATE TRIGGER fail_bulk BEFORE INSERT ON worker_env_vars WHEN NEW.name = 'FAIL' BEGIN SELECT RAISE(ABORT, 'private-storage-marker'); END").Error; err != nil {
		t.Fatal(err)
	}
	bulk(`{"entries":[{"name":"COUNT","value":"99","secret":true},{"name":"FAIL","value":"`+marker+`","secret":true}]}`, 500)
	var auditAfter int64
	env.db.Model(&models.AuditLog{}).Where("action = ?", "worker.env.bulk").Count(&auditAfter)
	if auditBefore != auditAfter {
		t.Fatal("rolled back mutation audited as success")
	}
	if err := env.db.Exec("DROP TRIGGER fail_bulk").Error; err != nil {
		t.Fatal(err)
	}
	var count models.WorkerEnvVar
	env.db.First(&count, "site_id = ? AND name = ?", site.ID, "COUNT")
	// Remote validation must decrypt the persisted (unchanged) numeric value.
	request("POST", base+"/validate", `{"schema":{"COUNT":{"type":"enum","values":["12"]}}}`, token, 200)
	// Candidate required omissions are allowed for merge; it does not activate.
	bulk(`{"entries":[{"name":"COUNT","value":"13","secret":true}],"schema":`+schema+`}`, 200)
	request("POST", base, `{"name":"COUNT","value":"not-a-number"}`, token, 200)
	// Install an established active snapshot, as deployment task 09 will do.
	declaration := `{"TOKEN":{"type":"string"},"COUNT":{"type":"number"},"OPTIONAL":{"type":"boolean","required":false}}`
	request("POST", base, `{"name":"COUNT","value":"12"}`, token, 200)
	deployment := models.Deployment{SiteID: site.ID, Version: 1, FileHash: "test", EnvDeclaration: &declaration}
	if err := env.db.Create(&deployment).Error; err != nil {
		t.Fatal(err)
	}
	if err := env.db.Model(&site).Updates(map[string]any{"active_deploy_id": deployment.ID, "env_declarations_enforced": true}).Error; err != nil {
		t.Fatal(err)
	}
	request("POST", base, `{"name":"COUNT","value":"bad"}`, token, 400)
	request("POST", base, `{"name":"TOKEN","value":""}`, token, 400)
	request("POST", base, `{"name":"TOKEN"}`, token, 400)
	request("POST", base, `{"name":"TOKEN","value":null}`, token, 400)
	request("POST", base+"/validate", `{"schema":null}`, token, 400)
	request("POST", base+"/validate", `{"schema":{}} {}`, token, 400)
	request("POST", base, `{"name":"TOKEN","value":"x","secret":false}`, token, 400)
	request("DELETE", base+"/"+tokenID, "", token, 400)
	bulk(`{"entries":[{"name":"COUNT","value":"bad","secret":true}],"schema":{"COUNT":{"type":"string"}}}`, 400)
	request("POST", base, `{"name":"LEGACY","value":"","secret":false}`, token, 200)
	request("POST", base, `{"name":"OPTIONAL","value":"false","secret":true}`, token, 200)
	request("POST", base, `{"name":"OPTIONAL","value":"bad"}`, token, 400)
	request("POST", base, `{"name":"OPTIONAL","value":"false","secret":false}`, token, 400)
	var optional models.WorkerEnvVar
	env.db.First(&optional, "site_id = ? AND name = ?", site.ID, "OPTIONAL")
	request("DELETE", base+"/"+optional.ID, "", token, 204)
	// Every operation rejects another user, missing auth and a different owner's site.
	for _, op := range []struct{ method, suffix, body string }{{"GET", "", ""}, {"POST", "", `{"name":"TOKEN","value":"x"}`}, {"DELETE", "/" + tokenID, ""}, {"POST", "/bulk", body}, {"POST", "/validate", `{"schema":{}}`}} {
		request(op.method, base+op.suffix, op.body, otherToken, 403)
		request(op.method, base+op.suffix, op.body, "", 401)
		request(op.method, "/api/v1/sites/"+foreign.ID+"/worker/env"+op.suffix, op.body, token, 403)
		request(op.method, "/api/v1/sites/missing/worker/env"+op.suffix, op.body, token, 404)
	}
	request("DELETE", "/api/v1/sites/"+foreign.ID+"/worker/env/"+tokenID, "", otherToken, 404)
	request("POST", base+"/validate", `{"schema":`+schema+`}`, admin, 200)
	bulk(`{"entries":[{"name":"COUNT","value":"14"},{"name":"TOKEN","value":"`+marker+`"}]}`, 200)
	env.db.First(&count, "site_id = ? AND name = ?", site.ID, "COUNT")
	if count.ID != entries[1]["id"].(string) || !count.Secret {
		t.Fatal("merge changed identity/classification")
	}
	// Audit remains best-effort after commit, not a condition for mutation success.
	if err := env.db.Exec("CREATE TRIGGER fail_env_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT, 'audit-unavailable'); END").Error; err != nil {
		t.Fatal(err)
	}
	bulk(`{"entries":[{"name":"COUNT","value":"15"}]}`, 200)
	if err := env.db.Exec("DROP TRIGGER fail_env_audit").Error; err != nil {
		t.Fatal(err)
	}
	if err := env.db.Model(&count).Update("ciphertext", []byte("tampered")).Error; err != nil {
		t.Fatal(err)
	}
	request("POST", base+"/validate", `{"schema":`+schema+`}`, token, 500)
	var audits []models.AuditLog
	env.db.Find(&audits)
	encoded, _ = json.Marshal(audits)
	if strings.Contains(string(encoded), marker) {
		t.Fatal("audit exposed value")
	}
}
