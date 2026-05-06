package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/elvis/flixflox/internal/config"
	"github.com/elvis/flixflox/internal/database"
	"github.com/elvis/flixflox/internal/middleware"
	"github.com/elvis/flixflox/internal/models"
	"github.com/elvis/flixflox/internal/utils"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func RegisterAuthRoutes(r chi.Router, client *mongo.Client, cfg *config.Config) {
	r.Post("/v1/api/auth/login", handleLogin(client, cfg))
	r.Post("/v1/api/auth/register", handleRegister(client, cfg))

	r.With(middleware.JWTRefresh(cfg.JWTSecret, client)).
		Post("/v1/api/auth/token/refresh", handleRefreshToken(client, cfg))

	r.Group(func(r chi.Router) {
		r.Use(middleware.JWTAuth(cfg.JWTSecret, client))
		r.Delete("/v1/api/auth/logout", handleLogout(client))
		r.Get("/v1/api/auth/check", handleAuthCheck(client))
	})
}

func handleLogin(client *mongo.Client, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := utils.DecodeBody(r, &req); err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if req.Username == "" || req.Password == "" {
			utils.Error(w, http.StatusBadRequest, "Username and password are required")
			return
		}

		coll := database.Collection(client, "users")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		var user models.User
		err := coll.FindOne(ctx, bson.M{"username": req.Username}).Decode(&user)
		if err != nil {
			utils.Error(w, http.StatusUnauthorized, "Invalid username or password")
			return
		}

		if !utils.VerifyPassword(user.Password, req.Password) {
			utils.Error(w, http.StatusUnauthorized, "Invalid username or password")
			return
		}

		accessToken, err := middleware.GenerateAccessToken(
			cfg.JWTSecret, user.ID.Hex(), user.Role, user.UUID, user.Privileges, cfg.AccessTokenTTL,
		)
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to generate access token")
			return
		}

		refreshToken, err := middleware.GenerateRefreshToken(
			cfg.JWTSecret, user.ID.Hex(), user.UUID, cfg.RefreshTokenTTL,
		)
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to generate refresh token")
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     "access_token",
			Value:    accessToken,
			Path:     "/",
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteNoneMode,
			MaxAge:   int(cfg.AccessTokenTTL.Seconds()),
		})
		http.SetCookie(w, &http.Cookie{
			Name:     "refresh_token",
			Value:    refreshToken,
			Path:     "/",
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteNoneMode,
			MaxAge:   int(cfg.RefreshTokenTTL.Seconds()),
		})

		utils.Success(w, http.StatusOK, map[string]any{
			"status":        "success",
			"access_token":  accessToken,
			"refresh_token": refreshToken,
			"username":      user.Username,
			"role":          user.Role,
		})
	}
}

func handleRegister(client *mongo.Client, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Email    string `json:"email"`
			Role     string `json:"role"`
		}
		if err := utils.DecodeBody(r, &req); err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if req.Username == "" || req.Password == "" || req.Email == "" {
			utils.Error(w, http.StatusBadRequest, "Username, password, and email are required")
			return
		}

		if len(req.Password) < 8 {
			utils.Error(w, http.StatusBadRequest, "Password must be at least 8 characters")
			return
		}

		hashedPassword, err := utils.HashPassword(req.Password)
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to hash password")
			return
		}

		userUUID := uuid.New().String()
		now := time.Now()

		role := req.Role
		if role == "" {
			role = "user"
		}

		user := models.User{
			UUID:       userUUID,
			Username:   req.Username,
			Password:   hashedPassword,
			Email:      req.Email,
			Role:       role,
			Privileges: []string{"read"},
			CreatedAt:  now,
		}

		coll := database.Collection(client, "users")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		count, _ := coll.CountDocuments(ctx, bson.M{
			"$or": []bson.M{
				{"username": req.Username},
				{"email": req.Email},
			},
		})
		if count > 0 {
			utils.Error(w, http.StatusConflict, "Username or email already exists")
			return
		}

		_, err = coll.InsertOne(ctx, user)
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to create user")
			return
		}

		viewer := models.Viewer{
			UUID:      uuid.New().String(),
			Name:      req.Username,
			Color:     "#1890ff",
			UsePin:    false,
			Status:    "active",
			UserUUID:  userUUID,
			CreatedAt: now,
		}
		viewerColl := database.Collection(client, "viewers")
		viewerColl.InsertOne(ctx, viewer)

		utils.Success(w, http.StatusCreated, map[string]any{
			"status":  "success",
			"message": "User registered successfully",
		})
	}
}

func handleRefreshToken(client *mongo.Client, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.GetClaims(r)
		if claims == nil {
			utils.Error(w, http.StatusUnauthorized, "Invalid token")
			return
		}

		coll := database.Collection(client, "users")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		objID, err := bson.ObjectIDFromHex(claims.UserID)
		if err != nil {
			utils.Error(w, http.StatusUnauthorized, "Invalid user ID in token")
			return
		}

		var user models.User
		if err := coll.FindOne(ctx, bson.M{"_id": objID}).Decode(&user); err != nil {
			utils.Error(w, http.StatusUnauthorized, "User not found")
			return
		}

		accessToken, err := middleware.GenerateAccessToken(
			cfg.JWTSecret, user.ID.Hex(), user.Role, user.UUID, user.Privileges, cfg.AccessTokenTTL,
		)
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to generate token")
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     "access_token",
			Value:    accessToken,
			Path:     "/",
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteNoneMode,
			MaxAge:   int(cfg.AccessTokenTTL.Seconds()),
		})

		utils.Success(w, http.StatusOK, map[string]any{
			"status":       "success",
			"access_token": accessToken,
		})
	}
}

func handleLogout(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.GetClaims(r)
		if claims == nil {
			utils.Error(w, http.StatusUnauthorized, "Invalid token")
			return
		}

		coll := database.Collection(client, "token_blacklist")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		coll.InsertOne(ctx, models.BlacklistedToken{
			JTI:       claims.ID,
			CreatedAt: time.Now(),
			ExpiresAt: claims.ExpiresAt.Time,
		})

		http.SetCookie(w, &http.Cookie{
			Name:     "access_token",
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteNoneMode,
		})
		http.SetCookie(w, &http.Cookie{
			Name:     "refresh_token",
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   true,
			SameSite: http.SameSiteNoneMode,
		})

		utils.Success(w, http.StatusOK, map[string]any{
			"status":  "success",
			"message": "Logged out successfully",
		})
	}
}

func handleAuthCheck(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.GetClaims(r)
		if claims == nil {
			utils.Error(w, http.StatusUnauthorized, "Invalid token")
			return
		}

		coll := database.Collection(client, "users")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		objID, err := bson.ObjectIDFromHex(claims.UserID)
		if err != nil {
			utils.Error(w, http.StatusUnauthorized, "Invalid user ID")
			return
		}

		var user models.User
		if err := coll.FindOne(ctx, bson.M{"_id": objID}).Decode(&user); err != nil {
			utils.Error(w, http.StatusNotFound, "User not found")
			return
		}

		utils.Success(w, http.StatusOK, map[string]any{
			"status":        "success",
			"authenticated": true,
			"user":          user.ToResponse(),
		})
	}
}
