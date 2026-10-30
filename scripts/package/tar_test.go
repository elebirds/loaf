package main

import (
    "archive/tar"
    "compress/gzip"
    "io"
    "os"
    "path/filepath"
    "testing"
)

func TestUnixArchiveExecutablePermissionsDoNotDependOnHost(t *testing.T) {
    directory := t.TempDir()
    if err := os.WriteFile(filepath.Join(directory, "loaf"), []byte("built client"), 0600); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(filepath.Join(directory, "README.md"), []byte("manual"), 0600); err != nil {
        t.Fatal(err)
    }
    output := filepath.Join(t.TempDir(), "package.tar.gz")
    if err := writeTarGZ(output, directory); err != nil {
        t.Fatal(err)
    }
    file, err := os.Open(output)
    if err != nil {
        t.Fatal(err)
    }
    defer file.Close()
    compressed, err := gzip.NewReader(file)
    if err != nil {
        t.Fatal(err)
    }
    defer compressed.Close()
    archive := tar.NewReader(compressed)
    found := map[string]bool{}
    for {
        header, err := archive.Next()
        if err == io.EOF {
            break
        }
        if err != nil {
            t.Fatal(err)
        }
        mode := int64(0644)
        if header.Name == "loaf" {
            mode = 0755
        }
        if header.Mode != mode {
            t.Fatal("target mode depends on host", header.Name, header.Mode)
        }
        data, err := io.ReadAll(archive)
        if err != nil || len(data) == 0 {
            t.Fatal(err)
        }
        found[header.Name] = true
    }
    if !found["loaf"] || !found["README.md"] {
        t.Fatal("incomplete archive", found)
    }
}
