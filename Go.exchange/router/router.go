package router

import (
	"Go.exchange/controllers"
	"Go.exchange/middlewares"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func SetupRouter() *gin.Engine {
	r := gin.Default()

	// 配置 CORS
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// --- 公共认证接口 ---
	auth := r.Group("/api/auth")
	{
		auth.POST("/login", controllers.Login)
		auth.POST("/register", controllers.Register)
		
		// 放在这里调用它时 AccessToken 已过期，不能经过 AuthMiddleWare
		auth.POST("/refresh", controllers.RefreshToken) 
	}

	// --- 业务接口 ---
	api := r.Group("/api")
	
	// 公共业务接口 
	api.GET("/exchangeRates", controllers.GetExchangeRates)

	// --- 需要鉴权的接口 需要 Access Token ---
	// 启用中间件
	api.Use(middlewares.AuthMiddleWare()) 
	{
		api.POST("/exchangeRates", controllers.CreateExchangeRate)
		
		api.POST("/articles", controllers.CreateArticle)
		api.GET("/articles", controllers.GetArticle)
		api.GET("/articles/:id", controllers.GetArticleByID)

		api.POST("/articles/:id/like", controllers.LikeArticle)
		api.GET("/articles/:id/like", controllers.GetArticleLikes)
	}
	return r
}