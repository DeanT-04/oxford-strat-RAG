// Package config defines the scraper configuration: safe defaults,
// environment overrides, and validation. It is deliberately free of any
// I/O other than the injected getenv function so it is trivially testable.
package config

import (
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"time"
)

// Defaults. Kept as exported constants so tests and docs can reference them.
const (
	DefaultSeedURL     = "https://oxfordstrat.com/resources/"
	DefaultUserAgent   = "Vellum/1.0 (+https://github.com/DeanT-04/oxford-strat-RAG)"
	DefaultOutputDir   = "data"
	DefaultManifest    = "data/manifest.json"
	DefaultMaxDepth    = 2
	DefaultTimeout     = 30 * time.Second
	DefaultRetries     = 3
	DefaultPoliteness  = 250 * time.Millisecond
	DefaultMaxFileSize = 512 << 20 // 512 MiB per file
)

// Config holds every tunable for a scrape run. Zero values are never valid;
// construct through Default and then overlay flags/env before Validate.
type Config struct {
	SeedURL      string        // starting page to crawl
	OutputDir    string        // directory that receives downloaded files
	ManifestPath string        // path of the JSON manifest written at the end
	Concurrency  int           // download workers; <=0 means auto (60% of NumCPU)
	MaxDepth     int           // BFS depth limit for same-host page discovery
	Timeout      time.Duration // per-request timeout
	Retries      int           // additional attempts after the first
	UserAgent    string        // User-Agent header sent with every request
	Politeness   time.Duration // minimum interval between requests
	MaxFileSize  int64         // refuse files larger than this
	Resume       bool          // skip files that already exist locally
	DryRun       bool          // discover and report only; download nothing
	Verbose      bool          // verbose logging
}

// Default returns a Config populated with safe, production-sane defaults.
func Default() Config {
	return Config{
		SeedURL:      DefaultSeedURL,
		OutputDir:    DefaultOutputDir,
		ManifestPath: DefaultManifest,
		Concurrency:  0, // auto
		MaxDepth:     DefaultMaxDepth,
		Timeout:      DefaultTimeout,
		Retries:      DefaultRetries,
		UserAgent:    DefaultUserAgent,
		Politeness:   DefaultPoliteness,
		MaxFileSize:  DefaultMaxFileSize,
	}
}

// AutoConcurrency returns the worker count for cpus logical processors,
// capped at 60% (rounded down, minimum 1). Passing cpus < 1 uses NumCPU.
// This keeps memory and CPU usage within the documented 60% budget.
func AutoConcurrency(cpus int) int {
	if cpus < 1 {
		cpus = runtime.NumCPU()
	}
	n := int(float64(cpus) * 0.6)
	if n < 1 {
		return 1
	}
	return n
}

// ResolveConcurrency returns cfg.Concurrency, or the auto value when <= 0.
func (c Config) ResolveConcurrency() int {
	if c.Concurrency > 0 {
		return c.Concurrency
	}
	return AutoConcurrency(runtime.NumCPU())
}

// Validate checks the configuration and returns a descriptive error for the
// first invalid field. It never performs network I/O.
func (c Config) Validate() error {
	u, err := url.Parse(c.SeedURL)
	if err != nil {
		return fmt.Errorf("seed url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("seed url: scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("seed url: missing host")
	}
	if c.OutputDir == "" {
		return fmt.Errorf("output dir: must not be empty")
	}
	if c.Concurrency < 0 {
		return fmt.Errorf("concurrency: must be >= 0, got %d", c.Concurrency)
	}
	if c.MaxDepth < 0 {
		return fmt.Errorf("max depth: must be >= 0, got %d", c.MaxDepth)
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("timeout: must be > 0, got %s", c.Timeout)
	}
	if c.Retries < 0 {
		return fmt.Errorf("retries: must be >= 0, got %d", c.Retries)
	}
	if c.UserAgent == "" {
		return fmt.Errorf("user agent: must not be empty")
	}
	if c.Politeness < 0 {
		return fmt.Errorf("politeness: must be >= 0, got %s", c.Politeness)
	}
	if c.MaxFileSize <= 0 {
		return fmt.Errorf("max file size: must be > 0, got %d", c.MaxFileSize)
	}
	return nil
}

// ApplyEnv overlays VELLUM_* environment variables onto cfg. The getenv
// function is injected so tests can supply a map without touching the
// process environment. Returns an error if any value fails to parse.
func ApplyEnv(cfg *Config, getenv func(string) string) error {
	if cfg == nil {
		return fmt.Errorf("config: nil receiver")
	}

	setString := func(key string, dst *string) {
		if v := getenv(key); v != "" {
			*dst = v
		}
	}
	setInt := func(key string, dst *int) error {
		v := getenv(key)
		if v == "" {
			return nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		*dst = n
		return nil
	}
	setBool := func(key string, dst *bool) error {
		v := getenv(key)
		if v == "" {
			return nil
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		*dst = b
		return nil
	}
	setDuration := func(key string, dst *time.Duration) error {
		v := getenv(key)
		if v == "" {
			return nil
		}
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		*dst = d
		return nil
	}
	setSize := func(key string, dst *int64) error {
		v := getenv(key)
		if v == "" {
			return nil
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		*dst = n
		return nil
	}

	setString("VELLUM_SEED_URL", &cfg.SeedURL)
	setString("VELLUM_OUTPUT_DIR", &cfg.OutputDir)
	setString("VELLUM_MANIFEST_PATH", &cfg.ManifestPath)
	setString("VELLUM_USER_AGENT", &cfg.UserAgent)

	for _, step := range []func() error{
		func() error { return setInt("VELLUM_CONCURRENCY", &cfg.Concurrency) },
		func() error { return setInt("VELLUM_MAX_DEPTH", &cfg.MaxDepth) },
		func() error { return setInt("VELLUM_RETRIES", &cfg.Retries) },
		func() error { return setBool("VELLUM_RESUME", &cfg.Resume) },
		func() error { return setBool("VELLUM_DRY_RUN", &cfg.DryRun) },
		func() error { return setBool("VELLUM_VERBOSE", &cfg.Verbose) },
		func() error { return setDuration("VELLUM_TIMEOUT", &cfg.Timeout) },
		func() error { return setDuration("VELLUM_POLITENESS", &cfg.Politeness) },
		func() error { return setSize("VELLUM_MAX_FILE_SIZE", &cfg.MaxFileSize) },
	} {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// FromEnv builds a Config from defaults overlaid with the process
// environment, without validation. Callers should still call Validate.
func FromEnv() (Config, error) {
	cfg := Default()
	if err := ApplyEnv(&cfg, os.Getenv); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
