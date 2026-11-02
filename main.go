// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/furu04/air-sensor-api/internal/api"
	"github.com/furu04/air-sensor-api/internal/config"
	"github.com/furu04/air-sensor-api/internal/i2c"
	"github.com/furu04/air-sensor-api/internal/sampler"
	"github.com/furu04/air-sensor-api/internal/store"
)

var (
	version   = "dev"
	sourceURL = "https://github.com/furu04/air-sensor-api"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) > 0 {
		if len(args) == 1 && (args[0] == "--version" || args[0] == "-version") {
			fmt.Println(version)
			return 0
		}
		fmt.Fprintf(os.Stderr, "usage: air-sensor-api [--version]\nconfiguration is read from environment variables; see README\n")
		return 2
	}

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 2
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	log.Info("starting",
		"version", version,
		"listen", cfg.ListenAddr,
		"interval", cfg.Interval,
		"history", cfg.HistorySize,
	)

	if cfg.ExposedWithoutToken() {
		log.Warn("listening on a non-loopback address without API_TOKEN; the API is unauthenticated", "listen", cfg.ListenAddr)
	}

	clock := store.NewSystemClock()
	st := store.New(cfg.HistorySize, clock)
	open := sampler.NewOpener(
		func() (i2c.Bus, error) { return i2c.Open(cfg.I2CBus) },
		cfg.AHT21Addr, cfg.ENS160Addr, !cfg.NoCRC, time.Sleep,
	)
	sm := sampler.New(open, st, sampler.Config{Interval: cfg.Interval, TempOffset: cfg.TempOffset}, clock, log)

	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		log.Error("listen failed", "err", err)
		return 1
	}

	handler := api.New(st, sm, clock, api.Config{
		Version:    version,
		SourceURL:  sourceURL,
		Interval:   cfg.Interval,
		StaleAfter: cfg.StaleAfter,
		ENS160Addr: cfg.ENS160Addr,
		AHT21Addr:  cfg.AHT21Addr,
		Token:      cfg.APIToken,
	})
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8192,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sm.Run(ctx)
	}()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	exit := 0
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server failed", "err", err)
			exit = 1
		}
		stop()
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("shutdown incomplete", "err", err)
		srv.Close()
	}
	wg.Wait()
	return exit
}
