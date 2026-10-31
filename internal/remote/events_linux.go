//go:build linux

package remote

import (
    "context"
    "errors"
    "os"
    "path/filepath"

    "github.com/elebirds/loaf/internal/event"
    remotestate "github.com/elebirds/loaf/internal/state/remote"
    "github.com/elebirds/loaf/internal/wire"
)

// Events reads a project's persistent facts without deploying or creating data.
func Events(ctx context.Context, req wire.Request) (event.Page, error) {
    var result event.Page
    if req.FormatVersion != wire.FormatVersion {
        return result, errors.New("BAD_FORMAT")
    }
    if !wire.CompatibleVersion(wire.Version, req.Version) {
        return result, errors.New("VERSION_MISMATCH")
    }
    if req.Events == nil || req.Events.Source != "" && req.Events.Source != "project" || !identifier.MatchString(req.ProjectID) || !filepath.IsAbs(req.Root) {
        return result, errors.New("BAD_REQUEST")
    }
    root, err := filepath.EvalSymlinks(req.Root)
    home, homeErr := os.UserHomeDir()
    if err != nil || homeErr != nil || root != filepath.Clean(req.Root) || root == "/" || root == home {
        return result, errors.New("UNSAFE_PATH")
    }
    if err = ctx.Err(); err != nil {
        return result, err
    }
    store, err := remotestate.Open(root, false)
    if err != nil {
        return result, err
    }
    defer store.Close()
    if err = store.VerifyProject(req.ProjectID); err != nil {
        return result, err
    }
    result, err = store.Events(ctx, *req.Events)
    if errors.Is(err, event.ErrCursor) {
        return result, errors.New("EVENT_CURSOR_INVALID")
    }
    if errors.Is(err, event.ErrInvalid) {
        return result, errors.New("BAD_REQUEST")
    }
    return result, err
}
