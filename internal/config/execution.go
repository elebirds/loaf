package config

import (
    "time"

    "github.com/elebirds/loaf/internal/fault"
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

// ResolveWait applies individual overrides; waiting never limits remote runtime.
func ResolveWait(layers ...string) (time.Duration, error) {
    var result time.Duration
    for _, value := range layers {
        if value == "" {
            continue
        }
        duration, err := time.ParseDuration(value)
        if err != nil || duration < 0 || duration > 5*time.Minute {
            return 0, fault.New("BAD_CONFIG", "invalid_wait", nil)
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
            return 0, fault.New("BAD_CONFIG", "invalid_tail", nil)
        }
        result = *layer.Tail
    }
    return result, nil
}
