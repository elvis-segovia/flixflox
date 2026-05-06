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

func RegisterUserRoutes(r chi.Router, client *mongo.Client, cfg *config.Config) {
	r.Group(func(r chi.Router) {
		r.Use(middleware.JWTAuth(cfg.JWTSecret, client))
		r.Get("/v1/api/users", handleListUsers(client))
		r.Get("/v1/api/users/{id}", handleGetUser(client))
		r.Post("/v1/api/users", handleCreateUser(client))
		r.Put("/v1/api/users/{id}", handleUpdateUser(client))
		r.Delete("/v1/api/users/{id}", handleDeleteUser(client))
	})
}

func handleListUsers(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		coll := database.Collection(client, "users")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		cursor, err := coll.Find(ctx, bson.M{})
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to fetch users")
			return
		}
		defer cursor.Close(ctx)

		var users []models.User
		if err := cursor.All(ctx, &users); err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to decode users")
			return
		}

		responses := make([]models.UserResponse, len(users))
		for i, u := range users {
			responses[i] = u.ToResponse()
		}

		utils.Success(w, http.StatusOK, responses)
	}
}

func handleGetUser(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		objID, err := bson.ObjectIDFromHex(id)
		if err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid user ID")
			return
		}

		coll := database.Collection(client, "users")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		var user models.User
		if err := coll.FindOne(ctx, bson.M{"_id": objID}).Decode(&user); err != nil {
			utils.Error(w, http.StatusNotFound, "User not found")
			return
		}

		utils.Success(w, http.StatusOK, map[string]any{
			"status": "success",
			"user":   user.ToResponse(),
		})
	}
}

func handleCreateUser(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Username   string   `json:"username"`
			Password   string   `json:"password"`
			Email      string   `json:"email"`
			Role       string   `json:"role"`
			Privileges []string `json:"privileges"`
		}
		if err := utils.DecodeBody(r, &req); err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if req.Username == "" || req.Password == "" || req.Email == "" {
			utils.Error(w, http.StatusBadRequest, "Username, password, and email are required")
			return
		}

		hashedPassword, err := utils.HashPassword(req.Password)
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to hash password")
			return
		}

		privileges := req.Privileges
		if privileges == nil {
			privileges = []string{"read"}
		}

		userUUID := uuid.New().String()
		now := time.Now()

		user := models.User{
			UUID:       userUUID,
			Username:   req.Username,
			Password:   hashedPassword,
			Email:      req.Email,
			Role:       req.Role,
			Privileges: privileges,
			CreatedAt:  now,
		}

		coll := database.Collection(client, "users")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		result, err := coll.InsertOne(ctx, user)
		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				utils.Error(w, http.StatusConflict, "Username or email already exists")
				return
			}
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
			"status": "success",
			"id":     result.InsertedID,
		})
	}
}

func handleUpdateUser(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		objID, err := bson.ObjectIDFromHex(id)
		if err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid user ID")
			return
		}

		var updates map[string]any
		if err := utils.DecodeBody(r, &updates); err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if pw, ok := updates["password"].(string); ok && pw != "" {
			hashed, err := utils.HashPassword(pw)
			if err != nil {
				utils.Error(w, http.StatusInternalServerError, "Failed to hash password")
				return
			}
			updates["password"] = hashed
		}

		updates["updated_at"] = time.Now()
		delete(updates, "_id")
		delete(updates, "id")
		delete(updates, "uuid")

		coll := database.Collection(client, "users")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		result, err := coll.UpdateOne(ctx, bson.M{"_id": objID}, bson.M{"$set": updates})
		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				utils.Error(w, http.StatusConflict, "Username or email already exists")
				return
			}
			utils.Error(w, http.StatusInternalServerError, "Failed to update user")
			return
		}

		if result.MatchedCount == 0 {
			utils.Error(w, http.StatusNotFound, "User not found")
			return
		}

		utils.Success(w, http.StatusOK, map[string]any{
			"status": "success",
			"id":     id,
		})
	}
}

func handleDeleteUser(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		objID, err := bson.ObjectIDFromHex(id)
		if err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid user ID")
			return
		}

		coll := database.Collection(client, "users")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		result, err := coll.DeleteOne(ctx, bson.M{"_id": objID})
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to delete user")
			return
		}

		if result.DeletedCount == 0 {
			utils.Error(w, http.StatusNotFound, "User not found")
			return
		}

		utils.Success(w, http.StatusOK, map[string]any{
			"status": "success",
			"id":     id,
		})
	}
}
