// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package psychro

import (
	"math"
	"testing"
)

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestSaturationVaporPressure(t *testing.T) {
	tests := []struct{ temp, want float64 }{
		{0, 6.112},
		{20, 23.326},
		{25, 31.601},
		{28.31, 38.398},
	}
	for _, tt := range tests {
		if got := SaturationVaporPressure(tt.temp); !near(got, tt.want, 0.01) {
			t.Errorf("es(%v) = %v want %v", tt.temp, got, tt.want)
		}
	}
}

func TestRescaleRH(t *testing.T) {
	tests := []struct{ rh, from, to, want float64 }{
		{34, 28.31, 23, 46.58},
		{38, 27, 24, 45.41},
		{50, 20, 25, 36.9},
		{50, 25, 20, 67.7},
	}
	for _, tt := range tests {
		if got := RescaleRH(tt.rh, tt.from, tt.to); !near(got, tt.want, 0.1) {
			t.Errorf("RescaleRH(%v, %v, %v) = %v want %v", tt.rh, tt.from, tt.to, got, tt.want)
		}
	}
}

func TestRescaleRHIdentityIsExact(t *testing.T) {
	for _, rh := range []float64{0, 33.3, 100} {
		if got := RescaleRH(rh, 24.7, 24.7); got != rh {
			t.Errorf("identity changed %v to %v", rh, got)
		}
	}
}

func TestRescaleRHRoundTrip(t *testing.T) {
	got := RescaleRH(RescaleRH(41.5, 27, 22), 22, 27)
	if !near(got, 41.5, 1e-9) {
		t.Fatalf("round trip = %v", got)
	}
}

func TestRescaleRHDirection(t *testing.T) {
	if RescaleRH(40, 28, 23) <= 40 {
		t.Error("cooling must raise RH")
	}
	if RescaleRH(40, 23, 28) >= 40 {
		t.Error("warming must lower RH")
	}
}

func TestRescaleRHZeroStaysZero(t *testing.T) {
	if got := RescaleRH(0, 30, 10); got != 0 {
		t.Fatalf("got %v", got)
	}
}
