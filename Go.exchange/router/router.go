package router

import (
	"time"

	"Go.exchange/auth"
	"Go.exchange/config"
	"Go.exchange/controllers"
	"Go.exchange/eventing"
	"Go.exchange/metrics"
	"Go.exchange/middlewares"
	"Go.exchange/ratelimit"
	"Go.exchange/runtimehealth"
	"Go.exchange/translation"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func SetupRouter(authController *controllers.AuthController, verifier auth.AccessTokenVerifier, publisher eventing.BatchPublisher, readiness runtimehealth.APIReadinessProvider, translationServices ...translation.Service) (*gin.Engine, error) {
	return setupRouter(authController, verifier, publisher, readiness, nil, translationServices...)
}

func SetupRouterWithRateLimiter(authController *controllers.AuthController, verifier auth.AccessTokenVerifier, publisher eventing.BatchPublisher, readiness runtimehealth.APIReadinessProvider, applicationLimiter ratelimit.Limiter, translationServices ...translation.Service) (*gin.Engine, error) {
	return setupRouter(authController, verifier, publisher, readiness, applicationLimiter, translationServices...)
}

func setupRouter(authController *controllers.AuthController, verifier auth.AccessTokenVerifier, publisher eventing.BatchPublisher, readiness runtimehealth.APIReadinessProvider, applicationLimiter ratelimit.Limiter, translationServices ...translation.Service) (*gin.Engine, error) {
	trustedProxies, err := config.TrustedProxyCIDRs()
	if err != nil {
		return nil, err
	}
	allowedOrigins, err := config.CORSAllowedOrigins()
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
		AllowOrigins:     allowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "Idempotency-Key"},
		ExposeHeaders:    []string{"Content-Length", "Retry-After", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset", "Idempotency-Replayed"},
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
	api.GET("/public/recommendations/posts", controllers.GetPublicPostRecommendations)
	api.GET("/posts/:id", controllers.GetPostByID)
	api.GET("/posts/:id/replies", controllers.GetPostReplies)
	api.GET("/users/:id", controllers.GetUserByID)
	api.GET("/users/:id/timeline", controllers.GetUserTimeline)

	api.Use(middlewares.AuthMiddleware(verifier))
	var translationService translation.Service
	if len(translationServices) > 0 {
		translationService = translationServices[0]
	}
	{
		api.GET("/recommendations/posts", withRateLimit(applicationLimiter, ratelimit.ActionRecommendations, ratelimit.FailOpen, controllers.GetPostRecommendations)...)
		api.POST("/recommendation-events", controllers.NewRecommendationEventsHandler(publisher))
		api.POST("/post-view-events", controllers.NewPostViewEventsHandler(publisher))
		api.POST("/uploads/post-media", withRateLimit(applicationLimiter, ratelimit.ActionMediaUpload, ratelimit.FailClosed, controllers.UploadPostMedia)...)
		api.POST("/uploads/profile-avatar", controllers.UploadProfileAvatar)
		api.POST("/uploads/profile-cover", controllers.UploadProfileCover)
		api.GET("/users/search", controllers.SearchUsers)
		api.PATCH("/users/:id", controllers.UpdateUserProfile)
		api.GET("/users/:id/follow", controllers.GetUserFollowState)
		api.PUT("/users/:id/follow", withRateLimit(applicationLimiter, ratelimit.ActionFollowMutation, ratelimit.FailClosed, controllers.FollowUser)...)
		api.GET("/users/:id/followers", controllers.GetUserFollowers)
		api.GET("/users/:id/following", controllers.GetUserFollowing)
		api.DELETE("/users/:id/follow", withRateLimit(applicationLimiter, ratelimit.ActionFollowMutation, ratelimit.FailClosed, controllers.UnfollowUser)...)
		api.GET("/feed/following", controllers.GetFollowingTimeline)
		api.GET("/me/history/likes", controllers.GetMyLikedHistory)
		api.GET("/me/bookmarks", controllers.GetMyBookmarks)
		api.GET("/me/notifications", controllers.GetMyNotifications)
		api.GET("/me/notifications/unread-count", controllers.GetMyUnreadNotificationCount)
		api.PUT("/me/notifications/:id/read", controllers.MarkMyNotificationRead)
		api.PUT("/me/notifications/read-all", controllers.MarkMyNotificationsReadAll)
		api.POST("/posts", controllers.NewCreatePostHandler(applicationLimiter))
		api.POST("/posts/repost-states", controllers.GetPostRepostStates)
		api.POST("/posts/bookmark-states", controllers.GetPostBookmarkStates)
		api.POST("/posts/:id/translation", withRateLimit(applicationLimiter, ratelimit.ActionTranslation, ratelimit.FailOpen, controllers.NewPostTranslationHandler(translationService))...)
		api.DELETE("/posts/:id", controllers.DeletePost)
		api.POST("/posts/like-states", controllers.GetPostLikeStates)
		api.GET("/posts/:id/like", controllers.GetPostLikes)
		api.PUT("/posts/:id/like", controllers.LikePost)
		api.DELETE("/posts/:id/like", controllers.UnlikePost)
		api.PUT("/posts/:id/bookmark", controllers.BookmarkPost)
		api.DELETE("/posts/:id/bookmark", controllers.UnbookmarkPost)
		api.GET("/posts/:id/repost", controllers.GetPostRepostState)
		api.PUT("/posts/:id/repost", controllers.RepostPost)
		api.DELETE("/posts/:id/repost", controllers.UndoRepostPost)
	}

	return router, nil
}

func withRateLimit(limiter ratelimit.Limiter, action ratelimit.Action, failureMode ratelimit.FailureMode, handler gin.HandlerFunc) []gin.HandlerFunc {
	if limiter == nil {
		return []gin.HandlerFunc{handler}
	}
	return []gin.HandlerFunc{ratelimit.Middleware(limiter, action, failureMode), handler}
}
