package cli

import (
    "context"
    "errors"
    "io"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
)

type readinessOperations struct {
    fakeOperations
    failures int
    detail   string
}

func (o *readinessOperations) Exec(_ context.Context, target config.Target, in ssh.ExecInput, _ io.Reader, _, _ io.Writer) (ssh.ExecResult, error) {
    o.calls++
    if len(in.Args) != 1 || in.Args[0] != "/bin/true" || !in.Batch {
        panic("readiness is not a fixed probe")
    }
    if o.calls <= o.failures {
        return ssh.ExecResult{Status: "unconfirmed"}, fault.New("SSH_FAILED", "ssh_failed", map[string]any{"Detail": o.detail})
    }
    zero := 0
    return ssh.ExecResult{Status: "succeeded", ExitCode: &zero}, nil
}

func TestCloudReadinessRetriesOnlyTransientAndSameBindingIsIdempotent(t *testing.T) {
    for _, tc := range []struct {
        detail         string
        failures, want int
    }{
        {"Connection refused", 2, 3}, {"Permission denied", 2, 1}, {"Host key verification failed", 2, 1},
    } {
        o, _ := uxFixture(t)
        operations := &readinessOperations{failures: tc.failures, detail: tc.detail}
        o.Operations = operations
        settings, _ := o.Config.Load()
        app := client.New(o.Dependencies, settings)
        waits := 0
        app.Resources.ReadyWait = func(ctx context.Context, d time.Duration) error {
            if d != 5*time.Second {
                t.Fatal(d)
            }
            waits++
            return ctx.Err()
        }
        input := client.CloudBindInput{Name: "existing", Instance: "instance", ReadyTimeout: time.Minute}
        result, err := app.Resources.CloudBind(context.Background(), input)
        if operations.calls != tc.want {
            t.Fatal(tc, operations.calls, err)
        }
        if tc.want == 3 {
            if err != nil || result.Status != "ready" || waits != 2 {
                t.Fatal(result, err, waits)
            }
            result, err = app.Resources.CloudBind(context.Background(), input)
            if err != nil || result.Status != "ready" {
                t.Fatal("same binding was not idempotent", result, err)
            }
        } else if err == nil || result.Status != "bound" || waits != 0 {
            t.Fatal(result, err, waits)
        }
    }
}

func TestCloudDirectBindingRecoveryKeepsNewAliasAndOriginalIdentity(t *testing.T) {
    o, p := uxFixture(t)
    ops := &readinessOperations{failures: 1, detail: "Permission denied"}
    o.Operations = ops
    settings, _ := o.Config.Load()
    app := client.New(o.Dependencies, settings)
    binding, err := app.Resources.CloudBind(context.Background(), client.CloudBindInput{Name: "new-alias", Instance: "original-instance", ReadyTimeout: 0})
    if err == nil || binding.Status != "bound" {
        t.Fatal(binding, err)
    }
    binding, err = app.Resources.RetryCloudBinding(context.Background(), binding, client.CloudBindInput{ReadyTimeout: 0})
    if err != nil || binding.Status != "ready" || p.observed.ID != "original-instance" || p.creates != 0 {
        t.Fatal(binding, err, p.observed)
    }
    current, _ := o.Config.Load()
    target := current.Targets["new-alias"]
    changed := *target.Cloud
    changed.Ref.ID = "replacement"
    target.Cloud = &changed
    if err := o.Config.Add(context.Background(), "new-alias", target, true); err != nil {
        t.Fatal(err)
    }
    calls := ops.calls
    if _, err = app.Resources.RetryCloudBinding(context.Background(), binding, client.CloudBindInput{ReadyTimeout: 0}); err == nil || calls != ops.calls {
        t.Fatal("alias change redirected recovery", err)
    }
}

func TestCloudRecoveryBeforeAliasSavedUsesFrozenResource(t *testing.T) {
    o, p := uxFixture(t)
    p.getError = &cloud.APIError{Code: "TRANSPORT", Transient: true}
    settings, _ := o.Config.Load()
    app := client.New(o.Dependencies, settings)
    binding, err := app.Resources.CloudBind(context.Background(), client.CloudBindInput{Name: "late-alias", Instance: "original", ReadyTimeout: 0})
    if err == nil || binding.Status != "not_bound" {
        t.Fatal(binding, err)
    }
    credentials, err := app.Resources.CloudBindingCredentials(context.Background(), binding)
    if err != nil || credentials.Instance.ID != "original" || credentials.Password != nil {
        t.Fatal(credentials.Instance, err)
    }
    p.getError = nil
    binding, err = app.Resources.RetryCloudBinding(context.Background(), binding, client.CloudBindInput{ReadyTimeout: 0})
    if err != nil || binding.Status != "ready" || p.observed.ID != "original" || p.creates != 0 {
        t.Fatal(binding, err)
    }
}

type transientReadinessCloud struct{ *uxCloud }

func (p *transientReadinessCloud) Get(_ context.Context, ref cloud.Ref) (cloud.Instance, error) {
    p.gets++
    if p.gets == 1 {
        return cloud.Instance{}, &cloud.APIError{Code: "INVALID_RESPONSE", HTTPStatus: 500, Transient: true}
    }
    return cloud.Instance{Ref: ref, Status: "running"}, nil
}
func TestCloudReadinessRetriesTransientPlatformReadWithoutAllocating(t *testing.T) {
    o, base := uxFixture(t)
    p := &transientReadinessCloud{base}
    o.Cloud.Open = func(context.Context, string, cloud.Account, string) (cloud.Provider, error) { return p, nil }
    settings, _ := o.Config.Load()
    app := client.New(o.Dependencies, settings)
    waits := 0
    app.Resources.ReadyWait = func(context.Context, time.Duration) error { waits++; return nil }
    result, err := app.Resources.CloudBind(context.Background(), client.CloudBindInput{Name: "api-recovery", Instance: "original", ReadyTimeout: time.Minute})
    if err != nil || result.Status != "ready" || p.gets != 2 || waits != 1 || p.creates != 0 {
        t.Fatal(result, err, p.gets, waits)
    }
}

func TestCloudCanceledBindingDoesNotOpenAnotherForm(t *testing.T) {
    binding := client.CloudBinding{Target: "retained", Instance: cloud.Ref{ID: "original"}, Status: "bound"}
    call := &invocation{}
    result, err := call.recoverCloudBinding(context.Background(), binding, fault.Wrap("CLOUD_NOT_READY", "cloud_not_ready", context.Canceled))
    if !errors.Is(err, context.Canceled) || result.Instance.ID != "original" {
        t.Fatal(result, err)
    }
}
