package main

import (
    "archive/zip"
    "errors"
    "io"
    "io/fs"
    "os"
    "path/filepath"
)

// writeZIP collects an already built fresh staging tree. Compilation remains
// exclusively in scripts/build.sh, including both embedded Linux runners.
func writeZIP(destination, directory string) (err error) {
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
    archive := zip.NewWriter(file)
    err = filepath.WalkDir(directory, func(name string, entry fs.DirEntry, walkErr error) error {
        if walkErr != nil {
            return walkErr
        }
        if entry.IsDir() {
            return nil
        }
        info, err := entry.Info()
        if err != nil {
            return err
        }
        if !info.Mode().IsRegular() {
            return errors.New("archive input must be a regular file")
        }
        relative, err := filepath.Rel(directory, name)
        if err != nil || !filepath.IsLocal(relative) {
            return errors.New("archive input escaped staging directory")
        }
        header, err := zip.FileInfoHeader(info)
        if err != nil {
            return err
        }
        header.Name, header.Method = filepath.ToSlash(relative), zip.Deflate
        output, err := archive.CreateHeader(header)
        if err != nil {
            return err
        }
        input, err := os.Open(name)
        if err != nil {
            return err
        }
        _, copyErr := io.Copy(output, input)
        return errors.Join(copyErr, input.Close())
    })
    return errors.Join(err, archive.Close())
}
