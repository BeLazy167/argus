package config

import (
	"os"
	"strings"
	"testing"
)

func TestGetEnv(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		fallback string
		envVal   string
		setEnv   bool
		want     string
	}{
		{
			name:     "returns value when set",
			key:      "TEST_GETENV_SET",
			fallback: "default",
			envVal:   "custom",
			setEnv:   true,
			want:     "custom",
		},
		{
			name:     "returns fallback when not set",
			key:      "TEST_GETENV_UNSET",
			fallback: "default",
			setEnv:   false,
			want:     "default",
		},
		{
			name:     "returns fallback for empty value",
			key:      "TEST_GETENV_EMPTY",
			fallback: "default",
			envVal:   "",
			setEnv:   true,
			want:     "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv(tt.key, tt.envVal)
			} else {
				os.Unsetenv(tt.key)
			}

			got := getEnv(tt.key, tt.fallback)
			if got != tt.want {
				t.Errorf("getEnv(%q, %q) = %q, want %q", tt.key, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestRequireEnv(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		envVal  string
		setEnv  bool
		want    string
		wantErr bool
	}{
		{
			name:   "returns value when set",
			key:    "TEST_REQUIRE_SET",
			envVal: "secret",
			setEnv: true,
			want:   "secret",
		},
		{
			name:    "error when not set",
			key:     "TEST_REQUIRE_UNSET",
			setEnv:  false,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv(tt.key, tt.envVal)
			} else {
				os.Unsetenv(tt.key)
			}

			got, err := requireEnv(tt.key)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.key) {
					t.Errorf("error %q should contain key name %q", err, tt.key)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("requireEnv(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	setRequired := func(t *testing.T) {
		t.Helper()
		t.Setenv("DATABASE_URL", "postgres://localhost/test")
		t.Setenv("GITHUB_WEBHOOK_SECRET", "whsec_test")
		t.Setenv("GITHUB_PRIVATE_KEY", "fake-key")
	}

	t.Run("all required vars set", func(t *testing.T) {
		setRequired(t)
		t.Setenv("GITHUB_APP_ID", "12345")
		t.Setenv("PORT", "9090")
		t.Setenv("ENV", "production")
		t.Setenv("MAX_CONCURRENT_REVIEWS", "5")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Port != 9090 {
			t.Errorf("Port = %d, want 9090", cfg.Port)
		}
		if cfg.Env != "production" {
			t.Errorf("Env = %q, want %q", cfg.Env, "production")
		}
		if cfg.DatabaseURL != "postgres://localhost/test" {
			t.Errorf("DatabaseURL = %q, want %q", cfg.DatabaseURL, "postgres://localhost/test")
		}
		if cfg.GitHubAppID != 12345 {
			t.Errorf("GitHubAppID = %d, want 12345", cfg.GitHubAppID)
		}
		if string(cfg.GitHubPrivateKey) != "fake-key" {
			t.Errorf("GitHubPrivateKey = %q, want %q", cfg.GitHubPrivateKey, "fake-key")
		}
		if cfg.GitHubWebhookSecret != "whsec_test" {
			t.Errorf("GitHubWebhookSecret = %q, want %q", cfg.GitHubWebhookSecret, "whsec_test")
		}
		if cfg.MaxConcurrentReviews != 5 {
			t.Errorf("MaxConcurrentReviews = %d, want 5", cfg.MaxConcurrentReviews)
		}
	})

	t.Run("defaults applied", func(t *testing.T) {
		setRequired(t)
		os.Unsetenv("PORT")
		os.Unsetenv("ENV")
		os.Unsetenv("MAX_CONCURRENT_REVIEWS")
		os.Unsetenv("GITHUB_APP_ID")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Port != 8080 {
			t.Errorf("Port = %d, want default 8080", cfg.Port)
		}
		if cfg.Env != "development" {
			t.Errorf("Env = %q, want default %q", cfg.Env, "development")
		}
		if cfg.MaxConcurrentReviews != 10 {
			t.Errorf("MaxConcurrentReviews = %d, want default 10", cfg.MaxConcurrentReviews)
		}
	})

	t.Run("missing DATABASE_URL", func(t *testing.T) {
		os.Unsetenv("DATABASE_URL")
		t.Setenv("GITHUB_WEBHOOK_SECRET", "whsec_test")
		t.Setenv("GITHUB_PRIVATE_KEY", "fake-key")

		_, err := Load()
		if err == nil {
			t.Fatal("expected error for missing DATABASE_URL")
		}
		if !strings.Contains(err.Error(), "DATABASE_URL") {
			t.Errorf("error %q should mention DATABASE_URL", err)
		}
	})

	t.Run("missing GITHUB_WEBHOOK_SECRET", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://localhost/test")
		os.Unsetenv("GITHUB_WEBHOOK_SECRET")
		t.Setenv("GITHUB_PRIVATE_KEY", "fake-key")

		_, err := Load()
		if err == nil {
			t.Fatal("expected error for missing GITHUB_WEBHOOK_SECRET")
		}
		if !strings.Contains(err.Error(), "GITHUB_WEBHOOK_SECRET") {
			t.Errorf("error %q should mention GITHUB_WEBHOOK_SECRET", err)
		}
	})

	t.Run("invalid PORT", func(t *testing.T) {
		setRequired(t)
		t.Setenv("PORT", "not-a-number")

		_, err := Load()
		if err == nil {
			t.Fatal("expected error for invalid PORT")
		}
		if !strings.Contains(err.Error(), "PORT") {
			t.Errorf("error %q should mention PORT", err)
		}
	})

	t.Run("invalid GITHUB_APP_ID", func(t *testing.T) {
		setRequired(t)
		t.Setenv("GITHUB_APP_ID", "not-a-number")

		_, err := Load()
		if err == nil {
			t.Fatal("expected error for invalid GITHUB_APP_ID")
		}
		if !strings.Contains(err.Error(), "GITHUB_APP_ID") {
			t.Errorf("error %q should mention GITHUB_APP_ID", err)
		}
	})
}
