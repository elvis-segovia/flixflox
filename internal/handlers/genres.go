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

func RegisterGenresRoutes(r chi.Router, client *mongo.Client, cfg *config.Config) {
	r.Group(func(r chi.Router) {
		r.Use(middleware.JWTAuth(cfg.JWTSecret, client))
		r.Get("/v1/api/genres", handleListGenres(client))
		r.Get("/v1/api/genres/{id}", handleGetGenre(client))
		r.Post("/v1/api/genres", handleCreateGenre(client))
		r.Put("/v1/api/genres/{id}", handleUpdateGenre(client))
		r.Delete("/v1/api/genres/{id}", handleDeleteGenre(client))
	})
}

func handleListGenres(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		coll := database.Collection(client, "genres")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		cursor, err := coll.Find(ctx, bson.M{})
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to fetch genres")
			return
		}
		defer cursor.Close(ctx)

		var genres []models.Genre
		if err := cursor.All(ctx, &genres); err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to decode genres")
			return
		}

		utils.Success(w, http.StatusOK, genres)
	}
}

func handleGetGenre(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		objID, err := bson.ObjectIDFromHex(id)
		if err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid genre ID")
			return
		}

		coll := database.Collection(client, "genres")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		var genres models.Genre
		if err := coll.FindOne(ctx, bson.M{"_id": objID}).Decode(&genres); err != nil {
			utils.Error(w, http.StatusNotFound, "Genre not found")
			return
		}

		utils.Success(w, http.StatusOK, map[string]any{
			"status": "success",
			"genre":  genres.ToResponse(),
		})
	}
}

func handleCreateGenre(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Genre string `json:"genre"`
		}
		if err := utils.DecodeBody(r, &req); err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if req.Genre == "" {
			utils.Error(w, http.StatusBadRequest, "Genre is required")
			return
		}

		genreUUID := uuid.New().String()
		now := time.Now()

		genre := models.Genre{
			UUID:      genreUUID,
			Genre:     req.Genre,
			CreatedAt: now,
		}

		coll := database.Collection(client, "genres")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		result, err := coll.InsertOne(ctx, genre)
		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				utils.Error(w, http.StatusConflict, "Genre already exists")
				return
			}
			utils.Error(w, http.StatusInternalServerError, "Failed to create genre")
			return
		}

		utils.Success(w, http.StatusCreated, map[string]any{
			"status": "success",
			"id":     result.InsertedID,
		})
	}
}

func handleUpdateGenre(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		objID, err := bson.ObjectIDFromHex(id)
		if err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid genre ID")
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

		coll := database.Collection(client, "genres")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		result, err := coll.UpdateOne(ctx, bson.M{"_id": objID}, bson.M{"$set": updates})
		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				utils.Error(w, http.StatusConflict, "Genre already exists")
				return
			}
			utils.Error(w, http.StatusInternalServerError, "Failed to update genre")
			return
		}

		if result.MatchedCount == 0 {
			utils.Error(w, http.StatusNotFound, "Genre not found")
			return
		}

		utils.Success(w, http.StatusOK, map[string]any{
			"status": "success",
			"id":     id,
		})
	}
}
func handleDeleteGenre(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		objID, err := bson.ObjectIDFromHex(id)
		if err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid genre ID")
			return
		}

		coll := database.Collection(client, "genres")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		result, err := coll.DeleteOne(ctx, bson.M{"_id": objID})
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to delete genre")
			return
		}

		if result.DeletedCount == 0 {
			utils.Error(w, http.StatusNotFound, "Genre not found")
			return
		}

		utils.Success(w, http.StatusOK, map[string]any{
			"status": "success",
			"id":     id,
		})
	}
}
