// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package crc8

import "testing"

func TestSum(t *testing.T) {
	tests := []struct {
		in   []byte
		want byte
	}{
		{nil, 0xFF},
		{[]byte{0xBE, 0xEF}, 0x92},
		{[]byte{0x01, 0x90}, 0x4C},
		{[]byte{0x66, 0x66}, 0x93},
	}
	for _, tt := range tests {
		if got := Sum(tt.in); got != tt.want {
			t.Errorf("Sum(% X) = 0x%02X want 0x%02X", tt.in, got, tt.want)
		}
	}
}
