package environment

import (
	"github.com/cryguy/hostedat/internal/envschema"
	"github.com/cryguy/hostedat/internal/models"
	"gorm.io/gorm"
)

// bindingExists covers every persisted binding consumed by BuildEnvFromDB.
// Queues, service bindings and custom bindings have no provisioning path here.
func bindingExists(tx *gorm.DB, siteID string, names []string) (bool, error) {
	if len(names) == 0 {
		return false, nil
	}
	for _, model := range []any{&models.KVNamespace{}, &models.D1Database{}, &models.StorageBucket{}, &models.DurableObjectNamespace{}} {
		var count int64
		if err := tx.Model(model).Where("site_id = ? AND name IN ?", siteID, names).Count(&count).Error; err != nil {
			return false, ErrStorage
		}
		if count != 0 {
			return true, nil
		}
	}
	return false, nil
}

// CheckEnvironmentNames checks declarations or writes against runtime bindings.
// Mutations must call it inside WithSiteTransaction; read-only validation uses a
// read snapshot. It intentionally does not reserve type labels such as KV/D1:
// only actual per-site binding names occupy those namespaces.
func CheckEnvironmentNames(tx *gorm.DB, siteID string, names []string) error {
	for _, name := range names {
		if err := envschema.ValidateRuntimeName(name); err != nil {
			return err
		}
	}
	found, err := bindingExists(tx, siteID, names)
	if err != nil {
		return err
	}
	if found {
		return &envschema.Diagnostic{Constraint: "name collides with an existing site binding"}
	}
	return nil
}

// CheckBindingCreation must run in the same WithSiteTransaction callback as
// the insert. Optional declarations reserve names even before a value exists.
// Existing namespace grammars are preserved; only collisions are restricted.
func CheckBindingCreation(tx *gorm.DB, site *models.Site, name string) error {
	if err := CheckEnvironmentNames(tx, site.ID, []string{name}); err != nil {
		return err
	}
	var count int64
	if err := tx.Model(&models.WorkerEnvVar{}).Where("site_id = ? AND name = ?", site.ID, name).Count(&count).Error; err != nil {
		return ErrStorage
	}
	if count != 0 {
		return &envschema.Diagnostic{Constraint: "name collides with an existing environment binding"}
	}
	schema, err := ActiveSchema(tx, site)
	if err != nil {
		return err
	}
	if _, declared := schema[name]; declared {
		return &envschema.Diagnostic{Constraint: "name collides with an active secret declaration"}
	}
	return nil
}
