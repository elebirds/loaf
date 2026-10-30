package remote

import (
    "context"
    "errors"
    "os"
    "path/filepath"
    "syscall"
    "time"

    "github.com/elebirds/loaf/internal/privatefs"
    remotestate "github.com/elebirds/loaf/internal/state/remote"
    "github.com/elebirds/loaf/internal/wire"
    "golang.org/x/sys/unix"
)

// Abandon fences one preparation before deleting its private payload. A
// dispatched supervisor is never reclaimed, even before it has published a PID.
func Abandon(ctx context.Context, req wire.Request, dry bool) (*wire.AbandonReceipt, error) {
    if req.FormatVersion != wire.FormatVersion {
        return nil, errors.New("BAD_FORMAT")
    }
    if req.Version != wire.Version {
        return nil, errors.New("VERSION_MISMATCH")
    }
    if ctx.Err() != nil {
        return nil, errors.New("ABANDON_CANCELED")
    }
    if !identifier.MatchString(req.ProjectID) || !identifier.MatchString(req.RunID) || req.QueueRequestID != "" {
        return nil, errors.New("BAD_REQUEST")
    }
    if err := abandonRoot(req.Root); err != nil {
        return nil, err
    }
    if err := abandonQueueGuard(req.RunID); err != nil {
        return nil, err
    }
    receipt := &wire.AbandonReceipt{FormatVersion: 1, Version: wire.Version, Root: req.Root, ProjectID: req.ProjectID, RunID: req.RunID}
    if _, err := os.Lstat(remotestate.Path(req.Root)); errors.Is(err, os.ErrNotExist) {
        entries, readErr := os.ReadDir(req.Root)
        if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
            return nil, errors.New("ABANDON_UNSAFE")
        }
        if len(entries) != 0 {
            return nil, errors.New("PROJECT_UNOWNED")
        }
        if dry {
            return receipt, nil
        }
        // A missing directory alone is not no-start proof: establish ownership
        // and the current database so late receivers observe the same fence.
        if _, err := initProject(req.Root, req.ProjectID); err != nil {
            return nil, err
        }
    } else if err != nil {
        return nil, errors.New("ABANDON_UNSAFE")
    }
    state, err := remotestate.Open(req.Root, false)
    if err != nil {
        return nil, err
    }
    defer state.Close()
    if err := state.VerifyProject(req.ProjectID); err != nil {
        return nil, err
    }
    runs := filepath.Join(req.Root, "runs")
    before, err := state.Abandonment(req.RunID)
    if err != nil {
        return nil, err
    }
    if before == nil {
        info, statErr := os.Lstat(filepath.Join(runs, req.RunID))
        if statErr == nil && !info.IsDir() {
            return nil, errors.New("ABANDON_UNSAFE")
        }
        if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
            return nil, errors.New("ABANDON_UNSAFE")
        }
        if err := abandonRecord(state, req, statErr == nil); err != nil {
            return nil, err
        }
    }
    if !dry {
        if err := os.Mkdir(runs, 0700); err != nil && !errors.Is(err, os.ErrExist) {
            return nil, errors.New("WRITE_FAILED")
        }
    }
    if info, err := os.Lstat(runs); err == nil && !info.IsDir() {
        return nil, errors.New("ABANDON_UNSAFE")
    } else if err != nil && !errors.Is(err, os.ErrNotExist) {
        return nil, errors.New("ABANDON_UNSAFE")
    }
    for _, name := range []string{req.Root, runs, remotestate.Path(req.Root)} {
        var stat unix.Stat_t
        if err := unix.Lstat(name, &stat); err != nil {
            if name == runs && dry && errors.Is(err, os.ErrNotExist) {
                continue
            }
            return nil, errors.New("ABANDON_UNSAFE")
        }
        if stat.Uid != uint32(os.Geteuid()) {
            return nil, errors.New("ABANDON_UNSAFE")
        }
    }
    // Match start's external claim -> project ordering. All later locks are
    // nonblocking, so an active receiver or supervisor wins without disruption.
    claim, err := cleanupLock(filepath.Join(runs, "."+req.RunID+".claim"), !dry)
    if err != nil {
        return nil, abandonLockError(err)
    }
    defer closeLock(claim)
    project, err := cleanupLock(filepath.Join(req.Root, ".loaf-project.lock"), !dry)
    if err != nil {
        return nil, abandonLockError(err)
    }
    defer closeLock(project)
    if err := abandonQueueGuard(req.RunID); err != nil {
        return nil, err
    }
    existing, err := state.Abandonment(req.RunID)
    if err != nil {
        return nil, err
    }
    if existing != nil {
        if existing.Root != req.Root || existing.ProjectID != req.ProjectID {
            return nil, errors.New("BAD_FORMAT")
        }
        receipt = existing
    }
    dir := filepath.Join(runs, req.RunID)
    data, err := lockData(dir, true)
    if err != nil && err.Error() != "RUN_NOT_FOUND" {
        return receipt, abandonLockError(err)
    }
    defer closeLock(data)
    var stat unix.Stat_t
    if data != nil {
        if unix.Fstat(int(data.Fd()), &stat) != nil || stat.Uid != uint32(os.Geteuid()) {
            return receipt, errors.New("ABANDON_UNSAFE")
        }
        if existing != nil && (!existing.PayloadPresent || existing.Inode != stat.Ino || existing.Device != uint64(stat.Dev) || existing.CleanedAt != nil) {
            return receipt, errors.New("ABANDON_UNSAFE")
        }
        if existing == nil {
            receipt.PayloadPresent, receipt.Device, receipt.Inode = true, uint64(stat.Dev), stat.Ino
        }
    }
    writer := state
    if !dry {
        writer, err = remotestate.Open(req.Root, true)
        if err != nil {
            return receipt, err
        }
        defer writer.Close()
    }
    if existing == nil {
        if err := abandonRecord(state, req, data != nil); err != nil {
            return receipt, err
        }
    }
    if data != nil {
        for _, name := range []string{"control.lock", "launch.lock", "owner.lock"} {
            lock, err := cleanupLock(filepath.Join(dir, name), !dry && name == "control.lock")
            if err != nil {
                return receipt, abandonLockError(err)
            }
            defer closeLock(lock)
        }
    }
    if existing == nil {
        if err := abandonRecord(state, req, data != nil); err != nil {
            return receipt, err
        }
        if dry {
            return receipt, nil
        }
        if ctx.Err() != nil {
            return receipt, errors.New("ABANDON_CANCELED")
        }
        // A newly initialized root must itself survive a crash, otherwise a
        // delayed receiver could initialize it again without this ID's fence.
        if err := privatefs.SyncAncestors(remotestate.Path(req.Root), "/"); err != nil {
            return receipt, errors.New("WRITE_FAILED")
        }
        now := time.Now().UTC()
        receipt.NoStartAt = &now
        if err := writer.SaveAbandonment(receipt); err != nil {
            receipt.NoStartAt = nil
            return receipt, err
        }
    }
    if dry || receipt.CleanedAt != nil {
        return receipt, nil
    }
    if ctx.Err() != nil {
        return receipt, errors.New("ABANDON_UNCONFIRMED")
    }
    if data != nil {
        root, err := os.OpenRoot(dir)
        if err != nil {
            return receipt, errors.New("ABANDON_UNCONFIRMED")
        }
        info, statErr := root.Stat(".")
        if statErr != nil {
            root.Close()
            return receipt, errors.New("ABANDON_UNSAFE")
        }
        opened, ok := info.Sys().(*syscall.Stat_t)
        if !ok || opened.Ino != stat.Ino || uint64(opened.Dev) != uint64(stat.Dev) {
            root.Close()
            return receipt, errors.New("ABANDON_UNSAFE")
        }
        err = removeRunTree(ctx, root, ".")
        root.Close()
        if err != nil || os.Remove(dir) != nil {
            return receipt, errors.New("ABANDON_UNCONFIRMED")
        }
    }
    // Retry may find that removal succeeded but its parent flush or final SQL
    // checkpoint did not. Flush that same directory before confirming deletion.
    if receipt.PayloadPresent {
        parent, err := os.Open(runs)
        if err != nil {
            return receipt, errors.New("ABANDON_UNCONFIRMED")
        }
        err = parent.Sync()
        parent.Close()
        if err != nil {
            return receipt, errors.New("ABANDON_UNCONFIRMED")
        }
    }
    now := time.Now().UTC()
    if err := writer.CompleteAbandonment(req.RunID, now); err != nil {
        return receipt, err
    }
    receipt.CleanedAt = &now
    return receipt, nil
}

func abandonRoot(root string) error {
    home, err := os.UserHomeDir()
    if err != nil || !filepath.IsAbs(root) || root != filepath.Clean(root) || root == "/" || root == home {
        return errors.New("UNSAFE_PATH")
    }
    for dir := root; dir != "/"; dir = filepath.Dir(dir) {
        info, err := os.Lstat(dir)
        if err == nil && !info.IsDir() {
            return errors.New("UNSAFE_PATH")
        }
        if err != nil && !errors.Is(err, os.ErrNotExist) {
            return errors.New("UNSAFE_PATH")
        }
    }
    return nil
}

func abandonRecord(state *remotestate.Store, req wire.Request, payload bool) error {
    record, err := state.Run(req.RunID)
    if err != nil {
        if err.Error() != "RUN_NOT_FOUND" {
            return err
        }
        claimed, err := state.HasClaim(req.RunID)
        if err != nil {
            return err
        }
        if claimed || payload {
            return errors.New("ABANDON_UNSAFE")
        }
        return nil
    }
    if record.Request.FormatVersion != wire.FormatVersion || record.Request.Root != req.Root || record.Request.ProjectID != req.ProjectID || record.Request.RunID != req.RunID {
        return errors.New("BAD_FORMAT")
    }
    if record.Request.QueueRequestID != "" {
        return errors.New("ABANDON_USE_QUEUE")
    }
    if (record.Phase != "copying" && record.Phase != "prepared") || record.SupervisorPID != 0 || record.ChildPID != 0 || record.Status != "starting" && !(record.Phase == "copying" && record.Status == "failed") {
        return errors.New("ABANDON_NOT_PREPARED")
    }
    return nil
}

func abandonQueueGuard(id string) error {
    root, err := QueueDirectory()
    if err != nil {
        return err
    }
    if _, err := os.Lstat(filepath.Join(root, "claims", id)); err == nil {
        return errors.New("ABANDON_USE_QUEUE")
    } else if !errors.Is(err, os.ErrNotExist) {
        return errors.New("ABANDON_UNSAFE")
    }
    return nil
}

func abandonLockError(err error) error {
    if err.Error() == "CLEAN_BUSY" {
        return errors.New("ABANDON_BUSY")
    }
    return errors.New("ABANDON_UNSAFE")
}

func preparationAvailability(req wire.Request) error {
    state, err := remotestate.Open(req.Root, false)
    if err != nil {
        return err
    }
    defer state.Close()
    if err := state.VerifyProject(req.ProjectID); err != nil {
        return err
    }
    r, err := state.Abandonment(req.RunID)
    if err != nil {
        return err
    }
    if r != nil {
        return errors.New("RUN_ABANDONED")
    }
    return nil
}
