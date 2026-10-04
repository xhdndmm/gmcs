package config

import "testing"

func TestDefaultConfigIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config should be valid: %v", err)
	}
}

func TestValidateRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "empty listen address", mutate: func(c *Config) { c.ListenAddress = "" }},
		{name: "empty version", mutate: func(c *Config) { c.VersionName = "" }},
		{name: "negative protocol", mutate: func(c *Config) { c.ProtocolVersion = -1 }},
		{name: "zero players", mutate: func(c *Config) { c.MaxPlayers = 0 }},
		{name: "zero connections", mutate: func(c *Config) { c.MaxConnections = 0 }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
