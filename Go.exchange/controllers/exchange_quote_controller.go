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

const (
	exchangeErrorInvalidCurrency     = "invalid_currency"
	exchangeErrorInvalidAmount       = "invalid_amount"
	exchangeErrorUnsupportedCurrency = "unsupported_currency"
	exchangeErrorUnavailable         = "exchange_unavailable"
)

type exchangeErrorResponse struct {
	Code  string `json:"code"`
	Error string `json:"error"`
}

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
		writeExchangeErrorResponse(ctx, http.StatusBadRequest, exchangeErrorInvalidCurrency, "Invalid currency.")
	case errors.Is(err, services.ErrInvalidAmount):
		writeExchangeErrorResponse(ctx, http.StatusBadRequest, exchangeErrorInvalidAmount, "Invalid amount.")
	case errors.Is(err, services.ErrUnsupportedCurrency):
		writeExchangeErrorResponse(ctx, http.StatusUnprocessableEntity, exchangeErrorUnsupportedCurrency, "Currency is not supported.")
	default:
		writeExchangeErrorResponse(ctx, http.StatusServiceUnavailable, exchangeErrorUnavailable, "Exchange-rate service is temporarily unavailable.")
	}
}

func writeExchangeErrorResponse(ctx *gin.Context, status int, code, message string) {
	ctx.JSON(status, exchangeErrorResponse{Code: code, Error: message})
}
