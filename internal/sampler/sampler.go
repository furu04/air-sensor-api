// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package sampler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/furu04/air-sensor-api/internal/aht21"
	"github.com/furu04/air-sensor-api/internal/ens160"
	"github.com/furu04/air-sensor-api/internal/i2c"
	"github.com/furu04/air-sensor-api/internal/psychro"
	"github.com/furu04/air-sensor-api/internal/scd4x"
	"github.com/furu04/air-sensor-api/internal/store"
)

const (
	reinitEvery = 5
	maxErrorLen = 200
)

type ClimateSource interface {
	Measure() (tempC, rhPct float64, err error)
}

type GasSource interface {
	Compensate(tempC, rhPct float64) error
	ReadGas() (ens160.Data, error)
}

type CO2Source interface {
	ReadCO2() (ppm int, err error)
}

type Session interface {
	Climate() ClimateSource
	Gas() GasSource
	CO2() CO2Source
	Close() error
}

type OpenFunc func() (Session, error)

type ensSource struct{ s *ens160.Sensor }

func (e ensSource) Compensate(t, rh float64) error { return e.s.Compensate(t, rh) }
func (e ensSource) ReadGas() (ens160.Data, error)  { return e.s.Read() }

type session struct {
	bus     i2c.Bus
	climate ClimateSource
	gas     GasSource
	co2     CO2Source
}

func (s *session) Climate() ClimateSource { return s.climate }
func (s *session) Gas() GasSource         { return s.gas }
func (s *session) CO2() CO2Source         { return s.co2 }
func (s *session) Close() error           { return s.bus.Close() }

type scdSource struct {
	sensor *scd4x.Sensor
	cached scd4x.Measurement
	has    bool
}

func (s *scdSource) Measure() (float64, float64, error) {
	s.has = false
	m, err := s.sensor.Read()
	if err != nil {
		return 0, 0, err
	}
	s.cached, s.has = m, true
	return m.TempC, m.RHPct, nil
}

func (s *scdSource) ReadCO2() (int, error) {
	if s.has {
		s.has = false
		return s.cached.CO2PPM, nil
	}
	m, err := s.sensor.Read()
	if err != nil {
		return 0, err
	}
	return m.CO2PPM, nil
}

type openerOptions struct {
	aht21     bool
	ahtAddr   uint8
	checkCRC  bool
	ens160    bool
	ensAddr   uint8
	scd4x     bool
	scd4xAddr uint8
}

type OpenerOption func(*openerOptions)

func WithAHT21(addr uint8, checkCRC bool) OpenerOption {
	return func(o *openerOptions) {
		o.aht21, o.ahtAddr, o.checkCRC = true, addr, checkCRC
	}
}

func WithENS160(addr uint8) OpenerOption {
	return func(o *openerOptions) { o.ens160, o.ensAddr = true, addr }
}

func WithSCD4x(addr uint8) OpenerOption {
	return func(o *openerOptions) { o.scd4x, o.scd4xAddr = true, addr }
}

var ErrNoSensors = errors.New("no sensors configured")

func NewOpener(open func() (i2c.Bus, error), sleep func(time.Duration), opts ...OpenerOption) OpenFunc {
	var o openerOptions
	for _, opt := range opts {
		opt(&o)
	}
	return func() (Session, error) {
		if !o.aht21 && !o.ens160 && !o.scd4x {
			return nil, ErrNoSensors
		}
		bus, err := open()
		if err != nil {
			return nil, fmt.Errorf("open bus: %w", err)
		}
		sess := &session{bus: bus}
		if o.aht21 {
			aht := aht21.New(bus, o.ahtAddr, o.checkCRC, sleep)
			if err := aht.Init(); err != nil {
				bus.Close()
				return nil, fmt.Errorf("init aht21: %w", err)
			}
			sess.climate = aht
		}
		if o.ens160 {
			ens := ens160.New(bus, o.ensAddr)
			if err := ens.Init(); err != nil {
				bus.Close()
				return nil, fmt.Errorf("init ens160: %w", err)
			}
			sess.gas = ensSource{ens}
		}
		if o.scd4x {
			scd := scd4x.New(bus, o.scd4xAddr, sleep)
			if err := scd.Init(); err != nil {
				bus.Close()
				return nil, fmt.Errorf("init scd4x: %w", err)
			}
			src := &scdSource{sensor: scd}
			sess.co2 = src
			if sess.climate == nil {
				sess.climate = src
			}
		}
		return sess, nil
	}
}

type Snapshot struct {
	Initialized         bool
	ConsecutiveFailures int
	TotalFailures       int
	Reinits             int
	LastError           string
	HasLastError        bool
	LastErrorMono       time.Duration
	HasLastSuccess      bool
	LastSuccessMono     time.Duration
}

type Config struct {
	Interval   time.Duration
	TempOffset float64
}

type Sampler struct {
	open  OpenFunc
	store *store.Store
	cfg   Config
	clock store.Clock
	log   *slog.Logger

	mu                  sync.Mutex
	session             Session
	consecutiveFailures int
	totalFailures       int
	reinits             int
	lastError           string
	hasLastError        bool
	lastErrorMono       time.Duration
	hasLastSuccess      bool
	lastSuccessMono     time.Duration
}

func New(open OpenFunc, st *store.Store, cfg Config, clock store.Clock, log *slog.Logger) *Sampler {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Sampler{open: open, store: st, cfg: cfg, clock: clock, log: log}
}

func (s *Sampler) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{
		Initialized:         s.session != nil,
		ConsecutiveFailures: s.consecutiveFailures,
		TotalFailures:       s.totalFailures,
		Reinits:             s.reinits,
		LastError:           s.lastError,
		HasLastError:        s.hasLastError,
		LastErrorMono:       s.lastErrorMono,
		HasLastSuccess:      s.hasLastSuccess,
		LastSuccessMono:     s.lastSuccessMono,
	}
}

func (s *Sampler) currentSession() Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.session
}

func (s *Sampler) Tick() {
	sess := s.currentSession()
	if sess == nil {
		opened, err := s.open()
		if err != nil {
			s.fail(err, "sensor init failed")
			return
		}
		s.mu.Lock()
		s.session = opened
		s.mu.Unlock()
		sess = opened
		s.log.Info("sensor init ok")
	}

	var rd store.Reading
	var t, rh float64
	climate := sess.Climate()
	if climate != nil {
		rawT, rawRH, err := climate.Measure()
		if err != nil {
			s.failSample(err)
			return
		}
		t = rawT + s.cfg.TempOffset
		rh = clampRH(psychro.RescaleRH(clampRH(rawRH), rawT, t))
		rd.HasClimate = true
		rd.TempC = round(t, 2)
		rd.RHPct = round(rh, 1)
	}

	if gas := sess.Gas(); gas != nil {
		if climate != nil {
			if err := gas.Compensate(t, rh); err != nil {
				s.failSample(err)
				return
			}
		}
		d, err := gas.ReadGas()
		if err != nil {
			s.failSample(err)
			return
		}
		rd.HasGas = true
		rd.AQI = d.AQI
		rd.TVOCPPB = d.TVOCPPB
		rd.ECO2PPM = d.ECO2PPM
		rd.Validity = d.Validity
	}

	if co2 := sess.CO2(); co2 != nil {
		ppm, err := co2.ReadCO2()
		if err != nil {
			s.failSample(err)
			return
		}
		rd.HasCO2 = true
		rd.CO2PPM = ppm
	}

	rd.Wall = s.clock.Now()
	rd.Mono = s.clock.Mono()
	s.store.Add(rd)
	s.success()
}

func (s *Sampler) fail(err error, msg string) int {
	text := err.Error()
	if runes := []rune(text); len(runes) > maxErrorLen {
		text = string(runes[:maxErrorLen])
	}
	s.mu.Lock()
	s.consecutiveFailures++
	s.totalFailures++
	s.lastError = text
	s.hasLastError = true
	s.lastErrorMono = s.clock.Mono()
	n := s.consecutiveFailures
	s.mu.Unlock()
	if n == 1 || n%reinitEvery == 0 {
		s.log.Warn(msg, "err", err, "consecutive", n)
	}
	return n
}

func (s *Sampler) failSample(err error) {
	n := s.fail(err, "sample failed")
	if n%reinitEvery != 0 {
		return
	}
	s.mu.Lock()
	sess := s.session
	s.session = nil
	s.reinits++
	reinits := s.reinits
	s.mu.Unlock()
	if sess != nil {
		sess.Close()
	}
	s.log.Warn("reinit", "reinits", reinits)
}

func (s *Sampler) success() {
	s.mu.Lock()
	prev := s.consecutiveFailures
	s.consecutiveFailures = 0
	s.hasLastSuccess = true
	s.lastSuccessMono = s.clock.Mono()
	s.mu.Unlock()
	if prev > 0 {
		s.log.Info("sample recovered", "after", prev)
	}
}

func (s *Sampler) Run(ctx context.Context) {
	s.Tick()
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			sess := s.session
			s.session = nil
			s.mu.Unlock()
			if sess != nil {
				sess.Close()
			}
			return
		case <-ticker.C:
			s.Tick()
		}
	}
}

func clampRH(rh float64) float64 { return math.Min(math.Max(rh, 0), 100) }

func round(x float64, digits int) float64 {
	p := math.Pow(10, float64(digits))
	return math.Round(x*p) / p
}
