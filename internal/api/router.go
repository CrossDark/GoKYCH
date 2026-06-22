package api

import "github.com/gin-gonic/gin"

// Setup registers all API routes on the Gin engine.
func Setup(r *gin.Engine) {
	api := r.Group("/api")
	{
		api.GET("/health", healthHandler)

		// M1: auth routes
		// M2: article/tag/comment/rating/search/home API routes
		// M6: admin routes
	}
}

// healthHandler returns a simple health check response.
func healthHandler(c *gin.Context) {
	c.JSON(200, gin.H{
		"status": "ok",
	})
}
