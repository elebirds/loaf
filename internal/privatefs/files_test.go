package privatefs

import (
    "os"
    "path/filepath"
    "testing"
)

func TestPrivateCreationAndReplacement(t *testing.T) {
    directory := filepath.Join(t.TempDir(), "private", "state")
    if err := MkdirAll(directory); err != nil {
        t.Fatal(err)
    }
    if err := Check(directory, true); err != nil {
        t.Fatal(err)
    }
    destination := filepath.Join(directory, "state 中文.json")
    for _, value := range []string{"first", "replacement"} {
        file, err := CreateTemp(directory)
        if err != nil {
            t.Fatal(err)
        }
        if _, err = file.WriteString(value); err != nil {
            t.Fatal(err)
        }
        if err := file.Sync(); err != nil {
            t.Fatal(err)
        }
        file.Close()
        if err := Replace(file.Name(), destination); err != nil {
            t.Fatal(err)
        }
        if err := Check(destination, false); err != nil {
            t.Fatal(err)
        }
        data, err := os.ReadFile(destination)
        if err != nil || string(data) != value {
            t.Fatal(string(data), err)
        }
    }
    if _, err := Create(destination); !os.IsExist(err) {
        t.Fatal("exclusive creation replaced an existing object", err)
    }
    if err := Remove(destination); err != nil {
        t.Fatal(err)
    }
    if _, err := os.Lstat(destination); !os.IsNotExist(err) {
        t.Fatal(err)
    }
}

func TestRejectUnexpectedObjectAndPreserveExistingData(t *testing.T) {
    dir := filepath.Join(t.TempDir(), "private")
    if err := MkdirAll(dir); err != nil {
        t.Fatal(err)
    }
    if _, err := Open(dir, true); err == nil {
        t.Fatal("opened directory as private file")
    }
    file, err := Create(filepath.Join(dir, "file"))
    if err != nil {
        t.Fatal(err)
    }
    file.Close()
    if err := MkdirAll(file.Name()); err == nil {
        t.Fatal("file accepted as a directory")
    }
    link := filepath.Join(dir, "link")
    if err := os.Symlink(file.Name(), link); err != nil {
        t.Skipf("native symlink creation unavailable: %v", err)
    }
    if err := Check(link, false); err == nil {
        t.Fatal("private symlink accepted")
    }
    if _, err := Open(link, true); err == nil {
        t.Fatal("private symlink followed")
    }
}
