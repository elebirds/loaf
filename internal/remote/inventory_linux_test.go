package remote

import (
    "context"
    "encoding/json"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/wire"
)

func TestInventoryIsBoundedReadOnlyAndRejectsEscapes(t *testing.T) {
    req, work := outputFixture(t)
    req.Depth = 1
    req.Limit = 3
    req.Outputs = append(req.Outputs, "missing")
    writeSource(t, work, "outputs/sub/deep/file", "deep")
    before, _ := os.ReadFile(filepath.Join(filepath.Dir(work), "run.json"))
    inventory, err := Inventory(context.Background(), req)
    if err != nil || len(inventory.Entries) > 3 || inventory.Examined != len(inventory.Entries) || !inventory.Truncated {
        t.Fatal(inventory, err)
    }
    data, _ := json.Marshal(inventory)
    if strings.Contains(string(data), "must-not-leak") || strings.Contains(string(data), "metrics") {
        t.Fatal(string(data))
    }
    after, _ := os.ReadFile(filepath.Join(filepath.Dir(work), "run.json"))
    if string(before) != string(after) {
        t.Fatal("changed record")
    }
    req.Depth = 20
    req.Limit = 1000
    inventory, err = Inventory(context.Background(), req)
    if err != nil || inventory.Truncated || len(inventory.Missing) != 1 || inventory.RootsExamined != 3 {
        t.Fatal(inventory, err)
    }
    if err := os.Symlink("../../run.json", filepath.Join(work, "outputs/escape")); err != nil {
        t.Fatal(err)
    }
    _, err = Inventory(context.Background(), req)
    expectCode(t, err, "UNSAFE_PATH")
    _ = os.Remove(filepath.Join(work, "outputs/escape"))
    req.Outputs = []string{"../escape"}
    _, err = Inventory(context.Background(), req)
    expectCode(t, err, "UNSAFE_PATH")
}
func TestInventoryUnknownMissingAndCleanedDoNotCreateDirectories(t *testing.T) {
    req, _ := outputFixture(t)
    req.Depth = 2
    req.Limit = 20
    _ = os.RemoveAll(filepath.Join(req.Root, "runs"))
    _, err := Inventory(context.Background(), req)
    expectCode(t, err, "RUN_NOT_FOUND")
    if _, err := os.Stat(filepath.Join(req.Root, "runs")); !os.IsNotExist(err) {
        t.Fatal("created runs", err)
    }
    req.Version = "old"
    _, err = Inventory(context.Background(), req)
    expectCode(t, err, "VERSION_MISMATCH")
    req.Version = wire.Version
    req.Limit = 0
    _, err = Inventory(context.Background(), req)
    expectCode(t, err, "BAD_REQUEST")
}
