package api

import (
	"log/slog"
	"net/http"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

const maxRequestBodyBytes = 10 << 20 // 10 MiB — generous for article content + file uploads

// bodySizeLimitMiddleware caps request bodies to maxRequestBodyBytes to
// prevent memory exhaustion from oversized POST/PUT bodies (e.g. a
// malicious client sending a GiB-sized JSON payload). Uses http.MaxBytesReader
// so the TCP connection is closed on overflow rather than reading the whole
// body into memory.
func bodySizeLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > maxRequestBodyBytes {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
				"error": "请求体过大（最大 10MB）。",
			})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBodyBytes)
		c.Next()
	}
}

// Setup registers all API routes on the Gin engine.
func (s *Server) Setup(r *gin.Engine) {
	// Cap in-memory multipart form parsing to 10 MiB. File uploads use
	// FormFile which streams to disk above this threshold, but we want
	// a hard cap to avoid OOM from a malicious multipart payload.
	r.MaxMultipartMemory = maxRequestBodyBytes

	// Configure trusted proxies for c.ClientIP().
	//  - TRUSTED_PROXIES unset/empty → trust NONE: gin uses RemoteAddr only,
	//    so spoofed X-Forwarded-For can't bypass rate limiting / IP checks.
	//  - TRUSTED_PROXIES=10.0.0.0/8,127.0.0.1 → trust those proxies, real
	//    client IP is read from X-Forwarded-For when the request came through one.
	// Note: passing nil to SetTrustedProxies means "trust ALL" in gin, so we
	// explicitly pass an empty (non-nil) slice for the trust-none case.
	tp := s.trustedProxies
	if len(tp) == 0 {
		tp = []string{}
	}
	if err := r.SetTrustedProxies(tp); err != nil {
		slog.Warn("SetTrustedProxies failed", "err", err)
	}

	r.Use(securityHeaders())
	r.Use(bodySizeLimitMiddleware())
	r.Use(gzip.Gzip(gzip.DefaultCompression))
	r.Use(s.requestIDMiddleware())
	// CORS runs BEFORE session/CSRF so an OPTIONS preflight short-circuits
	// with 204 — CSRF would otherwise 403 the preflight (no session token
	// presented) and the browser would never get to send the real mutation
	// request. Same-origin requests are unaffected because the middleware
	// only sets headers when an Origin header is present.
	r.Use(corsMiddleware(s.CORSAllowedOrigins))
	r.Use(s.sessionMiddleware())
	r.Use(s.loadUserMiddleware())

	apiG := r.Group("/api")
	{
		apiG.GET("/health", s.healthHandler)

		// Public read endpoints.
		apiG.GET("/site", s.getSite)
		apiG.GET("/home", s.getHome)
		apiG.GET("/themes", s.listThemes)
		apiG.GET("/themes/:name", s.getThemeCSS)
		apiG.GET("/notifications", s.listNotifications)
		apiG.GET("/articles", s.listArticles)
		apiG.GET("/articles/:type/:slug", s.getArticle)
		// Typst compile status polling (public — used by the editor/reader to
		// show "compiling..." progress and auto-refresh when ready).
		apiG.GET("/articles/:type/:slug/compile-status", s.getCompileStatus)
		// Typst-only: download the compiled PDF. 404 if the article isn't
		// a typst article or typst isn't installed.
		apiG.GET("/articles/:type/:slug/pdf", s.getArticlePDF)
		// Article revision history (V3) — public read, same visibility as
		// the article itself. The list endpoint omits the diff body to
		// keep the response small for articles with hundreds of
		// revisions; per-version content is fetched separately.
		apiG.GET("/articles/:type/:slug/revisions", s.listRevisions)
		// diff must be registered before {seq} so gin's tree router
		// matches the literal segment "diff" instead of trying to
		// parse it as an int (and 400-ing). Same goes for any future
		// literal sub-routes under /revisions/.
		apiG.GET("/articles/:type/:slug/revisions/diff", s.getRevisionDiff)
		apiG.GET("/articles/:type/:slug/revisions/:seq", s.getRevision)
		apiG.GET("/labels", s.listLabels)
		apiG.GET("/labels/:tag", s.getLabelArticles)
		// Sidebar cards (left rail ☰ drawer) — public read of active
		// rows only. Mutated via /api/admin/sidebar-cards (below).
		apiG.GET("/sidebar-cards", s.listSidebarCards)
		apiG.GET("/search", s.search)
		apiG.GET("/articles/:type/:slug/comments", s.listComments)
		apiG.GET("/articles/:type/:slug/line-comments", s.listLineComments)
		apiG.GET("/articles/:type/:slug/rating", s.getRating)
		apiG.GET("/articles/:type/:slug/ratings", s.listRatings)

		// Auth (public + CSRF-gated).
		authG := apiG.Group("/auth")
		{
			authG.GET("/me", s.getMe)
			authG.GET("/csrf", s.getCSRF)
			authWithCSRF := authG.Group("", s.csrfMiddleware())
			{
				authWithCSRF.POST("/login", s.postLogin)
				authWithCSRF.POST("/logout", s.postLogout)
			}
			// Passkey endpoints. /begin and /finish are CSRF-gated (no
			// anonymous write to the DB), but anonymous OK for /login/* —
			// that's the whole point.
			authG.GET("/passkey", requireLogin, s.listMyPasskeys)
			authG.DELETE("/passkey/:id", requireLogin, s.deleteMyPasskey)
			authWithCSRF.POST("/passkey/register/begin", requireLogin, s.beginPasskeyRegistration)
			authWithCSRF.POST("/passkey/register/finish", requireLogin, s.finishPasskeyRegistration)
			authWithCSRF.POST("/passkey/login/begin", s.beginPasskeyLogin)
			authWithCSRF.POST("/passkey/login/finish", s.finishPasskeyLogin)
		}

		// CSRF-gated mutations.
		mutG := apiG.Group("", s.csrfMiddleware())
		{
			// Comment / line-comment / rating write paths require an authenticated
			// user. Anonymous read endpoints still exist, but writing is gated.
			mutG.POST("/articles/:type/:slug/comments", requireLogin, s.addComment)
			mutG.POST("/articles/:type/:slug/line-comments", requireLogin, s.addLineComment)
			mutG.POST("/articles/:type/:slug/rating", requireLogin, s.setRating)
			mutG.DELETE("/articles/:type/:slug/rating", requireLogin, s.deleteRating)

			// Article CRUD. Any logged-in user can create, but PUT/DELETE
			// additionally check ownership inside the handler — a non-admin
			// user can only touch articles they authored (see
			// canModifyArticle). The route group only enforces "must be
			// logged in" so the create path stays open to everyone.
			art := mutG.Group("/articles", requireLogin)
			{
				art.POST("", s.createArticle)
				art.PUT("/:type/:slug", s.updateArticle)
				art.DELETE("/:type/:slug", s.deleteArticle)
				art.POST("/:type/:slug/recompile", s.recompileArticle)
			}

			// Admin management.
			adminG := mutG.Group("/admin")
			{
				adminG.GET("/users", requireAdmin, s.listUsers)
				adminG.POST("/users", requireAdmin, s.createUser)
				adminG.PUT("/users/:username/role", requireOwner, s.updateUserRole)
				adminG.DELETE("/users/:username", requireOwner, s.deleteUser)

				adminG.GET("/notifications", requireAdmin, s.listAdminNotifications)
				adminG.POST("/notifications", requireAdmin, s.createNotification)
				adminG.PUT("/notifications/:id", requireAdmin, s.updateNotification)
				adminG.DELETE("/notifications/:id", requireAdmin, s.deleteNotification)

			adminG.GET("/tags", requireAdmin, s.listAdminTags)
			adminG.POST("/tags", requireAdmin, s.createTag)
			adminG.PUT("/tags/:id", requireAdmin, s.renameTag)
			adminG.DELETE("/tags/:id", requireAdmin, s.deleteTag)

			// Sidebar cards admin CRUD. requireAdmin — the public
			// /api/sidebar-cards above is anonymous.
			adminG.GET("/sidebar-cards", requireAdmin, s.listAdminSidebarCards)
			adminG.POST("/sidebar-cards", requireAdmin, s.createSidebarCard)
			adminG.PUT("/sidebar-cards/:id", requireAdmin, s.updateSidebarCard)
			adminG.DELETE("/sidebar-cards/:id", requireAdmin, s.deleteSidebarCard)

				adminG.GET("/settings", requireAdmin, s.getSettings)
				adminG.PUT("/settings", requireOwner, s.updateSettings)

				adminG.GET("/home", requireAdmin, s.getAdminHome)
				adminG.POST("/home/links", requireAdmin, s.addSubsiteLink)
				adminG.DELETE("/home/links/:id", requireAdmin, s.deleteSubsiteLink)
				adminG.POST("/home/featured", requireAdmin, s.addFeatured)
				adminG.DELETE("/home/featured/:id", requireAdmin, s.deleteFeatured)

				adminG.GET("/files", requireAdmin, s.listFiles)
				adminG.POST("/files", requireAdmin, s.uploadFile)
				adminG.DELETE("/files/:id", requireAdmin, s.deleteFile)

				// API keys — owner-only management. The requirement is that
				// only the site owner may add/revoke API keys, so we gate
				// the CRUD with requireOwner (not merely requireAdmin). The
				// X-API-Key middleware in loadUserMiddleware still lets a
				// valid key substitute for the session cookie on any other
				// endpoint no matter who created it.
				adminG.GET("/api-keys", requireOwner, s.listAPIKeys)
				adminG.POST("/api-keys", requireOwner, s.createAPIKey)
				adminG.DELETE("/api-keys/:id", requireOwner, s.deleteAPIKey)

				// Metrics (admin+)
				adminG.GET("/metrics", requireAdmin, s.getMetrics)

				adminG.GET("/profile", s.getProfile)
				adminG.PUT("/profile", s.updateProfile)
				// Self-service password change — any authenticated user can change
				// their own password from their profile page. (adminG is inside
				// mutG, so CSRF + a logged-in session already gate this; no
				// role check needed — the handler scopes updates to the caller.)
				adminG.PUT("/profile/password", s.changeMyPassword)

				// Owner-only: list + revoke ANY user's passkey from the profile
				// page's admin section.
				adminG.GET("/passkeys", requireOwner, s.listAllPasskeys)
				adminG.DELETE("/passkeys/:id", requireOwner, s.deleteAnyPasskey)

				// Self-update: check for new GitHub release and apply it.
				// Owner-only because this replaces the running binary.
				adminG.GET("/update/check", requireOwner, s.checkUpdateHandler)
				adminG.POST("/update/apply", requireOwner, s.applyUpdateHandler)
				adminG.GET("/update/status", requireOwner, s.updateStatusHandler)

				// Theme management — owner-only (upload/delete/activate).
				adminG.GET("/themes", requireOwner, s.adminListThemes)
				adminG.POST("/themes/upload", requireOwner, s.adminUploadTheme)
				adminG.DELETE("/themes/:name", requireOwner, s.adminDeleteTheme)
				adminG.PUT("/themes/:name/activate", requireOwner, s.adminActivateTheme)
			}
		}
	}
}

// healthHandler returns a deep health check: it pings the DB so that load
// balancers / orchestrators can route around a backend whose dependency has
// gone away. Returns 503 if the DB is unreachable so callers can react.
//
// It's a method on *Server rather than a free function so we can reach
// s.DB directly without stashing a pinger closure in the gin context.
func (s *Server) healthHandler(c *gin.Context) {
	if s.DB == nil {
		// Pre-startup probe — don't pretend we're healthy just because
		// the pool isn't built yet.
		c.JSON(503, gin.H{"status": "degraded", "db": "not_initialised"})
		return
	}
	if err := s.DB.PingContext(c.Request.Context()); err != nil {
		c.JSON(503, gin.H{"status": "degraded", "db": "unreachable", "error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"status": "ok", "db": "ok"})
}

// GET /api/admin/metrics — returns basic request metrics (total, status
// distribution, average latency). Admin-only to avoid leaking operational data.
func (s *Server) getMetrics(c *gin.Context) {
	c.JSON(200, s.Metrics.Snapshot())
}
