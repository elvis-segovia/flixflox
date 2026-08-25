# Roadmap — Authentication, authorization and media access control

**Goal:** make "who may do what, and who may watch what" an enforced property of the server rather than
an assumption about who can reach it. Today FlixFlox authenticates *some* requests, authorizes none of
them, and serves the entire media library to anyone who can open the port.

Status: proposal / not implemented. Owner: @elvus.

Unlike the [Telegram](TELEGRAM_INGEST_ROADMAP.md) and [all-in-one image](ALL_IN_ONE_IMAGE_ROADMAP.md)
roadmaps, most of this document is not new features — it is **defects that exist in `main` right now**.
Phase 0 should be read as a bug list.

---

## 1. Where things stand

| | Today |
| --- | --- |
| Authentication | JWT (HS256), access + refresh, `token_blacklist` for logout. Passwords bcrypt-hashed. Solid. |
| Authorization | **None.** `Role` and `Privileges` ride in every token and are never read by any handler. |
| Registration | **Open to the public**, and a registered user can do everything an admin can. |
| Media access | **Unauthenticated.** Catalog, streams, posters and the job queue are all public. |
| Viewer PINs | Hashed on write, **never verified** — neither server nor UI checks them. |

The parts that were built carefully (bcrypt via `internal/utils/hash.go`, JTI blacklisting, refresh-token
separation, hashed viewer PINs) make the gaps more surprising, not less: the pieces are there, they're
just not wired to anything.

---

## 2. Phase 0 — Defects to fix before anything else

### 0.1 Open registration + zero authorization = anonymous admin

`POST /v1/api/auth/register` is registered outside every auth group (`internal/handlers/auth.go:21`) and
grants `Privileges: []string{"read"}` (`auth.go:153`). But **no handler ever reads `Role` or
`Privileges`** — grep confirms the only consumers are the token generator and the user-management
handlers writing them. `middleware.JWTAuth` proves *authentication* and stops there.

So the actual access model is: anyone who can reach `/v1/api/auth/register` can create an account and
then call `POST /v1/api/videos/upload`, `PUT /v1/api/videos/{id}/new-episode`, `POST /v1/api/videos/queue/cleanup`,
`POST /v1/api/users` and `DELETE /v1/api/users/{id}`. The `"read"` privilege is decorative.

Two changes, both small:

```go
// internal/middleware/authz.go
func RequirePrivilege(p string) func(http.Handler) http.Handler   // 403, not 401
func RequireRole(roles ...string) func(http.Handler) http.Handler
```

and an `ALLOW_REGISTRATION` flag (**default `false`**) so a fresh instance isn't self-service. Bootstrap
the first admin from `ADMIN_USERNAME`/`ADMIN_PASSWORD` on an empty `users` collection — which is also
what the all-in-one image needs for its first-run story
([`ALL_IN_ONE_IMAGE_ROADMAP.md`](ALL_IN_ONE_IMAGE_ROADMAP.md) §7).

Write the privilege matrix down in one place. Guessing which of `read`/`write`/`admin` guards which
route is how this drifts again.

### 0.2 `JWTAuth` accepts refresh tokens as access tokens

`JWTRefresh` checks the token type (`internal/middleware/jwt.go:128`):

```go
if claims.TokenType != "refresh" { ... }
```

`JWTAuth` (`jwt.go:87-111`) has no equivalent check. A refresh token is therefore valid on every
protected endpoint. That matters because of `extractToken` (`jwt.go:144-158`), which falls through a
chain:

```go
Authorization: Bearer …  →  access_token cookie  →  refresh_token cookie
```

Once the 1-hour access cookie expires, the browser stops sending it and the **30-day refresh cookie is
picked up instead** — silently, with no refresh call, no rotation, and no blacklist entry. The short
access-token TTL that the whole design rests on is not actually enforced.

Fix both halves: add `if claims.TokenType != "access"` to `JWTAuth`, and make the cookie lookup
explicit per middleware (`extractToken(r, "access_token")` / `extractToken(r, "refresh_token")`) instead
of a fallthrough. Add refresh-token rotation while you're in there — `handleRefreshToken` issues a new
access token but leaves the old refresh token live for its full 30 days.

### 0.3 The path guard on `/stream/*` and `/image/*` is a string comparison

`handleStream` and `handleBgImage` (`internal/handlers/videos.go`) take the whole filesystem path from
the URL and validate it like this:

```go
filePath := chi.URLParam(r, "*")
if !strings.HasPrefix(filepath.Clean(filePath), filepath.Clean(cfg.UploadFolder)) {
```

Two problems. `strings.HasPrefix` is not a path-boundary check — with `UPLOAD_FOLDER=/app/uploads`,
a request for `/app/uploads_backup/anything` passes, because `"uploads_backup"` string-prefixes
`"uploads"`. And the client supplies an *absolute* path that goes straight to `http.ServeFile`, so this
one line is the entire boundary. Symlinks inside the upload tree aren't considered at all.

Go 1.26 (already the builder image) makes this structural rather than a matter of getting the string
comparison right:

```go
root, err := os.OpenRoot(cfg.UploadFolder)      // traversal outside root is impossible, symlinks included
http.ServeFileFS(w, r, root.FS(), relPath)      // relPath is relative, never client-absolute
```

That also means the API should stop storing and returning absolute host paths in `file_path` /
`thumbail_path` (`internal/models/catalog.go`, written by `queue.updateCatalogStatus`). They should be
relative to the upload root: it removes the traversal surface, stops leaking host layout to every
client, and makes the library relocatable. It is a **migration** — existing documents hold absolute
paths — so plan a one-shot script and a read-time fallback.

### 0.4 `Secure` cookies over the documented HTTP deployment

Every `http.SetCookie` in `auth.go` sets `Secure: true, SameSite: http.SameSiteNoneMode`. Browsers
reject `Secure` cookies over plain `http://` (localhost excepted) — and `k8s/ingress.yaml` serves
`http://api.flixflox.lan` with no TLS. On that deployment, cookie authentication silently does nothing;
only the `Authorization` header path works.

Make it `COOKIE_SECURE` / `COOKIE_SAMESITE`, default to secure, and document that a LAN HTTP
deployment must opt out. Better: put TLS in front and leave the defaults alone.

### 0.5 `queue/info` is public

`r.Get("/v1/api/videos/queue/info", handleQueueInfo(q))` (`videos.go:41`) returns
`ConversionQueue.Info()`, which includes the whole `jobs` slice — absolute `InputPath` / `OutputDir`,
titles, and `Error` strings containing raw ffmpeg output (`queue.go:98-106`, `queue.go:256`). That is
host filesystem layout and an unreleased-content list, handed to anonymous callers. Move it behind
`RequirePrivilege("admin")` and truncate `Error` in the response.

---

## 3. Phase 1 — Authenticating the media path

This is the part with an actual design decision, because **HLS segment requests can't carry an
`Authorization` header**. video.js delegates to hls.js (where headers need an `xhrSetup` hook) or, on
Safari/iOS, to the browser's native HLS engine, which offers no hook at all. Manifests, segments,
keys and posters are all plain browser-issued GETs.

| Approach | Works with native HLS | Notes |
| --- | --- | --- |
| **A. Cookie auth on `/stream/*` + `/image/*`** | yes | `extractToken` already reads cookies, so this is nearly free once §0.2 is fixed. Requires same-origin (or `SameSite=None` + credentialed CORS, which is what §4 wants to stop). Best fit alongside the [all-in-one image](ALL_IN_ONE_IMAGE_ROADMAP.md). |
| **B. Signed expiring URLs** (`?exp=…&sig=HMAC(path+exp+viewer)`) | yes | Origin-independent, so it survives a CDN or a separate media host. Needs care: sign the *directory*, not each segment, or every `.m4s` needs its own signature; pick a TTL longer than the longest episode; a leaked URL is valid until expiry. |
| C. Short-lived playback token in the path | yes | A variant of B with server-side state. More moving parts, easier revocation. |
| D. `Authorization` header via `xhrSetup` | **no** | Breaks Safari and every native-HLS client. Non-starter for a media server. |

**Decision: A now, B as an option.** Cookie auth costs one middleware and matches the single-origin
direction the project is already heading. Add `MEDIA_SIGNED_URLS=true` later for anyone fronting
`/stream/` with a CDN — the HMAC helper is ~40 lines and the player only needs the signed prefix.

Whichever ships, keep the catalog endpoints (`/videos`, `/{vtype}/list`, `/{id}/details`,
`/{id}/season/{season}`) behind authentication too. A public catalog that lists every title, path and
description is the same disclosure as a public library, one step removed.

**Do not skip `Range` testing.** Auth middleware that buffers, rewrites or compresses a response breaks
seeking in ways that look like player bugs, not auth bugs.

---

## 4. Phase 2 — CSRF and cookie hardening

`SameSite: None` means the browser attaches FlixFlox cookies to cross-site requests, and
`middleware.CORS` sets `Access-Control-Allow-Credentials: true` unconditionally. For JSON endpoints the
`Content-Type: application/json` preflight blocks the attack. **`POST /v1/api/videos/upload` is
`multipart/form-data`** — a CORS-safelisted content type, so it is a *simple* request and is never
preflighted. An auto-submitting form on any page a logged-in admin visits can upload to their library
with their cookies. The attacker can't read the response; they don't need to.

- `SameSite=Lax` by default. Same-origin deployment makes this free; the standalone-UI deployment needs
  `None` and should have to ask for it.
- Validate `Origin` (falling back to `Sec-Fetch-Site`) on every state-changing method, and reject
  mismatches — cheap, and independent of `SameSite` support.
- Set `Access-Control-Allow-Credentials` **only** for allowlisted origins, and always send
  `Vary: Origin` (`internal/middleware/middleware.go` currently does neither), or a caching proxy will
  serve one origin's headers to another.
- Scope cookies with `Path=/` and an explicit `Domain` only when needed.

**The UI defeats `HttpOnly` anyway.** The server carefully sets `HttpOnly` cookies, and then every
controller reads the token back out of localStorage and attaches it by hand
(`streamadmin/src/controllers/catalogController.ts:11` and its five siblings):

```ts
headers: { 'Authorization': `Bearer ${localStorage.getItem('authToken')}` }
```

A token in localStorage is readable by any script on the page, which is exactly what `HttpOnly` exists
to prevent — so the flag currently buys nothing. Pick one model and commit: cookie-only is the better
fit here, since Phase 1 needs cookies for media requests regardless. Note also that this header is
captured **once at controller construction**, so a token refreshed mid-session isn't picked up by an
already-instantiated controller — use an axios interceptor instead.

---

## 5. Phase 3 — Viewer profiles are not an access boundary

`models.Viewer` has `Pin` and `UsePin`; `handleCreateViewer` bcrypts the PIN (`viewers.go:132-138`).
Nothing ever verifies it. There is no verify endpoint in `RegisterViewerRoutes`, and the UI navigates
straight into the catalog on click, persisting only a display name:

```ts
localStorage.setItem("username", selectedViewer.name)   // streamadmin/src/pages/web/users/index.tsx:54
navigate("/web/catalog")
```

So "PIN-protected profile" currently means "profile with a PIN stored in the database". If the intent
is a kids' profile with content restrictions, that needs to be real:

- `POST /v1/api/viewers/{uuid}/verify-pin` → sets a short-lived, `HttpOnly` viewer-scoped cookie or
  claim; rate-limited, constant-time, with lockout after N attempts.
- Send the active viewer with every catalog and stream request; enforce it **server-side**. A
  `localStorage` name is a label, not a credential.
- Maturity rating on `CatalogItem` + an allowed-rating list per viewer, filtered in the query rather
  than in the UI.

This is also the prerequisite the [watch-progress roadmap](WATCH_PROGRESS_ROADMAP.md) needs — it can't
attribute playback to a viewer the server doesn't know about.

---

## 6. Phase 4 — Abuse resistance

- **Login rate limiting.** `handleLogin` does an unthrottled bcrypt compare per request. No limiter, no
  lockout, no backoff. Add per-IP and per-username limits (`chimw.Throttle` for the crude version,
  Mongo-backed counters for something that survives a restart) plus progressive lockout. Note the
  double edge: bcrypt makes brute force expensive *and* makes login a cheap DoS amplifier.
- **Timing.** `handleLogin` returns early when the username misses (`auth.go:56`), skipping bcrypt
  entirely — a measurable difference that enumerates valid usernames. Compare against a dummy hash on
  the miss path.
- **Upload limits.** `MAX_FILE_SIZE` is checked, but there is no per-user quota and no disk-space
  preflight; the [Telegram roadmap](TELEGRAM_INGEST_ROADMAP.md) §6 raises the same point from the other
  direction. Solve it once, in `internal/ingest`.
- **Audit log.** Login success/failure, privilege changes, uploads, deletions — `{who, what, when, from}`.
  Without it, "was this instance touched?" is unanswerable.

---

## 7. Phase 5 — Ops

- **`JWT_SECRET_KEY` defaults to `change-me-in-production`** in `config.Load()` *and* in
  `docker-compose.yaml` (`:-change-me-in-production`). A known signing key is total authentication
  bypass — anyone can mint an admin token. Refuse to boot when the secret is unset or equal to the
  default, and require ≥32 bytes. This is one `if` and it closes the worst hole in the project.
- **Secret rotation:** support two verification keys so tokens survive a rotation, and invalidate all
  sessions on demand (bump a `token_version` per user, check it in `JWTAuth`).
- **Security headers** on HTML: `nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`,
  `X-Frame-Options`, HSTS behind TLS, and a CSP once video.js and antd inline styles are surveyed.
- **Dependency scanning:** `govulncheck` and `npm audit` in CI — neither repo has any CI today.
- **`SECURITY.md` already exists** and promises a disclosure process; make sure it names a real contact
  and a response window.

---

## 8. New configuration

| Variable | Default | Description |
| --- | --- | --- |
| `ALLOW_REGISTRATION` | `false` | Public `POST /auth/register` |
| `ADMIN_USERNAME` / `ADMIN_PASSWORD` | — | First-run admin seed on an empty `users` collection |
| `COOKIE_SECURE` | `true` | Set `false` only for a plain-HTTP LAN deployment |
| `COOKIE_SAMESITE` | `lax` | `none` required for split-origin UI + API |
| `MEDIA_AUTH` | `cookie` | `cookie` \| `signed` \| `none` (`none` = today's behaviour, explicit opt-in) |
| `MEDIA_URL_TTL` | `6h` | Signed-URL lifetime; must exceed the longest title |
| `LOGIN_RATE_LIMIT` | `10/min` | Per IP and per username |
| `LOGIN_LOCKOUT_ATTEMPTS` | `10` | Then progressive backoff |

---

## 9. Risks and open decisions

| Risk | Mitigation |
| --- | --- |
| Enforcing authz breaks existing users whose accounts have no `role`/`privileges` | Migration assigning `admin` to existing accounts; log-only mode for one release before enforcing |
| Authenticating `/stream/*` breaks playback on Safari / native HLS | Cookie or signed URL only (§3); test on iOS Safari, tvOS and Chromecast before shipping |
| Path migration to relative `file_path` breaks every existing catalog row | One-shot migration + read-time fallback for absolute paths; ship them in the same release |
| Auth middleware breaks `Range` requests → seeking fails | Explicit range tests; keep the media handler free of buffering/compressing wrappers |
| `SameSite=Lax` breaks the split-origin deployment | Keep `None` available via config; default matters more than capability |
| Rate limiting locks out a household behind one NAT IP | Limit per username primarily, per IP with a generous ceiling |
| Signed URLs leak via referrer/history and stay valid until expiry | Short TTL, bind the signature to the viewer, never sign with the JWT secret |

**Open questions:** is FlixFlox meant to be internet-facing, or LAN-only behind a VPN? The answer
changes how much of §4 and §6 is worth building — but not §0, which is wrong either way. Should the
public catalog stay public as a "browse before login" feature (then it needs its own trimmed
projection, not the full documents)? And is `Privileges []string` or `Role string` the real model — both
exist, neither is used, and picking one now avoids a second migration.

---

## 10. Suggested sequencing

1. **Phase 0 — §0.1 through §0.5, plus §7's `JWT_SECRET_KEY` boot check.** All small, all present-tense
   bugs. This is the release that matters; everything below is improvement rather than repair.
2. Phase 1 — authenticate catalog and media, cookie mode. **← the "not world-readable" milestone**
3. Phase 2 — CSRF, cookie flags, CORS correctness.
4. Phase 3 — viewer PIN verification and maturity ratings (unblocks
   [watch progress](WATCH_PROGRESS_ROADMAP.md)).
5. Phase 4 — rate limiting, lockout, timing, audit log.
6. Phase 5 — rotation, headers, dependency scanning.

Every item in Phase 0 is independently shippable and none of them depend on each other, so they can go
out as separate small PRs — which is also the only way they'll get reviewed properly.

**Testing note:** there is not a single `_test.go` file in this repository. Authorization is exactly the
kind of logic that is regression-prone and cheap to table-test (`{route, method, token, expected
status}`). Whatever else happens, Phase 0 should arrive with tests.

---

### References
- [OWASP — Authorization Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html)
- [`os.OpenRoot`](https://pkg.go.dev/os#OpenRoot) — traversal-proof file serving, Go 1.24+
- [MDN — SameSite cookies](https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Set-Cookie/SameSite) ·
  [CORS-safelisted request headers](https://developer.mozilla.org/en-US/docs/Glossary/CORS-safelisted_request_header)
