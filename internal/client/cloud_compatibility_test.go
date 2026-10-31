package client

import (
    "context"
    "errors"
    "path/filepath"
    "reflect"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/state/local"
    "github.com/elebirds/loaf/internal/wire"
)

type compatibilityRecoveryProvider struct {
    gets                   []cloud.Ref
    creates, starts, stops int
}

func (*compatibilityRecoveryProvider) List(context.Context, int) (cloud.Page, error) {
    return cloud.Page{}, errors.New("unexpected inventory during recovery")
}
func (p *compatibilityRecoveryProvider) Get(_ context.Context, ref cloud.Ref) (cloud.Instance, error) {
    p.gets = append(p.gets, ref)
    return cloud.Instance{Ref: ref, Status: "running"}, nil
}
func (*compatibilityRecoveryProvider) Connection(context.Context, cloud.Ref) (cloud.Connection, error) {
    return cloud.Connection{}, errors.New("unexpected connection during recovery")
}
func (p *compatibilityRecoveryProvider) Create(context.Context, cloud.Plan, *cloud.Offer) (cloud.Ref, error) {
    p.creates++
    return cloud.Ref{}, errors.New("unexpected purchase during recovery")
}
func (p *compatibilityRecoveryProvider) Start(context.Context, cloud.Ref) error {
    p.starts++
    return errors.New("unexpected start during recovery")
}
func (p *compatibilityRecoveryProvider) Stop(context.Context, cloud.Ref) error {
    p.stops++
    return errors.New("unexpected stop during recovery")
}

func TestCloudFormalUpgradeRecoversOriginalReceiptWithoutPurchase(t *testing.T) {
    originalVersion := wire.Version
    defer func() { wire.Version = originalVersion }()
    ctx := context.Background()
    stateRoot := t.TempDir()
    wire.Version = "v1.0.0"
    oldState := local.New(stateRoot)
    if _, err := oldState.Select(ctx, t.TempDir(), "original-gpu"); err != nil {
        t.Fatal(err)
    }
    oldReceipts := cloud.Receipts{Dir: filepath.Dir(oldState.Path)}
    settings := cloud.Account{Provider: "autodl", DefaultProduct: "standard", Credential: cloud.CredentialRef{Mode: "developer", Env: "ORIGINAL_TOKEN"}}
    ref := cloud.Ref{Provider: "autodl", Product: "pro", Account: "original", ID: "pro-original-instance"}
    now := time.Now().UTC()
    original, err := oldReceipts.New(cloud.Receipt{
        Operation: "acquire", Account: ref.Account, Settings: settings,
        Target: "original-gpu", RemoteBase: "~/original-projects",
        Plan: cloud.Plan{Product: ref.Product}, CreatedAt: now, Deadline: now.Add(time.Minute),
        Attempts: 1, Status: "uncertain", Instance: &ref,
    })
    if err != nil {
        t.Fatal(err)
    }
    if err := oldReceipts.Save(original); err != nil {
        t.Fatal(err)
    }

    wire.Version = "v1.1.0"
    newState := local.New(stateRoot)
    if newState.Path != oldState.Path || filepath.Base(filepath.Dir(newState.Path)) != "stable-v1" {
        t.Fatal("software update selected a fresh history", oldState.Path, newState.Path)
    }
    changed := cloud.Account{Provider: "autodl", DefaultProduct: "standard", Credential: cloud.CredentialRef{Mode: "session", Env: "CURRENT_TOKEN"}}
    provider := &compatibilityRecoveryProvider{}
    opened := 0
    resources := Resources{
        Settings: config.Global{Cloud: cloud.Settings{DefaultAccount: "replacement", Accounts: map[string]cloud.Account{"original": changed, "replacement": changed}}},
        Cloud: &cloud.Service{
            Receipts: cloud.Receipts{Dir: filepath.Dir(newState.Path)},
            Open: func(_ context.Context, account string, saved cloud.Account, product string) (cloud.Provider, error) {
                opened++
                if account != ref.Account || saved != settings || product != ref.Product {
                    t.Fatal("current defaults reinterpreted the original receipt", account, saved, product)
                }
                return provider, nil
            },
        },
    }
    loaded, err := resources.CloudRequest(ctx, CloudSelect{}, original.ID)
    if err != nil || !reflect.DeepEqual(loaded, original) || opened != 0 {
        t.Fatal("offline receipt lookup changed the request or opened a provider", loaded, err, opened)
    }
    recovered, err := resources.CloudReconcile(ctx, CloudReconcileInput{Request: original.ID}, nil)
    if err != nil || recovered.ID != original.ID || recovered.Instance == nil || *recovered.Instance != ref || recovered.Status != "created" {
        t.Fatal(recovered, err)
    }
    want := original
    want.Status = "created"
    saved, err := resources.CloudRequest(ctx, CloudSelect{}, original.ID)
    if err != nil || !reflect.DeepEqual(recovered, want) || !reflect.DeepEqual(saved, want) {
        t.Fatal("reconciliation changed the original binding or request", recovered, saved, err)
    }
    rows, err := resources.CloudRequests(ctx, CloudSelect{})
    if err != nil || len(rows.Requests) != 1 || rows.Requests[0].ID != original.ID || opened != 1 || !reflect.DeepEqual(provider.gets, []cloud.Ref{ref}) || provider.creates != 0 || provider.starts != 0 || provider.stops != 0 {
        t.Fatal("recovery did not remain a same-request observation", rows, err, opened, provider)
    }
}
