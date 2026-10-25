package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"localconverter/internal/api"
	"localconverter/internal/camera"
	"localconverter/internal/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "configs/localconverter.json", "JSON configuration file")
	listen := flag.String("listen", "", "override the configured HTTP listen address")
	flag.Parse()
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.LogDirectory, 0755); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	name := fmt.Sprintf("localconverter-runtime-%s-%d.log", time.Now().Format("20060102-150405"), os.Getpid())
	file, err := os.OpenFile(filepath.Join(cfg.LogDirectory, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open runtime log: %w", err)
	}
	defer file.Close()
	logger := log.New(io.MultiWriter(file, os.Stderr), "", log.LstdFlags)
	logger.Print("H264 passthrough: source resolution and frame rate; no re-encoding")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	media := camera.NewMedia(cfg.Media, cfg.LogDirectory, logger)
	defer media.Close()
	manager := camera.NewManager(cfg.Camera, media)
	defer manager.Close()
	go manager.Reap(ctx)
	if err := api.Run(ctx, cfg.Listen, logger, manager, media, cfg.Camera.HeartbeatIntervalSeconds, cfg.AllowedOrigins, cfg.AccessToken); err != nil {
		logger.Printf("service stopped with error: %v", err)
		return err
	}
	return nil
}
