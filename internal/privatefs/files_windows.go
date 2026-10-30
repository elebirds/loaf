package privatefs

import (
    "crypto/rand"
    "encoding/hex"
    "os"
    "unsafe"

    "golang.org/x/sys/windows"
)

// SecurityDescriptor limits newly created objects to the current user and the
// Windows system/administrator principals. Inheritable ACEs protect SQLite's
// own WAL/SHM creation as well as files created by native child processes.
func SecurityDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
    token, err := windows.GetCurrentProcessToken().GetTokenUser()
    if err != nil {
        return nil, err
    }
    return windows.SecurityDescriptorFromString("O:" + token.User.Sid.String() + "D:P(A;OICI;FA;;;" + token.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
}

func attributes() (*windows.SecurityAttributes, error) {
    descriptor, err := SecurityDescriptor()
    if err != nil {
        return nil, err
    }
    return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}, nil
}

func mkdir(path string) error {
    name, err := windows.UTF16PtrFromString(path)
    if err != nil {
        return err
    }
    security, err := attributes()
    if err != nil {
        return err
    }
    if err := windows.CreateDirectory(name, security); err != nil {
        return &os.PathError{Op: "mkdir", Path: path, Err: err}
    }
    return nil
}

// Check verifies the opened object rather than trusting emulated POSIX modes.
func Check(path string, directory bool) error {
    name, err := windows.UTF16PtrFromString(path)
    if err != nil {
        return err
    }
    handle, err := windows.CreateFile(name, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES,
        windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
        windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
    if err != nil {
        return &os.PathError{Op: "inspect", Path: path, Err: err}
    }
    defer windows.CloseHandle(handle)
    return checkHandle(handle, directory)
}

func checkHandle(handle windows.Handle, directory bool) error {
    var info windows.ByHandleFileInformation
    if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
        return err
    }
    if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 ||
        (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory {
        return ErrUnsafe
    }
    return CheckSecurity(handle, windows.SE_FILE_OBJECT)
}

// CheckSecurity verifies the owner and access grants of a private Windows object.
// Kernel locks use the same rules as private files, including on an existing name.
func CheckSecurity(handle windows.Handle, objectType windows.SE_OBJECT_TYPE) error {
    descriptor, err := windows.GetSecurityInfo(handle, objectType,
        windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
    if err != nil {
        return err
    }
    current, err := windows.GetCurrentProcessToken().GetTokenUser()
    if err != nil {
        return err
    }
    owner, _, err := descriptor.Owner()
    if err != nil || !windows.EqualSid(owner, current.User.Sid) {
        return ErrUnsafe
    }
    acl, _, err := descriptor.DACL()
    if err != nil || acl == nil {
        return ErrUnsafe
    }
    // Unknown ACE forms are rejected rather than guessed to be restrictive.
    // Deny ACEs can only remove access and are safe to retain.
    for i := uint32(0); i < uint32(acl.AceCount); i++ {
        var ace *windows.ACCESS_ALLOWED_ACE
        if err := windows.GetAce(acl, i, &ace); err != nil {
            return err
        }
        header := &ace.Header
        if header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
            continue
        }
        if header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
            return ErrUnsafe
        }
        sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
        if ace.Mask != 0 && !windows.EqualSid(sid, current.User.Sid) &&
            !sid.IsWellKnown(windows.WinLocalSystemSid) && !sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
            return ErrUnsafe
        }
    }
    return nil
}

func open(path string, disposition uint32) (*os.File, error) {
    name, err := windows.UTF16PtrFromString(path)
    if err != nil {
        return nil, err
    }
    security, err := attributes()
    if err != nil {
        return nil, err
    }
    handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL,
        windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, security, disposition,
        windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_WRITE_THROUGH, 0)
    if err != nil {
        return nil, &os.PathError{Op: "open", Path: path, Err: err}
    }
    if err := checkHandle(handle, false); err != nil {
        windows.CloseHandle(handle)
        return nil, err
    }
    return os.NewFile(uintptr(handle), path), nil
}

// Open opens a private regular file with delete sharing disabled for lock safety.
func Open(path string, create bool) (*os.File, error) {
    disposition := uint32(windows.OPEN_EXISTING)
    if create {
        disposition = windows.OPEN_ALWAYS
    }
    return open(path, disposition)
}

// Create creates the file with its final ACL before exposing a handle.
func Create(path string) (*os.File, error) { return open(path, windows.CREATE_NEW) }

// SyncParent flushes the new file on Windows. Windows does not expose POSIX
// directory fsync; publication uses MoveFileEx WRITE_THROUGH instead.
func SyncParent(path string) error {
    file, err := Open(path, false)
    if err != nil {
        return err
    }
    defer file.Close()
    return file.Sync()
}

// SyncAncestors uses Windows file write-through/flush semantics. Unlike Unix,
// Windows cannot fsync directory handles, including ancestor directory handles.
func SyncAncestors(path, stop string) error { return SyncParent(path) }

// Replace performs a same-volume publication after the caller flushes the file.
func Replace(source, destination string) error {
    from, err := windows.UTF16PtrFromString(source)
    if err != nil {
        return err
    }
    to, err := windows.UTF16PtrFromString(destination)
    if err != nil {
        return err
    }
    return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

// Remove first durably retires the public name. A crash can leave a private
// tombstone, but cannot make a successfully retired credential addressable again.
func Remove(path string) error {
    if err := Check(path, false); err != nil {
        return err
    }
    var nonce [16]byte
    if _, err := rand.Read(nonce[:]); err != nil {
        return err
    }
    tombstone := path + ".deleted-" + hex.EncodeToString(nonce[:])
    from, err := windows.UTF16PtrFromString(path)
    if err != nil {
        return err
    }
    to, err := windows.UTF16PtrFromString(tombstone)
    if err != nil {
        return err
    }
    if err := windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH); err != nil {
        return err
    }
    return os.Remove(tombstone)
}
