package storage

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/cryguy/hostedat/internal/artifactpolicy"
	"github.com/cryguy/hostedat/internal/envschema"
)

const (
	maxFileSize          = 25 << 20  // 25 MB per file (matches Cloudflare Pages)
	maxFileCount         = 20000     // 20k files (matches Cloudflare Pages)
	MaxZipSize           = 25 << 20  // 25 MB compressed upload
	maxTotalUncompressed = 256 << 20 // 256 MB aggregate uncompressed limit
)

type Manager struct {
	BasePath string
}

func NewManager(basePath string) *Manager {
	return &Manager{BasePath: basePath}
}

// ExtractZip preflights the entire archive before creating deployment files.
// It returns canonical declaration metadata separately; it is never extracted
// into the public asset directory. nil distinguishes legacy uploads from {}.
func (m *Manager) ExtractZip(siteID string, deployKey string, reader io.ReaderAt, size int64) (declaration *string, extractErr error) {
	if size > MaxZipSize {
		return nil, fmt.Errorf("zip file too large (max %d bytes)", MaxZipSize)
	}
	zr, err := zip.NewReader(reader, size)
	if err != nil {
		return nil, fmt.Errorf("reading zip: %w", err)
	}
	if len(zr.File) > maxFileCount {
		return nil, fmt.Errorf("zip contains too many files (max %d)", maxFileCount)
	}

	// Validate original components before stripping any supported wrapper.
	for _, f := range zr.File {
		if err := validateArchivePath(f.Name); err != nil {
			return nil, err
		}
		mode := f.Mode()
		if !mode.IsDir() && !mode.IsRegular() {
			return nil, fmt.Errorf("zip contains unsupported file type")
		}
		if f.UncompressedSize64 > maxFileSize {
			return nil, fmt.Errorf("zip file exceeds maximum size")
		}
	}
	prefix := detectSingleTopDir(zr.File)
	names := make([]string, len(zr.File))
	destinations := make(map[string]bool, len(zr.File))
	var headerTotal uint64
	var metadataBytes uint64
	for i, f := range zr.File {
		name := strings.TrimPrefix(f.Name, prefix)
		if name == "" {
			continue // explicit wrapper directory
		}
		if err := validateArchivePath(name); err != nil {
			return nil, err
		}
		name = path.Clean(name)
		if name == "." {
			return nil, fmt.Errorf("zip contains invalid destination")
		}
		if _, exists := destinations[name]; exists {
			return nil, fmt.Errorf("zip contains duplicate destination")
		}
		destinations[name] = f.Mode().IsDir()
		names[i] = name
		headerTotal += f.UncompressedSize64
		if headerTotal > maxTotalUncompressed {
			return nil, fmt.Errorf("zip total uncompressed size exceeds limit")
		}
		for _, component := range strings.Split(name, "/") {
			if artifactpolicy.IsMetadataComponent(component) && (name != artifactpolicy.EnvManifest || f.Mode().IsDir()) {
				return nil, fmt.Errorf("environment metadata must be an artifact-root file with its canonical name")
			}
		}
	}
	// File/directory conflicts are invalid even without explicit dir entries.
	for name := range destinations {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if isDir, exists := destinations[parent]; exists && !isDir {
				return nil, fmt.Errorf("zip contains conflicting destinations")
			}
		}
	}
	for i, f := range zr.File {
		if names[i] != artifactpolicy.EnvManifest {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("cannot open environment metadata")
		}
		limited := &io.LimitedReader{R: rc, N: envschema.MaxDocumentBytes + 1}
		schema, parseErr := envschema.Parse(limited, envschema.JSON)
		metadataBytes = uint64(envschema.MaxDocumentBytes + 1 - limited.N)
		closeErr := rc.Close()
		if parseErr != nil {
			return nil, parseErr
		}
		if closeErr != nil {
			return nil, fmt.Errorf("cannot read environment metadata")
		}
		canonical, err := schema.CanonicalJSON()
		if err != nil {
			return nil, err
		}
		value := string(canonical)
		declaration = &value
	}

	destDir := m.GetDeploymentPath(siteID, deployKey)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, fmt.Errorf("creating deployment dir: %w", err)
	}
	defer func() {
		if extractErr != nil {
			_ = os.RemoveAll(destDir)
		}
	}()
	totalUncompressed := metadataBytes
	for i, f := range zr.File {
		name := names[i]
		if name == "" || name == artifactpolicy.EnvManifest {
			continue
		}
		destPath := filepath.Join(destDir, filepath.FromSlash(name))
		if f.Mode().IsDir() {
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return nil, fmt.Errorf("creating deployment directory: %w", err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return nil, fmt.Errorf("creating deployment parent: %w", err)
		}
		written, err := extractFile(f, destPath)
		if err != nil {
			return nil, err
		}
		totalUncompressed += uint64(written)
		if totalUncompressed > maxTotalUncompressed {
			return nil, fmt.Errorf("zip total uncompressed size exceeds limit")
		}
	}
	return declaration, nil
}

func validateArchivePath(name string) error {
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\\x00") ||
		(len(name) >= 2 && name[1] == ':') {
		return fmt.Errorf("zip contains invalid path")
	}
	components := strings.Split(name, "/")
	for i, component := range components {
		if artifactpolicy.IsMetadataComponent(component) &&
			(component != artifactpolicy.EnvManifest || i != len(components)-1) {
			return fmt.Errorf("zip contains misplaced environment metadata")
		}
	}
	for _, component := range strings.Split(name, "/") {
		if component == ".." || artifactpolicy.IsDotenvComponent(component) {
			return fmt.Errorf("zip contains forbidden path component")
		}
	}
	return nil
}

func detectSingleTopDir(files []*zip.File) string {
	if len(files) == 0 {
		return ""
	}

	var topDir string
	hasDirEntry := false
	for _, f := range files {
		parts := strings.SplitN(f.Name, "/", 2)
		if len(parts) == 1 && !f.Mode().IsDir() {
			return ""
		}
		if topDir == "" {
			topDir = parts[0]
		} else if parts[0] != topDir {
			return ""
		}
		// Track whether any entry actually lives inside this directory.
		if len(parts) == 2 && parts[1] != "" {
			hasDirEntry = true
		}
	}

	// Only strip if all entries share the same top-level directory and at
	// least one entry exists inside it (i.e., it's truly a wrapper directory).
	if !hasDirEntry {
		return ""
	}

	return topDir + "/"
}

func extractFile(f *zip.File, destPath string) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, fmt.Errorf("opening zip entry %s: %w", f.Name, err)
	}
	defer func() { _ = rc.Close() }()

	out, err := os.Create(destPath)
	if err != nil {
		return 0, fmt.Errorf("creating file %s: %w", destPath, err)
	}

	written, err := io.Copy(out, io.LimitReader(rc, maxFileSize+1))
	if err != nil {
		_ = out.Close()
		return 0, fmt.Errorf("writing file %s: %w", destPath, err)
	}
	if written > maxFileSize {
		_ = out.Close()
		return 0, fmt.Errorf("file exceeds maximum size during extraction: %s", f.Name)
	}

	if err := out.Close(); err != nil {
		return 0, fmt.Errorf("closing file %s: %w", destPath, err)
	}
	return written, nil
}

func (m *Manager) GetDeploymentPath(siteID string, deployKey string) string {
	return filepath.Join(m.BasePath, siteID, deployKey)
}

func (m *Manager) DeleteSite(siteID string) error {
	return os.RemoveAll(filepath.Join(m.BasePath, siteID))
}

// ResolveFile tries to find a file at the given request path.
// It checks: exact path, path/index.html, path.html
func (m *Manager) ResolveFile(deploymentPath, requestPath string) (string, bool) {
	if !artifactpolicy.PublicPathAllowed(requestPath) {
		return "", false
	}
	// Clean and normalize the path
	requestPath = strings.TrimPrefix(requestPath, "/")
	if requestPath == "" {
		requestPath = "index.html"
	}

	// Try exact path
	fullPath := filepath.Join(deploymentPath, filepath.FromSlash(requestPath))

	// Path traversal protection
	absDeployment, err := filepath.Abs(deploymentPath)
	if err != nil {
		return "", false
	}
	absFullPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", false
	}
	if !strings.HasPrefix(absFullPath, absDeployment+string(os.PathSeparator)) && absFullPath != absDeployment {
		return "", false
	}

	for _, candidate := range []string{fullPath, filepath.Join(fullPath, "index.html"), fullPath + ".html"} {
		if !isFile(candidate) {
			continue
		}
		// Old deployments may contain symlinks or forbidden artifacts. Check
		// the physical destination too, never just the incoming URL spelling.
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		root, err := filepath.EvalSymlinks(absDeployment)
		if err != nil {
			return "", false
		}
		rel, err := filepath.Rel(root, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) ||
			!artifactpolicy.PublicPathAllowed(filepath.ToSlash(rel)) {
			continue
		}
		relativeCandidate, err := filepath.Rel(deploymentPath, candidate)
		if err == nil && artifactpolicy.PublicPathAllowed(filepath.ToSlash(relativeCandidate)) {
			return candidate, true
		}
	}
	return "", false
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// HasWorkerScript checks if a _worker.js file exists in the deployment.
func (m *Manager) HasWorkerScript(siteID string, deployKey string) bool {
	workerPath := filepath.Join(m.GetDeploymentPath(siteID, deployKey), "_worker.js")
	return isFile(workerPath)
}

// GetWorkerScript reads the _worker.js source from a deployment.
func (m *Manager) GetWorkerScript(siteID string, deployKey string) (string, error) {
	workerPath := filepath.Join(m.GetDeploymentPath(siteID, deployKey), "_worker.js")
	data, err := os.ReadFile(workerPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// GetWorkerBytecodeDir returns the path to the .worker directory for a deployment.
func (m *Manager) GetWorkerBytecodeDir(siteID string, deployKey string) string {
	return filepath.Join(m.GetDeploymentPath(siteID, deployKey), ".worker")
}
