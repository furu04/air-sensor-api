// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package config

import (
	"os"
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

func TestSCD4xEnable(t *testing.T) {
	c, err := Load(env(nil))
	if err != nil || c.SCD4xEnable {
		t.Fatalf("default: %+v %v", c, err)
	}
	for _, v := range []string{"true", "1", "TRUE"} {
		c, err := Load(env(map[string]string{"SCD4X_ENABLE": v}))
		if err != nil || !c.SCD4xEnable {
			t.Fatalf("%s: %+v %v", v, c, err)
		}
	}
	c, err = Load(env(map[string]string{"SCD4X_ENABLE": "false", "AHT21_ADDR": "0x62"}))
	if err != nil || c.SCD4xEnable || c.AHT21Addr != 0x62 {
		t.Fatalf("disabled must not reserve 0x62: %+v %v", c, err)
	}
	c, err = Load(env(map[string]string{"SCD4X_ENABLE": ""}))
	if err != nil || c.SCD4xEnable {
		t.Fatalf("empty: %+v %v", c, err)
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
		{"scd4x garbage", map[string]string{"SCD4X_ENABLE": "maybe"}, "SCD4X_ENABLE"},
		{"scd4x aht conflict", map[string]string{"SCD4X_ENABLE": "true", "AHT21_ADDR": "0x62"}, "AHT21_ADDR"},
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

func fsOf(files map[string]string) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) {
		if c, ok := files[p]; ok {
			return []byte(c), nil
		}
		return nil, os.ErrNotExist
	}
}

func loadFile(content string, envs map[string]string) (Config, error) {
	m := map[string]string{"CONFIG_FILE": "/etc/c.json"}
	for k, v := range envs {
		m[k] = v
	}
	return LoadWith(env(m), fsOf(map[string]string{"/etc/c.json": content}))
}

func TestFileFull(t *testing.T) {
	c, err := loadFile(`{
		"listen_addr": ":9090", "i2c_bus": 0, "interval": "30s", "temp_offset": -1.5,
		"history_size": 10, "stale_after": "45s", "no_crc": true,
		"sensors": {
			"aht21": {"enabled": true, "addr": "0x39"},
			"ens160": {"enabled": true, "addr": 82},
			"scd4x": {"enabled": true}
		}
	}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != ":9090" || c.I2CBus != 0 || c.Interval != 30*time.Second || c.TempOffset != -1.5 ||
		c.HistorySize != 10 || c.StaleAfter != 45*time.Second || !c.NoCRC ||
		!c.AHT21Enable || !c.ENS160Enable || !c.SCD4xEnable || c.AHT21Addr != 0x39 || c.ENS160Addr != 0x52 {
		t.Fatalf("%+v", c)
	}
}

func TestFileEmptyObjectKeepsDefaults(t *testing.T) {
	c, err := loadFile(`{}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := Load(env(nil))
	if c != want {
		t.Fatalf("%+v want %+v", c, want)
	}
}

func TestFileSensorSelection(t *testing.T) {
	c, err := loadFile(`{"sensors": {"aht21": {"enabled": false}, "ens160": {"enabled": false}, "scd4x": {"enabled": true}}}`, nil)
	if err != nil || c.AHT21Enable || c.ENS160Enable || !c.SCD4xEnable {
		t.Fatalf("%+v %v", c, err)
	}
	c, err = loadFile(`{"sensors": {"ens160": {"enabled": false}}}`, nil)
	if err != nil || !c.AHT21Enable || c.ENS160Enable || c.SCD4xEnable {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	c, err := loadFile(`{"interval": "30s", "sensors": {"scd4x": {"enabled": true}, "ens160": {"enabled": false}}}`,
		map[string]string{"INTERVAL": "10s", "SCD4X_ENABLE": "false", "ENS160_ENABLE": "true"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Interval != 10*time.Second || c.SCD4xEnable || !c.ENS160Enable {
		t.Fatalf("%+v", c)
	}
}

func TestEnvDoesNotOverrideInvalidFileValue(t *testing.T) {
	if _, err := loadFile(`{"interval": "1s"}`, nil); err == nil || !strings.HasPrefix(err.Error(), "INTERVAL: ") {
		t.Fatalf("err=%v", err)
	}
	if _, err := loadFile(`{"interval": "1s"}`, map[string]string{"INTERVAL": "20s"}); err != nil {
		t.Fatalf("env should supersede the file value: %v", err)
	}
}

func TestNoSensorsEnabled(t *testing.T) {
	_, err := Load(env(map[string]string{"AHT21_ENABLE": "false", "ENS160_ENABLE": "0"}))
	if err == nil || !strings.HasPrefix(err.Error(), "sensors: ") {
		t.Fatalf("err=%v", err)
	}
	_, err = loadFile(`{"sensors": {"aht21": {"enabled": false}, "ens160": {"enabled": false}}}`, nil)
	if err == nil || !strings.Contains(err.Error(), "at least one sensor") {
		t.Fatalf("err=%v", err)
	}
}

func TestAddressConflictsOnlyAmongEnabled(t *testing.T) {
	c, err := Load(env(map[string]string{"ENS160_ENABLE": "false", "AHT21_ADDR": "0x53"}))
	if err != nil || c.AHT21Addr != 0x53 {
		t.Fatalf("%+v %v", c, err)
	}
	c, err = Load(env(map[string]string{"AHT21_ENABLE": "false", "SCD4X_ENABLE": "true", "AHT21_ADDR": "0x62"}))
	if err != nil || !c.SCD4xEnable {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err = Load(env(map[string]string{"ENS160_ENABLE": "false", "AHT21_ADDR": "0x80"})); err == nil {
		t.Fatal("range must be validated even when the sensor is disabled")
	}
}

func TestFileInvalid(t *testing.T) {
	big := `{"listen_addr": "` + strings.Repeat("a", maxFileSize) + `"}`
	tests := []struct {
		name, content string
	}{
		{"unknown key", `{"intervall": "30s"}`},
		{"api token", `{"api_token": "0123456789abcdef"}`},
		{"unknown sensor", `{"sensors": {"bme280": {"enabled": true}}}`},
		{"scd4x addr", `{"sensors": {"scd4x": {"addr": "0x62"}}}`},
		{"wrong type", `{"i2c_bus": "1"}`},
		{"bool as string", `{"sensors": {"aht21": {"enabled": "yes"}}}`},
		{"addr float", `{"sensors": {"aht21": {"addr": 56.5}}}`},
		{"addr negative", `{"sensors": {"aht21": {"addr": -1}}}`},
		{"addr bool", `{"sensors": {"aht21": {"addr": true}}}`},
		{"trailing object", `{} {}`},
		{"trailing garbage", `{} x`},
		{"not json", `interval=30s`},
		{"empty", ``},
		{"array", `[]`},
		{"too big", big},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadFile(tt.content, nil)
			if err == nil || !strings.HasPrefix(err.Error(), "CONFIG_FILE: ") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestFileMissing(t *testing.T) {
	_, err := LoadWith(env(map[string]string{"CONFIG_FILE": "/nope.json"}), fsOf(nil))
	if err == nil || !strings.HasPrefix(err.Error(), "CONFIG_FILE: ") {
		t.Fatalf("err=%v", err)
	}
}

func TestFileNotReadWithoutPath(t *testing.T) {
	called := false
	_, err := LoadWith(env(nil), func(string) ([]byte, error) { called = true; return nil, nil })
	if err != nil || called {
		t.Fatalf("called=%v err=%v", called, err)
	}
}

func TestAPITokenOnlyFromEnv(t *testing.T) {
	c, err := loadFile(`{}`, map[string]string{"API_TOKEN": "0123456789abcdef"})
	if err != nil || c.APIToken != "0123456789abcdef" {
		t.Fatalf("%+v %v", c, err)
	}
}
