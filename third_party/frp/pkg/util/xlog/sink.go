// Copyright 2026 KurumiFRP contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package xlog

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

type Level uint8

const (
	Trace Level = iota
	Debug
	Info
	Warn
	Error
)

// MaxRecordBytes limits retained message size, including prefixes.
const MaxRecordBytes = 4096

// Record belongs to exactly one service instance. Message can contain private
// data and must only be made available to that instance's authorized readers.
type Record struct {
	Time    time.Time
	Level   Level
	Message string
}

// Sink owns a bounded, nonblocking channel. Readers cannot close the channel:
// late service goroutines can safely drop messages after a reader has stopped.
// A sink is single-use, must not be copied, and must never be attached to
// multiple services.
type Sink struct {
	records chan Record
	level   Level
	claimed atomic.Bool
	dropped atomic.Uint64
}

// NewSink creates a sink with room for 1 to 4096 records.
func NewSink(capacity int, minimum Level) (*Sink, error) {
	if capacity < 1 || capacity > 4096 {
		return nil, fmt.Errorf("log sink capacity must be between 1 and 4096")
	}
	if minimum > Error {
		return nil, fmt.Errorf("invalid minimum log level")
	}
	return &Sink{records: make(chan Record, capacity), level: minimum}, nil
}

// Records is receive-only and is intentionally never closed. Its owner must
// stop reading when its service generation and log consumer have stopped.
func (s *Sink) Records() <-chan Record { return s.records }

// Dropped returns the number of records discarded because the channel was full.
func (s *Sink) Dropped() uint64 { return s.dropped.Load() }

// Claim prevents accidental sharing across service instances, even after a
// failed construction: upstream construction can leave background work alive.
func (s *Sink) Claim() error {
	if s == nil || s.records == nil || cap(s.records) == 0 || !s.claimed.CompareAndSwap(false, true) {
		return fmt.Errorf("log sink must be non-nil and dedicated to one service")
	}
	return nil
}

func (s *Sink) emit(level Level, prefix, format string, args ...any) {
	if level < s.level {
		return
	}
	message := format
	if len(args) > 0 {
		if format == "" {
			message = fmt.Sprint(args...)
		} else {
			message = fmt.Sprintf(format, args...)
		}
	}
	message = prefix + message
	if len(message) > MaxRecordBytes {
		message = strings.ToValidUTF8(message[:MaxRecordBytes], "?")
		// Do not retain the entire backing allocation of an oversized message.
		message = strings.Clone(message)
	}
	record := Record{Time: time.Now(), Level: level, Message: message}
	select {
	case s.records <- record:
	default:
		s.dropped.Add(1)
	}
}
