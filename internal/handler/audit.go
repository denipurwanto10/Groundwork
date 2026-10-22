package handler

import (
	"context"
	"log"
	"net/http"
	"strconv"

	"entity-management/internal/audit"

	"github.com/gin-gonic/gin"
)

// AuditHandler serves the trail of successful writes. Only admins read it:
// the trail names the accounts of the workspace.
type AuditHandler struct {
	audit *audit.Recorder
}

func NewAuditHandler(recorder *audit.Recorder) *AuditHandler {
	return &AuditHandler{audit: recorder}
}

// The page size of the trail. The panel asks for one page and pages through
// the rest with the offset.
const (
	auditDefaultLimit = 50
	auditMaxLimit     = 200
)

// List handles GET /api/audit-logs. The trail is read newest first and can
// be narrowed to one action or one kind of object.
func (h *AuditHandler) List(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), dbTimeout)
	defer cancel()

	filter := audit.Filter{Limit: auditDefaultLimit}
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			c.JSON(http.StatusBadRequest, errorBody("invalid_limit", "limit must be a number"))
			return
		}
		filter.Limit = min(n, auditMaxLimit)
	}
	if raw := c.Query("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			c.JSON(http.StatusBadRequest, errorBody("invalid_offset", "offset must be a number"))
			return
		}
		filter.Offset = n
	}
	filter.Action = c.Query("action")
	filter.EntityType = c.Query("entity_type")
	filter.Query = c.Query("q")

	logs, total, err := h.audit.List(ctx, filter)
	if err != nil {
		log.Printf("audit: list failed: %v", err)
		c.JSON(http.StatusInternalServerError, errorBody("internal_error", "internal server error"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"logs":  logs,
		"count": len(logs),
		"total": total,
	}})
}
