# Memory profiler & OOM forensics

Argus runs on Fly `shared-cpu-1x:512MB`. The backend has been OOM-killed under
graph-indexing load, which orphans in-flight reviews. This module provides:

- **Passive sampler** — every 30s, logs RSS + Go heap + goroutine count so you
  can correlate memory trends with stage activity in `fly logs tail`.
- **Threshold-triggered snapshot** — when sampled RSS crosses 400 MB
  (configurable), writes a gzipped pprof heap profile to `/tmp`. Throttled to
  one dump per 5 min so a sustained spike does not flood logs.
- **On-demand endpoints** — standard `net/http/pprof` surface under
  `/debug/pprof/*`, gated by the `X-Admin-Token` header.

## Config (all optional)

| Env var | Default | Notes |
|---|---|---|
| `MEMPROF_ENABLED` | `true` | Set to `false` to disable the sampler. |
| `MEMPROF_INTERVAL_SEC` | `30` | Sample cadence. |
| `MEMPROF_RSS_THRESHOLD_MB` | `400` | Dump when RSS crosses this. |
| `MEMPROF_COOLDOWN_SEC` | `300` | Minimum gap between dumps. |
| `MEMPROF_DUMP_DIR` | `/tmp` | Where `argus-heap-*.pprof.gz` files land. |
| `MEMPROF_LOG_BASE64` | `false` | Also emit the dump as chunked base64 log lines. Log-heavy; only enable when you expect to lose the file to a restart. |
| `ADMIN_DEBUG_TOKEN` | _(unset)_ | Required to enable `/debug/pprof/*`. When unset, the routes return 404 (we do not advertise the surface). |

## Retrieving a heap dump from a live machine

```bash
# Attach to the running Fly machine.
fly ssh console -a argus-ai

# Inside the machine:
ls /tmp/argus-heap-*.pprof.gz
# Copy one out — use sftp from your laptop:
fly sftp get -a argus-ai /tmp/argus-heap-YYYYMMDDTHHMMSS.pprof.gz
```

If the machine has already restarted, `/tmp` is gone. Grep the log stream for
`[memprof] heap_b64` (requires `MEMPROF_LOG_BASE64=1`) and reassemble:

```bash
fly logs -a argus-ai | grep heap_b64 | jq -r .data | base64 -d > heap.pprof.gz
```

## Analyzing a dump

```bash
go tool pprof -http=:8081 argus-heap-YYYYMMDDTHHMMSS.pprof.gz
```

Or from the CLI:

```bash
go tool pprof argus-heap-*.pprof.gz
(pprof) top 20
(pprof) list <suspected-function>
```

## On-demand (live) profiling

Requires `ADMIN_DEBUG_TOKEN` to be set on the running machine:

```bash
curl -H "X-Admin-Token: $ADMIN_DEBUG_TOKEN" \
  https://argus-ai.fly.dev/debug/pprof/heap > heap.pprof

# 30-second CPU profile
curl -H "X-Admin-Token: $ADMIN_DEBUG_TOKEN" \
  'https://argus-ai.fly.dev/debug/pprof/profile?seconds=30' > cpu.pprof

# Goroutines
curl -H "X-Admin-Token: $ADMIN_DEBUG_TOKEN" \
  'https://argus-ai.fly.dev/debug/pprof/goroutine?debug=2'
```

Available endpoints: `/debug/pprof/` (index), `heap`, `goroutine`, `allocs`,
`block`, `mutex`, `threadcreate`, `profile` (CPU), `trace`, `cmdline`, `symbol`.

## What to look for after an OOM

1. `fly logs -a argus-ai | grep memprof` — trace RSS trajectory. Look for
   `[memprof] heap snapshot written` lines with path + size.
2. If `MEMPROF_LOG_BASE64=1` was on, grep `heap_b64` chunks.
3. Correlate sample timestamps with the last triage/review/specialist stage
   log line before the kill. The RSS that mattered is the value at the
   snapshot, not at `ReadMemStats` time (Go-heap can be much smaller than
   process RSS on systems with fragmented allocators).
