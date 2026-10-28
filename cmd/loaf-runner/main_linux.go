package main

import (
    "os"

    "github.com/elebirds/loaf/internal/remote"
)

func main() {
    if len(os.Args) == 3 && os.Args[1] == "__coordinate" {
        if remote.Coordinate(os.Args[2]) != nil {
            os.Exit(1)
        }
        return
    }
    if len(os.Args) >= 2 && os.Args[1] == "__send" {
        os.Exit(remote.SendOutputs(os.Stdin, os.Stdout, os.Stderr, os.Args[2:]))
    }
    if len(os.Args) >= 2 && os.Args[1] == "__receive" {
        os.Exit(remote.Receive(os.Stdin, os.Stdout, os.Stderr, os.Args[2:]))
    }
    if len(os.Args) == 3 && os.Args[1] == "__supervise" {
        if remote.Supervise(os.Args[2]) != nil {
            os.Exit(1)
        }
        return
    }
    os.Exit(remote.Serve(os.Stdin, os.Stdout))
}
