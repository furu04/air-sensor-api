// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package readingjson

import (
	"time"

	"github.com/furu04/air-sensor-api/internal/store"
)

type Measurements struct {
	TempC        *float64 `json:"temp_c,omitempty"`
	RHPct        *float64 `json:"rh_pct,omitempty"`
	AQI          *int     `json:"aqi,omitempty"`
	TVOCPPB      *int     `json:"tvoc_ppb,omitempty"`
	ECO2PPM      *int     `json:"eco2_ppm,omitempty"`
	CO2PPM       *int     `json:"co2_ppm,omitempty"`
	Validity     *int     `json:"validity,omitempty"`
	ValidityName string   `json:"validity_name,omitempty"`
	Valid        bool     `json:"valid"`
}

func Of(r store.Reading) Measurements {
	m := Measurements{Valid: r.Valid()}
	if r.HasClimate {
		t, rh := r.TempC, r.RHPct
		m.TempC, m.RHPct = &t, &rh
	}
	if r.HasGas {
		aqi, tvoc, eco2, validity := r.AQI, r.TVOCPPB, r.ECO2PPM, r.Validity
		m.AQI, m.TVOCPPB, m.ECO2PPM, m.Validity = &aqi, &tvoc, &eco2, &validity
		m.ValidityName = r.ValidityName()
	}
	if r.HasCO2 {
		co2 := r.CO2PPM
		m.CO2PPM = &co2
	}
	return m
}

func FormatTS(r store.Reading) string { return r.Wall.UTC().Format(time.RFC3339) }
