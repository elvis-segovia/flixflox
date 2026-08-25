# Roadmap — Adaptive bitrate, subtitles and audio tracks

**Goal:** turn the conversion pipeline from "one stream, one audio track, no subtitles" into a real HLS
package — a bitrate ladder behind a master playlist, every audio track selectable, and subtitles that
survive ingestion instead of being silently discarded.

Status: proposal / not implemented. Owner: @elvus.

Everything here lives in `internal/queue/queue.go` (`processJob`, ~70 lines of ffmpeg argument
assembly) plus the catalog model and the player's track menus.

---

## 1. What the pipeline produces today

One rendition, one audio track, no subtitles:

```go
args := []string{"-y", "-hide_banner", "-loglevel", "error", "-i", job.InputPath,
    "-map", "0:v:0",
    "-map", "0:a:0?",     // first audio track only; everything else dropped
}                          // no -map 0:s — every subtitle stream discarded
// -c:v libx264 -preset veryfast -crf 23   (or -c:v copy when h264/yuv420p)
// -c:a aac -b:a 128k -ac 2                (or copy for aac/mp3)
// -f hls -hls_time N -hls_segment_type fmp4 -hls_flags independent_segments
```

The consequences on a home connection: a viewer on hotel wifi gets the same 8 Mbps 1080p stream as one
on the LAN and buffers through the whole episode; a foreign-language film has no subtitles at all,
because they were thrown away at ingest and the source file was then deleted (`queue.go:260`); a
5.1 track is downmixed to stereo with no way back.

Re-ingesting is the only recovery, and for anything that arrived by upload the source is already gone.
That makes this more urgent than it looks: **every day this ships as-is, more originals are destroyed.**

---

## 2. Phase 0 — Transcode defects to fix first

These are wrong today, independent of ABR, and several are cheap.

### 0.1 The playlist isn't marked VOD

`-hls_list_size 0` keeps every segment in the playlist, but nothing sets `-hls_playlist_type vod`, so
the output carries no `#EXT-X-PLAYLIST-TYPE:VOD` tag. Players are then entitled to treat it as a live
or event stream: seeking behaviour becomes implementation-defined and some clients refuse to scrub past
the buffered region. One flag.

### 0.2 Stream-copy silently breaks segment durations

```go
canCopyVideo := vcodec == "h264" && pixFmt == "yuv420p"
if canCopyVideo { videoArgs = []string{"-c:v", "copy"} }
```

The re-encode path carefully forces a keyframe every `HLS_SEGMENT_TIME` seconds with `-sc_threshold 0`
and `-force_key_frames`. The copy path **drops both**, because it can't re-encode — so ffmpeg can only
cut at whatever keyframes the source happens to have. A source with a 10-second GOP is fine; one with
sparse or irregular keyframes produces wildly uneven segments, which wrecks ABR switching later and
makes `-hls_flags independent_segments` an assertion that isn't true.

Fix: probe the source GOP (`ffprobe -select_streams v -show_frames -show_entries frame=key_frame,pkt_pts_time`)
and only copy when keyframe spacing actually divides the target segment duration. Otherwise re-encode.
Stream copy is a large win when it's valid — it's just not valid as often as this code assumes.

### 0.3 MP3 audio copied into fMP4

```go
case "mp3": audioArgs = []string{"-c:a", "copy"}
```

With the default `HLS_SEGMENT_TYPE=fmp4`, this muxes MP3 into CMAF segments. HLS's fMP4 profile expects
AAC, AC-3 or EC-3; Safari — the one browser where HLS is native and non-negotiable — will not play it.
Restrict the MP3 copy path to `mpegts`, or just re-encode MP3 to AAC. It's a small file and a cheap
encode.

### 0.4 HDR is flattened, not tonemapped

`canCopyVideo` requires `yuv420p`, so any 10-bit HDR source (`yuv420p10le`, PQ/HLG) takes the re-encode
path, where `-pix_fmt yuv420p` truncates it to 8-bit SDR **without tonemapping**. The result plays, and
looks grey and washed out. Proper handling is a `zscale`/`tonemap` filter chain, and it is the single
most visible quality difference in the whole pipeline for anyone with 4K HDR sources:

```
-vf zscale=t=linear:npl=100,tonemap=hable,zscale=t=bt709:m=bt709:r=tv,format=yuv420p
```

Detect via `color_transfer` in `smpte2084`/`arib-std-b67` and apply only then; the filter is expensive
and destructive on SDR input.

### 0.5 Thumbnails are taken at a fixed five seconds

`-ss 00:00:05` (`queue.go:194`) lands on a black frame, a distributor logo or a fade-in more often than
not. Take several frames across the title and pick the one with the highest variance, or seek to ~10%
of duration. Also move `-ss` **before** `-i` — as an output option it decodes five seconds of video to
get one frame.

### 0.6 ffmpeg's full output goes into an error string

`return fmt.Errorf("ffmpeg conversion failed: %w, output: %s", err, string(out))` (`queue.go:256`) puts
the entire stderr into `Job.Error`, which `Info()` serves to anyone hitting the public
`/v1/api/videos/queue/info` — including absolute host paths. Truncate to the last ~15 lines and see
[`AUTH_AND_MEDIA_SECURITY_ROADMAP.md`](AUTH_AND_MEDIA_SECURITY_ROADMAP.md) §0.5 for the endpoint itself.

---

## 3. Phase 1 — Probe properly

`probeStreams` returns three strings (`vcodec`, `acodec`, `pixFmt`) parsed out of CSV. Everything below
needs much more: resolution, bitrate, duration, frame rate, colour transfer, and the full stream list
with `language` and `title` dispositions.

Replace it with one `ffprobe -show_format -show_streams -of json` call decoded into a struct. It's less
code than the current CSV splitting, it's not positional, and it gives
[`WATCH_PROGRESS_ROADMAP.md`](WATCH_PROGRESS_ROADMAP.md) §0.1 the duration it needs at the same time.

Persist the result. Re-probing a deleted source file is impossible, and "what was in this file?" is a
question the next three phases all ask.

---

## 4. Phase 2 — The bitrate ladder

### 4.1 Never upscale

The ladder must be derived from the source, not fixed. A 720p source gets 720p/480p/360p; emitting a
1080p rendition of it wastes CPU and disk to produce something visibly worse than the original. Cap each
rung at the source resolution and bitrate, and drop rungs that would exceed either.

A reasonable default for a self-hosted server — three rungs, not five:

| Rung | Resolution | Video bitrate | Audio |
| --- | --- | --- | --- |
| 0 | source (≤1080p) | ~5000k | 128k AAC |
| 1 | 720p | ~2800k | 128k AAC |
| 2 | 480p | ~1400k | 96k AAC |

### 4.2 One ffmpeg process, not three

Use a `split` filter so the source is decoded once and scaled N ways, with `-var_stream_map` writing the
master playlist:

```
-filter_complex "[0:v]split=3[v1][v2][v3];[v2]scale=-2:720[v2o];[v3]scale=-2:480[v3o]"
-map "[v1]" -map "[v2o]" -map "[v3o]" -map 0:a:0 -map 0:a:0 -map 0:a:0
-var_stream_map "v:0,a:0 v:1,a:1 v:2,a:2"
-master_pl_name master.m3u8
-hls_segment_filename ".../stream_%v_%03d.m4s"  .../stream_%v.m3u8
```

One decode instead of three is roughly a 30-40% saving over running separate processes.

### 4.3 Keyframes must align across renditions

This is the requirement that makes ABR work, and the one that produces "it plays but stutters when the
quality changes" when it's missed. **Every** rendition needs identical GOP structure:

```
-g <fps × segment_time> -keyint_min <same> -sc_threshold 0
-force_key_frames "expr:gte(t,n_forced*<segment_time>)"
```

The existing code already does this for its single stream — the work is applying it per-rung and not
letting the stream-copy path (§0.2) opt out.

### 4.4 The cost, stated plainly

Three renditions is roughly 2.5× the CPU and ~1.6× the storage of one. On a small home server that can
mean a 45-minute episode taking two hours. Mitigations, in order of value: `-preset veryfast` is already
a sensible choice; make the ladder configurable so a modest box can run two rungs; hardware acceleration
(§8); and let people opt out entirely with `HLS_LADDER=source`.

### 4.5 Migration

`catalog.file_path` currently points at `<name>.m3u8`; with a ladder it must point at `master.m3u8`.
Existing single-rendition titles keep working — a media playlist is a valid thing to hand a player — so
a read-time fallback plus a one-shot migration is enough. Don't re-transcode a working library.

---

## 5. Phase 3 — Subtitles

Currently `-map 0:s` appears nowhere: **every subtitle stream is dropped and the source is deleted.**
For a lot of libraries that's the single most valuable thing being lost.

- **Text subtitles** (`subrip`, `ass`, `mov_text`, `webvtt`) convert to WebVTT sidecars, one per
  language, declared in the master playlist:
  ```
  #EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English",LANGUAGE="en",AUTOSELECT=YES,URI="subs_en.m3u8"
  ```
  Each needs its own (single-segment is fine) playlist, and the variants need `SUBTITLES="subs"`.
- **Image subtitles** (`hdmv_pgs_subtitle` from Blu-ray, `dvd_subtitle`) cannot become WebVTT without
  OCR. Three honest options: skip them with a warning, burn them into a dedicated rendition
  (destructive, and a separate encode), or run OCR out of band. Recommend skip-with-warning first, and
  **say so in the UI** — silently dropping them is how the current pipeline got here.
- **Sidecar files:** pick up `Movie.en.srt` / `Movie.srt` next to the source at ingest. Cheap, and
  covers most real libraries.
- **Styling is lost** converting ASS to WebVTT — positioning and karaoke effects don't survive. Fine for
  dialogue, poor for signs/songs. Note it rather than discovering it later.
- `LANGUAGE` comes from the stream's `language` tag; fall back to `und` and let an admin fix it. Which
  means the catalog needs an editable subtitle list.

---

## 6. Phase 4 — Audio tracks

`-map 0:a:0?` takes the first track and drops the rest — dual-language releases lose one, commentary
tracks vanish.

- Map every audio stream into an `#EXT-X-MEDIA:TYPE=AUDIO` group with `LANGUAGE` and `NAME`, and point
  the variants at it with `AUDIO="aud"`.
- **Surround:** `-ac 2` is applied to everything that isn't already AAC or MP3, so AC-3/EAC3/DTS 5.1
  becomes stereo. Keep a stereo AAC track for compatibility (mandatory — it's the only universally
  supported codec) and optionally pass through EC-3 as a second rendition of the same track. DTS is not
  HLS-legal and must be transcoded.
- **Downmix quality:** ffmpeg's default stereo downmix routinely buries dialogue under the surround
  channels. A dialogue-forward downmix matrix or a `loudnorm` pass is the difference between "why is
  everyone whispering" and a usable track.

---

## 7. Phase 5 — Catalog and player

Model additions (`internal/models/catalog.go`), per movie and per episode:

```go
Renditions   []Rendition   // resolution, bitrate, codec
AudioTracks  []AudioTrack  // index, language, name, channels, codec, default
Subtitles    []Subtitle    // language, name, forced, path
```

On the player side most of this is free: video.js exposes HLS-declared subtitle and audio tracks
through `textTracks()` / `audioTracks()`, and ABR switching is handled by hls.js or, on Safari, by the
browser. What needs building:

- a **quality selector** — automatic ABR is the right default, but a manual override needs a plugin or a
  small custom menu component;
- **remembering the choice** per viewer (language especially — nobody wants to re-pick subtitles every
  episode), which pairs naturally with
  [`WATCH_PROGRESS_ROADMAP.md`](WATCH_PROGRESS_ROADMAP.md);
- the **admin UI** for editing subtitle languages and setting defaults, since probed language tags are
  frequently wrong or `und`.

---

## 8. Phase 6 — Throughput

- **Hardware acceleration.** VAAPI (Intel/AMD), NVENC, or VideoToolbox on macOS turn a 2-hour transcode
  into a 10-minute one, and matter far more once each title is encoded three times. It needs device
  passthrough in `docker-compose.yaml`/k8s and a graceful fall back to libx264 when the device is
  absent. Quality per bit is worse than a good software encode — an acceptable trade at 3× the work.
- **Progress reporting** (`-progress pipe:1`) is already specified in
  [`TELEGRAM_INGEST_ROADMAP.md`](TELEGRAM_INGEST_ROADMAP.md) §6 and matters more here, since jobs get
  substantially longer.
- **Concurrency** shares the persisted-queue prerequisite from that same roadmap (§0.1). One ffmpeg at a
  time is the right default when each job is now three encodes.
- **Don't delete the source until the whole package verifies.** `os.Remove(job.InputPath)` runs on
  ffmpeg exit status alone (`queue.go:260`). With multiple outputs there's more to get wrong — validate
  that the master playlist, every variant and every sidecar exist and are non-empty first. Better still,
  make source retention configurable; re-transcoding is impossible otherwise, and every phase above is a
  reason you might want to.

---

## 9. New configuration

| Variable | Default | Description |
| --- | --- | --- |
| `HLS_LADDER` | `720p,480p` | Extra rungs below source; `source` disables ABR |
| `HLS_PLAYLIST_TYPE` | `vod` | §0.1 |
| `SUBTITLES_EXTRACT` | `true` | Extract text subtitles to WebVTT |
| `SUBTITLES_SIDECAR` | `true` | Pick up `*.srt` next to the source |
| `AUDIO_ALL_TRACKS` | `true` | Map every audio stream, not just the first |
| `AUDIO_SURROUND_PASSTHROUGH` | `false` | Keep an EC-3 5.1 track alongside stereo AAC |
| `TONEMAP_HDR` | `true` | §0.4 |
| `FFMPEG_HWACCEL` | — | `vaapi` \| `nvenc` \| `videotoolbox`; empty = software |
| `KEEP_SOURCE` | `false` | Retain the original after a successful conversion |

---

## 10. Risks and open decisions

| Risk | Mitigation |
| --- | --- |
| 3× transcode time on modest hardware | Configurable ladder; `HLS_LADDER=source` opt-out; hwaccel |
| Misaligned keyframes → stutter on quality switch | Identical `-g`/`-force_key_frames`/`-sc_threshold` per rung (§4.3); verify segment durations post-encode |
| Existing library points at a media playlist, not a master | Read-time fallback; no forced re-transcode |
| Image subtitles can't be converted | Explicit skip + warning surfaced in the UI, not silent |
| Source deleted before the package is verified | Validate all outputs first; `KEEP_SOURCE` option |
| HDR tonemapping applied to SDR input | Gate on `color_transfer`; the filter is destructive |
| Disk usage grows ~1.6× | Document it; ladder is configurable; preflight free space |
| Stream copy assumed safe when it isn't | GOP probe (§0.2) |

**Open questions:** is ABR worth it for a LAN-only deployment (where the answer may genuinely be no, and
`HLS_LADDER=source` is the right default)? Should existing titles be re-transcoded on upgrade, or only
new ingests? And is burning in PGS subtitles worth an extra rendition, or is skip-with-warning enough?

---

## 11. Suggested sequencing

**MVP (Phases 0–3):** subtitles survive ingest and a viewer on a weak connection gets a lower rung
instead of a buffering spinner.

1. Phase 0 — VOD flag, GOP-aware stream copy, MP3/fMP4, HDR tonemapping, thumbnail selection, error
   truncation. *Small, independent, each shippable alone.*
2. Phase 1 — JSON ffprobe; persist stream metadata. *Also unblocks watch-progress duration.*
3. Phase 2 — the ladder, single-process split filter, master playlist, `file_path` migration.
4. Phase 3 — subtitle extraction and sidecar pickup. **← MVP ships here**
5. Phase 4 — multi audio track mapping and surround policy.
6. Phase 5 — catalog fields, player menus, remembered preferences, admin editing.
7. Phase 6 — hwaccel, progress, verified deletion.

Phase 0 is worth doing on its own even if none of the rest is ever built — five of its six items are
quality or correctness bugs affecting every title converted today. **Phase 3 is the one to prioritise if
you only do one thing**, because unlike the others it is not an improvement you can apply later: the
subtitle streams are gone once the source is deleted.

---

### References
- [Apple — HTTP Live Streaming authoring specification](https://developer.apple.com/documentation/http-live-streaming/hls-authoring-specification-for-apple-devices)
- [ffmpeg — HLS muxer](https://ffmpeg.org/ffmpeg-formats.html#hls-2) (`var_stream_map`, `master_pl_name`)
- [ffmpeg — tonemapping](https://trac.ffmpeg.org/wiki/colorspace) · [Hardware acceleration](https://trac.ffmpeg.org/wiki/HWAccelIntro)
- Related: [`TELEGRAM_INGEST_ROADMAP.md`](TELEGRAM_INGEST_ROADMAP.md) §0.1 (persisted queue) ·
  [`WATCH_PROGRESS_ROADMAP.md`](WATCH_PROGRESS_ROADMAP.md) §0.1 (duration)
