package core

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"Go.exchange/auth"
	"Go.exchange/config"
	"Go.exchange/controllers"
	"Go.exchange/eventing"
	"Go.exchange/global"
	"Go.exchange/initialize"
	"Go.exchange/ratelimit"
	"Go.exchange/recommendation"
	"Go.exchange/router"
	"Go.exchange/runtimehealth"
	"Go.exchange/services"
	"Go.exchange/translation"
)

const (
	apiShutdownTimeout        = 10 * time.Second
	defaultTraceDrainTimeout  = 5 * time.Second
	forcedRequestDrainTimeout = 3 * time.Second
)

type APIRuntime struct {
	Server              *http.Server
	readiness           *runtimehealth.APIReadiness
	traceDispatcher     recommendation.TraceDispatcher
	traceDrainTimeout   time.Duration
	translationService  *translation.TranslationService
	rateService         *services.RateService
	requests            *apiRequestLifecycle
	httpShutdownTimeout time.Duration
}

func StartHttpServer(tokens auth.TokenService, publisher eventing.BatchPublisher) (*APIRuntime, error) {
	return StartHttpServerWithTracing(tokens, publisher, true)
}

func StartHttpServerWithTracing(tokens auth.TokenService, publisher eventing.BatchPublisher, traceEnabled bool) (_ *APIRuntime, returnErr error) {
	recommendation.SetTracingEnabled(traceEnabled)

	limiter, err := auth.NewRedisAttemptLimiter(global.RedisDB)
	if err != nil {
		return nil, fmt.Errorf("initialize auth rate limiter: %w", err)
	}
	authController, err := controllers.NewAuthController(global.APIDb, tokens, limiter)
	if err != nil {
		return nil, fmt.Errorf("initialize auth controller: %w", err)
	}
	applicationLimiter, err := ratelimit.NewRedisLimiter(global.RedisDB)
	if err != nil {
		return nil, fmt.Errorf("initialize application rate limiter: %w", err)
	}
	port := config.AppPort()
	readiness := runtimehealth.NewAPIReadiness(runtimehealth.APIOptions{
		Role:                  "api",
		RequiredSchemaVersion: initialize.RequiredSchemaVersion,
	})
	translationConfig := config.AppConfig.Translation.Normalized()
	translationService := translation.NewService(
		translation.NewWorkersAIClient(translation.ClientConfig{
			BaseURL:             translationConfig.BaseURL,
			APIKey:              translationConfig.APIKey,
			Model:               translationConfig.Model,
			Timeout:             time.Duration(translationConfig.TimeoutSeconds) * time.Second,
			MaxCompletionTokens: translationConfig.MaxCompletionTokens,
		}),
		translation.NewRedisCache(global.RedisDB),
		translation.ServiceConfig{
			MaxConcurrent:  translationConfig.MaxConcurrent,
			MaxQueued:      translationConfig.MaxQueued,
			Enabled:        translationConfig.Enabled,
			Model:          translationConfig.Model,
			PromptVersion:  translationConfig.PromptVersion,
			BaseTTL:        time.Duration(translationConfig.CacheTTLHours) * time.Hour,
			CacheJitter:    time.Duration(translationConfig.CacheJitterHours) * time.Hour,
			MaxSourceRunes: translationConfig.MaxSourceRunes,
			BaseURL:        translationConfig.BaseURL,
			WorkTimeout:    time.Duration(translationConfig.TimeoutSeconds)*time.Second + 2*translation.DefaultCacheTimeout,
		},
	)
	defer func() {
		if returnErr != nil {
			translationService.Close()
		}
	}()
	apiDB := global.APIDb
	recommendationDependencies, err := recommendation.NewGormServiceDependencies(apiDB, global.RedisDB)
	if err != nil {
		return nil, fmt.Errorf("initialize recommendation dependencies: %w", err)
	}
	recommendationConfig := recommendation.NormalizeConfig(config.AppConfig.Recommendation, config.AppConfig.RecommendationPresence)
	servingTimeout := time.Duration(recommendationConfig.ServingTimeoutMS) * time.Millisecond
	traceRepository, err := recommendation.NewGormTraceRepository(apiDB)
	if err != nil {
		return nil, fmt.Errorf("initialize recommendation trace repository: %w", err)
	}
	traceDispatcher, err := recommendation.NewAsyncTraceDispatcher(traceRepository, recommendation.NewPrometheusMetrics(), recommendationConfig.Trace)
	if err != nil {
		return nil, fmt.Errorf("initialize recommendation trace dispatcher: %w", err)
	}
	defer func() {
		if returnErr == nil {
			return
		}
		cleanupTimeout := time.Duration(recommendationConfig.Trace.ShutdownDrainTimeoutMS) * time.Millisecond
		if cleanupTimeout <= 0 {
			cleanupTimeout = defaultTraceDrainTimeout
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cleanupCancel()
		if err := traceDispatcher.Shutdown(cleanupCtx); err != nil {
			log.Printf("stop recommendation trace dispatcher after startup failure: %v", err)
		}
	}()
	serviceConfig := recommendation.ServiceConfig{
		Recommendation: recommendationConfig,
		Tracking: recommendation.TrackingConfig{
			Enabled:        config.RecommendationTelemetryEnabled(),
			RolloutPercent: config.RecommendationTelemetryRolloutPercent(),
			SigningKey:     []byte(config.RecommendationTelemetrySigningKey()),
			TokenTTL:       config.RecommendationTelemetryTokenTTL(),
		},
	}
	recommendationDependencies.Metrics = recommendation.NewPrometheusMetrics()
	recommendationDependencies.TraceEnqueuer = traceDispatcher
	recommendationService, err := recommendation.NewService(recommendationDependencies, serviceConfig)
	if err != nil {
		return nil, fmt.Errorf("initialize recommendation service: %w", err)
	}
	responseMapper, err := controllers.NewGormRecommendationResponseMapper(apiDB)
	if err != nil {
		return nil, fmt.Errorf("initialize recommendation response mapper: %w", err)
	}
	recommendationHandler, err := controllers.NewRecommendationHandler(recommendationService, responseMapper, servingTimeout)
	if err != nil {
		return nil, fmt.Errorf("initialize recommendation handler: %w", err)
	}
	telemetryRateLimiter := controllers.NewRecommendationTelemetryRedisRateLimiter(global.RedisDB)
	handler, err := router.SetupRouterWithTracing(authController, tokens, publisher, readiness, applicationLimiter, recommendationHandler, telemetryRateLimiter, traceEnabled, translationService)
	if err != nil {
		return nil, fmt.Errorf("initialize HTTP router: %w", err)
	}
	readiness.Start(context.Background())
	runtime := newAPIHTTPRuntime(port, handler)
	server := runtime.Server
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Listen: %s\n", err)
		}
	}()
	runtime.readiness = readiness
	runtime.traceDispatcher = traceDispatcher
	runtime.traceDrainTimeout = time.Duration(recommendationConfig.Trace.ShutdownDrainTimeoutMS) * time.Millisecond
	runtime.translationService = translationService
	runtime.rateService = services.DefaultExchangeRateService()
	return runtime, nil
}

// Once closing is set, no new handler may join the drain. Active requests are
// cancelled together only after their graceful shutdown budget has expired.
type apiRequestLifecycle struct {
	handler http.Handler
	cancel  context.CancelFunc
	mu      sync.Mutex
	active  int
	closing bool
	done    chan struct{}
}

func newAPIHTTPRuntime(addr string, handler http.Handler) *APIRuntime {
	ctx, cancel := context.WithCancel(context.Background())
	requests := &apiRequestLifecycle{handler: handler, cancel: cancel, done: make(chan struct{})}
	server := newAPIServer(addr, requests)
	server.BaseContext = func(net.Listener) context.Context { return ctx }
	return &APIRuntime{Server: server, requests: requests}
}

func (requests *apiRequestLifecycle) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	requests.mu.Lock()
	if requests.closing {
		requests.mu.Unlock()
		http.Error(writer, "Server is shutting down", http.StatusServiceUnavailable)
		return
	}
	requests.active++
	requests.mu.Unlock()
	defer func() {
		requests.mu.Lock()
		requests.active--
		if requests.closing && requests.active == 0 {
			close(requests.done)
		}
		requests.mu.Unlock()
	}()
	requests.handler.ServeHTTP(writer, request)
}

func (requests *apiRequestLifecycle) stop() <-chan struct{} {
	requests.mu.Lock()
	if !requests.closing {
		requests.closing = true
		if requests.active == 0 {
			close(requests.done)
		}
	}
	requests.mu.Unlock()
	requests.cancel()
	return requests.done
}

func newAPIServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

func WaitForShutdown(ctx context.Context, cancel context.CancelFunc, runtime *APIRuntime, waitGroup *sync.WaitGroup) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)
	if ctx == nil {
		<-quit
	} else {
		select {
		case <-quit:
		case <-ctx.Done():
		}
	}
	shutdownAPIRuntime(cancel, runtime, waitGroup)
}

func shutdownAPIRuntime(cancel context.CancelFunc, runtime *APIRuntime, waitGroup *sync.WaitGroup) {
	if runtime == nil {
		if cancel != nil {
			cancel()
		}
		return
	}
	if runtime.readiness != nil {
		runtime.readiness.MarkShuttingDown()
		runtime.readiness.Stop()
	}
	shutdownTimeout := runtime.httpShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = apiShutdownTimeout
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()
	if runtime.Server != nil {
		if err := runtime.Server.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP server graceful shutdown timed out: %v", err)
			if runtime.requests != nil {
				runtime.requests.stop()
			}
			if err := runtime.Server.Close(); err != nil {
				log.Printf("close remaining HTTP connections: %v", err)
			}
		}
	}
	if runtime.requests != nil {
		timer := time.NewTimer(forcedRequestDrainTimeout)
		select {
		case <-runtime.requests.stop():
		case <-timer.C:
			log.Println("cancelled HTTP handlers did not finish within shutdown budget; in-flight results may be unknown")
		}
		timer.Stop()
	}
	if runtime.translationService != nil {
		runtime.translationService.Close()
	}
	if runtime.rateService != nil {
		runtime.rateService.Close()
	}
	traceDrainTimeout := runtime.traceDrainTimeout
	if traceDrainTimeout <= 0 {
		traceDrainTimeout = defaultTraceDrainTimeout
	}
	if runtime.traceDispatcher != nil {
		traceCtx, traceCancel := context.WithTimeout(context.Background(), traceDrainTimeout)
		if err := runtime.traceDispatcher.Shutdown(traceCtx); err != nil {
			log.Printf("recommendation trace dispatcher shutdown timed out: %v", err)
		}
		traceCancel()
	}
	if cancel != nil {
		cancel()
	}
	if waitGroup == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		waitGroup.Wait()
		close(done)
	}()
	select {
	case <-done:
		log.Println("background tasks stopped")
	case <-time.After(apiShutdownTimeout):
		log.Println("background task shutdown timed out")
	}
}
