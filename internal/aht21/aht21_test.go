// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package aht21

import (
	"bytes"
	"errors"
	"math"
	"syscall"
	"testing"
	"time"

	"github.com/furu04/air-sensor-api/internal/i2c/i2ctest"
)

type sleeps []time.Duration

func (s *sleeps) fn(d time.Duration) { *s = append(*s, d) }

type frameBus struct {
	frames [][]byte
	reads  int
}

func (b *frameBus) Write(addr uint8, data []byte) error { return nil }
func (b *frameBus) Read(addr uint8, n int) ([]byte, error) {
	i := b.reads
	if i >= len(b.frames) {
		i = len(b.frames) - 1
	}
	b.reads++
	return b.frames[i], nil
}
func (b *frameBus) WriteRead(addr uint8, w []byte, n int) ([]byte, error) { return []byte{0x18}, nil }
func (b *frameBus) Close() error                                          { return nil }

func hexb(v ...byte) []byte { return v }

func TestCRC8(t *testing.T) {
	tests := []struct {
		in   []byte
		want byte
	}{
		{hexb(0xBE, 0xEF), 0x92},
		{nil, 0xFF},
		{hexb(0x1C, 0x80, 0x00, 0x06, 0x00, 0x00), 0x4E},
		{hexb(0x1C, 0x00, 0x00, 0x03, 0x33, 0x33), 0x9B},
		{hexb(0x1C, 0x73, 0xB6, 0x45, 0xF1, 0xDE), 0x83},
	}
	for _, tt := range tests {
		if got := CRC8(tt.in); got != tt.want {
			t.Errorf("CRC8(% X)=%02X want %02X", tt.in, got, tt.want)
		}
	}
}

func TestConversion(t *testing.T) {
	tests := []struct {
		frame []byte
		temp  float64
		rh    float64
	}{
		{hexb(0x1C, 0x80, 0x00, 0x06, 0x00, 0x00, 0x4E), 25.00, 50.00},
		{hexb(0x1C, 0x00, 0x00, 0x03, 0x33, 0x33, 0x9B), -10.00, 0.00},
		{hexb(0x1C, 0x73, 0xB6, 0x45, 0xF1, 0xDE, 0x83), 24.31, 45.20},
	}
	for _, tt := range tests {
		var sl sleeps
		s := New(&frameBus{frames: [][]byte{tt.frame}}, 0x38, true, sl.fn)
		temp, rh, err := s.Measure()
		if err != nil {
			t.Fatalf("% X: %v", tt.frame, err)
		}
		if math.Abs(temp-tt.temp) > 0.001 || math.Abs(rh-tt.rh) > 0.001 {
			t.Errorf("% X: temp=%v rh=%v", tt.frame, temp, rh)
		}
	}
}

func TestMeasureBusyThenOK(t *testing.T) {
	sim := i2ctest.New()
	sim.SetBusy(2)
	var sl sleeps
	s := New(sim, 0x38, true, sl.fn)
	if _, _, err := s.Measure(); err != nil {
		t.Fatal(err)
	}
	want := sleeps{80 * time.Millisecond, 20 * time.Millisecond, 20 * time.Millisecond}
	if !equalSleeps(sl, want) {
		t.Fatalf("sleeps=%v", sl)
	}
}

func TestMeasureBusyForever(t *testing.T) {
	sim := i2ctest.New()
	sim.SetBusy(100)
	var sl sleeps
	s := New(sim, 0x38, true, sl.fn)
	_, _, err := s.Measure()
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err=%v", err)
	}
	want := sleeps{80 * time.Millisecond, 20 * time.Millisecond, 20 * time.Millisecond, 20 * time.Millisecond, 20 * time.Millisecond}
	if !equalSleeps(sl, want) {
		t.Fatalf("sleeps=%v", sl)
	}
	if n := len(sim.Reads()); n != 5 {
		t.Fatalf("reads=%d", n)
	}
}

func TestMeasureCRC(t *testing.T) {
	frame := hexb(0x1C, 0x80, 0x00, 0x06, 0x00, 0x00, 0x4E^0x01)
	var sl sleeps
	s := New(&frameBus{frames: [][]byte{frame}}, 0x38, true, sl.fn)
	if _, _, err := s.Measure(); !errors.Is(err, ErrCRC) {
		t.Fatalf("err=%v", err)
	}
	s = New(&frameBus{frames: [][]byte{frame}}, 0x38, false, sl.fn)
	if _, _, err := s.Measure(); err != nil {
		t.Fatalf("crc disabled: %v", err)
	}
}

func TestInitCalibrated(t *testing.T) {
	for _, st := range []byte{0x18, 0x1C} {
		sim := i2ctest.New()
		sim.SetAHTStatus(st)
		var sl sleeps
		s := New(sim, 0x38, true, sl.fn)
		if err := s.Init(); err != nil {
			t.Fatal(err)
		}
		if len(sim.Writes()) != 0 {
			t.Fatalf("status %02X: writes=%v", st, sim.Writes())
		}
		want := sleeps{100 * time.Millisecond, 10 * time.Millisecond}
		if !equalSleeps(sl, want) {
			t.Fatalf("sleeps=%v", sl)
		}
	}
}

func TestInitUncalibrated(t *testing.T) {
	sim := i2ctest.New()
	sim.SetAHTCalibrated(false)
	var sl sleeps
	s := New(sim, 0x38, true, sl.fn)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	w := sim.Writes()
	if len(w) != 1 || !bytes.Equal(w[0].Data, hexb(0xBE, 0x08, 0x00)) {
		t.Fatalf("writes=%v", w)
	}
	want := sleeps{100 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}
	if !equalSleeps(sl, want) {
		t.Fatalf("sleeps=%v", sl)
	}
}

func TestInitNotCalibrated(t *testing.T) {
	sim := i2ctest.New()
	sim.SetAHTCalibrated(false)
	sim.SetAHTInitWorks(false)
	var sl sleeps
	s := New(sim, 0x38, true, sl.fn)
	err := s.Init()
	if !errors.Is(err, ErrNotCalibrated) {
		t.Fatalf("err=%v", err)
	}
	if !bytes.Contains([]byte(err.Error()), []byte("status=0x00")) {
		t.Fatalf("msg=%v", err)
	}
}

func TestBusErrors(t *testing.T) {
	sim := i2ctest.New()
	sim.SetUnplugged(true)
	var sl sleeps
	s := New(sim, 0x38, true, sl.fn)
	if err := s.Init(); !errors.Is(err, syscall.EREMOTEIO) {
		t.Fatalf("init err=%v", err)
	}
	if _, _, err := s.Measure(); !errors.Is(err, syscall.EREMOTEIO) {
		t.Fatalf("measure err=%v", err)
	}
}

func equalSleeps(a, b sleeps) bool {
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
