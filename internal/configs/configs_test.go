package configs

import "testing"

func clearEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"DATABASE_DRIVER", "DATABASE_URL", "BROKER_DRIVER", "BROKER_URL", "REDIS_URL", "VALKEY_URL", "NATS_URL", "RABBITMQ_URL", "AMQP_URL", "HTTP_ADDR", "TLS_CERT", "TLS_KEY"} {
		t.Setenv(name, "")
	}
}

func TestDefaults(t *testing.T) {
	clearEnv(t)
	// Old service credentials must not turn on external infrastructure implicitly.
	t.Setenv("REDIS_URL", "redis://not-needed:6379")
	cfg, err := NewConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.Driver != "" || cfg.Database.URL != "" || cfg.Broker.Driver != "memory" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	http, err := cfg.HTTP()
	if err != nil || http.Addr != ":8443" || http.TLSCert != "" || http.TLSKey != "" {
		t.Fatalf("HTTP defaults = %+v, error = %v", http, err)
	}
}

func TestBrokerURLPrecedence(t *testing.T) {
	for driver, variable := range map[string]string{"redis": "REDIS_URL", "valkey": "VALKEY_URL", "nats": "NATS_URL", "rabbitmq": "RABBITMQ_URL"} {
		t.Run(driver, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("BROKER_DRIVER", driver)
			t.Setenv(variable, "specific")
			cfg, _ := NewConfig()
			if cfg.Broker.URL != "specific" {
				t.Fatalf("provider URL = %q", cfg.Broker.URL)
			}
			t.Setenv("BROKER_URL", "override")
			cfg, _ = NewConfig()
			if cfg.Broker.URL != "override" {
				t.Fatalf("generic URL = %q", cfg.Broker.URL)
			}
		})
	}
}

func TestHTTPConfig(t *testing.T) {
	clearEnv(t)
	cfg, _ := NewConfig()
	t.Setenv("HTTP_ADDR", ":9000")
	t.Setenv("TLS_CERT", "cert.pem")
	if _, err := cfg.HTTP(); err == nil {
		t.Fatal("partial TLS configuration should fail")
	}
	t.Setenv("TLS_KEY", "key.pem")
	http, err := cfg.HTTP()
	if err != nil || http.Addr != ":9000" || http.TLSCert != "cert.pem" || http.TLSKey != "key.pem" {
		t.Fatalf("HTTP config = %+v, error = %v", http, err)
	}
}
