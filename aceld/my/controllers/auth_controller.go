package controllers

import (
	"aceld/global"
	"aceld/models"
	"aceld/utils"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func Register(ctx *gin.Context) {
	var user models.User

	// 1. 绑定请求中的 JSON 数据到 user 结构体
	if err := ctx.ShouldBindJSON(&user); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request data"})
		return
	}

	// 2. 对密码进行哈希加密
	hashedPwd, err := utils.HashPassword(user.Password)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}
	user.Password = hashedPwd

	// 3. 【已修改】使用 Create 创建新用户，而不是 AutoMigrate
	//    AutoMigrate 是用来同步表结构的，Create 才是插入数据
	if err := global.Db.Create(&user).Error; err != nil {
		// 可以在这里增加一个检查，判断是否是用户名冲突的错误
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
		return
	}

	// 4. 为新注册的用户生成 JWT
	token, err := utils.GenerateJWT(user.Username)
	if err != nil {
		// 【已修改】使用 err.Error() 来获取错误字符串
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 5. 返回成功响应和 token
	ctx.JSON(http.StatusOK, gin.H{
		"message": "Registration successful",
		"token":   token,
	})
}

func Login(ctx *gin.Context) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	// 1. 绑定请求数据
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request data"})
		return
	}

	var user models.User
	// 2. 【已修改】修正数据库查询逻辑
	//    - 使用 .Where("username = ?", ...).First(&user) 来查找并填充 user 对象
	//    - 假设您的数据库列名是 username
	if err := global.Db.Where("username = ?", input.Username).First(&user).Error; err != nil {
		// 如果查询出错，检查是否是因为“记录未找到”
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ctx.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"})
		} else {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		}
		return
	}

	// 3. 检查密码是否匹配
	if !utils.CheckPassword(input.Password, user.Password) {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"})
		return
	}

	// 4. 生成 JWT
	token, err := utils.GenerateJWT(user.Username)
	if err != nil {
		// 【已修改】使用 err.Error() 来获取错误字符串
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 5. 返回成功响应和 token
	ctx.JSON(http.StatusOK, gin.H{"token": token})
}
