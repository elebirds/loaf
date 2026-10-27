package cli

import (
    "context"
    "encoding/json"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cloud"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/wire"
)

func TestNamedRunsAndLocalLabelFilters(t *testing.T) {
    o, remote := runFixture(t)
    o.Runs.Metadata = o.State
    code, out, stderr := run(t, o, "run", "--name", "中文 A", "--tag", "seed1", "--tag", "ab", "--tag", "seed1", "--json", "--", "python", "--name", "literal")
    var accepted struct {
        Data execution.View `json:"data"`
    }
    if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &accepted) != nil || accepted.Data.Name != "中文 A" || len(accepted.Data.Tags) != 2 {
        t.Fatal(code, out, stderr)
    }
    if len(remote.req.Args) != 3 || remote.req.Args[1] != "--name" {
        t.Fatal(remote.req)
    }
    for _, local := range []bool{false, true} {
        args := []string{"ps", "--name", "中文 A", "--tag", "ab", "--tag", "seed1", "--json"}
        if local {
            args = append(args, "--local")
        }
        code, out, stderr = run(t, o, args...)
        var list struct {
            Data execution.History `json:"data"`
        }
        if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &list) != nil || len(list.Data.Runs) != 1 || list.Data.Runs[0].ID != accepted.Data.ID {
            t.Fatal(code, out, stderr)
        }
        if local && (list.Data.Source != "local" || list.Data.Runs[0].Status != "unknown") {
            t.Fatal(out)
        }
    }
    if code, out, stderr := run(t, o, "ps", "--local", "--tag", "absent", "--json"); code != 0 || stderr != "" || !strings.Contains(out, `"runs":[]`) {
        t.Fatal(code, out, stderr)
    }
}

func TestOfflineInspectionSurvivesVersionAndAliasChangesWithoutSSH(t *testing.T) {
    o, remote, refs := psFixture(t)
    old := refs[0]
    old.ID = "r_aaaaaaaaaaaaaaaaaaaaaaaa"
    old.Version = "older-version"
    old.Connection.Cloud = &cloud.Binding{Ref: cloud.Ref{Provider: "autodl", Product: "standard", Account: "original", ID: "instance-original"}, Settings: cloud.Account{Credential: cloud.CredentialRef{Mode: "session", Key: "must-not-show-credential-reference", Store: "keyring"}}}
    old.Connection.Args = []string{"-i", "must-not-show-private-key-path"}
    if err := o.State.SaveRun(context.Background(), old); err != nil {
        t.Fatal(err)
    }
    if code, _, _ := run(t, o, "remove", "lab", "--json"); code != 0 {
        t.Fatal(code)
    }
    o.Runs = nil
    for _, lang := range []string{"en", "zh-Hans"} {
        for _, args := range [][]string{{"inspect", old.ID}, {"ps", old.ID, "--local"}} {
            code, out, stderr := run(t, o, append(args, "--lang", lang, "--json")...)
            var r struct {
                Data execution.LocalDetails `json:"data"`
            }
            if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &r) != nil || r.Data.Version != old.Version || r.Data.ClientVersion != wire.Version || r.Data.Compatible || r.Data.Status != "unknown" || r.Data.Instance == nil || r.Data.Instance.ID != "instance-original" || strings.Contains(out, "must-not-show") {
                t.Fatal(code, out, stderr)
            }
            code, out, stderr = run(t, o, append(args, "--lang", lang)...)
            if code != 0 || stderr != "" || !strings.Contains(out, "instance-original") || !strings.Contains(out, old.Version) || strings.Contains(out, "must-not-show") {
                t.Fatal(code, out, stderr)
            }
        }
    }
    if remote.calls != 0 {
        t.Fatal("offline command contacted a machine", remote.calls)
    }
    for _, args := range [][]string{{"inspect", "missing"}, {"ps", "missing", "--local"}} {
        if code, out, stderr := run(t, o, append(args, "--json")...); code != 1 || stderr != "" || !strings.Contains(out, `"code":"RUN_NOT_FOUND"`) || strings.Contains(out, `"data"`) {
            t.Fatal(args, code, out, stderr)
        }
    }
    for _, args := range [][]string{{"ps", "--local", "--status", "running"}, {"ps", old.ID, "--local", "--wait", "1s"}, {"ps", old.ID, "--local", "--tag", "x"}, {"inspect", old.ID, "--target", "other"}} {
        if code, out, stderr := run(t, o, append(args, "--json")...); code != 2 || stderr != "" || !json.Valid([]byte(out)) {
            t.Fatal(args, code, out, stderr)
        }
    }
}

func TestOfflinePreparationIdentityDoesNotReplaceLatest(t *testing.T) {
    o, remote, refs := psFixture(t)
    preparation := refs[0]
    preparation.ID = "r_cccccccccccccccccccccccc"
    preparation.Submission, preparation.AttemptedAt = "preparing", nil
    if err := o.State.SaveRun(context.Background(), preparation); err != nil {
        t.Fatal(err)
    }
    o.Runs = nil
    for _, lang := range []string{"en", "zh-Hans"} {
        for _, args := range [][]string{{"inspect", preparation.ID}, {"ps", preparation.ID, "--local"}} {
            code, out, stderr := run(t, o, append(args, "--lang", lang, "--json")...)
            var result struct {
                Data execution.LocalDetails `json:"data"`
            }
            if code != 0 || stderr != "" || json.Unmarshal([]byte(out), &result) != nil || result.Data.ID != preparation.ID || result.Data.Submission != "preparing" || result.Data.Status != "unknown" || result.Data.AttemptedAt != nil {
                t.Fatal(code, out, stderr)
            }
            code, out, stderr = run(t, o, append(args, "--lang", lang)...)
            label := "preparation record"
            if lang == "zh-Hans" {
                label = "准备记录"
            }
            if code != 0 || stderr != "" || !strings.Contains(out, label) {
                t.Fatal(code, out, stderr)
            }
        }
    }
    code, out, stderr := run(t, o, "ps", "--local", "--json")
    if code != 0 || stderr != "" || strings.Contains(out, preparation.ID) || remote.calls != 0 {
        t.Fatal("preparation changed navigation or contacted remote", code, out, stderr, remote.calls)
    }
    service := execution.Service{Index: o.State}
    latest, err := service.Select(preparation.ProjectID, "")
    if err != nil || latest.ID != refs[0].ID {
        t.Fatal("preparation replaced latest", latest, err)
    }
}

func TestVersionMismatchHasStructuredVersions(t *testing.T) {
    o, _, refs := psFixture(t)
    ref := refs[0]
    ref.ID = "r_bbbbbbbbbbbbbbbbbbbbbbbb"
    ref.Version = "old-build"
    if err := o.State.SaveRun(context.Background(), ref); err != nil {
        t.Fatal(err)
    }
    for _, lang := range []string{"en", "zh-Hans"} {
        code, out, stderr := run(t, o, "ps", ref.ID, "--lang", lang, "--json")
        var r struct {
            Error errorView      `json:"error"`
            Data  execution.View `json:"data"`
        }
        if code != 1 || stderr != "" || json.Unmarshal([]byte(out), &r) != nil || r.Error.ActualVersion != "old-build" || r.Error.ExpectedVersion != wire.Version || r.Data.Version != "old-build" {
            t.Fatal(code, out, stderr)
        }
    }
}
