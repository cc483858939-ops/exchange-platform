package router

import (
	"time"

	"Go.exchange/auth"
	"Go.exchange/config"
	"Go.exchange/controllers"
	"Go.exchange/eventing"
	"Go.exchange/metrics"
	"Go.exchange/middlewares"
	"Go.exchange/runtimehealth"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func SetupRouter(authController *controllers.AuthController, verifier auth.AccessTokenVerifier, publisher eventing.BatchPublisher, readiness runtimehealth.APIReadinessProvider) (*gin.Engine, error) {
	trustedProxies, err := config.TrustedProxyCIDRs()
	if err != nil {
		return nil, err
	}

	router := gin.Default()
	if err := router.SetTrustedProxies(trustedProxies); err != nil {
		return nil, err
	}
	router.ForwardedByClientIP = true
	router.RemoteIPHeaders = []string{"X-Forwarded-For", "X-Real-IP"}
	router.TrustedPlatform = ""

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
	router.GET("/readyz", controllers.ReadyzWithProvider(readiness))
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
		api.GET("/recommendations/posts", controllers.GetPostRecommendations)
		api.POST("/recommendation-events", controllers.NewRecommendationEventsHandler(publisher))
		api.POST("/post-view-events", controllers.NewPostViewEventsHandler(publisher))
		api.POST("/uploads/article-cover", controllers.UploadPostCover)
		api.POST("/uploads/profile-avatar", controllers.UploadProfileAvatar)
		api.GET("/users/search", controllers.SearchUsers)
		api.GET("/users/:id", controllers.GetUserByID)
		api.PATCH("/users/:id", controllers.UpdateUserProfile)
		api.GET("/users/:id/posts", controllers.GetUserPosts)
		api.GET("/users/:id/follow", controllers.GetUserFollowState)
		api.PUT("/users/:id/follow", controllers.FollowUser)
		api.GET("/users/:id/followers", controllers.GetUserFollowers)
		api.GET("/users/:id/following", controllers.GetUserFollowing)
		api.DELETE("/users/:id/follow", controllers.UnfollowUser)
		api.GET("/feed/following", controllers.GetFollowingTimeline)
		api.GET("/me/history/likes", controllers.GetMyLikedHistory)
		api.GET("/me/notifications", controllers.GetMyNotifications)
		api.GET("/me/notifications/unread-count", controllers.GetMyUnreadNotificationCount)
		api.PUT("/me/notifications/:id/read", controllers.MarkMyNotificationRead)
		api.PUT("/me/notifications/read-all", controllers.MarkMyNotificationsReadAll)
		api.POST("/posts", controllers.NewCreatePostHandler(publisher))
		api.POST("/posts/repost-states", controllers.GetPostRepostStates)
		api.GET("/posts/:id", controllers.GetPostByID)
		api.DELETE("/posts/:id", controllers.DeletePost)
		api.GET("/posts/:id/replies", controllers.GetPostReplies)
		api.POST("/posts/like-states", controllers.GetPostLikeStates)
		api.GET("/posts/:id/like", controllers.GetPostLikes)
		api.PUT("/posts/:id/like", controllers.LikePost)
		api.DELETE("/posts/:id/like", controllers.UnlikePost)
		api.GET("/posts/:id/repost", controllers.GetPostRepostState)
		api.PUT("/posts/:id/repost", controllers.RepostPost)
		api.DELETE("/posts/:id/repost", controllers.UndoRepostPost)
	}

	return router, nil
}
