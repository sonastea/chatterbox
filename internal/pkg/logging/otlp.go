// Package logging formats application logs as newline-delimited OTLP JSON.
package logging

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LevelFatal records a fatal error before the caller exits the process.
const LevelFatal = slog.LevelError + 4

type handler struct {
	output     *output
	resource   resource
	attributes []attribute
	prefix     string
}

type output struct {
	mu     sync.Mutex
	writer io.Writer
}

// NewHandler emits one ExportLogsServiceRequest JSON object per line. It uses
// OTLP's lowerCamelCase field names, numeric severity enums, and decimal strings
// for 64-bit integers. All log levels are enabled. An empty service name defaults
// to chatterbox. Derived handlers share the output lock to prevent interleaving.
func NewHandler(writer io.Writer, serviceName string) slog.Handler {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		serviceName = "chatterbox"
	}
	return &handler{
		output: &output{writer: writer},
		resource: resource{Attributes: []attribute{
			{Key: "service.name", Value: anyValue{"stringValue": serviceName}},
		}},
	}
}

var _ slog.Handler = (*handler)(nil)

func (h *handler) Enabled(context.Context, slog.Level) bool { return true }

func (h *handler) Handle(_ context.Context, record slog.Record) error {
	observed := time.Now()
	if record.Time.IsZero() {
		record.Time = observed
	}
	attributes := append([]attribute(nil), h.attributes...)
	record.Attrs(func(attr slog.Attr) bool {
		attributes = appendAttribute(attributes, h.prefix, attr)
		return true
	})
	number, text := severity(record.Level)
	request := logRequest{ResourceLogs: []resourceLogs{{
		Resource: h.resource,
		ScopeLogs: []scopeLogs{{
			Scope: scope{Name: "github.com/sonastea/chatterbox"},
			LogRecords: []logRecord{{
				TimeUnixNano:         strconv.FormatInt(record.Time.UnixNano(), 10),
				ObservedTimeUnixNano: strconv.FormatInt(observed.UnixNano(), 10),
				SeverityNumber:       number,
				SeverityText:         text,
				Body:                 anyValue{"stringValue": record.Message},
				Attributes:           uniqueAttributes(attributes),
			}},
		}},
	}}}
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	h.output.mu.Lock()
	defer h.output.mu.Unlock()
	n, err := h.output.writer.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attributes = append([]attribute(nil), h.attributes...)
	for _, attr := range attrs {
		clone.attributes = appendAttribute(clone.attributes, h.prefix, attr)
	}
	return &clone
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := *h
	clone.prefix += name + "."
	return &clone
}

func appendAttribute(attrs []attribute, prefix string, attr slog.Attr) []attribute {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return attrs
	}
	if attr.Value.Kind() == slog.KindGroup {
		if attr.Key != "" {
			prefix += attr.Key + "."
		}
		for _, child := range attr.Value.Group() {
			attrs = appendAttribute(attrs, prefix, child)
		}
		return attrs
	}
	return append(attrs, attribute{Key: prefix + attr.Key, Value: value(attr.Value)})
}

// OTLP requires unique attribute keys. Record attributes override bound ones.
func uniqueAttributes(attrs []attribute) []attribute {
	indexes := make(map[string]int, len(attrs))
	result := make([]attribute, 0, len(attrs))
	for _, attr := range attrs {
		if index, ok := indexes[attr.Key]; ok {
			result[index] = attr
		} else {
			indexes[attr.Key] = len(result)
			result = append(result, attr)
		}
	}
	return result
}

func value(v slog.Value) anyValue {
	switch v.Kind() {
	case slog.KindString:
		return anyValue{"stringValue": v.String()}
	case slog.KindBool:
		return anyValue{"boolValue": v.Bool()}
	case slog.KindInt64:
		return anyValue{"intValue": strconv.FormatInt(v.Int64(), 10)}
	case slog.KindUint64:
		if v.Uint64() <= math.MaxInt64 {
			return anyValue{"intValue": strconv.FormatUint(v.Uint64(), 10)}
		}
		// OTLP AnyValue has no unsigned integer field.
		return anyValue{"stringValue": strconv.FormatUint(v.Uint64(), 10)}
	case slog.KindFloat64:
		f := v.Float64()
		switch {
		case math.IsNaN(f):
			return anyValue{"doubleValue": "NaN"}
		case math.IsInf(f, 1):
			return anyValue{"doubleValue": "Infinity"}
		case math.IsInf(f, -1):
			return anyValue{"doubleValue": "-Infinity"}
		default:
			return anyValue{"doubleValue": f}
		}
	case slog.KindDuration:
		return anyValue{"intValue": strconv.FormatInt(int64(v.Duration()), 10)}
	case slog.KindTime:
		return anyValue{"stringValue": v.Time().UTC().Format(time.RFC3339Nano)}
	default:
		switch v := v.Any().(type) {
		case error:
			return anyValue{"stringValue": v.Error()}
		case []byte:
			return anyValue{"bytesValue": base64.StdEncoding.EncodeToString(v)}
		default:
			return anyValue{"stringValue": fmt.Sprint(v)}
		}
	}
}

func severity(level slog.Level) (int, string) {
	switch {
	case level >= LevelFatal:
		return 21, "FATAL"
	case level >= slog.LevelError:
		return 17, "ERROR"
	case level >= slog.LevelWarn:
		return 13, "WARN"
	case level >= slog.LevelInfo:
		return 9, "INFO"
	case level >= slog.LevelDebug:
		return 5, "DEBUG"
	default:
		return 1, "TRACE"
	}
}

type anyValue map[string]any

type attribute struct {
	Key   string   `json:"key"`
	Value anyValue `json:"value"`
}

type logRequest struct {
	ResourceLogs []resourceLogs `json:"resourceLogs"`
}

type resourceLogs struct {
	Resource  resource    `json:"resource"`
	ScopeLogs []scopeLogs `json:"scopeLogs"`
}

type resource struct {
	Attributes []attribute `json:"attributes"`
}

type scopeLogs struct {
	Scope      scope       `json:"scope"`
	LogRecords []logRecord `json:"logRecords"`
}

type scope struct {
	Name string `json:"name"`
}

type logRecord struct {
	TimeUnixNano         string      `json:"timeUnixNano"`
	ObservedTimeUnixNano string      `json:"observedTimeUnixNano"`
	SeverityNumber       int         `json:"severityNumber"`
	SeverityText         string      `json:"severityText"`
	Body                 anyValue    `json:"body"`
	Attributes           []attribute `json:"attributes,omitempty"`
}
