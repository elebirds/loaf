package cli

import (
    "context"
    "encoding/json"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

type acceptedObservationFailure struct {
    runRemote
    launches int
}

type unavailableObservationState struct{ execution.ObservationStore }

func (unavailableObservationState) SaveRunObservation(context.Context, string, string, task.RunObservation) error {
    return fault.New("BAD_CONFIG", "run_state", nil)
}

func TestObservedResultSeparatesLocalStateFailureInBothLanguages(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        for _, jsonMode := range []bool{true, false} {
            o, _ := runFixture(t)
            o.Runs.Observations = unavailableObservationState{}
            args := []string{"--lang", lang, "run", "--wait", "1s"}
            if jsonMode {
                args = append(args, "--json")
            }
            args = append(args, "--", "fixture-command")
            code, out, stderr := run(t, o, args...)
            if code != 2 {
                t.Fatal(code, out, stderr)
            }
            if jsonMode {
                var result struct {
                    Data  execution.View
                    Error errorView
                }
                if json.Unmarshal([]byte(out), &result) != nil || stderr != "" || result.Data.Status != "failed" || result.Data.Submission != "submitted" || result.Data.StateError != "BAD_CONFIG" || result.Data.QueryError != "" || result.Error.Stage != "local_state" {
                    t.Fatal(out, stderr)
                }
            } else {
                label, stage := "local state save failed", "local state persistence"
                if lang == "zh-Hans" {
                    label, stage = "本地状态保存失败", "保存本地状态"
                }
                if !strings.Contains(out, label) || !strings.Contains(stderr, stage) {
                    t.Fatal(out, stderr)
                }
            }
        }
    }
}

func (r *acceptedObservationFailure) Control(_ context.Context, _ config.Target, _ string, req wire.Request, _ bool) (*wire.RunRecord, error) {
    r.launches++
    r.req = req
    return r.record(req), nil
}

func (r *acceptedObservationFailure) Observe(context.Context, config.Target, string, wire.Request, bool) (wire.Observation, error) {
    f := fault.New("SSH_FAILED", "ssh_failed", map[string]any{"Detail": "Connection closed by fixture"})
    f.Hint = "cloud_ssh_hint"
    return wire.Observation{}, fault.At(f, "observe")
}

func (r *acceptedObservationFailure) Follow(context.Context, config.Target, string, wire.Request, bool, func(wire.LogEvent) error) error {
    _, err := r.Observe(context.Background(), config.Target{}, "", wire.Request{}, true)
    return err
}

func TestAcceptedSubmissionSurvivesObservationFailure(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        for _, jsonMode := range []bool{true, false} {
            o, _ := runFixture(t)
            remote := &acceptedObservationFailure{}
            o.Runs.Remote = remote
            args := []string{"--lang", lang, "run", "--wait", "1s"}
            if jsonMode {
                args = append(args, "--json")
            }
            args = append(args, "--", "fixture-command")
            code, out, stderr := run(t, o, args...)
            if code != 1 || remote.launches != 1 {
                t.Fatal(code, out, stderr, remote.launches)
            }
            if jsonMode {
                var response struct {
                    OK    bool
                    Data  execution.View
                    Error errorView
                }
                if err := json.Unmarshal([]byte(out), &response); err != nil || response.OK || stderr != "" || response.Data.Status != "unknown" || response.Data.Submission != "submitted" || response.Error.Code != "SSH_FAILED" || response.Error.Stage != "observe" || response.Error.Detail == "" || response.Error.Hint == "" {
                    t.Fatal(out, stderr, err)
                }
            } else {
                confirmed, stage := "submission confirmed", "Failed stage: experiment observation"
                if lang == "zh-Hans" {
                    confirmed, stage = "提交已确认", "失败阶段：观察实验状态"
                }
                if !strings.Contains(out, confirmed) || !strings.Contains(stderr, stage) {
                    t.Fatal(out, stderr)
                }
            }
            root, _, err := config.Discover(context.Background(), o.CWD)
            if err != nil {
                t.Fatal(err)
            }
            project, err := o.State.Existing(root)
            if err != nil {
                t.Fatal(err)
            }
            ref, err := o.Runs.Select(project.ID, "")
            if err != nil || ref.Submission != "submitted" || ref.ID != remote.req.RunID {
                t.Fatal(ref, err)
            }
        }
    }
}
