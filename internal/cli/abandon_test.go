package cli

import (
    "context"
    "encoding/json"
    "fmt"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

type fakeAbandoner struct {
    previews, calls  int
    repairs          []bool
    blocked, partial string
}

func (f *fakeAbandoner) Preview(_ context.Context, ref task.RunRef, _, repair bool) (execution.AbandonResult, error) {
    f.previews++
    f.repairs = append(f.repairs, repair)
    row := execution.AbandonResult{Run: execution.View{ID: ref.ID, Target: ref.Target, Submission: ref.Submission, Status: "unknown"}, Path: ref.RemoteDir + "/runs/" + ref.ID, Eligible: true, Ref: ref}
    if ref.ID == f.blocked {
        row.Eligible = false
        return row, fault.New("ABANDON_NOT_PREPARED", "abandon_not_prepared", nil)
    }
    return row, nil
}
func (f *fakeAbandoner) Abandon(_ context.Context, row execution.AbandonResult, _ bool) (execution.AbandonResult, error) {
    f.calls++
    now := time.Now().UTC()
    row.NoStartAt = &now
    if row.Run.ID == f.partial {
        return row, fault.New("ABANDON_UNCONFIRMED", "abandon_unconfirmed", nil)
    }
    row.CleanedAt = &now
    return row, nil
}

func abandonCLI(t *testing.T, n int) (testOptions, *fakeAbandoner, []string) {
    t.Helper()
    o := fixture(t)
    cwd, _, err := config.Discover(context.Background(), o.CWD)
    if err != nil {
        t.Fatal(err)
    }
    o.CWD = cwd
    p, err := o.State.Select(context.Background(), cwd, "lab")
    if err != nil {
        t.Fatal(err)
    }
    var ids []string
    for i := 0; i < n; i++ {
        id := fmt.Sprintf("r_%024x", i+1)
        ref := task.RunRef{ID: id, ProjectID: p.ID, Target: "lab", Connection: config.Target{Destination: "saved-host"}, RemoteDir: "/saved/project", Runner: "/saved/runner", Version: wire.Version, CreatedAt: time.Now().UTC(), Submission: "preparing"}
        if err := o.State.SaveRun(context.Background(), ref); err != nil {
            t.Fatal(err)
        }
        ids = append(ids, id)
    }
    fake := &fakeAbandoner{}
    o.Runs, o.Abandoner = &execution.Service{Index: o.State}, fake
    return o, fake, ids
}

func TestAbandonCLIExplicitIDsConsentAndReadOnlyPreview(t *testing.T) {
    o, fake, ids := abandonCLI(t, 1)
    for _, args := range [][]string{{"abandon"}, {"abandon", "--yes"}, {"abandon", ids[0]}, {"abandon", ids[0], "--yes", "--target", "other"}} {
        code, out, stderr := run(t, o, append(args, "--json")...)
        if code != 2 || stderr != "" || fake.previews != 0 || fake.calls != 0 || !strings.Contains(out, `"code":"BAD_CONFIG"`) {
            t.Fatal(args, code, out, stderr, fake)
        }
    }
    for _, lang := range []string{"en", "zh-Hans"} {
        code, out, stderr := run(t, o, "--lang", lang, "abandon", ids[0], ids[0], "--dry-run", "--json")
        var response struct {
            Data client.AbandonBatch `json:"data"`
        }
        if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &response) != nil || !response.Data.DryRun || len(response.Data.Runs) != 1 || !response.Data.Runs[0].Eligible || response.Data.Runs[0].NoStartAt != nil || fake.calls != 0 || fake.repairs[len(fake.repairs)-1] {
            t.Fatal(code, out, stderr)
        }
        code, out, stderr = run(t, o, "--lang", lang, "abandon", "--help")
        if code != 0 || stderr != "" || !strings.Contains(out, "--dry-run") || !strings.Contains(out, "--yes") || strings.Contains(out, "--target") {
            t.Fatal(code, out, stderr)
        }
    }
}

func TestAbandonCLIHumanDeclineAndFinitePartialResults(t *testing.T) {
    o, fake, ids := abandonCLI(t, 2)
    o.Interactive, o.In = true, strings.NewReader("no\n")
    code, out, stderr := run(t, o, "-i", "abandon", ids[0])
    if code != 1 || fake.calls != 0 || strings.Count(stderr, "[y/N]") != 1 || !strings.Contains(out+stderr, ids[0]) {
        t.Fatal(code, out, stderr, fake)
    }
    fake.blocked = ids[1]
    code, out, stderr = run(t, o, "abandon", ids[0], ids[1], "--yes", "--json")
    if code != 1 || stderr != "" || fake.calls != 0 {
        t.Fatal(code, out, stderr, fake)
    }
    fake.blocked, fake.partial = "", ids[1]
    code, out, stderr = run(t, o, "abandon", ids[0], ids[1], "--yes", "--json")
    var response struct {
        Data client.AbandonBatch `json:"data"`
    }
    if code != 3 || stderr != "" || strings.Count(strings.TrimSpace(out), "\n") != 0 || json.Unmarshal([]byte(out), &response) != nil || len(response.Data.Runs) != 2 || response.Data.Runs[0].CleanedAt == nil || response.Data.Runs[1].NoStartAt == nil || response.Data.Runs[1].CleanedAt != nil || response.Data.Runs[1].ErrorCode != "ABANDON_UNCONFIRMED" {
        t.Fatal(code, out, stderr)
    }
}
