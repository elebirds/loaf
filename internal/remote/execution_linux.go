package remote

import (
    "errors"
    "io/fs"
    "os"
    "path/filepath"
    "regexp"
    "strings"

    "github.com/elebirds/loaf/internal/wire"
    "golang.org/x/sys/unix"
)

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateExecution(req wire.Request) error {
    if len(req.Args) == 0 || req.Args[0] == "" || strings.ContainsRune(req.Setup, 0) {
        return errors.New("BAD_COMMAND")
    }
    for _, arg := range req.Args {
        if strings.ContainsRune(arg, 0) {
            return errors.New("BAD_COMMAND")
        }
    }
    for key, value := range req.Env {
        if !environmentName.MatchString(key) || strings.ContainsRune(value, 0) {
            return errors.New("BAD_ENV")
        }
    }
    return nil
}

func commandEnvironment(overrides map[string]string) []string {
    env := []string{}
    for _, entry := range os.Environ() {
        key, _, _ := strings.Cut(entry, "=")
        if _, ok := overrides[key]; !ok {
            env = append(env, entry)
        }
    }
    for key, value := range overrides {
        env = append(env, key+"="+value)
    }
    return env
}

// Resolve with the child's PATH, including explicit project overrides. Relative
// PATH components refer to its work directory, not the supervisor's directory.
func executable(name, work string, env map[string]string) (string, error) {
    if strings.ContainsRune(name, '/') {
        return name, nil
    }
    paths, ok := env["PATH"]
    if !ok {
        paths = os.Getenv("PATH")
    }
    for _, dir := range filepath.SplitList(paths) {
        if !filepath.IsAbs(dir) {
            dir = filepath.Join(work, dir)
        }
        candidate := filepath.Join(dir, name)
        info, err := os.Stat(candidate)
        if err == nil && info.Mode().IsRegular() && unix.Access(candidate, unix.X_OK) == nil {
            return candidate, nil
        }
    }
    return "", fs.ErrNotExist
}
