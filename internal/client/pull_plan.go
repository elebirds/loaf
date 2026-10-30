package client

import (
    "context"
    "path/filepath"
    "slices"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/elebirds/loaf/internal/task"
)

// PullPreferences describes the effective local download defaults, without
// contacting a machine, allocating state or creating a result directory.
type PullPreferences struct {
    Policy                                    config.PullPolicy
    BaseDirectory, BaseSource, ChecksumSource string
}

func (c *Projects) pullPreferences(root string, file config.ProjectFile, checksum *bool) (PullPreferences, error) {
    policy, err := config.ResolvePull(c.Settings.Pull, file.Pull, config.PullSettings{Checksum: checksum})
    if err != nil {
        return PullPreferences{}, err
    }
    result := PullPreferences{Policy: policy, BaseSource: "default", ChecksumSource: "default"}
    for _, layer := range []struct {
        name     string
        settings config.PullSettings
    }{{"user", c.Settings.Pull}, {"project", file.Pull}} {
        if layer.settings.BaseDir != "" {
            result.BaseSource = layer.name
        }
        if layer.settings.Checksum != nil {
            result.ChecksumSource = layer.name
        }
    }
    if checksum != nil {
        result.ChecksumSource = "flag"
    }
    // PullBase resolves preferences; an explicit --to must remain usable even
    // when the default directory cannot be used on this machine.
    result.BaseDirectory, err = config.PullBase(root, policy)
    return result, err
}

// PullPreviewRun is the public, locally resolved selection for one saved run.
type PullPreviewRun struct {
    ID, Target, Destination string
    Paths                   []string
}

// PullPreview is a read-only review, not proof of remote availability. Input
// freezes exact IDs, absolute destinations and checksum for an explicit Pull.
type PullPreview struct {
    Preferences       PullPreferences
    DestinationSource string
    Input             PullInput
    Runs              []PullPreviewRun
    refs              []task.RunRef
    failure           *PullBatch
}

func pullOptions(in PullInput) (multi bool, concurrency int, err error) {
    multi, concurrency = len(in.IDs) > 1 || in.Into != nil, 1
    if in.Concurrency != nil {
        concurrency = *in.Concurrency
    }
    if (multi && (value(in.Into) == "" || len(in.IDs) == 0 || in.To != nil)) || (!multi && in.Concurrency != nil) || concurrency < 1 || concurrency > 4 || (concurrency > 1 && !in.Policy.Batch) {
        return multi, concurrency, fault.Invalid("--into/--to/--concurrency", "conflicting_arguments", "pull_batch_flags")
    }
    return multi, concurrency, nil
}

// PreviewPull uses the existing policy, path and destination resolvers. It
// neither prepares a runner nor tests download eligibility over the network.
func (c *Projects) PreviewPull(ctx context.Context, in PullInput) (PullPreview, error) {
    multi, _, err := pullOptions(in)
    if err != nil {
        return PullPreview{}, err
    }
    if c.Runs == nil {
        return PullPreview{}, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    project, file, err := c.boundProject(ctx)
    if err != nil {
        return PullPreview{}, err
    }
    // ResolvePull intentionally validates only preference values here. An
    // explicit destination overrides an unusable default base, as in Pull.
    policy, err := config.ResolvePull(c.Settings.Pull, file.Pull, config.PullSettings{Checksum: in.Checksum})
    if err != nil {
        return PullPreview{}, err
    }
    preferences, prefErr := c.pullPreferences(project.Root, file, in.Checksum)
    if prefErr != nil && in.To == nil && in.Into == nil {
        return PullPreview{}, prefErr
    }
    preferences.Policy = policy
    if in.To != nil && *in.To == "" {
        return PullPreview{}, fault.New("BAD_CONFIG", "pull_destination", nil)
    }
    var refs []task.RunRef
    if multi {
        refs, err = c.Runs.Freeze(project.ID, in.IDs, nil)
    } else {
        id := ""
        if len(in.IDs) > 0 {
            id = in.IDs[0]
        }
        var ref task.RunRef
        ref, err = c.Runs.Select(project.ID, id)
        refs = []task.RunRef{ref}
    }
    if err != nil {
        return PullPreview{}, err
    }
    result := PullPreview{Preferences: preferences, DestinationSource: preferences.BaseSource, refs: refs, Input: in}
    result.Input.IDs = nil
    result.Input.Paths = slices.Clone(in.Paths)
    checksum := policy.Checksum
    result.Input.Checksum = &checksum
    parent := ""
    if multi {
        // Preserve Pull's existing batch error envelope: destination failures
        // before selection have an empty batch; later validation failures
        // retain the frozen set and any rows already locally resolved.
        result.failure = &PullBatch{}
        parent, err = sources.Destination(project.Root, c.CWD, *in.Into, "", policy)
        if err != nil {
            return result, err
        }
        result.Input.Into, result.DestinationSource = &parent, "into"
        result.failure = &PullBatch{Runs: make([]PullRow, len(refs)), Selected: len(refs)}
    } else if in.To != nil {
        result.DestinationSource = "to"
    }
    for i, ref := range refs {
        selected := ref.Outputs
        if in.Paths != nil {
            selected = in.Paths
        }
        paths, err := sources.OutputPaths(selected)
        if err != nil {
            return result, err
        }
        to := value(in.To)
        if multi {
            to = filepath.Join(parent, ref.ID)
        }
        destination, err := sources.Destination(project.Root, c.CWD, to, ref.ID, policy)
        if err != nil {
            return result, err
        }
        result.Runs = append(result.Runs, PullPreviewRun{ID: ref.ID, Target: ref.Target, Destination: destination, Paths: paths})
        result.Input.IDs = append(result.Input.IDs, ref.ID)
        if !multi {
            result.Input.To = &destination
        } else {
            result.failure.Runs[i].Result = sources.PullResult{RunID: ref.ID, Target: ref.Target, Destination: destination, Policy: policy, Paths: paths, DownloadedPaths: []string{}, MissingPaths: []string{}}
        }
    }
    return result, nil
}
