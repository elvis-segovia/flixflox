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

func RegisterViewerRoutes(r chi.Router, client *mongo.Client, cfg *config.Config) {
	r.Group(func(r chi.Router) {
		r.Use(middleware.JWTAuth(cfg.JWTSecret, client))
		r.Get("/v1/api/viewers", handleListViewers(client))
		r.Get("/v1/api/viewers/{uuid}", handleGetViewer(client))
		r.Post("/v1/api/viewers", handleCreateViewer(client))
		r.Put("/v1/api/viewers/{uuid}", handleUpdateViewer(client))
		r.Delete("/v1/api/viewers/{uuid}", handleDeleteViewer(client))
	})
}

func handleListViewers(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.GetClaims(r)
		if claims == nil {
			utils.Error(w, http.StatusUnauthorized, "Unauthorized")
			return
		}

		coll := database.Collection(client, "viewers")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		cursor, err := coll.Find(ctx, bson.M{"user_uuid": claims.UserUUID})
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to fetch viewers")
			return
		}
		defer cursor.Close(ctx)

		var viewers []models.Viewer
		if err := cursor.All(ctx, &viewers); err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to decode viewers")
			return
		}

		responses := make([]models.ViewerResponse, len(viewers))
		for i, v := range viewers {
			responses[i] = v.ToResponse()
		}

		utils.Success(w, http.StatusOK, responses)
	}
}

func handleGetViewer(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.GetClaims(r)
		if claims == nil {
			utils.Error(w, http.StatusUnauthorized, "Unauthorized")
			return
		}

		viewerUUID := chi.URLParam(r, "uuid")

		coll := database.Collection(client, "viewers")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		var viewer models.Viewer
		err := coll.FindOne(ctx, bson.M{
			"uuid":      viewerUUID,
			"user_uuid": claims.UserUUID,
		}).Decode(&viewer)
		if err != nil {
			utils.Error(w, http.StatusNotFound, "Viewer not found")
			return
		}

		utils.Success(w, http.StatusOK, viewer.ToResponse())
	}
}

func handleCreateViewer(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.GetClaims(r)
		if claims == nil {
			utils.Error(w, http.StatusUnauthorized, "Unauthorized")
			return
		}

		var req struct {
			Name   string `json:"name"`
			Color  string `json:"color"`
			Pin    string `json:"pin"`
			UsePin bool   `json:"use_pin"`
			Status string `json:"status"`
		}
		if err := utils.DecodeBody(r, &req); err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if req.Name == "" {
			utils.Error(w, http.StatusBadRequest, "Name is required")
			return
		}

		color := req.Color
		if color == "" {
			color = "#1890ff"
		}

		viewer := models.Viewer{
			UUID:      uuid.New().String(),
			Name:      req.Name,
			Color:     color,
			UsePin:    req.UsePin,
			Status:    req.Status,
			UserUUID:  claims.UserUUID,
			CreatedAt: time.Now(),
		}

		if req.UsePin && req.Pin != "" {
			hashed, err := utils.HashPassword(req.Pin)
			if err != nil {
				utils.Error(w, http.StatusInternalServerError, "Failed to hash pin")
				return
			}
			viewer.Pin = hashed
		}

		coll := database.Collection(client, "viewers")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		result, err := coll.InsertOne(ctx, viewer)
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to create viewer")
			return
		}

		utils.Success(w, http.StatusCreated, map[string]any{
			"status": "success",
			"id":     result.InsertedID,
		})
	}
}

func handleUpdateViewer(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.GetClaims(r)
		if claims == nil {
			utils.Error(w, http.StatusUnauthorized, "Unauthorized")
			return
		}

		viewerUUID := chi.URLParam(r, "uuid")

		var updates map[string]any
		if err := utils.DecodeBody(r, &updates); err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if pin, ok := updates["pin"].(string); ok && pin != "" {
			hashed, err := utils.HashPassword(pin)
			if err != nil {
				utils.Error(w, http.StatusInternalServerError, "Failed to hash pin")
				return
			}
			updates["pin"] = hashed
		}

		updates["updated_at"] = time.Now()
		delete(updates, "_id")
		delete(updates, "id")
		delete(updates, "uuid")
		delete(updates, "user_uuid")

		coll := database.Collection(client, "viewers")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		result, err := coll.UpdateOne(ctx,
			bson.M{"uuid": viewerUUID, "user_uuid": claims.UserUUID},
			bson.M{"$set": updates},
		)
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to update viewer")
			return
		}

		if result.MatchedCount == 0 {
			utils.Error(w, http.StatusNotFound, "Viewer not found")
			return
		}

		utils.Success(w, http.StatusOK, map[string]any{
			"status": "success",
		})
	}
}

func handleDeleteViewer(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.GetClaims(r)
		if claims == nil {
			utils.Error(w, http.StatusUnauthorized, "Unauthorized")
			return
		}

		viewerUUID := chi.URLParam(r, "uuid")

		coll := database.Collection(client, "viewers")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		result, err := coll.DeleteOne(ctx, bson.M{
			"uuid":      viewerUUID,
			"user_uuid": claims.UserUUID,
		})
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to delete viewer")
			return
		}

		if result.DeletedCount == 0 {
			utils.Error(w, http.StatusNotFound, "Viewer not found")
			return
		}

		utils.Success(w, http.StatusOK, map[string]any{
			"status": "success",
		})
	}
}
