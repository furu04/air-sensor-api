// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package ens160

import (
	"errors"
	"fmt"
	"math"

	"github.com/furu04/air-sensor-api/internal/i2c"
)

const DefaultAddr = 0x53
const PartID = 0x0160

type Data struct {
	AQI      int
	TVOCPPB  int
	ECO2PPM  int
	Validity int
}

var (
	ErrPartID            = errors.New("unexpected part id")
	ErrStatusError       = errors.New("device status error")
	ErrCompensationRange = errors.New("compensation value out of range")
)

type Sensor struct {
	bus  i2c.Bus
	addr uint8
}

func New(bus i2c.Bus, addr uint8) *Sensor {
	return &Sensor{bus: bus, addr: addr}
}

func (s *Sensor) Init() error {
	b, err := s.bus.WriteRead(s.addr, []byte{0x00}, 2)
	if err != nil {
		return fmt.Errorf("ens160: %w", err)
	}
	if len(b) != 2 {
		return fmt.Errorf("ens160: short part id read (%d bytes)", len(b))
	}
	partID := int(b[0]) | int(b[1])<<8
	if partID != PartID {
		return fmt.Errorf("ens160: part id 0x%04X (want 0x0160): %w", partID, ErrPartID)
	}
	if err := s.bus.Write(s.addr, []byte{0x10, 0x01}); err != nil {
		return fmt.Errorf("ens160: %w", err)
	}
	if err := s.bus.Write(s.addr, []byte{0x10, 0x02}); err != nil {
		return fmt.Errorf("ens160: %w", err)
	}
	return nil
}

func (s *Sensor) Compensate(tempC, rhPct float64) error {
	tempIn := math.Round((tempC + 273.15) * 64)
	rhIn := math.Round(rhPct * 512)
	if math.IsNaN(tempIn) || math.IsNaN(rhIn) || tempIn < 0 || tempIn > 65535 || rhIn < 0 || rhIn > 65535 {
		return fmt.Errorf("ens160: temp=%.1f rh=%.1f: %w", tempC, rhPct, ErrCompensationRange)
	}
	t := uint16(tempIn)
	h := uint16(rhIn)
	if err := s.bus.Write(s.addr, []byte{0x13, byte(t), byte(t >> 8)}); err != nil {
		return fmt.Errorf("ens160: %w", err)
	}
	if err := s.bus.Write(s.addr, []byte{0x15, byte(h), byte(h >> 8)}); err != nil {
		return fmt.Errorf("ens160: %w", err)
	}
	return nil
}

func (s *Sensor) Read() (Data, error) {
	b, err := s.bus.WriteRead(s.addr, []byte{0x20}, 6)
	if err != nil {
		return Data{}, fmt.Errorf("ens160: %w", err)
	}
	if len(b) != 6 {
		return Data{}, fmt.Errorf("ens160: short read (%d bytes)", len(b))
	}
	status := b[0]
	if status&0x40 != 0 {
		return Data{}, fmt.Errorf("ens160: status 0x%02X: %w", status, ErrStatusError)
	}
	return Data{
		Validity: int(status>>2) & 0x03,
		AQI:      int(b[1] & 0x07),
		TVOCPPB:  int(b[2]) | int(b[3])<<8,
		ECO2PPM:  int(b[4]) | int(b[5])<<8,
	}, nil
}
