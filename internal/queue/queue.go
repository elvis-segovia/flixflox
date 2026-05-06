package queue

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/elvis/flixflox/internal/config"
	"github.com/elvis/flixflox/internal/database"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type JobStatus string

const (
	StatusPending    JobStatus = "pending"
	StatusProcessing JobStatus = "processing"
	StatusCompleted  JobStatus = "completed"
	StatusFailed     JobStatus = "failed"
)

type Job struct {
	UUID          string    `json:"uuid"`
	InputPath     string    `json:"input_path"`
	OutputDir     string    `json:"output_dir"`
	OutputName    string    `json:"output_name"`
	ContentType   string    `json:"content_type"` // "movie" or "tvshow"
	Season        int       `json:"season,omitempty"`
	Episode       int       `json:"episode,omitempty"`
	Status        JobStatus `json:"status"`
	Error         string    `json:"error,omitempty"`
	QueuedAt      time.Time `json:"queued_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

type ConversionQueue struct {
	mu      sync.Mutex
	jobs    []Job
	running bool
	client  *mongo.Client
	cfg     *config.Config
}

func New(client *mongo.Client, cfg *config.Config) *ConversionQueue {
	return &ConversionQueue{
		client: client,
		cfg:    cfg,
	}
}

func (q *ConversionQueue) Add(job Job) {
	q.mu.Lock()
	defer q.mu.Unlock()
	job.Status = StatusPending
	job.QueuedAt = time.Now()
	q.jobs = append(q.jobs, job)
}

func (q *ConversionQueue) Start() {
	q.mu.Lock()
	if q.running {
		q.mu.Unlock()
		return
	}
	q.running = true
	q.mu.Unlock()

	go q.processLoop()
}

func (q *ConversionQueue) Info() map[string]any {
	q.mu.Lock()
	defer q.mu.Unlock()

	pending, processing, completed, failed := 0, 0, 0, 0
	for _, j := range q.jobs {
		switch j.Status {
		case StatusPending:
			pending++
		case StatusProcessing:
			processing++
		case StatusCompleted:
			completed++
		case StatusFailed:
			failed++
		}
	}

	return map[string]any{
		"running":    q.running,
		"total":      len(q.jobs),
		"pending":    pending,
		"processing": processing,
		"completed":  completed,
		"failed":     failed,
		"jobs":       q.jobs,
	}
}

func (q *ConversionQueue) Cleanup() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	var remaining []Job
	removed := 0
	for _, j := range q.jobs {
		if j.Status == StatusCompleted || j.Status == StatusFailed {
			removed++
		} else {
			remaining = append(remaining, j)
		}
	}
	q.jobs = remaining
	return removed
}

func (q *ConversionQueue) processLoop() {
	for {
		q.mu.Lock()
		var job *Job
		for i := range q.jobs {
			if q.jobs[i].Status == StatusPending {
				q.jobs[i].Status = StatusProcessing
				job = &q.jobs[i]
				break
			}
		}
		q.mu.Unlock()

		if job == nil {
			time.Sleep(2 * time.Second)
			continue
		}

		err := q.processJob(job)

		q.mu.Lock()
		now := time.Now()
		for i := range q.jobs {
			if q.jobs[i].UUID == job.UUID {
				if err != nil {
					q.jobs[i].Status = StatusFailed
					q.jobs[i].Error = err.Error()
				} else {
					q.jobs[i].Status = StatusCompleted
				}
				q.jobs[i].CompletedAt = &now
				break
			}
		}
		q.mu.Unlock()
	}
}

func (q *ConversionQueue) processJob(job *Job) error {
	log.Printf("Processing conversion for %s: %s", job.UUID, job.InputPath)

	if err := os.MkdirAll(job.OutputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}

	// Generate thumbnail
	thumbnailPath := filepath.Join(job.OutputDir, "thumbnail.jpg")
	thumbCmd := exec.Command("ffmpeg", "-y",
		"-i", job.InputPath,
		"-ss", "00:00:05",
		"-vframes", "1",
		thumbnailPath,
	)
	if out, err := thumbCmd.CombinedOutput(); err != nil {
		log.Printf("Thumbnail generation warning: %v, output: %s", err, string(out))
	}

	// Convert to HLS
	outputPath := filepath.Join(job.OutputDir, job.OutputName+".m3u8")
	segmentTime := fmt.Sprintf("%d", q.cfg.HLSSegmentTime)
	listSize := fmt.Sprintf("%d", q.cfg.HLSListSize)

	args := []string{
		"-y",
		"-i", job.InputPath,
		"-c:v", "libx264",
		"-c:a", "aac",
		"-preset", "fast",
		"-crf", "23",
		"-f", "hls",
		"-hls_time", segmentTime,
		"-hls_list_size", listSize,
		"-hls_segment_type", q.cfg.HLSSegmentType,
		"-hls_flags", "independent_segments",
		"-hls_segment_filename", filepath.Join(job.OutputDir, job.OutputName+"_%03d."+q.segmentExt()),
		outputPath,
	}

	cmd := exec.Command("ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg conversion failed: %w, output: %s", err, string(out))
	}

	// Remove original file
	os.Remove(job.InputPath)

	// Update catalog status
	q.updateCatalogStatus(job)

	log.Printf("Conversion completed for %s", job.UUID)
	return nil
}

func (q *ConversionQueue) segmentExt() string {
	if q.cfg.HLSSegmentType == "fmp4" {
		return "m4s"
	}
	return "ts"
}

func (q *ConversionQueue) updateCatalogStatus(job *Job) {
	coll := database.Collection(q.client, "catalog")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hlsPath := filepath.Join(job.OutputDir, job.OutputName+".m3u8")

	if job.ContentType == "movie" {
		coll.UpdateOne(ctx,
			bson.M{"uuid": job.UUID},
			bson.M{"$set": bson.M{
				"status":     "Ready",
				"file_path":  hlsPath,
				"updated_at": time.Now(),
			}},
		)
	} else {
		// TV show episode update
		coll.UpdateOne(ctx,
			bson.M{
				"uuid":                              job.UUID,
				"seasons.season_number":              job.Season,
				"seasons.episodes.episode_number":    job.Episode,
			},
			bson.M{"$set": bson.M{
				"seasons.$[s].episodes.$[e].status":    "Ready",
				"seasons.$[s].episodes.$[e].file_path": hlsPath,
				"updated_at":                            time.Now(),
			}},
			// Array filters would need to be passed as options
		)
	}
}
