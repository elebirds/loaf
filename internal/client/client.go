// Package client implements local application workflows independently of a CLI parser.
package client

import (
    "context"
    "io"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/state/local"
    sources "github.com/elebirds/loaf/internal/sync"
    "github.com/elebirds/loaf/internal/task"
)

// Checker observes the prerequisites consumed by local check workflows.
type Checker interface {
    Check(context.Context, config.Target, string, bool) (ssh.Report, error)
}

// RunnerVerifier explicitly prepares and verifies a remote runner.
type RunnerVerifier interface {
    Verify(context.Context, config.Target, string, bool) (execution.RunnerInfo, error)
}

// Synchronizer copies a selected source plan to its managed destination.
type Synchronizer interface {
    Sync(context.Context, config.Target, string, string, sources.Plan, bool) (sources.Result, error)
}

// RunCleaner owns preview and revalidated cleanup operations.
type RunCleaner interface {
    Preview(context.Context, task.RunRef, bool, bool) (execution.CleanResult, error)
    Clean(context.Context, execution.CleanResult, bool) (execution.CleanResult, error)
}

// OutputBrowser reads bounded metadata within frozen outputs.
type OutputBrowser interface {
    List(context.Context, task.RunRef, int, int, bool, bool) (execution.OutputListing, error)
}

// OutputPuller retrieves selected outputs without presentation concerns.
type OutputPuller interface {
    Pull(context.Context, task.RunRef, string, []string, config.PullPolicy, bool) (sources.PullResult, error)
}

// MachineOperations owns native foreground streams and connections.
type MachineOperations interface {
    Exec(context.Context, config.Target, ssh.ExecInput, io.Reader, io.Writer, io.Writer) (ssh.ExecResult, error)
    Interactive(context.Context, config.Target, io.Reader, io.Writer, io.Writer) error
    Forward(context.Context, config.Target, int, int, bool, io.Writer) error
}

// Dependencies are assembled once by the executable. No service locator is used.
type Dependencies struct {
    Events     EventService
    Notify     NotificationService
    Queue      QueueService
    Config     *config.Store
    State      *local.Store
    Checker    Checker
    Runner     RunnerVerifier
    Syncer     Synchronizer
    Cleaner    RunCleaner
    Outputs    OutputBrowser
    Puller     OutputPuller
    Runs       *execution.Service
    Operations MachineOperations
    Cloud      *cloud.Service
    Auth       *cloud.Authentication
    CWD        string
    Trace      *ssh.Trace
}

// Projects owns project planning and durable run workflows.
type Projects struct {
    Notify   NotificationService
    Config   *config.Store
    State    *local.Store
    Settings config.Global
    Runs     *execution.Service
    Syncer   Synchronizer
    Cleaner  RunCleaner
    Outputs  OutputBrowser
    Puller   OutputPuller
    CWD      string
}

// Machines owns configured targets and foreground machine operations.
type Machines struct {
    Config     *config.Store
    State      *local.Store
    Settings   config.Global
    Checker    Checker
    Runner     RunnerVerifier
    Operations MachineOperations
    Trace      *ssh.Trace
    CWD        string
}

// Resources owns cloud acquisition, binding and account authentication.
type Resources struct {
    Notify     NotificationService
    Config     *config.Store
    Settings   config.Global
    Cloud      *cloud.Service
    Auth       *cloud.Authentication
    Operations MachineOperations
    CWD        string
}

// App is only a composition facade; workflows live on focused services.
type App struct {
    Events    EventService
    Notify    NotificationService
    Queue     QueueService
    Projects  *Projects
    Machines  *Machines
    Resources *Resources
}

// New constructs focused services from an already loaded configuration snapshot.
func New(d Dependencies, settings config.Global) *App {
    if d.Trace == nil {
        d.Trace = &ssh.Trace{}
    }
    return &App{
        Events:    d.Events,
        Notify:    d.Notify,
        Queue:     d.Queue,
        Projects:  &Projects{Notify: d.Notify, Config: d.Config, State: d.State, Settings: settings, Runs: d.Runs, Syncer: d.Syncer, Cleaner: d.Cleaner, Outputs: d.Outputs, Puller: d.Puller, CWD: d.CWD},
        Machines:  &Machines{Config: d.Config, State: d.State, Settings: settings, Checker: d.Checker, Runner: d.Runner, Operations: d.Operations, Trace: d.Trace, CWD: d.CWD},
        Resources: &Resources{Notify: d.Notify, Config: d.Config, Settings: settings, Cloud: d.Cloud, Auth: d.Auth, Operations: d.Operations, CWD: d.CWD},
    }
}

// CallPolicy describes authentication and observation capabilities, not output format.
type CallPolicy struct{ Batch bool }

// Progress is an optional, nonlocalized application-stage observation.
type Progress struct {
    Stage  string
    Target string
    RunID  string
    Count  int
}

// ProgressFunc receives client stages without deciding business outcomes.
type ProgressFunc func(Progress)

func progress(emit ProgressFunc, event Progress) {
    if emit != nil {
        emit(event)
    }
}

func value[T any](p *T) (v T) {
    if p != nil {
        return *p
    }
    return v
}
