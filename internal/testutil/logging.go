// Package testutil provides shared test helpers.
package testutil

import (
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"testing"

	"github.com/sonastea/chatterbox/internal/pkg/logging"
)

// CaptureLogs installs an OTLP logger and restores the slog and standard log
// settings during test cleanup. Callers must run serially and finish logging
// before reading the returned buffer.
func CaptureLogs(t testing.TB) *bytes.Buffer {
	t.Helper()
	previousLogger, previousOutput, previousFlags := slog.Default(), log.Writer(), log.Flags()
	t.Cleanup(func() {
		slog.SetDefault(previousLogger)
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
	})
	var output bytes.Buffer
	slog.SetDefault(slog.New(logging.NewHandler(&output, "chatterbox")))
	return &output
}

// LogAttribute is a named OTLP value, retaining its JSON value type.
type LogAttribute struct {
	Key   string         `json:"key"`
	Value map[string]any `json:"value"`
}

// LogResource contains the resource attributes from an OTLP envelope.
type LogResource struct {
	Attributes []LogAttribute `json:"attributes"`
}

// LogRecord contains the OTLP fields used by application logging tests.
type LogRecord struct {
	SeverityNumber int    `json:"severityNumber"`
	SeverityText   string `json:"severityText"`
	Body           struct {
		StringValue string `json:"stringValue"`
	} `json:"body"`
	Attributes []LogAttribute `json:"attributes"`
}

// AttributeMap indexes the record's attributes by key.
func (r LogRecord) AttributeMap() map[string]map[string]any {
	attrs := make(map[string]map[string]any, len(r.Attributes))
	for _, attr := range r.Attributes {
		attrs[attr.Key] = attr.Value
	}
	return attrs
}

// DecodeLog decodes exactly one OTLP JSON envelope containing one resource,
// one scope, and one log record.
func DecodeLog(t testing.TB, data []byte) (LogResource, LogRecord) {
	t.Helper()
	var request struct {
		ResourceLogs []struct {
			Resource  LogResource `json:"resource"`
			ScopeLogs []struct {
				LogRecords []LogRecord `json:"logRecords"`
			} `json:"scopeLogs"`
		} `json:"resourceLogs"`
	}
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatalf("invalid OTLP JSON: %v: %s", err, data)
	}
	if len(request.ResourceLogs) != 1 || len(request.ResourceLogs[0].ScopeLogs) != 1 || len(request.ResourceLogs[0].ScopeLogs[0].LogRecords) != 1 {
		t.Fatalf("expected one OTLP resource, scope, and log record: %s", data)
	}
	resource := request.ResourceLogs[0]
	return resource.Resource, resource.ScopeLogs[0].LogRecords[0]
}
