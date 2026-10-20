package favoritebundles

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	coreauth "github.com/perber/wiki/internal/core/auth"
	httpinternal "github.com/perber/wiki/internal/http"
	authmw "github.com/perber/wiki/internal/http/middleware/auth"
)

// Routes is the RouteRegistrar for team reading lists ("favorite bundles").
type Routes struct {
	createBundle *CreateBundleUseCase
	updateBundle *UpdateBundleUseCase
	deleteBundle *DeleteBundleUseCase
	getBundle    *GetBundleUseCase
	listBundles  *ListBundlesUseCase
	syncBundle   *SyncBundleUseCase
	unsubscribe  *UnsubscribeBundleUseCase
	authService  *coreauth.AuthService
}

type RoutesConfig struct {
	CreateBundle *CreateBundleUseCase
	UpdateBundle *UpdateBundleUseCase
	DeleteBundle *DeleteBundleUseCase
	GetBundle    *GetBundleUseCase
	ListBundles  *ListBundlesUseCase
	SyncBundle   *SyncBundleUseCase
	Unsubscribe  *UnsubscribeBundleUseCase
	AuthService  *coreauth.AuthService
}

func NewRoutes(cfg RoutesConfig) *Routes {
	return &Routes{
		createBundle: cfg.CreateBundle,
		updateBundle: cfg.UpdateBundle,
		deleteBundle: cfg.DeleteBundle,
		getBundle:    cfg.GetBundle,
		listBundles:  cfg.ListBundles,
		syncBundle:   cfg.SyncBundle,
		unsubscribe:  cfg.Unsubscribe,
		authService:  cfg.AuthService,
	}
}

// RegisterRoutes implements httpinternal.RouteRegistrar. Reading lists are
// visible to every authenticated user; only admins may change them, and the
// write routes require a cookie session like the api-key routes so they can
// only be managed through the UI.
func (r *Routes) RegisterRoutes(ctx httpinternal.RouterContext) {
	opts := ctx.Opts
	authGroup := ctx.APIAuthGroup(r.authService)

	authGroup.POST("/favorite-bundles", authmw.RequireCookieSession(), authmw.RequireAdmin(opts.AuthDisabled), r.handleCreateBundle)
	authGroup.GET("/favorite-bundles", r.handleListBundles)
	authGroup.GET("/favorite-bundles/:id", r.handleGetBundle)
	authGroup.PUT("/favorite-bundles/:id", authmw.RequireCookieSession(), authmw.RequireAdmin(opts.AuthDisabled), r.handleUpdateBundle)
	authGroup.DELETE("/favorite-bundles/:id", authmw.RequireCookieSession(), authmw.RequireAdmin(opts.AuthDisabled), r.handleDeleteBundle)
	authGroup.POST("/favorite-bundles/:id/sync", r.handleSyncBundle)
	authGroup.POST("/favorite-bundles/:id/unsubscribe", r.handleUnsubscribe)
}

// ─── Handlers ────────────────────────────────────────────────────────────────

type bundleRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	PageIDs     []string `json:"pageIds"`
}

func (r *Routes) handleCreateBundle(c *gin.Context) {
	admin := authmw.MustGetUser(c)
	if admin == nil {
		return
	}
	var req bundleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithBundleStatusError(c, http.StatusBadRequest, ErrCodeBundleInvalidRequest, "Invalid request", "invalid request")
		return
	}
	bundle, err := r.createBundle.Execute(c.Request.Context(), CreateBundleInput{
		Name:        req.Name,
		Description: req.Description,
		PageIDs:     req.PageIDs,
		CreatedBy:   admin.ID,
	})
	if err != nil {
		respondWithBundleError(c, err)
		return
	}
	c.JSON(http.StatusCreated, bundleJSON(BundleSummary{
		Bundle:  *bundle,
		PageIDs: stringsToJSON(normalizePageIDs(req.PageIDs)),
	}))
}

func (r *Routes) handleUpdateBundle(c *gin.Context) {
	if authmw.MustGetUser(c) == nil {
		return
	}
	var req bundleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithBundleStatusError(c, http.StatusBadRequest, ErrCodeBundleInvalidRequest, "Invalid request", "invalid request")
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if err := r.updateBundle.Execute(c.Request.Context(), UpdateBundleInput{
		BundleID:    id,
		Name:        req.Name,
		Description: req.Description,
		PageIDs:     req.PageIDs,
	}); err != nil {
		respondWithBundleError(c, err)
		return
	}
	out, err := r.getBundle.Execute(c.Request.Context(), GetBundleInput{
		UserID:   authmw.MustGetUser(c).ID,
		BundleID: id,
	})
	if err != nil {
		respondWithBundleError(c, err)
		return
	}
	c.JSON(http.StatusOK, bundleJSON(*out))
}

func (r *Routes) handleDeleteBundle(c *gin.Context) {
	if authmw.MustGetUser(c) == nil {
		return
	}
	if err := r.deleteBundle.Execute(c.Request.Context(), DeleteBundleInput{
		BundleID: strings.TrimSpace(c.Param("id")),
	}); err != nil {
		respondWithBundleError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (r *Routes) handleListBundles(c *gin.Context) {
	user := authmw.MustGetUser(c)
	if user == nil {
		return
	}
	out, err := r.listBundles.Execute(c.Request.Context(), ListBundlesInput{UserID: user.ID})
	if err != nil {
		respondWithBundleError(c, err)
		return
	}
	bundles := make([]gin.H, 0, len(out.Bundles))
	for _, summary := range out.Bundles {
		bundles = append(bundles, bundleJSON(summary))
	}
	c.JSON(http.StatusOK, gin.H{"bundles": bundles})
}

func (r *Routes) handleGetBundle(c *gin.Context) {
	user := authmw.MustGetUser(c)
	if user == nil {
		return
	}
	out, err := r.getBundle.Execute(c.Request.Context(), GetBundleInput{
		UserID:   user.ID,
		BundleID: strings.TrimSpace(c.Param("id")),
	})
	if err != nil {
		respondWithBundleError(c, err)
		return
	}
	c.JSON(http.StatusOK, bundleJSON(*out))
}

func (r *Routes) handleSyncBundle(c *gin.Context) {
	user := authmw.MustGetUser(c)
	if user == nil {
		return
	}
	result, err := r.syncBundle.Execute(c.Request.Context(), SyncBundleInput{
		UserID:   user.ID,
		BundleID: strings.TrimSpace(c.Param("id")),
	})
	if err != nil {
		respondWithBundleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"added":        stringsToJSON(result.Added),
		"removed":      stringsToJSON(result.Removed),
		"lastSyncedAt": formatTime(result.LastSyncedAt),
	})
}

func (r *Routes) handleUnsubscribe(c *gin.Context) {
	user := authmw.MustGetUser(c)
	if user == nil {
		return
	}
	if err := r.unsubscribe.Execute(c.Request.Context(), UnsubscribeBundleInput{
		UserID:   user.ID,
		BundleID: strings.TrimSpace(c.Param("id")),
	}); err != nil {
		respondWithBundleError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ─── DTO ──────────────────────────────────────────────────────────────────────

func bundleJSON(s BundleSummary) gin.H {
	resp := gin.H{
		"id":          s.Bundle.ID,
		"name":        s.Bundle.Name,
		"description": s.Bundle.Description,
		"createdBy":   s.Bundle.CreatedBy,
		"createdAt":   formatTime(s.Bundle.CreatedAt),
		"updatedAt":   formatTime(s.Bundle.UpdatedAt),
		"pageIds":     stringsToJSON(s.PageIDs),
		"subscribed":  s.Subscribed,
	}
	if s.SubscribedAt != nil {
		resp["subscribedAt"] = formatTime(*s.SubscribedAt)
	}
	if s.LastSyncedAt != nil {
		resp["lastSyncedAt"] = formatTime(*s.LastSyncedAt)
	}
	return resp
}

func stringsToJSON(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
