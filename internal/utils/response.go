package utils

import (
	"encoding/json"
	"net/http"
)

type APIResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

func JSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func Error(w http.ResponseWriter, status int, message string) {
	JSON(w, status, APIResponse{Status: "failed", Message: message})
}

func Success(w http.ResponseWriter, status int, data any) {
	JSON(w, status, data)
}

func DecodeBody(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
