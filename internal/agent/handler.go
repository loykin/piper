package agent

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	registry *Registry
}

func NewHandler(registry *Registry) *Handler {
	return &Handler{registry: registry}
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/agents", h.registerAgent)
	rg.GET("/agents", h.listAgents)
	rg.POST("/agents/:id/heartbeat", h.heartbeatAgent)
}

func (h *Handler) registerAgent(c *gin.Context) {
	var info Info
	if err := c.ShouldBindJSON(&info); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if info.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}
	h.registry.Register(info)
	c.JSON(http.StatusOK, gin.H{"id": info.ID})
}

func (h *Handler) listAgents(c *gin.Context) {
	c.JSON(http.StatusOK, h.registry.List())
}

func (h *Handler) heartbeatAgent(c *gin.Context) {
	if err := h.registry.Heartbeat(c.Param("id")); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
