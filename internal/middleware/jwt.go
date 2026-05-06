package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/elvis/flixflox/internal/database"
	"github.com/elvis/flixflox/internal/utils"
	"github.com/golang-jwt/jwt/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type contextKey string

const ClaimsKey contextKey = "claims"

type TokenClaims struct {
	UserID     string   `json:"user_id"`
	Role       string   `json:"role"`
	Privileges []string `json:"privileges"`
	UserUUID   string   `json:"user_uuid"`
	TokenType  string   `json:"token_type"` // "access" or "refresh"
	jwt.RegisteredClaims
}

func GenerateAccessToken(secret []byte, userID, role, userUUID string, privileges []string, ttl time.Duration) (string, error) {
	claims := TokenClaims{
		UserID:     userID,
		Role:       role,
		Privileges: privileges,
		UserUUID:   userUUID,
		TokenType:  "access",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        bson.NewObjectID().Hex(),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

func GenerateRefreshToken(secret []byte, userID, userUUID string, ttl time.Duration) (string, error) {
	claims := TokenClaims{
		UserID:    userID,
		UserUUID:  userUUID,
		TokenType: "refresh",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        bson.NewObjectID().Hex(),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

func parseToken(secret []byte, tokenString string) (*TokenClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &TokenClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*TokenClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}

func isTokenBlacklisted(client *mongo.Client, jti string) bool {
	coll := database.Collection(client, "token_blacklist")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	count, err := coll.CountDocuments(ctx, bson.M{"jti": jti})
	return err == nil && count > 0
}

func JWTAuth(secret []byte, client *mongo.Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenString := extractToken(r)
			if tokenString == "" {
				utils.Error(w, http.StatusUnauthorized, "Missing authorization token")
				return
			}

			claims, err := parseToken(secret, tokenString)
			if err != nil {
				utils.Error(w, http.StatusUnauthorized, "Invalid or expired token")
				return
			}

			if isTokenBlacklisted(client, claims.ID) {
				utils.Error(w, http.StatusUnauthorized, "Token has been revoked")
				return
			}

			ctx := context.WithValue(r.Context(), ClaimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func JWTRefresh(secret []byte, client *mongo.Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenString := extractToken(r)
			if tokenString == "" {
				utils.Error(w, http.StatusUnauthorized, "Missing refresh token")
				return
			}

			claims, err := parseToken(secret, tokenString)
			if err != nil {
				utils.Error(w, http.StatusUnauthorized, "Invalid or expired refresh token")
				return
			}

			if claims.TokenType != "refresh" {
				utils.Error(w, http.StatusUnauthorized, "Not a refresh token")
				return
			}

			if isTokenBlacklisted(client, claims.ID) {
				utils.Error(w, http.StatusUnauthorized, "Token has been revoked")
				return
			}

			ctx := context.WithValue(r.Context(), ClaimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func extractToken(r *http.Request) string {
	// Check Authorization header first
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	// Fall back to cookie
	if cookie, err := r.Cookie("access_token"); err == nil {
		return cookie.Value
	}
	if cookie, err := r.Cookie("refresh_token"); err == nil {
		return cookie.Value
	}
	return ""
}

func GetClaims(r *http.Request) *TokenClaims {
	claims, _ := r.Context().Value(ClaimsKey).(*TokenClaims)
	return claims
}
