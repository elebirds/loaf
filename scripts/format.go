// Run with go run ./scripts/format.go [-check].
package main

import (
    "bytes"
    "flag"
    "fmt"
    "go/format"
    "go/parser"
    "go/printer"
    "go/token"
    "io/fs"
    "os"
    "path/filepath"
)

func main() {
    check := flag.Bool("check", false, "report files needing formatting without writing")
    flag.Parse()
    failed := false
    err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
        if err != nil {
            return err
        }
        if d.IsDir() && path != "." && (d.Name()[0] == '.' || d.Name() == "dist" || d.Name() == "docs" || d.Name() == "vendor") {
            return filepath.SkipDir
        }
        if d.IsDir() || filepath.Ext(path) != ".go" {
            return nil
        }
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
        if *check {
            fmt.Println(path)
            failed = true
            return nil
        }
        return os.WriteFile(path, result, 0644)
    })
    if err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
    if failed {
        os.Exit(1)
    }
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
