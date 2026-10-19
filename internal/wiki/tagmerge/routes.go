package tagmerge

import (
	"net/http"

	"github.com/gin-gonic/gin"
	coreauth "github.com/perber/wiki/internal/core/auth"
	httpinternal "github.com/perber/wiki/internal/http"
	authmw "github.com/perber/wiki/internal/http/middleware/auth"
	coretagmerge "github.com/perber/wiki/internal/tagmerge"
)

// Routes is the RouteRegistrar for the tag merge admin endpoints.
type Routes struct {
	getRules    *GetRulesUseCase
	updateRules *UpdateRulesUseCase
	apply       *TriggerApplyUseCase
	status      *GetStatusUseCase
	authService *coreauth.AuthService
}

// RoutesConfig holds the dependencies required to build a Routes instance.
type RoutesConfig struct {
	GetRules    *GetRulesUseCase
	UpdateRules *UpdateRulesUseCase
	Apply       *TriggerApplyUseCase
	Status      *GetStatusUseCase
	AuthService *coreauth.AuthService
}

// NewRoutes constructs the tag merge RouteRegistrar.
func NewRoutes(cfg RoutesConfig) *Routes {
	return &Routes{
		getRules:    cfg.GetRules,
		updateRules: cfg.UpdateRules,
		apply:       cfg.Apply,
		status:      cfg.Status,
		authService: cfg.AuthService,
	}
}

// RegisterRoutes implements RouteRegistrar.
func (r *Routes) RegisterRoutes(ctx httpinternal.RouterContext) {
	opts := ctx.Opts
	adminGroup := ctx.APIAuthGroup(r.authService).Group("/admin")
	adminGroup.Use(authmw.RequireAdmin(opts.AuthDisabled))

	adminGroup.GET("/tag-merge/rules", r.handleGetRules)
	adminGroup.PUT("/tag-merge/rules", r.handleUpdateRules)
	adminGroup.POST("/tag-merge/apply", r.handleApply)
	adminGroup.GET("/tag-merge/status", r.handleStatus)
}

type updateRulesRequest struct {
	Rules []coretagmerge.Rule `json:"rules"`
}

func (r *Routes) handleGetRules(c *gin.Context) {
	c.JSON(http.StatusOK, r.getRules.Execute(c.Request.Context()))
}

func (r *Routes) handleUpdateRules(c *gin.Context) {
	var req updateRulesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithTagMergeBadRequest(c, "Invalid request body")
		return
	}
	out, err := r.updateRules.Execute(c.Request.Context(), UpdateRulesInput{Rules: req.Rules})
	if err != nil {
		respondWithTagMergeError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (r *Routes) handleApply(c *gin.Context) {
	if err := r.apply.Execute(c.Request.Context()); err != nil {
		respondWithTagMergeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"ok": true})
}

func (r *Routes) handleStatus(c *gin.Context) {
	c.JSON(http.StatusOK, r.status.Execute(c.Request.Context()))
}
