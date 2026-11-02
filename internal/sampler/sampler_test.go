// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package sampler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/furu04/air-sensor-api/internal/ens160"
	"github.com/furu04/air-sensor-api/internal/i2c"
	"github.com/furu04/air-sensor-api/internal/i2c/i2ctest"
	"github.com/furu04/air-sensor-api/internal/store"
)

type fakeClock struct {
	mu   sync.Mutex
	now  time.Time
	mono time.Duration
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Mono() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mono
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	c.mono += d
}

type fakeSession struct {
	temp, rh   float64
	measureErr error
	compErr    error
	gasErr     error
	gas        ens160.Data
	compTemp   float64
	compRH     float64
	compCalls  int
	closeCalls int
}

func (s *fakeSession) Measure() (float64, float64, error) { return s.temp, s.rh, s.measureErr }
func (s *fakeSession) Compensate(t, rh float64) error {
	s.compTemp, s.compRH = t, rh
	s.compCalls++
	return s.compErr
}
func (s *fakeSession) ReadGas() (ens160.Data, error) { return s.gas, s.gasErr }
func (s *fakeSession) Close() error                  { s.closeCalls++; return nil }

type harness struct {
	sm       *Sampler
	st       *store.Store
	clk      *fakeClock
	sess     *fakeSession
	opens    int
	openErrs int
	logs     *bytes.Buffer
}

func newHarness(offset float64) *harness {
	h := &harness{
		clk:  &fakeClock{now: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC), mono: time.Minute},
		sess: &fakeSession{temp: 25, rh: 50, gas: ens160.Data{AQI: 1, TVOCPPB: 10, ECO2PPM: 400}},
		logs: &bytes.Buffer{},
	}
	h.st = store.New(100, h.clk)
	open := func() (Session, error) {
		h.opens++
		if h.openErrs > 0 {
			h.openErrs--
			return nil, errors.New("no device")
		}
		return h.sess, nil
	}
	log := slog.New(slog.NewTextHandler(h.logs, nil))
	h.sm = New(open, h.st, Config{Interval: time.Second, TempOffset: offset}, h.clk, log)
	return h
}

func (h *harness) tick() {
	h.clk.advance(time.Second)
	h.sm.Tick()
}

func TestNormalTick(t *testing.T) {
	h := newHarness(-2)
	h.sess.temp, h.sess.rh = 27, 40
	h.tick()
	if h.sess.compTemp != 25 || h.sess.compRH != 40 {
		t.Fatalf("comp=%v,%v", h.sess.compTemp, h.sess.compRH)
	}
	r, ok := h.st.Latest()
	if !ok || h.st.Len() != 1 || r.TempC != 25 || r.RHPct != 40 {
		t.Fatalf("%+v", r)
	}
}

func TestRounding(t *testing.T) {
	tests := []struct {
		t, rh, wantT, wantRH float64
	}{
		{24.3099, 45.2000, 24.31, 45.2},
		{24.0, 45.04, 24, 45.0},
		{24.0, 100.4, 24, 100},
		{24.0, -0.3, 24, 0},
	}
	for _, tt := range tests {
		h := newHarness(0)
		h.sess.temp, h.sess.rh = tt.t, tt.rh
		h.tick()
		r, _ := h.st.Latest()
		if r.TempC != tt.wantT || r.RHPct != tt.wantRH {
			t.Errorf("%+v: got %v %v", tt, r.TempC, r.RHPct)
		}
	}
	h := newHarness(0)
	h.sess.temp, h.sess.rh = 24.3099, 45.2
	h.tick()
	if h.sess.compTemp != 24.3099 {
		t.Fatalf("compensation got rounded value: %v", h.sess.compTemp)
	}
}

func TestValidityStored(t *testing.T) {
	h := newHarness(0)
	h.sess.gas.Validity = 1
	h.tick()
	r, ok := h.st.Latest()
	if !ok || r.Validity != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestFailureNotStored(t *testing.T) {
	h := newHarness(0)
	h.sess.measureErr = errors.New("boom")
	h.tick()
	if h.st.Len() != 0 {
		t.Fatal("stored on failure")
	}
}

func TestCompensateFailure(t *testing.T) {
	h := newHarness(0)
	h.sess.compErr = errors.New("range")
	h.tick()
	s := h.sm.Snapshot()
	if h.st.Len() != 0 || s.ConsecutiveFailures != 1 || s.TotalFailures != 1 {
		t.Fatalf("%+v len=%d", s, h.st.Len())
	}
}

func TestGasFailure(t *testing.T) {
	h := newHarness(0)
	h.sess.gasErr = errors.New("status")
	h.tick()
	if h.st.Len() != 0 || h.sm.Snapshot().ConsecutiveFailures != 1 {
		t.Fatal("gas failure not counted")
	}
}

func TestReinit(t *testing.T) {
	h := newHarness(0)
	h.sess.measureErr = errors.New("boom")
	for i := 1; i <= 4; i++ {
		h.tick()
	}
	if h.sess.closeCalls != 0 || h.sm.Snapshot().Reinits != 0 {
		t.Fatal("early reinit")
	}
	h.tick()
	if h.sess.closeCalls != 1 || h.sm.Snapshot().Reinits != 1 || h.sm.Snapshot().Initialized {
		t.Fatalf("%+v close=%d", h.sm.Snapshot(), h.sess.closeCalls)
	}
	opensBefore := h.opens
	h.tick()
	if h.opens != opensBefore+1 {
		t.Fatalf("opens=%d", h.opens)
	}
	for i := 7; i <= 10; i++ {
		h.tick()
	}
	if h.sm.Snapshot().Reinits != 2 {
		t.Fatalf("reinits=%d", h.sm.Snapshot().Reinits)
	}
}

func TestRecovery(t *testing.T) {
	h := newHarness(0)
	h.sess.measureErr = errors.New("boom")
	h.tick()
	h.tick()
	h.sess.measureErr = nil
	h.tick()
	s := h.sm.Snapshot()
	if s.ConsecutiveFailures != 0 || s.TotalFailures != 2 {
		t.Fatalf("%+v", s)
	}
	if !strings.Contains(h.logs.String(), "sample recovered") {
		t.Fatal("no recovery log")
	}
}

func TestOpenFailureAtStartup(t *testing.T) {
	h := newHarness(0)
	h.openErrs = 3
	for i := 0; i < 3; i++ {
		h.tick()
	}
	s := h.sm.Snapshot()
	if s.ConsecutiveFailures != 3 || s.Reinits != 0 || h.st.Len() != 0 || s.Initialized {
		t.Fatalf("%+v", s)
	}
	h.tick()
	if h.st.Len() != 1 || !h.sm.Snapshot().Initialized {
		t.Fatal("did not recover")
	}
}

func TestSnapshotMono(t *testing.T) {
	h := newHarness(0)
	h.sess.measureErr = errors.New("boom")
	h.tick()
	errMono := h.clk.Mono()
	h.sess.measureErr = nil
	h.tick()
	okMono := h.clk.Mono()
	s := h.sm.Snapshot()
	if !s.HasLastError || s.LastErrorMono != errMono || !s.HasLastSuccess || s.LastSuccessMono != okMono || s.LastError != "boom" {
		t.Fatalf("%+v", s)
	}
}

func TestLastErrorTruncated(t *testing.T) {
	h := newHarness(0)
	h.sess.measureErr = errors.New(strings.Repeat("x", 500))
	h.tick()
	if n := len(h.sm.Snapshot().LastError); n != 200 {
		t.Fatalf("len=%d", n)
	}
}

func TestRunClosesSession(t *testing.T) {
	h := newHarness(0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.sm.Run(ctx); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for h.st.Len() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
	}
	if h.sess.closeCalls != 1 {
		t.Fatalf("closeCalls=%d", h.sess.closeCalls)
	}
}

func TestLogging(t *testing.T) {
	h := newHarness(0)
	h.sess.measureErr = errors.New("boom")
	for i := 1; i <= 10; i++ {
		h.tick()
	}
	out := h.logs.String()
	var consecutive []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "sample failed") {
			for _, f := range strings.Fields(line) {
				if strings.HasPrefix(f, "consecutive=") {
					consecutive = append(consecutive, strings.TrimPrefix(f, "consecutive="))
				}
			}
		}
	}
	if fmt.Sprint(consecutive) != "[1 5 10]" {
		t.Fatalf("sample failed logged at %v\n%s", consecutive, out)
	}
	if n := strings.Count(out, "msg=reinit"); n != 2 {
		t.Fatalf("reinit logs=%d\n%s", n, out)
	}
}

func TestNewOpener(t *testing.T) {
	sim := i2ctest.New()
	sim.SetClimate(25, 50)
	sim.SetGas(3, 300, 400, 0)
	open := NewOpener(func() (i2c.Bus, error) { return sim, nil }, 0x38, 0x53, true, func(time.Duration) {})
	sess, err := open()
	if err != nil {
		t.Fatal(err)
	}
	if sim.OpMode() != 0x02 {
		t.Fatalf("opmode=%x", sim.OpMode())
	}
	if _, _, err := sess.Measure(); err != nil {
		t.Fatal(err)
	}
	if err := sess.Compensate(25, 50); err != nil {
		t.Fatal(err)
	}
	d, err := sess.ReadGas()
	if err != nil || d.AQI != 3 || d.TVOCPPB != 300 {
		t.Fatalf("%+v %v", d, err)
	}
	if err := sess.Close(); err != nil || sim.CloseCount() != 1 {
		t.Fatalf("close=%d err=%v", sim.CloseCount(), err)
	}
}

func TestNewOpenerPartIDMismatch(t *testing.T) {
	sim := i2ctest.New()
	sim.SetPartID(0x0161)
	open := NewOpener(func() (i2c.Bus, error) { return sim, nil }, 0x38, 0x53, true, func(time.Duration) {})
	sess, err := open()
	if err == nil || sess != nil {
		t.Fatalf("sess=%v err=%v", sess, err)
	}
	if sim.CloseCount() != 1 {
		t.Fatalf("close=%d", sim.CloseCount())
	}
}

func TestNewOpenerBusFailure(t *testing.T) {
	open := NewOpener(func() (i2c.Bus, error) { return nil, errors.New("no bus") }, 0x38, 0x53, true, func(time.Duration) {})
	sess, err := open()
	if err == nil || sess != nil {
		t.Fatalf("sess=%v err=%v", sess, err)
	}
}
