// Command package collects the notices and build metadata for generated binaries.
// scripts/build.sh remains the sole build and archive entry point.
package main

import (
    "crypto/sha256"
    "debug/buildinfo"
    "encoding/hex"
    "encoding/json"
    "errors"
    "flag"
    "fmt"
    "io"
    "os"
    "os/exec"
    "path/filepath"
    "runtime"
    "runtime/debug"
    "sort"
    "strconv"
    "strings"
)

type dependency struct {
    Path    string   `json:"path"`
    Version string   `json:"version"`
    Notices []string `json:"notices"`
}

type manifest struct {
    FormatVersion int          `json:"format_version"`
    Version       string       `json:"version"`
    Revision      string       `json:"revision"`
    Modified      *bool        `json:"modified"`
    Platform      string       `json:"platform"`
    GoVersion     string       `json:"go_version"`
    Dependencies  []dependency `json:"dependencies"`
}

func main() {
    out := flag.String("out", "", "package staging directory")
    version := flag.String("version", "", "software version")
    checksums := flag.String("checksums", "", "write SHA-256 download checksums instead of package metadata")
    zipPath := flag.String("zip", "", "collect one already built staging directory into a ZIP archive")
    tarPath := flag.String("tar-gz", "", "collect one already built staging directory into a tar.gz archive")
    flag.Parse()
    var err error
    if *tarPath != "" && len(flag.Args()) == 1 {
        err = writeTarGZ(*tarPath, flag.Arg(0))
    } else if *zipPath != "" && len(flag.Args()) == 1 {
        err = writeZIP(*zipPath, flag.Arg(0))
    } else if *checksums != "" {
        err = writeChecksums(*checksums, flag.Args())
    } else if *out == "" || *version == "" || len(flag.Args()) == 0 {
        err = errors.New("provide -out, -version, client binary and embedded runner paths")
    } else {
        err = collect(*out, *version, flag.Args())
    }
    if err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}

func collect(out, version string, binaries []string) error {
    modules := map[string]string{}
    var client *debug.BuildInfo
    for i, binary := range binaries {
        info, err := buildinfo.ReadFile(binary)
        if err != nil {
            return fmt.Errorf("read build metadata %s: %w", binary, err)
        }
        if i == 0 {
            client = info
        }
        for _, module := range info.Deps {
            if module.Replace != nil {
                return fmt.Errorf("packaging replaced dependency %s is not supported", module.Path)
            }
            if previous, ok := modules[module.Path]; ok && previous != module.Version {
                return fmt.Errorf("client and runner dependency versions differ: %s", module.Path)
            }
            modules[module.Path] = module.Version
        }
    }
    result := manifest{FormatVersion: 1, Version: version, Revision: "unknown", GoVersion: client.GoVersion, Dependencies: []dependency{}}
    settings := map[string]string{}
    for _, setting := range client.Settings {
        settings[setting.Key] = setting.Value
    }
    result.Platform = settings["GOOS"] + "/" + settings["GOARCH"]
    if revision := settings["vcs.revision"]; revision != "" {
        result.Revision = revision
    }
    if modified, err := strconv.ParseBool(settings["vcs.modified"]); err == nil {
        result.Modified = &modified
    }
    notices, err := copyNotices(runtime.GOROOT(), filepath.Join(out, "THIRD_PARTY_LICENSES", "go"))
    if err != nil {
        return err
    }
    result.Dependencies = append(result.Dependencies, dependency{"Go standard library", client.GoVersion, noticePaths("go", notices)})
    paths := make([]string, 0, len(modules))
    for path := range modules {
        paths = append(paths, path)
    }
    sort.Strings(paths)
    if len(paths) > 0 {
        cmd := exec.Command("go", append([]string{"list", "-m", "-json"}, paths...)...)
        data, err := cmd.Output()
        if err != nil {
            return fmt.Errorf("resolve dependency notice directories: %w", err)
        }
        decoder := json.NewDecoder(strings.NewReader(string(data)))
        found := map[string]bool{}
        for {
            var module struct {
                Path, Version, Dir string
                Main               bool
            }
            err := decoder.Decode(&module)
            if errors.Is(err, io.EOF) {
                break
            }
            if err != nil {
                return err
            }
            builtVersion := modules[module.Path]
            if module.Dir == "" || !sourceVersionMatches(module.Path, builtVersion, module.Version, module.Main) {
                return fmt.Errorf("dependency source unavailable or mismatched: %s", module.Path)
            }
            found[module.Path] = true
            name := module.Path + "@" + builtVersion
            notices, err := copyNotices(module.Dir, filepath.Join(out, "THIRD_PARTY_LICENSES", filepath.FromSlash(name)))
            if err != nil {
                return fmt.Errorf("%s: %w", module.Path, err)
            }
            result.Dependencies = append(result.Dependencies, dependency{module.Path, builtVersion, noticePaths(name, notices)})
        }
        if len(found) != len(paths) {
            return errors.New("dependency notice inventory is incomplete")
        }
    }
    data, err := json.MarshalIndent(result, "", "    ")
    if err != nil {
        return err
    }
    return os.WriteFile(filepath.Join(out, "BUILD.json"), append(data, '\n'), 0644)
}

// sourceVersionMatches retains the binary's actual dependency identity. Go marks
// a linked workspace main module as (devel), while go list leaves Version empty.
// Only our independently versioned, source-distributed delivery SDK uses that
// development path; arbitrary replaced or unversioned dependencies stay rejected.
func sourceVersionMatches(path, built, resolved string, main bool) bool {
    if built != "" && built == resolved {
        return true
    }
    return path == "github.com/elebirds/loaf/sdk/agentdelivery" && built == "(devel)" && resolved == "" && main
}

func noticePaths(module string, names []string) []string {
    result := make([]string, len(names))
    for i, name := range names {
        result[i] = "THIRD_PARTY_LICENSES/" + module + "/" + name
    }
    return result
}

func copyNotices(source, destination string) ([]string, error) {
    entries, err := os.ReadDir(source)
    if err != nil {
        return nil, err
    }
    var names []string
    licensed := false
    for _, entry := range entries {
        name := strings.ToUpper(entry.Name())
        license := strings.HasPrefix(name, "LICENSE") || strings.HasPrefix(name, "COPYING")
        if entry.Type().IsRegular() && (license || strings.HasPrefix(name, "NOTICE") || name == "PATENTS") {
            names = append(names, entry.Name())
            licensed = licensed || license
        }
    }
    if !licensed {
        return nil, errors.New("no root license text found")
    }
    if err := os.MkdirAll(destination, 0755); err != nil {
        return nil, err
    }
    for _, name := range names {
        data, err := os.ReadFile(filepath.Join(source, name))
        if err != nil {
            return nil, err
        }
        if err := os.WriteFile(filepath.Join(destination, name), data, 0644); err != nil {
            return nil, err
        }
    }
    return names, nil
}

func writeChecksums(destination string, files []string) error {
    if len(files) == 0 {
        return errors.New("no archives selected for download checksums")
    }
    sort.Strings(files)
    var result strings.Builder
    for _, path := range files {
        file, err := os.Open(path)
        if err != nil {
            return err
        }
        hash := sha256.New()
        _, copyErr := io.Copy(hash, file)
        closeErr := file.Close()
        if copyErr != nil || closeErr != nil {
            return errors.Join(copyErr, closeErr)
        }
        fmt.Fprintf(&result, "%s  %s\n", hex.EncodeToString(hash.Sum(nil)), filepath.Base(path))
    }
    return os.WriteFile(destination, []byte(result.String()), 0644)
}
