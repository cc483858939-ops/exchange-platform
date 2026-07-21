package controllers

import (
	"context"
	"errors"
	"net/http"

	"Go.exchange/services"

	"github.com/gin-gonic/gin"
)

type exchangeQuoteReader interface {
	Currencies(context.Context) (services.CurrencyList, error)
	Quote(context.Context, string, string, string) (services.Quote, error)
}

var liveExchangeQuoteReader exchangeQuoteReader

func currentExchangeQuoteReader() exchangeQuoteReader {
	if liveExchangeQuoteReader == nil {
		liveExchangeQuoteReader = services.DefaultExchangeRateService()
	}
	return liveExchangeQuoteReader
}

func GetExchangeCurrencies(ctx *gin.Context) {
	currencies, err := currentExchangeQuoteReader().Currencies(ctx.Request.Context())
	if err != nil {
		writeExchangeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, currencies)
}

func GetExchangeQuote(ctx *gin.Context) {
	quote, err := currentExchangeQuoteReader().Quote(
		ctx.Request.Context(),
		ctx.Query("from"),
		ctx.Query("to"),
		ctx.Query("amount"),
	)
	if err != nil {
		writeExchangeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, quote)
}

func writeExchangeError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, services.ErrInvalidCurrency):
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "?????????????"})
	case errors.Is(err, services.ErrInvalidAmount):
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "?????????????"})
	case errors.Is(err, services.ErrUnsupportedCurrency):
		ctx.JSON(http.StatusUnprocessableEntity, gin.H{"error": "?????????"})
	default:
		ctx.JSON(http.StatusServiceUnavailable, gin.H{"error": "??????????????"})
	}
}
