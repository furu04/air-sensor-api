// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package recorder

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/furu04/air-sensor-api/internal/store"
)

var base = time.Date(2026, 11, 11, 5, 0, 0, 0, time.UTC)

func reading(minute int) store.Reading {
	return store.Reading{
		Wall:       base.Add(time.Duration(minute) * time.Minute),
		HasClimate: true,
		TempC:      23.64,
		RHPct:      37,
		HasGas:     true,
		AQI:        1,
		TVOCPPB:    24,
		ECO2PPM:    400,
		Validity:   2,
		HasCO2:     true,
		CO2PPM:     527,
	}
}

func newRecorder(t *testing.T) (*Recorder, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "readings.jsonl")
	r, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	return r, path
}

func TestNewFailsWhenDirectoryMissing(t *testing.T) {
	if _, err := New(filepath.Join(t.TempDir(), "missing", "r.jsonl")); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewCreatesFile(t *testing.T) {
	_, path := newRecorder(t)
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestRecordWritesOneJSONLinePerSample(t *testing.T) {
	r, path := newRecorder(t)
	for i := 0; i < 3; i++ {
		if err := r.Record(reading(i)); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 3 || !strings.HasSuffix(string(data), "\n") {
		t.Fatalf("%q", data)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"ts": "2026-11-11T05:00:00Z", "temp_c": 23.64, "rh_pct": 37.0, "aqi": 1.0, "tvoc_ppb": 24.0,
		"eco2_ppm": 400.0, "co2_ppm": 527.0, "validity": 2.0, "validity_name": "initial_startup", "valid": false,
	}
	if len(m) != len(want) {
		t.Fatalf("%v", m)
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s=%v want %v", k, m[k], v)
		}
	}
}

func TestRecordOmitsDisabledSensors(t *testing.T) {
	r, path := newRecorder(t)
	rd := reading(0)
	rd.HasGas, rd.HasCO2 = false, false
	if err := r.Record(rd); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, k := range []string{"aqi", "tvoc_ppb", "eco2_ppm", "co2_ppm", "validity"} {
		if strings.Contains(string(data), `"`+k+`"`) {
			t.Errorf("unexpected key %s in %s", k, data)
		}
	}
	if !strings.Contains(string(data), `"valid":true`) {
		t.Errorf("valid must be true without ENS160: %s", data)
	}
}

func TestRecordAppendsAcrossInstances(t *testing.T) {
	r, path := newRecorder(t)
	if err := r.Record(reading(0)); err != nil {
		t.Fatal(err)
	}
	r2, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := r2.Record(reading(1)); err != nil {
		t.Fatal(err)
	}
	rows, _, err := r2.Read(context.Background(), time.Time{}, time.Time{}, 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
}

func TestRecordSurvivesRotation(t *testing.T) {
	r, path := newRecorder(t)
	if err := r.Record(reading(0)); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	if err := r.Record(reading(1)); err != nil {
		t.Fatal(err)
	}
	rotated, _ := os.ReadFile(path + ".1")
	current, _ := os.ReadFile(path)
	if strings.Count(string(rotated), "\n") != 1 || strings.Count(string(current), "\n") != 1 {
		t.Fatalf("rotated=%q current=%q", rotated, current)
	}
}

func TestRecordFailsWhenDirectoryRemoved(t *testing.T) {
	dir := t.TempDir()
	r, err := New(filepath.Join(dir, "sub.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, "sub.jsonl"))
	os.Remove(dir)
	if err := r.Record(reading(0)); err == nil {
		t.Fatal("expected error")
	}
}

func fill(t *testing.T, r *Recorder, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := r.Record(reading(i)); err != nil {
			t.Fatal(err)
		}
	}
}

func tsOf(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var h struct {
		TS string `json:"ts"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatal(err)
	}
	return h.TS
}

func TestReadAllAscending(t *testing.T) {
	r, _ := newRecorder(t)
	fill(t, r, 5)
	rows, truncated, err := r.Read(context.Background(), time.Time{}, time.Time{}, 10)
	if err != nil || truncated || len(rows) != 5 {
		t.Fatalf("rows=%d truncated=%v err=%v", len(rows), truncated, err)
	}
	if tsOf(t, rows[0]) != "2026-11-11T05:00:00Z" || tsOf(t, rows[4]) != "2026-11-11T05:04:00Z" {
		t.Fatalf("%s %s", rows[0], rows[4])
	}
}

func TestReadSinceIsInclusiveUntilIsExclusive(t *testing.T) {
	r, _ := newRecorder(t)
	fill(t, r, 10)
	rows, _, err := r.Read(context.Background(), base.Add(2*time.Minute), base.Add(5*time.Minute), 100)
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	if tsOf(t, rows[0]) != "2026-11-11T05:02:00Z" || tsOf(t, rows[2]) != "2026-11-11T05:04:00Z" {
		t.Fatalf("%s %s", rows[0], rows[2])
	}
}

func TestReadLimitAndTruncated(t *testing.T) {
	r, _ := newRecorder(t)
	fill(t, r, 5)
	rows, truncated, err := r.Read(context.Background(), time.Time{}, time.Time{}, 3)
	if err != nil || !truncated || len(rows) != 3 {
		t.Fatalf("rows=%d truncated=%v err=%v", len(rows), truncated, err)
	}
	rows, truncated, err = r.Read(context.Background(), time.Time{}, time.Time{}, 5)
	if err != nil || truncated || len(rows) != 5 {
		t.Fatalf("exact fit must not be truncated: rows=%d truncated=%v err=%v", len(rows), truncated, err)
	}
}

func TestReadMissingFileIsEmpty(t *testing.T) {
	r, path := newRecorder(t)
	os.Remove(path)
	rows, truncated, err := r.Read(context.Background(), time.Time{}, time.Time{}, 10)
	if err != nil || truncated || rows == nil || len(rows) != 0 {
		t.Fatalf("rows=%v truncated=%v err=%v", rows, truncated, err)
	}
	if b, _ := json.Marshal(rows); string(b) != "[]" {
		t.Fatalf("%s", b)
	}
}

func TestReadSkipsMalformedAndPartialLines(t *testing.T) {
	r, path := newRecorder(t)
	fill(t, r, 2)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("not json\n{\"ts\":\"garbage\"}\n{}\n")
	f.Close()
	if err := r.Record(reading(2)); err != nil {
		t.Fatal(err)
	}
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"ts":"2026-11-11T05:03:00Z","temp_c":2`)
	f.Close()
	rows, _, err := r.Read(context.Background(), time.Time{}, time.Time{}, 100)
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
}

func TestReadHonoursCancelledContext(t *testing.T) {
	r, path := newRecorder(t)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	line := `{"ts":"2026-11-11T05:00:00Z","valid":true}` + "\n"
	f.WriteString(strings.Repeat(line, 2*ctxCheckEvery))
	f.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := r.Read(ctx, time.Time{}, time.Time{}, 100000); err == nil {
		t.Fatal("expected context error")
	}
}

func TestReadReturnsIndependentCopies(t *testing.T) {
	r, _ := newRecorder(t)
	fill(t, r, 3)
	rows, _, err := r.Read(context.Background(), time.Time{}, time.Time{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		if !strings.Contains(string(row), `"ts":"2026-11-11T05:0`+string(rune('0'+i))+`:00Z"`) {
			t.Fatalf("row %d corrupted: %s", i, row)
		}
	}
}
