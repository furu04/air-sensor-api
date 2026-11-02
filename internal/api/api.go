// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/furu04/air-sensor-api/internal/sampler"
	"github.com/furu04/air-sensor-api/internal/store"
)

const (
	defaultLimit        = 100
	failingThreshold    = 5
	ens160PartIDDisplay = "0x0160"
)

type Sampler interface{ Snapshot() sampler.Snapshot }

type Config struct {
	Version    string
	SourceURL  string
	Interval   time.Duration
	StaleAfter time.Duration
	ENS160Addr uint8
	AHT21Addr  uint8
	Token      string
}

type handler struct {
	st    *store.Store
	sm    Sampler
	clock store.Clock
	cfg   Config
	token [sha256.Size]byte
}

func New(st *store.Store, sm Sampler, clock store.Clock, cfg Config) http.Handler {
	h := &handler{st: st, sm: sm, clock: clock, cfg: cfg}
	if cfg.Token != "" {
		h.token = sha256.Sum256([]byte(cfg.Token))
	}
	return h
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type latestBody struct {
	TS           string  `json:"ts"`
	AgeSeconds   float64 `json:"age_seconds"`
	Stale        bool    `json:"stale"`
	TempC        float64 `json:"temp_c"`
	RHPct        float64 `json:"rh_pct"`
	AQI          int     `json:"aqi"`
	TVOCPPB      int     `json:"tvoc_ppb"`
	ECO2PPM      int     `json:"eco2_ppm"`
	Validity     int     `json:"validity"`
	ValidityName string  `json:"validity_name"`
	Valid        bool    `json:"valid"`
}

type readingItem struct {
	TS           string  `json:"ts"`
	AgeSeconds   float64 `json:"age_seconds"`
	TempC        float64 `json:"temp_c"`
	RHPct        float64 `json:"rh_pct"`
	AQI          int     `json:"aqi"`
	TVOCPPB      int     `json:"tvoc_ppb"`
	ECO2PPM      int     `json:"eco2_ppm"`
	Validity     int     `json:"validity"`
	ValidityName string  `json:"validity_name"`
	Valid        bool    `json:"valid"`
}

type readingsBody struct {
	Count           int           `json:"count"`
	IntervalSeconds int           `json:"interval_seconds"`
	Readings        []readingItem `json:"readings"`
}

type historyBody struct {
	Size  int `json:"size"`
	Count int `json:"count"`
}

type samplerBody struct {
	Initialized           bool     `json:"initialized"`
	LastSuccessAgeSeconds *float64 `json:"last_success_age_seconds"`
	LastError             string   `json:"last_error"`
	LastErrorAgeSeconds   *float64 `json:"last_error_age_seconds"`
	ConsecutiveFailures   int      `json:"consecutive_failures"`
	TotalFailures         int      `json:"total_failures"`
	Reinits               int      `json:"reinits"`
}

type ens160Body struct {
	Addr         string `json:"addr"`
	PartID       string `json:"part_id"`
	ValidityName string `json:"validity_name"`
}

type aht21Body struct {
	Addr string `json:"addr"`
}

type statusBody struct {
	Version         string      `json:"version"`
	Source          string      `json:"source"`
	UptimeSeconds   int         `json:"uptime_seconds"`
	IntervalSeconds int         `json:"interval_seconds"`
	History         historyBody `json:"history"`
	Sampler         samplerBody `json:"sampler"`
	ENS160          ens160Body  `json:"ens160"`
	AHT21           aht21Body   `json:"aht21"`
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hd := w.Header()
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Content-Type-Options", "nosniff")

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		hd.Set("Allow", "GET, HEAD")
		h.writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "only GET and HEAD are allowed")
		return
	}

	path := r.URL.Path
	if h.cfg.Token != "" && path != "/healthz" && !h.authorized(r) {
		hd.Set("WWW-Authenticate", "Bearer")
		h.writeError(w, r, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
		return
	}

	switch path {
	case "/v1/readings/latest":
		h.latest(w, r)
	case "/v1/readings":
		h.readings(w, r)
	case "/v1/status":
		h.status(w, r)
	case "/healthz":
		h.healthz(w, r)
	default:
		h.writeError(w, r, http.StatusNotFound, "not_found", "no such path")
	}
}

func (h *handler) authorized(r *http.Request) bool {
	const prefix = "Bearer "
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, prefix) {
		return false
	}
	got := sha256.Sum256([]byte(v[len(prefix):]))
	return subtle.ConstantTimeCompare(got[:], h.token[:]) == 1
}

func (h *handler) writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	b = append(b, '\n')
	hd := w.Header()
	hd.Set("Content-Type", "application/json; charset=utf-8")
	hd.Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		w.Write(b)
	}
}

func (h *handler) writeError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	h.writeJSON(w, r, status, errorBody{Error: errorDetail{Code: code, Message: msg}})
}

func ageAt(now time.Duration, r store.Reading) time.Duration {
	d := now - r.Mono
	if d < 0 {
		return 0
	}
	return d
}

func round1(x float64) float64 { return math.Round(x*10) / 10 }

func formatTS(r store.Reading) string { return r.Wall.UTC().Format(time.RFC3339) }

func (h *handler) latest(w http.ResponseWriter, r *http.Request) {
	rd, ok := h.st.Latest()
	if !ok {
		h.writeError(w, r, http.StatusServiceUnavailable, "no_data", "no measurement available yet")
		return
	}
	age := ageAt(h.clock.Mono(), rd)
	h.writeJSON(w, r, http.StatusOK, latestBody{
		TS:           formatTS(rd),
		AgeSeconds:   round1(age.Seconds()),
		Stale:        age > h.cfg.StaleAfter,
		TempC:        rd.TempC,
		RHPct:        rd.RHPct,
		AQI:          rd.AQI,
		TVOCPPB:      rd.TVOCPPB,
		ECO2PPM:      rd.ECO2PPM,
		Validity:     rd.Validity,
		ValidityName: rd.ValidityName(),
		Valid:        rd.Valid(),
	})
}

func firstParam(r *http.Request, key string) (string, bool) {
	vals, ok := r.URL.Query()[key]
	if !ok || len(vals) == 0 {
		return "", false
	}
	return vals[0], true
}

func (h *handler) parseQuery(r *http.Request) (store.Query, error) {
	q := store.Query{Limit: min(defaultLimit, h.st.Capacity())}

	winRaw, hasWindow := firstParam(r, "window")
	sinceRaw, hasSince := firstParam(r, "since")
	if hasWindow && hasSince {
		return q, fmt.Errorf("window and since are mutually exclusive")
	}
	if hasWindow {
		d, err := time.ParseDuration(winRaw)
		if err != nil {
			return q, fmt.Errorf("window: %v", err)
		}
		if d <= 0 {
			return q, fmt.Errorf("window: must be positive")
		}
		q.Window = d
	}
	if hasSince {
		t, err := time.Parse(time.RFC3339, sinceRaw)
		if err != nil {
			return q, fmt.Errorf("since: %v", err)
		}
		q.Since = t
	}
	if v, ok := firstParam(r, "limit"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return q, fmt.Errorf("limit: %v", err)
		}
		if n < 1 || n > h.st.Capacity() {
			return q, fmt.Errorf("limit: must be between 1 and %d", h.st.Capacity())
		}
		q.Limit = n
	}
	if v, ok := firstParam(r, "valid_only"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return q, fmt.Errorf("valid_only: %v", err)
		}
		q.ValidOnly = b
	}
	return q, nil
}

func (h *handler) readings(w http.ResponseWriter, r *http.Request) {
	q, err := h.parseQuery(r)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	now := h.clock.Mono()
	rows := h.st.Query(q)
	items := make([]readingItem, 0, len(rows))
	for _, rd := range rows {
		items = append(items, readingItem{
			TS:           formatTS(rd),
			AgeSeconds:   round1(ageAt(now, rd).Seconds()),
			TempC:        rd.TempC,
			RHPct:        rd.RHPct,
			AQI:          rd.AQI,
			TVOCPPB:      rd.TVOCPPB,
			ECO2PPM:      rd.ECO2PPM,
			Validity:     rd.Validity,
			ValidityName: rd.ValidityName(),
			Valid:        rd.Valid(),
		})
	}
	h.writeJSON(w, r, http.StatusOK, readingsBody{
		Count:           len(items),
		IntervalSeconds: int(h.cfg.Interval / time.Second),
		Readings:        items,
	})
}

func (h *handler) status(w http.ResponseWriter, r *http.Request) {
	now := h.clock.Mono()
	snap := h.sm.Snapshot()

	sb := samplerBody{
		Initialized:         snap.Initialized,
		ConsecutiveFailures: snap.ConsecutiveFailures,
		TotalFailures:       snap.TotalFailures,
		Reinits:             snap.Reinits,
	}
	if snap.HasLastSuccess {
		v := round1((now - snap.LastSuccessMono).Seconds())
		sb.LastSuccessAgeSeconds = &v
	}
	if snap.HasLastError {
		v := round1((now - snap.LastErrorMono).Seconds())
		sb.LastErrorAgeSeconds = &v
		sb.LastError = snap.LastError
	}

	eb := ens160Body{Addr: fmt.Sprintf("0x%02X", h.cfg.ENS160Addr)}
	if snap.Initialized {
		eb.PartID = ens160PartIDDisplay
	}
	if rd, ok := h.st.Latest(); ok {
		eb.ValidityName = rd.ValidityName()
	}

	h.writeJSON(w, r, http.StatusOK, statusBody{
		Version:         h.cfg.Version,
		Source:          h.cfg.SourceURL,
		UptimeSeconds:   int(now.Seconds()),
		IntervalSeconds: int(h.cfg.Interval / time.Second),
		History:         historyBody{Size: h.st.Capacity(), Count: h.st.Len()},
		Sampler:         sb,
		ENS160:          eb,
		AHT21:           aht21Body{Addr: fmt.Sprintf("0x%02X", h.cfg.AHT21Addr)},
	})
}

func (h *handler) healthz(w http.ResponseWriter, r *http.Request) {
	snap := h.sm.Snapshot()
	status := http.StatusOK
	var body string
	switch {
	case !snap.HasLastSuccess:
		status, body = http.StatusServiceUnavailable, "no data yet"
	case h.clock.Mono()-snap.LastSuccessMono > h.cfg.StaleAfter:
		ago := h.clock.Mono() - snap.LastSuccessMono
		status, body = http.StatusServiceUnavailable, fmt.Sprintf("stale: last success %.1fs ago", ago.Seconds())
	case snap.ConsecutiveFailures >= failingThreshold:
		status, body = http.StatusServiceUnavailable, fmt.Sprintf("sensor failing: %d consecutive failures", snap.ConsecutiveFailures)
	default:
		body = "ok"
	}
	b := []byte(body + "\n")
	hd := w.Header()
	hd.Set("Content-Type", "text/plain; charset=utf-8")
	hd.Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		w.Write(b)
	}
}
