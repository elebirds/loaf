package client

import (
    "context"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
)

// CloudSelect contains optional account/product constraints, never credentials.
type CloudSelect struct{ Account, Product *string }

// CloudPlanInput preserves per-field absence for layered resource planning.
type CloudPlanInput struct {
    Profile  string
    Explicit cloud.Profile
}

// CloudApproval identifies the exact lifecycle operation requiring consent.
type CloudApproval struct {
    Operation, InstanceID               string
    Account, Product, Target, RequestID string
    Plan                                *cloud.Plan
}

// ApproveCloud is supplied only by an authorized caller; it does not mutate resources.
type ApproveCloud func(context.Context, CloudApproval) error

// CloudInstanceInput addresses an alias or resource using frozen binding rules.
type CloudInstanceInput struct {
    Select       CloudSelect
    ID           string
    ShowPassword bool
}

// CloudPowerInput requests one explicitly authorized instance operation.
type CloudPowerInput struct {
    Select     CloudSelect
    ID         string
    Start, Yes bool
    Wait       time.Duration
}

// CloudSearchInput freezes resource/profile selection for search or watch.
type CloudSearchInput struct {
    Select CloudSelect
    Plan   CloudPlanInput
    Limit  int
}

// CloudAcquireInput requests at most one resource and subsequent explicit binding.
type CloudAcquireInput struct {
    Select             CloudSelect
    Plan               CloudPlanInput
    Name               string
    Limit              int
    CloneFrom          *string
    Yes, AllowUnquoted bool
    RemoteBase         string
    ReadyTimeout       time.Duration
}

// CloudBindInput binds an existing instance or saved acquisition request.
type CloudBindInput struct {
    Select                  CloudSelect
    Name, Instance, Request string
    RemoteBase              *string
    Replace                 bool
    ReadyTimeout            time.Duration
}

// CloudReconcileInput recovers one saved mutation without replaying it.
type CloudReconcileInput struct {
    Select            CloudSelect
    Request, Instance string
    NoInstance, Yes   bool
}

// CloudAccountInput describes an account configuration mutation.
type CloudAccountInput struct {
    Name             string
    Account          cloud.Account
    Replace, Default bool
}

// CloudAccounts is the public account configuration view.
type CloudAccounts struct {
    Accounts map[string]cloud.Account `json:"accounts"`
    Default  string                   `json:"default_account,omitempty"`
}

// CloudReceipts lists recoverable resource requests.
type CloudReceipts struct {
    Requests []cloud.Receipt `json:"requests"`
}

// CloudBinding reports binding and readiness independently of allocation.
type CloudBinding struct {
    Target          string              `json:"target"`
    Instance        cloud.Ref           `json:"instance"`
    Status          string              `json:"status"`
    CloneDataStatus string              `json:"clone_data_status,omitempty"`
    Request         *cloud.Receipt      `json:"request,omitempty"`
    Observation     *cloud.SearchResult `json:"observation,omitempty"`
}

// CloudAcquisition retains the durable receipt and last read-only observation.
type CloudAcquisition struct {
    cloud.Receipt
    Observation *cloud.SearchResult `json:"observation,omitempty"`
}

// CloudCredentials exposes a password only for an explicit reveal request.
type CloudCredentials struct {
    Instance   cloud.Ref        `json:"instance"`
    Connection cloud.Connection `json:"connection"`
    Password   *string          `json:"password,omitempty"`
}

// AcquisitionResult preserves progress at an interrupted application boundary.
type AcquisitionResult struct {
    Plan        *cloud.Plan
    Acquisition *CloudAcquisition
    Binding     *CloudBinding
}

// PowerResult preserves the selected reference before mutation and its receipt afterwards.
type PowerResult struct {
    Reference cloud.Ref
    Receipt   *cloud.Receipt
}

// LoginInput describes explicit login preferences; absence retains existing account settings.
type LoginInput struct {
    Name, Username                                            string
    PhoneArea, CredentialStore, Product                       *string
    PasswordStdin, Prompt, Verify, Remember, Replace, Default bool
}

// LoginPrompts supply input without introducing terminal or localization dependencies.
type LoginPrompts struct {
    Username      func(context.Context) (string, error)
    Password      func(context.Context) ([]byte, error)
    Verification  func(context.Context) ([]byte, error)
    StdinPassword func(context.Context) ([]byte, error)
}

// LoginResult describes the stored authentication identity without secret material.
type LoginResult struct {
    Account    string         `json:"account"`
    Identity   cloud.Identity `json:"identity"`
    Store      string         `json:"credential_store"`
    Remembered bool           `json:"password_remembered"`
    Standard   bool           `json:"standard"`
    Pro        *bool          `json:"pro,omitempty"`
}

// LogoutResult confirms erasure of the selected credential entry.
type LogoutResult struct {
    Account string `json:"account"`
    Status  string `json:"status"`
}
