package sync

import (
    "context"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/ssh"
)

// Checker observes only the capabilities needed before a real transfer.
type Checker interface {
    Check(context.Context, config.Target, string, bool) (ssh.Report, error)
}

// Sender transfers selected paths through the receiver's project lock.
type Sender interface {
    Sync(context.Context, config.Target, string, string, ssh.SyncInput, bool) (ssh.Transfer, error)
}

// Service composes existing connections and packaging; it owns no CLI rendering.
type Service struct {
    Check  Checker
    Runner func(context.Context, config.Target, string, bool) (string, error)
    Send   Sender
}

// Result identifies the cache updated by a successfully acknowledged transfer.
type Result struct {
    RemoteDir    string `json:"remote_dir"`
    ChangedFiles int    `json:"changed_files"`
}

// Sync deploys the receiver only for a real sync, never for local selection.
func (s Service) Sync(ctx context.Context, target config.Target, projectID, remoteDir string, plan Plan, batch bool) (Result, error) {
    var result Result
    report, err := s.Check.Check(ctx, target, remoteDir, batch)
    if err != nil {
        return result, err
    }
    runner, err := s.Runner(ctx, target, report.Arch, batch)
    if err != nil {
        return result, err
    }
    req := ssh.SyncInput{Root: report.RemoteDir, ProjectID: projectID, Mode: plan.Policy.Mode}
    for _, file := range plan.Files {
        req.Paths = append(req.Paths, file.Path)
    }
    transferred, err := s.Send.Sync(ctx, target, runner, plan.Root, req, batch)
    if err != nil {
        return result, err
    }
    return Result{RemoteDir: report.RemoteDir, ChangedFiles: transferred.ChangedFiles}, nil
}
