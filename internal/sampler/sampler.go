// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package sampler

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/furu04/air-sensor-api/internal/aht21"
	"github.com/furu04/air-sensor-api/internal/ens160"
	"github.com/furu04/air-sensor-api/internal/i2c"
	"github.com/furu04/air-sensor-api/internal/store"
)

const (
	reinitEvery = 5
	maxErrorLen = 200
)

type Session interface {
	Measure() (tempC, rhPct float64, err error)
	Compensate(tempC, rhPct float64) error
	ReadGas() (ens160.Data, error)
	Close() error
}

type OpenFunc func() (Session, error)

type session struct {
	bus i2c.Bus
	aht *aht21.Sensor
	ens *ens160.Sensor
}

func (s *session) Measure() (float64, float64, error) { return s.aht.Measure() }
func (s *session) Compensate(t, rh float64) error     { return s.ens.Compensate(t, rh) }
func (s *session) ReadGas() (ens160.Data, error)      { return s.ens.Read() }
func (s *session) Close() error                       { return s.bus.Close() }

func NewOpener(open func() (i2c.Bus, error), ahtAddr, ensAddr uint8, checkCRC bool, sleep func(time.Duration)) OpenFunc {
	return func() (Session, error) {
		bus, err := open()
		if err != nil {
			return nil, fmt.Errorf("open bus: %w", err)
		}
		aht := aht21.New(bus, ahtAddr, checkCRC, sleep)
		if err := aht.Init(); err != nil {
			bus.Close()
			return nil, fmt.Errorf("init aht21: %w", err)
		}
		ens := ens160.New(bus, ensAddr)
		if err := ens.Init(); err != nil {
			bus.Close()
			return nil, fmt.Errorf("init ens160: %w", err)
		}
		return &session{bus: bus, aht: aht, ens: ens}, nil
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

	t, rh, err := sess.Measure()
	if err != nil {
		s.failSample(err)
		return
	}
	t += s.cfg.TempOffset
	rh = math.Min(math.Max(rh, 0), 100)

	if err := sess.Compensate(t, rh); err != nil {
		s.failSample(err)
		return
	}
	d, err := sess.ReadGas()
	if err != nil {
		s.failSample(err)
		return
	}

	s.store.Add(store.Reading{
		Wall:     s.clock.Now(),
		Mono:     s.clock.Mono(),
		TempC:    round(t, 2),
		RHPct:    round(rh, 1),
		AQI:      d.AQI,
		TVOCPPB:  d.TVOCPPB,
		ECO2PPM:  d.ECO2PPM,
		Validity: d.Validity,
	})
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

func round(x float64, digits int) float64 {
	p := math.Pow(10, float64(digits))
	return math.Round(x*p) / p
}
