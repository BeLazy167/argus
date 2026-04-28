// Package app — memprof.go: background memory telemetry + threshold heap snapshots.
//
// Two concerns:
//  1. Periodic sampler logs process RSS + Go heap stats + goroutine count every
//     memProfInterval at Info level. Cheap (~microseconds). Correlates memory
//     trends to stage activity in the fly logs tail.
//  2. Threshold-triggered heap snapshot. When sampled RSS crosses
//     rssThresholdMB, write a gzipped pprof heap profile to dumpDir and log
//     the path + size. Throttled: no more than one snapshot per cooldown to
//     avoid log flooding during a sustained spike.
//
// On Fly without a volume, /tmp is ephemeral — a subsequent restart loses the
// file. To survive restart, the log line includes the absolute path AND an
// optional env knob (MEMPROF_LOG_BASE64=1) that additionally emits the
// compressed profile as chunked base64 log lines so forensics can be
// reassembled from the log stream. Default off (chunked-base64 is log-heavy).
package app

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"
)

// memProfConfig holds all memprof tuning knobs, resolved from env at startup.
type memProfConfig struct {
	Enabled        bool
	Interval       time.Duration
	RSSThresholdMB uint64
	Cooldown       time.Duration
	DumpDir        string
	LogBase64      bool
}

// defaults — referenced by tests.
const (
	defaultMemProfEnabled        = true
	defaultMemProfIntervalSec    = 30
	defaultMemProfRSSThresholdMB = 400
	defaultMemProfCooldownSec    = 300
	defaultMemProfDumpDir        = "/tmp"
)

// loadMemProfConfig reads env vars and falls back to sensible defaults.
//
// Env keys:
//
//	MEMPROF_ENABLED            bool   (default true)
//	MEMPROF_INTERVAL_SEC       int    (default 30)
//	MEMPROF_RSS_THRESHOLD_MB   int    (default 400)
//	MEMPROF_COOLDOWN_SEC       int    (default 300)
//	MEMPROF_DUMP_DIR           string (default /tmp)
//	MEMPROF_LOG_BASE64         bool   (default false)
//
// Any unparseable value silently falls back to the default — this is
// instrumentation, not business logic, and we must never block startup.
func loadMemProfConfig() memProfConfig {
	return memProfConfig{
		Enabled:        envBool("MEMPROF_ENABLED", defaultMemProfEnabled),
		Interval:       time.Duration(envInt("MEMPROF_INTERVAL_SEC", defaultMemProfIntervalSec)) * time.Second,
		RSSThresholdMB: uint64(envInt("MEMPROF_RSS_THRESHOLD_MB", defaultMemProfRSSThresholdMB)),
		Cooldown:       time.Duration(envInt("MEMPROF_COOLDOWN_SEC", defaultMemProfCooldownSec)) * time.Second,
		DumpDir:        envStr("MEMPROF_DUMP_DIR", defaultMemProfDumpDir),
		LogBase64:      envBool("MEMPROF_LOG_BASE64", false),
	}
}

// StartMemoryProfiler launches the background sampler goroutine.
//
// The goroutine exits only when ctx is cancelled. A deferred recover ensures
// a panic inside the profiler cannot crash the main process.
func StartMemoryProfiler(ctx context.Context, logger *slog.Logger) {
	cfg := loadMemProfConfig()
	if !cfg.Enabled {
		logger.Info("[memprof] disabled")
		return
	}
	logger.Info("[memprof] enabled",
		"interval", cfg.Interval.String(),
		"rss_threshold_mb", cfg.RSSThresholdMB,
		"cooldown", cfg.Cooldown.String(),
		"dump_dir", cfg.DumpDir,
		"log_base64", cfg.LogBase64,
	)
	go memProfLoop(ctx, logger, cfg)
}

func memProfLoop(ctx context.Context, logger *slog.Logger, cfg memProfConfig) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("[memprof] panic",
				"recover", r,
				"stack", string(debug.Stack()),
			)
		}
	}()

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	var lastDump time.Time
	for {
		select {
		case <-ctx.Done():
			// Emit a final sample so the log stream ends with the final RSS
			// trajectory — useful right before an OOM kill.
			sampleAndLog(logger, cfg, &lastDump)
			logger.Info("[memprof] stopped")
			return
		case <-ticker.C:
			sampleAndLog(logger, cfg, &lastDump)
		}
	}
}

// sampleAndLog takes one RSS + runtime sample, logs it, and may trigger a
// heap-snapshot dump. lastDump is mutated in place when a dump is taken.
func sampleAndLog(logger *slog.Logger, cfg memProfConfig, lastDump *time.Time) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	rssMB, rssOK := readRSSMB()

	logger.Info("[memprof] sample",
		"rss_mb", rssMB,
		"rss_source", rssSourceName(rssOK),
		"heap_alloc_mb", m.HeapAlloc/(1<<20),
		"heap_inuse_mb", m.HeapInuse/(1<<20),
		"sys_mb", m.Sys/(1<<20),
		"num_gc", m.NumGC,
		"pause_total_ms", m.PauseTotalNs/uint64(time.Millisecond),
		"goroutines", runtime.NumGoroutine(),
	)

	if rssMB < cfg.RSSThresholdMB {
		return
	}
	if !lastDump.IsZero() && time.Since(*lastDump) < cfg.Cooldown {
		logger.Info("[memprof] threshold crossed but in cooldown",
			"rss_mb", rssMB,
			"threshold_mb", cfg.RSSThresholdMB,
			"cooldown_remaining_sec", int((cfg.Cooldown - time.Since(*lastDump)).Seconds()),
		)
		return
	}

	path, size, err := writeHeapSnapshot(cfg.DumpDir)
	if err != nil {
		logger.Warn("[memprof] snapshot failed",
			"error", err,
			"rss_mb", rssMB,
		)
		return
	}
	*lastDump = time.Now()
	logger.Warn("[memprof] heap snapshot written",
		"path", path,
		"size_bytes", size,
		"rss_mb", rssMB,
		"threshold_mb", cfg.RSSThresholdMB,
	)

	if cfg.LogBase64 {
		if err := logBase64Chunks(logger, path); err != nil {
			logger.Warn("[memprof] base64 emit failed", "error", err)
		}
	}
}

// rssSourceName returns a short tag describing which RSS source was used.
func rssSourceName(fromProc bool) string {
	if fromProc {
		return "proc"
	}
	return "memstats"
}

// readRSSMB returns the process RSS in MiB. On Linux it parses
// /proc/self/status's VmRSS line; elsewhere it falls back to
// runtime.MemStats.Sys (imprecise, but always non-zero). The bool signals
// whether the value came from /proc.
func readRSSMB() (uint64, bool) {
	if runtime.GOOS == "linux" {
		if kb, err := readLinuxRSSKB("/proc/self/status"); err == nil {
			return kb / 1024, true
		}
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Sys / (1 << 20), false
}

// readLinuxRSSKB parses a Linux /proc/<pid>/status file and returns the VmRSS
// value in KiB. Returns an error if the file is missing or the line absent.
// Exposed for testing.
func readLinuxRSSKB(path string) (uint64, error) {
	f, err := os.Open(path) //nolint:gosec // well-known proc path
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		// Expected: ["VmRSS:", "<N>", "kB"]
		if len(fields) < 2 {
			return 0, fmt.Errorf("malformed VmRSS line: %q", line)
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse VmRSS kB %q: %w", fields[1], err)
		}
		return kb, nil
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("scan %s: %w", path, err)
	}
	return 0, errors.New("VmRSS line not found")
}

// writeHeapSnapshot writes a gzipped pprof heap profile to dir and returns the
// absolute path and size in bytes.
func writeHeapSnapshot(dir string) (string, int64, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	name := fmt.Sprintf("argus-heap-%s.pprof.gz", time.Now().UTC().Format("20060102T150405"))
	path := filepath.Join(dir, name)
	f, err := os.Create(path) //nolint:gosec // path built from trusted dir + timestamp
	if err != nil {
		return "", 0, fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()

	gzw := gzip.NewWriter(f)
	// Force a GC so the snapshot reflects reachable memory rather than
	// lingering garbage; matches `go tool pprof`'s assumption.
	runtime.GC()
	if err := pprof.WriteHeapProfile(gzw); err != nil {
		_ = gzw.Close()
		return "", 0, fmt.Errorf("WriteHeapProfile: %w", err)
	}
	if err := gzw.Close(); err != nil {
		return "", 0, fmt.Errorf("gzip close: %w", err)
	}
	if err := f.Sync(); err != nil {
		return "", 0, fmt.Errorf("fsync: %w", err)
	}
	fi, err := f.Stat()
	if err != nil {
		return path, 0, fmt.Errorf("stat: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return abs, fi.Size(), nil
}

// logBase64Chunks emits the snapshot file as chunked base64 log lines so it
// can be recovered from the fly log stream after a restart wipes /tmp.
// Chunks are 2 KiB of raw bytes → ~2.7 KiB of base64 text per log line.
func logBase64Chunks(logger *slog.Logger, path string) error {
	const chunkBytes = 2 << 10
	f, err := os.Open(path) //nolint:gosec // path produced by writeHeapSnapshot
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	buf := make([]byte, chunkBytes)
	seq := 0
	for {
		n, err := f.Read(buf)
		if n > 0 {
			logger.Info("[memprof] heap_b64",
				"path", path,
				"seq", seq,
				"data", base64.StdEncoding.EncodeToString(buf[:n]),
			)
			seq++
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
	}
}

// --- env helpers (duplicated locally so memprof stays decoupled from config) ---

func envStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return fallback
}
