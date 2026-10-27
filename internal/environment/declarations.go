package environment

import (
	"strings"

	"github.com/cryguy/hostedat/internal/envschema"
	"github.com/cryguy/hostedat/internal/models"
	"gorm.io/gorm"
)

type declarationSnapshot struct {
	deploymentID string
	document     string
	schema       envschema.Schema
}

// Declaration parses an immutable deployment snapshot once while it is loaded.
// Keep only the latest loaded snapshot per site, not a history-sized cache. The
// returned schema is read-only; values are never cached with the declaration.
func (s *Service) Declaration(dep *models.Deployment) (envschema.Schema, error) {
	if dep.EnvDeclaration == nil {
		return nil, nil
	}
	s.schemaMu.Lock()
	defer s.schemaMu.Unlock()
	cached, ok := s.schemas[dep.SiteID]
	if ok && cached.deploymentID == dep.ID && cached.document == *dep.EnvDeclaration {
		return cached.schema, nil
	}
	schema, err := envschema.Parse(strings.NewReader(*dep.EnvDeclaration), envschema.JSON)
	if err != nil {
		return nil, ErrStorage
	}
	if s.schemas == nil {
		s.schemas = make(map[string]declarationSnapshot)
	}
	s.schemas[dep.SiteID] = declarationSnapshot{dep.ID, *dep.EnvDeclaration, schema}
	return schema, nil
}

// ValidateActivation must run again in WithSiteTransaction immediately before
// activation; the same read-only check may also be used before worker compilation.
func (s *Service) ValidateActivation(tx *gorm.DB, site *models.Site, schema envschema.Schema) error {
	if schema == nil {
		if site.EnvDeclarationsEnforced {
			return &envschema.Diagnostic{Constraint: "deployment requires an environment declaration"}
		}
		return nil
	}
	return s.ValidateStored(tx, site.ID, schema)
}
