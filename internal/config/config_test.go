// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	for _, m := range []map[string]string{nil, {"INTERVAL": "", "LISTEN_ADDR": "", "API_TOKEN": "", "NO_CRC": ""}} {
		c, err := Load(env(m))
		if err != nil {
			t.Fatal(err)
		}
		if c.ListenAddr != "127.0.0.1:8080" || c.I2CBus != 1 || c.ENS160Addr != 0x53 || c.AHT21Addr != 0x38 ||
			c.Interval != 60*time.Second || c.TempOffset != 0 || c.HistorySize != 1440 ||
			c.StaleAfter != 180*time.Second || c.NoCRC || c.APIToken != "" {
			t.Fatalf("%+v", c)
		}
	}
}

func TestValid(t *testing.T) {
	c, err := Load(env(map[string]string{
		"LISTEN_ADDR": ":9090", "I2C_BUS": "0", "ENS160_ADDR": "0x52", "AHT21_ADDR": "56",
		"INTERVAL": "30s", "TEMP_OFFSET": "-2.5", "HISTORY_SIZE": "10", "STALE_AFTER": "45s",
		"NO_CRC": "true", "API_TOKEN": "0123456789abcdef",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != ":9090" || c.I2CBus != 0 || c.ENS160Addr != 0x52 || c.AHT21Addr != 0x38 ||
		c.Interval != 30*time.Second || c.TempOffset != -2.5 || c.HistorySize != 10 ||
		c.StaleAfter != 45*time.Second || !c.NoCRC || c.APIToken != "0123456789abcdef" {
		t.Fatalf("%+v", c)
	}
}

func TestStaleDefault(t *testing.T) {
	c, err := Load(env(map[string]string{"INTERVAL": "10s"}))
	if err != nil || c.StaleAfter != 30*time.Second {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestHexAndDecimal(t *testing.T) {
	for _, v := range []string{"0x53", "83"} {
		c, err := Load(env(map[string]string{"ENS160_ADDR": v}))
		if err != nil || c.ENS160Addr != 0x53 {
			t.Fatalf("%s: %+v %v", v, c, err)
		}
	}
}

func TestInvalid(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		key  string
	}{
		{"interval low", map[string]string{"INTERVAL": "4s"}, "INTERVAL"},
		{"interval fraction", map[string]string{"INTERVAL": "1500ms"}, "INTERVAL"},
		{"interval high", map[string]string{"INTERVAL": "2h"}, "INTERVAL"},
		{"interval garbage", map[string]string{"INTERVAL": "abc"}, "INTERVAL"},
		{"ens addr", map[string]string{"ENS160_ADDR": "0x54"}, "ENS160_ADDR"},
		{"addr conflict", map[string]string{"ENS160_ADDR": "0x52", "AHT21_ADDR": "0x52"}, "AHT21_ADDR"},
		{"aht range", map[string]string{"AHT21_ADDR": "0x80"}, "AHT21_ADDR"},
		{"aht low", map[string]string{"AHT21_ADDR": "0x07"}, "AHT21_ADDR"},
		{"offset nan", map[string]string{"TEMP_OFFSET": "NaN"}, "TEMP_OFFSET"},
		{"offset inf", map[string]string{"TEMP_OFFSET": "Inf"}, "TEMP_OFFSET"},
		{"offset range", map[string]string{"TEMP_OFFSET": "21"}, "TEMP_OFFSET"},
		{"history zero", map[string]string{"HISTORY_SIZE": "0"}, "HISTORY_SIZE"},
		{"history big", map[string]string{"HISTORY_SIZE": "100001"}, "HISTORY_SIZE"},
		{"stale short", map[string]string{"STALE_AFTER": "30s"}, "STALE_AFTER"},
		{"token short", map[string]string{"API_TOKEN": "short"}, "API_TOKEN"},
		{"no crc", map[string]string{"NO_CRC": "maybe"}, "NO_CRC"},
		{"bus range", map[string]string{"I2C_BUS": "256"}, "I2C_BUS"},
		{"bus negative", map[string]string{"I2C_BUS": "-1"}, "I2C_BUS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(env(tt.env))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.HasPrefix(err.Error(), tt.key+": ") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestExposedWithoutToken(t *testing.T) {
	token := "0123456789abcdef"
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"default", nil, false},
		{"ipv4 loopback", map[string]string{"LISTEN_ADDR": "127.0.0.1:9000"}, false},
		{"ipv6 loopback", map[string]string{"LISTEN_ADDR": "[::1]:9000"}, false},
		{"localhost", map[string]string{"LISTEN_ADDR": "localhost:9000"}, false},
		{"all interfaces", map[string]string{"LISTEN_ADDR": ":8080"}, true},
		{"wildcard", map[string]string{"LISTEN_ADDR": "0.0.0.0:8080"}, true},
		{"lan", map[string]string{"LISTEN_ADDR": "192.168.1.10:8080"}, true},
		{"all interfaces with token", map[string]string{"LISTEN_ADDR": ":8080", "API_TOKEN": token}, false},
	}
	for _, c := range cases {
		cfg, err := Load(env(c.env))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := cfg.ExposedWithoutToken(); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
