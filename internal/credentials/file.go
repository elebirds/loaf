package credentials

import (
    "context"
    "errors"
    "io"
    "io/fs"
    "os"
    "path/filepath"

    "github.com/elebirds/loaf/internal/atomicfile"
    "github.com/elebirds/loaf/internal/privatefs"
)

// File stores unencrypted credentials in a dedicated private user directory.
// OS permissions protect against other users, not code running as the owner.
type File struct{ Dir string }

func (s File) directory(create bool) error {
    if s.Dir == "" || !filepath.IsAbs(s.Dir) {
        return ErrUnavailable
    }
    if create {
        if err := privatefs.MkdirAll(s.Dir); err != nil {
            return ErrUnavailable
        }
    }
    err := privatefs.Check(s.Dir, true)
    if errors.Is(err, fs.ErrNotExist) {
        return fs.ErrNotExist
    }
    if err != nil {
        return ErrUnavailable
    }
    return nil
}

func (s File) inspect(key string) (fs.FileInfo, error) {
    if err := privatefs.Check(filepath.Join(s.Dir, key+".json"), false); err != nil {
        if errors.Is(err, fs.ErrNotExist) {
            return nil, fs.ErrNotExist
        }
        return nil, ErrUnavailable
    }
    info, err := os.Lstat(filepath.Join(s.Dir, key+".json"))
    if errors.Is(err, fs.ErrNotExist) {
        return nil, fs.ErrNotExist
    }
    if err != nil || !info.Mode().IsRegular() || info.Size() > 16*1024 {
        return nil, ErrUnavailable
    }
    return info, nil
}

// Get refuses symlinks, insecure permissions and oversized entries.
func (s File) Get(ctx context.Context, key string) (string, error) {
    if err := ctx.Err(); err != nil {
        return "", err
    }
    if !keyPattern.MatchString(key) {
        return "", ErrUnavailable
    }
    if err := s.directory(false); err != nil {
        return "", err
    }
    info, err := s.inspect(key)
    if err != nil {
        return "", err
    }
    root, err := os.OpenRoot(s.Dir)
    if err != nil {
        return "", ErrUnavailable
    }
    defer root.Close()
    f, err := root.Open(key + ".json")
    if err != nil {
        return "", ErrUnavailable
    }
    defer f.Close()
    actual, err := f.Stat()
    if err != nil || !os.SameFile(info, actual) {
        return "", ErrUnavailable
    }
    data, err := io.ReadAll(io.LimitReader(f, 16*1024+1))
    if ctx.Err() != nil {
        return "", ctx.Err()
    }
    if err != nil || len(data) > 16*1024 {
        return "", ErrUnavailable
    }
    return string(data), nil
}

// Set atomically replaces one private file; it never repairs unsafe paths.
func (s File) Set(ctx context.Context, key, value string) error {
    if err := ctx.Err(); err != nil {
        return err
    }
    if !keyPattern.MatchString(key) || len(value) > 16*1024 {
        return ErrUnavailable
    }
    if err := s.directory(true); err != nil {
        return err
    }
    if _, err := s.inspect(key); err != nil && !errors.Is(err, fs.ErrNotExist) {
        return err
    }
    if err := atomicfile.Write(filepath.Join(s.Dir, key+".json"), []byte(value)); err != nil {
        return ErrUnavailable
    }
    return nil
}

// Delete touches exactly one Loaf entry, never a directory or another account.
func (s File) Delete(ctx context.Context, key string) error {
    if err := ctx.Err(); err != nil {
        return err
    }
    if !keyPattern.MatchString(key) {
        return ErrUnavailable
    }
    if err := s.directory(false); err != nil {
        if errors.Is(err, fs.ErrNotExist) {
            return nil
        }
        return err
    }
    if _, err := s.inspect(key); err != nil {
        if errors.Is(err, fs.ErrNotExist) {
            return nil
        }
        return err
    }
    if err := privatefs.Remove(filepath.Join(s.Dir, key+".json")); err != nil && !errors.Is(err, fs.ErrNotExist) {
        return ErrUnavailable
    }
    return nil
}
