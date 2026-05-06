package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	MongoURI       string
	JWTSecret      []byte
	CORSOrigins    []string
	UploadFolder   string
	Port           string
	MaxFileSize    int64
	HLSSegmentTime int
	HLSListSize    int
	HLSSegmentType string

	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
}

func Load() *Config {
	return &Config{
		MongoURI:        getEnv("MONGO_URI", "mongodb://localhost:27017/flixflox"),
		JWTSecret:       []byte(getEnv("JWT_SECRET_KEY", "change-me-in-production")),
		CORSOrigins:     strings.Split(getEnv("CORS_ORIGIN", "http://localhost:5173"), ","),
		UploadFolder:    getEnv("UPLOAD_FOLDER", "./uploads"),
		Port:            getEnv("PORT", "5000"),
		MaxFileSize:     getEnvInt64("MAX_FILE_SIZE", 2<<30), // 2GB
		HLSSegmentTime:  getEnvInt("HLS_SEGMENT_TIME", 10),
		HLSListSize:     getEnvInt("HLS_LIST_SIZE", 0),
		HLSSegmentType:  getEnv("HLS_SEGMENT_TYPE", "fmp4"),
		AccessTokenTTL:  time.Hour * 1,
		RefreshTokenTTL: time.Hour * 24 * 30,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}
