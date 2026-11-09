// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package store

import (
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
	Mono() time.Duration
}

type systemClock struct {
	start time.Time
}

func NewSystemClock() Clock {
	return &systemClock{start: time.Now()}
}

func (c *systemClock) Now() time.Time { return time.Now() }

func (c *systemClock) Mono() time.Duration { return time.Since(c.start) }

type Reading struct {
	Wall       time.Time
	Mono       time.Duration
	HasClimate bool
	TempC      float64
	RHPct      float64
	HasGas     bool
	AQI        int
	TVOCPPB    int
	ECO2PPM    int
	HasCO2     bool
	CO2PPM     int
	Validity   int
}

func (r Reading) Valid() bool { return !r.HasGas || r.Validity == 0 }

func (r Reading) ValidityName() string {
	switch r.Validity {
	case 0:
		return "ok"
	case 1:
		return "warmup"
	case 2:
		return "initial_startup"
	default:
		return "invalid"
	}
}

type Query struct {
	Window    time.Duration
	Since     time.Time
	Limit     int
	ValidOnly bool
}

type Store struct {
	mu    sync.RWMutex
	buf   []Reading
	start int
	count int
	clock Clock
}

func New(capacity int, clock Clock) *Store {
	if capacity < 1 {
		capacity = 1
	}
	return &Store{buf: make([]Reading, capacity), clock: clock}
}

func (s *Store) Add(r Reading) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.count < len(s.buf) {
		s.buf[(s.start+s.count)%len(s.buf)] = r
		s.count++
		return
	}
	s.buf[s.start] = r
	s.start = (s.start + 1) % len(s.buf)
}

func (s *Store) Latest() (Reading, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.count == 0 {
		return Reading{}, false
	}
	return s.buf[(s.start+s.count-1)%len(s.buf)], true
}

func (s *Store) Query(q Query) []Reading {
	nowMono := s.clock.Mono()
	s.mu.RLock()
	matched := make([]Reading, 0, min(s.count, max(q.Limit, 0)))
	for i := 0; i < s.count; i++ {
		r := s.buf[(s.start+i)%len(s.buf)]
		if q.Window > 0 && ageOf(nowMono, r) > q.Window {
			continue
		}
		if !q.Since.IsZero() && r.Wall.Before(q.Since) {
			continue
		}
		if q.ValidOnly && !r.Valid() {
			continue
		}
		matched = append(matched, r)
	}
	s.mu.RUnlock()
	if q.Limit > 0 && len(matched) > q.Limit {
		matched = append([]Reading(nil), matched[len(matched)-q.Limit:]...)
	}
	return matched
}

func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.count
}

func (s *Store) Capacity() int { return len(s.buf) }

func (s *Store) Age(r Reading) time.Duration {
	return ageOf(s.clock.Mono(), r)
}

func ageOf(nowMono time.Duration, r Reading) time.Duration {
	d := nowMono - r.Mono
	if d < 0 {
		return 0
	}
	return d
}
