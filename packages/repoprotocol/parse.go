package repoprotocol

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

func ParseConfigYAML(data []byte) (Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode devplane config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate devplane config: %w", err)
	}
	return cfg, nil
}
