package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkipReleaseCheckRoundTripsAndIsOmittedWhenFalse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	SetPathOverride(path)
	t.Cleanup(func() { SetPathOverride("") })

	cfg := &Config{
		APIVersion:     APIVersion,
		CurrentContext: "local",
		Contexts: map[string]*Context{
			"local": {Server: "http://localhost:8000", SkipReleaseCheck: true},
			"cloud": {Server: "https://cloud.example.com"},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "skipReleaseCheck"); n != 1 {
		t.Fatalf("skipReleaseCheck appears %d times, want once (only the true one):\n%s", n, data)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.Contexts["local"].SkipReleaseCheck || loaded.Contexts["cloud"].SkipReleaseCheck {
		t.Fatalf("loaded local=%v cloud=%v, want true/false",
			loaded.Contexts["local"].SkipReleaseCheck, loaded.Contexts["cloud"].SkipReleaseCheck)
	}
}

func TestPathOverrideReportsTheOverride(t *testing.T) {
	t.Cleanup(func() { SetPathOverride("") })
	SetPathOverride("/some/where")
	if got := PathOverride(); got != "/some/where" {
		t.Fatalf("PathOverride() = %q", got)
	}
	SetPathOverride("")
	if got := PathOverride(); got != "" {
		t.Fatalf("PathOverride() after reset = %q", got)
	}
}

func TestDirIsDotQctlUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	SetPathOverride(filepath.Join(t.TempDir(), "elsewhere"))
	t.Cleanup(func() { SetPathOverride("") })

	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ConfigDirName); dir != want {
		t.Fatalf("Dir() = %q, want %q (--config must not move it)", dir, want)
	}
}
