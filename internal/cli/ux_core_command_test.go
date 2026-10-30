package cli

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "os"
    "reflect"
    "regexp"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
    "mvdan.cc/sh/v3/shell"
)

func TestUXCoreBareEntryRetainsExplicitDefaultOptions(t *testing.T) {
    for _, test := range []struct {
        args []string
        hub  string
    }{
        {[]string{"machine"}, "machine"},
        {[]string{"--lang", "zh-Hans", "machine"}, "machine"},
        {[]string{"queue"}, "queue"},
        {[]string{"queue", "--target", "temporary"}, "queue"},
        {[]string{"machine", "list"}, ""},
        {[]string{"machine", "--trace=false"}, ""},
        {[]string{"queue", "list"}, ""},
        {[]string{"queue", "--limit", "100"}, ""},
        {[]string{"queue", "--limit=0"}, ""},
        {[]string{"queue", "--trace=false"}, ""},
        {[]string{"queue", "--json"}, ""},
        {[]string{"queue", "--help"}, ""},
        {[]string{"help", "queue"}, ""},
    } {
        parser, err := newParser(&grammar{})
        if err != nil {
            t.Fatal(err)
        }
        test.args = append([]string{"-i"}, test.args...)
        early := bootstrap(parser.Model, test.args)
        for _, command := range []string{"machine", "queue"} {
            if got := uxBareEntry(parser, test.args, early, command); got != (test.hub == command) {
                t.Fatal(test.args, command, got)
            }
        }
    }
}

func TestUXCoreSyncCancellationKeepsDirectContractBeforeInputOwnership(t *testing.T) {
    for _, language := range []string{"en", "zh-Hans"} {
        for _, args := range [][]string{{"sync", "--dry-run", "--json"}, {"sync", "--target", "lab", "--json"}} {
            o := fixture(t)
            sender := &fakeSyncer{}
            o.Syncer, o.In = sender, uxCoreNoInput{t}
            out, diagnostic := &bytes.Buffer{}, &bytes.Buffer{}
            o.Out, o.Err = out, diagnostic
            ctx, cancel := context.WithCancel(context.Background())
            cancel()
            code := execute(ctx, append([]string{"--lang", language}, args...), o)
            var value envelope
            if err := json.Unmarshal(out.Bytes(), &value); err != nil || code != 1 || value.Error == nil || value.Error.Code != "SYNC_CANCELED" || diagnostic.Len() != 0 || sender.Calls != 0 {
                t.Fatal(args, code, out, diagnostic, sender, err)
            }
            if _, err := os.Stat(o.State.Path); !os.IsNotExist(err) {
                t.Fatal("canceled sync created state", err)
            }
        }
    }
}

// Plain menus write their complete choices before reading. Resolve the actual
// displayed label rather than freezing integration-sensitive menu numbers.
type uxCoreMenuReader struct {
    output  *bytes.Buffer
    labels  []string
    pending []byte
}

func (r *uxCoreMenuReader) Read(p []byte) (int, error) {
    if len(r.pending) == 0 {
        if len(r.labels) == 0 {
            return 0, io.EOF
        }
        pattern := regexp.MustCompile(`(?m)^\s*\*?\s*(\d+)\.\s*` + regexp.QuoteMeta(r.labels[0]) + `\s*$`)
        rows := pattern.FindAllStringSubmatch(r.output.String(), -1)
        if len(rows) == 0 {
            return 0, errors.New("expected menu label was not displayed")
        }
        r.labels = r.labels[1:]
        r.pending = []byte(rows[len(rows)-1][1] + "\n")
    }
    n := copy(p, r.pending)
    r.pending = r.pending[n:]
    return n, nil
}

func TestUXCoreCloudActionsConsumeCheckedPublicArgv(t *testing.T) {
    c := uxCoreCall(t, "")
    receipt := cloud.Receipt{ID: "# saved request", Operation: "acquire", Status: "uncertain"}
    cause := fault.New("CLOUD_UNCONFIRMED", "internal_error", nil)
    if err := c.cloudNavigation("cloud acquire", receipt, cause); err != nil {
        t.Fatal(err)
    }
    if len(c.output.nextActions) != 1 {
        t.Fatal(c.output.nextActions)
    }
    next := c.output.nextActions[0]
    argv := []string{"loaf", "cloud", "requests", receipt.ID}
    if next.Kind != "inspect_request" || next.RequiresConfirmation || !reflect.DeepEqual(next.Command, argv) {
        t.Fatal(next)
    }
    if err := c.output.printCloudActions(receipt); err != nil {
        t.Fatal(err)
    }
    display, err := uxJCommandDisplay(argv)
    if err != nil || !strings.Contains(c.options.Out.(*bytes.Buffer).String(), display) {
        t.Fatal(display, err, c.options.Out)
    }
    read, err := shell.Fields(display, func(string) string { return "must not expand" })
    if err != nil || !reflect.DeepEqual(read, argv) {
        t.Fatal(display, read, err)
    }
}

func TestUXCoreActionFailureRetainsOriginalCloudResultAndCode(t *testing.T) {
    c := uxCoreCall(t, "")
    receipt := cloud.Receipt{ID: "invalid\nrequest", Operation: "acquire", Status: "uncertain"}
    cause := fault.New("CLOUD_UNCONFIRMED", "internal_error", nil)
    actionErr := c.cloudNavigation("cloud acquire", receipt, cause)
    if actionErr == nil || len(c.output.nextActions) != 0 || strings.Contains(actionErr.Error(), receipt.ID) {
        t.Fatal(actionErr, c.output.nextActions)
    }
    c.output.json, c.output.trace = true, &ssh.Trace{}
    if code := c.output.finish(receipt, errors.Join(cause, actionErr)); code != 3 {
        t.Fatal(code)
    }
    var value envelope
    if err := json.Unmarshal(c.options.Out.(*bytes.Buffer).Bytes(), &value); err != nil || value.Error == nil || value.Error.Code != "CLOUD_UNCONFIRMED" || value.Data == nil || len(value.NextActions) != 0 {
        t.Fatal(value, err)
    }
}

func TestUXCoreLogRecoveryUsesCheckedDisplayWithoutFallback(t *testing.T) {
    c := uxCoreCall(t, "")
    action := uxNextAction{Kind: "fresh_tail", Command: []string{"loaf", "logs", "# saved run", "-f"}}
    if err := c.uxELogCommand(action); err != nil {
        t.Fatal(err)
    }
    display, _ := uxJCommandDisplay(action.Command)
    output := c.options.Err.(*bytes.Buffer)
    if !strings.Contains(output.String(), display) {
        t.Fatal(output)
    }
    output.Reset()
    action.Command[2] = "private\x1b[31m"
    if err := c.uxELogCommand(action); err == nil || output.Len() != 0 || strings.Contains(err.Error(), "private") {
        t.Fatal(err, output)
    }
}

func TestUXCoreCloudInfoUsesSelectedFrozenBinding(t *testing.T) {
    c := uxCoreCall(t, "")
    frozen := cloud.Binding{
        Ref:      cloud.Ref{Provider: "autodl", Product: "pro", Account: "lab", ID: "original"},
        Settings: cloud.Account{Provider: "autodl", DefaultProduct: "pro", Credential: cloud.CredentialRef{Mode: "developer", Env: "FROZEN_TOKEN"}},
    }
    replacement := frozen
    replacement.Ref.ID, replacement.Ref.Product = "replacement", "standard"
    replacement.Settings.Credential.Env = "REPLACEMENT_TOKEN"
    c.app.Resources.Settings.Cloud.DefaultAccount = "different"
    c.app.Resources.Settings.Cloud.Accounts = map[string]cloud.Account{"lab": replacement.Settings}
    c.app.Resources.Settings.Targets = map[string]config.Target{"gpu": {Cloud: &replacement}}
    provider := &cloudProvider{}
    opened := 0
    c.app.Resources.Cloud = &cloud.Service{Open: func(_ context.Context, name string, settings cloud.Account, product string) (cloud.Provider, error) {
        opened++
        if name != frozen.Ref.Account || product != frozen.Ref.Product || !reflect.DeepEqual(settings, frozen.Settings) {
            t.Fatal("frozen selection was redirected", name, settings, product)
        }
        return provider, nil
    }}
    data, err := c.uxNavigate(context.Background(), uxRoute{Kind: "cloud-info", Binding: &frozen})
    if err != nil || data.(cloud.Instance).Ref != frozen.Ref || provider.observed != frozen.Ref || opened != 1 || provider.creates != 0 {
        t.Fatal(data, err, provider, opened)
    }
    if c.app.Resources.Settings.Targets["gpu"].Cloud.Ref != replacement.Ref || c.app.Resources.Settings.Cloud.DefaultAccount != "different" {
        t.Fatal("navigation rewrote current preferences")
    }
    invalid := frozen
    invalid.Ref.ID = ""
    if _, err := c.uxNavigate(context.Background(), uxRoute{Kind: "cloud-info", Binding: &invalid}); err == nil || opened != 1 {
        t.Fatal("invalid binding reached a provider", err, opened)
    }
    target := "replacement"
    for _, route := range []uxRoute{{Kind: "cloud-info"}, {Kind: "cloud-info", Binding: &frozen, Target: &target}, {Kind: "cloud", Binding: &frozen}} {
        if _, err := c.uxNavigate(context.Background(), route); err == nil || opened != 1 {
            t.Fatal("conflicting binding reached a provider", route, err, opened)
        }
    }
    // Consume the real A detail action, not only its standalone route.
    if err := c.app.Machines.Config.Add(context.Background(), "gpu", config.Target{Cloud: &frozen, RemoteBase: "~/projects"}, false); err != nil {
        t.Fatal(err)
    }
    if err := c.uxARefreshSettings(); err != nil {
        t.Fatal(err)
    }
    reader := &uxCoreMenuReader{output: c.options.Err.(*bytes.Buffer), labels: []string{
        c.output.locale.Text("ux_core_cloud_instance_info"), c.output.locale.Text("ux_core_back"), c.output.locale.Text("ux_core_back"),
    }}
    c.input = interaction.New(interaction.Options{Interactive: true, In: reader, Err: c.options.Err})
    if err := c.uxAMachineDetail(context.Background(), "gpu"); err != nil || opened != 2 || provider.creates != 0 || len(reader.labels) != 0 {
        t.Fatal("machine detail did not observe its original cloud instance", err, opened, reader.labels, c.options.Err)
    }
}
