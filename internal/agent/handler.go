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
	rg.GET("/agents", h.listAgents)
	rg.GET("/notebook-workers", h.listNotebookWorkers)
}

func (h *Handler) listAgents(c *gin.Context) {
	c.JSON(http.StatusOK, h.registry.List())
}

func (h *Handler) listNotebookWorkers(c *gin.Context) {
	agents := h.registry.List()
	out := make([]Info, 0, len(agents))
	for _, a := range agents {
		if !hasCapability(&a, CapabilityNotebook) {
			continue
		}
		out = append(out, a)
	}
	c.JSON(http.StatusOK, out)
}
