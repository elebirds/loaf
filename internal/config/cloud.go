package config

import (
    "context"
    "regexp"

    "github.com/elebirds/loaf/internal/atomicfile"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/pelletier/go-toml/v2"
)

var instanceID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// ValidCloudBinding checks a locator independently of mutable account settings.
func ValidCloudBinding(b cloud.Binding) bool {
    return b.Settings.Validate() == nil && ValidName(b.Ref.Account) && instanceID.MatchString(b.Ref.ID) && b.Ref.Provider == b.Settings.Provider && cloud.ValidProduct(b.Ref.Product)
}

func validateCloud(c cloud.Settings, profiles map[string]cloud.Profile) error {
    if c.CredentialStore != "" && !cloud.ValidCredentialStore(c.CredentialStore) {
        return fault.New("BAD_CONFIG", "cloud_config", nil)
    }
    if c.DefaultAccount != "" && !ValidName(c.DefaultAccount) {
        return fault.New("BAD_CONFIG", "cloud_config", nil)
    }
    if _, err := cloud.Resolve(c.Safety, c.Defaults); err != nil {
        return err
    }
    for name, a := range c.Accounts {
        if !ValidName(name) {
            return fault.New("BAD_CONFIG", "cloud_config", nil)
        }
        if err := a.Validate(); err != nil {
            return err
        }
    }
    for name, p := range profiles {
        if !ValidName(name) {
            return fault.New("BAD_CONFIG", "cloud_config", nil)
        }
        if _, err := cloud.Resolve(cloud.Ceiling{}, p); err != nil {
            return err
        }
    }
    return nil
}

// AddCloudAccount persists only credential references and public identity. Account
// names may be reused, but existing target/run bindings keep their original ref.
func (s *Store) AddCloudAccount(ctx context.Context, name string, a cloud.Account, replace, makeDefault bool) error {
    if !ValidName(name) {
        return fault.New("BAD_CONFIG", "cloud_config", nil)
    }
    if err := a.Validate(); err != nil {
        return err
    }
    return locked(ctx, s.ConfigFile, func() error {
        c, err := s.Load()
        if err != nil {
            return err
        }
        if _, ok := c.Cloud.Accounts[name]; ok && !replace {
            return fault.New("BAD_CONFIG", "target_exists", map[string]any{"Target": name})
        }
        if c.Cloud.Accounts == nil {
            c.Cloud.Accounts = map[string]cloud.Account{}
        }
        c.Cloud.Accounts[name] = a
        if makeDefault {
            c.Cloud.DefaultAccount = name
        }
        data, err := toml.Marshal(c)
        if err != nil {
            return err
        }
        return atomicfile.Write(s.ConfigFile, data)
    })
}

// RemoveCloudAccount removes only the account alias, not frozen bindings.
func (s *Store) RemoveCloudAccount(ctx context.Context, name string) error {
    return locked(ctx, s.ConfigFile, func() error {
        c, err := s.Load()
        if err != nil {
            return err
        }
        if _, ok := c.Cloud.Accounts[name]; !ok {
            return fault.New("BAD_CONFIG", "cloud_account_unknown", nil)
        }
        delete(c.Cloud.Accounts, name)
        if c.Cloud.DefaultAccount == name {
            c.Cloud.DefaultAccount = ""
        }
        data, err := toml.Marshal(c)
        if err != nil {
            return err
        }
        return atomicfile.Write(s.ConfigFile, data)
    })
}

// SaveLogin publishes a verified credential reference only if the account has
// not changed since the login started. It never writes a secret into TOML.
func (s *Store) SaveLogin(ctx context.Context, name string, a cloud.Account, previous *cloud.Account, makeDefault bool) error {
    if !ValidName(name) || a.Validate() != nil {
        return fault.New("BAD_CONFIG", "cloud_config", nil)
    }
    return locked(ctx, s.ConfigFile, func() error {
        cfg, err := s.Load()
        if err != nil {
            return err
        }
        current, exists := cfg.Cloud.Accounts[name]
        if previous == nil && exists || previous != nil && (!exists || current != *previous) {
            return fault.New("CLOUD_BUSY", "cloud_login_changed", nil)
        }
        if cfg.Cloud.Accounts == nil {
            cfg.Cloud.Accounts = map[string]cloud.Account{}
        }
        cfg.Cloud.Accounts[name] = a
        if makeDefault {
            cfg.Cloud.DefaultAccount = name
        }
        data, err := toml.Marshal(cfg)
        if err != nil {
            return err
        }
        return atomicfile.Write(s.ConfigFile, data)
    })
}
