package ssh

import "errors"

// InterruptPrivateMasterForTest lets external-package SSH fixtures interrupt
// the sole process owned by their client without exposing a production API.
func InterruptPrivateMasterForTest(client *Client) error {
    scope := client.Connections
    if scope == nil {
        return errors.New("missing fixture scope")
    }
    scope.mu.Lock()
    if len(scope.entries) != 1 {
        scope.mu.Unlock()
        return errors.New("fixture requires one private connection")
    }
    var entry *connectionEntry
    for _, e := range scope.entries {
        entry = e
    }
    scope.mu.Unlock()
    entry.gate <- struct{}{}
    master := entry.master
    <-entry.gate
    if master == nil {
        return errors.New("missing fixture master")
    }
    return master.process.Process.Kill()
}
