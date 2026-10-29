package cli

import (
    "errors"
    "fmt"
    "strings"

    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/fault"
)

type cloudContext struct {
    Mode       string `json:"mode,omitempty"`
    Operation  string `json:"operation"`
    Account    string `json:"account,omitempty"`
    Product    string `json:"product,omitempty"`
    Target     string `json:"target,omitempty"`
    Stage      string `json:"stage,omitempty"`
    RequestID  string `json:"request_id,omitempty"`
    InstanceID string `json:"instance_id,omitempty"`
}

type inputIssue struct {
    Argument string `json:"argument"`
    Reason   string `json:"reason"`
}
type cloudNextAction struct {
    Kind                 string   `json:"kind"`
    Command              []string `json:"command"`
    RequiresConfirmation bool     `json:"requires_confirmation"`
}

func (c *invocation) cloudNavigation(path string, data any, err error) {
    p := c.output
    context := &cloudContext{Operation: strings.TrimPrefix(path, "cloud ")}
    context.Account = c.app.Resources.Settings.Cloud.DefaultAccount
    if a, ok := c.app.Resources.Settings.Cloud.Accounts[context.Account]; ok {
        context.Product = a.DefaultProduct
    }
    if a := c.grammar.Cloud.Account; a != nil {
        context.Account = *a
    }
    if a := c.grammar.Cloud.Product; a != nil {
        context.Product = *a
    }
    setRef := func(ref cloud.Ref) {
        context.Account, context.Product, context.InstanceID = ref.Account, ref.Product, ref.ID
    }
    var receipt *cloud.Receipt
    switch r := data.(type) {
    case cloud.Plan:
        context.Product, context.Target = r.Product, c.grammar.Cloud.Acquire.Name
    case client.CloudAccounts:
        context.Account = r.Default
    case client.CloudBinding:
        context.Target = r.Target
        context.Stage = r.Status
        setRef(r.Instance)
        receipt = r.Request
    case client.CloudAcquisition:
        receipt = &r.Receipt
    case cloud.Receipt:
        receipt = &r
    case cloud.Instance:
        setRef(r.Ref)
        context.Stage = r.Status
    case cloud.Ref:
        setRef(r)
    case cloud.Page:
        context.Account, context.Product = r.Account, r.Product
    case cloud.CatalogPage:
        context.Account, context.Product = r.Account, r.Product
    case client.CloudPreview:
        context.Account, context.Product, context.Target, context.Stage = r.Account, r.Plan.Product, r.Target, "validation"
    case client.LoginResult:
        context.Account = r.Account
    }
    if receipt != nil {
        if receipt.Operation == "acquire" {
            context.Mode = "auto"
            if receipt.Plan.OfferID != "" {
                context.Mode = "select"
            }
        }
        context.RequestID = receipt.ID
        if context.Account == "" {
            context.Account = receipt.Account
        }
        if context.Product == "" {
            context.Product = receipt.Plan.Product
        }
        if context.Target == "" {
            context.Target = receipt.Target
        }
        if context.Stage == "" {
            context.Stage = receipt.Status
        }
        if receipt.Instance != nil {
            setRef(*receipt.Instance)
        }
    }
    p.cloudContext = context
    p.nextActions = nil
    if preview, ok := data.(client.CloudPreview); ok {
        if err == nil {
            in, e := c.acquireInput()
            if e == nil {
                p.nextActions = append(p.nextActions, cloudNextAction{Kind: "acquire_with_plan", Command: cloudPlanCommand(in, preview), RequiresConfirmation: true})
            }
        } else if preview.Plan.Requirements.GPUCount > 0 {
            p.issues = cloudInputIssues(preview.Plan, preview.AuthMode, preview.CloneSource != nil)
        }
    }
    if plan, ok := data.(cloud.Plan); ok && err != nil && plan.Requirements.GPUCount > 0 {
        mode := "session"
        if a, ok := c.app.Resources.Settings.Cloud.Accounts[context.Account]; ok {
            mode = a.Credential.Mode
        }
        p.issues = cloudInputIssues(plan, mode, c.grammar.Cloud.Acquire.CloneFrom != nil)
    }
    action := func(kind string, confirm bool, args ...string) {
        p.nextActions = append(p.nextActions, cloudNextAction{Kind: kind, Command: append([]string{"loaf"}, args...), RequiresConfirmation: confirm})
    }
    if context.Stage == "ready" && context.Target != "" {
        action("select_machine", false, "machine", "use", context.Target)
    } else if context.RequestID != "" && err != nil {
        if receipt != nil && receipt.Operation == "acquire" && receipt.Instance != nil && receipt.Status != "uncertain" && receipt.Status != "intent" {
            action("resume_binding", false, "cloud", "bind", "--request", context.RequestID)
        } else {
            action("inspect_request", false, "cloud", "requests", context.RequestID)
        }
    } else if context.InstanceID != "" {
        args := []string{"cloud", "info", context.InstanceID, "--account", context.Account, "--product", context.Product}
        action("observe_instance", false, args...)
    }
    var f *fault.Error
    if errors.As(err, &f) && f.Input != nil && f.Input.Argument == "--image" {
        args := []string{"cloud", "catalog", "image"}
        if context.Account != "" {
            args = append(args, "--account", context.Account)
        }
        if context.Product != "" {
            args = append(args, "--product", context.Product)
        }
        action("choose_image", false, args...)
    }
}

func commandDisplay(args []string) string {
    parts := make([]string, len(args))
    for i, arg := range args {
        if arg != "" && !strings.ContainsAny(arg, " \t\n'\"$`\\;|&<>()*?![]{}") {
            parts[i] = arg
        } else {
            parts[i] = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
        }
    }
    return strings.Join(parts, " ")
}

func (p *presenter) printCloudActions(data any) {
    binding, bound := data.(client.CloudBinding)
    for _, a := range p.nextActions {
        if bound && binding.Status == "ready" && a.Kind == "select_machine" {
            continue
        }
        fmt.Fprintln(p.out, p.locale.Text("cloud_next_action", map[string]any{"Value": commandDisplay(a.Command)}))
    }
}

type cloudProfilesResult struct {
    Profiles []client.CloudProfile `json:"profiles"`
}
