package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"easy_proxies/internal/app"
	"easy_proxies/internal/buildinfo"
	"easy_proxies/internal/config"
	"easy_proxies/internal/monitor"

	"gopkg.in/natefinch/lumberjack.v2"
)

func main() {
	var configPath string
	var showVersion bool
	var showVersionJSON bool
	var showThirdPartyNotices bool
	flag.StringVar(&configPath, "config", "config.yaml", "path to config file")
	flag.BoolVar(&showVersion, "version", false, "print build version and capabilities")
	flag.BoolVar(&showVersionJSON, "version-json", false, "print build information as JSON")
	flag.BoolVar(&showThirdPartyNotices, "third-party-notices", false, "print third-party license notices")
	flag.Parse()
	if showThirdPartyNotices {
		fmt.Print(buildinfo.ThirdPartyNotices)
		return
	}
	if showVersion || showVersionJSON {
		info := buildinfo.Current()
		if showVersionJSON {
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(info); err != nil {
				log.Fatalf("encode build info: %v", err)
			}
			return
		}
		fmt.Printf("%s %s (%s) %s/%s\n", info.Product, info.Version, info.Commit, info.GOOS, info.GOARCH)
		fmt.Printf("official release capabilities: %t\n", info.OfficialReleaseReady)
		fmt.Printf("features: %v\n", info.Capabilities)
		fmt.Printf("protocols: %s\n", strings.Join(info.Protocols, ", "))
		return
	}

	resolvedConfigPath, bootstrap, err := prepareConfigFile(configPath)
	if err != nil {
		log.Fatalf("prepare config: %v", err)
	}
	if bootstrap.Created {
		log.Printf("Created default config file: %s", resolvedConfigPath)
		log.Printf("🔐 Generated one-time management password: %s", bootstrap.ManagementPassword)
		log.Printf("⚠️  Change the generated management password after your first login. It will not be printed again.")
	}
	log.Printf("Using config file: %s", resolvedConfigPath)
	configPath = resolvedConfigPath

	var cfg *config.Config
	for attempt := 1; attempt <= 3; attempt++ {
		var err error
		cfg, err = config.Load(configPath)
		if err == nil {
			break
		}
		if attempt < 3 && strings.Contains(err.Error(), "config.nodes cannot be empty") {
			log.Printf("⚠️  Attempt %d/3: %v (retrying in %ds...)", attempt, err, attempt*10)
			time.Sleep(time.Duration(attempt*10) * time.Second)
			continue
		}
		log.Fatalf("load config: %v", err)
	}

	// Setup logging based on config
	stopLogging := setupLogging(cfg)
	defer stopLogging()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := app.Run(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "proxy pool exited with error: %v\n", err)
		os.Exit(1)
	}
}

func prepareConfigFile(path string) (string, config.BootstrapResult, error) {
	resolvedPath, err := filepath.Abs(path)
	if err != nil {
		return "", config.BootstrapResult{}, fmt.Errorf("resolve config path: %w", err)
	}
	result, err := config.EnsureDefaultFileWithResult(resolvedPath)
	if err != nil {
		return "", config.BootstrapResult{}, err
	}
	return resolvedPath, result, nil
}

func setupLogging(cfg *config.Config) func() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	// Always include the in-memory ring buffer for dashboard console.
	writers := []io.Writer{os.Stdout, monitor.LogWriter()}
	var rotatingFile *lumberjack.Logger
	if cfg.Log.Output == "file" {
		logDir := filepath.Dir(cfg.Log.File)
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			log.Printf("⚠️ Failed to create log dir %s: %v, falling back to stdout", logDir, err)
		} else {
			rotatingFile = &lumberjack.Logger{
				Filename:   cfg.Log.File,
				MaxSize:    cfg.Log.MaxSize,
				MaxBackups: cfg.Log.MaxBackups,
				MaxAge:     cfg.Log.MaxAge,
				Compress:   cfg.Log.Compress,
			}
			writers = append(writers, rotatingFile)
		}
	}
	log.SetOutput(io.MultiWriter(writers...))
	if rotatingFile == nil {
		return func() {}
	}

	log.Printf("✅ Log rotation enabled: file=%s, maxSize=%dMB, maxBackups=%d, maxAge=%dd, interval=%s",
		cfg.Log.File, cfg.Log.MaxSize, cfg.Log.MaxBackups, cfg.Log.MaxAge, cfg.Log.RotateInterval)
	if cfg.Log.RotateInterval <= 0 {
		return func() { _ = rotatingFile.Close() }
	}

	stop := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		ticker := time.NewTicker(cfg.Log.RotateInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := rotatingFile.Rotate(); err != nil {
					log.Printf("⚠️ Timed log rotation failed: %v", err)
				} else {
					log.Printf("✅ Timed log archive created")
				}
			case <-stop:
				return
			}
		}
	}()
	var stopOnce sync.Once
	return func() {
		stopOnce.Do(func() {
			close(stop)
			wait.Wait()
			_ = rotatingFile.Close()
		})
	}
}
