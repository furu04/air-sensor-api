// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package i2c

type Bus interface {
	Write(addr uint8, data []byte) error
	Read(addr uint8, n int) ([]byte, error)
	WriteRead(addr uint8, w []byte, n int) ([]byte, error)
	Close() error
}
