package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/cryguy/hostedat/internal/audit"
	"github.com/cryguy/hostedat/internal/environment"
	"github.com/cryguy/hostedat/internal/envschema"
	"github.com/cryguy/hostedat/internal/models"
	"github.com/labstack/echo/v4"
)

func decodeEnvRequest(c echo.Context, dst any) error {
	data, err := io.ReadAll(io.LimitReader(c.Request().Body, environment.MaxRequestBytes+1))
	if err != nil || len(data) > environment.MaxRequestBytes {
		return environment.ErrInput
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return environment.ErrInput
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return environment.ErrInput
	}
	return nil
}

func candidateSchema(raw json.RawMessage, required bool) (envschema.Schema, error) {
	if len(raw) == 0 {
		if required {
			return nil, environment.ErrInput
		}
		return nil, nil
	}
	return envschema.Parse(bytes.NewReader(raw), envschema.JSON)
}

func envOperationError(c echo.Context, err error) error {
	var diagnostic *envschema.Diagnostic
	if errors.As(err, &diagnostic) {
		return errorJSON(c, http.StatusBadRequest, diagnostic.Error())
	}
	if errors.Is(err, environment.ErrInput) {
		return errorJSON(c, http.StatusBadRequest, "invalid or oversized environment request")
	}
	return errorJSON(c, http.StatusInternalServerError, "environment operation failed")
}

func (h *WorkerHandler) authorizeEnv(c echo.Context) error {
	userID, _, role := GetUserFromContext(c)
	var site models.Site
	if err := h.DB.First(&site, "id = ?", c.Param("id")).Error; err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "site not found")
	}
	if site.UserID != userID && role != "admin" && role != "superadmin" {
		return echo.NewHTTPError(http.StatusForbidden, "access denied")
	}
	return nil
}

// BulkEnvVars is merge-only: omitted bindings and active declaration snapshots
// are never replaced by the caller's candidate declaration.
func (h *WorkerHandler) BulkEnvVars(c echo.Context) error {
	if err := h.authorizeEnv(c); err != nil {
		return err
	}
	var req struct {
		Entries []environment.Write `json:"entries"`
		Schema  json.RawMessage     `json:"schema"`
	}
	if err := decodeEnvRequest(c, &req); err != nil {
		return envOperationError(c, err)
	}
	schema, err := candidateSchema(req.Schema, false)
	if err != nil {
		return envOperationError(c, err)
	}
	rows, err := h.environments().Bulk(c.Param("id"), req.Entries, schema)
	if err != nil {
		return envOperationError(c, err)
	}
	// Even plaintext entries in a bulk response are metadata-only.
	responses := make([]workerEnvVarResponse, len(rows))
	names := make([]string, len(rows))
	for i, row := range rows {
		responses[i] = workerEnvVarResponse{ID: row.ID, SiteID: row.SiteID, Name: row.Name, Secret: row.Secret}
		names[i] = row.Name
	}
	details, _ := json.Marshal(struct {
		Names []string `json:"names"`
		Count int      `json:"count"`
	}{names, len(names)})
	audit.Record(h.DB, c, "worker.env.bulk", "site", c.Param("id"), audit.Ptr(string(details)))
	return c.JSON(http.StatusOK, responses)
}

// ValidateEnvVars accepts declarations only, never client-provided values.
func (h *WorkerHandler) ValidateEnvVars(c echo.Context) error {
	if err := h.authorizeEnv(c); err != nil {
		return err
	}
	var req struct {
		Schema json.RawMessage `json:"schema"`
	}
	if err := decodeEnvRequest(c, &req); err != nil {
		return envOperationError(c, err)
	}
	schema, err := candidateSchema(req.Schema, true)
	if err != nil {
		return envOperationError(c, err)
	}
	if err := h.environments().Validate(c.Param("id"), schema); err != nil {
		return envOperationError(c, err)
	}
	return c.JSON(http.StatusOK, struct {
		Valid bool     `json:"valid"`
		Names []string `json:"names"`
	}{true, schema.Names()})
}
