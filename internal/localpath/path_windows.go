package localpath

import (
    "errors"
    "path/filepath"
    "strings"

    "golang.org/x/sys/windows"
)

// Supported limits native Windows files to local NTFS drives. UNC, device names,
// ADS and drive-relative paths must not become rsync remote specifications.
func Supported(path string) error {
    if !filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) || len(filepath.VolumeName(path)) != 2 || strings.Contains(path[2:], ":") {
        return errors.New("Windows requires an absolute local NTFS path")
    }
    root, err := windows.UTF16PtrFromString(filepath.VolumeName(path) + `\`)
    if err != nil {
        return err
    }
    if windows.GetDriveType(root) == windows.DRIVE_REMOTE {
        return errors.New("Windows network drives are unsupported")
    }
    var filesystem [32]uint16
    if err := windows.GetVolumeInformation(root, nil, 0, nil, nil, nil, &filesystem[0], uint32(len(filesystem))); err != nil {
        return err
    }
    if !strings.EqualFold(windows.UTF16ToString(filesystem[:]), "NTFS") {
        return errors.New("Windows requires NTFS")
    }
    return nil
}

// Canonical uses the opened directory's final name, preserving true case while
// normalizing aliases, drive spelling, short names and separator differences.
func Canonical(path string) (string, error) {
    absolute, err := filepath.Abs(path)
    if err != nil {
        return "", err
    }
    if err := Supported(absolute); err != nil {
        return "", err
    }
    name, err := windows.UTF16PtrFromString(`\\?\` + absolute)
    if err != nil {
        return "", err
    }
    handle, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
    if err != nil {
        return "", err
    }
    defer windows.CloseHandle(handle)
    buffer := make([]uint16, 512)
    for {
        // Zero selects FILE_NAME_NORMALIZED | VOLUME_NAME_DOS.
        n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
        if err != nil {
            return "", err
        }
        if n >= uint32(len(buffer)) {
            buffer = make([]uint16, n+1)
            continue
        }
        result := strings.TrimPrefix(windows.UTF16ToString(buffer[:n]), `\\?\`)
        if err := Supported(result); err != nil {
            return "", err
        }
        return filepath.Clean(result), nil
    }
}
