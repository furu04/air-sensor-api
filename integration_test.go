// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/furu04/air-sensor-api/internal/api"
	"github.com/furu04/air-sensor-api/internal/i2c"
	"github.com/furu04/air-sensor-api/internal/i2c/i2ctest"
	"github.com/furu04/air-sensor-api/internal/sampler"
	"github.com/furu04/air-sensor-api/internal/store"
)

func TestIntegration(t *testing.T) {
	sim := i2ctest.New()
	sim.SetClimate(25.0, 50.0)
	sim.SetGas(3, 300, 400, 0)

	clock := store.NewSystemClock()
	st := store.New(100, clock)
	open := sampler.NewOpener(func() (i2c.Bus, error) { return sim, nil }, 0x38, 0x53, true, func(time.Duration) {})
	sm := sampler.New(open, st, sampler.Config{Interval: 10 * time.Second}, clock, nil)
	h := api.New(st, sm, clock, api.Config{
		Version:    "test",
		SourceURL:  "https://example.invalid",
		Interval:   10 * time.Second,
		StaleAfter: time.Hour,
		ENS160Addr: 0x53,
		AHT21Addr:  0x38,
	})

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	sm.Tick()
	rec := get("/v1/readings/latest")
	if rec.Code != 200 {
		t.Fatalf("latest: %d %s", rec.Code, rec.Body)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"temp_c": 25.0, "rh_pct": 50.0, "aqi": 3.0, "tvoc_ppb": 300.0, "eco2_ppm": 400.0, "valid": true}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v want %v", k, m[k], v)
		}
	}
	if rec := get("/healthz"); rec.Code != 200 {
		t.Fatalf("healthz before unplug: %d", rec.Code)
	}

	sim.SetUnplugged(true)
	for i := 0; i < 5; i++ {
		sm.Tick()
	}
	if rec := get("/healthz"); rec.Code != 503 {
		t.Fatalf("healthz after unplug: %d %s", rec.Code, rec.Body)
	}
	var status struct {
		Sampler struct {
			Reinits int `json:"reinits"`
		} `json:"sampler"`
	}
	if err := json.Unmarshal(get("/v1/status").Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Sampler.Reinits != 1 {
		t.Fatalf("reinits=%d", status.Sampler.Reinits)
	}

	sim.SetUnplugged(false)
	sm.Tick()
	if rec := get("/healthz"); rec.Code != 200 {
		t.Fatalf("healthz after replug: %d %s", rec.Code, rec.Body)
	}
}

func TestNoSensorStaysUp(t *testing.T) {
	clock := store.NewSystemClock()
	st := store.New(10, clock)
	open := sampler.NewOpener(func() (i2c.Bus, error) { return i2c.Open(250) }, 0x38, 0x53, true, func(time.Duration) {})
	sm := sampler.New(open, st, sampler.Config{Interval: 10 * time.Second}, clock, nil)
	h := api.New(st, sm, clock, api.Config{Interval: 10 * time.Second, StaleAfter: time.Hour, ENS160Addr: 0x53, AHT21Addr: 0x38})

	sm.Tick()
	sm.Tick()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != 503 {
		t.Fatalf("healthz: %d", rec.Code)
	}
	var status struct {
		Sampler struct {
			LastError           string `json:"last_error"`
			ConsecutiveFailures int    `json:"consecutive_failures"`
			Reinits             int    `json:"reinits"`
		} `json:"sampler"`
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	if rec.Code != 200 {
		t.Fatalf("status: %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Sampler.LastError == "" || status.Sampler.ConsecutiveFailures != 2 || status.Sampler.Reinits != 0 {
		t.Fatalf("%+v", status)
	}
}

func TestRunArgs(t *testing.T) {
	if got := run([]string{"--version"}); got != 0 {
		t.Fatalf("--version: %d", got)
	}
	if got := run([]string{"--bogus"}); got != 2 {
		t.Fatalf("--bogus: %d", got)
	}
	if got := run([]string{"--version", "extra"}); got != 2 {
		t.Fatalf("extra arg: %d", got)
	}
}
