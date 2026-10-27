package config

import (
    "time"

    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/wire"
)

// RunPreferences contains personal waiting behavior, not executable commands.
type RunPreferences struct {
    Wait string `toml:"wait,omitempty"`
}

// RunSettings describes explicit project execution and optional waiting.
type RunSettings struct {
    Command string            `toml:"command,omitempty"`
    Setup   string            `toml:"setup,omitempty"`
    Env     map[string]string `toml:"env,omitempty"`
    Wait    string            `toml:"wait,omitempty"`
}

// LogSettings controls bounded initial log reads. Zero means no initial lines.
type LogSettings struct {
    Tail *int `toml:"tail,omitempty"`
}

// StopSettings controls the TERM-to-KILL grace, independently of observation.
type StopSettings struct {
    Grace string `toml:"grace,omitempty"`
}

// ResolveStopGrace retains absence so legacy runners can use their own default.
// An explicit zero is a request to escalate without a grace interval.
func ResolveStopGrace(layers ...StopSettings) (*time.Duration, error) {
    var result *time.Duration
    for _, layer := range layers {
        if layer.Grace == "" {
            continue
        }
        duration, err := time.ParseDuration(layer.Grace)
        if err != nil || duration < 0 || duration > wire.MaxStopGrace {
            reason := "out_of_range"
            if err != nil {
                reason = "invalid_type"
            }
            return nil, fault.Invalid("--grace", reason, "invalid_stop_grace")
        }
        result = &duration
    }
    return result, nil
}

// ResolveWait applies individual overrides; waiting never limits remote runtime.
func ResolveWait(layers ...string) (time.Duration, error) {
    var result time.Duration
    for _, value := range layers {
        if value == "" {
            continue
        }
        duration, err := time.ParseDuration(value)
        if err != nil || duration < 0 || duration > 5*time.Minute {
            reason := "out_of_range"
            if err != nil {
                reason = "invalid_type"
            }
            return 0, fault.Invalid("--wait", reason, "invalid_wait")
        }
        result = duration
    }
    return result, nil
}

// ResolveTail applies scalar precedence with a default of 100 lines.
func ResolveTail(layers ...LogSettings) (int, error) {
    result := 100
    for _, layer := range layers {
        if layer.Tail == nil {
            continue
        }
        if *layer.Tail < 0 || *layer.Tail > 10000 {
            return 0, fault.Invalid("--tail", "out_of_range", "invalid_tail")
        }
        result = *layer.Tail
    }
    return result, nil
}
