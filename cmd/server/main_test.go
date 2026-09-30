package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"testing"
)

func TestMainLogsFatalError(t *testing.T) {
	if os.Getenv("CHATTERBOX_TEST_MAIN") == "1" {
		main()
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestMainLogsFatalError$")
	command.Env = append(os.Environ(), "CHATTERBOX_TEST_MAIN=1", "TLS_CERT=cert.pem", "TLS_KEY=", "OTEL_SERVICE_NAME=test-service")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 1 {
		t.Fatalf("exit error = %v, want exit code 1; stderr: %s", err, stderr.String())
	}
	if stdout.Len() != 0 || bytes.Count(stderr.Bytes(), []byte{'\n'}) != 1 {
		t.Fatalf("expected one JSON log on stderr; stdout: %s; stderr: %s", stdout.String(), stderr.String())
	}
	var request struct {
		ResourceLogs []struct {
			Resource struct {
				Attributes []struct {
					Key   string            `json:"key"`
					Value map[string]string `json:"value"`
				} `json:"attributes"`
			} `json:"resource"`
			ScopeLogs []struct {
				LogRecords []struct {
					SeverityNumber int    `json:"severityNumber"`
					SeverityText   string `json:"severityText"`
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
	if err := json.Unmarshal(stderr.Bytes(), &request); err != nil {
		t.Fatalf("invalid OTLP JSON: %v: %s", err, stderr.String())
	}
	if len(request.ResourceLogs) != 1 || len(request.ResourceLogs[0].ScopeLogs) != 1 || len(request.ResourceLogs[0].ScopeLogs[0].LogRecords) != 1 {
		t.Fatalf("expected one OTLP log record: %s", stderr.String())
	}
	resource := request.ResourceLogs[0]
	if len(resource.Resource.Attributes) != 1 || resource.Resource.Attributes[0].Key != "service.name" || resource.Resource.Attributes[0].Value["stringValue"] != "test-service" {
		t.Fatalf("unexpected resource: %+v", resource.Resource)
	}
	record := resource.ScopeLogs[0].LogRecords[0]
	if record.SeverityNumber != 21 || record.SeverityText != "FATAL" || record.Body.StringValue != "chatterbox stopped" {
		t.Fatalf("unexpected fatal log: %+v", record)
	}
	if len(record.Attributes) != 1 || record.Attributes[0].Key != "error" || record.Attributes[0].Value["stringValue"] != "TLS_CERT and TLS_KEY must be configured together" {
		t.Fatalf("missing configuration error: %+v", record.Attributes)
	}
}
