package controllers

import (
	"errors"
	"github.com/gin-gonic/gin"
	"testing"
)

func TestRecordArticleBehaviorFromContextSkipsWithoutUserID(t *testing.T) {
	old, oldLog := recordArticleBehavior, articleBehaviorLogError
	t.Cleanup(func() { recordArticleBehavior = old; articleBehaviorLogError = oldLog })
	called := false
	recordArticleBehavior = func(uint, uint, string) error { called = true; return nil }
	articleBehaviorLogError = func(*gin.Context, string, error) {}
	c, _ := gin.CreateTestContext(nil)
	recordArticleBehaviorFromContext(c, 7, ArticleBehaviorActionView)
	if called {
		t.Fatal("called")
	}
}
func TestRecordArticleBehaviorFromContextRecordsUserID(t *testing.T) {
	old, oldLog := recordArticleBehavior, articleBehaviorLogError
	t.Cleanup(func() { recordArticleBehavior = old; articleBehaviorLogError = oldLog })
	var user, article uint
	recordArticleBehavior = func(u, a uint, _ string) error { user, article = u, a; return nil }
	articleBehaviorLogError = func(*gin.Context, string, error) {}
	c, _ := gin.CreateTestContext(nil)
	c.Set("user_id", uint(11))
	recordArticleBehaviorFromContext(c, 9, ArticleBehaviorActionLike)
	if user != 11 || article != 9 {
		t.Fatal(user, article)
	}
}
func TestRecordArticleBehaviorFromContextLogsError(t *testing.T) {
	old, oldLog := recordArticleBehavior, articleBehaviorLogError
	t.Cleanup(func() { recordArticleBehavior = old; articleBehaviorLogError = oldLog })
	want := errors.New("down")
	recordArticleBehavior = func(uint, uint, string) error { return want }
	logged := false
	articleBehaviorLogError = func(_ *gin.Context, _ string, err error) { logged = errors.Is(err, want) }
	c, _ := gin.CreateTestContext(nil)
	c.Set("user_id", uint(1))
	recordArticleBehaviorFromContext(c, 1, ArticleBehaviorActionView)
	if !logged {
		t.Fatal("not logged")
	}
}
