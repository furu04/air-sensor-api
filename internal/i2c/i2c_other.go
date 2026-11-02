// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

//go:build !linux

package i2c

import "errors"

func Open(busNumber int) (Bus, error) {
	return nil, errors.New("i2c: unsupported platform")
}
