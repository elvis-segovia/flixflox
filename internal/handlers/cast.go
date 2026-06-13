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

func RegisterCastRoutes(r chi.Router, client *mongo.Client, cfg *config.Config) {
	r.Group(func(r chi.Router) {
		r.Use(middleware.JWTAuth(cfg.JWTSecret, client))
		r.Get("/v1/api/cast", handleListCast(client))
		r.Get("/v1/api/cast/{id}", handleGetCast(client))
		r.Post("/v1/api/cast", handleCreateCast(client))
		r.Put("/v1/api/cast/{id}", handleUpdateCast(client))
		r.Delete("/v1/api/cast/{id}", handleDeleteCast(client))
	})
}

func handleListCast(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		coll := database.Collection(client, "cast")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		cursor, err := coll.Find(ctx, bson.M{})
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to fetch cast")
			return
		}
		defer cursor.Close(ctx)

		var cast []models.Cast
		if err := cursor.All(ctx, &cast); err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to decode genres")
			return
		}

		utils.Success(w, http.StatusOK, cast)
	}
}

func handleGetCast(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		objID, err := bson.ObjectIDFromHex(id)
		if err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid cast ID")
			return
		}

		coll := database.Collection(client, "cast")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		var cast models.Cast
		if err := coll.FindOne(ctx, bson.M{"_id": objID}).Decode(&cast); err != nil {
			utils.Error(w, http.StatusNotFound, "Cast not found")
			return
		}

		utils.Success(w, http.StatusOK, map[string]any{
			"status": "success",
			"genre":  cast.ToResponse(),
		})
	}
}

func handleCreateCast(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name        string `json:"name"`
			BirthDate   string `json:"birth_date"`
			Nationality string `json:"nationality"`
			Gender      string `json:"gender"`
		}

		if err := utils.DecodeBody(r, &req); err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if req.Name == "" {
			utils.Error(w, http.StatusBadRequest, "Name is required")
			return
		}

		if req.BirthDate == "" {
			utils.Error(w, http.StatusBadRequest, "BirthDate is required")
			return
		}

		if req.Nationality == "" {
			utils.Error(w, http.StatusBadRequest, "Nationality is required")
			return
		}

		if req.Gender == "" {
			utils.Error(w, http.StatusBadRequest, "Gender is required")
			return
		}

		castUUID := uuid.New().String()
		now := time.Now()

		cast := models.Cast{
			UUID:        castUUID,
			Name:        req.Name,
			BirthDate:   req.BirthDate,
			Nationality: req.Nationality,
			Gender:      req.Gender,
			CreatedAt:   now,
		}

		coll := database.Collection(client, "cast")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		result, err := coll.InsertOne(ctx, cast)
		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				utils.Error(w, http.StatusConflict, "Cast already exists")
				return
			}
			utils.Error(w, http.StatusInternalServerError, "Failed to create cast")
			return
		}

		utils.Success(w, http.StatusCreated, map[string]any{
			"status": "success",
			"id":     result.InsertedID,
		})
	}
}

func handleUpdateCast(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		objID, err := bson.ObjectIDFromHex(id)
		if err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid cast ID")
			return
		}

		var updates map[string]any
		if err := utils.DecodeBody(r, &updates); err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		updates["updated_at"] = time.Now()
		delete(updates, "_id")
		delete(updates, "id")
		delete(updates, "uuid")

		coll := database.Collection(client, "cast")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		result, err := coll.UpdateOne(ctx, bson.M{"_id": objID}, bson.M{"$set": updates})
		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				utils.Error(w, http.StatusConflict, "Cast already exists")
				return
			}
			utils.Error(w, http.StatusInternalServerError, "Failed to update cast")
			return
		}

		if result.MatchedCount == 0 {
			utils.Error(w, http.StatusNotFound, "Cast not found")
			return
		}

		utils.Success(w, http.StatusOK, map[string]any{
			"status": "success",
			"id":     id,
		})
	}
}
func handleDeleteCast(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		objID, err := bson.ObjectIDFromHex(id)
		if err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid cast ID")
			return
		}

		coll := database.Collection(client, "cast")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		result, err := coll.DeleteOne(ctx, bson.M{"_id": objID})
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to delete cast")
			return
		}

		if result.DeletedCount == 0 {
			utils.Error(w, http.StatusNotFound, "Cast not found")
			return
		}

		utils.Success(w, http.StatusOK, map[string]any{
			"status": "success",
			"id":     id,
		})
	}
}
