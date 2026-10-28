//go:build darwin || linux

package agentdelivery

func privateDesktopSocket(path string) error { return privateSocket(path, false) }
