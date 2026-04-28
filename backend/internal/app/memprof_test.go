package app

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseLinuxRSS(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    uint64
		wantErr bool
	}{
		{
			name: "happy path",
			content: "Name:\targus\n" +
				"State:\tR (running)\n" +
				"VmRSS:\t  123456 kB\n" +
				"VmData:\t  200000 kB\n",
			want: 123456,
		},
		{
			name:    "missing VmRSS line",
			content: "Name:\targus\nVmPeak:\t  500000 kB\n",
			wantErr: true,
		},
		{
			name:    "malformed VmRSS value",
			content: "VmRSS:\tnot_a_number kB\n",
			wantErr: true,
		},
		{
			name:    "VmRSS line but no value",
			content: "VmRSS:\n",
			wantErr: true,
		},
		{
			name:    "empty file",
			content: "",
			wantErr: true,
		},
		{
			name:    "VmRSS at bottom of file",
			content: "X: 1\nY: 2\nVmRSS:\t42 kB\n",
			want:    42,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "status")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("write temp status: %v", err)
			}
			got, err := readLinuxRSSKB(path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("readLinuxRSSKB(%q) = %d, nil; want error", tt.name, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("readLinuxRSSKB(%q): %v", tt.name, err)
			}
			if got != tt.want {
				t.Fatalf("readLinuxRSSKB(%q) = %d; want %d", tt.name, got, tt.want)
			}
		})
	}
}

func TestReadLinuxRSSKB_MissingFile(t *testing.T) {
	_, err := readLinuxRSSKB(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestMemProfConfig_DefaultsWhenEnvUnset(t *testing.T) {
	// Isolate from the real environment.
	for _, k := range []string{
		"MEMPROF_ENABLED",
		"MEMPROF_INTERVAL_SEC",
		"MEMPROF_RSS_THRESHOLD_MB",
		"MEMPROF_COOLDOWN_SEC",
		"MEMPROF_DUMP_DIR",
		"MEMPROF_LOG_BASE64",
	} {
		t.Setenv(k, "")
	}

	cfg := loadMemProfConfig()

	if !cfg.Enabled {
		t.Error("Enabled should default to true")
	}
	if cfg.Interval != time.Duration(defaultMemProfIntervalSec)*time.Second {
		t.Errorf("Interval = %v; want %v", cfg.Interval, time.Duration(defaultMemProfIntervalSec)*time.Second)
	}
	if cfg.RSSThresholdMB != defaultMemProfRSSThresholdMB {
		t.Errorf("RSSThresholdMB = %d; want %d", cfg.RSSThresholdMB, defaultMemProfRSSThresholdMB)
	}
	if cfg.Cooldown != time.Duration(defaultMemProfCooldownSec)*time.Second {
		t.Errorf("Cooldown = %v; want %v", cfg.Cooldown, time.Duration(defaultMemProfCooldownSec)*time.Second)
	}
	if cfg.DumpDir != defaultMemProfDumpDir {
		t.Errorf("DumpDir = %q; want %q", cfg.DumpDir, defaultMemProfDumpDir)
	}
	if cfg.LogBase64 {
		t.Error("LogBase64 should default to false")
	}
}

func TestMemProfConfig_EnvOverrides(t *testing.T) {
	t.Setenv("MEMPROF_ENABLED", "false")
	t.Setenv("MEMPROF_INTERVAL_SEC", "10")
	t.Setenv("MEMPROF_RSS_THRESHOLD_MB", "200")
	t.Setenv("MEMPROF_COOLDOWN_SEC", "60")
	t.Setenv("MEMPROF_DUMP_DIR", "/var/tmp/xx")
	t.Setenv("MEMPROF_LOG_BASE64", "1")

	cfg := loadMemProfConfig()
	if cfg.Enabled {
		t.Error("Enabled override = false failed")
	}
	if cfg.Interval != 10*time.Second {
		t.Errorf("Interval = %v; want 10s", cfg.Interval)
	}
	if cfg.RSSThresholdMB != 200 {
		t.Errorf("RSSThresholdMB = %d; want 200", cfg.RSSThresholdMB)
	}
	if cfg.Cooldown != 60*time.Second {
		t.Errorf("Cooldown = %v; want 60s", cfg.Cooldown)
	}
	if cfg.DumpDir != "/var/tmp/xx" {
		t.Errorf("DumpDir = %q; want /var/tmp/xx", cfg.DumpDir)
	}
	if !cfg.LogBase64 {
		t.Error("LogBase64 override = true failed")
	}
}

func TestMemProfConfig_BadIntFallsBack(t *testing.T) {
	t.Setenv("MEMPROF_INTERVAL_SEC", "abc")
	t.Setenv("MEMPROF_RSS_THRESHOLD_MB", "-5")
	cfg := loadMemProfConfig()
	if cfg.Interval != time.Duration(defaultMemProfIntervalSec)*time.Second {
		t.Errorf("bad int should fall back; got %v", cfg.Interval)
	}
	if cfg.RSSThresholdMB != defaultMemProfRSSThresholdMB {
		t.Errorf("negative int should fall back; got %d", cfg.RSSThresholdMB)
	}
}

// TestSampleFormat asserts the sampler emits the expected keys. We capture
// JSON logs into a buffer; the values can be anything, but the keys are the
// contract operators rely on when grepping fly logs.
func TestSampleFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := memProfConfig{
		RSSThresholdMB: 1 << 40, // unreachable → no snapshot
		Cooldown:       time.Hour,
		DumpDir:        t.TempDir(),
	}
	var lastDump time.Time
	sampleAndLog(logger, cfg, &lastDump)

	got := buf.String()
	for _, key := range []string{
		`"rss_mb"`,
		`"heap_alloc_mb"`,
		`"heap_inuse_mb"`,
		`"sys_mb"`,
		`"num_gc"`,
		`"goroutines"`,
		`"[memprof] sample"`,
	} {
		if !strings.Contains(got, key) {
			t.Errorf("sample log missing key %s; got %s", key, got)
		}
	}
	if !lastDump.IsZero() {
		t.Error("lastDump should remain zero when threshold is not crossed")
	}
}

// TestWriteHeapSnapshot exercises the full dump path. Runs on all OSes since
// it only uses runtime/pprof + gzip; no /proc required.
func TestWriteHeapSnapshot(t *testing.T) {
	dir := t.TempDir()
	path, size, err := writeHeapSnapshot(dir)
	if err != nil {
		t.Fatalf("writeHeapSnapshot: %v", err)
	}
	if size <= 0 {
		t.Fatalf("size = %d; want > 0", size)
	}
	if !strings.HasPrefix(filepath.Base(path), "argus-heap-") {
		t.Errorf("unexpected filename: %s", path)
	}
	if !strings.HasSuffix(path, ".pprof.gz") {
		t.Errorf("filename missing .pprof.gz suffix: %s", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != size {
		t.Errorf("reported size %d != actual %d", size, info.Size())
	}
}
