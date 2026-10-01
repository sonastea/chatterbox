package broker

import (
	"context"
	"errors"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/sonastea/chatterbox/internal/testutil"
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
			output := testutil.CaptureLogs(t)
			test.log()
			_, record := testutil.DecodeLog(t, output.Bytes())
			if record.Body.StringValue != test.body || record.SeverityNumber != test.severity {
				t.Fatalf("unexpected diagnostic: %+v", record)
			}
			attrs := record.AttributeMap()
			topic, _ := attrs["topic"]["stringValue"].(string)
			if attrs["component"]["stringValue"] != test.component || topic != test.topic {
				t.Fatalf("unexpected diagnostic attributes: %+v", attrs)
			}
			if test.component == "nats" && attrs["error"]["stringValue"] != "test error" {
				t.Fatalf("missing NATS error: %+v", attrs)
			}
		})
	}
}
