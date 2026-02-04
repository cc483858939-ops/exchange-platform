package utils

import (
	"errors"
	"strings" 
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	
	AccessTokenDuration  = time.Hour * 1
	RefreshTokenDuration = time.Hour * 24 * 7
)

var Secret = []byte("secret")

func HashPassword(pwd string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(pwd), 12)
	return string(hash), err
}

func CheckPassword(password string, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

func GenerateTokenPair(username string) (accessToken string, refreshToken string, err error) {
	// 1. 生成 Access Token
	atClaim := jwt.MapClaims{
		"username": username,
		"exp":      time.Now().Add(AccessTokenDuration).Unix(),
		"type":     "access", 
	}
	
	at := jwt.NewWithClaims(jwt.SigningMethodHS256, atClaim)
	accessToken, err = at.SignedString(Secret)
	if err != nil {
		return "", "", err
	}
	
	accessToken = "Bearer " + accessToken

	// 2. 生成 Refresh Token
	rtClaim := jwt.MapClaims{
		"username": username,
		"exp":      time.Now().Add(RefreshTokenDuration).Unix(), 
		"type":     "refresh",
	}
	
	rt := jwt.NewWithClaims(jwt.SigningMethodHS256, rtClaim)
	refreshToken, err = rt.SignedString(Secret)
	if err != nil {

		return "", "", err
	}

	return accessToken, refreshToken, nil
}

func ParseJWT(tokenString string) (*jwt.Token, jwt.MapClaims, error) {
	// 处理 Bearer 前缀
	if len(tokenString) > 7 && strings.ToUpper(tokenString[:7]) == "BEARER " {
		tokenString = tokenString[7:]
	}

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected Signing Method")
		}
		return Secret, nil
	})

	if err != nil {
		return nil, nil, err
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		return token, claims, nil
	}

	return nil, nil, errors.New("invalid token")
}