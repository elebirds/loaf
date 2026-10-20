package integration

import (
    "debug/buildinfo"
    "encoding/json"
    "io/fs"
    "os"
    "path/filepath"
    "reflect"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/wire"
)

func TestPackagedMetadataAndDependencyNotices(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    directory := filepath.Dir(os.Getenv("LOAF_BIN"))
    data, err := os.ReadFile(filepath.Join(directory, "BUILD.json"))
    if err != nil {
        t.Fatal(err)
    }
    var manifest struct {
        FormatVersion int    `json:"format_version"`
        Version       string `json:"version"`
        Revision      string `json:"revision"`
        Modified      *bool  `json:"modified"`
        Platform      string `json:"platform"`
        GoVersion     string `json:"go_version"`
        Dependencies  []struct {
            Path, Version string
            Notices       []string
        }   `json:"dependencies"`
    }
    if json.Unmarshal(data, &manifest) != nil || manifest.FormatVersion != 1 || manifest.Version != wire.Version || manifest.Revision == "" || manifest.GoVersion == "" || len(manifest.Dependencies) < 2 {
        t.Fatal(string(data))
    }
    code, version := cli(t, t.TempDir(), "version", "--json")
    var reported struct {
        Data struct {
            Revision  string `json:"revision"`
            Modified  *bool  `json:"modified"`
            OS, Arch  string
            GoVersion string `json:"go_version"`
        } `json:"data"`
    }
    if code != 0 || json.Unmarshal(version, &reported) != nil || reported.Data.Revision != manifest.Revision || reported.Data.GoVersion != manifest.GoVersion || reported.Data.OS+"/"+reported.Data.Arch != manifest.Platform || !reflect.DeepEqual(reported.Data.Modified, manifest.Modified) {
        t.Fatal(string(data), string(version))
    }
    inventory := map[string]string{}
    for _, dependency := range manifest.Dependencies {
        inventory[dependency.Path] = dependency.Version
        if len(dependency.Notices) == 0 {
            t.Fatal("missing license inventory", dependency.Path)
        }
        for _, name := range dependency.Notices {
            if !fs.ValidPath(name) || !strings.HasPrefix(name, "THIRD_PARTY_LICENSES/") {
                t.Fatal("unsafe license location", name)
            }
            data, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(name)))
            if err != nil || len(data) == 0 {
                t.Fatal("missing notice text", name, err)
            }
        }
    }
    info, err := buildinfo.ReadFile(os.Getenv("LOAF_BIN"))
    if err != nil {
        t.Fatal(err)
    }
    for _, module := range info.Deps {
        if inventory[module.Path] != module.Version {
            t.Fatal("linked dependency missing or mismatched", module.Path)
        }
    }
    expected := map[string]bool{"loaf": true, "LICENSE": true, "README.md": true, "README.zh-Hans.md": true, "BUILD.json": true, "THIRD_PARTY_LICENSES": true, "skills": true, "examples": true}
    entries, err := os.ReadDir(directory)
    if err != nil || len(entries) != len(expected) {
        t.Fatal("unexpected package inventory", entries, err)
    }
    for _, entry := range entries {
        if !expected[entry.Name()] {
            t.Fatal("unexpected packaged file", entry.Name())
        }
    }
}
