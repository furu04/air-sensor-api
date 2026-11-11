// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package config

import (
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/furu04/air-sensor-api/internal/scd4x"
)

type Config struct {
	ListenAddr   string
	I2CBus       int
	AHT21Bus     int
	ENS160Bus    int
	SCD4xBus     int
	AHT21Enable  bool
	ENS160Enable bool
	SCD4xEnable  bool
	ENS160Addr   uint8
	AHT21Addr    uint8
	Interval     time.Duration
	TempOffset   float64
	HistorySize  int
	StaleAfter   time.Duration
	NoCRC        bool
	APIToken     string
}

func Load(getenv func(string) string) (Config, error) {
	return LoadWith(getenv, os.ReadFile)
}

func LoadWith(getenv func(string) string, readFile func(string) ([]byte, error)) (Config, error) {
	var fileVals map[string]string
	if path := getenv("CONFIG_FILE"); path != "" {
		data, err := readFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("CONFIG_FILE: %v", err)
		}
		fileVals, err = parseFile(data)
		if err != nil {
			return Config{}, fmt.Errorf("CONFIG_FILE: %s: %v", path, err)
		}
	}
	get := func(key string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return fileVals[key]
	}

	cfg := Config{
		ListenAddr:   "127.0.0.1:8080",
		I2CBus:       1,
		AHT21Enable:  true,
		ENS160Enable: true,
		ENS160Addr:   0x53,
		AHT21Addr:    0x38,
		Interval:     60 * time.Second,
		HistorySize:  1440,
	}

	if v := get("LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}

	parseBus := func(key string, def int) (int, error) {
		v := get(key)
		if v == "" {
			return def, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("%s: %v", key, err)
		}
		if n < 0 || n > 255 {
			return 0, fmt.Errorf("%s: %d out of range 0-255", key, n)
		}
		return n, nil
	}
	var err error
	if cfg.I2CBus, err = parseBus("I2C_BUS", cfg.I2CBus); err != nil {
		return Config{}, err
	}
	for _, e := range []struct {
		key string
		dst *int
	}{
		{"AHT21_BUS", &cfg.AHT21Bus},
		{"ENS160_BUS", &cfg.ENS160Bus},
		{"SCD4X_BUS", &cfg.SCD4xBus},
	} {
		if *e.dst, err = parseBus(e.key, cfg.I2CBus); err != nil {
			return Config{}, err
		}
	}

	for _, e := range []struct {
		key string
		dst *bool
	}{
		{"AHT21_ENABLE", &cfg.AHT21Enable},
		{"ENS160_ENABLE", &cfg.ENS160Enable},
		{"SCD4X_ENABLE", &cfg.SCD4xEnable},
	} {
		if v := get(e.key); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return Config{}, fmt.Errorf("%s: %v", e.key, err)
			}
			*e.dst = b
		}
	}
	if !cfg.AHT21Enable && !cfg.ENS160Enable && !cfg.SCD4xEnable {
		return Config{}, fmt.Errorf("sensors: at least one sensor must be enabled")
	}

	if v := get("ENS160_ADDR"); v != "" {
		n, err := strconv.ParseUint(v, 0, 8)
		if err != nil {
			return Config{}, fmt.Errorf("ENS160_ADDR: %v", err)
		}
		if n != 0x52 && n != 0x53 {
			return Config{}, fmt.Errorf("ENS160_ADDR: 0x%02X must be 0x52 or 0x53", n)
		}
		cfg.ENS160Addr = uint8(n)
	}

	if v := get("AHT21_ADDR"); v != "" {
		n, err := strconv.ParseUint(v, 0, 8)
		if err != nil {
			return Config{}, fmt.Errorf("AHT21_ADDR: %v", err)
		}
		if n < 0x08 || n > 0x77 {
			return Config{}, fmt.Errorf("AHT21_ADDR: 0x%02X out of range 0x08-0x77", n)
		}
		cfg.AHT21Addr = uint8(n)
	}
	if cfg.AHT21Enable && cfg.ENS160Enable && cfg.AHT21Bus == cfg.ENS160Bus && cfg.AHT21Addr == cfg.ENS160Addr {
		return Config{}, fmt.Errorf("AHT21_ADDR: 0x%02X conflicts with ENS160_ADDR", cfg.AHT21Addr)
	}
	if cfg.AHT21Enable && cfg.SCD4xEnable && cfg.AHT21Bus == cfg.SCD4xBus && cfg.AHT21Addr == scd4x.DefaultAddr {
		return Config{}, fmt.Errorf("AHT21_ADDR: 0x%02X conflicts with SCD4X (0x%02X)", cfg.AHT21Addr, scd4x.DefaultAddr)
	}

	if v := get("INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("INTERVAL: %v", err)
		}
		if d%time.Second != 0 {
			return Config{}, fmt.Errorf("INTERVAL: %s must be a whole number of seconds", v)
		}
		if d < 5*time.Second || d > time.Hour {
			return Config{}, fmt.Errorf("INTERVAL: %s out of range 5s-1h", v)
		}
		cfg.Interval = d
	}

	if v := get("TEMP_OFFSET"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return Config{}, fmt.Errorf("TEMP_OFFSET: %v", err)
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return Config{}, fmt.Errorf("TEMP_OFFSET: %s is not finite", v)
		}
		if f < -20 || f > 20 {
			return Config{}, fmt.Errorf("TEMP_OFFSET: %s out of range -20 to 20", v)
		}
		cfg.TempOffset = f
	}

	if v := get("HISTORY_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("HISTORY_SIZE: %v", err)
		}
		if n < 1 || n > 100000 {
			return Config{}, fmt.Errorf("HISTORY_SIZE: %d out of range 1-100000", n)
		}
		cfg.HistorySize = n
	}

	cfg.StaleAfter = 3 * cfg.Interval
	if v := get("STALE_AFTER"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("STALE_AFTER: %v", err)
		}
		if d < cfg.Interval {
			return Config{}, fmt.Errorf("STALE_AFTER: %s is shorter than INTERVAL", v)
		}
		cfg.StaleAfter = d
	}

	if v := get("NO_CRC"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("NO_CRC: %v", err)
		}
		cfg.NoCRC = b
	}

	if v := getenv("API_TOKEN"); v != "" {
		if len(v) < 16 {
			return Config{}, fmt.Errorf("API_TOKEN: must be at least 16 characters")
		}
		cfg.APIToken = v
	}

	return cfg, nil
}

func (c Config) ExposedWithoutToken() bool {
	if c.APIToken != "" {
		return false
	}
	host, _, err := net.SplitHostPort(c.ListenAddr)
	if err != nil {
		return true
	}
	if host == "localhost" {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}
