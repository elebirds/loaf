package main

import (
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestCopyNoticesPreservesRequiredTextsAndRejectsMissingLicense(t *testing.T) {
    source, destination := t.TempDir(), t.TempDir()
    for _, name := range []string{"LICENSE.txt", "NOTICE", "PATENTS", "README.md"} {
        if err := os.WriteFile(filepath.Join(source, name), []byte(name+" original text\n"), 0600); err != nil {
            t.Fatal(err)
        }
    }
    names, err := copyNotices(source, destination)
    if err != nil || strings.Join(names, ",") != "LICENSE.txt,NOTICE,PATENTS" {
        t.Fatal(names, err)
    }
    for _, name := range names {
        data, err := os.ReadFile(filepath.Join(destination, name))
        if err != nil || string(data) != name+" original text\n" {
            t.Fatal(string(data), err)
        }
    }
    if _, err := copyNotices(t.TempDir(), destination); err == nil {
        t.Fatal("silently omitted a dependency license")
    }
}

func TestDownloadChecksumInventoryIsExplicit(t *testing.T) {
    root := t.TempDir()
    archive := filepath.Join(root, "loaf_example.tar.gz")
    if err := os.WriteFile(archive, []byte("abc"), 0600); err != nil {
        t.Fatal(err)
    }
    checksums := filepath.Join(root, "checksums.txt")
    if err := writeChecksums(checksums, []string{archive}); err != nil {
        t.Fatal(err)
    }
    data, err := os.ReadFile(checksums)
    if err != nil || string(data) != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad  loaf_example.tar.gz\n" {
        t.Fatal(string(data), err)
    }
    if err := writeChecksums(checksums, nil); err == nil {
        t.Fatal("wrote a misleading empty download inventory")
    }
}
