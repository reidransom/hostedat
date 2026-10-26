package client

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cryguy/hostedat/internal/artifactpolicy"
	"github.com/cryguy/hostedat/internal/envschema"
)

type Deployment struct {
	ID         string    `json:"id"`
	SiteID     string    `json:"site_id"`
	Version    int       `json:"version"`
	FileHash   string    `json:"file_hash"`
	UploadedAt time.Time `json:"uploaded_at"`
}

// Deploy packages an already-built directory with the command-scoped declaration.
// A nil project or schema preserves legacy schema-less packaging.
func (c *Client) Deploy(siteID, dirPath string, project *envschema.Project) (*Deployment, error) {
	zipData, err := zipDirectory(dirPath, project)
	if err != nil {
		return nil, fmt.Errorf("failed to zip directory: %w", err)
	}
	return c.DeployArchive(siteID, zipData)
}

// DeployArchive negotiates support and validates remote bindings before uploading
// a manifest-bearing ZIP. Activation still revalidates transactionally on the server.
func (c *Client) DeployArchive(siteID string, zipData []byte) (*Deployment, error) {
	archive, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("invalid deployment ZIP")
	}
	var manifest *zip.File
	for _, file := range archive.File {
		for _, component := range strings.Split(strings.ReplaceAll(file.Name, "\\", "/"), "/") {
			if artifactpolicy.IsMetadataComponent(component) {
				if manifest != nil {
					return nil, fmt.Errorf("conflicting environment metadata in deployment ZIP")
				}
				manifest = file
			}
		}
	}
	hasManifest := manifest != nil
	uploader := *c
	if hasManifest {
		source, err := manifest.Open()
		if err != nil {
			return nil, fmt.Errorf("cannot read deployment environment declaration")
		}
		schema, err := envschema.Parse(source, envschema.JSON)
		_ = source.Close()
		if err != nil {
			return nil, err
		}
		validation, err := c.ValidateEnvVars(siteID, schema)
		if err != nil {
			return nil, fmt.Errorf("deployment environment preflight failed: %w", err)
		}
		if !validation.Valid {
			return nil, fmt.Errorf("deployment environment preflight failed: remote bindings are invalid")
		}
		uploader.safeErrors = true
	}

	// Build multipart form.
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "deploy.zip")
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, bytes.NewReader(zipData)); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finalizing multipart form: %w", err)
	}

	resp, err := uploader.do("POST", "/api/v1/sites/"+siteID+"/deploy", &body, writer.FormDataContentType())
	if err != nil {
		if hasManifest {
			return nil, safeClientError(err)
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	var deployment Deployment
	if err := json.NewDecoder(resp.Body).Decode(&deployment); err != nil {
		if hasManifest {
			return nil, safeClientError(err)
		}
		return nil, err
	}
	return &deployment, nil
}

// defaultExcludes lists file/directory names that should never be included in
// deployments. These patterns protect against accidentally shipping secrets,
// build caches, and version-control internals.
var defaultExcludes = map[string]bool{
	".git":         true,
	".DS_Store":    true,
	"node_modules": true,
	".svn":         true,
	".hg":          true,
	"__pycache__":  true,
	".terraform":   true,
}

func isExcluded(name string) bool {
	return defaultExcludes[name] || artifactpolicy.IsDotenvComponent(name)
}

func zipDirectory(dirPath string, project *envschema.Project) ([]byte, error) {
	dirPath, err := filepath.Abs(dirPath)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(dirPath)
	if err != nil {
		return nil, fmt.Errorf("directory not found: %s", dirPath)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", dirPath)
	}

	var declaration []byte
	var sourceInfo os.FileInfo
	if project != nil && project.Schema != nil {
		declaration, err = project.Schema.CanonicalJSON()
		if err != nil {
			return nil, err
		}
		if project.SchemaPath != "" {
			sourceInfo, err = os.Stat(project.SchemaPath)
			if err != nil {
				return nil, fmt.Errorf("cannot inspect selected environment declaration")
			}
		}
	}

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	err = filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip excluded files and directories
		if isExcluded(info.Name()) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip symlinks
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		// Exclude the selected source, including resolved-path/hard-link aliases.
		// Its parsed snapshot is emitted once as canonical private metadata below.
		if sourceInfo != nil && os.SameFile(info, sourceInfo) {
			return nil
		}
		if artifactpolicy.IsMetadataComponent(info.Name()) {
			return fmt.Errorf("reserved environment metadata requires schema-aware packaging")
		}

		if info.IsDir() {
			return nil
		}

		// Relative path with forward slashes (zip standard)
		relPath, err := filepath.Rel(dirPath, path)
		if err != nil {
			return err
		}
		relPath = strings.ReplaceAll(relPath, string(filepath.Separator), "/")

		f, err := w.Create(relPath)
		if err != nil {
			return err
		}

		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = src.Close() }()

		_, err = io.Copy(f, src)
		return err
	})
	if err != nil {
		return nil, err
	}

	if declaration != nil {
		manifest, err := w.Create(artifactpolicy.EnvManifest)
		if err != nil {
			return nil, err
		}
		if _, err := manifest.Write(declaration); err != nil {
			return nil, err
		}
	}

	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
