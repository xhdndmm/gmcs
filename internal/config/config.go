package config

import "fmt"

type Config struct {
	ListenAddress   string
	MOTD            string
	VersionName     string
	ProtocolVersion int32
	MaxPlayers      int
	MaxConnections  int
}

func Default() Config {
	return Config{
		ListenAddress:   ":25565",
		MOTD:            "A gmcs server",
		VersionName:     "gmcs-dev",
		ProtocolVersion: 0,
		MaxPlayers:      100,
		MaxConnections:  256,
	}
}

func (c Config) Validate() error {
	if c.ListenAddress == "" {
		return fmt.Errorf("listen address must not be empty")
	}
	if c.VersionName == "" {
		return fmt.Errorf("version name must not be empty")
	}
	if c.ProtocolVersion < 0 {
		return fmt.Errorf("protocol version must not be negative")
	}
	if c.MaxPlayers < 1 {
		return fmt.Errorf("max players must be positive")
	}
	if c.MaxConnections < 1 {
		return fmt.Errorf("max connections must be positive")
	}
	return nil
}
