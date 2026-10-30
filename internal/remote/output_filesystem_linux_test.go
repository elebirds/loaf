package remote

import (
    "context"
    "os"
    "path/filepath"
    "testing"

    "github.com/elebirds/loaf/internal/wire"
)

func TestWindowsOutputConstraintIsTransientAndRevalidated(t *testing.T) {
    for _, bad := range []string{"CON.txt", "a.", "a:b", "中文?", "UPPER.txt"} {
        t.Run(bad, func(t *testing.T) {
            req, work := outputFixture(t)
            writeSource(t, work, "outputs/upper.txt", "lower")
            writeSource(t, work, "outputs/"+bad, "bad")
            req.DestinationFS = &wire.OutputFilesystem{Name: "windows-ntfs"}
            _, err := Outputs(context.Background(), req)
            expectCode(t, err, "UNSAFE_PATH")
            req.DestinationFS = nil
            if _, err := Outputs(context.Background(), req); err != nil {
                t.Fatal("Unix names changed", err)
            }
        })
    }
    req, work := outputFixture(t)
    req.DestinationFS = &wire.OutputFilesystem{Name: "windows-ntfs"}
    selected, err := Outputs(context.Background(), req)
    if err != nil || len(selected.Names) == 0 || selected.Namespace == "" {
        t.Fatal(selected, err)
    }
    req.DestinationFS.Namespace = selected.Namespace
    writeSource(t, work, "outputs/new.txt", "later")
    if _, err := Outputs(context.Background(), req); err == nil {
        t.Fatal("accepted changed namespace")
    }
    req.DestinationFS.Namespace = ""
    if err := os.Symlink("new.txt", filepath.Join(work, "outputs/link")); err != nil {
        t.Fatal(err)
    }
    if _, err := Outputs(context.Background(), req); err == nil {
        t.Fatal("accepted unsupported links")
    }
    req.DestinationFS.Symlinks = true
    if _, err := Outputs(context.Background(), req); err != nil {
        t.Fatal(err)
    }
}
