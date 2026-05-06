package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/elvis/flixflox/internal/database"
	"github.com/elvis/flixflox/internal/utils"
	"github.com/go-chi/chi/v5"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func RegisterHealthRoutes(r chi.Router, client *mongo.Client) {
	r.Get("/healthz", handleHealth(client))
	r.Get("/healthz/ping", handlePing())
	r.Get("/healthz/ready", handleReady(client))
}

func handleHealth(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		status := "healthy"
		code := http.StatusOK
		if err := client.Ping(ctx, nil); err != nil {
			status = "unhealthy"
			code = http.StatusServiceUnavailable
		}

		utils.JSON(w, code, map[string]any{
			"status":  status,
			"message": "FlixFlox API",
		})
	}
}

func handlePing() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		utils.JSON(w, http.StatusOK, map[string]string{
			"message": "pong",
		})
	}
}

func handleReady(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		_ = database.Collection(client, "users")

		status := "ready"
		code := http.StatusOK
		if err := client.Ping(ctx, nil); err != nil {
			status = "not ready"
			code = http.StatusServiceUnavailable
		}

		utils.JSON(w, code, map[string]any{
			"status":    status,
			"message":   "FlixFlox API",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	}
}
