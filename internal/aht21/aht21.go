// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package aht21

import (
	"errors"
	"fmt"
	"time"

	"github.com/furu04/air-sensor-api/internal/crc8"
	"github.com/furu04/air-sensor-api/internal/i2c"
)

const DefaultAddr = 0x38

var (
	ErrNotCalibrated = errors.New("not calibrated")
	ErrBusy          = errors.New("measurement not finished")
	ErrCRC           = errors.New("crc mismatch")
)

type Sensor struct {
	bus      i2c.Bus
	addr     uint8
	checkCRC bool
	sleep    func(time.Duration)
}

func New(bus i2c.Bus, addr uint8, checkCRC bool, sleep func(time.Duration)) *Sensor {
	if sleep == nil {
		sleep = time.Sleep
	}
	return &Sensor{bus: bus, addr: addr, checkCRC: checkCRC, sleep: sleep}
}

func (s *Sensor) readStatus() (byte, error) {
	b, err := s.bus.WriteRead(s.addr, []byte{0x71}, 1)
	if err != nil {
		return 0, fmt.Errorf("aht21: %w", err)
	}
	if len(b) != 1 {
		return 0, fmt.Errorf("aht21: short status read (%d bytes)", len(b))
	}
	return b[0], nil
}

func (s *Sensor) Init() error {
	s.sleep(100 * time.Millisecond)
	status, err := s.readStatus()
	if err != nil {
		return err
	}
	if status&0x18 != 0x18 {
		if err := s.bus.Write(s.addr, []byte{0xBE, 0x08, 0x00}); err != nil {
			return fmt.Errorf("aht21: %w", err)
		}
		s.sleep(10 * time.Millisecond)
		status, err = s.readStatus()
		if err != nil {
			return err
		}
		if status&0x18 != 0x18 {
			return fmt.Errorf("aht21: status=0x%02X: %w", status, ErrNotCalibrated)
		}
	}
	s.sleep(10 * time.Millisecond)
	return nil
}

func (s *Sensor) Measure() (tempC, rhPct float64, err error) {
	if err := s.bus.Write(s.addr, []byte{0xAC, 0x33, 0x00}); err != nil {
		return 0, 0, fmt.Errorf("aht21: %w", err)
	}
	s.sleep(80 * time.Millisecond)

	const attempts = 5
	var frame []byte
	ready := false
	for i := 0; i < attempts; i++ {
		frame, err = s.bus.Read(s.addr, 7)
		if err != nil {
			return 0, 0, fmt.Errorf("aht21: %w", err)
		}
		if len(frame) != 7 {
			return 0, 0, fmt.Errorf("aht21: short read (%d bytes)", len(frame))
		}
		if frame[0]&0x80 == 0 {
			ready = true
			break
		}
		if i < attempts-1 {
			s.sleep(20 * time.Millisecond)
		}
	}
	if !ready {
		return 0, 0, fmt.Errorf("aht21: %w", ErrBusy)
	}
	if s.checkCRC && CRC8(frame[:6]) != frame[6] {
		return 0, 0, fmt.Errorf("aht21: %w", ErrCRC)
	}

	rawH := uint32(frame[1])<<12 | uint32(frame[2])<<4 | uint32(frame[3])>>4
	rawT := uint32(frame[3]&0x0F)<<16 | uint32(frame[4])<<8 | uint32(frame[5])
	rhPct = float64(rawH) / 1048576 * 100
	tempC = float64(rawT)/1048576*200 - 50
	return tempC, rhPct, nil
}

func CRC8(data []byte) byte { return crc8.Sum(data) }
