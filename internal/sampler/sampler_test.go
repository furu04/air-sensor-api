// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package sampler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
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
func (s *fakeSession) Climate() ClimateSource        { return s }
func (s *fakeSession) Gas() GasSource                { return s }
func (s *fakeSession) CO2() CO2Source                { return nil }

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
	if h.sess.compTemp != 25 || math.Abs(h.sess.compRH-45.023) > 0.01 {
		t.Fatalf("comp=%v,%v", h.sess.compTemp, h.sess.compRH)
	}
	r, ok := h.st.Latest()
	if !ok || h.st.Len() != 1 || r.TempC != 25 || r.RHPct != 45 {
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
	open := NewOpener(func() (i2c.Bus, error) { return sim, nil }, func(time.Duration) {}, WithAHT21(0x38, true), WithENS160(0x53))
	sess, err := open()
	if err != nil {
		t.Fatal(err)
	}
	if sim.OpMode() != 0x02 {
		t.Fatalf("opmode=%x", sim.OpMode())
	}
	if sess.CO2() != nil {
		t.Fatal("CO2 source present although SCD4x not requested")
	}
	if _, _, err := sess.Climate().Measure(); err != nil {
		t.Fatal(err)
	}
	if err := sess.Gas().Compensate(25, 50); err != nil {
		t.Fatal(err)
	}
	d, err := sess.Gas().ReadGas()
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
	open := NewOpener(func() (i2c.Bus, error) { return sim, nil }, func(time.Duration) {}, WithAHT21(0x38, true), WithENS160(0x53))
	sess, err := open()
	if err == nil || sess != nil {
		t.Fatalf("sess=%v err=%v", sess, err)
	}
	if sim.CloseCount() != 1 {
		t.Fatalf("close=%d", sim.CloseCount())
	}
}

func TestNewOpenerBusFailure(t *testing.T) {
	open := NewOpener(func() (i2c.Bus, error) { return nil, errors.New("no bus") }, func(time.Duration) {}, WithAHT21(0x38, true), WithENS160(0x53))
	sess, err := open()
	if err == nil || sess != nil {
		t.Fatalf("sess=%v err=%v", sess, err)
	}
}

type fakeCO2Session struct {
	*fakeSession
	co2    int
	co2Err error
	calls  int
}

func (s *fakeCO2Session) ReadCO2() (int, error) { s.calls++; return s.co2, s.co2Err }
func (s *fakeCO2Session) CO2() CO2Source        { return s }

func newCO2Harness() (*harness, *fakeCO2Session) {
	h := newHarness(0)
	cs := &fakeCO2Session{fakeSession: h.sess, co2: 812}
	h.sm.open = func() (Session, error) { h.opens++; return cs, nil }
	return h, cs
}

func TestCO2Stored(t *testing.T) {
	h, cs := newCO2Harness()
	h.tick()
	r, ok := h.st.Latest()
	if !ok || !r.HasCO2 || r.CO2PPM != 812 || cs.calls != 1 {
		t.Fatalf("%+v calls=%d", r, cs.calls)
	}
}

func TestCO2ZeroIsStored(t *testing.T) {
	h, cs := newCO2Harness()
	cs.co2 = 0
	h.tick()
	r, _ := h.st.Latest()
	if !r.HasCO2 || r.CO2PPM != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestNoCO2WithoutReader(t *testing.T) {
	h := newHarness(0)
	h.tick()
	r, _ := h.st.Latest()
	if r.HasCO2 || r.CO2PPM != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestCO2Failure(t *testing.T) {
	h, cs := newCO2Harness()
	cs.co2Err = errors.New("scd4x: crc mismatch")
	h.tick()
	if h.st.Len() != 0 {
		t.Fatal("reading stored despite CO2 failure")
	}
	snap := h.sm.Snapshot()
	if snap.ConsecutiveFailures != 1 || !strings.Contains(snap.LastError, "scd4x") {
		t.Fatalf("%+v", snap)
	}
}

func TestCO2FailureTriggersReinit(t *testing.T) {
	h, cs := newCO2Harness()
	cs.co2Err = errors.New("scd4x: measurement not ready")
	for i := 0; i < reinitEvery; i++ {
		h.tick()
	}
	if h.sm.Snapshot().Reinits != 1 || cs.closeCalls != 1 {
		t.Fatalf("%+v close=%d", h.sm.Snapshot(), cs.closeCalls)
	}
}

func opener(sim *i2ctest.Sim, opts ...OpenerOption) OpenFunc {
	return NewOpener(func() (i2c.Bus, error) { return sim, nil }, func(time.Duration) {}, opts...)
}

func TestNewOpenerWithSCD4x(t *testing.T) {
	sim := i2ctest.New()
	sim.EnableSCD4x()
	sim.SetCO2(1500)
	sess, err := opener(sim, WithAHT21(0x38, true), WithENS160(0x53), WithSCD4x(0x62))()
	if err != nil {
		t.Fatal(err)
	}
	if sess.Climate() == nil || sess.Gas() == nil || sess.CO2() == nil {
		t.Fatal("missing source")
	}
	ppm, err := sess.CO2().ReadCO2()
	if err != nil || ppm != 1500 {
		t.Fatalf("ppm=%d err=%v", ppm, err)
	}
	if err := sess.Close(); err != nil || sim.CloseCount() != 1 {
		t.Fatalf("close=%d err=%v", sim.CloseCount(), err)
	}
}

func TestNewOpenerSCD4xOnly(t *testing.T) {
	sim := i2ctest.New()
	sim.EnableSCD4x()
	sess, err := opener(sim, WithSCD4x(0x62))()
	if err != nil {
		t.Fatal(err)
	}
	if sess.Climate() == nil || sess.Gas() != nil || sess.CO2() == nil {
		t.Fatalf("climate=%v gas=%v co2=%v", sess.Climate(), sess.Gas(), sess.CO2())
	}
	for _, w := range sim.Writes() {
		if w.Addr != 0x62 {
			t.Fatalf("touched disabled sensor: %+v", w)
		}
	}
	for _, r := range sim.Reads() {
		if r.Addr != 0x62 {
			t.Fatalf("touched disabled sensor: %+v", r)
		}
	}
}

func TestNewOpenerOmitsDisabled(t *testing.T) {
	sim := i2ctest.New()
	sim.EnableSCD4x()
	sess, err := opener(sim, WithAHT21(0x38, true), WithENS160(0x53))()
	if err != nil {
		t.Fatal(err)
	}
	if sess.CO2() != nil {
		t.Fatal("CO2 source present although not requested")
	}
	if sim.SCDMeasuring() {
		t.Fatal("SCD4x touched although not requested")
	}
	sess, err = opener(sim, WithENS160(0x53))()
	if err != nil || sess.Climate() != nil || sess.Gas() == nil {
		t.Fatalf("err=%v", err)
	}
}

func TestNewOpenerNoSensors(t *testing.T) {
	sim := i2ctest.New()
	if _, err := opener(sim)(); !errors.Is(err, ErrNoSensors) {
		t.Fatalf("err=%v", err)
	}
	if sim.CloseCount() != 0 {
		t.Fatal("bus opened without sensors")
	}
}

func TestNewOpenerSCD4xAbsent(t *testing.T) {
	sim := i2ctest.New()
	sess, err := opener(sim, WithAHT21(0x38, true), WithENS160(0x53), WithSCD4x(0x62))()
	if err == nil || sess != nil {
		t.Fatalf("sess=%v err=%v", sess, err)
	}
	if !strings.Contains(err.Error(), "init scd4x") {
		t.Fatalf("err=%v", err)
	}
	if sim.CloseCount() != 1 {
		t.Fatalf("close=%d", sim.CloseCount())
	}
}

type onlyGas struct{ *fakeSession }

func (onlyGas) Climate() ClimateSource { return nil }

type onlyClimate struct{ *fakeSession }

func (onlyClimate) Gas() GasSource { return nil }

func harnessWith(h *harness, sess Session) {
	h.sm.open = func() (Session, error) { h.opens++; return sess, nil }
}

func TestTickGasWithoutClimate(t *testing.T) {
	h := newHarness(0)
	harnessWith(h, onlyGas{h.sess})
	h.tick()
	r, ok := h.st.Latest()
	if !ok || r.HasClimate || !r.HasGas || r.TVOCPPB != 10 || h.sess.compCalls != 0 {
		t.Fatalf("%+v comp=%d", r, h.sess.compCalls)
	}
}

func TestTickClimateWithoutGas(t *testing.T) {
	h := newHarness(1)
	harnessWith(h, onlyClimate{h.sess})
	h.tick()
	r, ok := h.st.Latest()
	if !ok || !r.HasClimate || r.HasGas || r.TempC != 26 || h.sess.compCalls != 0 {
		t.Fatalf("%+v comp=%d", r, h.sess.compCalls)
	}
}

func TestCO2Only(t *testing.T) {
	h := newHarness(0)
	cs := &fakeCO2Session{fakeSession: h.sess, co2: 640}
	harnessWith(h, co2Only{cs})
	h.tick()
	r, ok := h.st.Latest()
	if !ok || r.HasClimate || r.HasGas || !r.HasCO2 || r.CO2PPM != 640 || !r.Valid() {
		t.Fatalf("%+v", r)
	}
}

type co2Only struct{ *fakeCO2Session }

func (co2Only) Climate() ClimateSource { return nil }
func (co2Only) Gas() GasSource         { return nil }

func readMeasurementCount(sim *i2ctest.Sim) int {
	n := 0
	for _, w := range sim.Writes() {
		if w.Addr == 0x62 && len(w.Data) == 2 && w.Data[0] == 0xEC && w.Data[1] == 0x05 {
			n++
		}
	}
	return n
}

func simHarness(sim *i2ctest.Sim, offset float64, opts ...OpenerOption) *harness {
	h := newHarness(offset)
	open := NewOpener(func() (i2c.Bus, error) { return sim, nil }, func(time.Duration) {}, opts...)
	h.sm.open = open
	return h
}

func TestSCD4xSuppliesClimateWhenAHT21Disabled(t *testing.T) {
	sim := i2ctest.New()
	sim.EnableSCD4x()
	sim.SetSCDClimate(24.0, 41.0)
	sim.SetCO2(910)
	h := simHarness(sim, -1.5, WithSCD4x(0x62))
	h.tick()
	r, ok := h.st.Latest()
	if !ok || !r.HasClimate || !r.HasCO2 || r.CO2PPM != 910 {
		t.Fatalf("%+v", r)
	}
	if r.TempC != 22.5 || r.RHPct != 44.9 {
		t.Fatalf("temp=%v rh=%v", r.TempC, r.RHPct)
	}
}

func TestSCD4xReadOncePerTick(t *testing.T) {
	sim := i2ctest.New()
	sim.EnableSCD4x()
	h := simHarness(sim, 0, WithSCD4x(0x62))
	for i := 1; i <= 3; i++ {
		h.tick()
		if n := readMeasurementCount(sim); n != i {
			t.Fatalf("tick %d: read_measurement x%d", i, n)
		}
	}
}

func TestAHT21WinsOverSCD4xClimate(t *testing.T) {
	sim := i2ctest.New()
	sim.EnableSCD4x()
	sim.SetClimate(21, 55)
	sim.SetSCDClimate(30, 20)
	sim.SetCO2(700)
	h := simHarness(sim, 0, WithAHT21(0x38, true), WithENS160(0x53), WithSCD4x(0x62))
	h.tick()
	r, ok := h.st.Latest()
	if !ok || r.TempC != 21 || r.RHPct != 55 || r.CO2PPM != 700 {
		t.Fatalf("%+v", r)
	}
	if n := readMeasurementCount(sim); n != 1 {
		t.Fatalf("read_measurement x%d", n)
	}
}

func TestENS160CompensatedFromSCD4xClimate(t *testing.T) {
	sim := i2ctest.New()
	sim.EnableSCD4x()
	sim.SetSCDClimate(20, 40)
	h := simHarness(sim, 0, WithENS160(0x53), WithSCD4x(0x62))
	h.tick()
	tin, rhin := sim.Compensation()
	if wantT := uint16(math.Round((20 + 273.15) * 64)); tin < wantT-2 || tin > wantT+2 {
		t.Fatalf("temp comp=%d want ~%d", tin, wantT)
	}
	if wantRH := uint16(40 * 512); rhin < wantRH-40 || rhin > wantRH+40 {
		t.Fatalf("rh comp=%d want ~%d", rhin, wantRH)
	}
}

func TestSCD4xSampleNotReusedAfterLaterFailure(t *testing.T) {
	sim := i2ctest.New()
	sim.EnableSCD4x()
	sim.SetCO2(500)
	h := simHarness(sim, 0, WithENS160(0x53), WithSCD4x(0x62))
	sim.SetStatusError(true)
	h.tick()
	if h.st.Len() != 0 {
		t.Fatal("stored despite ENS160 failure")
	}
	sim.SetStatusError(false)
	sim.SetCO2(1200)
	h.tick()
	r, ok := h.st.Latest()
	if !ok || r.CO2PPM != 1200 {
		t.Fatalf("%+v", r)
	}
}

func TestSCD4xMeasureFailure(t *testing.T) {
	sim := i2ctest.New()
	sim.EnableSCD4x()
	h := simHarness(sim, 0, WithSCD4x(0x62))
	h.tick()
	sim.SetSCDBadCRC(true)
	h.tick()
	if h.st.Len() != 1 || h.sm.Snapshot().ConsecutiveFailures != 1 {
		t.Fatalf("len=%d snap=%+v", h.st.Len(), h.sm.Snapshot())
	}
}

func TestHumidityRescaledByTempOffset(t *testing.T) {
	tests := []struct {
		name         string
		offset       float64
		temp, rh     float64
		wantT, wantH float64
	}{
		{"cooling raises rh", -5, 28.31, 34, 23.31, 45.7},
		{"warming lowers rh", 10, 20, 30, 30, 16.5},
		{"clamped at 100", -10, 25, 90, 15, 100},
		{"zero offset leaves rh untouched", 0, 28.31, 34, 28.31, 34},
		{"zero rh stays zero", -5, 28, 0, 23, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(tt.offset)
			h.sess.temp, h.sess.rh = tt.temp, tt.rh
			h.tick()
			r, ok := h.st.Latest()
			if !ok || r.TempC != tt.wantT || r.RHPct != tt.wantH {
				t.Fatalf("temp=%v rh=%v want %v/%v", r.TempC, r.RHPct, tt.wantT, tt.wantH)
			}
			if tt.wantH != 100 && math.Abs(h.sess.compRH-tt.wantH) > 0.05 {
				t.Fatalf("compensation rh=%v want ~%v", h.sess.compRH, tt.wantH)
			}
		})
	}
}

func TestHumidityClampedBeforeRescale(t *testing.T) {
	h := newHarness(-5)
	h.sess.temp, h.sess.rh = 25, 100.4
	h.tick()
	r, _ := h.st.Latest()
	if r.RHPct != 100 {
		t.Fatalf("rh=%v", r.RHPct)
	}
}
