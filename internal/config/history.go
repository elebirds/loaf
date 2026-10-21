package config

import "github.com/elebirds/loaf/internal/fault"

// PSSettings controls the number of eligible recent locators queried, not the
// number of status matches returned. A missing value retains the previous scope.
type PSSettings struct {
    Limit *int `toml:"limit,omitempty"`
}

// ResolvePSLimit applies scalar overrides with the existing default of 20.
// Zero remains invalid; it must not silently remove the bounded-query guarantee.
func ResolvePSLimit(layers ...PSSettings) (int, error) {
    limit := 20
    for _, layer := range layers {
        if layer.Limit == nil {
            continue
        }
        if *layer.Limit < 1 || *layer.Limit > 1000 {
            return 0, fault.New("BAD_CONFIG", "ps_invalid_limit", nil)
        }
        limit = *layer.Limit
    }
    return limit, nil
}
