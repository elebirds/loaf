// Run with go run ./scripts/format.go [-check] -- [files or directories...].
package main

import (
    "bytes"
    "flag"
    "fmt"
    "go/format"
    "go/parser"
    "go/printer"
    "go/token"
    "io"
    "io/fs"
    "os"
    "path/filepath"
)

func main() {
    check := flag.Bool("check", false, "report files needing formatting without writing")
    flag.Parse()
    failed, err := formatPaths(flag.Args(), *check, os.Stdout)
    if err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
    if failed {
        os.Exit(1)
    }
}

func formatPaths(paths []string, check bool, output io.Writer) (bool, error) {
    if len(paths) == 0 {
        paths = []string{"."}
    }
    failed := false
    seen := map[string]bool{}
    visit := func(path string, d fs.DirEntry, err error) error {
        if err != nil {
            return err
        }
        if d.IsDir() && path != "." && (d.Name()[0] == '.' || d.Name() == "dist" || d.Name() == "docs" || d.Name() == "vendor") {
            return filepath.SkipDir
        }
        if d.IsDir() || filepath.Ext(path) != ".go" {
            return nil
        }
        absolute, err := filepath.Abs(path)
        if err != nil {
            return err
        }
        if seen[absolute] {
            return nil
        }
        seen[absolute] = true
        source, err := os.ReadFile(path)
        if err != nil {
            return err
        }
        result, err := spaceFormat(path, source)
        if err != nil {
            return err
        }
        if bytes.Equal(source, result) {
            return nil
        }
        if check {
            failed = true
            _, err := fmt.Fprintln(output, path)
            return err
        }
        return os.WriteFile(path, result, 0644)
    }
    for _, path := range paths {
        if err := filepath.WalkDir(filepath.Clean(path), visit); err != nil {
            return failed, err
        }
    }
    return failed, nil
}

func spaceFormat(name string, source []byte) ([]byte, error) {
    canonical, err := format.Source(source)
    if err != nil {
        return nil, err
    }
    fset := token.NewFileSet()
    file, err := parser.ParseFile(fset, name, canonical, parser.ParseComments)
    if err != nil {
        return nil, err
    }
    var result bytes.Buffer
    cfg := printer.Config{Mode: printer.UseSpaces, Tabwidth: 4}
    if err := cfg.Fprint(&result, fset, file); err != nil {
        return nil, err
    }
    return result.Bytes(), nil
}
