package environment

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/cryguy/hostedat/internal/envschema"
	"github.com/cryguy/hostedat/internal/models"
	"gorm.io/gorm"
)

// Shared wire/input bounds; requests include JSON escaping and declaration overhead.
const (
	MaxBatchEntries    = 256
	MaxBatchValueBytes = 4 << 20
	MaxRequestBytes    = 8 << 20
)

func safeError(err error) error {
	var diagnostic *envschema.Diagnostic
	if err == nil || errors.Is(err, ErrInput) || errors.As(err, &diagnostic) {
		return err
	}
	return ErrStorage
}

// ActiveSchema reads the running deployment inside the caller's site transaction.
// A candidate document never changes this snapshot or the site's enforcement flag.
func ActiveSchema(tx *gorm.DB, site *models.Site) (envschema.Schema, error) {
	if site.ActiveDeployID == nil {
		if site.EnvDeclarationsEnforced {
			return nil, ErrStorage
		}
		return nil, nil
	}
	var deployment models.Deployment
	if err := tx.Select("env_declaration").First(&deployment, "id = ? AND site_id = ?", *site.ActiveDeployID, site.ID).Error; err != nil {
		return nil, ErrStorage
	}
	if deployment.EnvDeclaration == nil {
		if site.EnvDeclarationsEnforced {
			return nil, ErrStorage
		}
		return nil, nil
	}
	schema, err := envschema.Parse(strings.NewReader(*deployment.EnvDeclaration), envschema.JSON)
	if err != nil {
		return nil, ErrStorage
	}
	return schema, nil
}

// ValidateStored checks only declared rows, including optional-but-present ones.
// It is read-only and uses the supplied database snapshot/transaction.
func (s *Service) ValidateStored(tx *gorm.DB, siteID string, schema envschema.Schema) error {
	if err := schema.Check(); err != nil {
		return err
	}
	values := make(map[string]string, len(schema))
	for _, name := range schema.Names() {
		row, err := models.FindWorkerEnvVar(tx, siteID, name)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return ErrStorage
		}
		if !row.Secret {
			return &envschema.Diagnostic{Name: name, Constraint: "must be a secret"}
		}
		value, err := s.value(row)
		if err != nil {
			return ErrStorage
		}
		values[name] = value
	}
	return schema.Validate(values)
}

// Validate checks a candidate against decrypted remote values without writes or
// activation. A read transaction keeps the complete validation on one snapshot.
func (s *Service) Validate(siteID string, schema envschema.Schema) error {
	if schema == nil {
		return ErrInput
	}
	return safeError(s.DB.Transaction(func(tx *gorm.DB) error { return s.ValidateStored(tx, siteID, schema) }))
}

// Bulk merges supplied names in one coordinated transaction. Candidate rules
// constrain supplied entries only; running rules constrain the resulting state.
func (s *Service) Bulk(siteID string, writes []Write, candidate envschema.Schema) ([]models.WorkerEnvVar, error) {
	if len(writes) == 0 || len(writes) > MaxBatchEntries {
		return nil, ErrInput
	}
	if candidate != nil {
		if err := candidate.Check(); err != nil {
			return nil, err
		}
	}
	seen := make(map[string]bool, len(writes))
	size := 0
	for _, w := range writes {
		if w.Value == nil || len(*w.Value) > MaxValueBytes || !utf8.ValidString(*w.Value) || envschema.ValidateName(w.Name) != nil || seen[w.Name] {
			return nil, ErrInput
		}
		seen[w.Name] = true
		size += len(w.Name) + len(*w.Value)
		if size > MaxBatchValueBytes {
			return nil, ErrInput
		}
		if candidate != nil {
			if err := candidate.ValidateValue(w.Name, *w.Value, true); err != nil {
				return nil, err
			}
		}
	}
	rows := make([]models.WorkerEnvVar, len(writes))
	err := models.WithSiteTransaction(s.DB, siteID, func(tx *gorm.DB, site *models.Site) error {
		active, err := ActiveSchema(tx, site)
		if err != nil {
			return err
		}
		// Prepare and validate every entry before the first database mutation.
		for i, w := range writes {
			row, err := models.FindWorkerEnvVar(tx, siteID, w.Name)
			create := errors.Is(err, gorm.ErrRecordNotFound)
			if err != nil && !create {
				return ErrStorage
			}
			if create {
				row = models.WorkerEnvVar{SiteID: siteID, Name: w.Name}
			}
			if w.Secret != nil {
				row.Secret = *w.Secret
			}
			_, declared := active[w.Name]
			if candidate != nil || declared {
				if !row.Secret {
					return &envschema.Diagnostic{Name: w.Name, Constraint: "must be a secret"}
				}
			}
			if declared {
				if err := active.ValidateValue(w.Name, *w.Value, true); err != nil {
					return err
				}
			}
			row.Value, row.SecretVersion, row.SecretKeyID, row.SecretNonce, row.Ciphertext = *w.Value, 0, "", nil, nil
			if row.Secret {
				row.SecretKeyID, row.SecretNonce, row.Ciphertext, err = s.Keys.encrypt(siteID, w.Name, *w.Value)
				if err != nil {
					return ErrStorage
				}
				row.Value, row.SecretVersion = "", FormatVersion
			}
			rows[i] = row
		}
		for i := range rows {
			if rows[i].ID == "" {
				err = tx.Create(&rows[i]).Error
			} else {
				err = tx.Save(&rows[i]).Error
			}
			if err != nil {
				return ErrStorage
			}
		}
		if active != nil {
			return s.ValidateStored(tx, siteID, active)
		}
		return nil
	})
	if err != nil {
		return nil, safeError(err)
	}
	return rows, nil
}
