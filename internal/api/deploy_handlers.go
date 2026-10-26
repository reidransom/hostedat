package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/cryguy/hostedat/internal/audit"
	"github.com/cryguy/hostedat/internal/environment"
	"github.com/cryguy/hostedat/internal/models"
	"github.com/cryguy/hostedat/internal/storage"
	"github.com/cryguy/worker/v2"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

type DeployHandler struct {
	DB              *gorm.DB
	Storage         *storage.Manager
	Environment     *environment.Service
	MaxScriptSizeKB int
	WorkerEngine    interface {
		CompileAndCache(siteID string, deployKey string, source string) ([]byte, error)
		InvalidatePool(siteID string, deployKey string)
	}
}

func (h *DeployHandler) Deploy(c echo.Context) error {
	userID, _, role := GetUserFromContext(c)
	siteID := c.Param("id")

	var site models.Site
	if err := h.DB.First(&site, "id = ?", siteID).Error; err != nil {
		return errorJSON(c, http.StatusNotFound, "site not found")
	}

	if site.UserID != userID && role != "admin" && role != "superadmin" {
		return errorJSON(c, http.StatusForbidden, "access denied")
	}

	// Read uploaded zip
	file, err := c.FormFile("file")
	if err != nil {
		return errorJSON(c, http.StatusBadRequest, "file is required (multipart field 'file')")
	}

	src, err := file.Open()
	if err != nil {
		return errorJSON(c, http.StatusInternalServerError, "failed to open uploaded file")
	}
	defer func() { _ = src.Close() }()

	// Stream to temp file while computing hash
	tmpFile, err := os.CreateTemp("", "hostedat-deploy-*.zip")
	if err != nil {
		return errorJSON(c, http.StatusInternalServerError, "failed to create temp file")
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()
	defer func() { _ = tmpFile.Close() }()

	hasher := sha256.New()
	written, err := io.Copy(tmpFile, io.TeeReader(io.LimitReader(src, storage.MaxZipSize+1), hasher))
	if err != nil {
		return errorJSON(c, http.StatusInternalServerError, "failed to read uploaded file")
	}
	if written > storage.MaxZipSize {
		return errorJSON(c, http.StatusBadRequest, "file too large")
	}

	fileHash := hex.EncodeToString(hasher.Sum(nil))

	// Pre-generate deployment ID to use as the storage directory key.
	// This avoids incrementing numeric paths entirely — each deploy gets
	// a unique, non-sequential directory name.
	deployID := models.GenerateID()

	// Extract zip before the DB transaction using the deployment ID as path key.
	if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
		return errorJSON(c, http.StatusInternalServerError, "failed to process uploaded file")
	}
	declaration, err := h.Storage.ExtractZip(siteID, deployID, tmpFile, written)
	if err != nil {
		log.Printf("deploy: failed to extract zip for site %s: %v", siteID, err)
		return errorJSON(c, http.StatusBadRequest, "invalid zip file")
	}
	// Every failure after extraction removes both staged assets/bytecode and
	// any compiled pool. Activation is the only ownership transfer.
	activated := false
	defer func() {
		if !activated {
			if h.WorkerEngine != nil {
				h.WorkerEngine.InvalidatePool(siteID, deployID)
			}
			_ = os.RemoveAll(h.Storage.GetDeploymentPath(siteID, deployID))
		}
	}()
	service := h.environments()
	schema, err := service.Declaration(&models.Deployment{ID: deployID, SiteID: siteID, EnvDeclaration: declaration})
	if err != nil {
		return envOperationError(c, err)
	}
	if err := h.DB.Transaction(func(tx *gorm.DB) error {
		var current models.Site
		if err := tx.First(&current, "id = ?", siteID).Error; err != nil {
			return err
		}
		return service.ValidateActivation(tx, &current, schema)
	}); err != nil {
		return envOperationError(c, err)
	}

	// Check for _worker.js and compile if present
	hasWorker := h.Storage.HasWorkerScript(siteID, deployID)
	if hasWorker && h.WorkerEngine != nil {
		// Bundle ES module imports (if any) via esbuild before compilation.
		deployPath := h.Storage.GetDeploymentPath(siteID, deployID)
		source, err := worker.BundleWorkerScript(deployPath)
		if err != nil {
			log.Printf("deploy: bundle error for site %s: %v", siteID, err)
			return errorJSON(c, http.StatusBadRequest, "worker script bundling failed: check your import paths")
		}

		maxScriptBytes := h.MaxScriptSizeKB * 1024
		if maxScriptBytes <= 0 {
			maxScriptBytes = 1024 * 1024
		}
		if len(source) > maxScriptBytes {
			return errorJSON(c, http.StatusBadRequest, fmt.Sprintf("_worker.js exceeds maximum size (%d KB)", h.MaxScriptSizeKB))
		}

		bytecode, err := h.WorkerEngine.CompileAndCache(siteID, deployID, source)
		if err != nil {
			log.Printf("deploy: compilation error for site %s: %v", siteID, err)
			return errorJSON(c, http.StatusBadRequest, "worker compilation failed: check your script syntax")
		}

		bcDir := h.Storage.GetWorkerBytecodeDir(siteID, deployID)
		if mkErr := os.MkdirAll(bcDir, 0755); mkErr == nil {
			_ = os.WriteFile(filepath.Join(bcDir, "bytecode.bin"), bytecode, 0644)
		}
	}

	// DB transaction: create deployment record and update site atomically.
	var deployment models.Deployment
	var oldDeployID *string

	txErr := models.WithSiteTransaction(h.DB, siteID, func(tx *gorm.DB, freshSite *models.Site) error {
		if err := service.ValidateActivation(tx, freshSite, schema); err != nil {
			return err
		}
		var maxVersion int
		if err := tx.Model(&models.Deployment{}).Where("site_id = ?", siteID).Select("COALESCE(MAX(version), 0)").Scan(&maxVersion).Error; err != nil {
			return err
		}
		nextVersion := maxVersion + 1

		oldDeployID = freshSite.ActiveDeployID

		deployment = models.Deployment{
			ID:             deployID,
			SiteID:         siteID,
			Version:        nextVersion,
			FileHash:       fileHash,
			HasWorker:      hasWorker,
			EnvDeclaration: declaration,
		}
		if err := tx.Create(&deployment).Error; err != nil {
			return fmt.Errorf("create deployment: %w", err)
		}

		if err := tx.Model(freshSite).Updates(map[string]interface{}{
			"active_version":            nextVersion,
			"active_deploy_id":          deployID,
			"has_worker":                hasWorker,
			"env_declarations_enforced": freshSite.EnvDeclarationsEnforced || declaration != nil,
		}).Error; err != nil {
			return fmt.Errorf("update site: %w", err)
		}

		return nil
	})
	if txErr != nil {
		return envOperationError(c, txErr)
	}
	activated = true

	// Invalidate old worker pool AFTER DB transaction commits.
	if h.WorkerEngine != nil && oldDeployID != nil {
		h.WorkerEngine.InvalidatePool(siteID, *oldDeployID)
	}

	audit.Record(h.DB, c, "deploy.create", "site", siteID, audit.Ptr(fmt.Sprintf("v%d", deployment.Version)))

	return c.JSON(http.StatusCreated, deployment)
}

func (h *DeployHandler) Rollback(c echo.Context) error {
	userID, _, role := GetUserFromContext(c)
	siteID := c.Param("id")
	version := 0
	if err := echo.PathParamsBinder(c).Int("version", &version).BindError(); err != nil || version <= 0 {
		return errorJSON(c, http.StatusBadRequest, "invalid version")
	}

	var site models.Site
	if err := h.DB.First(&site, "id = ?", siteID).Error; err != nil {
		return errorJSON(c, http.StatusNotFound, "site not found")
	}

	if site.UserID != userID && role != "admin" && role != "superadmin" {
		return errorJSON(c, http.StatusForbidden, "access denied")
	}

	var dep models.Deployment
	var oldDeployID *string
	errAlreadyActive := fmt.Errorf("already the active version")
	err := models.WithSiteTransaction(h.DB, siteID, func(tx *gorm.DB, freshSite *models.Site) error {
		if freshSite.ActiveVersion != nil && *freshSite.ActiveVersion == version {
			return errAlreadyActive
		}
		if err := tx.First(&dep, "site_id = ? AND version = ?", siteID, version).Error; err != nil {
			return err
		}
		service := h.environments()
		schema, err := service.Declaration(&dep)
		if err != nil {
			return err
		}
		if err := service.ValidateActivation(tx, freshSite, schema); err != nil {
			return err
		}
		oldDeployID = freshSite.ActiveDeployID
		return tx.Model(freshSite).Updates(map[string]interface{}{
			"active_version":            version,
			"active_deploy_id":          dep.ID,
			"has_worker":                dep.HasWorker,
			"env_declarations_enforced": freshSite.EnvDeclarationsEnforced || dep.EnvDeclaration != nil,
		}).Error
	})
	if err == errAlreadyActive {
		return errorJSON(c, http.StatusBadRequest, "already the active version")
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errorJSON(c, http.StatusNotFound, "deployment not found")
	}
	if err != nil {
		return envOperationError(c, err)
	}

	// Invalidate old worker pool after successful transaction.
	if h.WorkerEngine != nil && oldDeployID != nil {
		h.WorkerEngine.InvalidatePool(siteID, *oldDeployID)
	}

	audit.Record(h.DB, c, "deploy.rollback", "site", siteID, audit.Ptr(fmt.Sprintf("v%d", version)))

	return c.JSON(http.StatusOK, map[string]interface{}{
		"message":        "rolled back",
		"active_version": version,
	})
}

func (h *DeployHandler) List(c echo.Context) error {
	userID, _, role := GetUserFromContext(c)
	siteID := c.Param("id")

	var site models.Site
	if err := h.DB.First(&site, "id = ?", siteID).Error; err != nil {
		return errorJSON(c, http.StatusNotFound, "site not found")
	}

	if site.UserID != userID && role != "admin" && role != "superadmin" {
		return errorJSON(c, http.StatusForbidden, "access denied")
	}

	page, _ := strconv.Atoi(c.QueryParam("page"))
	if page < 1 {
		page = 1
	}
	perPage := 20
	offset := (page - 1) * perPage

	var deployments []models.Deployment
	var total int64
	h.DB.Model(&models.Deployment{}).Where("site_id = ?", siteID).Count(&total)
	if err := h.DB.Where("site_id = ?", siteID).Order("version DESC").Offset(offset).Limit(perPage).Find(&deployments).Error; err != nil {
		return errorJSON(c, http.StatusInternalServerError, "failed to list deployments")
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"deployments": deployments,
		"total":       total,
		"page":        page,
	})
}

func (h *DeployHandler) environments() *environment.Service {
	if h.Environment != nil {
		return h.Environment
	}
	return &environment.Service{DB: h.DB}
}
