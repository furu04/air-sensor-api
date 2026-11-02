// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package store

import (
	"testing"
	"time"
)

type fakeClock struct {
	now  time.Time
	mono time.Duration
}

func (c *fakeClock) Now() time.Time      { return c.now }
func (c *fakeClock) Mono() time.Duration { return c.mono }

var epoch = time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)

func reading(i int) Reading {
	return Reading{
		Wall:    epoch.Add(time.Duration(i) * time.Minute),
		Mono:    time.Duration(i) * time.Minute,
		TempC:   float64(i),
		AQI:     1,
		TVOCPPB: i,
	}
}

func TestRingOverwrite(t *testing.T) {
	clk := &fakeClock{now: epoch, mono: 10 * time.Minute}
	s := New(3, clk)
	for i := 1; i <= 5; i++ {
		s.Add(reading(i))
	}
	if s.Len() != 3 {
		t.Fatalf("len=%d", s.Len())
	}
	if s.Capacity() != 3 {
		t.Fatalf("cap=%d", s.Capacity())
	}
	l, ok := s.Latest()
	if !ok || l.TVOCPPB != 5 {
		t.Fatalf("latest=%+v ok=%v", l, ok)
	}
	got := s.Query(Query{Limit: 10})
	if len(got) != 3 || got[0].TVOCPPB != 3 || got[1].TVOCPPB != 4 || got[2].TVOCPPB != 5 {
		t.Fatalf("got=%+v", got)
	}
}

func TestLatestEmpty(t *testing.T) {
	s := New(3, &fakeClock{})
	if _, ok := s.Latest(); ok {
		t.Fatal("expected false")
	}
}

func TestQuery(t *testing.T) {
	clk := &fakeClock{now: epoch.Add(10 * time.Minute), mono: 10 * time.Minute}
	s := New(20, clk)
	for i := 1; i <= 10; i++ {
		r := reading(i)
		if i%2 == 0 {
			r.Validity = 1
		}
		s.Add(r)
	}

	tests := []struct {
		name string
		q    Query
		want []int
	}{
		{"window inclusive", Query{Window: 3 * time.Minute, Limit: 100}, []int{7, 8, 9, 10}},
		{"since", Query{Since: epoch.Add(8 * time.Minute), Limit: 100}, []int{8, 9, 10}},
		{"valid only then limit", Query{ValidOnly: true, Limit: 2}, []int{7, 9}},
		{"limit newest side oldest first", Query{Limit: 3}, []int{8, 9, 10}},
		{"none", Query{Window: time.Second, Since: time.Time{}, Limit: 5, ValidOnly: true}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.Query(tt.q)
			if got == nil {
				t.Fatal("nil slice")
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d want %d: %+v", len(got), len(tt.want), got)
			}
			for i, w := range tt.want {
				if got[i].TVOCPPB != w {
					t.Fatalf("idx %d: got %d want %d", i, got[i].TVOCPPB, w)
				}
			}
		})
	}
}

func TestAge(t *testing.T) {
	clk := &fakeClock{now: epoch, mono: 5 * time.Second}
	s := New(2, clk)
	r := Reading{Mono: 5 * time.Second}
	if s.Age(r) != 0 {
		t.Fatal("age should be 0")
	}
	clk.mono = 20 * time.Second
	if s.Age(r) != 15*time.Second {
		t.Fatalf("age=%v", s.Age(r))
	}
	clk.now = clk.now.Add(-24 * time.Hour)
	if s.Age(r) != 15*time.Second {
		t.Fatalf("age changed after wall rewind: %v", s.Age(r))
	}
	clk.mono = time.Second
	if s.Age(r) != 0 {
		t.Fatalf("negative age not clamped: %v", s.Age(r))
	}
}

func TestValidityName(t *testing.T) {
	want := []string{"ok", "warmup", "initial_startup", "invalid"}
	for i, w := range want {
		if got := (Reading{Validity: i}).ValidityName(); got != w {
			t.Fatalf("%d: %s", i, got)
		}
	}
	if !(Reading{}).Valid() || (Reading{Validity: 1}).Valid() {
		t.Fatal("Valid")
	}
}

func TestSystemClock(t *testing.T) {
	c := NewSystemClock()
	a := c.Mono()
	time.Sleep(2 * time.Millisecond)
	if c.Mono() <= a {
		t.Fatal("mono not advancing")
	}
	if c.Now().IsZero() {
		t.Fatal("zero now")
	}
}
