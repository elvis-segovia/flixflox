# Roadmap — Telegram ingestion for FlixFlox

**Goal:** send a video file (or a link) to a Telegram bot, and have the FlixFlox server pick it up,
store it, transcode it to HLS through the existing conversion queue, and publish it in the catalog —
with progress and completion reported back in the chat.

Status: proposal / not implemented. Owner: @elvus.

---

## 1. The one constraint that shapes everything

The public Bot API (`https://api.telegram.org`) lets a bot **download files of at most 20 MB** via
`getFile`. There is no way around it on the cloud API — no chunking, no ranges. A 1.4 GB episode
simply cannot be fetched by a normal bot.

Three ways out, in the order I recommend them:

| Option | Max size | Cost | Notes |
| --- | --- | --- | --- |
| **A. Self-hosted local Bot API server** (`tdlib/telegram-bot-api`, `--local`) | **no download limit**, 2000 MB upload | one extra container + `api_id`/`api_hash` | `getFile` returns an **absolute local path** — no HTTP download at all. Best fit: this project is already `docker compose`-based. |
| B. MTProto user client (`gotd/td`) | 2 GB (4 GB Premium) | more code, a user session to protect | Acts as *your account*, not a bot. Useful if you also want to pull from channels/saved messages. |
| C. Bot receives a **URL**, server downloads it | n/a | least code | Different feature, not "send the video to the bot". Good Phase 0 win, keep it. |

**Decision: build on A.** Because `getFile` hands back a path on a volume both containers can see,
ingesting a 2 GB file becomes an `os.Rename` — no copy, no network hop, no memory spike. B stays as a
documented escape hatch; C ships as a separate command.

```
Telegram client
      │ sends video (as FILE, not "as video")
      ▼
telegram-bot-api container  ──writes──▶  bot_api_data volume
      │ long-poll getUpdates                    │ (mounted at the SAME path
      │ getFile → /var/lib/telegram-bot-api/...  │  in both containers)
      ▼                                          ▼
FlixFlox api container ── os.Rename ──▶ uploads/<Title>/S01/E02/source.mkv
      │
      ├─ ingest.FromFile() → upsert catalog item / season / episode (status In-Progress)
      ├─ queue.Add(Job) → ffmpeg → HLS + thumbnail → status Ready
      └─ bot edits its own message: queued → 37% → Ready ▸ deep link
```

---

## 2. Phase 0 — Prerequisites in existing code (blocking)

These are not Telegram features, but the Telegram path will inherit or amplify each problem. Do them
first.

### 0.1 Persist the conversion queue — `internal/queue/queue.go`
Today jobs live in a `[]Job` slice guarded by a mutex. Consequences that get much worse once ingestion
is asynchronous and unattended:

- a restart or crash silently loses every pending job, and the catalog row stays `In-Progress` forever;
- there is exactly one worker, no retry, no cancellation, and no progress (needed for Phase 4);
- `Info()` returns the whole job list and grows without bound until someone calls `/queue/cleanup`.

Change to a `jobs` collection: claim with `FindOneAndUpdate({status: pending}, {$set:{status: processing, worker_id, claimed_at}})`,
add `attempts`, `last_error`, `progress`, `source` (`http` \| `telegram` \| `url`), and on boot reset
orphaned `processing` jobs back to `pending`. Keep the in-memory queue as the interface, swap the
storage behind it. Add `QUEUE_WORKERS` (default 1) and a stale-claim timeout.

### 0.2 Extract the ingest core — new `internal/ingest`
`handleUploadVideo` (`internal/handlers/videos.go:356`) is ~240 lines mixing multipart parsing, path
building, catalog writes and enqueueing. The Telegram handler must call the same logic, not a copy of
it. Target shape:

```go
type Request struct {
    SourcePath  string // already on local disk; ingest may Rename it
    ContentType string // "movie" | "tvshow"
    Title       string
    Season, Episode int
    Poster      io.Reader // optional
    Meta        Metadata  // year, genres, cast, description, skip-intro fields
}
func FromFile(ctx context.Context, r Request) (contentUUID string, jobID string, err error)
```

Two things must change during the extraction, or the Telegram path is dead on arrival:

- **`bg_image` is mandatory** — a missing poster returns `400`. Telegram gives you no poster. Make it
  optional and fall back to the `thumbnail.jpg` the queue already generates (`queue.go` processJob).
- **The duplicated multipart parse is broken.** Lines 370-375 declare `const maxUploadSize = 2*1024*1024 // 5MB`
  (comment disagrees with the value), then wrap `r.Body` in `MaxBytesReader` and call
  `ParseMultipartForm` a *second* time on a body already consumed at line 358. Delete it and validate
  the poster size from the `*multipart.FileHeader` instead.

### 0.3 Fix the server read timeout — `cmd/server/main.go:56`
`ReadTimeout: 30 * time.Second` bounds reading the **entire request**, body included. Any HTTP upload
that takes more than 30 s to arrive is killed mid-transfer — i.e. every real movie. Use
`ReadHeaderTimeout: 30s` plus a much larger (or zero) `ReadTimeout`, or set per-route deadlines with
`http.ResponseController`. Telegram ingestion sidesteps this, which is precisely why it is attractive.

### 0.4 Idempotency
Telegram redelivers updates when an ack is lost. Store `update_id` (processed-set, TTL) and
`file_unique_id` per ingested file, and reject a duplicate `file_unique_id` with "already in your
library" instead of transcoding it twice.

---

## 3. Phase 1 — Bot skeleton and access control

New package `internal/telegram`:

```
internal/telegram/
  bot.go        client construction, long-poll loop, graceful stop
  handlers.go   command routing
  auth.go       chat allowlist + account linking
  session.go    per-chat conversation state (Mongo, TTL index)
  parse.go      filename → {title, season, episode, year}
```

- **Library:** `github.com/go-telegram/bot` (dependency-free, supports a custom server URL — required
  for the local server). `mymmrac/telego` and `go-telegram-bot-api/telegram-bot-api` are fine
  alternatives; the only hard requirement is being able to override the API base URL.
- **Transport:** long polling. It works behind NAT with no TLS and no public hostname, which matches a
  self-hosted deployment. Webhook mode is a Phase 6 option.
- **Wiring:** start from `main.go` behind `TELEGRAM_ENABLED=true`, next to `queue.New`, and stop it in
  the existing SIGINT/SIGTERM block. The bot must never be able to panic the HTTP server — recover per
  update.
- **Access control, deny by default.** The bot is an *admin* ingestion tool. Only linked Telegram IDs
  may do anything.
  - `POST /v1/api/auth/telegram/link-code` (JWT) → short-lived one-time code.
  - In chat: `/link 482913` → writes `telegram_links {telegram_id, chat_id, user_uuid, linked_at}`.
  - Everything else replies "not authorized" and logs the attempt. An `TELEGRAM_ADMIN_IDS` env
    allowlist is the bootstrap escape hatch.
- **Commands:** `/start`, `/help`, `/link <code>`, `/status [uuid]`, `/queue`, `/cancel <job>`,
  and later `/download <url>`.

Ship Phase 1 against the **cloud** API with small files. It proves auth, routing and linking before
the local-server plumbing lands.

---

## 4. Phase 2 — Large-file ingestion via the local Bot API server

### 4.1 Compose
```yaml
telegram-bot-api:
  image: aiogram/telegram-bot-api:latest   # or build tdlib/telegram-bot-api
  command: ["--local"]
  environment:
    - TELEGRAM_API_ID=${TELEGRAM_API_ID}     # from my.telegram.org
    - TELEGRAM_API_HASH=${TELEGRAM_API_HASH}
  volumes:
    - bot_api_data:/var/lib/telegram-bot-api
  restart: unless-stopped
```
Mount `bot_api_data` into the `api` container **at the identical path** and make `uploads` live on the
same filesystem/volume. Both details matter: the path so the absolute `file_path` resolves, the
filesystem so `os.Rename` is atomic instead of a 2 GB copy. Point the bot client at
`http://telegram-bot-api:8081`.

### 4.2 Handler flow
1. Update carries `message.Video` **or** `message.Document` — handle both. Telegram clients recompress
   anything sent "as video"; the bot should tell users to **send as file** to preserve quality, and
   `.mkv` always arrives as a Document.
2. Validate: linked user, extension against the existing `allowedExtensions` map
   (`videos.go:28`), `file_size` ≤ `MAX_FILE_SIZE`, and **free disk ≥ ~2.5× file size** (source + HLS
   output coexist until conversion finishes).
3. `getFile` → absolute path. Reply immediately with "received, resolving metadata…" — never block the
   update loop.
4. `os.Rename` into `uploads/<SafeTitle>/[S01/E02/]`, then `ingest.FromFile`, then `queue.Add`.
   On any failure, delete the partial destination and report the error in chat.
5. Clean up the bot-api data dir; it retains downloaded files and will fill the disk otherwise.

**Concurrency:** cap simultaneous ingests (`TELEGRAM_MAX_CONCURRENT_INGEST`, default 1). Ten forwarded
episodes should queue, not race for disk.

---

## 5. Phase 3 — Metadata: turning a filename into a catalog entry

The catalog needs `title`, `type`, `season`, `episode`, and ideally year/genre/cast/description
(`internal/models/catalog.go`). A Telegram message gives you a filename and maybe a caption.

- **Parser (`parse.go`)** for `Show.Name.S01E02.1080p.WEB-DL.x264-GRP.mkv` → title/season/episode, plus
  `1x02`, `E02`, `Movie Title (2019)`. Strip release tags. Table-driven unit tests — this is the
  cheapest place in the whole feature to get regressions.
- **Caption overrides**, always wins over the parser:
  `#tvshow title=Breaking Bad s=1 e=2` / `#movie title=Arrival year=2016`.
- **Interactive confirmation** with inline keyboards: "Detected *Breaking Bad* S01E02 — [Confirm]
  [Movie instead] [Rename]". Callback state in `telegram_sessions` with a TTL index; never keep it in
  process memory (it must survive a restart mid-conversation).
- **Existing show?** Match by `uuid` or fuzzy title against `catalog` and route to the
  add-episode path instead of creating a duplicate show. Reuse the array-filter update already in
  `handleAddEpisode` / `updateCatalogStatus`.
- **Optional TMDB enrichment** (`TMDB_API_KEY`): fills description, genres, cast, year, and a real
  poster — which closes the `bg_image` gap from §0.2 properly. Keep it behind a flag and degrade
  gracefully; never fail an ingest because a metadata provider is down.

---

## 6. Phase 4 — Feedback in the chat

- **Progress:** run ffmpeg with `-progress pipe:1`, parse `out_time_ms`, divide by the `ffprobe`
  duration, store on the job (needs §0.1). Edit the original bot message on a ticker.
- **Respect rate limits:** ~1 message/second per chat and ~20 messages/minute per group. Throttle edits
  to one every 10–15 s and skip an edit when the percentage hasn't moved. Handle HTTP 429 +
  `retry_after` with a backoff, or the bot will get temporarily muted.
- **Completion:** "*Breaking Bad* S01E02 is ready" + thumbnail + deep link to the player.
- **Failure:** last ~15 lines of ffmpeg stderr (truncated, no paths that leak host layout) and a
  [Retry] button that re-enqueues the same job.

---

## 7. Phase 5 — `/download <url>` (server-side fetch)

The other reading of "trigger the download on the server", and genuinely useful: send a link, the
server fetches it into `uploads/` and runs the same ingest path.

**This is an SSRF sink — treat it as hostile input.** Non-negotiables:

- scheme allowlist (`https` only by default), and resolve the hostname *then* reject the request if any
  resolved IP is loopback / link-local / private / CGNAT / IPv6 ULA / `169.254.169.254`;
- re-validate after **every** redirect (cap at 3), pin the connection to the validated IP;
- hard caps on `Content-Length`, total bytes written, and wall-clock; stream to disk, never to memory;
- optional host allowlist for a paranoid deployment.

`yt-dlp` support and magnet/torrent handoff are possible later; both belong behind an explicit opt-in
flag, and the usual "only ingest content you have the right to host" caveat applies.

---

## 8. Phase 6 — Hardening, ops, docs

- **Secrets:** `TELEGRAM_BOT_TOKEN` in env only (`.env` is already gitignored), never logged, never
  echoed in an error. A leaked token is full control of the bot — rotate via BotFather.
- **Quotas:** per-user daily ingest count/bytes; global concurrent-ffmpeg cap so one binge doesn't
  starve streaming clients on the same box.
- **Webhook mode (optional):** public HTTPS, validate `X-Telegram-Bot-Api-Secret-Token` on every
  request, and keep polling as the default. The local server also allows plain HTTP webhooks on any
  port behind your own proxy.
- **Observability:** structured logs correlating `update_id → chat_id → content_uuid → job_id`; counters
  for ingests started/failed, bytes ingested, transcode duration; extend `/healthz` readiness with bot
  connectivity (`getMe`) and free disk on `UPLOAD_FOLDER`.
- **Tests:** `httptest` fake Bot API (the custom-base-URL requirement pays off here), golden tests for
  the filename parser, and one end-to-end test with a 2-second sample clip through
  ingest → queue → HLS.
- **Docs:** README section, `.env.example` additions, new endpoints in `openapi.yml` and the Postman
  collection, and a "how to set up your bot" guide (BotFather → token → `my.telegram.org` → api_id/hash
  → `/link`).
- **Privacy note for users:** files pass through Telegram's servers and stay in the chat history; a
  local Bot API server does not change that.

---

## 9. New configuration

| Variable | Default | Description |
| --- | --- | --- |
| `TELEGRAM_ENABLED` | `false` | Master switch for the bot |
| `TELEGRAM_BOT_TOKEN` | — | BotFather token |
| `TELEGRAM_API_URL` | `https://api.telegram.org` | Set to `http://telegram-bot-api:8081` for local mode |
| `TELEGRAM_LOCAL_MODE` | `false` | `getFile` returns a local path; skip HTTP download |
| `TELEGRAM_API_ID` / `TELEGRAM_API_HASH` | — | Required by the local Bot API server |
| `TELEGRAM_ADMIN_IDS` | — | Comma-separated bootstrap allowlist |
| `TELEGRAM_MAX_CONCURRENT_INGEST` | `1` | Parallel ingests |
| `QUEUE_WORKERS` | `1` | Parallel ffmpeg jobs |
| `TMDB_API_KEY` | — | Optional metadata enrichment |
| `URL_INGEST_ENABLED` | `false` | Enables `/download <url>` |

## 10. Data model additions

- `jobs` — persisted conversion queue (§0.1).
- `telegram_links` — `{telegram_id (unique), chat_id, user_uuid, linked_at}`.
- `telegram_sessions` — conversation state, TTL index on `expires_at`.
- `ingested_files` — `{file_unique_id (unique), content_uuid, season, episode, ingested_at}` for
  dedupe.
- `catalog` — add `source` (`http`/`telegram`/`url`) and `source_ref` for provenance.

## 11. Risks and open decisions

| Risk | Mitigation |
| --- | --- |
| 20 MB cloud cap makes the naive version useless | Local Bot API server from Phase 2; don't ship a cloud-only "upload a video" promise |
| Local server needs `api_id`/`api_hash` and a container | Documented setup; keep cloud mode working for small files and commands |
| Clients recompress video sent "as video" | Bot instructs "send as file"; warn when `message.Video` arrives without a document |
| Disk exhaustion (2 GB source + HLS) | Preflight free-space check, delete source after success (already done in `processJob`), orphan sweeper |
| Losing a 40-minute transcode on restart | Persisted queue + resumable claims (§0.1) |
| Telegram rate limits muting the bot | Throttled edits, 429/`retry_after` backoff |
| SSRF via `/download` | Phase 5 hard rules; feature off by default |
| Duplicate ingests from redelivered updates | `update_id` + `file_unique_id` dedupe |

**Open questions:** is the bot admin-only or per-user self-service? Do you want the MTProto path (B) to
pull existing archives out of channels? Cloud mode kept as a supported configuration, or local-only?

## 12. Suggested sequencing

**MVP (Phases 0–2):** send a file to the bot → it lands in the catalog and streams. That is the whole
value; everything after is polish.

1. Phase 0 — persisted queue, `internal/ingest`, read-timeout fix, dedupe. *No user-visible change.*
2. Phase 1 — bot skeleton, linking, commands, small files via cloud API.
3. Phase 2 — local Bot API server, big-file ingest via rename. **← MVP ships here**
4. Phase 3 — filename parsing, inline confirmation, TMDB.
5. Phase 4 — progress and completion notifications.
6. Phase 5 — `/download <url>`, if still wanted.
7. Phase 6 — quotas, metrics, webhook mode, docs.

Phases 0–2 are each 1–2 focused PRs. Keep `internal/telegram` free of Mongo/ffmpeg specifics: it
translates Telegram updates into `ingest.Request` values and nothing more. That boundary is what lets
you add option B, a web UI, or a watch-folder later without touching any of this again.

---

### References
- [Bot API — getFile](https://core.telegram.org/bots/api#getfile) · [Bots FAQ](https://core.telegram.org/bots/faq)
- [tdlib/telegram-bot-api](https://github.com/tdlib/telegram-bot-api) — local server, `--local`
- [Telegram — Uploading and Downloading Files](https://core.telegram.org/api/files) (MTProto path)
