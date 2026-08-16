package config

import (
	"strings"
	"testing"
	"time"
)

func TestDefault(t *testing.T) {
	c := Default()
	if c.SeedURL == "" || c.UserAgent == "" || c.OutputDir == "" || c.ManifestPath == "" {
		t.Fatalf("defaults must not be empty: %+v", c)
	}
	if c.Timeout <= 0 || c.MaxFileSize <= 0 || c.Politeness < 0 {
		t.Fatalf("defaults out of range: %+v", c)
	}
	if c.Concurrency != 0 {
		t.Fatalf("default concurrency should be auto (0), got %d", c.Concurrency)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("default config must validate: %v", err)
	}
}

func TestAutoConcurrency(t *testing.T) {
	cases := []struct{ in, want int }{
		{8, 4},
		{10, 6},
		{2, 1},
		{1, 1},
	}
	for _, tc := range cases {
		if got := AutoConcurrency(tc.in); got != tc.want {
			t.Errorf("AutoConcurrency(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
	// Non-positive inputs fall back to NumCPU but always yield >= 1 worker.
	if got := AutoConcurrency(-5); got < 1 {
		t.Errorf("AutoConcurrency(-5) = %d, want >= 1", got)
	}
	if got := AutoConcurrency(0); got < 1 {
		t.Errorf("AutoConcurrency(0) = %d, want >= 1", got)
	}
}

func TestResolveConcurrency(t *testing.T) {
	c := Default()
	c.Concurrency = 7
	if got := c.ResolveConcurrency(); got != 7 {
		t.Errorf("explicit concurrency: got %d, want 7", got)
	}
	c.Concurrency = 0
	if got := c.ResolveConcurrency(); got < 1 {
		t.Errorf("auto concurrency: got %d, want >= 1", got)
	}
}

func TestValidate(t *testing.T) {
	valid := Default()
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"valid", func(c *Config) {}, ""},
		{"bad scheme", func(c *Config) { c.SeedURL = "ftp://example.com/x" }, "scheme"},
		{"missing host", func(c *Config) { c.SeedURL = "https:///x" }, "missing host"},
		{"bad url", func(c *Config) { c.SeedURL = "://bad" }, "seed url"},
		{"empty output", func(c *Config) { c.OutputDir = "" }, "output dir"},
		{"neg concurrency", func(c *Config) { c.Concurrency = -1 }, "concurrency"},
		{"neg depth", func(c *Config) { c.MaxDepth = -1 }, "max depth"},
		{"zero timeout", func(c *Config) { c.Timeout = 0 }, "timeout"},
		{"neg retries", func(c *Config) { c.Retries = -1 }, "retries"},
		{"empty ua", func(c *Config) { c.UserAgent = "" }, "user agent"},
		{"neg politeness", func(c *Config) { c.Politeness = -1 }, "politeness"},
		{"zero max size", func(c *Config) { c.MaxFileSize = 0 }, "max file size"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.mutate(&c)
			err := c.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected valid, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestApplyEnv(t *testing.T) {
	env := map[string]string{
		"VELLUM_SEED_URL":      "https://example.com/seed/",
		"VELLUM_OUTPUT_DIR":    "out",
		"VELLUM_MANIFEST_PATH": "out/m.json",
		"VELLUM_USER_AGENT":    "ua",
		"VELLUM_CONCURRENCY":   "3",
		"VELLUM_MAX_DEPTH":     "5",
		"VELLUM_RETRIES":       "2",
		"VELLUM_RESUME":        "true",
		"VELLUM_DRY_RUN":       "true",
		"VELLUM_VERBOSE":       "true",
		"VELLUM_TIMEOUT":       "5s",
		"VELLUM_POLITENESS":    "10ms",
		"VELLUM_MAX_FILE_SIZE": "12345",
	}
	cfg := Default()
	if err := ApplyEnv(&cfg, func(k string) string { return env[k] }); err != nil {
		t.Fatalf("apply env: %v", err)
	}
	if cfg.SeedURL != "https://example.com/seed/" {
		t.Errorf("seed url = %q", cfg.SeedURL)
	}
	if cfg.OutputDir != "out" || cfg.ManifestPath != "out/m.json" || cfg.UserAgent != "ua" {
		t.Errorf("string overrides not applied: %+v", cfg)
	}
	if cfg.Concurrency != 3 || cfg.MaxDepth != 5 || cfg.Retries != 2 {
		t.Errorf("int overrides not applied: %+v", cfg)
	}
	if !cfg.Resume || !cfg.DryRun || !cfg.Verbose {
		t.Errorf("bool overrides not applied: %+v", cfg)
	}
	if cfg.Timeout != 5*time.Second || cfg.Politeness != 10*time.Millisecond {
		t.Errorf("duration overrides not applied: %+v", cfg)
	}
	if cfg.MaxFileSize != 12345 {
		t.Errorf("size override not applied: %d", cfg.MaxFileSize)
	}
}

func TestApplyEnvErrors(t *testing.T) {
	cases := []map[string]string{
		{"VELLUM_CONCURRENCY": "notanint"},
		{"VELLUM_RESUME": "notabool"},
		{"VELLUM_TIMEOUT": "notaduration"},
		{"VELLUM_MAX_FILE_SIZE": "notasize"},
	}
	for _, env := range cases {
		cfg := Default()
		if err := ApplyEnv(&cfg, func(k string) string { return env[k] }); err == nil {
			t.Errorf("expected error for env %v", env)
		}
	}
	if err := ApplyEnv(nil, func(string) string { return "" }); err == nil {
		t.Error("expected error for nil receiver")
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("VELLUM_SEED_URL", "https://env.example.com/")
	t.Setenv("VELLUM_CONCURRENCY", "9")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("from env: %v", err)
	}
	if cfg.SeedURL != "https://env.example.com/" || cfg.Concurrency != 9 {
		t.Fatalf("env not applied: %+v", cfg)
	}
}

func TestFromEnvError(t *testing.T) {
	t.Setenv("VELLUM_CONCURRENCY", "notanint")
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestParseKinds(t *testing.T) {
	set := ParseKinds("pdf, html ,video-text")
	if len(set) != 3 || !set["pdf"] || !set["html"] || !set["video-text"] {
		t.Fatalf("set = %v", set)
	}
	if len(ParseKinds("")) != 0 {
		t.Fatal("empty string must yield empty set")
	}
	if len(ParseKinds("  ,  ")) != 0 {
		t.Fatal("whitespace-only must yield empty set")
	}
}

func TestHasKind(t *testing.T) {
	cfg := Default()
	if !cfg.HasKind("pdf") || !cfg.HasKind("html") || !cfg.HasKind("video-text") {
		t.Fatalf("default kinds should include pdf, html and video-text: %q", cfg.Kinds)
	}
}
