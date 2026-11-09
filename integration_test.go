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
	open := sampler.NewOpener(func() (i2c.Bus, error) { return sim, nil }, func(time.Duration) {}, sampler.WithAHT21(0x38, true), sampler.WithENS160(0x53))
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

func TestIntegrationSCD4x(t *testing.T) {
	sim := i2ctest.New()
	sim.EnableSCD4x()
	sim.SetGas(3, 300, 400, 0)
	sim.SetCO2(950)
	sim.SetSCDNotReady(2)

	clock := store.NewSystemClock()
	st := store.New(100, clock)
	open := sampler.NewOpener(func() (i2c.Bus, error) { return sim, nil }, func(time.Duration) {}, sampler.WithAHT21(0x38, true), sampler.WithENS160(0x53), sampler.WithSCD4x(0x62))
	sm := sampler.New(open, st, sampler.Config{Interval: 10 * time.Second}, clock, nil)
	h := api.New(st, sm, clock, api.Config{
		Interval:   10 * time.Second,
		StaleAfter: time.Hour,
		ENS160Addr: 0x53,
		AHT21Addr:  0x38,
		SCD4xAddr:  0x62,
	})
	get := func(path string) map[string]any {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
		var m map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	sm.Tick()
	m := get("/v1/readings/latest")
	if m["co2_ppm"] != 950.0 || m["eco2_ppm"] != 400.0 || m["valid"] != true {
		t.Fatalf("%v", m)
	}
	if scd, _ := get("/v1/status")["scd4x"].(map[string]any); scd["addr"] != "0x62" {
		t.Fatalf("status scd4x: %v", scd)
	}

	sim.SetUnplugged(true)
	for i := 0; i < 5; i++ {
		sm.Tick()
	}
	sim.SetUnplugged(false)
	sim.SetSCDMeasuring(true)
	sim.SetCO2(1100)
	sm.Tick()
	m = get("/v1/readings/latest")
	if m["co2_ppm"] != 1100.0 {
		t.Fatalf("after replug: %v", m)
	}
}

func TestIntegrationSCD4xDisabledLeavesSensorAlone(t *testing.T) {
	sim := i2ctest.New()
	sim.EnableSCD4x()
	clock := store.NewSystemClock()
	st := store.New(10, clock)
	open := sampler.NewOpener(func() (i2c.Bus, error) { return sim, nil }, func(time.Duration) {}, sampler.WithAHT21(0x38, true), sampler.WithENS160(0x53))
	sm := sampler.New(open, st, sampler.Config{Interval: 10 * time.Second}, clock, nil)
	sm.Tick()
	if rd, ok := st.Latest(); !ok || rd.HasCO2 {
		t.Fatalf("%+v %v", rd, ok)
	}
	for _, w := range sim.Writes() {
		if w.Addr == 0x62 {
			t.Fatalf("unexpected SCD4x access: %+v", w)
		}
	}
}

func TestNoSensorStaysUp(t *testing.T) {
	clock := store.NewSystemClock()
	st := store.New(10, clock)
	open := sampler.NewOpener(func() (i2c.Bus, error) { return i2c.Open(250) }, func(time.Duration) {}, sampler.WithAHT21(0x38, true), sampler.WithENS160(0x53))
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

func newStack(t *testing.T, sim *i2ctest.Sim, cfg api.Config, opts ...sampler.OpenerOption) (*sampler.Sampler, func(string) map[string]any) {
	t.Helper()
	clock := store.NewSystemClock()
	st := store.New(100, clock)
	open := sampler.NewOpener(func() (i2c.Bus, error) { return sim, nil }, func(time.Duration) {}, opts...)
	sm := sampler.New(open, st, sampler.Config{Interval: 10 * time.Second}, clock, nil)
	cfg.Interval, cfg.StaleAfter = 10*time.Second, time.Hour
	h := api.New(st, sm, clock, cfg)
	return sm, func(path string) map[string]any {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
		var m map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
}

func TestIntegrationSCD4xOnly(t *testing.T) {
	sim := i2ctest.New()
	sim.EnableSCD4x()
	sim.SetCO2(780)
	sm, get := newStack(t, sim, api.Config{SCD4xAddr: 0x62}, sampler.WithSCD4x(0x62))
	sm.Tick()
	m := get("/v1/readings/latest")
	if m["co2_ppm"] != 780.0 || m["valid"] != true || m["temp_c"] != 25.0 || m["rh_pct"] != 50.0 {
		t.Fatalf("%v", m)
	}
	for _, k := range []string{"aqi", "tvoc_ppb", "eco2_ppm", "validity", "validity_name"} {
		if _, ok := m[k]; ok {
			t.Errorf("unexpected key %s", k)
		}
	}
	st := get("/v1/status")
	if _, ok := st["ens160"]; ok {
		t.Errorf("ens160 in status: %v", st)
	}
	if _, ok := st["aht21"]; ok {
		t.Errorf("aht21 in status: %v", st)
	}
	for _, w := range sim.Writes() {
		if w.Addr == 0x38 || w.Addr == 0x53 {
			t.Fatalf("touched disabled sensor: %+v", w)
		}
	}
	for _, r := range sim.Reads() {
		if r.Addr == 0x38 || r.Addr == 0x53 {
			t.Fatalf("touched disabled sensor: %+v", r)
		}
	}
}

func TestIntegrationENS160WithoutAHT21(t *testing.T) {
	sim := i2ctest.New()
	sim.SetGas(2, 40, 420, 0)
	sm, get := newStack(t, sim, api.Config{ENS160Addr: 0x53}, sampler.WithENS160(0x53))
	sm.Tick()
	m := get("/v1/readings/latest")
	if m["aqi"] != 2.0 || m["tvoc_ppb"] != 40.0 || m["valid"] != true {
		t.Fatalf("%v", m)
	}
	if _, ok := m["temp_c"]; ok {
		t.Errorf("temp_c present without AHT21: %v", m)
	}
	if tin, rhin := sim.Compensation(); tin != 0 || rhin != 0 {
		t.Fatalf("compensation written without a climate source: %d %d", tin, rhin)
	}
}

func TestIntegrationAHT21WithSCD4x(t *testing.T) {
	sim := i2ctest.New()
	sim.EnableSCD4x()
	sim.SetClimate(23, 45)
	sim.SetCO2(900)
	sm, get := newStack(t, sim, api.Config{AHT21Addr: 0x38, SCD4xAddr: 0x62}, sampler.WithAHT21(0x38, true), sampler.WithSCD4x(0x62))
	sm.Tick()
	m := get("/v1/readings/latest")
	if m["temp_c"] != 23.0 || m["co2_ppm"] != 900.0 {
		t.Fatalf("%v", m)
	}
	if _, ok := m["aqi"]; ok {
		t.Errorf("aqi present without ENS160: %v", m)
	}
}

func TestIntegrationSCD4xClimateWithTempOffset(t *testing.T) {
	sim := i2ctest.New()
	sim.EnableSCD4x()
	sim.SetSCDClimate(27.0, 38.0)
	clock := store.NewSystemClock()
	st := store.New(10, clock)
	open := sampler.NewOpener(func() (i2c.Bus, error) { return sim, nil }, func(time.Duration) {}, sampler.WithSCD4x(0x62))
	sm := sampler.New(open, st, sampler.Config{Interval: 10 * time.Second, TempOffset: -3}, clock, nil)
	sm.Tick()
	rd, ok := st.Latest()
	if !ok || rd.TempC != 24 || rd.RHPct != 45.4 || !rd.HasClimate {
		t.Fatalf("%+v %v", rd, ok)
	}
}
