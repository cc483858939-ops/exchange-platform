package controllers

import (
	"Go.exchange/consts"
	"Go.exchange/global"
	"Go.exchange/models"
	"Go.exchange/utils"
	"errors"
	"fmt" // 必须引入
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// SaveRefreshTokenToRedis 将 Refresh Token 存入 Redis
func SaveRefreshTokenToRedis(username string, token string) error {
	key := fmt.Sprintf(consts.Refresh, username)
	//  return err 变量，
	err := global.RedisDB.Set(key, token, utils.RefreshTokenDuration).Err()
	return err
}

// Register 注册
func Register(ctx *gin.Context) {
	var user models.User
	if err := ctx.ShouldBindJSON(&user); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request data"})
		return
	}

	// 1. 密码加密
	hashedPwd, err := utils.HashPassword(user.Password)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}
	// 加密后的密码赋回去
	user.Password = hashedPwd

	// 2. 存入数据库
	if err := global.Db.Create(&user).Error; err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "User already exists or database error"})
		return
	}

	// 3. 生成双 Token
	accessToken, refreshToken, err := utils.GenerateTokenPair(user.Username)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 4. 存入 Redis
	if err := SaveRefreshTokenToRedis(user.Username, refreshToken); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Redis error"})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"message":       "Registration successful",
		"access_token":  accessToken,
		"refresh_token": refreshToken,
	})
}

// Login 登录
func Login(ctx *gin.Context) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request data"})
		return
	}

	// 1. 查库
	var user models.User
	if err := global.Db.Where("username = ?", input.Username).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"})
		} else {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		}
		return
	}

	// 2. 校验密码
	if !utils.CheckPassword(input.Password, user.Password) {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"})
		return
	}

	// 3. 生成双 Token (匹配 RefreshToken 逻辑)
	accessToken, refreshToken, err := utils.GenerateTokenPair(user.Username)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 4. 存入 Redis
	if err := SaveRefreshTokenToRedis(user.Username, refreshToken); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Redis error"})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
	})
}

// RefreshToken 刷新 Token
func RefreshToken(ctx *gin.Context) {
	var input struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request data"})
		return
	}

	// 1. 解析与验签
	_, claims, err := utils.ParseJWT(input.RefreshToken)
	if err != nil {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid refresh token"})
		return
	}

	// 2. 校验 Token 类型
	if tokenType, ok := claims["type"].(string); !ok || tokenType != "refresh" {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token type"})
		return
	}

	username, ok := claims["username"].(string)
	if !ok {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token claims"})
		return
	}

	// 3. Redis 校验
	redisKey := fmt.Sprintf(consts.Refresh, username)
	storedToken, err := global.RedisDB.Get(redisKey).Result()
	if err != nil {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "Refresh token expired or logged out"})
		return
	}

	// 4. 核心比对
	if storedToken != input.RefreshToken {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "Token mismatch"})
		return
	}

	// 5. 生成新的双 Token (Rolling Update 机制)
	newAccessToken, newRefreshToken, err := utils.GenerateTokenPair(username)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Error generating token"})
		return
	}

	// 6. 更新 Redis 中的旧 Token
	if err := SaveRefreshTokenToRedis(username, newRefreshToken); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Redis error"})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"access_token":  newAccessToken,
		"refresh_token": newRefreshToken,
	})
}