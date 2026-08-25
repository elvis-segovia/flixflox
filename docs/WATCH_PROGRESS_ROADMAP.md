# Roadmap — Watch progress, resume and Continue Watching

**Goal:** playback position remembered per viewer, per title. Close the tab mid-episode, come back on
another device, press play, and land where you left off — with a "Continue Watching" row that puts the
right thing first.

Status: proposal / not implemented. Owner: @elvus.

The viewer-profile scaffolding is already built: `models.Viewer` with names, colours and PINs, a profile
picker, and a player that already fires the events this feature needs. What's missing is that **nothing
anywhere stores a playback position** — no field, no collection, no endpoint.

---

## 1. The blocker: the server doesn't know who is watching

Viewer selection is entirely client-side. Picking a profile does this and nothing else
(`streamadmin/src/pages/web/users/index.tsx:48-56`):

```ts
localStorage.setItem("username", selectedViewer.name)
navigate("/web/catalog")
```

A **display name**, in localStorage, never sent anywhere. The viewer's `uuid` is fetched and discarded,
the PIN is never verified, and no request the player makes carries any indication of which household
member is watching. Progress can't be attributed to a viewer the server has never heard of.

So the first slice of work isn't progress at all — it's giving the viewer an identity that survives a
page load and reaches the API:

- persist the viewer **`uuid`**, not the name, and send it with catalog and progress calls;
- a short-lived viewer-scoped cookie or claim set by a `verify-pin` endpoint is the right long-term
  shape, and is specified in [`AUTH_AND_MEDIA_SECURITY_ROADMAP.md`](AUTH_AND_MEDIA_SECURITY_ROADMAP.md) §5;
- until then, an explicit `viewer_uuid` parameter validated against the authenticated user's own viewers
  is enough to build on, and doesn't paint you into a corner.

**Don't** key progress on the localStorage name. Two viewers can share a name, renaming one orphans its
history, and it is trivially forgeable.

---

## 2. Phase 0 — Prerequisites

### 0.1 Duration is declared but never written

`CatalogItem.DurationSeconds` and `Episode.DurationSeconds` exist in `internal/models/catalog.go` — and
grep finds **no code that ever assigns them**. `probeStreams` (`internal/queue/queue.go:164`) asks
ffprobe only for `codec_type,codec_name,pix_fmt`.

Every interesting question here needs duration: what percentage is watched, is this title "finished",
how wide is the progress bar, should this appear in Continue Watching at all. Client-reported duration
is not a substitute — it is absent until `loadedmetadata`, and it is attacker-controlled.

Extend the existing ffprobe call in `processJob`:

```
ffprobe -v error -show_entries format=duration -of csv=p=0 <input>
```

and write it into the catalog in `updateCatalogStatus`, alongside `file_path` and `status`. Backfill
existing rows from the generated `.m3u8` — summing `#EXTINF` values is exact and needs no re-probe.

### 0.2 `updated_at` on the episode, not just the show

`updateCatalogStatus` sets a single top-level `updated_at` per catalog document. Continue Watching sorts
by *when you last watched*, which is a property of the progress record — so this doesn't block anything,
but don't be tempted to reuse the catalog's `updated_at` as a recency signal. It changes when an admin
edits metadata.

---

## 3. Phase 1 — Data model

A separate `watch_progress` collection, **not** a field on `catalog`. Progress is per-viewer and
high-write; the catalog is shared and read-heavy, and embedding would rewrite a whole show document
every fifteen seconds.

```js
{
  viewer_uuid:  "…",           // owner
  content_uuid: "…",           // catalog uuid
  season: 1, episode: 2,       // 0/0 for movies
  position_seconds: 1284.5,
  duration_seconds: 2712.0,    // snapshot; catalog is authoritative
  completed: false,
  completed_at: null,
  updated_at: ISODate(...)
}
```

Indexes — both matter, and the second is the one people forget:

```js
{ viewer_uuid: 1, content_uuid: 1, season: 1, episode: 1 }   // unique — the upsert key
{ viewer_uuid: 1, updated_at: -1 }                            // the Continue Watching query
```

Add them to `database.EnsureIndexes`, which already exists for exactly this.

**Retention:** this collection grows without bound. A TTL index on `updated_at` (a year) or a cap of
N records per viewer keeps it honest. Decide now — retrofitting a TTL onto data users think is
permanent is a different conversation.

---

## 4. Phase 2 — Write path

```
PUT /v1/api/viewers/{viewer_uuid}/progress
{ "content_uuid": "…", "season": 1, "episode": 2, "position_seconds": 1284.5 }
```

An upsert on the unique key. Authorization: the viewer must belong to the authenticated user —
`internal/handlers/viewers.go` already establishes that pattern with `middleware.GetClaims`.

**Throttling is the whole engineering problem.** video.js fires `timeupdate` roughly four times a
second; the handler at `streamadmin/src/components/video/videoPlayer.tsx:94` is already there and is
where this hooks in. Naively posting from it is ~14,000 writes per hour per viewer.

Send on:

| Trigger | Why |
| --- | --- |
| every 10–15 s of playback | bounded loss if the tab dies |
| `pause`, `seeked` | the two moments a user expects to be remembered |
| `ended` / `playlistitem` | mark complete, and record the next episode's start |
| `visibilitychange` → hidden, `pagehide` | the common close-the-laptop case |

For the unload cases use `navigator.sendBeacon` — a normal XHR is cancelled when the page goes away,
which is precisely when the position matters most. Beacons are always POST and can't set custom headers,
so this endpoint has to accept a cookie-authenticated `POST` variant. That interacts directly with the
CSRF work in [`AUTH_AND_MEDIA_SECURITY_ROADMAP.md`](AUTH_AND_MEDIA_SECURITY_ROADMAP.md) §4 — an
`Origin` check handles it; a `SameSite=Strict` cookie would not.

**Validation:** clamp `position_seconds` to `[0, duration]`, reject non-finite values, and ignore
positions from a viewer that doesn't own the request. Never trust a client-supplied duration.

### Completion

Don't hardcode 90%. `Episode.NextEpisodeTime` already exists and marks where the next-episode prompt
appears — i.e. where the credits start. That is a per-episode, human-curated completion threshold, and
reusing it is both more accurate and free:

```
completed  ⟸  position ≥ next_episode_time (when set)  or  position ≥ 0.9 × duration
```

On completion, set `completed: true` and drop the title from Continue Watching — replaced, for a show,
by the next episode.

---

## 5. Phase 3 — Read path

### 5.1 Resume

Return the viewer's progress with the details the player already fetches
(`GET /v1/api/videos/{id}/details`, `/{id}/season/{season}`) rather than adding a round trip on every
play. The player sets `currentTime` on `loadedmetadata` — before that event the seek is silently
dropped, which is the single most common way this feature "doesn't work".

UX details worth getting right the first time:
- resume only if position is between ~30 s and the completion threshold; below that, start over;
- rewind 5–10 s from the stored position — people re-orient better than they remember;
- show "Resume from 21:24" vs "Start over" rather than deciding for them.

### 5.2 Continue Watching

```
GET /v1/api/viewers/{viewer_uuid}/continue-watching?limit=20
```

Sorted by `updated_at` desc, `completed: false`, joined to `catalog` for title, poster and type.

**Collapse to one entry per show.** A viewer three episodes into a series should see *one* card, not
three — and for a finished episode the card should point at the *next* one, not the one they completed.
This is the part that's easy to get subtly wrong, and it's worth a `$group` on `content_uuid` taking the
highest `(season, episode)` rather than post-processing in Go.

Edge cases that will otherwise show up as bugs: the next episode doesn't exist yet (last aired episode →
drop the card, or show "caught up"); the episode was deleted or is still `In-Progress` from the
conversion queue; the whole show was removed. Every one of these is a dangling `content_uuid`.

The web home page (`streamadmin/src/pages/web/home/index.tsx`) is currently a nav shell with three
links — Movies, TV Shows, Browse All. Continue Watching is the row that turns it into a landing page,
and `videoCard.tsx` needs only a progress bar overlay to render it.

---

## 6. Phase 4 — What this unlocks

Once positions exist, several things become cheap:

- **Watched markers** on episode cards in the player's episode list — one boolean, already stored.
- **Up Next / autoplay** across episodes is half-built already (`playlist.next()` on `ended`,
  `videoPlayer.tsx:158`); progress makes "next unwatched" meaningful rather than "next in the list".
- **Mark as watched / remove from Continue Watching** — the two controls people look for the moment the
  row exists.
- **Per-viewer stats** — hours watched, most-watched show.
- **Multi-device handoff** falls out for free, since the position is server-side. Worth calling out in
  the README; it's the visible payoff.

Deliberately *not* in scope: recommendations, ratings, a "because you watched" row. Those need a lot
more data than a position and are a different project.

---

## 7. Risks and open decisions

| Risk | Mitigation |
| --- | --- |
| No server-side viewer identity | §1 — blocking; ties to the security roadmap's PIN verification |
| `timeupdate` write storm (~4/s per player) | Interval + event-driven sends only; `sendBeacon` on unload |
| Position lost exactly when the tab closes | `pagehide`/`visibilitychange` + beacon, not XHR |
| Resume seek silently ignored | Seek on `loadedmetadata`, never earlier |
| Continue Watching shows three cards for one show | `$group` by `content_uuid`, take max `(season, episode)` |
| Dangling `content_uuid` after a delete | Filter the join; sweep orphans periodically |
| `watch_progress` grows forever | TTL index or per-viewer cap, decided up front |
| Progress attributed to the wrong viewer on a shared device | Server-validated viewer scope; never key on the localStorage name |
| Percentages are meaningless without duration | §0.1 — blocking |

**Open questions:** should progress be per-viewer or per-user (i.e. do profiles exist for personalization
or just for show)? Should a completed title vanish from Continue Watching immediately or linger a day
for re-watching? And for movies — resume a film you finished last week, or start over?

---

## 8. Suggested sequencing

**MVP (Phases 0–3):** pick a profile, watch half an episode, close the tab, and find it waiting on the
home page at the right position.

1. Phase 0 — ffprobe duration + backfill. *No user-visible change, unblocks everything.*
2. Phase 1 — viewer `uuid` persisted client-side and sent with requests; `watch_progress` collection
   and indexes.
3. Phase 2 — `PUT …/progress`, throttled reporting from the existing `timeupdate` hook, beacon on unload.
4. Phase 3 — resume on `loadedmetadata`; Continue Watching endpoint and home row. **← MVP ships here**
5. Phase 4 — watched markers, next-unwatched autoplay, manual remove.

Phase 0 and Phase 1 are independent and can go in parallel. Phase 3's grouping logic is the one piece
that deserves table-driven tests before it ships — the input space (movies, shows, mid-episode,
completed, missing next episode, deleted title) is exactly where a hand-checked implementation quietly
breaks.

---

### References
- [`AUTH_AND_MEDIA_SECURITY_ROADMAP.md`](AUTH_AND_MEDIA_SECURITY_ROADMAP.md) §5 — viewer identity, the blocking dependency
- [`navigator.sendBeacon`](https://developer.mozilla.org/en-US/docs/Web/API/Navigator/sendBeacon) ·
  [Page Lifecycle: `pagehide`](https://developer.mozilla.org/en-US/docs/Web/API/Window/pagehide_event)
- [video.js — player events](https://videojs.com/guides/player-workflows/) (`timeupdate`, `loadedmetadata`, `ended`)
