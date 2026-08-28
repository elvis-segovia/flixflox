package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const minJWTSecretLen = 32

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

func Load() (*Config, error) {
	jwtKey := os.Getenv("JWT_SECRET_KEY")
	if jwtKey == "" {
		return nil, fmt.Errorf("JWT_SECRET_KEY environment variable must be set")
	}
	if len(jwtKey) < minJWTSecretLen {
		return nil, fmt.Errorf("JWT_SECRET_KEY must be at least %d bytes (got %d); generate one with: openssl rand -base64 48", minJWTSecretLen, len(jwtKey))
	}

	return &Config{
		MongoURI:        getEnv("MONGO_URI", "mongodb://localhost:27017/flixflox"),
		JWTSecret:       []byte(jwtKey),
		CORSOrigins:     strings.Split(getEnv("CORS_ORIGIN", "http://localhost:5173"), ","),
		UploadFolder:    getEnv("UPLOAD_FOLDER", "./uploads"),
		Port:            getEnv("PORT", "7777"),
		MaxFileSize:     getEnvInt64("MAX_FILE_SIZE", 2<<30), // 2GB
		HLSSegmentTime:  getEnvInt("HLS_SEGMENT_TIME", 10),
		HLSListSize:     getEnvInt("HLS_LIST_SIZE", 0),
		HLSSegmentType:  getEnv("HLS_SEGMENT_TYPE", "fmp4"),
		AccessTokenTTL:  time.Hour * 1,
		RefreshTokenTTL: time.Hour * 24 * 30,
	}, nil
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
