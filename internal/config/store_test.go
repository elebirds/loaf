package config

import (
    "os"
    "path/filepath"
    "testing"
)

func TestLoadLanguageAndPreserveUnknownFormats(t *testing.T) {
    s := New(t.TempDir(), "", "")
    if _, err := s.Load(); err != nil {
        t.Fatal(err)
    }
    if err := os.MkdirAll(filepath.Dir(s.ConfigFile), 0700); err != nil {
        t.Fatal(err)
    }
    for _, version := range []string{"1", "2"} {
        original := "format_version = " + version + "\nlanguage = 'zh-Hans'\n"
        if err := os.WriteFile(s.ConfigFile, []byte(original), 0600); err != nil {
            t.Fatal(err)
        }
        c, err := s.Load()
        if (err != nil) != (version == "2") || c.Language != "zh-Hans" {
            t.Fatal(c, err)
        }
        after, err := os.ReadFile(s.ConfigFile)
        if err != nil || string(after) != original {
            t.Fatal("modified configuration while reading", err)
        }
    }
}
