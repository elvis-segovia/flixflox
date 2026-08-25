# Roadmap — All-in-one image (UI + API)

**Goal:** one container image, one port, one `docker run` — the Go API and the React admin/player UI
served from the same origin, configured entirely by environment variables. Today FlixFlox ships as two
images from two repositories (`flixflox` here, `flixflox-ui` in [`streamadmin`](https://github.com/elvus/streamadmin)),
which means two deployments, two ingress hosts, a CORS allowlist to keep in sync, and a version skew
nobody tracks.

Status: proposal / not implemented. Owner: @elvus.

**Scope note:** "all-in-one" here means *UI + API*. MongoDB stays a separate service — see §9.

---

## 1. The shape decision

Three ways to put two things in one image:

| Option | Processes | Notes |
| --- | --- | --- |
| **A. Go serves the SPA** (`embed.FS` + static handler) | 1 | The API already owns every path it needs (`/v1/api/*`, `/healthz*`); everything else is the SPA. No process supervisor, no nginx config, one port, one log stream, one `PID 1` that handles signals correctly. Adds ~8 MB (the built `dist`) to the image. |
| B. nginx + Go under s6-overlay/supervisord | 2 | Keeps `config/default.conf` from the UI repo as-is. Costs a supervisor, a second log stream, signal-forwarding subtleties, and a health check that has to cover both. Buys nothing the Go stdlib can't do. |
| C. A `docker-compose.yaml` that ships both images | 2 containers | Not an image. Worth publishing anyway as the "I want to scale them separately" path, but it does not answer the ask. |

**Decision: A.** The routing table makes it nearly free — every server route is under `/v1/api/` or
`/healthz`, and every UI route (`/login`, `/web/...`, `/dashboard/...`) is under none of them. There is
no prefix collision to design around, so the SPA fallback is a `NotFound` handler and nothing more.

```
                     ┌──────────────── flixflox:all-in-one ────────────────┐
                     │                                                     │
  browser ──:5000──▶ │  chi router                                         │
                     │   ├─ /healthz, /healthz/ready      → handlers       │
                     │   ├─ /v1/api/*                     → handlers       │
                     │   │    └─ /v1/api/videos/stream/*  → HLS off disk   │
                     │   ├─ /config.js                    → generated from │
                     │   │                                   env at runtime│
                     │   ├─ /assets/*  (hashed)           → embed.FS       │
                     │   └─ *                             → index.html     │
                     │                                                     │
                     │  ffmpeg (conversion queue)   /app/uploads (volume)  │
                     └─────────────────────────────────────────────────────┘
                                          │
                                          ▼  MONGO_URI
                                    mongo (separate)
```

---

## 2. Phase 0 — Prerequisites in existing code (blocking)

### 0.1 Serve the SPA without swallowing the API — `cmd/server/main.go`

Routes are registered as full paths on the root router (`r.Get("/v1/api/videos", …)`), not inside an
`r.Route("/v1/api", …)` group, so there is no sub-router to hang a JSON 404 off. The fallback has to
discriminate by prefix itself:

```go
r.NotFound(func(w http.ResponseWriter, req *http.Request) {
    if strings.HasPrefix(req.URL.Path, "/v1/api/") || strings.HasPrefix(req.URL.Path, "/healthz") {
        writeJSONError(w, http.StatusNotFound, "not found") // never index.html
        return
    }
    spa.ServeHTTP(w, req)
})
r.MethodNotAllowed(...) // keep JSON for the API namespace here too
```

Three rules the SPA handler must follow, or debugging gets nasty:

- **A missing asset is a 404, not `index.html`.** If `/assets/index-a1b2c3.js` returns the HTML shell
  with `Content-Type: text/html`, the browser reports a MIME-type module error and you go looking in
  the wrong place. Only fall back for `GET`/`HEAD` requests whose `Accept` contains `text/html`.
- **`index.html` and `config.js` are `no-store`; hashed assets are `immutable`.** Vite already
  content-hashes everything in `assets/`, so `Cache-Control: public, max-age=31536000, immutable` is
  safe there and mandatory for the shell to be *un*cached — otherwise a redeploy serves a stale
  `index.html` pointing at assets that no longer exist.
- **Don't let the static handler see `/uploads`.** HLS is served by `handleStream`
  (`internal/handlers/videos.go:40`) with its own path validation. The embedded FS contains only
  `dist`, so this is free — but it stops being free the moment someone adds a `http.Dir("./")`.

### 0.2 Serve the runtime config from Go — replaces `docker-entrypoint.sh`

The UI reads `window._env_` from a `config.js` that the nginx image regenerates at startup
(`streamadmin/docker-entrypoint.sh` → `/docker-entrypoint.d/40-runtime-config.sh`). That whole
mechanism collapses into one handler:

```go
r.Get("/config.js", handleRuntimeConfig(cfg)) // Content-Type: application/javascript, no-store
```

emitting the same four keys the UI expects (`src/env.ts`): `VITE_STREAMAPI_URL`,
`VITE_STREAMAPI_PREFIX`, `VITE_STREAMAPI_PREFIX_ADMIN`, `VITE_DEFAULT_NEXT_EPISODE_OFFSET`.

The important value is **`VITE_STREAMAPI_URL=""`**. Same-origin means the axios `baseURL` becomes the
relative `/v1/api`, and the UI stops caring what hostname it was served from — no rebuild per
environment, no `COPY .env.production .env` at image build time (`streamadmin/dockerfile`), no
`CORS_ORIGIN` to maintain. This works *only* because `env.ts` uses `??`:

```ts
return runtime[key] ?? import.meta.env[key] ?? '';
```

`""` is not nullish, so an empty runtime value is preserved. Had that been `||`, an empty string would
silently fall through to the build-time value and the UI would call the wrong host. Add a unit test
pinning that behaviour before anything else depends on it.

**Escape hatch:** keep the values overridable. Someone fronting the API with a CDN, or serving media
from a different host, needs to set `VITE_STREAMAPI_URL` explicitly — default to `""`, don't hardcode it.

### 0.3 Fix the server read timeout — `cmd/server/main.go:56`

Already flagged in [`TELEGRAM_INGEST_ROADMAP.md`](TELEGRAM_INGEST_ROADMAP.md) §0.3, and the all-in-one
image makes it **worse, not equal**. Today nginx sits in front of the UI with `client_max_body_size 2048M`
and its own generous timeouts; in the merged image the Go server is the only thing between a browser
and a 2 GB upload. `ReadTimeout: 30 * time.Second` bounds reading the entire request body, so every
real upload dies mid-transfer. Use `ReadHeaderTimeout: 30s` with a large-or-zero `ReadTimeout`, or
per-route deadlines via `http.ResponseController`.

While you're there: `WriteTimeout: 300 * time.Second` now also applies to HLS segment responses and to
the SPA. It's generous enough today, but note that it caps *any* single response — including a slow
client pulling a large `.m4s`.

### 0.4 Settle the port

Three sources disagree: `config.Load()` defaults `PORT=5000`, `Dockerfile` says `EXPOSE 5000`,
`docker-compose.yaml` sets `PORT=8080` and publishes `8080:8080`, and `k8s/deployment.yaml` probes
`5000` while `k8s/service.yaml`/`ingress.yaml` route port `80`. Pick one (**5000**, matching the code
default and the k8s probe), fix compose, and make `EXPOSE` match. A single-image story where the
documented port is wrong is the first bug every new user files.

---

## 3. Phase 1 — Getting the UI source into the build

The UI is a different repository. Four ways to bridge that, and it is the only genuinely awkward part
of this whole roadmap:

| Approach | Reproducible | Offline `docker build` | Cost |
| --- | --- | --- | --- |
| **Git submodule at `web/`** | yes — the commit is pinned in this repo's tree | yes, after `--recursive` clone | contributors must remember `git submodule update --init`; CI needs `submodules: recursive` |
| Clone at build time (`ARG UI_REPO/UI_REF`) | only if `UI_REF` is a SHA | no — needs network in the build | zero repo changes; easy to leave on a floating `main` and lose reproducibility |
| Publish `dist` as a release artifact, `ADD` the tarball | yes, if version-pinned | no | requires a release pipeline in `streamadmin` first |
| Merge both repos into a monorepo | yes | yes | the real fix; also the largest one, and out of scope here |

**Decision: submodule**, with the build-arg clone kept as a documented fallback for people building
from a source tarball. It puts the UI commit in `git log` on this side, which is exactly the version
skew we're trying to kill, and it costs one line in `CONTRIBUTING.md`.

Whichever you pick, the UI's own `dockerfile`, `docker-entrypoint.sh`, `config/default.conf` and
`.env.production` stay where they are. The standalone UI image remains a supported deployment (§7) —
this roadmap adds a packaging option, it doesn't delete one.

### Embedding vs. mounting

`//go:embed all:web/dist` gives one self-contained binary: no runtime path config, no "the UI directory
is empty" failure mode, and `go build` output you can run outside Docker. The catch is that `go:embed`
fails to compile when the directory doesn't exist, which would break `go build ./...` for anyone who
hasn't initialized the submodule.

Fix it with a committed placeholder — `internal/web/dist/index.html` containing a one-line "UI not
built; see docs" page — that the Docker build overwrites with the real output. `go build` always works,
`go test ./...` always works, and the failure mode is a readable page instead of a compile error. Add a
`UI_DIR` env var that, when set, serves from disk instead of the embedded FS; it makes iterating on the
UI against a running API possible without a rebuild.

---

## 4. Phase 2 — The Dockerfile

Three stages, built on the two existing Dockerfiles:

```dockerfile
# 1. UI
FROM node:24.4.1-alpine AS ui
WORKDIR /ui
COPY web/package.json web/package-lock.json ./
RUN npm ci                       # ci, not install — lockfile is authoritative
COPY web/ .
RUN npm run build                # tsc && vite build → /ui/dist

# 2. API (dist must exist before `go build` for go:embed)
FROM golang:1.26.3-alpine3.22 AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /ui/dist ./internal/web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o flixflox ./cmd/server

# 3. Runtime
FROM alpine:3.23.4
RUN apk add --no-cache ffmpeg ca-certificates tzdata
WORKDIR /app
COPY --from=builder /build/flixflox .
RUN mkdir -p /app/uploads && adduser -D -u 10001 flixflox && chown -R flixflox /app
USER flixflox
EXPOSE 5000
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:5000/healthz || exit 1
CMD ["./flixflox"]
```

Notes that matter:

- **Stage order is load-bearing.** `go:embed` reads the filesystem at compile time, so the UI stage
  must complete first. This is the one thing that will bite whoever refactors the Dockerfile later —
  comment it.
- **Drop `--platform=linux/amd64`.** Both current Dockerfiles hardcode it (the UI one via `ARG PLATFORM`).
  It defeats `buildx` multi-arch and forces emulation on the arm64 boxes this project is most likely to
  run on. Let BuildKit infer, and use `--platform=$BUILDPLATFORM` on build stages with `GOARCH=$TARGETARCH`
  for the cross-compile.
- **Run as non-root.** The current image runs everything as root; the merged image is the moment to fix
  it, since UID ownership of the uploads volume has to be decided anyway. Document the UID for people
  with an existing bind mount, and note that k8s needs a matching `fsGroup`.
- **`npm ci`, not `npm install`.** The UI Dockerfile copies `package-lock.json` and then ignores it.
- **Size budget:** ~130 MB of that is ffmpeg. The Go binary is ~20 MB, `dist` under 10 MB. Don't chase
  a distroless base — ffmpeg is the whole point of the alpine layer.
- **Cache:** `COPY go.mod go.sum` and `COPY package*.json` before the source, as both files already do.
  Add `--mount=type=cache` for the Go build cache and npm cache once BuildKit is a given.

Also extend `.dockerignore` — the UI subtree brings `node_modules/` (260 entries), `.DS_Store`, and
`dist/` with it, and none of them belong in the build context.

---

## 5. Phase 3 — Serving quality

- **Compression.** `chimw.Compress(5, "text/html", "text/css", "application/javascript", "application/json")`
  — explicitly enumerated. Never gzip `.m4s`/`.ts` segments or `.jpg` posters: it burns CPU that ffmpeg
  wants and saves nothing on already-compressed data.
- **Range requests.** `http.ServeContent`/`ServeFileFS` handles `Range` correctly; make sure whatever
  wraps the static handler doesn't break it, and re-check `handleStream` under the compression
  middleware — a compressed response can't be byte-ranged.
- **Security headers** on HTML responses only: `X-Content-Type-Options: nosniff`, `Referrer-Policy`,
  `X-Frame-Options: SAMEORIGIN`. A CSP is worth doing but needs a pass over video.js and antd inline
  styles first; ship it in report-only mode initially.
- **CORS.** Same-origin makes `middleware.CORS` a no-op for the bundled UI, but leave it wired: a user
  running the standalone UI image against this API still needs it. Default `CORS_ORIGIN` to empty and
  skip the middleware entirely when unset, rather than defaulting to `http://localhost:5173` in an
  image where that origin is meaningless.
- **Version endpoint.** `/healthz` should report the API version *and* the UI commit baked in. With one
  image the two can't drift, and that's precisely the fact worth exposing.

---

## 6. Phase 4 — Deployment

### 6.1 Compose — the headline demo

```yaml
services:
  flixflox:
    image: elvus/flixflox:1.1.0        # UI + API
    ports: ["5000:5000"]
    environment:
      - MONGO_URI=mongodb://mongo:27017/flixflox
      - JWT_SECRET_KEY=${JWT_SECRET_KEY:?set me}
      - UPLOAD_FOLDER=/app/uploads
      - PORT=5000
    volumes: [uploads:/app/uploads]
    depends_on: { mongo: { condition: service_healthy } }
```

Two services instead of three, no `CORS_ORIGIN`, no `VITE_*` at all. Note `JWT_SECRET_KEY` uses `:?`
rather than today's `:-change-me-in-production` — an image people run in one command should refuse to
boot with a default signing key, not silently accept one.

### 6.2 Kubernetes

`k8s/` currently describes the API alone: one `Deployment` on port 5000, a `Service`, and an `Ingress`
for `api.flixflox.lan`. The all-in-one collapses the (undocumented, presumably separate) UI deployment
into it, and the ingress drops to a single host serving both — keep
`nginx.ingress.kubernetes.io/proxy-body-size: "0"`, it's what makes 2 GB uploads survive the ingress.

Add what's missing while touching these files: a readiness probe (`/healthz/ready` exists and is
unused), resource *limits* (only requests are set), and `fsGroup` matching the new non-root UID.
`replicas: 1` must stay — the conversion queue is in-memory and single-worker
([`TELEGRAM_INGEST_ROADMAP.md`](TELEGRAM_INGEST_ROADMAP.md) §0.1), and `RWO` PVC means a second replica
can't mount the volume anyway. Say so in a comment so nobody scales it and wonders why jobs vanish.

### 6.3 CI

There is no build workflow today (`.github/` holds only issue and PR templates). Add one:
`buildx` for `linux/amd64,linux/arm64`, push to GHCR and Docker Hub, tags `latest` / `1.2.3` / `1.2` /
`sha-<short>`, and a smoke test that boots the image against a throwaway mongo and asserts `/healthz`
is 200, `/` returns HTML, `/config.js` contains `window._env_`, and `/v1/api/nope` returns JSON.
Those four assertions catch every regression this roadmap's failure modes produce.

---

## 7. Phase 5 — Docs and first-run

- **README:** lead with the one-command quickstart, then a "deployment options" table — all-in-one
  (default), split images (scale/CDN the UI separately), source. Be explicit that the two-image path is
  still supported so existing users don't read this as a deprecation.
- **`.env.example`:** the merged image needs `MONGO_URI`, `JWT_SECRET_KEY`, `UPLOAD_FOLDER`, `PORT` and
  nothing else. Show the `VITE_*` overrides in a commented "advanced" block.
- **Upgrade note** for people on the two-image setup: drop the UI service, drop `CORS_ORIGIN`, point the
  browser at the API port. Two lines, but they're the two lines that get filed as bugs.
- **First-run:** with a single URL, "there are no users yet" becomes the first thing anyone sees.
  Either document `POST /v1/api/auth/register` in the quickstart or seed an admin from
  `ADMIN_EMAIL`/`ADMIN_PASSWORD` on an empty `users` collection. Worth deciding now; it's the difference
  between a working demo and a 401.

---

## 8. New configuration

| Variable | Default | Description |
| --- | --- | --- |
| `VITE_STREAMAPI_URL` | `""` | Injected into `/config.js`. Empty = same origin; set only to point the UI at another host |
| `VITE_STREAMAPI_PREFIX` | `/v1/api` | API path prefix |
| `VITE_STREAMAPI_PREFIX_ADMIN` | `/dashboard` | Admin route prefix |
| `VITE_DEFAULT_NEXT_EPISODE_OFFSET` | `15` | Next-episode countdown, seconds |
| `SERVE_UI` | `true` | Set `false` to run API-only from the same image |
| `UI_DIR` | — | Serve the SPA from this directory instead of the embedded FS (development) |
| `CORS_ORIGIN` | `""` | Now optional; empty disables the CORS middleware |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` | — | Optional first-run admin seed (§7) |

Everything else (`MONGO_URI`, `JWT_SECRET_KEY`, `UPLOAD_FOLDER`, `PORT`, `MAX_FILE_SIZE`, `HLS_*`)
keeps its current meaning.

---

## 9. Explicitly out of scope: bundling MongoDB

Tempting — it would make the image a literal single `docker run` — and wrong:

- data would live in the container filesystem, so `docker rm` deletes the library;
- `mongod` and the API would share a supervisor, contradicting §1's whole argument;
- backups, upgrades and resource limits all become bespoke;
- the image roughly doubles in size for something `docker compose` already solves in five lines.

Ship the compose file as the one-command experience instead. If a truly self-contained image is ever
wanted, the honest version is a separate `flixflox:standalone` tag with an embedded database and a loud
"development and evaluation only" banner — and a `MONGO_URI` that still overrides it.

---

## 10. Risks and open decisions

| Risk | Mitigation |
| --- | --- |
| SPA fallback swallows API 404s → clients parse HTML as JSON | Prefix check in `NotFound` (§0.1) + CI smoke test asserting `/v1/api/nope` is JSON |
| Missing asset served as `index.html` → misleading MIME errors | Fall back only for `GET`/`HEAD` with `Accept: text/html` |
| Stale `index.html` cached after redeploy, pointing at deleted hashed assets | `no-store` on the shell and `/config.js`; `immutable` only on hashed `assets/*` |
| Submodule not initialized → confusing build failure | Committed placeholder `dist/index.html` keeps `go build` green; CI uses `submodules: recursive`; `CONTRIBUTING.md` note |
| UI and API versions drift anyway (someone bumps one repo, forgets the other) | Submodule SHA is in this repo's history; `/healthz` reports both |
| 30 s `ReadTimeout` kills every real upload once nginx is gone | §0.3 — blocking, shared with the Telegram roadmap |
| Non-root UID breaks existing bind-mounted `uploads/` | Document the UID and a `chown` line; ship it in the same release as the image rename |
| `--platform=linux/amd64` emulation on arm64 hosts | Multi-arch `buildx`, `$BUILDPLATFORM` + `GOARCH=$TARGETARCH` |
| Users read this as "the standalone UI image is dead" | Keep publishing it; say so in the README table |

**Open questions:** submodule or monorepo — is folding `streamadmin` into this repo on the table, since
it makes every problem in §3 disappear? Should the all-in-one take over the `elvus/flixflox` tag, or
live at `elvus/flixflox-aio` until it's proven? And does anyone actually run the UI and API on separate
hosts today, or is the split purely historical?

---

## 11. Suggested sequencing

**MVP (Phases 0–2):** `docker run -p 5000:5000 -e MONGO_URI=… elvus/flixflox` opens the login page and
streams a video. Everything after is polish.

1. Phase 0 — SPA fallback, `/config.js` handler, read-timeout fix, port cleanup. *API-only change,
   ships and is testable before any Docker work.*
2. Phase 1 — submodule + `go:embed` + placeholder; `go build` produces a binary that serves the UI.
3. Phase 2 — the three-stage Dockerfile, non-root, healthcheck. **← MVP ships here**
4. Phase 3 — compression, cache and security headers, version endpoint.
5. Phase 4 — compose, k8s, CI with multi-arch and the smoke test.
6. Phase 5 — README, upgrade notes, first-run admin.

Phases 0–2 are one focused PR each. Phase 0 is worth doing regardless of whether the image ever ships:
the read-timeout bug is real today, the port inconsistency is real today, and a `/config.js` handler is
strictly less machinery than the entrypoint script it replaces.

---

### References
- [`TELEGRAM_INGEST_ROADMAP.md`](TELEGRAM_INGEST_ROADMAP.md) — shares the §0.3 read-timeout prerequisite
- [`go:embed`](https://pkg.go.dev/embed) · [`http.ServeFileFS`](https://pkg.go.dev/net/http#ServeFileFS)
- [Docker — multi-platform builds](https://docs.docker.com/build/building/multi-platform/)
- UI repository: `streamadmin` (`flixflox-ui`) — `src/env.ts`, `docker-entrypoint.sh`, `config/default.conf`
