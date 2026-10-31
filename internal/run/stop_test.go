package run

import (
    "context"
    "encoding/json"
    "errors"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/elebirds/loaf/internal/wire"
)

type stopRemote struct {
    Remote
    controls, observations int
    received, observed     wire.Request
    connection             config.Target
    runner                 string
    accepted, final        *wire.RunRecord
    controlErr, observeErr error
    cancel                 context.CancelFunc
}

func (r *stopRemote) Control(_ context.Context, target config.Target, runner string, req wire.Request, _ bool) (*wire.RunRecord, error) {
    r.controls++
    r.received, r.connection, r.runner = req, target, runner
    if r.cancel != nil {
        r.cancel()
    }
    return r.accepted, r.controlErr
}

func (r *stopRemote) Observe(_ context.Context, target config.Target, runner string, req wire.Request, _ bool) (wire.Observation, error) {
    r.observations++
    r.observed = req
    if target.Destination != r.connection.Destination || runner != r.runner {
        return wire.Observation{}, errors.New("changed bound location")
    }
    return wire.Observation{Record: r.final}, r.observeErr
}

func TestStopConfirmationAndBoundPolicy(t *testing.T) {
    zero := time.Duration(0)
    tiny := time.Nanosecond
    firstGrace := int64(60000)
    for _, tc := range []struct {
        name          string
        grace         *time.Duration
        acceptedGrace *int64
        lost          bool
        waitMillis    int64
    }{
        {name: "runner_default", waitMillis: 15000},
        {name: "explicit_zero", grace: &zero, waitMillis: 5000},
        {name: "positive_submillisecond", grace: &tiny, waitMillis: 5001},
        {name: "first_request_wins", grace: &zero, acceptedGrace: &firstGrace, waitMillis: 65000},
        {name: "lost_confirmation", lost: true, waitMillis: 15000},
    } {
        t.Run(tc.name, func(t *testing.T) {
            service, input, sender, original := executionFixture(t)
            started, err := service.Submit(context.Background(), input, nil)
            if err != nil {
                t.Fatal(err)
            }
            ref := started.Ref
            req := request(ref, 0, 0)
            req.Env = map[string]string{"PRIVATE_TOKEN": "secret-value"}
            remote := &stopRemote{accepted: &wire.RunRecord{FormatVersion: 1, Request: req, Status: "running", SupervisorPID: 123, StopGraceMillis: tc.acceptedGrace}, final: &wire.RunRecord{FormatVersion: 1, Request: req, Status: "stopped", Signal: "terminated"}}
            if tc.lost {
                remote.accepted = nil
                remote.controlErr = fault.New("SSH_FAILED", "ssh_failed", nil)
            }
            service.Remote = remote
            got, err := service.Stop(context.Background(), ref, tc.grace, true)
            if err != nil || got.ID != ref.ID || got.Status != "stopped" || got.ExitCode != nil || remote.controls != 1 || remote.observations != 1 || remote.observed.WaitMillis != tc.waitMillis {
                t.Fatal(got, err, remote)
            }
            if remote.received.Action != "stop" || remote.received.Version != ref.Version || remote.runner != ref.Runner || remote.connection.Destination != ref.Connection.Destination || sender.calls != 1 || original.launches != 1 {
                t.Fatal("stop redirected or repeated execution", remote)
            }
            if tc.grace == nil && remote.received.GraceMillis != nil || tc.grace != nil && remote.received.GraceMillis == nil {
                t.Fatal("lost absent/explicit grace", remote.received)
            }
            if tc.grace == &tiny && *remote.received.GraceMillis != 1 {
                t.Fatal("positive grace rounded to zero")
            }
            encoded, _ := json.Marshal(got)
            if strings.Contains(string(encoded), "PRIVATE_TOKEN") || strings.Contains(string(encoded), "secret-value") || strings.Contains(string(encoded), "supervisor_pid") {
                t.Fatal("private record leaked", string(encoded))
            }
        })
    }
}

func TestStopUnconfirmedDoesNotReplay(t *testing.T) {
    for _, kind := range []string{"still_running", "unreachable", "supervisor_lost", "canceled", "wrong_observation"} {
        t.Run(kind, func(t *testing.T) {
            service, input, _, _ := executionFixture(t)
            started, err := service.Submit(context.Background(), input, nil)
            if err != nil {
                t.Fatal(err)
            }
            req := request(started.Ref, 0, 0)
            remote := &stopRemote{accepted: &wire.RunRecord{FormatVersion: 1, Request: req, Status: "running", SupervisorPID: 123}, final: &wire.RunRecord{FormatVersion: 1, Request: req, Status: "running", SupervisorPID: 123}}
            ctx, cancel := context.WithCancel(context.Background())
            defer cancel()
            switch kind {
            case "unreachable":
                remote.final = nil
                remote.observeErr = fault.New("SSH_FAILED", "ssh_failed", nil)
            case "supervisor_lost":
                remote.accepted.Status = "unknown"
                remote.accepted.Reason = "SUPERVISOR_LOST"
                remote.controlErr = &ssh.RemoteError{Code: "STOP_UNCONFIRMED"}
            case "canceled":
                remote.cancel = cancel
            case "wrong_observation":
                remote.final.Request.RunID = "different"
            }
            service.Remote = remote
            got, err := service.Stop(ctx, started.Ref, nil, true)
            var f *fault.Error
            if !errors.As(err, &f) || f.Code != "STOP_UNCONFIRMED" || got.ID != started.ID || got.Status == "stopped" || got.ExitCode != nil || remote.controls != 1 || remote.observations > 1 {
                t.Fatal(got, err, remote)
            }
            if (kind == "canceled" || kind == "supervisor_lost") && remote.observations != 0 {
                t.Fatal("queried despite cancellation or known lost identity")
            }
        })
    }
}

func TestStopBadRequestAndRetainedNaturalExit(t *testing.T) {
    service, input, _, _ := executionFixture(t)
    started, err := service.Submit(context.Background(), input, nil)
    if err != nil {
        t.Fatal(err)
    }
    remote := &stopRemote{controlErr: &ssh.RemoteError{Code: "BAD_REQUEST"}}
    service.Remote = remote
    grace := time.Second
    _, err = service.Stop(context.Background(), started.Ref, &grace, true)
    var f *fault.Error
    if !errors.As(err, &f) || f.Code != "BAD_REQUEST" || remote.observations != 0 {
        t.Fatal(err, remote)
    }
    code := 7
    remote.controlErr = nil
    remote.accepted = &wire.RunRecord{FormatVersion: 1, Request: request(started.Ref, 0, 0), Status: "failed", ExitCode: &code}
    got, err := service.Stop(context.Background(), started.Ref, nil, true)
    if err != nil || got.Status != "failed" || got.ExitCode == nil || *got.ExitCode != code || remote.observations != 0 {
        t.Fatal("natural terminal result changed", got, err, remote)
    }
}

func TestStopFallbackRetainsQueryAndStateFailures(t *testing.T) {
    service, input, _, _ := executionFixture(t)
    started, err := service.Submit(context.Background(), input, nil)
    if err != nil {
        t.Fatal(err)
    }
    grace := int64(1000)
    remote := &stopRemote{
        accepted:   &wire.RunRecord{FormatVersion: 1, Request: request(started.Ref, 0, 0), Status: "running", SupervisorPID: 123, StopGraceMillis: &grace},
        observeErr: fault.New("SSH_FAILED", "ssh_failed", nil),
    }
    service.Remote = remote
    service.Observations = failingObservations{}
    got, err := service.Stop(context.Background(), started.Ref, nil, true)
    if errorCode(err) != "STOP_UNCONFIRMED" || got.Status != "running" || got.ID != started.ID || got.QueryError != "SSH_FAILED" || got.StateError != "BAD_CONFIG" || got.StopGraceMillis == nil || *got.StopGraceMillis != grace || remote.controls != 1 || remote.observations != 1 {
        t.Fatal("stop fallback lost an independent failure", got, err, remote)
    }
}

func TestFormalUpgradeStopConfirmsUncertainRunWithoutRebinding(t *testing.T) {
    originalVersion := wire.Version
    defer func() { wire.Version = originalVersion }()
    wire.Version = "v1.0.0"
    service, input, _, original := executionFixture(t)
    original.lost, original.unknown = true, true
    pending, _ := service.Submit(context.Background(), input, nil)
    if pending.Submission != "uncertain" {
        t.Fatal(pending)
    }
    ref := pending.Ref
    wire.Version = "v1.1.0"
    verifications := 0
    service.Runner = func(context.Context, config.Target, string, bool) (string, error) {
        verifications++
        return "/current/compatible/helper", nil
    }
    req := request(ref, 0, 0)
    remote := &stopRemote{
        accepted: &wire.RunRecord{FormatVersion: 1, Request: req, Status: "running", SupervisorPID: 123},
        final:    &wire.RunRecord{FormatVersion: 1, Request: req, Status: "stopped", Phase: "command"},
    }
    service.Remote = remote
    stopped, err := service.Stop(context.Background(), ref, nil, true)
    if err != nil || stopped.StateError != "" || stopped.Status != "stopped" || stopped.Submission != "submitted" || stopped.Version != "v1.0.0" || verifications != 1 {
        t.Fatal(stopped, err, verifications)
    }
    saved, err := service.Index.Run(ref.ProjectID, ref.ID)
    if err != nil || saved.Runner != ref.Runner || saved.Version != ref.Version || saved.Submission != "submitted" || remote.runner != "/current/compatible/helper" {
        t.Fatal("stop rebound the original Run", saved, err, remote.runner)
    }
}
