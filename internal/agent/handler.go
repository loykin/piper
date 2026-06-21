package agent

import (
	"net/http"

	"github.com/gin-gonic/gin"
	sigsyaml "sigs.k8s.io/yaml"
)

type Handler struct {
	registry    *Registry
	podPolicies WorkerPodPolicyRepository
}

func NewHandler(registry *Registry, policies ...WorkerPodPolicyRepository) *Handler {
	h := &Handler{registry: registry}
	if len(policies) > 0 {
		h.podPolicies = policies[0]
	}
	return h
}

func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/workers", h.listWorkers)
	rg.GET("/notebook-workers/pod-policies", h.listPodPolicies)
	rg.GET("/notebook-workers/:id/pod-policy", h.getPodPolicy)
	rg.PUT("/notebook-workers/:id/pod-policy", h.setPodPolicy)
	rg.DELETE("/notebook-workers/:id/pod-policy", h.deletePodPolicy)
}

func (h *Handler) listWorkers(c *gin.Context) {
	c.JSON(http.StatusOK, h.registry.List())
}

// GET /notebook-workers/pod-policies — all saved policies (includes offline workers)
func (h *Handler) listPodPolicies(c *gin.Context) {
	if h.podPolicies == nil {
		c.JSON(http.StatusOK, []WorkerPodPolicy{})
		return
	}
	policies, err := h.podPolicies.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if policies == nil {
		policies = []WorkerPodPolicy{}
	}
	c.JSON(http.StatusOK, policies)
}

// GET /notebook-workers/:id/pod-policy
func (h *Handler) getPodPolicy(c *gin.Context) {
	if h.podPolicies == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no policy set"})
		return
	}
	workerID := c.Param("id")
	policy, err := h.podPolicies.Get(c.Request.Context(), workerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if policy == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no policy set"})
		return
	}
	c.JSON(http.StatusOK, policy)
}

// PUT /notebook-workers/:id/pod-policy
// Body: { "yaml": "<pod template spec as YAML string>" }
func (h *Handler) setPodPolicy(c *gin.Context) {
	if h.podPolicies == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "pod policy store not configured"})
		return
	}
	workerID := c.Param("id")
	var req struct {
		YAML      string `json:"yaml"`
		UpdatedBy string `json:"updated_by"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.YAML == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "yaml field is required"})
		return
	}

	policy := WorkerPodPolicy{WorkerID: workerID, UpdatedBy: req.UpdatedBy}
	if err := sigsyaml.Unmarshal([]byte(req.YAML), &policy.PodTemplate); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid pod template YAML: " + err.Error()})
		return
	}

	if err := h.podPolicies.Set(c.Request.Context(), policy); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	saved, err := h.podPolicies.Get(c.Request.Context(), workerID)
	if err != nil || saved == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve saved policy"})
		return
	}
	c.JSON(http.StatusOK, saved)
}

// DELETE /notebook-workers/:id/pod-policy
func (h *Handler) deletePodPolicy(c *gin.Context) {
	if h.podPolicies == nil {
		c.Status(http.StatusNoContent)
		return
	}
	workerID := c.Param("id")
	if err := h.podPolicies.Delete(c.Request.Context(), workerID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
