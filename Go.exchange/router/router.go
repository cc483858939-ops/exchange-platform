package router

import (
	"Go.exchange/controllers"
	"Go.exchange/metrics"
	"Go.exchange/middlewares"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func SetupRouter() *gin.Engine {
	r := gin.Default()

	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))
	r.Use(metrics.Middleware())

	r.GET("/healthz", controllers.Healthz)
	r.GET("/readyz", controllers.Readyz)
	r.GET("/metrics", gin.WrapH(metrics.Handler()))

	auth := r.Group("/api/auth")
	{
		auth.POST("/login", controllers.Login)
		auth.POST("/register", controllers.Register)
		auth.POST("/refresh", controllers.RefreshToken)
	}

	api := r.Group("/api")
	api.GET("/exchangeRates", controllers.GetExchangeRates)
	api.GET("/exchange/currencies", controllers.GetExchangeCurrencies)
	api.GET("/exchange/quote", controllers.GetExchangeQuote)
	api.GET("/files/*objectKey", controllers.GetFile)

	api.Use(middlewares.AuthMiddleWare())
	{
		api.GET("/recommendations/articles", controllers.GetArticleRecommendations)
		api.POST("/recommendation-events", controllers.RecordRecommendationEvents)
		api.POST("/uploads/article-cover", controllers.UploadArticleCover)
		api.GET("/users/:id", controllers.GetUserByID)
		api.GET("/users/:id/articles", controllers.GetUserArticles)
		api.POST("/articles", controllers.CreateArticle)
		api.GET("/articles", controllers.GetArticle)
		api.GET("/articles/:id", controllers.GetArticleByID)
		api.GET("/articles/:id/like", controllers.GetArticleLikes)
		api.PUT("/articles/:id/like", controllers.LikeArticle)
		api.DELETE("/articles/:id/like", controllers.UnlikeArticle)
	}

	return r
}
