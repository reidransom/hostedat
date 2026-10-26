package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cryguy/hostedat/internal/models"
)

func TestWorkerEnvManagementResponses(t *testing.T) {
	env := setupTestEnv(t)
	owner, token := env.createTestUser(t, "env-owner@test.com", "password123", "user")
	_, otherToken := env.createTestUser(t, "env-other@test.com", "password123", "user")
	_, adminToken := env.createTestUser(t, "env-admin@test.com", "password123", "admin")
	site := models.Site{UserID: owner.ID, SubdomainSlug: "env-responses", Name: "Environment"}
	otherSite := models.Site{UserID: owner.ID, SubdomainSlug: "env-other", Name: "Other"}
	if err := env.db.Create(&site).Error; err != nil {
		t.Fatal(err)
	}
	if err := env.db.Create(&otherSite).Error; err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/sites/" + site.ID + "/worker/env"
	marker := "disposable-task03-secret-marker"

	request := func(method, path, body, auth string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+auth)
		rec := env.doRequest(req)
		if rec.Code != status {
			t.Fatalf("%s %s: status %d, want %d", method, path, rec.Code, status)
		}
		if strings.Contains(rec.Body.String(), marker) {
			t.Fatal("management response disclosed secret marker")
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Error("management response must disable caching")
		}
		return rec
	}
	secretBody := `{"name":"TOKEN","value":"` + marker + `","secret":true}`
	request(http.MethodPost, base, secretBody, "", http.StatusUnauthorized)
	request(http.MethodGet, base, "", "invalid-token", http.StatusUnauthorized)
	request(http.MethodDelete, base+"/missing", "", "", http.StatusUnauthorized)
	created := parseJSON(t, request(http.MethodPost, base, secretBody, token, http.StatusOK))
	if _, present := created["value"]; present {
		t.Fatal("secret create response contains value")
	}
	if created["secret"] != true {
		t.Fatal("secret classification missing")
	}
	updated := parseJSON(t, request(http.MethodPost, base, secretBody, token, http.StatusOK))
	if updated["id"] != created["id"] {
		t.Error("upsert changed ID")
	}
	if _, present := updated["value"]; present {
		t.Error("secret update response contains value")
	}
	plain := parseJSON(t, request(http.MethodPost, base, `{"name":"EMPTY","value":"","secret":false}`, token, http.StatusOK))
	if value, present := plain["value"]; !present || value != "" {
		t.Error("empty plaintext write value not preserved")
	}
	listed := request(http.MethodGet, base, "", token, http.StatusOK)
	var entries []map[string]interface{}
	if err := json.Unmarshal(listed.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	for _, entry := range entries {
		value, present := entry["value"]
		if entry["secret"] == true && present {
			t.Error("secret list entry contains value")
		}
		if entry["name"] == "EMPTY" && (!present || value != "") {
			t.Error("empty plaintext list value not preserved")
		}
	}
	request(http.MethodPost, base, `{"value":"`+marker+`"}`, token, http.StatusBadRequest)
	request(http.MethodPost, base, `{"name":"TOKEN","value":"`+marker+`","secret":"`+marker+`"}`, token, http.StatusBadRequest)
	request(http.MethodPost, "/api/v1/sites/missing/worker/env", secretBody, token, http.StatusNotFound)
	request(http.MethodPost, base, secretBody, otherToken, http.StatusForbidden)
	request(http.MethodGet, base, "", otherToken, http.StatusForbidden)
	request(http.MethodDelete, base+"/"+created["id"].(string), "", otherToken, http.StatusForbidden)
	request(http.MethodDelete, "/api/v1/sites/"+otherSite.ID+"/worker/env/"+created["id"].(string), "", token, http.StatusNotFound)
	request(http.MethodGet, base, "", adminToken, http.StatusOK)
	request(http.MethodDelete, base+"/"+created["id"].(string), "", adminToken, http.StatusNoContent)
	request(http.MethodDelete, base+"/"+created["id"].(string), "", token, http.StatusNotFound)
	if err := env.db.Migrator().DropTable(&models.WorkerEnvVar{}); err != nil {
		t.Fatal(err)
	}
	request(http.MethodPost, base, secretBody, token, http.StatusInternalServerError)
	request(http.MethodGet, base, "", token, http.StatusInternalServerError)
}
