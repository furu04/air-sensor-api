// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/furu04/air-sensor-api/internal/sampler"
	"github.com/furu04/air-sensor-api/internal/store"
)

type fakeClock struct {
	now  time.Time
	mono time.Duration
}

func (c *fakeClock) Now() time.Time      { return c.now }
func (c *fakeClock) Mono() time.Duration { return c.mono }

type fakeSampler struct{ snap sampler.Snapshot }

func (f *fakeSampler) Snapshot() sampler.Snapshot { return f.snap }

var epoch = time.Date(2026, 11, 3, 1, 23, 45, 0, time.UTC)

type env struct {
	clk *fakeClock
	st  *store.Store
	sm  *fakeSampler
	cfg Config
}

func newEnv(token string) *env {
	e := &env{
		clk: &fakeClock{now: epoch, mono: 1000 * time.Second},
		sm:  &fakeSampler{},
		cfg: Config{
			Version:    "0.1.0",
			SourceURL:  "https://example.invalid/src",
			Interval:   60 * time.Second,
			StaleAfter: 180 * time.Second,
			ENS160Addr: 0x53,
			AHT21Addr:  0x38,
			Token:      token,
		},
	}
	e.st = store.New(50, e.clk)
	return e
}

func (e *env) add(minutesAgo int, validity int, tvoc int) {
	e.st.Add(store.Reading{
		Wall:     epoch.Add(-time.Duration(minutesAgo) * time.Minute),
		Mono:     e.clk.mono - time.Duration(minutesAgo)*time.Minute,
		TempC:    24.31,
		RHPct:    45.2,
		AQI:      1,
		TVOCPPB:  tvoc,
		ECO2PPM:  400,
		Validity: validity,
	})
}

func (e *env) do(method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	h := New(e.st, e.sm, e.clk, e.cfg)
	req := httptest.NewRequest(method, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode: %v: %s", err, rec.Body.String())
	}
	return m
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	m := decode(t, rec)
	e, ok := m["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error object: %s", rec.Body.String())
	}
	if msg, _ := e["message"].(string); msg == "" {
		t.Fatalf("empty message: %s", rec.Body.String())
	}
	return e["code"].(string)
}

func TestLatestEmpty(t *testing.T) {
	e := newEnv("")
	rec := e.do("GET", "/v1/readings/latest", nil)
	if rec.Code != 503 || errCode(t, rec) != "no_data" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestLatestOne(t *testing.T) {
	e := newEnv("")
	e.add(0, 0, 12)
	e.clk.mono += 12*time.Second + 400*time.Millisecond
	rec := e.do("GET", "/v1/readings/latest", nil)
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("ct=%s", ct)
	}
	m := decode(t, rec)
	want := map[string]any{
		"ts": "2026-11-03T01:23:45Z", "age_seconds": 12.4, "stale": false, "temp_c": 24.31, "rh_pct": 45.2,
		"aqi": 1.0, "tvoc_ppb": 12.0, "eco2_ppm": 400.0, "validity": 0.0, "validity_name": "ok", "valid": true,
	}
	if len(m) != len(want) {
		t.Fatalf("keys: %v", m)
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v (%T) want %v", k, m[k], m[k], v)
		}
	}
}

func TestLatestStale(t *testing.T) {
	e := newEnv("")
	e.add(0, 0, 1)
	e.clk.mono += 181 * time.Second
	rec := e.do("GET", "/v1/readings/latest", nil)
	if rec.Code != 200 || decode(t, rec)["stale"] != true {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestLatestInvalidReading(t *testing.T) {
	e := newEnv("")
	e.add(0, 1, 1)
	m := decode(t, e.do("GET", "/v1/readings/latest", nil))
	if m["valid"] != false || m["validity_name"] != "warmup" {
		t.Fatalf("%v", m)
	}
}

func orderOK(body string, keys []string) bool {
	last := -1
	for _, k := range keys {
		i := strings.Index(body, `"`+k+`"`)
		if i < 0 || i <= last {
			return false
		}
		last = i
	}
	return true
}

func TestKeyOrder(t *testing.T) {
	e := newEnv("")
	e.add(0, 0, 1)
	rec := e.do("GET", "/v1/readings/latest", nil)
	keys := []string{"ts", "age_seconds", "stale", "temp_c", "rh_pct", "aqi", "tvoc_ppb", "eco2_ppm", "validity", "validity_name", "valid"}
	if !orderOK(rec.Body.String(), keys) {
		t.Fatalf("order: %s", rec.Body)
	}
	rec = e.do("GET", "/v1/readings", nil)
	if strings.Contains(rec.Body.String(), `"stale"`) {
		t.Fatalf("stale in readings: %s", rec.Body)
	}
	item := []string{"ts", "age_seconds", "temp_c", "rh_pct", "aqi", "tvoc_ppb", "eco2_ppm", "validity", "validity_name", "valid"}
	if !orderOK(rec.Body.String(), item) {
		t.Fatalf("order: %s", rec.Body)
	}
}

func readingsOf(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	m := decode(t, rec)
	raw, ok := m["readings"].([]any)
	if !ok {
		t.Fatalf("readings not an array: %s", rec.Body)
	}
	if int(m["count"].(float64)) != len(raw) {
		t.Fatalf("count mismatch: %s", rec.Body)
	}
	out := make([]map[string]any, len(raw))
	for i, r := range raw {
		out[i] = r.(map[string]any)
	}
	return out
}

func TestReadingsEmpty(t *testing.T) {
	e := newEnv("")
	rec := e.do("GET", "/v1/readings", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"readings":[]`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	m := decode(t, rec)
	if m["count"] != 0.0 || m["interval_seconds"] != 60.0 {
		t.Fatalf("%v", m)
	}
}

func TestReadingsFilters(t *testing.T) {
	e := newEnv("")
	for i := 10; i >= 1; i-- {
		v := 0
		if i%2 == 0 {
			v = 1
		}
		e.add(i, v, 100+i)
	}
	tvocs := func(rs []map[string]any) []int {
		var out []int
		for _, r := range rs {
			out = append(out, int(r["tvoc_ppb"].(float64)))
		}
		return out
	}
	eq := func(a, b []int) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	all := tvocs(readingsOf(t, e.do("GET", "/v1/readings", nil)))
	if !eq(all, []int{110, 109, 108, 107, 106, 105, 104, 103, 102, 101}) {
		t.Fatalf("all=%v", all)
	}
	if got := tvocs(readingsOf(t, e.do("GET", "/v1/readings?limit=3", nil))); !eq(got, []int{103, 102, 101}) {
		t.Fatalf("limit=%v", got)
	}
	if got := tvocs(readingsOf(t, e.do("GET", "/v1/readings?window=3m", nil))); !eq(got, []int{103, 102, 101}) {
		t.Fatalf("window=%v", got)
	}
	since := epoch.Add(-2 * time.Minute).Format(time.RFC3339)
	if got := tvocs(readingsOf(t, e.do("GET", "/v1/readings?since="+since, nil))); !eq(got, []int{102, 101}) {
		t.Fatalf("since=%v", got)
	}
	if got := tvocs(readingsOf(t, e.do("GET", "/v1/readings?valid_only=true&limit=2", nil))); !eq(got, []int{103, 101}) {
		t.Fatalf("valid_only=%v", got)
	}
}

func TestReadingsBadRequests(t *testing.T) {
	e := newEnv("")
	e.add(0, 0, 1)
	targets := []string{
		"/v1/readings?limit=0",
		"/v1/readings?limit=99999",
		"/v1/readings?limit=abc",
		"/v1/readings?window=abc",
		"/v1/readings?window=-1m",
		"/v1/readings?window=0s",
		"/v1/readings?since=yesterday",
		"/v1/readings?valid_only=maybe",
		"/v1/readings?window=1m&since=2026-11-03T00:00:00Z",
	}
	for _, tg := range targets {
		rec := e.do("GET", tg, nil)
		if rec.Code != 400 || errCode(t, rec) != "bad_request" {
			t.Errorf("%s: %d %s", tg, rec.Code, rec.Body)
		}
	}
}

func TestStatusAlways200(t *testing.T) {
	e := newEnv("")
	rec := e.do("GET", "/v1/status", nil)
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	m := decode(t, rec)
	sm := m["sampler"].(map[string]any)
	if sm["last_success_age_seconds"] != nil || sm["last_error_age_seconds"] != nil || sm["last_error"] != "" {
		t.Fatalf("%v", sm)
	}
	if m["source"] != "https://example.invalid/src" || m["version"] != "0.1.0" || m["uptime_seconds"] != 1000.0 {
		t.Fatalf("%v", m)
	}
	if m["ens160"].(map[string]any)["part_id"] != "" || m["ens160"].(map[string]any)["addr"] != "0x53" || m["aht21"].(map[string]any)["addr"] != "0x38" {
		t.Fatalf("%v", m)
	}
}

func TestStatusPopulated(t *testing.T) {
	e := newEnv("")
	e.add(0, 1, 1)
	e.sm.snap = sampler.Snapshot{
		Initialized: true, HasLastSuccess: true, LastSuccessMono: e.clk.mono - 12400*time.Millisecond,
		HasLastError: true, LastError: "boom", LastErrorMono: e.clk.mono - 30*time.Second,
		TotalFailures: 3, Reinits: 1,
	}
	m := decode(t, e.do("GET", "/v1/status", nil))
	sm := m["sampler"].(map[string]any)
	if sm["last_success_age_seconds"] != 12.4 || sm["last_error_age_seconds"] != 30.0 || sm["last_error"] != "boom" ||
		sm["total_failures"] != 3.0 || sm["reinits"] != 1.0 || sm["initialized"] != true {
		t.Fatalf("%v", sm)
	}
	en := m["ens160"].(map[string]any)
	if en["part_id"] != "0x0160" || en["validity_name"] != "warmup" {
		t.Fatalf("%v", en)
	}
	h := m["history"].(map[string]any)
	if h["size"] != 50.0 || h["count"] != 1.0 {
		t.Fatalf("%v", h)
	}
}

func TestHealthz(t *testing.T) {
	e := newEnv("")
	check := func(want int, prefix string) {
		t.Helper()
		rec := e.do("GET", "/healthz", nil)
		if rec.Code != want || !strings.HasPrefix(rec.Body.String(), prefix) {
			t.Fatalf("%d %q (want %d %q)", rec.Code, rec.Body.String(), want, prefix)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
			t.Fatalf("ct=%s", ct)
		}
	}
	check(503, "no data yet")

	e.sm.snap = sampler.Snapshot{HasLastSuccess: true, LastSuccessMono: e.clk.mono - 10*time.Second}
	check(200, "ok")

	e.sm.snap.ConsecutiveFailures = 4
	check(200, "ok")
	e.sm.snap.ConsecutiveFailures = 5
	check(503, "sensor failing: 5 consecutive failures")

	e.sm.snap = sampler.Snapshot{HasLastSuccess: true, LastSuccessMono: e.clk.mono - 312400*time.Millisecond}
	check(503, "stale: last success 312.4s ago")
}

func TestHEAD(t *testing.T) {
	e := newEnv("")
	e.add(0, 0, 1)
	e.sm.snap = sampler.Snapshot{HasLastSuccess: true, LastSuccessMono: e.clk.mono}
	for _, p := range []string{"/v1/readings/latest", "/v1/readings", "/v1/status", "/healthz", "/v1/nothing"} {
		g := e.do("GET", p, nil)
		h := e.do("HEAD", p, nil)
		if g.Code != h.Code || h.Body.Len() != 0 || g.Header().Get("Content-Type") != h.Header().Get("Content-Type") ||
			g.Header().Get("Cache-Control") != h.Header().Get("Cache-Control") {
			t.Errorf("%s: GET %d / HEAD %d body=%d", p, g.Code, h.Code, h.Body.Len())
		}
	}
}

func TestMethodNotAllowed(t *testing.T) {
	e := newEnv("")
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		rec := e.do(m, "/v1/readings/latest", nil)
		if rec.Code != 405 || rec.Header().Get("Allow") != "GET, HEAD" || errCode(t, rec) != "method_not_allowed" {
			t.Errorf("%s: %d %s", m, rec.Code, rec.Body)
		}
	}
}

func TestNotFound(t *testing.T) {
	e := newEnv("")
	for _, p := range []string{"/v1/readings/latest/", "/v1/nothing", "/metrics", "/", "/v1/readings/"} {
		rec := e.do("GET", p, nil)
		if rec.Code != 404 || errCode(t, rec) != "not_found" {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Body)
		}
	}
}

func TestAuth(t *testing.T) {
	const token = "0123456789abcdef"
	e := newEnv(token)
	e.add(0, 0, 1)
	e.sm.snap = sampler.Snapshot{HasLastSuccess: true, LastSuccessMono: e.clk.mono}

	cases := []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"none", nil, 401},
		{"wrong", map[string]string{"Authorization": "Bearer 0123456789abcdeX"}, 401},
		{"lowercase scheme", map[string]string{"Authorization": "bearer " + token}, 401},
		{"basic", map[string]string{"Authorization": "Basic " + token}, 401},
		{"prefix only", map[string]string{"Authorization": "Bearer "}, 401},
		{"correct", map[string]string{"Authorization": "Bearer " + token}, 200},
	}
	for _, c := range cases {
		rec := e.do("GET", "/v1/readings/latest", c.hdr)
		if rec.Code != c.want {
			t.Errorf("%s: %d", c.name, rec.Code)
		}
		if c.want == 401 && (rec.Header().Get("WWW-Authenticate") != "Bearer" || errCode(t, rec) != "unauthorized") {
			t.Errorf("%s: %v %s", c.name, rec.Header(), rec.Body)
		}
	}
	if rec := e.do("GET", "/healthz", nil); rec.Code != 200 {
		t.Fatalf("healthz %d", rec.Code)
	}
	if rec := e.do("GET", "/metrics", nil); rec.Code != 401 {
		t.Fatalf("unknown path without token: %d", rec.Code)
	}

	open := newEnv("")
	open.add(0, 0, 1)
	if rec := open.do("GET", "/v1/readings/latest", nil); rec.Code != 200 {
		t.Fatalf("no token configured: %d", rec.Code)
	}
}

func TestCommonHeaders(t *testing.T) {
	e := newEnv("0123456789abcdef")
	e.add(0, 0, 1)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/v1/readings/latest"}, {"GET", "/v1/readings"}, {"GET", "/v1/status"}, {"GET", "/healthz"},
		{"GET", "/nothing"}, {"POST", "/v1/status"}, {"GET", "/v1/readings?limit=0"},
	} {
		rec := e.do(tc.method, tc.path, nil)
		if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s %s: %v", tc.method, tc.path, rec.Header())
		}
	}
}

func TestWallClockRewind(t *testing.T) {
	e := newEnv("")
	e.add(2, 0, 1)
	e.add(1, 0, 2)
	e.add(0, 0, 3)
	e.clk.mono += 30 * time.Second

	before := decode(t, e.do("GET", "/v1/readings/latest", nil))
	beforeWin := readingsOf(t, e.do("GET", "/v1/readings?window=90s", nil))

	e.clk.now = e.clk.now.Add(-48 * time.Hour)

	after := decode(t, e.do("GET", "/v1/readings/latest", nil))
	afterWin := readingsOf(t, e.do("GET", "/v1/readings?window=90s", nil))

	if before["age_seconds"] != after["age_seconds"] || before["stale"] != after["stale"] || before["ts"] != after["ts"] {
		t.Fatalf("%v vs %v", before, after)
	}
	if len(beforeWin) != len(afterWin) || len(afterWin) != 2 {
		t.Fatalf("window: %d vs %d", len(beforeWin), len(afterWin))
	}
}
