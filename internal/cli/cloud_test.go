package cli

import (
    "context"
    "encoding/json"
    "errors"
    "os"
    "path/filepath"
    "reflect"
    "strings"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
)

type cloudProvider struct {
    creates, gets int
    observed      cloud.Ref
    createError   error
    getError      error
}

func (p *cloudProvider) List(context.Context, int) (cloud.Page, error) {
    return cloud.Page{Instances: []cloud.Instance{}, Complete: true}, nil
}
func (p *cloudProvider) Get(_ context.Context, ref cloud.Ref) (cloud.Instance, error) {
    p.gets++
    p.observed = ref
    return cloud.Instance{Ref: ref, Status: "running"}, p.getError
}
func (p *cloudProvider) Connection(_ context.Context, ref cloud.Ref) (cloud.Connection, error) {
    p.observed = ref
    return cloud.Connection{Host: "gpu.example.com", Port: 2200, User: "root", Password: "fixture-ssh-secret"}, nil
}
func (p *cloudProvider) Create(context.Context, cloud.Plan, *cloud.Offer) (cloud.Ref, error) {
    p.creates++
    return cloud.Ref{Provider: "autodl", Product: "pro", Account: "demo", ID: "pro-created"}, p.createError
}
func (*cloudProvider) Start(context.Context, cloud.Ref) error { return nil }
func (*cloudProvider) Stop(context.Context, cloud.Ref) error  { return nil }

type cloudStock struct{ *cloudProvider }

func (*cloudStock) Search(context.Context, cloud.Plan, int) (cloud.InventoryPage, error) {
    return cloud.InventoryPage{Offers: []cloud.Offer{}, Complete: true, ObservedAt: time.Now().UTC()}, nil
}

func cloudFixture(t *testing.T) (Options, *cloudProvider) {
    t.Helper()
    o := fixture(t)
    p := &cloudProvider{}
    o.Cloud = DefaultCloudService(o.Store, func(context.Context, string, cloud.Account, string) (cloud.Provider, error) { return p, nil })
    o.Operations = &fakeOperations{}
    for _, args := range [][]string{{"cloud", "account", "add", "demo", "--product", "pro", "--auth", "developer", "--token-env", "TEST_TOKEN", "--default", "--json"}, {"cloud", "account", "add", "ordinary", "--product", "standard", "--token-env", "TEST_WEB_TOKEN", "--json"}} {
        code, out, err := run(t, o, args...)
        if code != 0 || err != "" {
            t.Fatal(code, out, err)
        }
    }
    return o, p
}

func TestCloudJSONConsentAndRedaction(t *testing.T) {
    o, p := cloudFixture(t)
    o.Interactive = true
    o.In = strings.NewReader("yes\n")
    args := []string{"cloud", "acquire", "gpu", "--image", "image-fixture", "--gpu-spec", "spec", "--cuda-from", "118", "--allow-unquoted", "--json"}
    code, out, diagnostic := run(t, o, args...)
    if code != 2 || diagnostic != "" || p.creates != 0 || !strings.Contains(out, "BAD_CONFIG") {
        t.Fatal(code, out, diagnostic, p.creates)
    }
    code, out, diagnostic = run(t, o, "cloud", "credentials", "pro-existing", "--json")
    if code != 0 || diagnostic != "" || strings.Contains(out, "fixture-ssh-secret") {
        t.Fatal(code, out, diagnostic)
    }
    code, out, _ = run(t, o, "cloud", "credentials", "pro-existing", "--json", "--show-password")
    if code != 0 || !strings.Contains(out, "fixture-ssh-secret") {
        t.Fatal(code, out)
    }
}

func TestCloudAcquireBindsOneResourceAndUsesFrozenIdentity(t *testing.T) {
    o, p := cloudFixture(t)
    args := []string{"cloud", "acquire", "gpu", "--image", "image-fixture", "--gpu-spec", "spec", "--cuda-from", "118", "--allow-unquoted", "--ready-timeout", "0s", "--yes", "--json"}
    code, out, diagnostic := run(t, o, args...)
    if code != 0 || diagnostic != "" || p.creates != 1 || !strings.Contains(out, `"status":"ready"`) {
        t.Fatal(code, out, diagnostic)
    }
    var e struct{ Data cloudBinding }
    if err := json.Unmarshal([]byte(out), &e); err != nil {
        t.Fatal(err)
    }
    cfg, err := o.Store.Load()
    if err != nil {
        t.Fatal(err)
    }
    target := cfg.Targets["gpu"]
    if target.Cloud == nil || target.Destination != "" || target.Cloud.Ref.ID != "pro-created" || target.Cloud.Settings.Credential.Env != "TEST_TOKEN" {
        t.Fatal(target)
    }
    // A second invocation may not rent another instance for an existing alias.
    code, _, _ = run(t, o, args...)
    if code != 2 || p.creates != 1 {
        t.Fatal(code, p.creates)
    }
    // Changing/removing an account alias cannot redirect the bound instance.
    code, out, _ = run(t, o, "cloud", "account", "add", "demo", "--product", "standard", "--token-env", "OTHER_TOKEN", "--replace", "--json")
    if code != 0 {
        t.Fatal(out)
    }
    code, out, _ = run(t, o, "cloud", "info", "gpu", "--json")
    if code != 0 || p.observed.Product != "pro" || p.observed.ID != "pro-created" {
        t.Fatal(out, p.observed)
    }
    data, _ := os.ReadFile(o.Store.ConfigFile)
    if strings.Contains(string(data), "fixture-ssh-secret") || strings.Contains(string(data), "password") {
        t.Fatal("persisted password")
    }
    if e.Data.Request == nil {
        t.Fatal(out)
    }
}

func TestCloudUnknownAcquisitionReturnsReceiptAndExitThree(t *testing.T) {
    o, p := cloudFixture(t)
    p.createError = &cloud.APIError{Code: "TRANSPORT", Unconfirmed: true}
    code, out, diagnostic := run(t, o, "cloud", "acquire", "gpu", "--image", "image-fixture", "--gpu-spec", "spec", "--cuda-from", "118", "--allow-unquoted", "--yes", "--json")
    if code != 3 || diagnostic != "" || !strings.Contains(out, "CLOUD_UNCONFIRMED") || p.creates != 1 {
        t.Fatal(code, out, diagnostic)
    }
    var e struct{ Data cloud.Receipt }
    if json.Unmarshal([]byte(out), &e) != nil || e.Data.ID == "" {
        t.Fatal(out)
    }
    code, out, _ = run(t, o, "cloud", "reconcile", e.Data.ID, "--instance", "pro-created", "--json")
    if code != 0 || p.creates != 1 || !strings.Contains(out, `"status":"created"`) {
        t.Fatal(code, out)
    }
    code, out, _ = run(t, o, "cloud", "bind", "gpu", "--request", e.Data.ID, "--ready-timeout", "0s", "--json")
    if code != 0 || p.creates != 1 {
        t.Fatal(code, out)
    }
}

func TestCloudProfileAndDirectFlagsMergeWithoutPersistingSecrets(t *testing.T) {
    o, p := cloudFixture(t)
    if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte(`[resources.train]
gpu_count = 2
min_memory_gib = 64
gpu_models = ['one', 'two']
regions = ['west']
max_compute_hourly_price = '2.5'
wait_timeout = '30m'
[resources.train.autodl]
image = 'fixture-image'
`), 0600); err != nil {
        t.Fatal(err)
    }
    o.Cloud.Open = func(context.Context, string, cloud.Account, string) (cloud.Provider, error) {
        return &cloudStock{p}, nil
    }
    code, out, diagnostic := run(t, o, "cloud", "search", "--account", "ordinary", "--profile", "train", "--gpu-count", "1", "--gpu-model", "other", "--min-memory", "0GiB", "--wait", "0s", "--json")
    if code != 0 || diagnostic != "" {
        t.Fatal(code, out, diagnostic)
    }
    var e struct{ Data cloud.SearchResult }
    if err := json.Unmarshal([]byte(out), &e); err != nil {
        t.Fatal(err)
    }
    r := e.Data.Plan.Requirements
    if r.GPUCount != 1 || r.MinMemoryGiB != 0 || e.Data.Plan.WaitTimeout != "0s" || !reflect.DeepEqual(r.GPUModels, []string{"other"}) || !reflect.DeepEqual(r.Regions, []string{"west"}) {
        t.Fatal(e)
    }
    if _, err := os.Stat(o.Store.IndexFile); !errors.Is(err, os.ErrNotExist) {
        t.Fatal("read-only search created project state", err)
    }
}

func TestCloudWatchJSONHasChangeAndEndWithoutMutation(t *testing.T) {
    o, p := cloudFixture(t)
    o.Cloud.Open = func(context.Context, string, cloud.Account, string) (cloud.Provider, error) {
        return &cloudStock{p}, nil
    }
    o.Cloud.Wait = func(context.Context, time.Duration) error { return context.DeadlineExceeded }
    code, out, diagnostic := run(t, o, "cloud", "watch", "--wait", "1m", "--json")
    if code != 0 || diagnostic != "" || p.creates != 0 {
        t.Fatal(code, out, diagnostic)
    }
    rows := strings.Split(strings.TrimSpace(out), "\n")
    if len(rows) != 2 || !strings.Contains(rows[0], `"type":"change"`) || !strings.Contains(rows[1], `"type":"end"`) {
        t.Fatal(out)
    }
    for _, row := range rows {
        var e envelope
        if err := json.Unmarshal([]byte(row), &e); err != nil || e.SchemaVersion != 1 {
            t.Fatal(row, err)
        }
    }
}

func TestCloudHelpBothLanguagesAndFlagErrors(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        for _, args := range [][]string{{"cloud", "--help"}, {"cloud", "acquire", "--help"}, {"help", "cloud", "account", "add"}, {"cloud", "account", "add", "--help"}, {"cloud", "watch", "--help"}, {"cloud", "bind", "--help"}} {
            o, _ := cloudFixture(t)
            code, out, err := run(t, o, append([]string{"--lang", lang}, args...)...)
            if code != 0 || out == "" || err != "" {
                t.Fatal(args, code, out, err)
            }
        }
        o, p := cloudFixture(t)
        for _, args := range [][]string{{"cloud", "acquire", "gpu", "--gpu-count", "0", "--yes"}, {"cloud", "search", "--min-gpu-memory", "24GB"}, {"cloud", "search", "--poll-interval", "1s"}, {"cloud", "list", "--target", "demo"}} {
            code, out, _ := run(t, o, append([]string{"--lang", lang, "--json"}, args...)...)
            if code != 2 || p.creates != 0 {
                t.Fatal(code, out, args)
            }
        }
    }
    o, _ := cloudFixture(t)
    code, out, _ := run(t, o, "cloud", "account", "add", "bad", "--product", "pro", "--auth", "developer", "--token-env", "--json")
    if code != 2 || strings.HasPrefix(out, "{") {
        t.Fatal("flag value changed global JSON mode", code, out)
    }
}

func TestProjectDefaultsOverrideUserNamedProfile(t *testing.T) {
    o, p := cloudFixture(t)
    file, err := os.OpenFile(o.Store.ConfigFile, os.O_APPEND|os.O_WRONLY, 0600)
    if err != nil {
        t.Fatal(err)
    }
    _, err = file.WriteString("\n[resources.train]\nmin_memory_gib = 64\n")
    if closeErr := file.Close(); err == nil {
        err = closeErr
    }
    if err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte("[resources.default]\nmin_memory_gib = 32\n"), 0600); err != nil {
        t.Fatal(err)
    }
    o.Cloud.Open = func(context.Context, string, cloud.Account, string) (cloud.Provider, error) {
        return &cloudStock{p}, nil
    }
    code, out, diagnostic := run(t, o, "cloud", "search", "--profile", "train", "--json")
    var e struct{ Data cloud.SearchResult }
    if code != 0 || diagnostic != "" || json.Unmarshal([]byte(out), &e) != nil || e.Data.Plan.Requirements.MinMemoryGiB != 32 {
        t.Fatal(code, out, diagnostic)
    }
}

func TestCloudProductSelectionDoesNotReinterpretCredentials(t *testing.T) {
    o, p := cloudFixture(t)
    initial, err := o.Store.Load()
    if err != nil {
        t.Fatal(err)
    }
    selected, mode, opened := "", "", 0
    o.Cloud.Open = func(_ context.Context, _ string, a cloud.Account, product string) (cloud.Provider, error) {
        selected, mode = product, a.Credential.Mode
        opened++
        return &cloudStock{p}, nil
    }
    if err := os.WriteFile(filepath.Join(o.CWD, "loaf.toml"), []byte("[resources.train]\nproduct = 'standard'\n"), 0600); err != nil {
        t.Fatal(err)
    }
    for _, tc := range []struct {
        args          []string
        product, auth string
    }{
        {[]string{"cloud", "search"}, "pro", "developer"},
        {[]string{"cloud", "search", "--profile", "train"}, "standard", "developer"},
        {[]string{"cloud", "search", "--profile", "train", "--product", "pro"}, "pro", "developer"},
        {[]string{"cloud", "list", "--account", "ordinary", "--product", "pro"}, "pro", "session"},
    } {
        code, out, diagnostic := run(t, o, append(tc.args, "--json")...)
        if code != 0 || diagnostic != "" || selected != tc.product || mode != tc.auth {
            t.Fatal(tc, code, out, diagnostic, selected, mode)
        }
    }
    before := opened
    for _, product := range []string{"", "automatic", "elastic"} {
        code, out, _ := run(t, o, "cloud", "list", "--product", product, "--json")
        if code != 2 || opened != before {
            t.Fatal("invalid explicit product reached provider", code, out, opened)
        }
    }
    current, err := o.Store.Load()
    if err != nil || !reflect.DeepEqual(initial.Cloud.Accounts, current.Cloud.Accounts) {
        t.Fatal("operation product changed saved account preferences", current, err)
    }
}

func TestBoundProductWinsOverAccountDefaultAndRejectsRedirection(t *testing.T) {
    o, p := cloudFixture(t)
    cfg, err := o.Store.Load()
    if err != nil {
        t.Fatal(err)
    }
    a := cfg.Cloud.Accounts["demo"]
    a.DefaultProduct = "standard"
    b := cloud.Binding{Ref: cloud.Ref{Provider: "autodl", Product: "pro", Account: "demo", ID: "pro-saved"}, Settings: a}
    if err := o.Store.Add(context.Background(), "gpu", config.Target{Cloud: &b, RemoteBase: "~/projects"}, false); err != nil {
        t.Fatal(err)
    }
    selected := ""
    o.Cloud.Open = func(_ context.Context, _ string, _ cloud.Account, product string) (cloud.Provider, error) {
        selected = product
        return p, nil
    }
    code, out, diagnostic := run(t, o, "cloud", "info", "gpu", "--json")
    if code != 0 || diagnostic != "" || selected != "pro" || p.observed != b.Ref {
        t.Fatal(code, out, diagnostic, selected, p.observed)
    }
    before := p.gets
    for _, operation := range []string{"info", "credentials", "start", "stop"} {
        code, out, _ := run(t, o, "cloud", operation, "gpu", "--product", "standard", "--json")
        if code != 2 || p.gets != before {
            t.Fatal("explicit product redirected a bound resource", operation, code, out)
        }
    }
}
