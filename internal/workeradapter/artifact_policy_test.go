package workeradapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cryguy/hostedat/internal/storage"
	"github.com/cryguy/worker/v2"
)

func TestAssetsArtifactPolicy(t *testing.T) {
	store := storage.NewManager(t.TempDir())
	root := store.GetDeploymentPath("site", "deploy")
	for name, value := range map[string]string{"index.html": "safe-spa", ".env": "private-marker", "nested/.ENV.production": "private-marker", "_hostedat.env.json": "private-marker", "_redirects": "/rewrite /.env 200\n/meta /_hostedat.env.json 200\n"} {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	fetcher := &StaticAssetsFetcher{Store: store, Cache: storage.NewSiteRulesCache(), SiteID: "site", DeployKey: "deploy", SPAMode: true}
	for _, name := range []string{"/.env", "/%2eenv", "/nested/.ENV.production", "/_hostedat.env.json", "/./_hostedat.env.json", "/a/../.env", "/rewrite", "/meta"} {
		resp, err := fetcher.Fetch(&worker.WorkerRequest{Method: "GET", URL: "https://site.example" + name})
		if err != nil || resp.StatusCode != 404 || strings.Contains(string(resp.Body), "private-marker") {
			t.Fatalf("asset %s not blocked: %v %+v", name, err, resp)
		}
	}
	for _, name := range []string{"/", "/normal-spa"} {
		resp, err := fetcher.Fetch(&worker.WorkerRequest{Method: "GET", URL: "https://site.example" + name})
		if err != nil || resp.StatusCode != 200 || string(resp.Body) != "safe-spa" {
			t.Fatal("safe SPA broken")
		}
	}
}
