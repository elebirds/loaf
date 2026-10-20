package sync

import (
    "context"
    "errors"
    "reflect"
    "testing"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/ssh"
)

type checkFunc func(context.Context, config.Target, string, bool) (ssh.Report, error)

func (f checkFunc) Check(ctx context.Context, t config.Target, root string, batch bool) (ssh.Report, error) {
    return f(ctx, t, root, batch)
}

type sendFunc func(context.Context, config.Target, string, string, ssh.SyncInput, bool) (ssh.Transfer, error)

func (f sendFunc) Sync(ctx context.Context, t config.Target, runner, root string, req ssh.SyncInput, batch bool) (ssh.Transfer, error) {
    return f(ctx, t, runner, root, req, batch)
}

func TestSyncServiceSequencesExistingCapabilities(t *testing.T) {
    for _, fail := range []string{"", "check", "runner", "send"} {
        t.Run(fail, func(t *testing.T) {
            var calls []string
            failed := errors.New("failed")
            step := func(name string) error {
                calls = append(calls, name)
                if name == fail {
                    return failed
                }
                return nil
            }
            service := Service{
                Check: checkFunc(func(_ context.Context, target config.Target, root string, batch bool) (ssh.Report, error) {
                    if target.Destination != "host" || root != "~/projects/demo" || !batch {
                        t.Fatal(target, root, batch)
                    }
                    return ssh.Report{Arch: "aarch64", RemoteDir: "/home/researcher/projects/demo"}, step("check")
                }),
                Runner: func(_ context.Context, target config.Target, arch string, batch bool) (string, error) {
                    if arch != "aarch64" || !batch {
                        t.Fatal(arch, batch)
                    }
                    return "/private/runner", step("runner")
                },
                Send: sendFunc(func(_ context.Context, target config.Target, runner, root string, req ssh.SyncInput, batch bool) (ssh.Transfer, error) {
                    if runner != "/private/runner" || root != "/local/project" || req.Root != "/home/researcher/projects/demo" || req.ProjectID != "project" || req.Mode != "update" || len(req.Paths) != 1 || !batch {
                        t.Fatal(runner, root, req, batch)
                    }
                    return ssh.Transfer{ChangedFiles: 1}, step("send")
                }),
            }
            result, err := service.Sync(context.Background(), config.Target{Destination: "host"}, "project", "~/projects/demo", Plan{Root: "/local/project", Policy: config.SyncPolicy{Mode: "update"}, Files: []File{{Path: "train.py", Kind: "file"}}}, true)
            want := []string{"check", "runner", "send"}
            if fail == "check" {
                want = want[:1]
            }
            if fail == "runner" {
                want = want[:2]
            }
            if !reflect.DeepEqual(calls, want) || (fail != "" && !errors.Is(err, failed)) || (fail == "" && (err != nil || result.ChangedFiles != 1 || result.RemoteDir == "")) {
                t.Fatal(calls, result, err)
            }
        })
    }
}
