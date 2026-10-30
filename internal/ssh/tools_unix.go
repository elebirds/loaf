//go:build !windows

package ssh

import (
    "context"
    "os/exec"

    "github.com/elebirds/loaf/internal/fault"
)

// DiscoverTools finds native Unix tools without requiring rsync for SSH-only use.
func DiscoverTools() (Tools, error) {
    ssh, err := exec.LookPath("ssh")
    rsync, _ := exec.LookPath("rsync")
    if err != nil {
        err = fault.Wrap("SSH_FAILED", "ssh_missing", err)
    }
    return Tools{SSH: ssh, Rsync: rsync}, err
}

func platformSession(s connectionSession) connectionSession { return s }
func (c *Client) localTransferPath(ctx context.Context, path string) (string, error) {
    return path, nil
}
func (c *Client) sshTransferPath() string { return c.Binary }
