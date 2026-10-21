package controllers

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"Go.exchange/apierrors"
)

func requireAPIError(t *testing.T, response *httptest.ResponseRecorder, code string) apierrors.ErrorResponse {
	t.Helper()
	var body apierrors.ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode API error response: %v; body=%s", err, response.Body.String())
	}
	if string(body.Code) != code || body.Error == "" || body.RequestID == "" {
		t.Fatalf("API error response=%+v, want code %q and non-empty error/request_id", body, code)
	}
	if response.Header().Get(apierrors.RequestIDHeader) != body.RequestID {
		t.Fatalf("request ID header=%q body=%q", response.Header().Get(apierrors.RequestIDHeader), body.RequestID)
	}
	return body
}
