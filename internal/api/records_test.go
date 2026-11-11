// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeRecords struct {
	rows      []json.RawMessage
	truncated bool
	err       error
	calls     int
	since     time.Time
	until     time.Time
	limit     int
}

func (f *fakeRecords) Read(_ context.Context, since, until time.Time, limit int) ([]json.RawMessage, bool, error) {
	f.calls++
	f.since, f.until, f.limit = since, until, limit
	return f.rows, f.truncated, f.err
}

func recordsEnv(token string, fr *fakeRecords) *env {
	e := newEnv(token)
	if fr != nil {
		e.cfg.Records = fr
	}
	return e
}

func TestRecordsDisabled(t *testing.T) {
	e := recordsEnv("", nil)
	rec := e.do("GET", "/v1/records", nil)
	if rec.Code != 404 || errCode(t, rec) != "not_found" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestRecordsBody(t *testing.T) {
	fr := &fakeRecords{
		rows:      []json.RawMessage{json.RawMessage(`{"ts":"2026-11-11T05:00:00Z","valid":true}`), json.RawMessage(`{"ts":"2026-11-11T05:01:00Z","valid":true}`)},
		truncated: true,
	}
	e := recordsEnv("", fr)
	rec := e.do("GET", "/v1/records", nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	var body struct {
		Count     int              `json:"count"`
		Truncated bool             `json:"truncated"`
		Records   []map[string]any `json:"records"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Count != 2 || !body.Truncated || len(body.Records) != 2 || body.Records[1]["ts"] != "2026-11-11T05:01:00Z" {
		t.Fatalf("%+v", body)
	}
	if fr.limit != defaultRecordLimit || !fr.since.IsZero() || !fr.until.IsZero() {
		t.Fatalf("defaults: limit=%d since=%v until=%v", fr.limit, fr.since, fr.until)
	}
}

func TestRecordsEmptyIsEmptyArray(t *testing.T) {
	e := recordsEnv("", &fakeRecords{rows: []json.RawMessage{}})
	rec := e.do("GET", "/v1/records", nil)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["records"]) != "[]" || string(raw["count"]) != "0" || string(raw["truncated"]) != "false" {
		t.Fatalf("%s", rec.Body)
	}
}

func TestRecordsQueryParams(t *testing.T) {
	fr := &fakeRecords{rows: []json.RawMessage{}}
	e := recordsEnv("", fr)
	rec := e.do("GET", "/v1/records?since=2026-11-11T05:00:00Z&until=2026-11-11T16:00:00%2B09:00&limit=250", nil)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if !fr.since.Equal(time.Date(2026, 11, 11, 5, 0, 0, 0, time.UTC)) ||
		!fr.until.Equal(time.Date(2026, 11, 11, 7, 0, 0, 0, time.UTC)) || fr.limit != 250 {
		t.Fatalf("since=%v until=%v limit=%d", fr.since, fr.until, fr.limit)
	}
}

func TestRecordsBadQuery(t *testing.T) {
	for _, q := range []string{
		"since=yesterday", "until=x", "limit=0", "limit=5001", "limit=abc", "limit=",
		"since=2026-11-11T06:00:00Z&until=2026-11-11T05:00:00Z",
		"since=2026-11-11T05:00:00Z&until=2026-11-11T05:00:00Z",
	} {
		fr := &fakeRecords{}
		e := recordsEnv("", fr)
		rec := e.do("GET", "/v1/records?"+q, nil)
		if rec.Code != 400 || errCode(t, rec) != "bad_request" || fr.calls != 0 {
			t.Errorf("%s: %d %s calls=%d", q, rec.Code, rec.Body, fr.calls)
		}
	}
}

func TestRecordsReadErrorDoesNotLeakDetails(t *testing.T) {
	e := recordsEnv("", &fakeRecords{err: errors.New("open /var/lib/air-sensor-api/readings.jsonl: permission denied")})
	rec := e.do("GET", "/v1/records", nil)
	if rec.Code != 500 || errCode(t, rec) != "record_read_failed" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if body := rec.Body.String(); len(body) == 0 || strings.Contains(body, "/var/lib") {
		t.Fatalf("leaked path: %s", body)
	}
}

func TestRecordsRequiresAuth(t *testing.T) {
	const token = "0123456789abcdef"
	fr := &fakeRecords{rows: []json.RawMessage{}}
	e := recordsEnv(token, fr)
	if rec := e.do("GET", "/v1/records", nil); rec.Code != 401 || fr.calls != 0 {
		t.Fatalf("%d calls=%d", rec.Code, fr.calls)
	}
	if rec := e.do("GET", "/v1/records", map[string]string{"Authorization": "Bearer " + token}); rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
}

func TestRecordsHEAD(t *testing.T) {
	e := recordsEnv("", &fakeRecords{rows: []json.RawMessage{}})
	g := e.do("GET", "/v1/records", nil)
	h := e.do("HEAD", "/v1/records", nil)
	if g.Code != h.Code || h.Body.Len() != 0 || g.Header().Get("Content-Length") != h.Header().Get("Content-Length") {
		t.Fatalf("GET %d HEAD %d body=%d", g.Code, h.Code, h.Body.Len())
	}
}

func TestRecordsMethodNotAllowed(t *testing.T) {
	e := recordsEnv("", &fakeRecords{})
	if rec := e.do("POST", "/v1/records", nil); rec.Code != 405 {
		t.Fatalf("%d", rec.Code)
	}
}
