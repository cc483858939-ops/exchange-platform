package controllers

import (
	"net/http"

	"Go.exchange/runtimehealth"

	"github.com/gin-gonic/gin"
)

func Healthz(ctx *gin.Context) {
	ctx.Header("Cache-Control", "no-store")
	ctx.Header("Content-Type", "application/json")
	ctx.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func Readyz(ctx *gin.Context) {
	ReadyzWithProvider(nil)(ctx)
}

func ReadyzWithProvider(provider runtimehealth.APIReadinessProvider) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.Header("Cache-Control", "no-store")
		ctx.Header("Content-Type", "application/json")
		statusCode := http.StatusServiceUnavailable
		var snapshot runtimehealth.APISnapshot
		if provider == nil {
			snapshot = runtimehealth.APISnapshot{
				Status:      "not_ready",
				Role:        "api",
				Checks:      map[string]string{},
				ReasonCodes: []string{"readiness_provider_unavailable"},
			}
		} else {
			snapshot = provider.Snapshot()
			if snapshot.Status == "ready" {
				statusCode = http.StatusOK
			}
		}
		ctx.JSON(statusCode, snapshot)
	}
}
