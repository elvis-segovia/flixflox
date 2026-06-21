package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/elvis/flixflox/internal/config"
	"github.com/elvis/flixflox/internal/database"
	"github.com/elvis/flixflox/internal/middleware"
	"github.com/elvis/flixflox/internal/models"
	"github.com/elvis/flixflox/internal/queue"
	"github.com/elvis/flixflox/internal/utils"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var allowedExtensions = map[string]bool{
	".mp4": true, ".avi": true, ".flv": true, ".mkv": true,
	".mov": true, ".wmv": true, ".webm": true,
}

func RegisterVideoRoutes(r chi.Router, client *mongo.Client, cfg *config.Config, q *queue.ConversionQueue) {
	// Public routes
	r.Get("/v1/api/videos", handleListVideos(client))
	r.Get("/v1/api/videos/{vtype}/list", handleListVideosByType(client))
	r.Get("/v1/api/videos/{id}/details", handleGetVideoDetails(client))
	r.Get("/v1/api/videos/{id}/season/{season}", handleGetSeason(client))
	r.Get("/v1/api/videos/image/*", handleBgImage(cfg))
	r.Get("/v1/api/videos/stream/*", handleStream(cfg))
	r.Get("/v1/api/videos/queue/info", handleQueueInfo(q))

	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(middleware.JWTAuth(cfg.JWTSecret, client))
		r.Post("/v1/api/videos", handleCreateVideo(client))
		r.Post("/v1/api/videos/upload", handleUploadVideo(client, cfg, q))
		r.Put("/v1/api/videos/{id}/new-episode", handleAddEpisode(client, cfg, q))
		r.Post("/v1/api/videos/queue/start", handleQueueStart(q))
		r.Post("/v1/api/videos/queue/cleanup", handleQueueCleanup(q))
	})
}

func handleListVideos(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		coll := database.Collection(client, "catalog")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		cursor, err := coll.Find(ctx, bson.M{})
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to fetch videos")
			return
		}
		defer cursor.Close(ctx)

		var items []models.CatalogItem
		if err := cursor.All(ctx, &items); err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to decode videos")
			return
		}

		utils.Success(w, http.StatusOK, items)
	}
}

func handleListVideosByType(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vtype := chi.URLParam(r, "vtype")
		if vtype != "movie" && vtype != "tvshow" {
			utils.Error(w, http.StatusBadRequest, "Type must be 'movie' or 'tvshow'")
			return
		}

		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		if perPage < 1 || perPage > 100 {
			perPage = 20
		}

		coll := database.Collection(client, "catalog")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		filter := bson.M{"type": vtype}
		total, err := coll.CountDocuments(ctx, filter)
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to count videos")
			return
		}

		skip := int64((page - 1) * perPage)
		limit := int64(perPage)
		cursor, err := coll.Find(ctx, filter, options.Find().SetSkip(skip).SetLimit(limit).SetSort(bson.D{{Key: "created_at", Value: -1}}))
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to fetch videos")
			return
		}
		defer cursor.Close(ctx)

		var items []models.CatalogItem
		if err := cursor.All(ctx, &items); err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to decode videos")
			return
		}

		totalPages := (total + int64(perPage) - 1) / int64(perPage)

		utils.Success(w, http.StatusOK, map[string]any{
			"status": "success",
			"data":   items,
			"pagination": models.Pagination{
				Page:       page,
				PerPage:    perPage,
				Total:      total,
				TotalPages: totalPages,
			},
		})
	}
}

func handleGetVideoDetails(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentID := chi.URLParam(r, "id")

		coll := database.Collection(client, "catalog")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		var item models.CatalogItem
		err := coll.FindOne(ctx, bson.M{"uuid": contentID}).Decode(&item)
		if err != nil {
			utils.Error(w, http.StatusNotFound, "Content not found")
			return
		}

		utils.Success(w, http.StatusOK, item)
	}
}

func handleGetSeason(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentID := chi.URLParam(r, "id")
		seasonNum, err := strconv.Atoi(chi.URLParam(r, "season"))
		if err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid season number")
			return
		}

		coll := database.Collection(client, "catalog")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		var item models.CatalogItem
		if err := coll.FindOne(ctx, bson.M{"uuid": contentID}).Decode(&item); err != nil {
			utils.Error(w, http.StatusNotFound, "Content not found")
			return
		}

		for _, s := range item.Seasons {
			if s.SeasonNumber == seasonNum {
				utils.Success(w, http.StatusOK, s.Episodes)
				return
			}
		}

		utils.Error(w, http.StatusNotFound, "Season not found")
	}
}

func handleStream(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filePath := chi.URLParam(r, "*")
		// Prevent directory traversal
		if !strings.HasPrefix(filepath.Clean(filePath), filepath.Clean(cfg.UploadFolder)) {
			utils.Error(w, http.StatusBadRequest, "Invalid file path")
			return
		}

		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			utils.Error(w, http.StatusNotFound, "File not found")
			return
		}

		ext := strings.ToLower(filepath.Ext(filePath))
		switch ext {
		case ".m3u8":
			w.Header().Set("Content-Type", "application/x-mpegURL")
		case ".ts":
			w.Header().Set("Content-Type", "video/MP2T")
		case ".m4s":
			w.Header().Set("Content-Type", "video/iso.segment")
		case ".mp4":
			w.Header().Set("Content-Type", "video/mp4")
		}

		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, filePath)
	}
}

func handleBgImage(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filePath := chi.URLParam(r, "*")
		// Prevent directory traversal
		if !strings.HasPrefix(filepath.Clean(filePath), filepath.Clean(cfg.UploadFolder)) {
			utils.Error(w, http.StatusBadRequest, "Invalid file path")
			return
		}

		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			utils.Error(w, http.StatusNotFound, "File not found")
			return
		}

		ext := strings.ToLower(filepath.Ext(filePath))
		switch ext {
		case ".jpg":
			w.Header().Set("Content-Type", "image/jpeg")
		case ".png":
			w.Header().Set("Content-Type", "image/png")
		case ".jpeg":
			w.Header().Set("Content-Type", "image/jpeg")
		}

		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, filePath)
	}
}

func handleCreateVideo(client *mongo.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var item models.CatalogItem
		if err := utils.DecodeBody(r, &item); err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		if item.Title == "" || item.Type == "" {
			utils.Error(w, http.StatusBadRequest, "Title and type are required")
			return
		}

		now := time.Now()
		item.UUID = uuid.New().String()
		item.Status = "In-Progress"
		item.CreatedAt = now
		item.UpdatedAt = now

		coll := database.Collection(client, "catalog")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		result, err := coll.InsertOne(ctx, item)
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to create content")
			return
		}

		utils.Success(w, http.StatusCreated, map[string]any{
			"status": "success",
			"id":     result.InsertedID,
		})
	}
}

func handleUploadVideo(client *mongo.Client, cfg *config.Config, q *queue.ConversionQueue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(cfg.MaxFileSize); err != nil {
			utils.Error(w, http.StatusBadRequest, "File too large or invalid form data")
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			utils.Error(w, http.StatusBadRequest, "File is required")
			return
		}
		defer file.Close()

		const maxUploadSize = 2 * 1024 * 1024 // 5MB
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
		if err := r.ParseMultipartForm(maxUploadSize); err != nil {
			http.Error(w, "File size exceeds the 2MB limit", http.StatusBadRequest)
			return
		}

		image, poster, err := r.FormFile("bg_image")
		if err != nil {
			http.Error(w, "Invalid file or missing 'image' key", http.StatusBadRequest)
			return
		}
		defer image.Close()

		buff := make([]byte, 512)
		if _, err := image.Read(buff); err != nil {
			http.Error(w, "Failed to read file headers", http.StatusInternalServerError)
			return
		}

		if _, err := image.Seek(0, io.SeekStart); err != nil {
			http.Error(w, "Failed to reset file pointer", http.StatusInternalServerError)
			return
		}

		fileType := http.DetectContentType(buff)
		if fileType != "image/jpeg" && fileType != "image/png" && fileType != "image/jpg" {
			http.Error(w, "Unsupported file format. Please upload JPEG, PNG, or JPG.", http.StatusBadRequest)
			return
		}

		ext := strings.ToLower(filepath.Ext(header.Filename))
		if !allowedExtensions[ext] {
			utils.Error(w, http.StatusBadRequest, fmt.Sprintf("File type %s not allowed", ext))
			return
		}

		contentType := r.FormValue("type")
		if contentType != "movie" && contentType != "tvshow" {
			utils.Error(w, http.StatusBadRequest, "Type must be 'movie' or 'tvshow'")
			return
		}

		var values map[string]any
		if err := json.Unmarshal([]byte(r.FormValue("values")), &values); err != nil {
			utils.Error(w, http.StatusBadRequest, "Invalid values JSON")
			return
		}

		title, _ := values["title"].(string)
		if title == "" {
			utils.Error(w, http.StatusBadRequest, "Title is required in values")
			return
		}

		contentUUID := uuid.New().String()
		now := time.Now()
		safeTitle := sanitizeFilename(title)
		dstPath := filepath.Join(cfg.UploadFolder, safeTitle, filepath.Base(poster.Filename))

		var uploadDir, outputName string
		var season, episode int

		item := models.CatalogItem{
			UUID:      contentUUID,
			Title:     title,
			Type:      contentType,
			Status:    "In-Progress",
			BGImage:   dstPath,
			CreatedAt: now,
			UpdatedAt: now,
		}

		if ry, ok := values["release_year"].(float64); ok {
			item.ReleaseYear = int(ry)
		}
		if rating, ok := values["rating"].(float64); ok {
			item.Rating = rating
		}
		if desc, ok := values["description"].(string); ok {
			item.Description = desc
		}
		if genres, ok := values["genre"].([]any); ok {
			for _, g := range genres {
				if s, ok := g.(string); ok {
					item.Genre = append(item.Genre, s)
				}
			}
		}
		if cast, ok := values["cast"].([]any); ok {
			for _, c := range cast {
				if s, ok := c.(string); ok {
					item.Cast = append(item.Cast, s)
				}
			}
		}

		if contentType == "movie" {
			uploadDir = filepath.Join(cfg.UploadFolder, safeTitle)
			outputName = safeTitle
		} else {
			var metadata struct {
				Season  int    `json:"season"`
				Episode int    `json:"episode"`
				Title   string `json:"title"`
			}
			if metaStr := r.FormValue("metadata"); metaStr != "" {
				json.Unmarshal([]byte(metaStr), &metadata)
			}
			if metadata.Season == 0 {
				metadata.Season = 1
			}
			if metadata.Episode == 0 {
				metadata.Episode = 1
			}

			season = metadata.Season
			episode = metadata.Episode

			uploadDir = filepath.Join(cfg.UploadFolder, safeTitle,
				fmt.Sprintf("S%02d", metadata.Season),
				fmt.Sprintf("E%02d", metadata.Episode))
			outputName = fmt.Sprintf("%s_S%02dE%02d", safeTitle, metadata.Season, metadata.Episode)

			item.Seasons = []models.Season{{
				SeasonNumber: metadata.Season,
				Episodes: []models.Episode{{
					EpisodeNumber: metadata.Episode,
					Title:         metadata.Title,
					Status:        "In-Progress",
				}},
			}}
		}

		if err := os.MkdirAll(uploadDir, 0755); err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to create upload directory")
			return
		}

		dst, err := os.Create(dstPath)
		if err != nil {
			http.Error(w, "Unable to save file locally", http.StatusInternalServerError)
			return
		}
		defer dst.Close()

		if _, err := io.Copy(dst, image); err != nil {
			http.Error(w, "Error saving file content", http.StatusInternalServerError)
			return
		}

		inputPath := filepath.Join(uploadDir, header.Filename)
		dst, err = os.Create(inputPath)
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to save file")
			return
		}

		buf := make([]byte, 32*1024)
		for {
			n, readErr := file.Read(buf)
			if n > 0 {
				if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
					dst.Close()
					utils.Error(w, http.StatusInternalServerError, "Failed to write file")
					return
				}
			}
			if readErr != nil {
				break
			}
		}
		dst.Close()

		coll := database.Collection(client, "catalog")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		result, err := coll.InsertOne(ctx, item)
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to create catalog entry")
			return
		}

		q.Add(queue.Job{
			UUID:        contentUUID,
			InputPath:   inputPath,
			OutputDir:   uploadDir,
			OutputName:  outputName,
			ContentType: contentType,
			Season:      season,
			Episode:     episode,
		})
		q.Start()

		utils.Success(w, http.StatusCreated, map[string]any{
			"status": "success",
			"id":     result.InsertedID,
		})
	}
}

func handleAddEpisode(client *mongo.Client, cfg *config.Config, q *queue.ConversionQueue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentUUID := chi.URLParam(r, "id")

		if err := r.ParseMultipartForm(cfg.MaxFileSize); err != nil {
			utils.Error(w, http.StatusBadRequest, "File too large or invalid form data")
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			utils.Error(w, http.StatusBadRequest, "File is required")
			return
		}
		defer file.Close()

		ext := strings.ToLower(filepath.Ext(header.Filename))
		if !allowedExtensions[ext] {
			utils.Error(w, http.StatusBadRequest, fmt.Sprintf("File type %s not allowed", ext))
			return
		}

		var metadata struct {
			Season  int    `json:"season"`
			Episode int    `json:"episode"`
			Title   string `json:"title"`
		}
		if metaStr := r.FormValue("metadata"); metaStr != "" {
			json.Unmarshal([]byte(metaStr), &metadata)
		}

		coll := database.Collection(client, "catalog")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		var item models.CatalogItem
		if err := coll.FindOne(ctx, bson.M{"uuid": contentUUID}).Decode(&item); err != nil {
			utils.Error(w, http.StatusNotFound, "Content not found")
			return
		}

		if item.Type != "tvshow" {
			utils.Error(w, http.StatusBadRequest, "Content is not a TV show")
			return
		}

		safeTitle := sanitizeFilename(item.Title)
		uploadDir := filepath.Join(cfg.UploadFolder, safeTitle,
			fmt.Sprintf("S%02d", metadata.Season),
			fmt.Sprintf("E%02d", metadata.Episode))
		outputName := fmt.Sprintf("%s_S%02dE%02d", safeTitle, metadata.Season, metadata.Episode)

		if err := os.MkdirAll(uploadDir, 0755); err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to create directory")
			return
		}

		inputPath := filepath.Join(uploadDir, header.Filename)
		dst, err := os.Create(inputPath)
		if err != nil {
			utils.Error(w, http.StatusInternalServerError, "Failed to save file")
			return
		}

		buf := make([]byte, 32*1024)
		for {
			n, readErr := file.Read(buf)
			if n > 0 {
				if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
					dst.Close()
					utils.Error(w, http.StatusInternalServerError, "Failed to write file")
					return
				}
			}
			if readErr != nil {
				break
			}
		}
		dst.Close()

		newEpisode := models.Episode{
			EpisodeNumber: metadata.Episode,
			Title:         metadata.Title,
			Status:        "In-Progress",
		}

		seasonExists := false
		for _, s := range item.Seasons {
			if s.SeasonNumber == metadata.Season {
				seasonExists = true
				break
			}
		}

		if seasonExists {
			coll.UpdateOne(ctx,
				bson.M{"uuid": contentUUID},
				bson.M{
					"$push": bson.M{"seasons.$[s].episodes": newEpisode},
					"$set":  bson.M{"updated_at": time.Now()},
				},
				options.UpdateOne().SetArrayFilters([]interface{}{
					bson.M{"s.season_number": metadata.Season},
				}),
			)
		} else {
			coll.UpdateOne(ctx,
				bson.M{"uuid": contentUUID},
				bson.M{
					"$push": bson.M{"seasons": models.Season{
						SeasonNumber: metadata.Season,
						Episodes:     []models.Episode{newEpisode},
					}},
					"$set": bson.M{"updated_at": time.Now()},
				},
			)
		}

		q.Add(queue.Job{
			UUID:        contentUUID,
			InputPath:   inputPath,
			OutputDir:   uploadDir,
			OutputName:  outputName,
			ContentType: "tvshow",
			Season:      metadata.Season,
			Episode:     metadata.Episode,
		})
		q.Start()

		utils.Success(w, http.StatusOK, map[string]any{
			"status":  "success",
			"message": "Episode queued for processing",
		})
	}
}

func handleQueueInfo(q *queue.ConversionQueue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		utils.Success(w, http.StatusOK, q.Info())
	}
}

func handleQueueStart(q *queue.ConversionQueue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q.Start()
		utils.Success(w, http.StatusOK, map[string]any{
			"status":  "success",
			"message": "Queue processor started",
		})
	}
}

func handleQueueCleanup(q *queue.ConversionQueue) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		removed := q.Cleanup()
		utils.Success(w, http.StatusOK, map[string]any{
			"status":  "success",
			"removed": removed,
		})
	}
}

func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer(
		" ", "_", "/", "_", "\\", "_", ":", "_",
		"*", "_", "?", "_", "\"", "_", "<", "_",
		">", "_", "|", "_",
	)
	return strings.ToLower(replacer.Replace(name))
}
