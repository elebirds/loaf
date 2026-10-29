package cli

import (
    "bytes"
    "context"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/i18n"
)

func TestCloudRoadmapShowsFutureStepsAndCurrentPosition(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        l, _ := i18n.New(language, nil)
        for current, step := range []string{"mode", "compute", "candidate", "environment", "approval", "acquisition", "connection"} {
            text := cloudRoadmap(l, current)
            if strings.Count(text, "● ") != 1 || strings.Count(text, "✓ ") != current || strings.Count(text, "○ ") != 6-current || !strings.Contains(text, "● "+l.Text("cloud_flow_"+step)) {
                t.Fatal(text)
            }
            for _, id := range []string{"mode", "compute", "candidate", "environment", "approval", "acquisition", "connection"} {
                if !strings.Contains(text, l.Text("cloud_flow_"+id)) {
                    t.Fatal("missing future step", text, id)
                }
            }
        }
    }
}

func TestCloudWizardEntryAndBackNavigationKeepRoadmap(t *testing.T) {
    o, p := uxFixture(t)
    o.Interactive = true
    // Enter the compute page, return to the method page, then cancel.
    o.In = strings.NewReader("\n1\n\n\n\n2\n")
    code, _, transcript := run(t, o, "--lang", "zh-Hans", "cloud", "acquire")
    if code == 0 || p.creates != 0 || strings.Count(transcript, "● 方式") != 2 || !strings.Contains(transcript, "✓ 方式 → ● 算力") || !strings.Contains(transcript, "○ 获取 → ○ 接入") {
        t.Fatal(code, transcript, p.creates)
    }
}

func TestCloudWizardApprovalKeepsFutureAcquisitionAndConnectionVisible(t *testing.T) {
    l, _ := i18n.New("zh-Hans", nil)
    var output bytes.Buffer
    c := invocation{output: &presenter{locale: l, err: &output, cloudWizard: true}, input: interaction.New(interaction.Options{Interactive: true, In: strings.NewReader("no\n"), Err: &output})}
    plan := cloud.Plan{Product: "standard", Requirements: cloud.Requirements{GPUCount: 1, GPUModels: []string{"RTX 4090"}}}
    _ = c.approveCloud(context.Background(), client.CloudApproval{Operation: "acquire", Account: "fixture", Product: "standard", Target: "gpu", Plan: &plan})
    if !strings.Contains(output.String(), "● 授权 → ○ 获取 → ○ 接入") {
        t.Fatal(output.String())
    }
}

func TestCloudWizardProgressDistinguishesAcquisitionFromConnection(t *testing.T) {
    l, _ := i18n.New("zh-Hans", nil)
    c := &invocation{output: &presenter{locale: l, err: &bytes.Buffer{}, cloudWizard: true, command: "cloud acquire"}, grammar: &grammar{}}
    f := cloudFeedback{call: c, mode: "plain", phase: "waiting"}
    if err := f.emit(cloud.Event{Type: "waiting"}); err != nil {
        t.Fatal(err)
    }
    if view := f.frame("waiting", nil).Document.Context; !strings.Contains(view, "● 获取 → ○ 接入") {
        t.Fatal(view)
    }
    if err := f.emit(cloud.Event{Type: "connecting"}); err != nil {
        t.Fatal(err)
    }
    if view := f.frame("connecting", nil).Document.Context; !strings.Contains(view, "✓ 获取 → ● 接入") {
        t.Fatal(view)
    }
}
