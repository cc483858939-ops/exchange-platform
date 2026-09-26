package controllers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type sqlStateError string

func (err sqlStateError) Error() string    { return string(err) }
func (err sqlStateError) SQLState() string { return string(err) }

func newErrorTestContext(requestContext context.Context) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/test", nil).WithContext(requestContext)
	return ctx, recorder
}

func TestHandleRequestDBErrorSuppressesCanceledRequest(t *testing.T) {
	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	ctx, recorder := newErrorTestContext(requestContext)

	if !handleRequestDBError(ctx, context.Canceled) {
		t.Fatal("handleRequestDBError() = false, want true")
	}
	if recorder.Code == http.StatusGatewayTimeout || recorder.Body.Len() != 0 {
		t.Fatalf("canceled request wrote response: status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

func TestHandleRequestDBErrorMapsTimeoutsToGatewayTimeout(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "context deadline", err: context.DeadlineExceeded},
		{name: "statement timeout", err: sqlStateError("57014")},
		{name: "lock timeout", err: sqlStateError("55P03")},
		{name: "wrapped SQLSTATE", err: errors.Join(errors.New("query failed"), sqlStateError("57014"))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, recorder := newErrorTestContext(context.Background())
			if !handleRequestDBError(ctx, test.err) {
				t.Fatal("handleRequestDBError() = false, want true")
			}
			if recorder.Code != http.StatusGatewayTimeout {
				t.Fatalf("status=%d, want %d", recorder.Code, http.StatusGatewayTimeout)
			}
		})
	}
}

func TestHandleRequestDBErrorLeavesOrdinaryErrorsToCaller(t *testing.T) {
	ctx, recorder := newErrorTestContext(context.Background())
	if handleRequestDBError(ctx, errors.New("database unavailable")) {
		t.Fatal("ordinary database error was classified as a timeout")
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("ordinary database error wrote response body: %q", recorder.Body.String())
	}
}
