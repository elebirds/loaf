package main

import (
    "archive/tar"
    "compress/gzip"
    "errors"
    "io"
    "io/fs"
    "os"
    "path/filepath"
)

// Archive permissions describe the target installation, independently of the
// build host's ACLs or the different mode emulation used by MSYS installations.
func writeTarGZ(destination, directory string) (err error) {
    file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
    if err != nil {
        return err
    }
    defer func() {
        err = errors.Join(err, file.Close())
        if err != nil {
            os.Remove(destination)
        }
    }()
    compressed := gzip.NewWriter(file)
    archive := tar.NewWriter(compressed)
    err = filepath.WalkDir(directory, func(name string, entry fs.DirEntry, walkErr error) error {
        if walkErr != nil {
            return walkErr
        }
        if name == directory {
            return nil
        }
        info, err := entry.Info()
        if err != nil {
            return err
        }
        if !info.IsDir() && !info.Mode().IsRegular() {
            return errors.New("archive input must be a regular file or directory")
        }
        relative, err := filepath.Rel(directory, name)
        if err != nil || !filepath.IsLocal(relative) {
            return errors.New("archive input escaped staging directory")
        }
        header, err := tar.FileInfoHeader(info, "")
        if err != nil {
            return err
        }
        header.Name = filepath.ToSlash(relative)
        header.Mode = 0644
        if info.IsDir() || relative == "loaf" {
            header.Mode = 0755
        }
        header.Uid, header.Gid, header.Uname, header.Gname = 0, 0, "", ""
        if err := archive.WriteHeader(header); err != nil {
            return err
        }
        if info.IsDir() {
            return nil
        }
        input, err := os.Open(name)
        if err != nil {
            return err
        }
        _, copyErr := io.Copy(archive, input)
        return errors.Join(copyErr, input.Close())
    })
    return errors.Join(err, archive.Close(), compressed.Close())
}
