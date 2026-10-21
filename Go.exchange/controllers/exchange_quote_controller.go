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

const (
	exchangeErrorInvalidCurrency     = "INVALID_CURRENCY"
	exchangeErrorInvalidAmount       = "INVALID_AMOUNT"
	exchangeErrorUnsupportedCurrency = "UNSUPPORTED_CURRENCY"
	exchangeErrorUnavailable         = "EXCHANGE_UNAVAILABLE"
)

func GetExchangeCurrencies(ctx *gin.Context) {
	getExchangeCurrencies(ctx, services.DefaultExchangeRateService())
}

func getExchangeCurrencies(ctx *gin.Context, reader exchangeQuoteReader) {
	currencies, err := reader.Currencies(ctx.Request.Context())
	if err != nil {
		writeExchangeError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, currencies)
}

func GetExchangeQuote(ctx *gin.Context) {
	getExchangeQuote(ctx, services.DefaultExchangeRateService())
}

func getExchangeQuote(ctx *gin.Context, reader exchangeQuoteReader) {
	quote, err := reader.Quote(
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
		writeAPIError(ctx, exchangeErrorInvalidCurrency)
	case errors.Is(err, services.ErrInvalidAmount):
		writeAPIError(ctx, exchangeErrorInvalidAmount)
	case errors.Is(err, services.ErrUnsupportedCurrency):
		writeAPIError(ctx, exchangeErrorUnsupportedCurrency)
	default:
		writeInternalAPIErrorAs(ctx, err, exchangeErrorUnavailable)
	}
}
