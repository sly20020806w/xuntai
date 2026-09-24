package auth

import (
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	jwt.RegisteredClaims
	UID   uint     `json:"uid"`
	Name  string   `json:"name"`
	Roles []string `json:"roles"`
}

func Sign(secret string, uid uint, name string, roles []string) (string, error) {
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
		},
		UID:   uid,
		Name:  name,
		Roles: roles,
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

func Parse(secret, header string) (Claims, error) {
	raw := strings.TrimPrefix(header, "Bearer ")
	if raw == "" || raw == header {
		return Claims{}, errors.New("missing token")
	}
	var claims Claims
	token, err := jwt.ParseWithClaims(raw, &claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return []byte(secret), nil
	})
	if err != nil || !token.Valid {
		return Claims{}, errors.New("invalid token")
	}
	return claims, nil
}
