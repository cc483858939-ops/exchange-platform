package controllers

import (
	"Go.exchange/global"
	"Go.exchange/models"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func CreateExchangeRate(ctx *gin.Context) {
	var exchangeRate models.ExchangeRate
	if err := ctx.ShouldBind(&exchangeRate); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	exchangeRate.Date = time.Now()
	if err := global.Db.WithContext(ctx.Request.Context()).Create(&exchangeRate).Error; err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, exchangeRate)
}
func GetExchangeRates(ctx *gin.Context) {
	var exchangeRates []models.ExchangeRate
	if err := global.Db.WithContext(ctx.Request.Context()).Find(&exchangeRates).Error; err != nil {
		if handleRequestDBError(ctx, err) {
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		} else {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	ctx.JSON(http.StatusOK, exchangeRates)
}
