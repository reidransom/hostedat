package environment

import (
	"errors"

	"github.com/cryguy/hostedat/internal/envschema"
	"github.com/cryguy/hostedat/internal/models"
	"gorm.io/gorm"
)

const MaxValueBytes = 1 << 20

type Service struct {
	DB   *gorm.DB
	Keys *Keys
}
type Write struct {
	Name   string  `json:"name"`
	Value  *string `json:"value"`
	Secret *bool   `json:"secret"`
}

// Set requires an explicit value. Omitted classification preserves existing
// classification, and an existing secret can never be downgraded implicitly.
func (s *Service) Set(siteID string, w Write) (models.WorkerEnvVar, error) {
	rows, err := s.Bulk(siteID, []Write{w}, nil)
	if err != nil {
		return models.WorkerEnvVar{}, err
	}
	return rows[0], nil
}

// List projects metadata and public values only, without reading ciphertext.
func (s *Service) List(siteID string) ([]models.WorkerEnvVar, error) {
	var rows []models.WorkerEnvVar
	err := s.DB.Select("id", "site_id", "name", "secret", "CASE WHEN secret = false THEN value ELSE '' END AS value").Where("site_id = ?", siteID).Order("name").Find(&rows).Error
	if err != nil {
		return nil, ErrStorage
	}
	return rows, nil
}
func (s *Service) Delete(siteID, id string) (models.WorkerEnvVar, error) {
	var row models.WorkerEnvVar
	err := models.WithSiteTransaction(s.DB, siteID, func(tx *gorm.DB, site *models.Site) error {
		if err := tx.Select("id", "site_id", "name", "secret").First(&row, "id = ? AND site_id = ?", id, siteID).Error; err != nil {
			return err
		}
		schema, err := ActiveSchema(tx, site)
		if err != nil {
			return err
		}
		if rule, declared := schema[row.Name]; declared && rule.Required {
			return &envschema.Diagnostic{Name: row.Name, Constraint: "required secret cannot be deleted"}
		}
		return tx.Delete(&row).Error
	})
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		err = safeError(err)
	}
	return row, err
}

func (s *Service) value(row models.WorkerEnvVar) (string, error) {
	if !row.Secret {
		if row.SecretVersion != 0 || row.SecretKeyID != "" || len(row.SecretNonce) != 0 || len(row.Ciphertext) != 0 {
			return "", ErrStorage
		}
		return row.Value, nil
	}
	if row.Value != "" {
		return "", ErrStorage
	}
	return s.Keys.decrypt(row.SiteID, row.Name, row.SecretVersion, row.SecretKeyID, row.SecretNonce, row.Ciphertext)
}

// Load creates a fresh invocation snapshot. No cache or plaintext-secret fallback.
func (s *Service) Load(siteID string) (map[string]string, map[string]string, error) {
	var rows []models.WorkerEnvVar
	if err := s.DB.Where("site_id = ?", siteID).Find(&rows).Error; err != nil {
		return nil, nil, ErrStorage
	}
	vars, secrets := map[string]string{}, map[string]string{}
	seen := map[string]bool{}
	for _, row := range rows {
		if seen[row.Name] {
			return nil, nil, ErrStorage
		}
		seen[row.Name] = true
		value, err := s.value(row)
		if err != nil {
			return nil, nil, err
		}
		if row.Secret {
			secrets[row.Name] = value
		} else {
			vars[row.Name] = value
		}
	}
	return vars, secrets, nil
}

// ValidateStartup must succeed before traffic, workers, or schedulers start.
func (s *Service) ValidateStartup() error {
	if s.Keys == nil {
		return ErrStorage
	}
	if err := models.CheckWorkerEnvDuplicates(s.DB); err != nil {
		return err
	}
	if !s.DB.Migrator().HasIndex(&models.WorkerEnvVar{}, "idx_worker_env_site_name") {
		return errors.New("environment maintenance required: run offline migrate")
	}
	var rows []models.WorkerEnvVar
	if err := s.DB.Find(&rows).Error; err != nil {
		return ErrStorage
	}
	for _, row := range rows {
		if _, err := s.value(row); err != nil {
			return errors.New("environment storage validation failed; check keys and offline migration")
		}
	}
	return nil
}
