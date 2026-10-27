// Package artifactpolicy defines filename rules shared by packaging, extraction
// and resolved public serving. It does not inspect file contents for secrets.
package artifactpolicy

import (
	"path"
	"strings"
)

const EnvManifest = "_hostedat.env.json"

// IsDotenvComponent is case-insensitive on every platform, including Linux.
func IsDotenvComponent(name string) bool {
	return strings.EqualFold(name, ".env") ||
		(len(name) >= 5 && name[4] == '.' && strings.EqualFold(name[:4], ".env"))
}

func IsMetadataComponent(name string) bool {
	return strings.EqualFold(name, EnvManifest)
}

// PublicPathAllowed checks both the spelling supplied by a caller and its
// normalized destination, so normalization cannot hide a reserved component.
// URL callers pass the already-decoded URL.Path, not the escaped URL string.
func PublicPathAllowed(name string) bool {
	if strings.ContainsAny(name, "\\\x00") {
		return false
	}
	for _, candidate := range []string{name, path.Clean(name)} {
		for _, component := range strings.Split(candidate, "/") {
			if IsDotenvComponent(component) || IsMetadataComponent(component) {
				return false
			}
		}
	}
	return true
}
