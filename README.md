# FlixFlox

Self-hosted video streaming API written in Go. It ingests source video files, transcodes them to HLS via FFmpeg in a background worker queue, and serves the resulting catalog (movies and TV shows) to authenticated users with per-account viewer profiles.

## Features

- JWT-based authentication with access/refresh tokens delivered as HTTP-only cookies
- User and viewer-profile management (Netflix-style multi-profile per account)
- Movie and TV show catalog with seasons, episodes and rich metadata
- Multipart upload for source files with automatic FFmpeg-driven HLS conversion
- Background conversion queue with status, start and cleanup endpoints
- HLS streaming endpoint serving `.m3u8`, `.ts`, `.m4s` and `.mp4`
- Health, ping and readiness probes
- MongoDB persistence

## Tech stack

- Go 1.26
- [chi](https://github.com/go-chi/chi) router
- MongoDB (driver v2)
- [golang-jwt](https://github.com/golang-jwt/jwt) for JWT
- FFmpeg (system binary, used by the conversion worker)

## Project layout

```
cmd/server/          Application entrypoint
internal/config/     Environment configuration loader
internal/database/   MongoDB client and index setup
internal/handlers/   HTTP handlers (auth, users, viewers, videos, health)
internal/middleware/ JWT auth, refresh and CORS middleware
internal/models/     Domain models (User, Viewer, CatalogItem, ...)
internal/queue/      FFmpeg conversion queue
internal/utils/      JSON helpers, password hashing, etc.
uploads/             Default upload + HLS output directory
swagger.yaml         OpenAPI 3.0 specification
```

## Configuration

Configuration is loaded from environment variables. A starting point is provided in `.env.example`.

| Variable           | Default                                  | Description                                      |
| ------------------ | ---------------------------------------- | ------------------------------------------------ |
| `MONGO_URI`        | `mongodb://localhost:27017/flixflox`     | MongoDB connection string                        |
| `JWT_SECRET_KEY`   | `change-me-in-production`                | Secret used to sign JWTs                         |
| `CORS_ORIGIN`      | `http://localhost:5173`                  | Allowed CORS origin(s), comma-separated          |
| `UPLOAD_FOLDER`    | `./uploads`                              | Directory for uploaded files and HLS output      |
| `PORT`             | `5000`                                   | HTTP listen port                                 |
| `HLS_SEGMENT_TIME` | `10`                                     | HLS segment duration in seconds                  |
| `HLS_LIST_SIZE`    | `0`                                      | HLS playlist size (0 = unlimited)                |
| `HLS_SEGMENT_TYPE` | `fmp4`                                   | HLS segment type (`fmp4` or `mpegts`)            |
| `MAX_FILE_SIZE`    | `2147483648`                             | Max upload size in bytes (default 2 GiB)         |

## Running locally

Prerequisites: Go 1.26+, MongoDB, FFmpeg available on `PATH`.

```bash
cp .env.example .env
go mod download
go run ./cmd/server
```

The API will listen on `http://localhost:5000`.

## Running with Docker

```bash
docker compose up --build
```

This starts both the API container and a MongoDB 8 instance, with persistent volumes for uploads and database data.

## API

All routes are mounted under the `/v1/api` prefix, except for the health probes which live under `/healthz`.

| Group     | Prefix                | Auth                     |
| --------- | --------------------- | ------------------------ |
| Health    | `/healthz`            | Public                   |
| Auth      | `/v1/api/auth`        | Mixed (login/register public, others require JWT) |
| Users     | `/v1/api/users`       | JWT                      |
| Viewers   | `/v1/api/viewers`     | JWT, scoped to caller    |
| Videos    | `/v1/api/videos`      | Read public, write JWT   |

The full machine-readable contract is in [`swagger.yaml`](./swagger.yaml). Open it with any OpenAPI viewer (Swagger UI, Redoc, Stoplight, etc.) to explore endpoints, request bodies and response schemas.

### Authentication

`POST /v1/api/auth/login` and `POST /v1/api/auth/register` are public. They set `access_token` and `refresh_token` HTTP-only cookies and also return the tokens in the JSON body so non-browser clients can use the `Authorization: Bearer <token>` header.

`POST /v1/api/auth/token/refresh` requires a valid refresh token. Logout (`DELETE /v1/api/auth/logout`) blacklists the current token's `jti`.

### Streaming

`GET /v1/api/videos/stream/<path>` serves files from inside `UPLOAD_FOLDER`. The handler sets the appropriate `Content-Type` for HLS playlists and segments and rejects paths that escape the upload directory.

### Uploads and conversion

`POST /v1/api/videos/upload` accepts a `multipart/form-data` body with:

- `file` — the source video (`.mp4`, `.avi`, `.flv`, `.mkv`, `.mov`, `.wmv`, `.webm`)
- `type` — `movie` or `tvshow`
- `values` — JSON string with title, release year, rating, description, genre, cast
- `metadata` — JSON string with `season`, `episode`, `title` (TV shows only)

The file is saved to disk, a catalog entry is created, and a job is pushed onto the conversion queue. Adding more episodes to an existing TV show uses `PUT /v1/api/videos/{id}/new-episode`.

Queue control endpoints:

- `GET  /v1/api/videos/queue/info` — current queue snapshot
- `POST /v1/api/videos/queue/start` — kick the worker if idle
- `POST /v1/api/videos/queue/cleanup` — drop completed/failed jobs

## License

Unspecified — add a `LICENSE` file before publishing.
