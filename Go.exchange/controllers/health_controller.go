package controllers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func Healthz(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func Readyz(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{"status": "ready"})
}
