package router

import (
	"time"

	"Go.exchange/auth"
	"Go.exchange/controllers"
	"Go.exchange/eventing"
	"Go.exchange/metrics"
	"Go.exchange/middlewares"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func SetupRouter(authController *controllers.AuthController, verifier auth.AccessTokenVerifier, publisher eventing.BatchPublisher) *gin.Engine {
	router := gin.Default()

	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))
	router.Use(metrics.Middleware())

	router.GET("/healthz", controllers.Healthz)
	router.GET("/readyz", controllers.Readyz)
	router.GET("/metrics", gin.WrapH(metrics.Handler()))

	authRoutes := router.Group("/api/auth")
	{
		authRoutes.POST("/login", authController.Login)
		authRoutes.POST("/register", authController.Register)
		authRoutes.POST("/refresh", authController.Refresh)
	}

	api := router.Group("/api")
	api.GET("/exchangeRates", controllers.GetExchangeRates)
	api.GET("/exchange/currencies", controllers.GetExchangeCurrencies)
	api.GET("/exchange/quote", controllers.GetExchangeQuote)
	api.GET("/files/*objectKey", controllers.GetFile)

	api.Use(middlewares.AuthMiddleware(verifier))
	{
		api.GET("/recommendations/articles", controllers.GetArticleRecommendations)
		api.POST("/recommendation-events", controllers.NewRecommendationEventsHandler(publisher))
		api.POST("/article-view-events", controllers.NewArticleViewEventsHandler(publisher))
		api.POST("/uploads/article-cover", controllers.UploadArticleCover)
		api.POST("/uploads/profile-avatar", controllers.UploadProfileAvatar)
		api.GET("/users/search", controllers.SearchUsers)
		api.GET("/users/:id", controllers.GetUserByID)
		api.PATCH("/users/:id", controllers.UpdateUserProfile)
		api.GET("/users/:id/articles", controllers.GetUserArticles)
		api.GET("/users/:id/follow", controllers.GetUserFollowState)
		api.PUT("/users/:id/follow", controllers.FollowUser)
		api.GET("/users/:id/followers", controllers.GetUserFollowers)
		api.GET("/users/:id/following", controllers.GetUserFollowing)
		api.DELETE("/users/:id/follow", controllers.UnfollowUser)
		api.GET("/feed/following", controllers.GetFollowingTimeline)
		api.POST("/articles", controllers.NewCreateArticleHandler(publisher))
		api.GET("/articles/:id", controllers.GetArticleByID)
		api.DELETE("/articles/:id", controllers.DeleteArticle)
		api.GET("/articles/:id/comments", controllers.GetArticleComments)
		api.POST("/articles/:id/comments", controllers.CreateArticleComment)
		api.DELETE("/comments/:id", controllers.DeleteComment)
		api.POST("/articles/like-states", controllers.GetArticleLikeStates)
		api.GET("/articles/:id/like", controllers.GetArticleLikes)
		api.PUT("/articles/:id/like", controllers.LikeArticle)
		api.DELETE("/articles/:id/like", controllers.UnlikeArticle)
	}

	return router
}
