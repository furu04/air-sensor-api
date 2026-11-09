// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package psychro

import "math"

const (
	magnusA = 17.62
	magnusB = 243.12
	magnusE = 6.112
)

func SaturationVaporPressure(tempC float64) float64 {
	return magnusE * math.Exp(magnusA*tempC/(magnusB+tempC))
}

func RescaleRH(rhPct, fromTempC, toTempC float64) float64 {
	if fromTempC == toTempC {
		return rhPct
	}
	return rhPct * SaturationVaporPressure(fromTempC) / SaturationVaporPressure(toTempC)
}
