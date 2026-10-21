package remote

import (
    "context"
    "encoding/json"
    "errors"
    "io"
    "os"
    "path/filepath"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/atomicfile"
    "github.com/elebirds/loaf/internal/wire"
    "golang.org/x/sys/unix"
)

func cleanupFile(dir string) string {
    return filepath.Join(filepath.Dir(dir), "."+filepath.Base(dir)+".cleaned.json")
}

func cleanupReceipt(req wire.Request, dir string) (*wire.CleanupReceipt, error) {
    file, err := os.OpenFile(cleanupFile(dir), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
    if errors.Is(err, os.ErrNotExist) {
        return nil, nil
    }
    if err != nil {
        return nil, errors.New("BAD_FORMAT")
    }
    defer file.Close()
    var stat unix.Stat_t
    if unix.Fstat(int(file.Fd()), &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != uint32(os.Geteuid()) {
        return nil, errors.New("BAD_FORMAT")
    }
    var receipt wire.CleanupReceipt
    decoder := json.NewDecoder(io.LimitReader(file, 64*1024))
    decoder.DisallowUnknownFields()
    if decoder.Decode(&receipt) != nil || decoder.Decode(new(any)) != io.EOF || receipt.FormatVersion != 1 || receipt.Root != req.Root || receipt.ProjectID != req.ProjectID || receipt.RunID != req.RunID || !receipt.Summary.Valid() || receipt.Inode == 0 {
        return nil, errors.New("BAD_FORMAT")
    }
    if receipt.Version != wire.Version {
        return nil, errors.New("VERSION_MISMATCH")
    }
    return &receipt, nil
}

func saveCleanup(dir string, receipt *wire.CleanupReceipt) error {
    data, err := json.MarshalIndent(receipt, "", "    ")
    if err != nil || atomicfile.Write(cleanupFile(dir), append(data, '\n')) != nil {
        return errors.New("CLEAN_UNCONFIRMED")
    }
    return nil
}

// lockData protects the availability of a run directory, independently of its
// execution/control locks. Output senders share it; cleanup takes it exclusively.
// Opening the existing directory creates no metadata and never follows a link.
func lockData(dir string, exclusive bool) (*os.File, error) {
    file, err := os.OpenFile(dir, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
    if err != nil {
        if errors.Is(err, os.ErrNotExist) {
            return nil, errors.New("RUN_NOT_FOUND")
        }
        return nil, errors.New("UNSAFE_PATH")
    }
    mode := unix.LOCK_SH
    if exclusive {
        mode = unix.LOCK_EX
    }
    if err := unix.Flock(int(file.Fd()), mode|unix.LOCK_NB); err != nil {
        file.Close()
        if errors.Is(err, unix.EWOULDBLOCK) {
            return nil, errors.New("CLEAN_BUSY")
        }
        return nil, errors.New("CLEAN_UNSAFE")
    }
    return file, nil
}

func cleanupLock(name string, create bool) (*os.File, error) {
    flags := os.O_RDWR | unix.O_NOFOLLOW | unix.O_NONBLOCK
    if create {
        flags |= os.O_CREATE
    }
    file, err := os.OpenFile(name, flags, 0600)
    if !create && errors.Is(err, os.ErrNotExist) {
        return nil, nil
    }
    if err != nil {
        return nil, errors.New("CLEAN_UNSAFE")
    }
    var stat unix.Stat_t
    if unix.Fstat(int(file.Fd()), &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != uint32(os.Geteuid()) {
        file.Close()
        return nil, errors.New("CLEAN_UNSAFE")
    }
    if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
        file.Close()
        if errors.Is(err, unix.EWOULDBLOCK) {
            return nil, errors.New("CLEAN_BUSY")
        }
        return nil, errors.New("CLEAN_UNSAFE")
    }
    return file, nil
}

func closeLock(lock *os.File) {
    if lock != nil {
        lock.Close()
    }
}

// Clean removes only one explicitly identified, owned and confirmed terminal
// run payload. Its small external receipt survives so lost
// confirmation can be retried without widening the deletion scope.
func Clean(ctx context.Context, req wire.Request, dryRun bool) (*wire.CleanupReceipt, error) {
    if req.FormatVersion != wire.FormatVersion {
        return nil, errors.New("BAD_FORMAT")
    }
    if req.Version != wire.Version {
        return nil, errors.New("VERSION_MISMATCH")
    }
    req.Action = "status" // ownership checks must not create runs/.
    dir, err := runPath(req)
    if err != nil {
        return nil, err
    }
    for _, name := range []string{req.Root, filepath.Dir(dir), filepath.Join(req.Root, ".loaf-project")} {
        var stat unix.Stat_t
        if unix.Lstat(name, &stat) != nil || stat.Uid != uint32(os.Geteuid()) {
            return nil, errors.New("CLEAN_UNSAFE")
        }
    }
    receipt, err := cleanupReceipt(req, dir)
    if err != nil {
        return nil, err
    }
    // Reuse the ID claim outside the deletable directory. It serializes
    // cleanup/retry even after the run directory itself has been removed.
    // Inspect existing records before creating any file for a fresh cleanup.
    if receipt == nil {
        if _, err := cleanupRecord(req, dir); err != nil {
            return nil, err
        }
    }
    claim, err := cleanupLock(filepath.Join(filepath.Dir(dir), "."+req.RunID+".claim"), !dryRun)
    if err != nil {
        return receipt, err
    }
    defer closeLock(claim)
    receipt, err = cleanupReceipt(req, dir)
    if err != nil {
        return nil, err
    }
    dataLock, err := lockData(dir, true)
    if err != nil {
        if err.Error() != "RUN_NOT_FOUND" || receipt == nil {
            return receipt, err
        }
        if receipt.CleanedAt != nil || dryRun {
            return receipt, nil
        }
        now := time.Now().UTC()
        receipt.CleanedAt = &now
        return receipt, saveCleanup(dir, receipt)
    }
    defer dataLock.Close()
    if receipt != nil && receipt.CleanedAt != nil {
        return receipt, errors.New("CLEAN_UNSAFE")
    }
    var stat unix.Stat_t
    if unix.Fstat(int(dataLock.Fd()), &stat) != nil || stat.Uid != uint32(os.Geteuid()) {
        return receipt, errors.New("CLEAN_UNSAFE")
    }
    if receipt != nil && (receipt.Inode != stat.Ino || receipt.Device != uint64(stat.Dev)) {
        return receipt, errors.New("CLEAN_UNSAFE")
    }
    if receipt == nil {
        // Validate unknown/damaged records before creating even a control lock.
        // The authoritative record is read again after acquiring lifecycle locks.
        if _, err := cleanupRecord(req, dir); err != nil {
            return receipt, err
        }
        control, err := cleanupLock(filepath.Join(dir, "control.lock"), !dryRun)
        if err != nil {
            return receipt, err
        }
        defer closeLock(control)
        launch, err := cleanupLock(filepath.Join(dir, "launch.lock"), false)
        if err != nil {
            return receipt, err
        }
        defer closeLock(launch)
        owner, err := cleanupLock(filepath.Join(dir, "owner.lock"), false)
        if err != nil {
            return receipt, err
        }
        defer closeLock(owner)
        record, err := cleanupRecord(req, dir)
        if err != nil {
            return receipt, err
        }
        summary := wire.Summary(record)
        // Terminal publication happens just before the supervisor releases its
        // lifetime lock. Wait for that real boundary rather than trusting PID
        // existence or the terminal string alone.
        if record.SupervisorPID > 0 {
            if owner == nil || record.SupervisorStart == "" || record.BootID == "" {
                return nil, errors.New("CLEAN_UNSAFE")
            }
            boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
            if err != nil {
                return nil, errors.New("CLEAN_UNSAFE")
            }
            identity, err := process(record.SupervisorPID)
            if err == nil && identity.uid == uint32(os.Geteuid()) && identity.start == record.SupervisorStart && strings.TrimSpace(string(boot)) == record.BootID && identity.state != "Z" {
                return nil, errors.New("CLEAN_BUSY")
            }
        }
        if record.ChildPID > 0 && groupAlive(record.ChildPID) {
            return nil, errors.New("CLEAN_NOT_TERMINAL")
        }
        receipt = &wire.CleanupReceipt{FormatVersion: 1, Version: wire.Version, Root: req.Root, ProjectID: req.ProjectID, RunID: req.RunID, Summary: summary, Device: uint64(stat.Dev), Inode: stat.Ino}
    }
    if dryRun || receipt.CleanedAt != nil {
        return receipt, nil
    }
    if ctx.Err() != nil {
        return receipt, errors.New("CLEAN_UNCONFIRMED")
    }
    if err := saveCleanup(dir, receipt); err != nil {
        return receipt, err
    }
    root, err := os.OpenRoot(dir)
    if err != nil {
        return receipt, errors.New("CLEAN_UNCONFIRMED")
    }
    defer root.Close()
    // os.Root confines every operation; links are unlinked, never followed.
    // The receipt retains the terminal result if cancellation occurs after
    // run.json is removed; retry still stays bound to this exact inode.
    if err := removeRunTree(ctx, root, "."); err != nil {
        return receipt, errors.New("CLEAN_UNCONFIRMED")
    }
    if err := os.Remove(dir); err != nil {
        return receipt, errors.New("CLEAN_UNCONFIRMED")
    }
    parent, err := os.Open(filepath.Dir(dir))
    if err != nil {
        return receipt, errors.New("CLEAN_UNCONFIRMED")
    }
    err = parent.Sync()
    parent.Close()
    if err != nil {
        return receipt, errors.New("CLEAN_UNCONFIRMED")
    }
    now := time.Now().UTC()
    receipt.CleanedAt = &now
    return receipt, saveCleanup(dir, receipt)
}

func removeRunTree(ctx context.Context, root *os.Root, name string) error {
    if ctx.Err() != nil {
        return ctx.Err()
    }
    info, err := root.Lstat(name)
    if errors.Is(err, os.ErrNotExist) {
        return nil
    }
    if err != nil {
        return err
    }
    if info.IsDir() {
        file, err := root.OpenFile(name, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
        if err != nil {
            return err
        }
        entries, err := file.Readdirnames(-1)
        file.Close()
        if err != nil {
            return err
        }
        for _, entry := range entries {
            if err := removeRunTree(ctx, root, filepath.Join(name, entry)); err != nil {
                return err
            }
        }
    }
    if name == "." {
        return nil
    }
    if ctx.Err() != nil {
        return ctx.Err()
    }
    return root.Remove(name)
}

func cleanedAvailability(req wire.Request, dir string) error {
    receipt, err := cleanupReceipt(req, dir)
    if err != nil {
        return err
    }
    if receipt != nil {
        if receipt.CleanedAt != nil {
            return errors.New("RUN_CLEANED")
        }
        return errors.New("CLEAN_UNCONFIRMED")
    }
    return nil
}

func cleanupRecord(req wire.Request, dir string) (*wire.RunRecord, error) {
    record, err := read(dir)
    if err != nil {
        return nil, err
    }
    if record.Request.FormatVersion != wire.FormatVersion || record.Request.Root != req.Root || record.Request.RunID != req.RunID || record.Request.ProjectID != req.ProjectID {
        return nil, errors.New("BAD_FORMAT")
    }
    var stat unix.Stat_t
    if unix.Lstat(filepath.Join(dir, "run.json"), &stat) != nil || stat.Uid != uint32(os.Geteuid()) {
        return nil, errors.New("CLEAN_UNSAFE")
    }
    if !wire.Summary(record).Valid() {
        return nil, errors.New("CLEAN_NOT_TERMINAL")
    }
    return record, nil
}
