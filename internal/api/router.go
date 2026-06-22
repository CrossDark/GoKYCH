package api

import (
	"github.com/gin-gonic/gin"
)

// Setup registers all API routes on the Gin engine.
func (s *Server) Setup(r *gin.Engine) {
	r.Use(s.sessionMiddleware())
	r.Use(s.loadUserMiddleware())

	apiG := r.Group("/api")
	{
		apiG.GET("/health", healthHandler)

		// Public read endpoints.
		apiG.GET("/site", s.getSite)
		apiG.GET("/home", s.getHome)
		apiG.GET("/notifications", s.listNotifications)
		apiG.GET("/articles", s.listArticles)
		apiG.GET("/articles/:type/:slug", s.getArticle)
		apiG.GET("/labels", s.listLabels)
		apiG.GET("/labels/:tag", s.getLabelArticles)
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
		}

		// CSRF-gated mutations.
		mutG := apiG.Group("", s.csrfMiddleware())
		{
			mutG.POST("/articles/:type/:slug/comments", s.addComment)
			mutG.POST("/articles/:type/:slug/line-comments", s.addLineComment)
			mutG.POST("/articles/:type/:slug/rating", s.setRating)
			mutG.DELETE("/articles/:type/:slug/rating", s.deleteRating)

			// Article CRUD (admin+)
			artAdmin := mutG.Group("/articles", requireAdmin)
			{
				artAdmin.POST("", s.createArticle)
				artAdmin.PUT("/:type/:slug", s.updateArticle)
				artAdmin.DELETE("/:type/:slug", s.deleteArticle)
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

				adminG.GET("/settings", requireAdmin, s.getSettings)
				adminG.PUT("/settings", requireOwner, s.updateSettings)

				adminG.GET("/home", requireAdmin, s.getAdminHome)
				adminG.POST("/home/links", requireAdmin, s.addSubsiteLink)
				adminG.DELETE("/home/links/:id", requireAdmin, s.deleteSubsiteLink)
				adminG.POST("/home/featured", requireAdmin, s.addFeatured)
				adminG.DELETE("/home/featured/:id", requireAdmin, s.deleteFeatured)

				adminG.GET("/files", requireAdmin, s.listFiles)

				adminG.GET("/profile", s.getProfile)
				adminG.PUT("/profile", s.updateProfile)
			}
		}
	}
}

func healthHandler(c *gin.Context) {
	c.JSON(200, gin.H{"status": "ok"})
}
