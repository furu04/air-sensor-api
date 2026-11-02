// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package ens160

import (
	"bytes"
	"errors"
	"testing"

	"github.com/furu04/air-sensor-api/internal/i2c/i2ctest"
)

type readBus struct {
	resp  []byte
	calls [][]byte
	ns    []int
}

func (b *readBus) Write(addr uint8, data []byte) error    { return nil }
func (b *readBus) Read(addr uint8, n int) ([]byte, error) { return nil, errors.New("unused") }
func (b *readBus) Close() error                           { return nil }
func (b *readBus) WriteRead(addr uint8, w []byte, n int) ([]byte, error) {
	b.calls = append(b.calls, append([]byte(nil), w...))
	b.ns = append(b.ns, n)
	return b.resp, nil
}

func TestInit(t *testing.T) {
	sim := i2ctest.New()
	if err := New(sim, 0x53).Init(); err != nil {
		t.Fatal(err)
	}
	w := sim.Writes()
	if len(w) != 2 || !bytes.Equal(w[0].Data, []byte{0x10, 0x01}) || !bytes.Equal(w[1].Data, []byte{0x10, 0x02}) {
		t.Fatalf("writes=%v", w)
	}
}

func TestInitWrongPartID(t *testing.T) {
	sim := i2ctest.New()
	sim.SetPartID(0x0161)
	err := New(sim, 0x53).Init()
	if !errors.Is(err, ErrPartID) {
		t.Fatalf("err=%v", err)
	}
	if len(sim.Writes()) != 0 {
		t.Fatalf("writes=%v", sim.Writes())
	}
}

func TestCompensate(t *testing.T) {
	tests := []struct {
		t, rh float64
		w     [][]byte
	}{
		{25.0, 50.0, [][]byte{{0x13, 0x8A, 0x4A}, {0x15, 0x00, 0x64}}},
		{24.31, 45.2, [][]byte{{0x13, 0x5D, 0x4A}, {0x15, 0x66, 0x5A}}},
		{-10.0, 0.0, [][]byte{{0x13, 0xCA, 0x41}, {0x15, 0x00, 0x00}}},
	}
	for _, tt := range tests {
		sim := i2ctest.New()
		if err := New(sim, 0x53).Compensate(tt.t, tt.rh); err != nil {
			t.Fatal(err)
		}
		w := sim.Writes()
		if len(w) != 2 {
			t.Fatalf("writes=%v", w)
		}
		for i := range tt.w {
			if !bytes.Equal(w[i].Data, tt.w[i]) {
				t.Errorf("(%v,%v) write %d = % X want % X", tt.t, tt.rh, i, w[i].Data, tt.w[i])
			}
		}
	}
}

func TestCompensateOutOfRange(t *testing.T) {
	tests := []struct{ t, rh float64 }{{900, 50}, {25, 200}, {25, -1}}
	for _, tt := range tests {
		sim := i2ctest.New()
		err := New(sim, 0x53).Compensate(tt.t, tt.rh)
		if !errors.Is(err, ErrCompensationRange) {
			t.Fatalf("(%v,%v) err=%v", tt.t, tt.rh, err)
		}
		if len(sim.Writes()) != 0 {
			t.Fatalf("writes=%v", sim.Writes())
		}
	}
}

func TestRead(t *testing.T) {
	tests := []struct {
		raw  []byte
		want Data
	}{
		{[]byte{0x82, 0x03, 0x2C, 0x01, 0x90, 0x01}, Data{AQI: 3, TVOCPPB: 300, ECO2PPM: 400, Validity: 0}},
		{[]byte{0x86, 0x01, 0x00, 0x00, 0x90, 0x01}, Data{AQI: 1, TVOCPPB: 0, ECO2PPM: 400, Validity: 1}},
		{[]byte{0x8A, 0x01, 0x0C, 0x00, 0xC2, 0x01}, Data{AQI: 1, TVOCPPB: 12, ECO2PPM: 450, Validity: 2}},
		{[]byte{0x8E, 0x01, 0x00, 0x00, 0x90, 0x01}, Data{AQI: 1, TVOCPPB: 0, ECO2PPM: 400, Validity: 3}},
	}
	for _, tt := range tests {
		got, err := New(&readBus{resp: tt.raw}, 0x53).Read()
		if err != nil {
			t.Fatalf("% X: %v", tt.raw, err)
		}
		if got != tt.want {
			t.Errorf("% X: got %+v want %+v", tt.raw, got, tt.want)
		}
	}
}

func TestReadStatusError(t *testing.T) {
	_, err := New(&readBus{resp: []byte{0xC2, 0x01, 0x00, 0x00, 0x90, 0x01}}, 0x53).Read()
	if !errors.Is(err, ErrStatusError) {
		t.Fatalf("err=%v", err)
	}
}

func TestReadCall(t *testing.T) {
	sim := i2ctest.New()
	if _, err := New(sim, 0x53).Read(); err != nil {
		t.Fatal(err)
	}
	r := sim.Reads()
	if len(r) != 1 || !bytes.Equal(r[0].W, []byte{0x20}) || r[0].N != 6 {
		t.Fatalf("reads=%+v", r)
	}
}
