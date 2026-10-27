package envschema

import (
	"errors"
	"os"
	"path/filepath"
)

// Project is a command-scoped snapshot of the selected declaration. Schema is
// nil when neither default exists, distinct from an explicit empty declaration.
// Paths are absolute and independent of subsequent working-directory changes.
// DotenvPath belongs to the selected schema's directory (the invocation directory
// without a schema). A symlinked schema keeps its named directory as the root.
// Only LocalValues may read DotenvPath; discovery never opens local dotenv data.
type Project struct {
	Schema     Schema
	SchemaPath string
	Format     Format
	DotenvPath string

	invocationDir string
}

// Discover selects env.json or env.yml only in invocationDir, or an explicit
// schema path relative to that directory. It parses the selection exactly once.
// Both defaults require explicit selection; no parent directories are searched.
func Discover(invocationDir, explicitSchema string) (*Project, error) {
	dir, err := filepath.Abs(invocationDir)
	if err != nil {
		return nil, inputError("cannot resolve invocation directory")
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, inputError("cannot resolve invocation directory")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, inputError("invalid invocation directory")
	}
	project := &Project{DotenvPath: filepath.Join(dir, ".env"), invocationDir: dir}
	path := explicitSchema
	if path == "" {
		for _, name := range []string{"env.json", "env.yml"} {
			candidate := filepath.Join(dir, name)
			_, err := os.Lstat(candidate)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, inputError("cannot inspect default schema")
			}
			if path != "" {
				return nil, inputError("both env.json and env.yml exist; select --schema explicitly")
			}
			path = candidate
		}
		if path == "" {
			return project, nil
		}
	}
	path = project.resolve(path)
	var format Format
	switch filepath.Ext(path) {
	case ".json":
		format = JSON
	case ".yml":
		format = YAML
	default:
		return nil, inputError("unsupported schema extension; use .json or .yml")
	}
	file, err := openInput(path)
	if err != nil {
		return nil, inputError("cannot open schema as a regular file")
	}
	defer file.Close()
	schema, err := Parse(file, format)
	if err != nil {
		return nil, err
	}
	project.Schema, project.SchemaPath, project.Format = schema, path, format
	project.DotenvPath = filepath.Join(filepath.Dir(path), ".env")
	return project, nil
}

func (p *Project) resolve(path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(p.invocationDir, path)
	}
	return filepath.Clean(path)
}

// LocalValues reads only the associated root .env (absence is allowed) and
// overlays explicitly present declared shell variables, including empty values.
// lookup is normally os.LookupEnv; nil disables shell overlay. This method never
// writes files, changes the process environment, executes code or uses a network.
// Call Schema.Validate on its result for a complete local check.
func (p *Project) LocalValues(lookup func(string) (string, bool)) (map[string]string, error) {
	if p.Schema == nil {
		return nil, inputError("local checks require an environment declaration")
	}
	values := make(map[string]string)
	file, err := openInput(p.DotenvPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, inputError("cannot open local dotenv as a regular file")
	}
	if err == nil {
		defer file.Close()
		values, err = ParseDotenv(file)
		if err != nil {
			return nil, err
		}
	}
	if lookup != nil {
		for _, name := range p.Schema.Names() {
			if value, present := lookup(name); present {
				values[name] = value
			}
		}
	}
	return values, nil
}

// ReadSyncSource accepts a named regular dotenv file relative to the invocation
// directory. It never applies shell overrides. The project's local .env cannot
// be a sync source, even through cleaned paths, directory/file symlinks or hard
// links. Identity is checked on the opened source, not only its input pathname.
// Schema/value and binding-collision validation remain the caller's concern.
func (p *Project) ReadSyncSource(path string) (map[string]string, error) {
	if path == "" || path == "-" {
		return nil, inputError("sync requires a named dotenv file")
	}
	path = p.resolve(path)
	if path == p.DotenvPath {
		return nil, inputError("the project's local .env cannot be synced")
	}
	file, err := openInput(path)
	if err != nil {
		return nil, inputError("cannot open sync source as a regular file")
	}
	defer file.Close()
	sourceInfo, err := file.Stat()
	if err != nil {
		return nil, inputError("cannot inspect sync source")
	}
	localInfo, err := os.Stat(p.DotenvPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, inputError("cannot inspect local dotenv")
	}
	if err == nil && os.SameFile(sourceInfo, localInfo) {
		return nil, inputError("the project's local .env cannot be synced")
	}
	return ParseDotenv(file)
}

// Inspect before opening to reject ordinary FIFOs/devices without blocking, then
// inspect the opened file too. Preserve errors internally for missing-file logic;
// callers replace them with value-free diagnostics before returning them.
func openInput(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, inputError("expected regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, inputError("expected regular file")
	}
	return file, nil
}
