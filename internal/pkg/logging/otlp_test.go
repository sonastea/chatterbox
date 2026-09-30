package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"log/slog"
	"math"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestOTLPEnvelope(t *testing.T) {
	var output bytes.Buffer
	h := NewHandler(&output, "test-service")
	timestamp := time.Unix(1_700_000_000, 123456789)
	record := slog.NewRecord(timestamp, slog.LevelInfo, "listening\n\"quoted\"", 0)
	record.AddAttrs(slog.String("address", ":8443"), slog.Bool("tls.enabled", false))
	before := time.Now().UnixNano()
	if err := h.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UnixNano()
	if bytes.Count(output.Bytes(), []byte{'\n'}) != 1 || !bytes.HasSuffix(output.Bytes(), []byte{'\n'}) {
		t.Fatalf("expected one newline-delimited JSON object: %q", output.String())
	}
	resource, got := decodeLog(t, output.Bytes())
	if resource.Resource.Attributes[0].Key != "service.name" || resource.Resource.Attributes[0].Value["stringValue"] != "test-service" {
		t.Fatalf("unexpected resource: %+v", resource.Resource)
	}
	if resource.ScopeLogs[0].Scope.Name != "github.com/sonastea/chatterbox" {
		t.Fatalf("unexpected scope: %+v", resource.ScopeLogs[0].Scope)
	}
	if got.TimeUnixNano != strconv.FormatInt(timestamp.UnixNano(), 10) {
		t.Fatalf("timestamp = %q", got.TimeUnixNano)
	}
	observed, err := strconv.ParseInt(got.ObservedTimeUnixNano, 10, 64)
	if err != nil || observed < before || observed > after {
		t.Fatalf("observed timestamp = %q, error = %v", got.ObservedTimeUnixNano, err)
	}
	if got.SeverityNumber != 9 || got.SeverityText != "INFO" || got.Body["stringValue"] != record.Message {
		t.Fatalf("unexpected log record: %+v", got)
	}
	want := map[string]anyValue{
		"address":     {"stringValue": ":8443"},
		"tls.enabled": {"boolValue": false},
	}
	if !reflect.DeepEqual(attributeMap(got), want) {
		t.Fatalf("attributes = %+v, want %+v", got.Attributes, want)
	}
}

func TestOTLPSeverities(t *testing.T) {
	for _, test := range []struct {
		level  slog.Level
		number int
		text   string
	}{
		{slog.LevelDebug - 4, 1, "TRACE"},
		{slog.LevelDebug, 5, "DEBUG"},
		{slog.LevelInfo, 9, "INFO"},
		{slog.LevelWarn, 13, "WARN"},
		{slog.LevelError, 17, "ERROR"},
		{LevelFatal, 21, "FATAL"},
	} {
		t.Run(test.text, func(t *testing.T) {
			var output bytes.Buffer
			slog.New(NewHandler(&output, "")).Log(context.Background(), test.level, "message")
			resource, record := decodeLog(t, output.Bytes())
			if record.SeverityNumber != test.number || record.SeverityText != test.text {
				t.Fatalf("severity = %d/%s, want %d/%s", record.SeverityNumber, record.SeverityText, test.number, test.text)
			}
			if resource.Resource.Attributes[0].Value["stringValue"] != "chatterbox" {
				t.Fatal("missing default service name")
			}
		})
	}
}

func TestOTLPAttributeTypes(t *testing.T) {
	for _, test := range []struct {
		name  string
		value slog.Value
		want  anyValue
	}{
		{"string", slog.StringValue("hello\nworld"), anyValue{"stringValue": "hello\nworld"}},
		{"bool", slog.BoolValue(false), anyValue{"boolValue": false}},
		{"int", slog.Int64Value(9007199254740993), anyValue{"intValue": "9007199254740993"}},
		{"negative", slog.Int64Value(-42), anyValue{"intValue": "-42"}},
		{"uint", slog.Uint64Value(42), anyValue{"intValue": "42"}},
		{"large uint", slog.Uint64Value(math.MaxUint64), anyValue{"stringValue": "18446744073709551615"}},
		{"float", slog.Float64Value(1.25), anyValue{"doubleValue": 1.25}},
		{"nan", slog.Float64Value(math.NaN()), anyValue{"doubleValue": "NaN"}},
		{"positive infinity", slog.Float64Value(math.Inf(1)), anyValue{"doubleValue": "Infinity"}},
		{"negative infinity", slog.Float64Value(math.Inf(-1)), anyValue{"doubleValue": "-Infinity"}},
		{"duration", slog.DurationValue(time.Millisecond), anyValue{"intValue": "1000000"}},
		{"time", slog.TimeValue(time.Unix(0, 0)), anyValue{"stringValue": "1970-01-01T00:00:00Z"}},
		{"error", slog.AnyValue(errors.New("failed\nagain")), anyValue{"stringValue": "failed\nagain"}},
		{"bytes", slog.AnyValue([]byte{1, 2, 3}), anyValue{"bytesValue": "AQID"}},
		{"empty bytes", slog.AnyValue([]byte{}), anyValue{"bytesValue": ""}},
		{"log valuer", slog.AnyValue(testLogValuer{}), anyValue{"stringValue": "resolved"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			slog.New(NewHandler(&output, "")).LogAttrs(context.Background(), slog.LevelInfo, "typed",
				slog.Attr{Key: "value", Value: test.value})
			_, record := decodeLog(t, output.Bytes())
			if got := attributeMap(record)["value"]; !reflect.DeepEqual(got, test.want) {
				t.Fatalf("value = %+v, want %+v", got, test.want)
			}
		})
	}
}

type testLogValuer struct{}

func (testLogValuer) LogValue() slog.Value { return slog.StringValue("resolved") }

func TestOTLPGroupsAndBoundAttributes(t *testing.T) {
	var output bytes.Buffer
	base := slog.New(NewHandler(&output, "")).With("component", "http")
	grouped := base.WithGroup("request").With("method", "GET")
	grouped.WithGroup("").Info("request",
		slog.Group("peer", "address", "127.0.0.1"),
		slog.Group("", "inlined", true),
		slog.Group("empty"), slog.Attr{})
	_, record := decodeLog(t, output.Bytes())
	want := map[string]anyValue{
		"component":            {"stringValue": "http"},
		"request.method":       {"stringValue": "GET"},
		"request.peer.address": {"stringValue": "127.0.0.1"},
		"request.inlined":      {"boolValue": true},
	}
	if !reflect.DeepEqual(attributeMap(record), want) {
		t.Fatalf("attributes = %+v, want %+v", record.Attributes, want)
	}
	output.Reset()
	base.Info("base")
	_, record = decodeLog(t, output.Bytes())
	if len(record.Attributes) != 1 || record.Attributes[0].Key != "component" {
		t.Fatalf("derived handler mutated base: %+v", record.Attributes)
	}
	output.Reset()
	base.With("key", "bound").Info("override", "key", "record", "key", "last")
	_, record = decodeLog(t, output.Bytes())
	if len(record.Attributes) != 2 || attributeMap(record)["key"]["stringValue"] != "last" {
		t.Fatalf("duplicate attributes were not overridden: %+v", record.Attributes)
	}
}

func TestOTLPConcurrentOutput(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewHandler(&output, ""))
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.With("worker", i).Info("concurrent\nmessage")
		}()
	}
	wg.Wait()
	lines := bytes.Split(bytes.TrimSuffix(output.Bytes(), []byte{'\n'}), []byte{'\n'})
	if len(lines) != 200 {
		t.Fatalf("got %d JSON lines, want 200", len(lines))
	}
	workers := make(map[string]bool)
	for _, line := range lines {
		_, record := decodeLog(t, line)
		worker := attributeMap(record)["worker"]["intValue"].(string)
		if workers[worker] {
			t.Fatalf("duplicate worker %s", worker)
		}
		workers[worker] = true
	}
}

func TestOTLPStandardLogBridge(t *testing.T) {
	previousLogger, previousOutput, previousFlags := slog.Default(), log.Writer(), log.Flags()
	previousLevel := slog.SetLogLoggerLevel(slog.LevelInfo)
	t.Cleanup(func() {
		slog.SetDefault(previousLogger)
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
		slog.SetLogLoggerLevel(previousLevel)
	})
	var output bytes.Buffer
	slog.SetDefault(slog.New(NewHandler(&output, "")))
	log.Print("legacy message")
	_, record := decodeLog(t, output.Bytes())
	if record.Body["stringValue"] != "legacy message" || record.SeverityNumber != 9 {
		t.Fatalf("unexpected standard log output: %+v", record)
	}
}

func TestOTLPZeroTimestampAndWriteErrors(t *testing.T) {
	var output bytes.Buffer
	record := slog.NewRecord(time.Time{}, slog.LevelInfo, "message", 0)
	if err := NewHandler(&output, "").Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	_, decoded := decodeLog(t, output.Bytes())
	if decoded.TimeUnixNano != decoded.ObservedTimeUnixNano {
		t.Fatal("zero timestamp was not replaced with observed time")
	}
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{"failed write", io.ErrClosedPipe, io.ErrClosedPipe},
		{"short write", nil, io.ErrShortWrite},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := NewHandler(failedWriter{test.err}, "").Handle(context.Background(), record)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

type failedWriter struct{ err error }

func (w failedWriter) Write([]byte) (int, error) { return 0, w.err }

func decodeLog(t *testing.T, data []byte) (resourceLogs, logRecord) {
	t.Helper()
	var request logRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		t.Fatalf("invalid OTLP JSON: %v: %s", err, data)
	}
	if len(request.ResourceLogs) != 1 || len(request.ResourceLogs[0].ScopeLogs) != 1 || len(request.ResourceLogs[0].ScopeLogs[0].LogRecords) != 1 {
		t.Fatalf("expected one resource, scope, and log record: %s", data)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("unexpected trailing log data: %v: %s", err, data)
	}
	resource := request.ResourceLogs[0]
	return resource, resource.ScopeLogs[0].LogRecords[0]
}

func attributeMap(record logRecord) map[string]anyValue {
	attrs := make(map[string]anyValue)
	for _, attr := range record.Attributes {
		attrs[attr.Key] = attr.Value
	}
	return attrs
}
