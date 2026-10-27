package environment

import (
	"errors"
	"github.com/cryguy/hostedat/internal/models"
	"gorm.io/gorm"
)

// Maintain runs only while every writer and worker is stopped. SQLite's first
// write locks out competing writers; this does not stop already-running workers.
// Schema additions, encryption, verification and uniqueness commit together.
func Maintain(db *gorm.DB, oldKeys, newKeys *Keys, rotate bool) (int, error) {
	count := 0
	if oldKeys == nil || newKeys == nil {
		return 0, ErrStorage
	}
	if rotate && oldKeys.keys[newKeys.active] != nil {
		return 0, errors.New("rotation requires a new key ID")
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		// Lock before reading, including before schema introspection/migration.
		if err := tx.Exec("CREATE TABLE IF NOT EXISTS environment_maintenance_lock (id INTEGER PRIMARY KEY)").Error; err != nil {
			return ErrStorage
		}
		if err := tx.Exec("INSERT OR IGNORE INTO environment_maintenance_lock (id) VALUES (1)").Error; err != nil {
			return ErrStorage
		}
		if err := tx.Exec("UPDATE environment_maintenance_lock SET id = id WHERE id = 1").Error; err != nil {
			return ErrStorage
		}
		if err := models.CheckWorkerEnvDuplicates(tx); err != nil {
			return err
		}
		if err := tx.AutoMigrate(&models.WorkerEnvVar{}); err != nil {
			return ErrStorage
		}
		var rows []models.WorkerEnvVar
		if err := tx.Find(&rows).Error; err != nil {
			return ErrStorage
		}
		reader := &Service{Keys: oldKeys}
		writer := &Service{Keys: newKeys}
		for _, row := range rows {
			if !row.Secret {
				if _, err := reader.value(row); err != nil {
					return err
				}
				continue
			}
			var value string
			var err error
			legacy := row.SecretVersion == 0 && row.SecretKeyID == "" && len(row.SecretNonce) == 0 && len(row.Ciphertext) == 0
			if legacy {
				if rotate {
					return errors.New("rotation requires migration first")
				}
				value = row.Value
			} else {
				value, err = reader.value(row)
				if err != nil {
					return err
				}
			}
			if rotate || legacy {
				row.SecretKeyID, row.SecretNonce, row.Ciphertext, err = newKeys.encrypt(row.SiteID, row.Name, value)
				if err != nil {
					return err
				}
				row.SecretVersion = FormatVersion
				row.Value = ""
				if err := tx.Save(&row).Error; err != nil {
					return ErrStorage
				}
			}
			// Read back persisted fields, not the pre-write in-memory row.
			var saved models.WorkerEnvVar
			if err := tx.First(&saved, "id = ?", row.ID).Error; err != nil {
				return ErrStorage
			}
			got, err := writer.value(saved)
			if err != nil || got != value {
				return ErrStorage
			}
			count++
		}
		var persisted int64
		if err := tx.Model(&models.WorkerEnvVar{}).Where("secret = ?", true).Count(&persisted).Error; err != nil || int64(count) != persisted {
			return ErrStorage
		}
		if err := models.InstallWorkerEnvUniqueness(tx); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}
