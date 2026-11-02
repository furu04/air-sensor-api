// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package i2ctest

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
)

const (
	ahtAddr = 0x38
	ensAddr = 0x53
)

type WriteCall struct {
	Addr uint8
	Data []byte
}

type ReadCall struct {
	Addr uint8
	W    []byte
	N    int
}

type Sim struct {
	mu sync.Mutex

	unplugged bool

	ahtCalibrated bool
	ahtInitWorks  bool
	ahtStatus     byte
	ahtStatusSet  bool
	busy          int
	rawH          uint32
	rawT          uint32

	partID      uint16
	opMode      byte
	tempIn      uint16
	rhIn        uint16
	aqi         int
	tvoc        int
	eco2        int
	validity    int
	statusError bool

	writes    []WriteCall
	reads     []ReadCall
	closeCall int
}

func New() *Sim {
	s := &Sim{
		ahtCalibrated: true,
		ahtInitWorks:  true,
		partID:        0x0160,
	}
	s.SetClimate(25.0, 50.0)
	s.SetGas(1, 0, 400, 0)
	return s
}

func (s *Sim) lock() func() {
	s.mu.Lock()
	return s.mu.Unlock
}

func (s *Sim) SetUnplugged(v bool) {
	defer s.lock()()
	s.unplugged = v
}

func (s *Sim) SetAHTCalibrated(v bool) {
	defer s.lock()()
	s.ahtCalibrated = v
}

func (s *Sim) SetAHTInitWorks(v bool) {
	defer s.lock()()
	s.ahtInitWorks = v
}

func (s *Sim) SetAHTStatus(b byte) {
	defer s.lock()()
	s.ahtStatus = b
	s.ahtStatusSet = true
}

func (s *Sim) SetBusy(n int) {
	defer s.lock()()
	s.busy = n
}

func (s *Sim) SetClimate(tempC, rhPct float64) {
	defer s.lock()()
	s.rawT = uint32((tempC + 50) / 200 * 1048576)
	s.rawH = uint32(rhPct / 100 * 1048576)
}

func (s *Sim) SetPartID(id uint16) {
	defer s.lock()()
	s.partID = id
}

func (s *Sim) SetGas(aqi, tvoc, eco2, validity int) {
	defer s.lock()()
	s.aqi = aqi
	s.tvoc = tvoc
	s.eco2 = eco2
	s.validity = validity
}

func (s *Sim) SetStatusError(v bool) {
	defer s.lock()()
	s.statusError = v
}

func (s *Sim) Writes() []WriteCall {
	defer s.lock()()
	out := make([]WriteCall, len(s.writes))
	for i, w := range s.writes {
		out[i] = WriteCall{Addr: w.Addr, Data: append([]byte(nil), w.Data...)}
	}
	return out
}

func (s *Sim) Reads() []ReadCall {
	defer s.lock()()
	out := make([]ReadCall, len(s.reads))
	for i, r := range s.reads {
		out[i] = ReadCall{Addr: r.Addr, W: append([]byte(nil), r.W...), N: r.N}
	}
	return out
}

func (s *Sim) ResetLog() {
	defer s.lock()()
	s.writes = nil
	s.reads = nil
}

func (s *Sim) CloseCount() int {
	defer s.lock()()
	return s.closeCall
}

func (s *Sim) OpMode() byte {
	defer s.lock()()
	return s.opMode
}

func (s *Sim) Compensation() (tempIn, rhIn uint16) {
	defer s.lock()()
	return s.tempIn, s.rhIn
}

func (s *Sim) nack() error {
	return fmt.Errorf("i2c transfer: %w", syscall.EREMOTEIO)
}

func (s *Sim) Write(addr uint8, data []byte) error {
	defer s.lock()()
	if s.unplugged {
		return s.nack()
	}
	if len(data) < 1 {
		return errors.New("i2ctest: empty write")
	}
	if addr != ahtAddr && addr != ensAddr {
		return s.nack()
	}
	s.writes = append(s.writes, WriteCall{Addr: addr, Data: append([]byte(nil), data...)})
	switch addr {
	case ahtAddr:
		if len(data) == 3 && data[0] == 0xBE && data[1] == 0x08 && data[2] == 0x00 && s.ahtInitWorks {
			s.ahtCalibrated = true
		}
	case ensAddr:
		switch {
		case data[0] == 0x10 && len(data) == 2:
			s.opMode = data[1]
		case data[0] == 0x13 && len(data) == 3:
			s.tempIn = uint16(data[1]) | uint16(data[2])<<8
		case data[0] == 0x15 && len(data) == 3:
			s.rhIn = uint16(data[1]) | uint16(data[2])<<8
		}
	}
	return nil
}

func (s *Sim) Read(addr uint8, n int) ([]byte, error) {
	defer s.lock()()
	if s.unplugged {
		return nil, s.nack()
	}
	if n < 1 {
		return nil, errors.New("i2ctest: invalid read length")
	}
	if addr != ahtAddr {
		return nil, s.nack()
	}
	s.reads = append(s.reads, ReadCall{Addr: addr, N: n})
	frame := make([]byte, 7)
	status := byte(0x1C)
	if s.busy > 0 {
		s.busy--
		status = 0x9C
	}
	frame[0] = status
	frame[1] = byte(s.rawH >> 12)
	frame[2] = byte(s.rawH >> 4)
	frame[3] = byte(s.rawH&0x0F)<<4 | byte(s.rawT>>16)
	frame[4] = byte(s.rawT >> 8)
	frame[5] = byte(s.rawT)
	frame[6] = crc8(frame[:6])
	if n > len(frame) {
		n = len(frame)
	}
	return frame[:n], nil
}

func (s *Sim) WriteRead(addr uint8, w []byte, n int) ([]byte, error) {
	defer s.lock()()
	if s.unplugged {
		return nil, s.nack()
	}
	if len(w) < 1 || n < 1 {
		return nil, errors.New("i2ctest: invalid write-read arguments")
	}
	if addr != ahtAddr && addr != ensAddr {
		return nil, s.nack()
	}
	s.reads = append(s.reads, ReadCall{Addr: addr, W: append([]byte(nil), w...), N: n})
	switch addr {
	case ahtAddr:
		if w[0] != 0x71 {
			return nil, errors.New("i2ctest: unsupported aht21 register")
		}
		var st byte
		switch {
		case s.ahtStatusSet:
			st = s.ahtStatus
		case s.ahtCalibrated:
			st = 0x18
		}
		return pad([]byte{st}, n), nil
	default:
		switch w[0] {
		case 0x00:
			return pad([]byte{byte(s.partID), byte(s.partID >> 8)}, n), nil
		case 0x20:
			status := byte(0x80) | byte(s.validity&0x03)<<2 | 0x02
			if s.statusError {
				status |= 0x40
			}
			return pad([]byte{
				status,
				byte(s.aqi),
				byte(s.tvoc), byte(s.tvoc >> 8),
				byte(s.eco2), byte(s.eco2 >> 8),
			}, n), nil
		}
		return nil, errors.New("i2ctest: unsupported ens160 register")
	}
}

func (s *Sim) Close() error {
	defer s.lock()()
	s.closeCall++
	return nil
}

func pad(b []byte, n int) []byte {
	out := make([]byte, n)
	copy(out, b)
	return out
}

func crc8(data []byte) byte {
	crc := byte(0xFF)
	for _, b := range data {
		crc ^= b
		for i := 0; i < 8; i++ {
			if crc&0x80 != 0 {
				crc = crc<<1 ^ 0x31
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
