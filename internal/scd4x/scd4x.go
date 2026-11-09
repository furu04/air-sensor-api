// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package scd4x

import (
	"errors"
	"fmt"
	"time"

	"github.com/furu04/air-sensor-api/internal/crc8"
	"github.com/furu04/air-sensor-api/internal/i2c"
)

const DefaultAddr = 0x62

const (
	cmdStopPeriodic  = 0x3F86
	cmdSerialNumber  = 0x3682
	cmdStartPeriodic = 0x21B1
	cmdDataReady     = 0xE4B8
	cmdReadMeasure   = 0xEC05

	stopWait     = 500 * time.Millisecond
	commandWait  = 2 * time.Millisecond
	pollInterval = 250 * time.Millisecond
	pollAttempts = 24
	maxCO2PPM    = 40000
)

var (
	ErrCRC        = errors.New("crc mismatch")
	ErrNotReady   = errors.New("measurement not ready")
	ErrOutOfRange = errors.New("co2 out of range")
)

type Sensor struct {
	bus   i2c.Bus
	addr  uint8
	sleep func(time.Duration)
}

func New(bus i2c.Bus, addr uint8, sleep func(time.Duration)) *Sensor {
	if sleep == nil {
		sleep = time.Sleep
	}
	return &Sensor{bus: bus, addr: addr, sleep: sleep}
}

func (s *Sensor) command(cmd uint16) error {
	if err := s.bus.Write(s.addr, []byte{byte(cmd >> 8), byte(cmd)}); err != nil {
		return fmt.Errorf("scd4x: %w", err)
	}
	return nil
}

func (s *Sensor) query(cmd uint16, words int) ([]uint16, error) {
	if err := s.command(cmd); err != nil {
		return nil, err
	}
	s.sleep(commandWait)
	n := words * 3
	b, err := s.bus.Read(s.addr, n)
	if err != nil {
		return nil, fmt.Errorf("scd4x: %w", err)
	}
	if len(b) != n {
		return nil, fmt.Errorf("scd4x: short read (%d bytes, want %d)", len(b), n)
	}
	out := make([]uint16, words)
	for i := range out {
		chunk := b[i*3 : i*3+3]
		if crc8.Sum(chunk[:2]) != chunk[2] {
			return nil, fmt.Errorf("scd4x: %w", ErrCRC)
		}
		out[i] = uint16(chunk[0])<<8 | uint16(chunk[1])
	}
	return out, nil
}

func (s *Sensor) Init() error {
	if err := s.command(cmdStopPeriodic); err != nil {
		return err
	}
	s.sleep(stopWait)
	if _, err := s.query(cmdSerialNumber, 3); err != nil {
		return err
	}
	return s.command(cmdStartPeriodic)
}

func (s *Sensor) dataReady() (bool, error) {
	w, err := s.query(cmdDataReady, 1)
	if err != nil {
		return false, err
	}
	return w[0]&0x07FF != 0, nil
}

func (s *Sensor) ReadCO2() (int, error) {
	ready := false
	for i := 0; i < pollAttempts; i++ {
		ok, err := s.dataReady()
		if err != nil {
			return 0, err
		}
		if ok {
			ready = true
			break
		}
		if i < pollAttempts-1 {
			s.sleep(pollInterval)
		}
	}
	if !ready {
		return 0, fmt.Errorf("scd4x: %w", ErrNotReady)
	}
	w, err := s.query(cmdReadMeasure, 3)
	if err != nil {
		return 0, err
	}
	co2 := int(w[0])
	if co2 > maxCO2PPM {
		return 0, fmt.Errorf("scd4x: %d ppm: %w", co2, ErrOutOfRange)
	}
	return co2, nil
}
