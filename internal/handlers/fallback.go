package handlers

import (
	"net/http"

	"github.com/elvis/flixflox/internal/utils"
	"github.com/go-chi/chi/v5"
)

func RegisterFallback(r chi.Router) {
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		utils.Error(w, http.StatusNotFound, "Not found")
	})

	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		utils.Error(w, http.StatusMethodNotAllowed, "Method not allowed")
	})
}
