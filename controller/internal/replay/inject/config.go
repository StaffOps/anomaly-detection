package inject

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// InjectionConfig is the parsed representation of a YAML injection profile.
// It specifies which series to perturb, the fault type, time window, and
// magnitude. Deterministic given the seed.
type InjectionConfig struct {
	Seed       int64            `yaml:"seed"`
	Injections []InjectionEntry `yaml:"injections"`
}

// InjectionEntry defines a single fault injection targeting one metric series.
type InjectionEntry struct {
	Target    TargetSpec `yaml:"target"`
	Type      FaultType  `yaml:"type"`
	Start     time.Time  `yaml:"start"`
	End       time.Time  `yaml:"end"`
	Magnitude float64    `yaml:"magnitude"`
}

// TargetSpec identifies a metric series by name and optional label matchers.
type TargetSpec struct {
	Metric string            `yaml:"metric"`
	Labels map[string]string `yaml:"labels"`
}

// FaultType enumerates the supported synthetic fault types.
type FaultType string

const (
	FaultSpike   FaultType = "spike"
	FaultRamp    FaultType = "ramp"
	FaultStep    FaultType = "step"
	FaultSilence FaultType = "silence"
)

// LoadConfig reads and parses an injection profile YAML file.
// Returns an error if the file is unreadable or malformed.
func LoadConfig(path string) (*InjectionConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read injection profile: %w", err)
	}
	var cfg InjectionConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse injection profile: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid injection profile: %w", err)
	}
	return &cfg, nil
}

func (c *InjectionConfig) validate() error {
	for i, inj := range c.Injections {
		if inj.Target.Metric == "" {
			return fmt.Errorf("injection[%d]: target.metric is required", i)
		}
		switch inj.Type {
		case FaultSpike, FaultRamp, FaultStep, FaultSilence:
			// valid
		default:
			return fmt.Errorf("injection[%d]: unknown type %q (must be spike|ramp|step|silence)", i, inj.Type)
		}
		if !inj.End.After(inj.Start) {
			return fmt.Errorf("injection[%d]: end must be after start", i)
		}
		if inj.Magnitude <= 0 && inj.Type != FaultSilence {
			return fmt.Errorf("injection[%d]: magnitude must be > 0 for type %s", i, inj.Type)
		}
	}
	return nil
}
