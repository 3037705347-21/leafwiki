package wikireleasecheckpoints

import (
	"net/http"

	"github.com/gin-gonic/gin"
	coreauth "github.com/perber/wiki/internal/core/auth"
	httpinternal "github.com/perber/wiki/internal/http"
	authmw "github.com/perber/wiki/internal/http/middleware/auth"
	"github.com/perber/wiki/internal/releasecheckpoints"
)

// Routes is the RouteRegistrar for release checkpoint admin endpoints.
type Routes struct {
	getManifest *GetManifestUseCase
	reconcile   *ReconcileUseCase
	authService *coreauth.AuthService
}

// RoutesConfig holds the dependencies required to build a Routes instance.
type RoutesConfig struct {
	GetManifest *GetManifestUseCase
	Reconcile   *ReconcileUseCase
	AuthService *coreauth.AuthService
}

func NewRoutes(cfg RoutesConfig) *Routes {
	return &Routes{
		getManifest: cfg.GetManifest,
		reconcile:   cfg.Reconcile,
		authService: cfg.AuthService,
	}
}

// RegisterRoutes implements RouteRegistrar.
func (r *Routes) RegisterRoutes(ctx httpinternal.RouterContext) {
	authGroup := ctx.APIAuthGroup(r.authService)

	// Checkpoints are an administration tool: both reading and applying the
	// release checklist sit in the admin group.
	adminGroup := authGroup.Group("/admin")
	adminGroup.Use(authmw.RequireAdmin(ctx.Opts.AuthDisabled))

	adminGroup.GET("/release-checkpoints", r.handleGetManifest)
	adminGroup.PUT("/release-checkpoints/manifest", r.handleReconcile)
}

func (r *Routes) handleGetManifest(c *gin.Context) {
	out, err := r.getManifest.Execute(c.Request.Context())
	if err != nil {
		respondWithError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (r *Routes) handleReconcile(c *gin.Context) {
	var in ReconcileInput
	if err := c.ShouldBindJSON(&in); err != nil {
		respondWithStatusError(c, http.StatusBadRequest, releasecheckpoints.ErrCodeInvalidPayload,
			"The release checklist is invalid", "release checklist payload is invalid JSON")
		return
	}

	result, err := r.reconcile.Execute(c.Request.Context(), in)
	if err != nil {
		respondWithError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
