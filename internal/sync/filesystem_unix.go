//go:build !windows

package sync

import "github.com/elebirds/loaf/internal/wire"

func destinationFilesystem(string) *wire.OutputFilesystem { return nil }
func validateDestinationNamespace(string, wire.OutputSelection, *wire.OutputFilesystem) error {
    return nil
}
func validDestinationPaths([]string) bool { return true }
