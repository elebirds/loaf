package cli

import (
    "context"
    "encoding/json"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/fault"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/task"
)

type fakeCleaner struct {
    previews, cleans int
    failPreview      bool
    failClean        bool
    blockedID        string
    unconfirmedID    string
}

func (f *fakeCleaner) Preview(_ context.Context, ref task.RunRef, _, _ bool) (execution.CleanResult, error) {
    f.previews++
    result := execution.CleanResult{Run: execution.View{ID: ref.ID, Target: ref.Target, Status: "failed", CreatedAt: ref.CreatedAt, CompletedAt: new(ref.CreatedAt.Add(time.Second)), ExitCode: new(7)}, Path: ref.RemoteDir + "/runs/" + ref.ID, Eligible: true, Ref: ref}
    if f.failPreview || ref.ID == f.blockedID {
        result.Eligible = false
        return result, fault.New("CLEAN_NOT_TERMINAL", "clean_not_terminal", nil)
    }
    return result, nil
}
func (f *fakeCleaner) Clean(_ context.Context, result execution.CleanResult, _ bool) (execution.CleanResult, error) {
    f.cleans++
    if f.failClean || result.Run.ID == f.unconfirmedID {
        result.Run.CleanupPending = true
        return result, fault.New("CLEAN_UNCONFIRMED", "clean_unconfirmed", nil)
    }
    result.Cleaned = true
    result.Run.CleanedAt = new(time.Now().UTC())
    return result, nil
}

func TestCleanRequiresExplicitIDsAndNoninteractiveConsent(t *testing.T) {
    o, remote := runFixture(t)
    if code, out, stderr := run(t, o, "run", "--json", "--", "python", "train.py"); code != 0 {
        t.Fatal(code, out, stderr)
    }
    cleaner := &fakeCleaner{}
    o.Cleaner = cleaner
    for _, args := range [][]string{{"clean"}, {"clean", "--yes"}, {"clean", remote.req.RunID}, {"clean", remote.req.RunID, "--json"}, {"clean", remote.req.RunID, "--yes", "--target", "lab"}} {
        code, out, stderr := run(t, o, append(args, "--json")...)
        if code != 2 || stderr != "" || !strings.Contains(out, `"code":"BAD_CONFIG"`) || cleaner.previews != 0 || cleaner.cleans != 0 {
            t.Fatal(args, code, out, stderr, cleaner)
        }
    }
    for _, language := range []string{"en", "zh-Hans"} {
        code, out, stderr := run(t, o, "--lang", language, "clean", remote.req.RunID, remote.req.RunID, "--dry-run", "--json")
        var response struct {
            Data cleanBatch `json:"data"`
        }
        if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &response) != nil || !response.Data.DryRun || len(response.Data.Runs) != 1 || !response.Data.Runs[0].Eligible || response.Data.Runs[0].Cleaned || cleaner.cleans != 0 {
            t.Fatal(code, out, stderr)
        }
        code, out, stderr = run(t, o, "--lang", language, "clean", "--help")
        if code != 0 || stderr != "" || !strings.Contains(out, "--dry-run") || !strings.Contains(out, "--yes") || strings.Contains(out, "--target") {
            t.Fatal(code, out, stderr)
        }
    }
}

func TestCleanHumanConfirmationAndFinitePartialJSON(t *testing.T) {
    o, remote := runFixture(t)
    if code, out, stderr := run(t, o, "run", "--json", "--", "python", "train.py"); code != 0 {
        t.Fatal(code, out, stderr)
    }
    cleaner := &fakeCleaner{}
    o.Cleaner, o.Interactive = cleaner, true
    for _, language := range []string{"en", "zh-Hans"} {
        o.In = strings.NewReader("no\n")
        code, out, stderr := run(t, o, "-i", "--lang", language, "clean", remote.req.RunID)
        if code != 1 || cleaner.cleans != 0 || !strings.Contains(stderr, "[y/N]") || !strings.Contains(out+stderr, remote.req.RunID) {
            t.Fatal(code, out, stderr, cleaner)
        }
    }
    o.In = strings.NewReader("yes\n")
    code, out, stderr := run(t, o, "-i", "clean", remote.req.RunID)
    if code != 0 || cleaner.cleans != 1 || !strings.Contains(out, "data cleaned") || strings.Count(stderr, "[y/N]") != 1 {
        t.Fatal(code, out, stderr, cleaner)
    }
    cleaner.failClean = true
    code, out, stderr = run(t, o, "clean", remote.req.RunID, "--yes", "--json")
    if code != 3 || stderr != "" || !strings.Contains(out, `"cleanup_pending":true`) || !strings.Contains(out, `"code":"CLEAN_UNCONFIRMED"`) || strings.Count(strings.TrimSpace(out), "\n") != 0 {
        t.Fatal(code, out, stderr)
    }
    cleaner.failPreview = true
    before := cleaner.cleans
    code, out, stderr = run(t, o, "clean", remote.req.RunID, "--yes", "--json")
    if code != 1 || stderr != "" || !strings.Contains(out, `"eligible":false`) || cleaner.cleans != before {
        t.Fatal(code, out, stderr)
    }
}

func TestCleanBatchValidatesBeforeDeletionAndRetainsPartialSuccess(t *testing.T) {
    o, remote := runFixture(t)
    var ids []string
    for n := 0; n < 2; n++ {
        if code, out, stderr := run(t, o, "run", "--json", "--", "python", "train.py"); code != 0 {
            t.Fatal(code, out, stderr)
        }
        ids = append(ids, remote.req.RunID)
    }
    cleaner := &fakeCleaner{blockedID: ids[1]}
    o.Cleaner = cleaner
    code, out, stderr := run(t, o, "clean", ids[0], ids[1], "--yes", "--json")
    if code != 1 || stderr != "" || cleaner.cleans != 0 || cleaner.previews != 2 {
        t.Fatal(code, out, stderr, cleaner)
    }
    cleaner.blockedID, cleaner.unconfirmedID = "", ids[1]
    code, out, stderr = run(t, o, "clean", ids[0], ids[1], "--yes", "--json")
    var response struct {
        Data cleanBatch `json:"data"`
    }
    if code != 3 || stderr != "" || json.Unmarshal([]byte(out), &response) != nil || len(response.Data.Runs) != 2 || !response.Data.Runs[0].Cleaned || response.Data.Runs[1].Cleaned || response.Data.Runs[1].ErrorCode != "CLEAN_UNCONFIRMED" || !response.Data.Runs[1].Run.CleanupPending {
        t.Fatal(code, out, stderr)
    }
}
