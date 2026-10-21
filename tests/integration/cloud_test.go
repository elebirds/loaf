package integration

import (
    "bytes"
    "context"
    "encoding/json"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/autodl"
    commands "github.com/elebirds/loaf/internal/cli"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/credentials"
    execution "github.com/elebirds/loaf/internal/run"
    "github.com/elebirds/loaf/internal/ssh"
    sources "github.com/elebirds/loaf/internal/sync"
)

type cloudTransport func(*http.Request) (*http.Response, error)

func (f cloudTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The platform control plane is simulated; native OpenSSH, askpass, rsync,
// bundled runner, durable execution and result download are real and isolated.
func TestCloudAcquireToDurableRunAndPull(t *testing.T) {
    if os.Getenv("LOAF_INTEGRATION") != "1" {
        t.Skip("run bash scripts/integration.sh")
    }
    for _, scenario := range []struct{ name, product, kind string }{
        {"standard-main", "standard", "main"}, {"standard-sub", "standard", "sub"},
        {"pro-main", "pro", "main"}, {"pro-sub", "pro", "sub"}, {"pro-developer", "pro", ""},
    } {
        t.Run(scenario.name, func(t *testing.T) {
            product, kind := scenario.product, scenario.kind
            developer := kind == ""
            password := product == "pro"
            f := newSSHFixture(t, password)
            if password {
                // Prevent native SSH from choosing the fixture key: this case must
                // actually authenticate through the packaged askpass entry point.
                contents, err := os.ReadFile(f.config)
                if err != nil {
                    t.Fatal(err)
                }
                contents = bytes.ReplaceAll(contents, []byte(f.key), []byte(filepath.Join(f.dir, "no-private-key")))
                if err := os.WriteFile(f.config, contents, 0600); err != nil {
                    t.Fatal(err)
                }
            }
            project := t.TempDir()
            sourceFile(t, project, "loaf.toml", "[run]\nwait = '5s'\n[outputs]\npaths = ['outputs']\n")
            sourceFile(t, project, "experiment.sh", "set -eu\ntest -z \"${LOAF_FIXTURE_TOKEN-}\"\ntest ! -d config/loaf\ntest ! -d state/loaf\nmkdir outputs\nprintf collected > outputs/result.txt\necho experiment-done\nexit 7\n")
            created, powerOff := 0, 0
            observedIDs := []string{}
            factory := autodl.Factory{LookupEnv: func(name string) (string, bool) {
                if name != "LOAF_FIXTURE_TOKEN" {
                    t.Fatal(name)
                }
                return "mock-platform-token", true
            }, HTTP: &http.Client{Transport: cloudTransport(func(r *http.Request) (*http.Response, error) {
                host := "www.autodl.com"
                if developer {
                    host = "api.autodl.com"
                }
                if r.URL.Host != host {
                    t.Fatal("wrong credential destination", r.URL)
                }
                loginRequest := strings.HasSuffix(r.URL.Path, "/login") || strings.HasSuffix(r.URL.Path, "/new_login") || strings.HasSuffix(r.URL.Path, "/passport") || strings.HasSuffix(r.URL.Path, "/login_failed/count")
                if !loginRequest && r.Header.Get("Authorization") != "mock-platform-token" {
                    t.Fatal("missing authorization")
                }
                result := any(nil)
                switch {
                case strings.HasSuffix(r.URL.Path, "/login_failed/count"):
                    result = map[string]any{"sms_2fa": false, "pv": 0}
                case strings.HasSuffix(r.URL.Path, "/new_login"), strings.HasSuffix(r.URL.Path, "/sub_user/login"):
                    username, field := "13800000000", "username"
                    if kind == "sub" {
                        username, field = "student@master", "sub_name"
                    }
                    principal := map[string]any{"uuid": "principal-fixture", field: username}
                    if kind == "sub" {
                        principal["uuid"], principal["id"] = "", 123
                    }
                    result = map[string]any{"ticket": "fixture-ticket", "user": principal}
                case strings.HasSuffix(r.URL.Path, "/passport"):
                    result = map[string]string{"token": "mock-platform-token"}
                case r.URL.Path == "/api/v1/user/detail":
                    result = map[string]string{"uuid": "principal-fixture", "username": "13800000000"}
                case r.URL.Path == "/api/v1/sub_user":
                    result = map[string]any{"id": 123, "uuid": "parent-fixture", "sub_name": "student@master", "roles": map[string]bool{"instance": true, "instance_pro": true}}
                case strings.HasSuffix(r.URL.Path, "/machine_pro/gpu_type"):
                    result = []any{map[string]any{"gpu_type": "fixture-GPU", "gpu_idle_num": 1, "gpu_total_number": 1}}
                case strings.HasSuffix(r.URL.Path, "/machine_pro/gpu_spec_config"):
                    result = []any{map[string]any{"id": 21, "cpu_num_from": 8, "memory_size_from": 32 << 30}}
                case strings.HasSuffix(r.URL.Path, "/order/price/preview/pro"), strings.HasSuffix(r.URL.Path, "/order/price/preview"):
                    compute, disk := "instance", "data_disk"
                    if product == "pro" {
                        compute, disk = "instance_pro", "system_disk"
                    }
                    result = map[string]any{"discount_list": []any{map[string]any{"product_type": compute, "origin_price": 1000, "total_reduction": 0}, map[string]any{"product_type": disk, "origin_price": 0, "total_reduction": 0}}}
                case strings.HasSuffix(r.URL.Path, "/user/machine/list"):
                    result = map[string]any{"list": []any{map[string]any{"machine_id": "machine-fixture", "gpu_name": "fixture-GPU", "gpu_idle_num": 1, "payg_price": 1000, "gpu_memory": 24 << 30, "cpu_per_gpu": 8, "mem_per_gpu": 32 << 30, "max_instance_disk_size": 100 << 30}}}
                case strings.HasSuffix(r.URL.Path, "/create/payg"), strings.HasSuffix(r.URL.Path, "/pro/create"):
                    created++
                    result = "instance-fixture"
                case strings.HasSuffix(r.URL.Path, "/status"):
                    observedIDs = append(observedIDs, r.URL.Query().Get("instance_uuid"))
                    result = "running"
                case strings.HasSuffix(r.URL.Path, "/snapshot"):
                    observedIDs = append(observedIDs, r.URL.Query().Get("instance_uuid"))
                    result = map[string]any{"image": map[string]string{"image_type": "public"}}
                    if developer {
                        result = map[string]any{"status": "running", "ssh_command": "ssh -p " + f.port + " researcher@127.0.0.1", "root_password": "loaf-fixture-password", "jupyter_token": "never-public"}
                    }
                case strings.HasSuffix(r.URL.Path, "/power_off"):
                    powerOff++
                default:
                    result = map[string]any{"list": []any{map[string]any{"uuid": "instance-fixture", "status": "running", "ssh_command": "ssh -p " + f.port + " researcher@127.0.0.1", "root_password": "loaf-fixture-password"}}, "result_total": 1}
                }
                data, _ := json.Marshal(map[string]any{"code": "Success", "data": result})
                return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(data))}, nil
            })}}
            store := config.New(project, filepath.Join(project, "config"), filepath.Join(project, "state"))
            auth := &cloud.Authentication{Files: credentials.File{Dir: filepath.Join(filepath.Dir(store.IndexFile), "credentials")}}
            auth.Authenticate = factory.Login
            factory.ReadSecret = auth.Token
            f.client.AskpassExecutable = os.Getenv("LOAF_BIN")
            f.client.SensitiveEnv = []string{"LOAF_FIXTURE_TOKEN"}
            t.Setenv("LOAF_FIXTURE_TOKEN", "mock-platform-token")
            f.client.Resolve = func(ctx context.Context, target config.Target) (config.Target, string, error) {
                b := target.Cloud
                p, err := factory.Open(ctx, b.Ref.Account, b.Settings, b.Ref.Product)
                if err != nil {
                    return config.Target{}, "", err
                }
                c, err := p.Connection(ctx, b.Ref)
                if err != nil {
                    return config.Target{}, "", err
                }
                observedIDs = append(observedIDs, b.Ref.ID)
                target.Cloud = nil
                target.Destination = c.User + "@" + c.Host
                target.Args = []string{"-F", f.config, "-p", strconv.Itoa(c.Port)}
                return target, c.Password, nil
            }
            bootstrap := execution.Bootstrap{Remote: f.client}
            prepare := func(ctx context.Context, target config.Target, arch string, batch bool) (string, error) {
                if arch == "" {
                    var err error
                    arch, err = f.client.RunnerArch(ctx, target, batch)
                    if err != nil {
                        return "", err
                    }
                }
                info, err := bootstrap.Verify(ctx, target, arch, batch)
                return info.Path, err
            }
            sender := ssh.Rsync{Client: f.client, Binary: "rsync"}
            runs := &execution.Service{Index: store, Check: integrationPreflight{f.client}, Runner: prepare, Send: sender, Remote: f.client}
            o := commands.Options{Store: store, Auth: auth, Cloud: commands.DefaultCloudService(store, factory.Open), Runs: runs, Checker: f.client, Runner: bootstrap, Operations: f.client, Puller: sources.PullService{Remote: f.client, Runner: prepare, Receive: sender}, CWD: project, Env: map[string]string{"LANG": "C"}, In: strings.NewReader("")}
            invoke := func(args ...string) []byte {
                t.Helper()
                var out, diagnostic bytes.Buffer
                o.Out, o.Err = &out, &diagnostic
                code := commands.Execute(context.Background(), append(args, "--json"), o)
                if code != 0 || diagnostic.Len() != 0 {
                    t.Fatalf("%v: %d %s %s", args, code, out.String(), diagnostic.String())
                }
                if strings.Contains(out.String(), "loaf-fixture-password") || strings.Contains(out.String(), "mock-platform-token") || strings.Contains(out.String(), "never-public") {
                    t.Fatal("public output exposed a secret")
                }
                return out.Bytes()
            }
            if developer {
                invoke("cloud", "account", "add", "demo", "--product", "pro", "--auth", "developer", "--token-env", "LOAF_FIXTURE_TOKEN", "--default")
            } else {
                username := "13800000000"
                if kind == "sub" {
                    username = "student@master"
                }
                o.In = strings.NewReader("fixture-login-password\n")
                invoke("cloud", "login", "demo", "--username", username, "--password-stdin", "--credential-store", "file", "--default", "--product", product)
                o.In = strings.NewReader("")
            }
            acquire := []string{"cloud", "acquire", "gpu", "--image", "image-fixture", "--ready-timeout", "0s", "--yes"}
            if product == "pro" {
                acquire = append(acquire, "--cuda-from", "118")
                if developer {
                    acquire = append(acquire, "--gpu-spec", "spec-fixture", "--allow-unquoted")
                } else {
                    acquire = append(acquire, "--gpu-spec-id", "21", "--max-compute-hourly-price", "3.00")
                }
            }
            invoke(acquire...)
            if created != 1 {
                t.Fatal(created)
            }
            invoke("use", "gpu")
            var started struct{ Data execution.View }
            // Flags must precede --; the experiment argv must be preserved literally.
            var out, diagnostic bytes.Buffer
            o.Out, o.Err = &out, &diagnostic
            code := commands.Execute(context.Background(), []string{"run", "--json", "--", "/bin/sh", "experiment.sh"}, o)
            if code != 0 || json.Unmarshal(out.Bytes(), &started) != nil || started.Data.Status != "failed" || started.Data.ExitCode == nil || *started.Data.ExitCode != 7 {
                t.Fatal(code, out.String(), diagnostic.String())
            }
            // Replace the alias after submission; saved run operations must still
            // refresh the original instance ID, not the new alias's resource.
            cfg, err := store.Load()
            if err != nil {
                t.Fatal(err)
            }
            changed := cfg.Targets["gpu"]
            copy := *changed.Cloud
            copy.Ref.ID = "another-instance"
            changed.Cloud = &copy
            if err := store.Add(context.Background(), "gpu", changed, true); err != nil {
                t.Fatal(err)
            }
            observedIDs = nil
            invoke("logs", started.Data.ID)
            var pulled struct{ Data sources.PullResult }
            if err := json.Unmarshal(invoke("pull", started.Data.ID), &pulled); err != nil {
                t.Fatal(err)
            }
            contents, err := os.ReadFile(filepath.Join(pulled.Data.Destination, "outputs", "result.txt"))
            if err != nil || string(contents) != "collected" {
                t.Fatal(string(contents), err)
            }
            for _, id := range observedIDs {
                if id != "instance-fixture" {
                    t.Fatal("saved run redirected", observedIDs)
                }
            }
            if len(observedIDs) == 0 {
                t.Fatal("no connection refresh observed")
            }
            root, _, err := config.Discover(context.Background(), project)
            if err != nil {
                t.Fatal(err)
            }
            projectRecord, err := store.Existing(root)
            if err != nil {
                t.Fatal(err)
            }
            records, err := store.Runs(projectRecord.ID)
            if err != nil || len(records) != 1 || records[0].Connection.Cloud.Ref.ID != "instance-fixture" {
                t.Fatal(records, err)
            }
            invoke("cloud", "credentials", "instance-fixture")
            invoke("cloud", "stop", "instance-fixture", "--yes")
            if powerOff != 1 || created != 1 {
                t.Fatal(powerOff, created)
            }
            data, err := os.ReadFile(store.ConfigFile)
            if err != nil || bytes.Contains(data, []byte("loaf-fixture-password")) || bytes.Contains(data, []byte("mock-platform-token")) || bytes.Contains(data, []byte("fixture-login-password")) {
                t.Fatal("secret persisted", err)
            }
            // Unknown host keys must fail closed even with a fetched password.
            known, err := os.ReadFile(f.known)
            if err != nil {
                t.Fatal(err)
            }
            if err := os.WriteFile(f.known, nil, 0600); err != nil {
                t.Fatal(err)
            }
            t.Cleanup(func() { _ = os.WriteFile(f.known, known, 0600) })
            original := records[0].Connection
            if _, err := f.client.Execute(context.Background(), original, "true", nil, true); err == nil {
                t.Fatal("password bypassed host trust")
            }
            if created != 1 {
                t.Fatal("connection failure rented a replacement")
            }
        })
    }
}

type integrationPreflight struct{ client *ssh.Client }

func (p integrationPreflight) Check(ctx context.Context, target config.Target, dir string, batch bool) (ssh.Report, error) {
    return p.client.Preflight(ctx, target, dir, batch)
}
