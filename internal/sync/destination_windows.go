package sync

import (
    "errors"
    "fmt"
    "io"
    "runtime"
    "sync"
    "unsafe"

    "github.com/elebirds/loaf/internal/filelock"
    "github.com/elebirds/loaf/internal/privatefs"
    "golang.org/x/sys/windows"
)

type destinationLock struct {
    release chan struct{}
    done    chan struct{}
    once    sync.Once
}

func (l *destinationLock) Close() error {
    l.once.Do(func() { close(l.release); <-l.done })
    return nil
}

// Windows cannot byte-lock a directory. A user-private kernel mutex is keyed by
// its volume/file identity, while an open directory handle prevents replacement.
// The owning thread stays alive until Close; abandoned owners are recoverable.
func lockDestination(path string) (io.Closer, error) {
    name, err := windows.UTF16PtrFromString(path)
    if err != nil {
        return nil, err
    }
    directory, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
        windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
        windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
    if err != nil {
        return nil, err
    }
    var info windows.ByHandleFileInformation
    if err := windows.GetFileInformationByHandle(directory, &info); err != nil {
        windows.CloseHandle(directory)
        return nil, err
    }
    if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
        windows.CloseHandle(directory)
        return nil, privatefs.ErrUnsafe
    }
    key, err := windows.UTF16PtrFromString(fmt.Sprintf(`Global\loaf-pull-%08x-%08x%08x`, info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow))
    if err != nil {
        windows.CloseHandle(directory)
        return nil, err
    }
    descriptor, err := privatefs.SecurityDescriptor()
    if err != nil {
        windows.CloseHandle(directory)
        return nil, err
    }
    lock := &destinationLock{release: make(chan struct{}), done: make(chan struct{})}
    ready := make(chan error, 1)
    go func() {
        runtime.LockOSThread()
        defer runtime.UnlockOSThread()
        defer close(lock.done)
        defer windows.CloseHandle(directory)
        attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}
        mutex, err := windows.CreateMutex(&attributes, false, key)
        if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
            ready <- err
            return
        }
        defer windows.CloseHandle(mutex)
        if err := privatefs.CheckSecurity(mutex, windows.SE_KERNEL_OBJECT); err != nil {
            ready <- err
            return
        }
        status, err := windows.WaitForSingleObject(mutex, 0)
        if err != nil {
            ready <- err
            return
        }
        if status != windows.WAIT_OBJECT_0 && status != windows.WAIT_ABANDONED {
            ready <- filelock.ErrBusy
            return
        }
        defer windows.ReleaseMutex(mutex)
        ready <- nil
        <-lock.release
    }()
    if err := <-ready; err != nil {
        <-lock.done
        return nil, err
    }
    return lock, nil
}
