// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package scd4x

import (
	"errors"
	"math"
	"syscall"
	"testing"
	"time"

	"github.com/furu04/air-sensor-api/internal/crc8"
	"github.com/furu04/air-sensor-api/internal/i2c/i2ctest"
)

type sleeps []time.Duration

func (s *sleeps) fn(d time.Duration) { *s = append(*s, d) }

func (s sleeps) count(d time.Duration) int {
	n := 0
	for _, v := range s {
		if v == d {
			n++
		}
	}
	return n
}

func newSim() *i2ctest.Sim {
	sim := i2ctest.New()
	sim.EnableSCD4x()
	return sim
}

func cmdsWritten(sim *i2ctest.Sim) []uint16 {
	var out []uint16
	for _, w := range sim.Writes() {
		if w.Addr == DefaultAddr && len(w.Data) == 2 {
			out = append(out, uint16(w.Data[0])<<8|uint16(w.Data[1]))
		}
	}
	return out
}

func TestInitSequence(t *testing.T) {
	sim := newSim()
	var sl sleeps
	s := New(sim, DefaultAddr, sl.fn)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	got := cmdsWritten(sim)
	want := []uint16{cmdStopPeriodic, cmdSerialNumber, cmdStartPeriodic}
	if len(got) != len(want) {
		t.Fatalf("cmds=%X want %X", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cmds=%X want %X", got, want)
		}
	}
	if sl[0] != stopWait {
		t.Fatalf("first sleep=%v want %v", sl[0], stopWait)
	}
	if !sim.SCDMeasuring() {
		t.Fatal("periodic measurement not started")
	}
}

func TestInitWhileAlreadyMeasuring(t *testing.T) {
	sim := newSim()
	sim.SetSCDMeasuring(true)
	s := New(sim, DefaultAddr, func(time.Duration) {})
	if err := s.Init(); err != nil {
		t.Fatalf("init after unclean shutdown: %v", err)
	}
	if !sim.SCDMeasuring() {
		t.Fatal("periodic measurement not restarted")
	}
}

func TestInitAbsent(t *testing.T) {
	sim := i2ctest.New()
	s := New(sim, DefaultAddr, func(time.Duration) {})
	if err := s.Init(); !errors.Is(err, syscall.EREMOTEIO) {
		t.Fatalf("err=%v", err)
	}
}

func TestInitBadSerialCRC(t *testing.T) {
	sim := newSim()
	sim.SetSCDBadCRC(true)
	s := New(sim, DefaultAddr, func(time.Duration) {})
	if err := s.Init(); !errors.Is(err, ErrCRC) {
		t.Fatalf("err=%v", err)
	}
	if sim.SCDMeasuring() {
		t.Fatal("measurement started despite failed init")
	}
}

func TestReadCO2(t *testing.T) {
	for _, ppm := range []int{0, 400, 1234, 40000} {
		sim := newSim()
		sim.SetCO2(ppm)
		s := New(sim, DefaultAddr, func(time.Duration) {})
		if err := s.Init(); err != nil {
			t.Fatal(err)
		}
		got, err := readCO2(s)
		if err != nil || got != ppm {
			t.Fatalf("ppm=%d got=%d err=%v", ppm, got, err)
		}
	}
}

func TestReadCO2NotReadyThenReady(t *testing.T) {
	sim := newSim()
	sim.SetSCDNotReady(3)
	var sl sleeps
	s := New(sim, DefaultAddr, sl.fn)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	sl = nil
	got, err := readCO2(s)
	if err != nil || got != 800 {
		t.Fatalf("got=%d err=%v", got, err)
	}
	if n := sl.count(pollInterval); n != 3 {
		t.Fatalf("poll sleeps=%d want 3", n)
	}
}

func TestReadCO2NeverReady(t *testing.T) {
	sim := newSim()
	sim.SetSCDNotReady(1000)
	var sl sleeps
	s := New(sim, DefaultAddr, sl.fn)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	sl = nil
	if _, err := readCO2(s); !errors.Is(err, ErrNotReady) {
		t.Fatalf("err=%v", err)
	}
	if n := sl.count(pollInterval); n != pollAttempts-1 {
		t.Fatalf("poll sleeps=%d want %d", n, pollAttempts-1)
	}
	var total time.Duration
	for _, d := range sl {
		total += d
	}
	if total < 5*time.Second {
		t.Fatalf("total wait %v shorter than one measurement period", total)
	}
}

func TestReadCO2BadCRC(t *testing.T) {
	sim := newSim()
	s := New(sim, DefaultAddr, func(time.Duration) {})
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSCDBadCRC(true)
	if _, err := readCO2(s); !errors.Is(err, ErrCRC) {
		t.Fatalf("err=%v", err)
	}
}

func TestReadCO2OutOfRange(t *testing.T) {
	sim := newSim()
	sim.SetCO2(40001)
	s := New(sim, DefaultAddr, func(time.Duration) {})
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := readCO2(s); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("err=%v", err)
	}
}

func TestReadCO2Unplugged(t *testing.T) {
	sim := newSim()
	s := New(sim, DefaultAddr, func(time.Duration) {})
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetUnplugged(true)
	if _, err := readCO2(s); !errors.Is(err, syscall.EREMOTEIO) {
		t.Fatalf("err=%v", err)
	}
}

func TestNeverUsesRepeatedStart(t *testing.T) {
	sim := newSim()
	s := New(sim, DefaultAddr, func(time.Duration) {})
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if _, err := readCO2(s); err != nil {
		t.Fatal(err)
	}
	for _, r := range sim.Reads() {
		if r.Addr == DefaultAddr && len(r.W) != 0 {
			t.Fatalf("repeated start used: %+v", r)
		}
	}
}

type shortBus struct{ n int }

func (b shortBus) Write(uint8, []byte) error { return nil }
func (b shortBus) Read(_ uint8, n int) ([]byte, error) {
	out := make([]byte, b.n)
	for i := 0; i+2 < len(out); i += 3 {
		out[i+2] = crc8.Sum(out[i : i+2])
	}
	return out, nil
}
func (b shortBus) WriteRead(uint8, []byte, int) ([]byte, error) { return nil, nil }
func (b shortBus) Close() error                                 { return nil }

func TestShortRead(t *testing.T) {
	s := New(shortBus{n: 6}, DefaultAddr, func(time.Duration) {})
	if err := s.Init(); err == nil {
		t.Fatal("expected error for short serial read")
	}
	s = New(shortBus{n: 2}, DefaultAddr, func(time.Duration) {})
	if _, err := readCO2(s); err == nil {
		t.Fatal("expected error for short ready read")
	}
}

func readCO2(s *Sensor) (int, error) {
	m, err := s.Read()
	return m.CO2PPM, err
}

func TestReadClimate(t *testing.T) {
	tests := []struct{ temp, rh float64 }{{25, 50}, {-10, 5}, {40, 95}, {0, 0}}
	for _, tt := range tests {
		sim := newSim()
		sim.SetSCDClimate(tt.temp, tt.rh)
		s := New(sim, DefaultAddr, func(time.Duration) {})
		if err := s.Init(); err != nil {
			t.Fatal(err)
		}
		m, err := s.Read()
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(m.TempC-tt.temp) > 0.01 || math.Abs(m.RHPct-tt.rh) > 0.01 {
			t.Errorf("want %v/%v got %+v", tt.temp, tt.rh, m)
		}
	}
}

func TestConversionBounds(t *testing.T) {
	sim := newSim()
	s := New(sim, DefaultAddr, func(time.Duration) {})
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSCDClimate(130, 100)
	m, err := s.Read()
	if err != nil || math.Abs(m.TempC-130) > 0.01 || math.Abs(m.RHPct-100) > 0.01 {
		t.Fatalf("%+v %v", m, err)
	}
}
