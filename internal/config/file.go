// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
)

const maxFileSize = 64 << 10

type addrValue string

func (a *addrValue) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*a = addrValue(s)
		return nil
	}
	if _, err := strconv.ParseUint(string(b), 10, 8); err != nil {
		return fmt.Errorf("addr must be a string or an unsigned integer, got %s", b)
	}
	*a = addrValue(b)
	return nil
}

type sensorFile struct {
	Enabled *bool      `json:"enabled"`
	Addr    *addrValue `json:"addr"`
	Bus     *int       `json:"bus"`
}

type fixedAddrSensorFile struct {
	Enabled *bool `json:"enabled"`
	Bus     *int  `json:"bus"`
}

type sensorsFile struct {
	AHT21  *sensorFile          `json:"aht21"`
	ENS160 *sensorFile          `json:"ens160"`
	SCD4x  *fixedAddrSensorFile `json:"scd4x"`
}

type fileConfig struct {
	ListenAddr  *string      `json:"listen_addr"`
	I2CBus      *int         `json:"i2c_bus"`
	Interval    *string      `json:"interval"`
	TempOffset  *float64     `json:"temp_offset"`
	HistorySize *int         `json:"history_size"`
	StaleAfter  *string      `json:"stale_after"`
	NoCRC       *bool        `json:"no_crc"`
	RecordFile  *string      `json:"record_file"`
	Sensors     *sensorsFile `json:"sensors"`
}

func parseFile(data []byte) (map[string]string, error) {
	if len(data) > maxFileSize {
		return nil, fmt.Errorf("larger than %d bytes", maxFileSize)
	}
	var fc fileConfig
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&fc); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected content after the top-level object")
	}

	out := map[string]string{}
	str := func(key string, v *string) {
		if v != nil {
			out[key] = *v
		}
	}
	flag := func(key string, v *bool) {
		if v != nil {
			out[key] = strconv.FormatBool(*v)
		}
	}
	str("LISTEN_ADDR", fc.ListenAddr)
	str("INTERVAL", fc.Interval)
	str("STALE_AFTER", fc.StaleAfter)
	str("RECORD_FILE", fc.RecordFile)
	flag("NO_CRC", fc.NoCRC)
	integer := func(key string, v *int) {
		if v != nil {
			out[key] = strconv.Itoa(*v)
		}
	}
	integer("I2C_BUS", fc.I2CBus)
	integer("HISTORY_SIZE", fc.HistorySize)
	if fc.TempOffset != nil {
		out["TEMP_OFFSET"] = strconv.FormatFloat(*fc.TempOffset, 'g', -1, 64)
	}
	if fc.Sensors != nil {
		for _, e := range []struct {
			prefix string
			f      *sensorFile
		}{{"AHT21", fc.Sensors.AHT21}, {"ENS160", fc.Sensors.ENS160}} {
			if e.f == nil {
				continue
			}
			flag(e.prefix+"_ENABLE", e.f.Enabled)
			integer(e.prefix+"_BUS", e.f.Bus)
			if e.f.Addr != nil {
				out[e.prefix+"_ADDR"] = string(*e.f.Addr)
			}
		}
		if fc.Sensors.SCD4x != nil {
			flag("SCD4X_ENABLE", fc.Sensors.SCD4x.Enabled)
			integer("SCD4X_BUS", fc.Sensors.SCD4x.Bus)
		}
	}
	return out, nil
}
