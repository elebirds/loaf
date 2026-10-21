package credentials

import (
    "context"
    "crypto/rand"
    "encoding/hex"
    "errors"
    "io/fs"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/zalando/go-keyring"
)

func TestKeyringBoundaryAndRedactedErrors(t *testing.T) {
    ctx := context.Background()
    values := map[string]string{}
    s := Keyring{
        get: func(app, key string) (string, error) {
            if app != service {
                t.Fatal(app)
            }
            value, ok := values[key]
            if !ok {
                return "", keyring.ErrNotFound
            }
            return value, nil
        },
        set: func(app, key, value string) error {
            if app != service {
                t.Fatal(app)
            }
            values[key] = value
            return nil
        },
        remove: func(app, key string) error {
            if app != service {
                t.Fatal(app)
            }
            delete(values, key)
            return nil
        },
    }
    if _, err := s.Get(ctx, "new"); !errors.Is(err, fs.ErrNotExist) {
        t.Fatal(err)
    }
    if err := s.Set(ctx, "new", "a private secret\n中文"); err != nil {
        t.Fatal(err)
    }
    if value, err := s.Get(ctx, "new"); err != nil || value != values["new"] {
        t.Fatal("round trip", err)
    }
    if err := s.Set(ctx, "new", strings.Repeat("x", 2501)); err != ErrUnavailable {
        t.Fatal(err)
    }
    if err := s.Delete(ctx, "new"); err != nil || len(values) != 0 {
        t.Fatal(err)
    }
    s.get = func(string, string) (string, error) { return "secret diagnostic", errors.New("secret diagnostic") }
    if value, err := s.Get(ctx, "new"); value != "" || err != ErrUnavailable {
        t.Fatal("unredacted error")
    }
    s.get = func(string, string) (string, error) { return "wrong", nil }
    if err := s.Set(ctx, "new", "expected"); err != ErrUnavailable {
        t.Fatal("unchecked write", err)
    }
    canceled, cancel := context.WithCancel(ctx)
    cancel()
    if err := s.Set(canceled, "new", "value"); !errors.Is(err, context.Canceled) {
        t.Fatal(err)
    }
    if _, err := s.Get(ctx, "../other"); err != ErrUnavailable {
        t.Fatal(err)
    }
}

func TestPrivateFilesAndDeletionIsolation(t *testing.T) {
    s := File{Dir: filepath.Join(t.TempDir(), "credentials")}
    ctx := context.Background()
    if _, err := s.Get(ctx, "missing"); !errors.Is(err, fs.ErrNotExist) {
        t.Fatal(err)
    }
    for _, key := range []string{"one", "two"} {
        if err := s.Set(ctx, key, "plain-session-"+key); err != nil {
            t.Fatal(err)
        }
    }
    for path, perm := range map[string]fs.FileMode{s.Dir: 0700, filepath.Join(s.Dir, "one.json"): 0600} {
        info, err := os.Stat(path)
        if err != nil || info.Mode().Perm() != perm {
            t.Fatal(path, info, err)
        }
    }
    if err := s.Set(ctx, "one", "replaced"); err != nil {
        t.Fatal(err)
    }
    if value, err := s.Get(ctx, "one"); value != "replaced" || err != nil {
        t.Fatal(value, err)
    }
    if err := s.Delete(ctx, "one"); err != nil {
        t.Fatal(err)
    }
    if err := s.Delete(ctx, "one"); err != nil {
        t.Fatal(err)
    }
    if value, err := s.Get(ctx, "two"); value != "plain-session-two" || err != nil {
        t.Fatal(value, err)
    }
    canceled, cancel := context.WithCancel(ctx)
    cancel()
    if err := s.Delete(canceled, "two"); !errors.Is(err, context.Canceled) {
        t.Fatal(err)
    }
}

func TestFileStoreRejectsUnsafePathsWithoutMutation(t *testing.T) {
    for _, kind := range []string{"symlink", "file-permission", "directory-permission", "directory-symlink", "oversize"} {
        t.Run(kind, func(t *testing.T) {
            root := t.TempDir()
            s := File{Dir: filepath.Join(root, "credentials")}
            if err := s.Set(context.Background(), "key", "sentinel"); err != nil {
                t.Fatal(err)
            }
            path := filepath.Join(s.Dir, "key.json")
            switch kind {
            case "symlink":
                other := filepath.Join(root, "other")
                os.WriteFile(other, []byte("sentinel"), 0600)
                os.Remove(path)
                if err := os.Symlink(other, path); err != nil {
                    t.Fatal(err)
                }
            case "file-permission":
                os.Chmod(path, 0644)
            case "directory-permission":
                os.Chmod(s.Dir, 0755)
            case "directory-symlink":
                actual := filepath.Join(root, "actual")
                os.Rename(s.Dir, actual)
                os.Symlink(actual, s.Dir)
            case "oversize":
                os.WriteFile(path, []byte(strings.Repeat("x", 16*1024+1)), 0600)
            }
            if _, err := s.Get(context.Background(), "key"); err != ErrUnavailable {
                t.Fatal(err)
            }
            if err := s.Set(context.Background(), "key", "replacement"); err != ErrUnavailable {
                t.Fatal(err)
            }
            if err := s.Delete(context.Background(), "key"); err != ErrUnavailable {
                t.Fatal(err)
            }
            value, err := os.ReadFile(path)
            if err != nil || strings.Contains(string(value), "replacement") {
                t.Fatal("mutated rejected entry", err)
            }
        })
    }
    s := File{Dir: filepath.Join(t.TempDir(), "credentials")}
    for _, key := range []string{"../escape", "", "/tmp/entry", "a/b"} {
        if err := s.Set(context.Background(), key, "secret"); err != ErrUnavailable {
            t.Fatal(err)
        }
    }
}

func TestNativeKeyringRoundTrip(t *testing.T) {
    if os.Getenv("LOAF_TEST_KEYRING") != "1" {
        t.Skip("explicit disposable native-vault acceptance")
    }
    var nonce [12]byte
    if _, err := rand.Read(nonce[:]); err != nil {
        t.Fatal(err)
    }
    key := "acceptance-" + hex.EncodeToString(nonce[:])
    s := Keyring{}
    t.Cleanup(func() {
        if err := s.Delete(context.Background(), key); err != nil {
            t.Error(err)
        }
    })
    if err := s.Set(context.Background(), key, "Loaf disposable credential-store test"); err != nil {
        t.Fatal(err)
    }
    if value, err := s.Get(context.Background(), key); err != nil || value != "Loaf disposable credential-store test" {
        t.Fatal("native round trip failed", err)
    }
    if err := s.Delete(context.Background(), key); err != nil {
        t.Fatal(err)
    }
    if _, err := s.Get(context.Background(), key); !errors.Is(err, fs.ErrNotExist) {
        t.Fatal("entry retained", err)
    }
}

func TestNativeKeyringWithoutSessionBus(t *testing.T) {
    if os.Getenv("LOAF_TEST_KEYRING_UNAVAILABLE") != "1" {
        t.Skip("explicit isolated missing-session-bus acceptance")
    }
    s := Keyring{}
    ctx := context.Background()
    if value, err := s.Get(ctx, "acceptance-missing-bus"); value != "" || err != ErrUnavailable {
        t.Fatal("missing bus did not produce a redacted store error")
    }
    if err := s.Set(ctx, "acceptance-missing-bus", "disposable fixture secret"); err != ErrUnavailable {
        t.Fatal("missing bus accepted a vault write")
    }
    if err := s.Delete(ctx, "acceptance-missing-bus"); err != ErrUnavailable {
        t.Fatal("missing bus accepted a vault deletion")
    }
}
