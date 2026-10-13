package handler

import (
	"context"
	"net/http"
	"time"

	"entity-management/internal/db"
	"github.com/gin-gonic/gin"
)

type HealthHandler struct {
	db *db.Pool
}

func NewHealthHandler(db *db.Pool) *HealthHandler {
	return &HealthHandler{db: db}
}

func (h *HealthHandler) Health(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	dbStatus := "healthy"
	if err := h.db.HealthCheck(ctx); err != nil {
		dbStatus = "unhealthy"
	}

	status := http.StatusOK
	if dbStatus == "unhealthy" {
		status = http.StatusServiceUnavailable
	}

	c.JSON(status, gin.H{
		"status": "ok",
		"checks": gin.H{
			"database": dbStatus,
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}
