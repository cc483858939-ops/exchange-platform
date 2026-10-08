package controllers

import (
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	ginjson "github.com/gin-gonic/gin/codec/json"
)

const (
	postStateRequestMaxBytes = 16 << 10
	profilePatchMaxBytes     = 16 << 10
)

func limitJSONRequest(ctx *gin.Context, limit int64) error {
	if ctx.Request.ContentLength > limit {
		return &http.MaxBytesError{Limit: limit}
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, limit)
	return nil
}

func bindBoundedPostStates(ctx *gin.Context, destination any) error {
	if err := limitJSONRequest(ctx, postStateRequestMaxBytes); err != nil {
		return err
	}
	decoder := ginjson.API.NewDecoder(ctx.Request.Body)
	if binding.EnableDecoderUseNumber {
		decoder.UseNumber()
	}
	if binding.EnableDecoderDisallowUnknownFields {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values in post state request")
		}
		return err
	}
	if binding.Validator != nil {
		return binding.Validator.ValidateStruct(destination)
	}
	return nil
}

func isRequestBodyTooLarge(err error) bool {
	var sizeError *http.MaxBytesError
	return errors.As(err, &sizeError)
}

func writeJSONRequestError(ctx *gin.Context, err error, invalidMessage string) {
	if isRequestBodyTooLarge(err) {
		ctx.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "Request body too large"})
		return
	}
	ctx.JSON(http.StatusBadRequest, gin.H{"error": invalidMessage})
}
