package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerVersionRejectsURLCredentials(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
	}))
	defer srv.Close()
	c := New(strings.Replace(srv.URL, "http://", "http://user:private-marker@", 1), "token")
	_, err := c.ServerVersion()
	if err == nil || strings.Contains(err.Error(), "private-marker") || requests != 0 {
		t.Fatalf("version endpoint sent URL credentials or exposed them: %v requests=%d", err, requests)
	}
}
