package run

import (
    "context"
    "maps"
    "slices"
    "testing"
)

func TestPreviewAndSubmissionShareExecutionNormalization(t *testing.T) {
    for _, explicit := range []bool{false, true} {
        service, in, _, remote := executionFixture(t)
        in.File.Run.Command = "printf configured"
        in.File.Run.Setup = "printf setup"
        in.File.Run.Env = map[string]string{"Z_LAST": "private", "A_FIRST": "also-private"}
        in.File.Outputs.Paths = []string{"outputs/", "metrics", "outputs"}
        if !explicit {
            in.Args = nil
        }
        spec, err := ResolveExecution(in.File, in.Args)
        if err != nil {
            t.Fatal(err)
        }
        submitted, err := service.Submit(context.Background(), in, nil)
        if err != nil || !slices.Equal(remote.request.Args, spec.Args) || remote.request.Setup != spec.Setup || !maps.Equal(remote.request.Env, spec.Env) || !slices.Equal(submitted.Ref.Outputs, spec.Outputs) {
            t.Fatal(spec, submitted, remote.request, err)
        }
        if !slices.Equal(spec.Outputs, []string{"outputs", "metrics"}) || !slices.Equal(spec.EnvironmentKeys(), []string{"A_FIRST", "Z_LAST"}) {
            t.Fatal(spec)
        }
        if !explicit && !slices.Equal(spec.Args, []string{"/bin/sh", "-c", in.File.Run.Command}) {
            t.Fatal(spec.Args)
        }
        in.File.Run.Env["Z_LAST"] = "changed"
        in.File.Outputs.Paths[0] = "changed"
        if spec.Env["Z_LAST"] != "private" || spec.Outputs[0] != "outputs" {
            t.Fatal("resolved execution aliases input storage", spec)
        }
    }
    service, in, _, _ := executionFixture(t)
    spec, err := ResolveExecution(in.File, in.Args)
    if err != nil || !slices.Equal(spec.Outputs, []string{"outputs"}) {
        t.Fatal(spec, err)
    }
    if _, err := service.Submit(context.Background(), in, nil); err != nil {
        t.Fatal("existing Submit File/Args contract changed", err)
    }
}
