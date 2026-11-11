// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 furu04

package recorder

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/furu04/air-sensor-api/internal/readingjson"
	"github.com/furu04/air-sensor-api/internal/store"
)

const (
	fileMode      = 0o640
	maxLineSize   = 64 << 10
	ctxCheckEvery = 1024
)

type Recorder struct {
	path string
}

type line struct {
	TS string `json:"ts"`
	readingjson.Measurements
}

func New(path string) (*Recorder, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode)
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return &Recorder{path: path}, nil
}

func (r *Recorder) Record(rd store.Reading) error {
	b, err := json.Marshal(line{TS: readingjson.FormatTS(rd), Measurements: readingjson.Of(rd)})
	if err != nil {
		return err
	}
	b = append(b, '\n')
	f, err := os.OpenFile(r.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode)
	if err != nil {
		return err
	}
	n, err := f.Write(b)
	if err == nil && n < len(b) {
		err = fmt.Errorf("short write: %d of %d bytes", n, len(b))
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func (r *Recorder) Read(ctx context.Context, since, until time.Time, limit int) ([]json.RawMessage, bool, error) {
	f, err := os.Open(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return []json.RawMessage{}, false, nil
		}
		return nil, false, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), maxLineSize)
	out := []json.RawMessage{}
	for n := 1; sc.Scan(); n++ {
		if n%ctxCheckEvery == 0 {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
		}
		var head struct {
			TS string `json:"ts"`
		}
		raw := sc.Bytes()
		if json.Unmarshal(raw, &head) != nil {
			continue
		}
		ts, err := time.Parse(time.RFC3339, head.TS)
		if err != nil {
			continue
		}
		if !since.IsZero() && ts.Before(since) {
			continue
		}
		if !until.IsZero() && !ts.Before(until) {
			continue
		}
		if len(out) == limit {
			return out, true, nil
		}
		out = append(out, append(json.RawMessage(nil), raw...))
	}
	if err := sc.Err(); err != nil {
		return nil, false, err
	}
	return out, false, nil
}
