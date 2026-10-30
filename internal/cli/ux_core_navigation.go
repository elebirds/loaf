package cli

import (
    "context"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/fault"
)

// uxRoute carries navigation identity, not a command to execute or a replacement
// binding. Domain handlers still construct the existing typed client requests.
type uxRoute struct {
    Kind    string
    ID      string
    IDs     []string
    Target  *string
    Binding *cloud.Binding
}

// uxNextAction uses the existing cloud next_actions JSON contract. It carries no
// payment consent; displaying an action never executes it.
type uxNextAction = cloudNextAction

func (r uxRoute) validate() error {
    if r.Binding != nil && r.Kind != "cloud-info" {
        return fault.Invalid("instance", "conflicting_arguments", "ux_core_frozen_route")
    }
    switch r.Kind {
    case "cloud-info":
        if r.Binding == nil {
            return fault.Invalid("instance", "missing_argument", "cloud_config")
        }
        if r.Target != nil || r.ID != "" || len(r.IDs) > 0 {
            return fault.Invalid("--target/ids", "conflicting_arguments", "ux_core_frozen_route")
        }
    case "task", "logs", "outputs", "notify-run":
        if r.ID == "" {
            return fault.Invalid("run-id", "missing_argument", "ux_core_run_required")
        }
        if r.Target != nil || len(r.IDs) > 0 {
            return fault.Invalid("--target/ids", "conflicting_arguments", "ux_core_frozen_route")
        }
    case "clean":
        if r.Target != nil || r.ID != "" {
            return fault.Invalid("--target/run-id", "conflicting_arguments", "ux_core_frozen_route")
        }
    case "machine", "machine-add", "check", "ssh", "exec", "forward", "run", "sync", "tasks", "results", "queue", "queue-submit", "notify", "events", "cloud":
        if r.ID != "" || len(r.IDs) > 0 {
            return fault.Invalid("run-id/ids", "conflicting_arguments", "ux_core_route_identity")
        }
        if r.Target != nil {
            switch r.Kind {
            case "check", "ssh", "exec", "forward", "run", "sync", "queue", "queue-submit":
            default:
                return fault.Invalid("--target", "conflicting_arguments", "ux_core_frozen_route")
            }
        }
    default:
        return fault.Invalid("route", "invalid_value", "ux_core_invalid_route")
    }
    return nil
}

// uxRouteAvailable lists actually integrated capabilities. Adding a domain means
// adding its real handler to this explicit switch and uxNavigate together.
func (c *invocation) uxRouteAvailable(kind string) bool {
    switch kind {
    case "machine", "machine-add":
        return c.app != nil && c.app.Machines != nil && c.app.Machines.Config != nil
    case "run", "sync":
        return c.app != nil && c.app.Projects != nil && c.app.Machines != nil
    case "tasks", "task":
        return c.app != nil && c.app.Projects != nil && c.app.Projects.State != nil
    case "check":
        return c.app != nil && c.app.Machines != nil && c.app.Machines.Checker != nil
    case "ssh", "exec", "forward":
        return c.app != nil && c.app.Machines != nil && c.app.Machines.Operations != nil
    case "logs":
        return c.app != nil && c.app.Projects != nil && c.app.Projects.State != nil
    case "clean":
        return c.app != nil && c.app.Projects != nil && c.app.Projects.Runs != nil && c.app.Projects.Cleaner != nil
    case "queue", "queue-submit":
        return c.app != nil && c.app.Queue != nil && c.app.Machines != nil
    case "notify", "notify-run":
        return c.app != nil && c.app.Notify != nil
    case "events":
        return c.app != nil && c.app.Events != nil
    case "cloud":
        return c.app != nil && c.app.Resources != nil && c.app.Resources.Config != nil
    case "cloud-info":
        return c.app != nil && c.app.Resources != nil && c.app.Resources.Cloud != nil
    case "results", "outputs":
        return c.app != nil && c.app.Projects != nil && c.app.Projects.Runs != nil && c.app.Projects.Outputs != nil && c.app.Projects.Puller != nil
    default:
        return false
    }
}

func (c *invocation) uxNavigate(ctx context.Context, route uxRoute) (any, error) {
    if err := route.validate(); err != nil {
        return nil, err
    }
    if err := ctx.Err(); err != nil {
        return nil, err
    }
    if !c.uxInteractive() {
        return nil, interaction.ErrNonInteractive
    }
    if !c.uxRouteAvailable(route.Kind) {
        return nil, fault.New("FEATURE_UNAVAILABLE", "ux_core_unavailable", nil)
    }
    switch route.Kind {
    case "cloud-info":
        binding := *route.Binding
        child, err := c.uxChild("cloud", "info", binding.Ref.ID)
        if err != nil {
            return nil, err
        }
        var instance cloud.Instance
        err = child.cloudBusy(ctx, child.output.locale.Text("cloud_hub_loading"), cloudScope(child.output.locale, binding.Ref.Account, binding.Ref.Product, ""), func(work context.Context) error {
            var cause error
            instance, cause = child.app.Resources.CloudBoundInfo(work, binding)
            return cause
        })
        return instance, err
    case "check", "ssh", "exec", "forward":
        // Menu inputs come from the domain, with real declared check defaults;
        // do not invent a forward port or command just to initialize grammar.
        child, err := c.uxChild("check")
        if err != nil {
            return nil, err
        }
        child.output.command = route.Kind
        if route.Target != nil {
            target := *route.Target
            child.grammar.Target = &target
        }
        if route.Kind == "check" {
            return child.uxBCheck(ctx)
        }
        return child.uxBConnection(ctx, route.Kind, child.grammar.Target)
    case "queue", "queue-submit":
        command := "list"
        if route.Kind == "queue-submit" {
            command = "submit"
        }
        child, err := c.uxChild("queue", command)
        if err != nil {
            return nil, err
        }
        if route.Target != nil {
            target := *route.Target
            child.grammar.Target = &target
        }
        if route.Kind == "queue" {
            return child.uxHQueue(ctx)
        }
        return child.uxHSubmit(ctx)
    case "notify", "events", "notify-run":
        command := []string{"notify", "deliveries"}
        if route.Kind == "events" {
            command = []string{"events", "list"}
        }
        child, err := c.uxChild(command...)
        if err != nil {
            return nil, err
        }
        child.grammar.Target = nil
        switch route.Kind {
        case "notify":
            return child.uxINotify(ctx)
        case "events":
            return child.uxIEvents(ctx)
        default:
            return child.uxISubscribeRun(ctx, route.ID)
        }
    case "clean":
        // Help resolves declared defaults without a fabricated required ID.
        // The domain either uses these explicit IDs or opens D's selector.
        child, err := c.uxChild("help", "clean")
        if err != nil {
            return nil, err
        }
        child.grammar.Target = nil
        child.output.command = "clean"
        return child.uxGClean(ctx, append([]string(nil), route.IDs...))
    case "machine", "machine-add":
        subcommand := "list"
        if route.Kind == "machine-add" {
            subcommand = "add"
        }
        child, err := c.uxChild("machine", subcommand)
        if err != nil {
            return nil, err
        }
        if route.Kind == "machine-add" {
            return child.uxAMachineAdd(ctx)
        }
        child.output.command = "machine"
        return child.uxAMachineHub(ctx)
    case "run", "sync":
        child, err := c.uxChild(route.Kind)
        if err != nil {
            return nil, err
        }
        if route.Target != nil {
            target := *route.Target
            child.grammar.Target = &target
        }
        if route.Kind == "run" {
            return child.uxCRun(ctx)
        }
        return child.uxCSync(ctx)
    case "tasks":
        child, err := c.uxChild("status", "--local")
        if err != nil {
            return nil, err
        }
        child.grammar.Target = nil
        return child.uxDTasks(ctx)
    case "task":
        child, err := c.uxChild("inspect", route.ID)
        if err != nil {
            return nil, err
        }
        return child.uxDTask(ctx, route.ID)
    case "logs":
        child, err := c.uxChild("logs", route.ID)
        if err != nil {
            return nil, err
        }
        return child.uxELogs(ctx, route.ID)
    case "results":
        child, err := c.uxChild("outputs")
        if err != nil {
            return nil, err
        }
        return child.uxFResults(ctx)
    case "outputs":
        child, err := c.uxChild("outputs", route.ID)
        if err != nil {
            return nil, err
        }
        return child.uxFOutputs(ctx, route.ID)
    case "cloud":
        child, err := c.uxChild("cloud", "list")
        if err != nil {
            return nil, err
        }
        child.output.command = "cloud"
        return child.cloudHub(ctx)
    default:
        return nil, fault.New("FEATURE_UNAVAILABLE", "ux_core_unavailable", nil)
    }
}

// uxChild resolves defaults through Kong and isolates mutable grammar/presenter
// state. Streams, input ownership, trace and application services stay shared.
func (c *invocation) uxChild(command ...string) (*invocation, error) {
    model := &grammar{}
    parser, err := newParser(model)
    if err != nil {
        return nil, err
    }
    parsed, err := parser.Parse(command)
    if err != nil {
        return nil, err
    }
    path := tracedNode(parsed).Path()
    model.Interactive = c.grammar.Interactive
    if !targetForbidden(path) && c.grammar.Target != nil {
        target := *c.grammar.Target
        model.Target = &target
    }
    child := *c
    child.grammar = model
    output := *c.output
    output.command = path
    output.issues, output.regionCatalog, output.nextActions = nil, nil, nil
    output.cloudContext = nil
    output.cloudWizard, output.cloudDeclined, output.waited = false, false, false
    child.output = &output
    child.editAcquisition = nil
    child.profileOnly = false
    return &child, nil
}
