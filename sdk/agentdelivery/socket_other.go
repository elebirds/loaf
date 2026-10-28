//go:build !darwin && !linux

package agentdelivery

func privateSocket(string, bool) error    { return ErrUnsupported }
func privateSocketDirectory(string) error { return ErrUnsupported }
