package cli

import (
    "bytes"
    "context"
    "crypto/sha1"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/http/httptest"
    "net/url"
    "os"
    "os/exec"
    "path/filepath"
    "runtime"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/autodl"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/credentials"
)

// Only test processes can redirect the fixed provider hosts to a loopback
// server. Production has no configurable credential destination or HTTP mode.
type loopbackCloudTransport struct{ endpoint *url.URL }

func (x loopbackCloudTransport) RoundTrip(r *http.Request) (*http.Response, error) {
    if r.URL.Scheme != "https" || r.URL.Host != "www.autodl.com" {
        return nil, fmt.Errorf("unexpected test credential destination")
    }
    copy := r.Clone(r.Context())
    address := *r.URL
    address.Scheme, address.Host = x.endpoint.Scheme, x.endpoint.Host
    copy.URL, copy.Host = &address, r.URL.Host
    return http.DefaultTransport.RoundTrip(copy)
}

func processAuthentication(store *config.Store) *cloud.Authentication {
    state := filepath.Dir(store.IndexFile)
    auth := &cloud.Authentication{
        Files:   credentials.File{Dir: filepath.Join(state, "credentials")},
        Keyring: credentials.File{Dir: filepath.Join(state, "test-vault")},
    }
    // Native Linux acceptance runs inside an isolated session bus/keyring.
    if runtime.GOOS == "linux" && os.Getenv("LOAF_TEST_NATIVE_VAULT") == "1" {
        auth.Keyring = credentials.Keyring{}
    }
    return auth
}

func TestCloudProcessHelper(t *testing.T) {
    root := os.Getenv("LOAF_TEST_CLOUD_ROOT")
    if root == "" {
        t.Skip("subprocess helper")
    }
    endpoint, err := url.Parse(os.Getenv("LOAF_TEST_CLOUD_HTTP"))
    if err != nil || endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" {
        t.Fatal("invalid loopback fixture")
    }
    separator := 0
    for i, arg := range os.Args {
        if arg == "--" {
            separator = i + 1
            break
        }
    }
    if separator == 0 {
        t.Fatal("missing helper command")
    }
    store := config.New(root, filepath.Join(root, "config"), filepath.Join(root, "state"))
    auth := processAuthentication(store)
    factory := autodl.Factory{HTTP: &http.Client{Transport: loopbackCloudTransport{endpoint}}, ReadSecret: auth.Token}
    auth.Authenticate = factory.Login
    os.Exit(Execute(context.Background(), os.Args[separator:], Options{
        Store: store, Auth: auth, Cloud: DefaultCloudService(store, factory.Open), CWD: root,
        In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Env: map[string]string{"LANG": "C"},
    }))
}

type cloudProcessFixture struct {
    t                              *testing.T
    root                           string
    server                         *httptest.Server
    mu                             sync.Mutex
    digest                         string
    token                          string
    logins, issued, creates, stops int
    clones                         int
    cloneMode                      bool
    exists, expired                bool
    status, failure                string
    entered                        chan struct{}
    release                        chan struct{}
    childEnv                       []string
}

func newCloudProcessFixture(t *testing.T) *cloudProcessFixture {
    t.Helper()
    sum := sha1.Sum([]byte("fixture-password"))
    f := &cloudProcessFixture{t: t, root: t.TempDir(), digest: hex.EncodeToString(sum[:]), status: "running", entered: make(chan struct{}, 1), release: make(chan struct{})}
    f.server = httptest.NewServer(http.HandlerFunc(f.serveHTTP))
    t.Cleanup(func() { close(f.release); f.server.CloseClientConnections(); f.server.Close() })
    return f
}

func (f *cloudProcessFixture) store() *config.Store {
    return config.New(f.root, filepath.Join(f.root, "config"), filepath.Join(f.root, "state"))
}

func (f *cloudProcessFixture) child(input string, args ...string) *exec.Cmd {
    f.t.Helper()
    binary, err := os.Executable()
    if err != nil {
        f.t.Fatal(err)
    }
    ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    f.t.Cleanup(cancel)
    command := exec.CommandContext(ctx, binary, append([]string{"-test.run=^TestCloudProcessHelper$", "--"}, args...)...)
    command.Dir = f.root
    command.Env = append(os.Environ(), "LOAF_TEST_CLOUD_ROOT="+f.root, "LOAF_TEST_CLOUD_HTTP="+f.server.URL)
    command.Env = append(command.Env, f.childEnv...)
    command.Stdin = strings.NewReader(input)
    return command
}

type cloudProcessResult struct {
    OK    bool            `json:"ok"`
    Data  json.RawMessage `json:"data"`
    Error *struct {
        Code string `json:"code"`
    }   `json:"error"`
}

func (f *cloudProcessFixture) invoke(want int, input string, args ...string) cloudProcessResult {
    f.t.Helper()
    command := f.child(input, args...)
    var stdout, stderr bytes.Buffer
    command.Stdout, command.Stderr = &stdout, &stderr
    err := command.Run()
    code := 0
    if err != nil {
        if exit, ok := err.(*exec.ExitError); ok {
            code = exit.ExitCode()
        } else {
            f.t.Fatal(err)
        }
    }
    var result cloudProcessResult
    for _, secret := range []string{"fixture-password", "changed-fixture-password", "fixture-token-", "fixture-private-ssh", "fixture-private-jupyter"} {
        if strings.Contains(stdout.String()+stderr.String(), secret) {
            f.t.Fatal("private data in public CLI output")
        }
    }
    decoder := json.NewDecoder(&stdout)
    if code != want || stderr.Len() != 0 || decoder.Decode(&result) != nil || decoder.Decode(new(any)) != io.EOF || result.OK != (want == 0) {
        f.t.Fatalf("%v: exit=%d want=%d stderr=%s", args, code, want, stderr.String())
    }
    return result
}

func (f *cloudProcessFixture) login() {
    f.t.Helper()
    f.invoke(0, "fixture-password\n", "cloud", "login", "lab", "--username", "student@master", "--password-stdin", "--credential-store", "file", "--default", "--json")
}

func (f *cloudProcessFixture) account() cloud.Account {
    f.t.Helper()
    cfg, err := f.store().Load()
    if err != nil {
        f.t.Fatal(err)
    }
    return cfg.Cloud.Accounts["lab"]
}

func (f *cloudProcessFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
    f.mu.Lock()
    defer f.mu.Unlock()
    reply := func(code string, data any) {
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(map[string]any{"code": code, "data": data})
    }
    principal := map[string]any{"id": 123, "uuid": "parent-id", "sub_name": "student@master", "roles": map[string]bool{"instance": true, "instance_pro": true}}
    path := r.URL.Path
    if path == "/api/v1/sub_user/login" {
        f.logins++
        var in struct {
            Password string `json:"password"`
            Username string `json:"sub_name"`
        }
        if json.NewDecoder(r.Body).Decode(&in) != nil || in.Password != f.digest || in.Username != "student@master" {
            reply("AccountPasswordIsWrong", nil)
            return
        }
        principal["uuid"] = ""
        reply("Success", map[string]any{"ticket": "fixture-ticket", "user": principal})
        return
    }
    if path == "/api/v1/sub_user/passport" {
        f.issued++
        f.token = fmt.Sprintf("fixture-token-%d", f.issued)
        f.expired = false
        reply("Success", map[string]string{"token": f.token})
        return
    }
    if f.expired || f.token == "" || r.Header.Get("Authorization") != f.token {
        reply("TokenExpired", nil)
        return
    }
    switch path {
    case "/api/v1/sub_user":
        reply("Success", principal)
    case "/api/v1/sub_user/user/machine/list":
        machine := map[string]any{"machine_id": "machine-fixture", "gpu_name": "fixture-GPU", "gpu_idle_num": 1}
        if f.cloneMode {
            machine["chip_corp"] = "nvidia"
            machine["cpu_arch"] = "x86"
            machine["highest_cuda_version"] = "13.2"
            machine["region_sign"] = "west-D"
            machine["max_data_disk_expand_size"] = int64(500) << 30
            machine["max_instance_disk_size"] = int64(50) << 30
        }
        reply("Success", map[string]any{"list": []any{machine}, "result_total": 1})
    case "/api/v1/machine_pro/gpu_type":
        reply("Success", []any{map[string]any{"gpu_type": "fixture-GPU", "gpu_idle_num": 1}})
    case "/api/v1/machine_pro/gpu_spec_config":
        reply("Success", []any{map[string]any{"id": 21}})
    case "/api/v1/sub_user/order/price/preview", "/api/v1/sub_user/order/price/preview/pro":
        product := "instance"
        if strings.HasSuffix(path, "/pro") {
            product = "instance_pro"
        }
        reply("Success", map[string]any{"discount_list": []any{map[string]any{"product_type": product, "origin_price": 1000, "total_reduction": 0}}})
    case "/api/v1/sub_user/order/instance/create/payg", "/api/v1/sub_user/order/instance/pro/create/payg":
        _, _ = io.Copy(io.Discard, r.Body)
        f.creates++
        f.exists = true
        select {
        case f.entered <- struct{}{}:
        default:
        }
        f.mutationResponse(w, r, reply)
    case "/api/v1/sub_user/order/instance/clone/payg":
        var in struct {
            Source string         `json:"instance_uuid"`
            Info   map[string]any `json:"instance_info"`
        }
        if !f.cloneMode || json.NewDecoder(r.Body).Decode(&in) != nil || in.Source != "source-fixture" || in.Info["copy_data_disk_after_clone"] != true || in.Info["expand_data_disk"] != float64(150<<30) {
            f.t.Error("invalid clone mutation")
        }
        f.clones++
        f.exists = true
        select {
        case f.entered <- struct{}{}:
        default:
        }
        f.mutationResponse(w, r, reply)
    case "/api/v1/sub_user/instance", "/api/v1/sub_user/instance/pro":
        rows := []any{}
        if f.cloneMode {
            rows = append(rows, map[string]any{"uuid": "source-fixture", "status": "shutdown", "region_sign": "west-D", "expand_data_disk_size": int64(150) << 30})
        }
        if f.exists {
            rows = append(rows, map[string]any{"uuid": "instance-fixture", "status": f.status, "region_sign": "west-D", "root_password": "fixture-private-ssh", "jupyter_token": "fixture-private-jupyter"})
        }
        reply("Success", map[string]any{"list": rows, "result_total": len(rows)})
    case "/api/v1/sub_user/instance/pro/snapshot":
        reply("Success", map[string]any{"image": map[string]string{"image_type": "public"}})
    case "/api/v1/sub_user/instance/snapshot":
        if !f.cloneMode || r.URL.Query().Get("instance_uuid") != "source-fixture" {
            f.t.Error("unexpected clone source snapshot")
        }
        reply("Success", map[string]any{"image": map[string]string{"image_type": "public", "image_name": "image-fixture", "chip_corp": "nvidia", "cpu_arch": "x86", "cuda_version": "12.8"}})
    case "/api/v1/sub_user/instance/power_off", "/api/v1/sub_user/instance/pro/power_off":
        _, _ = io.Copy(io.Discard, r.Body)
        f.stops++
        f.status = "shutdown"
        f.mutationResponse(w, r, reply)
    default:
        f.t.Errorf("unexpected fixture route %s %s", r.Method, path)
        reply("RequestParameterIsWrong", nil)
    }
}

func (f *cloudProcessFixture) mutationResponse(w http.ResponseWriter, r *http.Request, reply func(string, any)) {
    switch f.failure {
    case "kill":
        // Release the fixture state mutex while a separate process is killed.
        f.mu.Unlock()
        select {
        case <-r.Context().Done():
        case <-f.release:
        }
        f.mu.Lock()
    case "drop":
        connection, _, err := w.(http.Hijacker).Hijack()
        if err != nil {
            f.t.Error(err)
            return
        }
        connection.Close()
    case "server":
        w.WriteHeader(http.StatusServiceUnavailable)
        reply("InternalError", nil)
    case "malformed":
        reply("Success", map[string]string{"unexpected": "not-an-instance-id"})
    default:
        reply("Success", "instance-fixture")
    }
}

func TestCloudCredentialLifecycleAcrossProcesses(t *testing.T) {
    f := newCloudProcessFixture(t)
    f.login()
    original := f.account()
    auth := processAuthentication(f.store())
    t.Cleanup(func() {
        cfg, _ := f.store().Load()
        for _, a := range cfg.Cloud.Accounts {
            _ = auth.Forget(context.Background(), a.Credential)
        }
    })
    f.invoke(0, "", "cloud", "list", "--json")
    f.mu.Lock()
    f.expired = true
    f.mu.Unlock()
    result := f.invoke(1, "", "cloud", "list", "--json")
    if result.Error == nil || result.Error.Code != "CLOUD_AUTH_REQUIRED" {
        t.Fatal("expired session was accepted")
    }
    f.invoke(0, "", "cloud", "login", "lab", "--json")
    if f.account().Credential != original.Credential {
        t.Fatal("reauthentication redirected saved runs")
    }
    f.invoke(0, "", "cloud", "list", "--json")
    // A rejected stored digest must not overwrite the known local credential.
    before, err := auth.Token(context.Background(), original.Credential)
    if err != nil {
        t.Fatal(err)
    }
    sum := sha1.Sum([]byte("changed-fixture-password"))
    f.mu.Lock()
    f.digest = hex.EncodeToString(sum[:])
    f.mu.Unlock()
    result = f.invoke(1, "", "cloud", "login", "lab", "--json")
    after, err := auth.Token(context.Background(), original.Credential)
    if result.Error == nil || result.Error.Code != "CLOUD_AUTH_FAILED" || err != nil || before != after {
        t.Fatal("rejected password changed the saved session")
    }
    f.invoke(0, "changed-fixture-password\n", "cloud", "login", "lab", "--username", "student@master", "--password-stdin", "--json")
    f.invoke(0, "", "cloud", "login", "lab", "--credential-store", "keyring", "--json")
    switched := f.account()
    if switched.Credential.Store != "keyring" || switched.Credential.Key == original.Credential.Key {
        t.Fatal("store switch reused a historical reference")
    }
    if _, err := auth.Token(context.Background(), original.Credential); err != nil {
        t.Fatal("store switch deleted historical access")
    }
    // A fresh process must surface an unavailable selected vault, not read the
    // retained file credential or overwrite either reference as a fallback.
    native := runtime.GOOS == "linux" && os.Getenv("LOAF_TEST_NATIVE_VAULT") == "1"
    if native {
        f.childEnv = []string{"DBUS_SESSION_BUS_ADDRESS=unix:path=" + filepath.Join(f.root, "missing-bus")}
    } else if err := os.Chmod(filepath.Join(filepath.Dir(f.store().IndexFile), "test-vault"), 0755); err != nil {
        t.Fatal(err)
    }
    result = f.invoke(1, "", "cloud", "list", "--json")
    f.childEnv = nil
    if !native {
        if err := os.Chmod(filepath.Join(filepath.Dir(f.store().IndexFile), "test-vault"), 0700); err != nil {
            t.Fatal(err)
        }
    }
    if result.Error == nil || result.Error.Code != "CREDENTIAL_STORE_UNAVAILABLE" || f.account().Credential != switched.Credential {
        t.Fatal("unavailable vault changed storage or identity")
    }
    f.invoke(0, "", "cloud", "logout", "lab", "--json")
    result = f.invoke(1, "", "cloud", "list", "--json")
    if result.Error == nil || result.Error.Code != "CLOUD_AUTH_REQUIRED" {
        t.Fatal("logout retained access")
    }
    f.invoke(0, "changed-fixture-password\n", "cloud", "login", "lab", "--username", "student@master", "--password-stdin", "--json")
    if f.account().Credential != switched.Credential {
        t.Fatal("re-login after logout broke historical bindings")
    }
    f.invoke(0, "", "cloud", "list", "--json")
}

func TestCloudAcquisitionRecoveryAcrossProcesses(t *testing.T) {
    for _, mode := range []string{"standard", "pro", "clone"} {
        product := mode
        if mode == "clone" {
            product = "standard"
        }
        for _, failure := range []string{"drop", "kill", "server", "malformed"} {
            t.Run(mode+"/"+failure, func(t *testing.T) {
                f := newCloudProcessFixture(t)
                f.login()
                f.mu.Lock()
                f.failure = failure
                f.cloneMode = mode == "clone"
                f.mu.Unlock()
                args := []string{"cloud", "acquire", "first", "--product", product, "--image", "image-fixture", "--max-compute-hourly-price", "3.00", "--yes", "--json"}
                if mode == "clone" {
                    args = []string{"cloud", "acquire", "first", "--product", "standard", "--clone-from", "source-fixture", "--wait", "0s", "--max-compute-hourly-price", "3.00", "--yes", "--json"}
                }
                if product == "pro" {
                    args = append(args, "--region", "westDC3", "--cuda-from", "118")
                }
                if failure == "kill" {
                    command := f.child("", args...)
                    var output bytes.Buffer
                    command.Stdout = &output
                    command.Stderr = &output
                    if err := command.Start(); err != nil {
                        t.Fatal(err)
                    }
                    select {
                    case <-f.entered:
                    case <-time.After(10 * time.Second):
                        command.Process.Kill()
                        command.Wait()
                        t.Fatal("create request not received")
                    }
                    if err := command.Process.Kill(); err != nil {
                        t.Fatal(err)
                    }
                    _ = command.Wait()
                } else {
                    result := f.invoke(3, "", args...)
                    if result.Error == nil || result.Error.Code != "CLOUD_UNCONFIRMED" {
                        t.Fatal("ambiguous creation was confirmed")
                    }
                }
                receipts := cloud.Receipts{Dir: filepath.Join(filepath.Dir(f.store().IndexFile), "cloud")}
                rows, err := receipts.List()
                if err != nil || len(rows) != 1 || rows[0].Attempts != 1 || rows[0].Status != "intent" && rows[0].Status != "uncertain" {
                    t.Fatal("lost durable intent", err)
                }
                original := rows[0]
                if mode == "clone" && (original.Plan.Clone == nil || original.Plan.Clone.Instance.ID != "source-fixture" || original.Plan.Requirements.Regions[0] != "west-D") {
                    t.Fatal("lost clone source/region", original)
                }
                f.invoke(0, "", "cloud", "requests", original.ID, "--json")
                // A new process and a different alias may not evade the guard.
                args[2] = "replacement"
                result := f.invoke(3, "", args...)
                if result.Error == nil || result.Error.Code != "CLOUD_UNCONFIRMED" {
                    t.Fatal("lost-response guard was bypassed")
                }
                f.invoke(0, "", "cloud", "reconcile", original.ID, "--instance", "instance-fixture", "--json")
                saved, err := receipts.Load(original.ID)
                f.mu.Lock()
                creates, clones := f.creates, f.clones
                f.mu.Unlock()
                if mode == "clone" {
                    if creates != 0 || clones != 1 || saved.Plan.Clone == nil {
                        t.Fatal("clone recovery replayed or fell back", creates, clones)
                    }
                    creates = clones
                }
                if err != nil || creates != 1 || saved.Status != "created" || saved.Instance == nil || saved.Instance.ID != "instance-fixture" || saved.Instance.Product != product {
                    t.Fatal("recovery replayed or redirected allocation", err, creates)
                }
            })
        }
    }
}

func TestCloudPowerRecoveryAcrossProcesses(t *testing.T) {
    for _, product := range []string{"standard", "pro"} {
        t.Run(product, func(t *testing.T) {
            f := newCloudProcessFixture(t)
            f.login()
            f.mu.Lock()
            f.exists = true
            f.failure = "drop"
            f.mu.Unlock()
            args := []string{"cloud", "stop", "instance-fixture", "--product", product, "--yes", "--json"}
            f.invoke(3, "", args...)
            receipts := cloud.Receipts{Dir: filepath.Join(filepath.Dir(f.store().IndexFile), "cloud")}
            rows, err := receipts.List()
            if err != nil || len(rows) != 1 {
                t.Fatal(err)
            }
            f.invoke(3, "", args...)
            f.invoke(0, "", "cloud", "reconcile", rows[0].ID, "--json")
            saved, err := receipts.Load(rows[0].ID)
            f.mu.Lock()
            stops := f.stops
            f.mu.Unlock()
            if err != nil || stops != 1 || saved.Status != "completed" {
                t.Fatal("power request replayed or not reconciled", err, stops)
            }
        })
    }
}
