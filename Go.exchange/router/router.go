package router

import (
	"errors"
	"net/http"
	"strings"
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

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func SetupRouter(authController *controllers.AuthController, verifier auth.AccessTokenVerifier, publisher eventing.BatchPublisher, readiness runtimehealth.APIReadinessProvider, applicationLimiter ratelimit.Limiter, recommendationHandler *controllers.RecommendationHandler, telemetryRateLimiter controllers.RecommendationTelemetryRateLimiter, translationServices ...translation.Service) (*gin.Engine, error) {
	return SetupRouterWithTracing(authController, verifier, publisher, readiness, applicationLimiter, recommendationHandler, telemetryRateLimiter, true, translationServices...)
}

// SetupRouterWithTracing preserves SetupRouter while allowing the API process
// to omit HTTP instrumentation entirely when tracing is disabled.
func SetupRouterWithTracing(authController *controllers.AuthController, verifier auth.AccessTokenVerifier, publisher eventing.BatchPublisher, readiness runtimehealth.APIReadinessProvider, applicationLimiter ratelimit.Limiter, recommendationHandler *controllers.RecommendationHandler, telemetryRateLimiter controllers.RecommendationTelemetryRateLimiter, tracingEnabled bool, translationServices ...translation.Service) (*gin.Engine, error) {
	return setupRouter(authController, verifier, publisher, readiness, applicationLimiter, recommendationHandler, telemetryRateLimiter, translationServices, traceProvider(), tracingEnabled)
}

func setupRouter(authController *controllers.AuthController, verifier auth.AccessTokenVerifier, publisher eventing.BatchPublisher, readiness runtimehealth.APIReadinessProvider, applicationLimiter ratelimit.Limiter, recommendationHandler *controllers.RecommendationHandler, telemetryRateLimiter controllers.RecommendationTelemetryRateLimiter, translationServices []translation.Service, tracerProvider trace.TracerProvider, tracingEnabled bool) (*gin.Engine, error) {
	if recommendationHandler == nil {
		return nil, errors.New("recommendation handler is required")
	}
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

	if tracingEnabled {
		router.Use(httpTracingMiddleware(tracerProvider))
	}
	router.Use(cors.New(cors.Config{
		AllowOrigins:     allowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "Idempotency-Key", "X-Guest-Recommendation-Session"},
		ExposeHeaders:    []string{"Content-Length", "Retry-After", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset", "Idempotency-Replayed"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))
	router.Use(metrics.Middleware())
	router.Use(middlewares.RequestTimeout())

	router.GET("/healthz", controllers.Healthz)
	router.GET("/readyz", controllers.ReadyzWithProvider(readiness))
	router.GET("/metrics", gin.WrapH(metrics.Handler()))
	publicRecommendationHandler := recommendationHandler.GetPublicPostRecommendations
	userRecommendationHandler := recommendationHandler.GetPostRecommendations
	enableApplicationRateLimit := applicationLimiter != nil

	authRoutes := router.Group("/api/auth")
	{
		authRoutes.POST("/login", authController.Login)
		authRoutes.POST("/register", authController.Register)
		authRoutes.POST("/refresh", authController.Refresh)
		authRoutes.POST("/logout", authController.Logout)
	}

	api := router.Group("/api")
	api.GET("/exchangeRates", controllers.GetExchangeRates)
	api.GET("/exchange/currencies", controllers.GetExchangeCurrencies)
	api.GET("/exchange/quote", controllers.GetExchangeQuote)
	api.GET("/files/*objectKey", controllers.GetFile)
	api.GET("/public/recommendations/posts", publicRecommendationHandler)
	api.GET("/posts/search", append(
		[]gin.HandlerFunc{middlewares.AuthMiddleware(verifier)},
		withRateLimit(enableApplicationRateLimit, applicationLimiter, ratelimit.ActionPostSearch, ratelimit.FailOpen, controllers.SearchPosts)...,
	)...)
	api.GET("/posts/:id", controllers.GetPostByID)
	api.GET("/posts/:id/replies", controllers.GetPostReplies)
	api.GET("/posts/:id/quotes", controllers.GetPostQuotes)
	api.GET("/topics", controllers.GetTopics)
	api.GET("/topics/:slug/posts", controllers.GetTopicPosts)
	api.GET("/users/:id", controllers.GetUserByID)
	api.GET("/users/:id/timeline", controllers.GetUserTimeline)

	api.Use(middlewares.AuthMiddleware(verifier))
	var translationService translation.Service
	if len(translationServices) > 0 {
		translationService = translationServices[0]
	}
	{
		api.GET("/recommendations/posts", withRateLimit(enableApplicationRateLimit, applicationLimiter, ratelimit.ActionRecommendations, ratelimit.FailOpen, userRecommendationHandler)...)
		api.POST("/recommendation-events", controllers.NewRecommendationEventsHandler(publisher, telemetryRateLimiter))
		api.POST("/post-view-events", controllers.NewPostViewEventsHandler(publisher))
		api.POST("/uploads/post-media", withRateLimit(enableApplicationRateLimit, applicationLimiter, ratelimit.ActionMediaUpload, ratelimit.FailClosed, controllers.UploadPostMedia)...)
		api.POST("/uploads/profile-avatar", controllers.UploadProfileAvatar)
		api.POST("/uploads/profile-cover", controllers.UploadProfileCover)
		api.GET("/users/search", controllers.SearchUsers)
		api.PATCH("/users/:id", controllers.UpdateUserProfile)
		api.GET("/users/:id/follow", controllers.GetUserFollowState)
		api.PUT("/users/:id/follow", withRateLimit(enableApplicationRateLimit, applicationLimiter, ratelimit.ActionFollowMutation, ratelimit.FailClosed, controllers.FollowUser)...)
		api.GET("/users/:id/followers", controllers.GetUserFollowers)
		api.GET("/users/:id/following", controllers.GetUserFollowing)
		api.DELETE("/users/:id/follow", withRateLimit(enableApplicationRateLimit, applicationLimiter, ratelimit.ActionFollowMutation, ratelimit.FailClosed, controllers.UnfollowUser)...)
		api.GET("/feed/following", controllers.GetFollowingTimeline)
		api.GET("/me/history/likes", controllers.GetMyLikedHistory)
		api.GET("/me/bookmarks", controllers.GetMyBookmarks)
		api.GET("/me/notifications", controllers.GetMyNotifications)
		api.GET("/me/notifications/unread-count", controllers.GetMyUnreadNotificationCount)
		api.PUT("/me/notifications/:id/read", controllers.MarkMyNotificationRead)
		api.PUT("/me/notifications/read-all", controllers.MarkMyNotificationsReadAll)
		api.POST("/posts", controllers.NewCreatePostHandlerWithRateLimit(applicationLimiter, enableApplicationRateLimit))
		api.POST("/posts/engagement-states", controllers.GetPostEngagementStates)
		api.POST("/posts/repost-states", controllers.GetPostRepostStates)
		api.POST("/posts/bookmark-states", controllers.GetPostBookmarkStates)
		api.POST("/posts/:id/translation", withRateLimit(enableApplicationRateLimit, applicationLimiter, ratelimit.ActionTranslation, ratelimit.FailOpen, controllers.NewPostTranslationHandler(translationService))...)
		api.DELETE("/posts/:id", controllers.DeletePost)
		api.POST("/posts/like-states", controllers.GetPostLikeStates)
		api.GET("/posts/:id/like", controllers.GetPostLikes)
		api.PUT("/posts/:id/like", ratelimit.Middleware(applicationLimiter, ratelimit.ActionLikeMutation, ratelimit.FailClosed), controllers.LikePost)
		api.DELETE("/posts/:id/like", ratelimit.Middleware(applicationLimiter, ratelimit.ActionLikeMutation, ratelimit.FailClosed), controllers.UnlikePost)
		api.PUT("/posts/:id/bookmark", controllers.BookmarkPost)
		api.DELETE("/posts/:id/bookmark", controllers.UnbookmarkPost)
		api.GET("/posts/:id/repost", controllers.GetPostRepostState)
		api.PUT("/posts/:id/repost", controllers.RepostPost)
		api.DELETE("/posts/:id/repost", controllers.UndoRepostPost)
	}

	return router, nil
}

func traceProvider() trace.TracerProvider {
	return otel.GetTracerProvider()
}

func httpTracingMiddleware(tracerProvider trace.TracerProvider) gin.HandlerFunc {
	tracer := tracerProvider.Tracer("Go.exchange/router")
	propagator := propagation.TraceContext{}
	return func(ctx *gin.Context) {
		switch ctx.Request.URL.Path {
		case "/metrics", "/healthz", "/readyz":
			ctx.Next()
			return
		}

		parentCtx := propagator.Extract(ctx.Request.Context(), propagation.HeaderCarrier(ctx.Request.Header))
		spanCtx, span := tracer.Start(parentCtx, recommendationHTTPSpanName(ctx),
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.request.method", ctx.Request.Method),
				attribute.String("http.route", ctx.FullPath()),
			),
		)
		ctx.Request = ctx.Request.WithContext(spanCtx)
		defer func() {
			recovered := recover()
			statusCode := ctx.Writer.Status()
			if recovered != nil {
				statusCode = http.StatusInternalServerError
				if ctx.Writer.Written() {
					statusCode = ctx.Writer.Status()
				}
				span.SetStatus(codes.Error, "HTTP request panicked")
			} else if statusCode >= http.StatusInternalServerError || len(ctx.Errors) > 0 {
				span.SetStatus(codes.Error, "HTTP request failed")
			}

			span.SetAttributes(attribute.Int("http.response.status_code", statusCode))
			span.End()

			if recovered != nil {
				panic(recovered)
			}
		}()

		ctx.Next()
	}
}

func recommendationHTTPSpanName(ctx *gin.Context) string {
	method := strings.ToUpper(ctx.Request.Method)
	if route := ctx.FullPath(); route != "" {
		return method + " " + route
	}
	return method + " route not found"
}

func withRateLimit(enabled bool, limiter ratelimit.Limiter, action ratelimit.Action, failureMode ratelimit.FailureMode, handler gin.HandlerFunc) []gin.HandlerFunc {
	if !enabled {
		return []gin.HandlerFunc{handler}
	}
	return []gin.HandlerFunc{ratelimit.Middleware(limiter, action, failureMode), handler}
}
