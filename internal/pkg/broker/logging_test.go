package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/sonastea/chatterbox/internal/pkg/logging"
)

func TestBrokerDiagnosticsUseOTLPJSON(t *testing.T) {
	for _, test := range []struct {
		name      string
		log       func()
		body      string
		component string
		severity  int
		topic     string
	}{
		{
			name: "redis",
			log: func() {
				redisLogger{}.Printf(context.Background(), "redis: connection failed: %s", "test error")
			},
			body: "redis: connection failed: test error", component: "redis", severity: 13,
		},
		{
			name: "nats connection",
			log: func() {
				logNATSError(nil, nil, errors.New("test error"))
			},
			body: "nats asynchronous error", component: "nats", severity: 17,
		},
		{
			name: "nats subscription",
			log: func() {
				logNATSError(nil, &nats.Subscription{Subject: "room.*"}, errors.New("test error"))
			},
			body: "nats asynchronous error", component: "nats", severity: 17, topic: "room.*",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			previousLogger, previousOutput, previousFlags := slog.Default(), log.Writer(), log.Flags()
			t.Cleanup(func() {
				slog.SetDefault(previousLogger)
				log.SetOutput(previousOutput)
				log.SetFlags(previousFlags)
			})
			var output bytes.Buffer
			slog.SetDefault(slog.New(logging.NewHandler(&output, "chatterbox")))
			test.log()
			var request struct {
				ResourceLogs []struct {
					ScopeLogs []struct {
						LogRecords []struct {
							SeverityNumber int `json:"severityNumber"`
							Body           struct {
								StringValue string `json:"stringValue"`
							} `json:"body"`
							Attributes []struct {
								Key   string            `json:"key"`
								Value map[string]string `json:"value"`
							} `json:"attributes"`
						} `json:"logRecords"`
					} `json:"scopeLogs"`
				} `json:"resourceLogs"`
			}
			if err := json.Unmarshal(output.Bytes(), &request); err != nil {
				t.Fatalf("invalid OTLP JSON: %v: %s", err, output.String())
			}
			if len(request.ResourceLogs) != 1 || len(request.ResourceLogs[0].ScopeLogs) != 1 || len(request.ResourceLogs[0].ScopeLogs[0].LogRecords) != 1 {
				t.Fatalf("expected one OTLP log record: %s", output.String())
			}
			record := request.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
			if record.Body.StringValue != test.body || record.SeverityNumber != test.severity {
				t.Fatalf("unexpected diagnostic: %+v", record)
			}
			attrs := make(map[string]string)
			for _, attr := range record.Attributes {
				attrs[attr.Key] = attr.Value["stringValue"]
			}
			if attrs["component"] != test.component || attrs["topic"] != test.topic {
				t.Fatalf("unexpected diagnostic attributes: %+v", attrs)
			}
			if test.component == "nats" && attrs["error"] != "test error" {
				t.Fatalf("missing NATS error: %+v", attrs)
			}
		})
	}
}
