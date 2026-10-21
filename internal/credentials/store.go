// Package credentials stores only explicitly selected, Loaf-owned secrets.
package credentials

import (
    "context"
    "errors"
    "io/fs"
    "regexp"

    "github.com/zalando/go-keyring"
)

const service = "com.elebirds.loaf"

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// ErrUnavailable deliberately omits OS diagnostics, which can contain secrets.
var ErrUnavailable = errors.New("credential store unavailable")

// Keyring uses the platform vault. Its zero value is ready for use. The native
// API may display an unlock dialog; context cannot dismiss an OS-owned dialog.
type Keyring struct {
    get    func(string, string) (string, error)
    set    func(string, string, string) error
    remove func(string, string) error
}

func storeError(err error) error {
    if errors.Is(err, keyring.ErrNotFound) {
        return fs.ErrNotExist
    }
    if err != nil {
        return ErrUnavailable
    }
    return nil
}

// Get reads one entry, never enumerating other applications' credentials.
func (s Keyring) Get(ctx context.Context, key string) (string, error) {
    if err := ctx.Err(); err != nil {
        return "", err
    }
    if !keyPattern.MatchString(key) {
        return "", ErrUnavailable
    }
    get := s.get
    if get == nil {
        get = keyring.Get
    }
    value, err := get(service, key)
    if ctx.Err() != nil {
        return "", ctx.Err()
    }
    if err != nil {
        return "", storeError(err)
    }
    if len(value) > 16*1024 {
        return "", ErrUnavailable
    }
    return value, nil
}

// Set writes through the native backend, not secret command-line arguments.
// The bound fits both Windows Credential Manager and macOS security's stdin.
func (s Keyring) Set(ctx context.Context, key, value string) error {
    if err := ctx.Err(); err != nil {
        return err
    }
    if !keyPattern.MatchString(key) || len(value) > 2500 {
        return ErrUnavailable
    }
    set := s.set
    if set == nil {
        set = keyring.Set
    }
    if err := set(service, key, value); err != nil {
        return storeError(err)
    }
    // macOS security -i can exit zero after an individual command fails.
    actual, err := s.Get(ctx, key)
    if err != nil {
        return err
    }
    if actual != value {
        return ErrUnavailable
    }
    return nil
}

// Delete removes one selected entry. Missing entries are already logged out.
func (s Keyring) Delete(ctx context.Context, key string) error {
    if err := ctx.Err(); err != nil {
        return err
    }
    if !keyPattern.MatchString(key) {
        return ErrUnavailable
    }
    remove := s.remove
    if remove == nil {
        remove = keyring.Delete
    }
    err := storeError(remove(service, key))
    if ctx.Err() != nil {
        return ctx.Err()
    }
    if errors.Is(err, fs.ErrNotExist) {
        return nil
    }
    return err
}
