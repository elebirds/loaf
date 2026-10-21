package config

import (
    "context"

    "github.com/elebirds/loaf/internal/atomicfile"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/pelletier/go-toml/v2"
)

// Remove deletes only a connection alias. Project choices and run locators retain
// their original names so removing a default cannot silently select another one.
func (s *Store) Remove(ctx context.Context, name string) error {
    if !ValidName(name) {
        return fault.New("BAD_CONFIG", "invalid_target", nil)
    }
    return locked(ctx, s.ConfigFile, func() error {
        c, err := s.Load()
        if err != nil {
            return err
        }
        if _, ok := c.Targets[name]; !ok {
            return fault.New("BAD_CONFIG", "target_unknown", map[string]any{"Target": name})
        }
        delete(c.Targets, name)
        data, err := toml.Marshal(c)
        if err != nil {
            return err
        }
        return atomicfile.Write(s.ConfigFile, data)
    })
}
