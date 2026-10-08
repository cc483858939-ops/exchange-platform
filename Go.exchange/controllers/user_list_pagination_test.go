package controllers

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestUserListPaginationRejectsUnsupportedDepth(t *testing.T) {
	for _, parser := range []struct {
		name  string
		parse func(*gin.Context) (int, int, error)
	}{
		{"search", parseUserSearchPagination}, {"connections", parseFollowListPagination},
	} {
		for _, tc := range []struct {
			offset string
			valid  bool
		}{{"0", true}, {"10000", true}, {"10001", false}, {"1000000000", false}, {"-1", false}, {"9999999999999999999999999", false}} {
			t.Run(parser.name+"/"+tc.offset, func(t *testing.T) {
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx.Request = httptest.NewRequest("GET", fmt.Sprintf("/users?limit=50&offset=%s", tc.offset), nil)
				limit, _, err := parser.parse(ctx)
				if (err == nil) != tc.valid {
					t.Fatalf("limit=%d err=%v valid=%t", limit, err, tc.valid)
				}
			})
		}
	}
}
