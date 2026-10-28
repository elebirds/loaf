//go:build !darwin && !linux

package agentdelivery

func privateDesktopSocket(string) error { return ErrUnsupported }
