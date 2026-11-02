// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

//go:build linux

package i2c

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

const (
	i2cRdwr = 0x0707
	i2cMRd  = 0x0001
)

type i2cMsg struct {
	addr  uint16
	flags uint16
	len   uint16
	buf   *byte
}

type i2cRdwrIoctlData struct {
	msgs  *i2cMsg
	nmsgs uint32
}

type linuxBus struct {
	mu sync.Mutex
	f  *os.File
}

func Open(busNumber int) (Bus, error) {
	path := fmt.Sprintf("/dev/i2c-%d", busNumber)
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("i2c open: %w", err)
	}
	return &linuxBus{f: f}, nil
}

func (b *linuxBus) transfer(msgs []i2cMsg) error {
	if b.f == nil {
		return errors.New("i2c: bus closed")
	}
	data := i2cRdwrIoctlData{msgs: &msgs[0], nmsgs: uint32(len(msgs))}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, b.f.Fd(), i2cRdwr, uintptr(unsafe.Pointer(&data)))
	runtime.KeepAlive(msgs)
	runtime.KeepAlive(&data)
	if errno != 0 {
		return fmt.Errorf("i2c transfer: %w", errno)
	}
	return nil
}

func (b *linuxBus) Write(addr uint8, data []byte) error {
	if len(data) < 1 {
		return errors.New("i2c: empty write")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.transfer([]i2cMsg{
		{addr: uint16(addr), flags: 0, len: uint16(len(data)), buf: &data[0]},
	})
}

func (b *linuxBus) Read(addr uint8, n int) ([]byte, error) {
	if n < 1 {
		return nil, errors.New("i2c: invalid read length")
	}
	buf := make([]byte, n)
	b.mu.Lock()
	defer b.mu.Unlock()
	err := b.transfer([]i2cMsg{
		{addr: uint16(addr), flags: i2cMRd, len: uint16(n), buf: &buf[0]},
	})
	if err != nil {
		return nil, err
	}
	return buf, nil
}

func (b *linuxBus) WriteRead(addr uint8, w []byte, n int) ([]byte, error) {
	if len(w) < 1 || n < 1 {
		return nil, errors.New("i2c: invalid write-read arguments")
	}
	buf := make([]byte, n)
	b.mu.Lock()
	defer b.mu.Unlock()
	err := b.transfer([]i2cMsg{
		{addr: uint16(addr), flags: 0, len: uint16(len(w)), buf: &w[0]},
		{addr: uint16(addr), flags: i2cMRd, len: uint16(n), buf: &buf[0]},
	})
	if err != nil {
		return nil, err
	}
	return buf, nil
}

func (b *linuxBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.f == nil {
		return nil
	}
	err := b.f.Close()
	b.f = nil
	return err
}
