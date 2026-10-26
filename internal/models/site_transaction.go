package models

import (
	"errors"

	"gorm.io/gorm"
)

// ErrDuplicateWorkerEnv means maintenance must explicitly resolve duplicate
// (site_id, name) rows. No row is selected as an implicit winner.
var ErrDuplicateWorkerEnv = errors.New("duplicate environment names; stop writers and resolve duplicate rows before maintenance")

// WithSiteTransaction serializes binding mutations and deployment activation.
// The first statement is a write, acquiring SQLite's database-backed writer lock
// BEFORE any reads/validation establish a snapshot. All checks and writes must
// use tx, never db; the callback must not perform external side effects. Return
// an error to roll back. Call with a root DB handle, not an existing transaction.
// SQLite serializes writers database-wide, so unrelated sites may also wait.
func WithSiteTransaction(db *gorm.DB, siteID string, fn func(tx *gorm.DB, site *Site) error) error {
	return db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&Site{}).Where("id = ?", siteID).UpdateColumn("id", gorm.Expr("id"))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		var site Site
		if err := tx.First(&site, "id = ?", siteID).Error; err != nil {
			return err
		}
		return fn(tx, &site)
	})
}

// CheckWorkerEnvDuplicates checks metadata only and never reads/logs values.
// Operators must resolve duplicates explicitly while writers/workers are stopped;
// migration must not choose a winner, including when duplicate values match.
func CheckWorkerEnvDuplicates(db *gorm.DB) error {
	if !db.Migrator().HasTable(&WorkerEnvVar{}) {
		return nil
	}
	var row struct{ Count int64 }
	if err := db.Model(&WorkerEnvVar{}).Select("COUNT(*) AS count").
		Group("site_id, name").Having("COUNT(*) > 1").Limit(1).Scan(&row).Error; err != nil {
		return err
	}
	if row.Count > 1 {
		return ErrDuplicateWorkerEnv
	}
	return nil
}

// InstallWorkerEnvUniqueness is for offline maintenance, inside its transaction
// after explicit duplicate resolution and before committing migrated data. It is
// intentionally not an AutoMigrate tag: legacy duplicates must never be silently
// discarded and the encrypted-storage maintenance task owns installation.
func InstallWorkerEnvUniqueness(tx *gorm.DB) error {
	if err := CheckWorkerEnvDuplicates(tx); err != nil {
		return err
	}
	return tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_worker_env_site_name ON worker_env_vars(site_id, name)").Error
}

// FindWorkerEnvVar refuses ambiguous legacy data rather than using First.
func FindWorkerEnvVar(db *gorm.DB, siteID, name string) (WorkerEnvVar, error) {
	var rows []WorkerEnvVar
	if err := db.Where("site_id = ? AND name = ?", siteID, name).Limit(2).Find(&rows).Error; err != nil {
		return WorkerEnvVar{}, err
	}
	switch len(rows) {
	case 0:
		return WorkerEnvVar{}, gorm.ErrRecordNotFound
	case 1:
		return rows[0], nil
	default:
		return WorkerEnvVar{}, ErrDuplicateWorkerEnv
	}
}
