package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnvAndResolveListenAddr(t *testing.T) {
	t.Run("missing .env uses default port", func(t *testing.T) {
		t.Setenv("PORT", "")
		if err := loadDotEnv(filepath.Join(t.TempDir(), ".env")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := resolveListenAddr(""); got != ":8180" {
			t.Fatalf("got %q, want %q", got, ":8180")
		}
	})

	t.Run("PORT in .env sets listen port", func(t *testing.T) {
		// Unset PORT so loadDotEnv can populate it.
		orig, hadOrig := os.LookupEnv("PORT")
		_ = os.Unsetenv("PORT")
		t.Cleanup(func() {
			if hadOrig {
				_ = os.Setenv("PORT", orig)
			} else {
				_ = os.Unsetenv("PORT")
			}
		})

		envPath := filepath.Join(t.TempDir(), ".env")
		content := "# comment\nLANESIEGE_ADMIN_TOKEN=secret\nPORT=\"9090\"\n"
		if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := loadDotEnv(envPath); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := resolveListenAddr(""); got != ":9090" {
			t.Fatalf("got %q, want %q", got, ":9090")
		}
	})

	t.Run("empty PORT in .env falls back to default", func(t *testing.T) {
		orig, hadOrig := os.LookupEnv("PORT")
		_ = os.Unsetenv("PORT")
		t.Cleanup(func() {
			if hadOrig {
				_ = os.Setenv("PORT", orig)
			} else {
				_ = os.Unsetenv("PORT")
			}
		})

		envPath := filepath.Join(t.TempDir(), ".env")
		if err := os.WriteFile(envPath, []byte("PORT=\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := loadDotEnv(envPath); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := resolveListenAddr(""); got != ":8180" {
			t.Fatalf("got %q, want %q", got, ":8180")
		}
	})

	t.Run("existing env var overrides .env", func(t *testing.T) {
		t.Setenv("PORT", "7000")
		envPath := filepath.Join(t.TempDir(), ".env")
		if err := os.WriteFile(envPath, []byte("PORT=9090\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := loadDotEnv(envPath); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := resolveListenAddr(""); got != ":7000" {
			t.Fatalf("got %q, want %q", got, ":7000")
		}
	})

	t.Run("addr flag overrides PORT", func(t *testing.T) {
		t.Setenv("PORT", "9090")
		if got := resolveListenAddr("127.0.0.1:8080"); got != "127.0.0.1:8080" {
			t.Fatalf("got %q, want %q", got, "127.0.0.1:8080")
		}
	})
}
