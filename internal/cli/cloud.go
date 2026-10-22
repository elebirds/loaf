package cli

import (
    "bufio"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "path/filepath"
    "strconv"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    "github.com/elebirds/loaf/internal/ssh"
    "github.com/spf13/cobra"
)

type cloudAccounts struct {
    Accounts map[string]cloud.Account `json:"accounts"`
    Default  string                   `json:"default_account,omitempty"`
}
type cloudReceipts struct {
    Requests []cloud.Receipt `json:"requests"`
}
type cloudBinding struct {
    Target          string              `json:"target"`
    Instance        cloud.Ref           `json:"instance"`
    Status          string              `json:"status"`
    CloneDataStatus string              `json:"clone_data_status,omitempty"`
    Request         *cloud.Receipt      `json:"request,omitempty"`
    Observation     *cloud.SearchResult `json:"observation,omitempty"`
}
type cloudAcquisition struct {
    cloud.Receipt
    Observation *cloud.SearchResult `json:"observation,omitempty"`
}
type cloudCredentials struct {
    Instance   cloud.Ref        `json:"instance"`
    Connection cloud.Connection `json:"connection"`
    Password   *string          `json:"password,omitempty"`
}

func newCloudCommand(o Options, cfg config.Global, l *i18n.Localizer, result func(any), streamed *bool) *cobra.Command {
    command := &cobra.Command{Use: "cloud", Short: l.Text("cloud_short"), Args: arguments(0, 0)}
    var account, product string
    command.PersistentFlags().StringVar(&account, "account", "", l.Text("cloud_flag_account"))
    command.PersistentFlags().StringVar(&product, "product", "", l.Text("cloud_flag_product"))
    command.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
        if jsonMode, _ := cmd.Flags().GetBool("json"); jsonMode {
            o.Interactive = false
        }
        if cmd.Flags().Changed("target") {
            return fault.New("BAD_CONFIG", "cloud_target_flag", nil)
        }
        if command.PersistentFlags().Changed("product") && !cloud.ValidProduct(product) {
            return fault.New("BAD_CONFIG", "cloud_config", nil)
        }
        if o.Cloud == nil {
            return fault.New("CLOUD_FAILED", "cloud_failed", nil)
        }
        return nil
    }
    command.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
    accountCommand := newCloudAccounts(o, cfg, l, result)
    command.AddCommand(accountCommand)
    command.AddCommand(newCloudLogin(o, cfg, l, result), newCloudLogout(o, cfg, l, result))
    choose := func() (string, cloud.Account, error) {
        name := account
        if name == "" {
            name = cfg.Cloud.DefaultAccount
        }
        a, ok := cfg.Cloud.Accounts[name]
        if !ok {
            return "", a, fault.New("BAD_CONFIG", "cloud_account_unknown", nil)
        }
        return name, a, nil
    }
    selectProduct := func(a cloud.Account, profile string) string {
        if product != "" {
            return product
        }
        if profile != "" {
            return profile
        }
        return a.DefaultProduct
    }
    binding := func(arg string) (cloud.Binding, error) {
        if target, ok := cfg.Targets[arg]; ok && target.Cloud != nil {
            b := *target.Cloud
            if account != "" && account != b.Ref.Account {
                return b, fault.New("BAD_CONFIG", "cloud_account_mismatch", nil)
            }
            if product != "" && product != b.Ref.Product {
                return b, fault.New("BAD_CONFIG", "cloud_product_mismatch", nil)
            }
            return b, nil
        }
        name, a, err := choose()
        if err != nil {
            return cloud.Binding{}, err
        }
        return cloud.Binding{Ref: cloud.Ref{Provider: a.Provider, Product: selectProduct(a, ""), Account: name, ID: arg}, Settings: a}, nil
    }
    list := &cobra.Command{Use: "list", Short: l.Text("cloud_list_short"), Args: arguments(0, 0)}
    limit := 100
    list.Flags().IntVar(&limit, "limit", 100, l.Text("cloud_flag_limit"))
    list.RunE = func(cmd *cobra.Command, _ []string) error {
        if limit < 1 || limit > 1000 {
            return fault.New("BAD_CONFIG", "cloud_config", nil)
        }
        name, a, err := choose()
        if err != nil {
            return err
        }
        p, err := o.Cloud.Open(cmd.Context(), name, a, selectProduct(a, ""))
        if err != nil {
            return cloud.Failure(err)
        }
        page, err := p.List(cmd.Context(), limit)
        result(page)
        return cloud.Failure(err)
    }
    command.AddCommand(list)
    for _, op := range []string{"info", "credentials", "start", "stop"} {
        cmd := &cobra.Command{Use: op + " " + l.Text("cloud_instance_arg"), Short: l.Text("cloud_" + op + "_short"), Args: arguments(1, 1)}
        var yes, reveal bool
        var wait time.Duration
        if op == "start" || op == "stop" {
            cmd.Flags().BoolVar(&yes, "yes", false, l.Text("cloud_flag_yes"))
            cmd.Flags().DurationVar(&wait, "wait", 0, l.Text("cloud_flag_power_wait"))
        }
        if op == "credentials" {
            cmd.Flags().BoolVar(&reveal, "show-password", false, l.Text("cloud_flag_password"))
        }
        cmd.RunE = func(cmd *cobra.Command, args []string) error {
            b, err := binding(args[0])
            if err != nil {
                return err
            }
            if !config.ValidCloudBinding(b) || wait < 0 || wait > 30*time.Minute {
                return fault.New("BAD_CONFIG", "cloud_config", nil)
            }
            if op == "start" || op == "stop" {
                result(b.Ref)
                if err := confirmCloud(o, l, yes, l.Text("cloud_power_confirm", map[string]any{"Value": op, "ID": b.Ref.ID})); err != nil {
                    return err
                }
                r, err := o.Cloud.Power(cmd.Context(), b, op == "start", wait)
                result(r)
                return err
            }
            p, err := o.Cloud.Open(cmd.Context(), b.Ref.Account, b.Settings, b.Ref.Product)
            if err != nil {
                return cloud.Failure(err)
            }
            if op == "info" {
                v, err := p.Get(cmd.Context(), b.Ref)
                result(v)
                return cloud.Failure(err)
            }
            c, err := p.Connection(cmd.Context(), b.Ref)
            if err != nil {
                return cloud.Failure(err)
            }
            r := cloudCredentials{Instance: b.Ref, Connection: c}
            if reveal {
                r.Password = &c.Password
            }
            result(r)
            return nil
        }
        command.AddCommand(cmd)
    }
    for _, op := range []string{"search", "watch", "acquire"} {
        use := op
        n := 0
        if op == "acquire" {
            use += " " + l.Text("name_arg")
            n = 1
        }
        cmd := &cobra.Command{Use: use, Short: l.Text("cloud_" + op + "_short"), Args: arguments(n, n)}
        planFor := cloudPlanFlags(cmd, o, cfg, l)
        limit := 100
        var yes, unquoted bool
        var remoteBase, cloneFrom string
        var ready time.Duration
        cmd.Flags().IntVar(&limit, "limit", 100, l.Text("cloud_flag_limit"))
        if op == "acquire" {
            cmd.Flags().StringVar(&cloneFrom, "clone-from", "", l.Text("cloud_flag_clone_from"))
            cmd.Flags().BoolVar(&yes, "yes", false, l.Text("cloud_flag_yes"))
            cmd.Flags().BoolVar(&unquoted, "allow-unquoted", false, l.Text("cloud_flag_unquoted"))
            cmd.Flags().StringVar(&remoteBase, "remote-dir", "~/.local/share/loaf/projects", l.Text("flag_remote"))
            cmd.Flags().DurationVar(&ready, "ready-timeout", 2*time.Minute, l.Text("cloud_flag_ready"))
        }
        cmd.RunE = func(cmd *cobra.Command, args []string) error {
            if cmd.Flags().Changed("clone-from") {
                if cloneFrom == "" || unquoted {
                    return fault.New("BAD_CONFIG", "cloud_clone_options", nil)
                }
                for _, flag := range []string{"image", "private-image", "gpu-spec", "gpu-spec-id", "cuda-from", "expand-system-disk"} {
                    if cmd.Flags().Changed(flag) {
                        return fault.New("BAD_CONFIG", "cloud_clone_options", nil)
                    }
                }
            }
            plan, err := planFor(cmd)
            if err != nil {
                return err
            }
            var name string
            var a cloud.Account
            var sourceBinding cloud.Binding
            if cloneFrom != "" {
                if target, ok := cfg.Targets[cloneFrom]; ok {
                    if target.Cloud == nil {
                        return fault.New("BAD_CONFIG", "cloud_clone_options", nil)
                    }
                    sourceBinding, err = binding(cloneFrom)
                } else {
                    name, a, err = choose()
                    sourceBinding = cloud.Binding{Ref: cloud.Ref{Provider: a.Provider, Product: selectProduct(a, plan.Product), Account: name, ID: cloneFrom}, Settings: a}
                }
                if err != nil {
                    return err
                }
                if plan.Product != "" && plan.Product != sourceBinding.Ref.Product {
                    return fault.New("BAD_CONFIG", "cloud_product_mismatch", nil)
                }
                name, a = sourceBinding.Ref.Account, sourceBinding.Settings
                plan.Product = sourceBinding.Ref.Product
                if !config.ValidCloudBinding(sourceBinding) {
                    return fault.New("BAD_CONFIG", "cloud_config", nil)
                }
                if plan.Product != "standard" || a.Credential.Mode != "session" {
                    return fault.New("UNSUPPORTED_CAPABILITY", "cloud_clone_unsupported", nil)
                }
            } else {
                name, a, err = choose()
                plan.Product = selectProduct(a, plan.Product)
            }
            if err != nil {
                return err
            }
            if limit < 1 || limit > 1000 {
                return fault.New("BAD_CONFIG", "cloud_config", nil)
            }
            p, err := o.Cloud.Open(cmd.Context(), name, a, plan.Product)
            if err != nil {
                return cloud.Failure(err)
            }
            if cloneFrom != "" {
                cloner, ok := p.(cloud.Cloner)
                if !ok {
                    return fault.New("UNSUPPORTED_CAPABILITY", "cloud_clone_unsupported", nil)
                }
                source, err := cloner.CloneSource(cmd.Context(), sourceBinding.Ref)
                if err != nil {
                    return cloud.Failure(err)
                }
                wait := "30m"
                plan, err = planFor(cmd, cloud.Profile{WaitTimeout: &wait, AutoDL: cloud.AutoDLProfile{ExpandDataDiskGB: &source.ExpandDataDiskGB}})
                if err != nil {
                    return err
                }
                plan.Product = sourceBinding.Ref.Product
                plan, err = cloud.PrepareClone(plan, source)
                if err != nil {
                    return err
                }
            }
            jsonMode, _ := cmd.Flags().GetBool("json")
            emit := cloudProgress(o, l, jsonMode && op == "watch")
            var observation *cloud.SearchResult
            if jsonMode && op == "acquire" {
                emit = func(e cloud.Event) error {
                    if e.Observation != nil {
                        observation = e.Observation
                    }
                    return nil
                }
            } else if op == "acquire" {
                progress := emit
                emit = func(e cloud.Event) error {
                    if e.Observation != nil {
                        observation = e.Observation
                    }
                    return progress(e)
                }
            }
            switch op {
            case "search":
                r, err := o.Cloud.Search(cmd.Context(), p, plan, limit)
                result(r)
                return cloud.Failure(err)
            case "watch":
                if jsonMode {
                    *streamed = true
                }
                r, err := o.Cloud.Watch(cmd.Context(), p, plan, limit, emit)
                if jsonMode {
                    if err != nil {
                        return cloud.Failure(err)
                    }
                    return json.NewEncoder(o.Out).Encode(envelope{SchemaVersion: 1, OK: true, Data: map[string]any{"type": "end", "observation": r}})
                }
                result(r)
                return cloud.Failure(err)
            default:
                if !config.ValidName(args[0]) || !config.ValidRemotePath(remoteBase) || ready < 0 || ready > 10*time.Minute {
                    return fault.New("BAD_CONFIG", "cloud_config", nil)
                }
                if _, ok := cfg.Targets[args[0]]; ok {
                    return fault.New("BAD_CONFIG", "target_exists", map[string]any{"Target": args[0]})
                }
                result(plan)
                if !jsonMode && !yes {
                    printCloud(o.Err, plan, l)
                }
                prompt := l.Text("cloud_acquire_confirm")
                if plan.Clone != nil {
                    prompt = l.Text("cloud_clone_confirm", map[string]any{"ID": plan.Clone.Instance.ID, "Region": plan.Clone.Region})
                }
                if err := confirmCloud(o, l, yes, prompt); err != nil {
                    return err
                }
                in := cloud.AcquireInput{Account: name, Settings: a, Target: args[0], RemoteBase: remoteBase, Plan: plan, Limit: limit, AllowUnquoted: unquoted}
                in.CheckTarget = func() error {
                    current, err := o.Store.Load()
                    if err != nil {
                        return err
                    }
                    if _, ok := current.Targets[args[0]]; ok {
                        return fault.New("BAD_CONFIG", "target_exists", map[string]any{"Target": args[0]})
                    }
                    return nil
                }
                r, err := o.Cloud.Acquire(cmd.Context(), in, emit)
                result(cloudAcquisition{Receipt: r, Observation: observation})
                if err != nil {
                    return err
                }
                if r.Instance == nil {
                    return fault.New("CLOUD_UNCONFIRMED", "cloud_unconfirmed", nil)
                }
                b := cloud.Binding{Ref: *r.Instance, Settings: a}
                bound, err := bindCloud(cmd.Context(), o, args[0], b, remoteBase, false, ready, plan.Clone)
                bound.Request = &r
                bound.Observation = observation
                result(bound)
                if err != nil {
                    return err
                }
                if err := o.Cloud.MarkReady(cmd.Context(), r.ID, b.Ref); err != nil {
                    return err
                }
                r.Status = "ready"
                bound.Request = &r
                result(bound)
                return nil
            }
        }
        command.AddCommand(cmd)
    }
    bind := &cobra.Command{Use: "bind " + l.Text("name_arg"), Short: l.Text("cloud_bind_short"), Args: arguments(1, 1)}
    var instance, request, remote string
    var replace bool
    var ready time.Duration
    bind.Flags().StringVar(&instance, "instance", "", l.Text("cloud_flag_instance"))
    bind.Flags().StringVar(&request, "request", "", l.Text("cloud_flag_request"))
    bind.Flags().StringVar(&remote, "remote-dir", "~/.local/share/loaf/projects", l.Text("flag_remote"))
    bind.Flags().BoolVar(&replace, "replace", false, l.Text("flag_replace"))
    bind.Flags().DurationVar(&ready, "ready-timeout", 2*time.Minute, l.Text("cloud_flag_ready"))
    bind.RunE = func(cmd *cobra.Command, args []string) error {
        if (instance == "") == (request == "") || ready < 0 || ready > 10*time.Minute {
            return fault.New("BAD_CONFIG", "cloud_config", nil)
        }
        var b cloud.Binding
        var saved cloud.Receipt
        var err error
        if request != "" {
            saved, err = o.Cloud.Receipts.Load(request)
            if err != nil {
                return err
            }
            if saved.Instance == nil || saved.Operation != "acquire" || saved.Target != args[0] || account != "" && account != saved.Account || product != "" && product != saved.Instance.Product {
                return fault.New("BAD_CONFIG", "cloud_config", nil)
            }
            b = cloud.Binding{Ref: *saved.Instance, Settings: saved.Settings}
            if !cmd.Flags().Changed("remote-dir") {
                remote = saved.RemoteBase
            }
            if target, ok := cfg.Targets[args[0]]; ok && target.Cloud != nil && *target.Cloud == b {
                replace = true
            }
        } else {
            b, err = binding(instance)
            if err != nil {
                return err
            }
        }
        r, err := bindCloud(cmd.Context(), o, args[0], b, remote, replace, ready, saved.Plan.Clone)
        result(r)
        if err == nil && request != "" {
            return o.Cloud.MarkReady(cmd.Context(), request, b.Ref)
        }
        return err
    }
    command.AddCommand(bind)
    requests := &cobra.Command{Use: "requests " + l.Text("cloud_request_arg"), Short: l.Text("cloud_requests_short"), Args: arguments(0, 1)}
    requests.RunE = func(_ *cobra.Command, args []string) error {
        if len(args) > 0 {
            r, err := o.Cloud.Receipts.Load(args[0])
            selected := r.Plan.Product
            if r.Instance != nil {
                selected = r.Instance.Product
            }
            if err == nil && (account != "" && account != r.Account || product != "" && product != selected) {
                return fault.New("BAD_CONFIG", "cloud_config", nil)
            }
            if err == nil {
                result(r)
            }
            return err
        }
        rows, err := o.Cloud.Receipts.List()
        if account != "" || product != "" {
            filtered := []cloud.Receipt{}
            for _, r := range rows {
                selected := r.Plan.Product
                if r.Instance != nil {
                    selected = r.Instance.Product
                }
                if (account == "" || r.Account == account) && (product == "" || product == selected) {
                    filtered = append(filtered, r)
                }
            }
            rows = filtered
        }
        result(cloudReceipts{Requests: rows})
        return err
    }
    reconcile := &cobra.Command{Use: "reconcile " + l.Text("cloud_required_request_arg"), Short: l.Text("cloud_reconcile_short"), Args: arguments(1, 1)}
    var known string
    var none, yes bool
    reconcile.Flags().StringVar(&known, "instance", "", l.Text("cloud_flag_instance"))
    reconcile.Flags().BoolVar(&none, "no-instance", false, l.Text("cloud_flag_no_instance"))
    reconcile.Flags().BoolVar(&yes, "yes", false, l.Text("cloud_flag_yes"))
    reconcile.RunE = func(cmd *cobra.Command, args []string) error {
        if none {
            if err := confirmCloud(o, l, yes, l.Text("cloud_no_instance_confirm")); err != nil {
                return err
            }
        }
        saved, err := o.Cloud.Receipts.Load(args[0])
        if err != nil {
            return err
        }
        boundProduct := saved.Plan.Product
        if saved.Instance != nil {
            boundProduct = saved.Instance.Product
        }
        if account != "" && account != saved.Account || product != "" && product != boundProduct {
            return fault.New("BAD_CONFIG", "cloud_config", nil)
        }
        r, err := o.Cloud.Reconcile(cmd.Context(), args[0], known, none)
        result(r)
        return err
    }
    command.AddCommand(requests, reconcile)
    return command
}

func confirmCloud(o Options, l *i18n.Localizer, yes bool, prompt string) error {
    if yes {
        return nil
    }
    if !o.Interactive {
        return fault.New("BAD_CONFIG", "cloud_confirm_required", nil)
    }
    // JSON callers may never consume an interactive prompt.
    fmt.Fprintln(o.Err, prompt)
    line, err := bufio.NewReader(o.In).ReadString('\n')
    if err != nil && err != io.EOF {
        return err
    }
    if strings.TrimSpace(line) != "yes" {
        return fault.New("BAD_CONFIG", "cloud_confirm_required", nil)
    }
    return nil
}

func bindCloud(ctx context.Context, o Options, name string, b cloud.Binding, remote string, replace bool, ready time.Duration, clone *cloud.CloneSource) (cloudBinding, error) {
    result := cloudBinding{Target: name, Instance: b.Ref, Status: "not_bound"}
    if !config.ValidCloudBinding(b) {
        return result, fault.New("BAD_CONFIG", "cloud_config", nil)
    }
    target := config.Target{Cloud: &b, RemoteBase: remote}
    if replace {
        current, err := o.Store.Load()
        if err != nil {
            return result, err
        }
        if saved, ok := current.Targets[name]; ok && saved.Cloud != nil && *saved.Cloud == b {
            target.Args = append([]string{}, saved.Args...)
        }
    }
    if o.Operations == nil {
        return result, fault.New("SSH_FAILED", "ssh_missing", nil)
    }
    deadline, cancel := context.WithTimeout(ctx, max(ready, 20*time.Second))
    defer cancel()
    p, err := o.Cloud.Open(deadline, b.Ref.Account, b.Settings, b.Ref.Product)
    if err != nil {
        return result, cloud.Failure(err)
    }
    for {
        observed, err := p.Get(deadline, b.Ref)
        if err != nil {
            return result, cloud.Failure(err)
        }
        if clone != nil {
            result.CloneDataStatus = observed.CloneDataStatus
            if b.Ref == clone.Instance || observed.Region != clone.Region {
                return result, fault.New("BAD_CONFIG", "cloud_clone_region", nil)
            }
        }
        if result.Status == "not_bound" {
            if err := o.Store.Add(ctx, name, target, replace); err != nil {
                return result, err
            }
            result.Status = "bound"
        }
        if clone != nil && observed.CloneDataStatus == "failed" {
            return result, fault.New("CLONE_FAILED", "cloud_clone_failed", map[string]any{"ID": b.Ref.ID})
        }
        if err == nil && observed.Status == "running" && (clone == nil || observed.CloneDataStatus == "succeeded") {
            _, err = o.Operations.Exec(deadline, target, ssh.ExecInput{Args: []string{"/bin/true"}, Batch: true}, nil, io.Discard, io.Discard)
            if err == nil {
                result.Status = "ready"
                return result, nil
            }
            // Unknown host/authentication needs human action, not a 2m retry loop.
            f := fault.Wrap("CLOUD_NOT_READY", "cloud_not_ready", err)
            f.Hint = "cloud_connect_hint"
            return result, f
        }
        if ready == 0 || deadline.Err() != nil {
            if clone != nil {
                f := fault.New("CLOUD_NOT_READY", "cloud_clone_not_ready", nil)
                f.Hint = "cloud_clone_wait_hint"
                return result, f
            }
            f := fault.Wrap("CLOUD_NOT_READY", "cloud_not_ready", err)
            f.Hint = "cloud_connect_hint"
            return result, f
        }
        select {
        case <-deadline.Done():
            if clone != nil {
                f := fault.New("CLOUD_NOT_READY", "cloud_clone_not_ready", nil)
                f.Hint = "cloud_clone_wait_hint"
                return result, f
            }
            return result, fault.New("CLOUD_NOT_READY", "cloud_not_ready", nil)
        case <-time.After(10 * time.Second):
        }
    }
}

func cloudProgress(o Options, l *i18n.Localizer, ndjson bool) func(cloud.Event) error {
    return func(e cloud.Event) error {
        if ndjson {
            return json.NewEncoder(o.Out).Encode(envelope{SchemaVersion: 1, OK: true, Data: e})
        }
        fmt.Fprintln(o.Err, l.Text("cloud_progress_"+e.Type))
        if e.Observation != nil {
            printCloud(o.Err, *e.Observation, l)
        }
        if e.RequestID != "" {
            fmt.Fprintln(o.Err, e.RequestID)
        }
        return nil
    }
}

func newCloudAccounts(o Options, cfg config.Global, l *i18n.Localizer, result func(any)) *cobra.Command {
    root := &cobra.Command{Use: "account", Short: l.Text("cloud_account_short"), Args: arguments(0, 0)}
    root.RunE = func(_ *cobra.Command, _ []string) error {
        result(cloudAccounts{Accounts: cfg.Cloud.Accounts, Default: cfg.Cloud.DefaultAccount})
        return nil
    }
    list := &cobra.Command{Use: "list", Short: l.Text("cloud_account_list_short"), Args: arguments(0, 0), RunE: root.RunE}
    add := &cobra.Command{Use: "add " + l.Text("name_arg"), Short: l.Text("cloud_account_add_short"), Args: arguments(1, 1)}
    a := cloud.Account{Provider: "autodl", Credential: cloud.CredentialRef{Mode: "session"}}
    var replace, defaultAccount bool
    add.Flags().StringVar(&a.DefaultProduct, "product", "standard", l.Text("cloud_flag_product"))
    add.Flags().StringVar(&a.Credential.Mode, "auth", "session", l.Text("cloud_flag_auth"))
    add.Flags().StringVar(&a.Credential.Env, "token-env", "", l.Text("cloud_flag_token_env"))
    add.Flags().StringVar(&a.AppVersion, "app-version", "", l.Text("cloud_flag_app_version"))
    add.Flags().BoolVar(&replace, "replace", false, l.Text("cloud_flag_account_replace"))
    add.Flags().BoolVar(&defaultAccount, "default", false, l.Text("cloud_flag_default"))
    add.RunE = func(cmd *cobra.Command, args []string) error {
        if err := o.Store.AddCloudAccount(cmd.Context(), args[0], a, replace, defaultAccount); err != nil {
            return err
        }
        c, err := o.Store.Load()
        result(cloudAccounts{Accounts: c.Cloud.Accounts, Default: c.Cloud.DefaultAccount})
        return err
    }
    remove := &cobra.Command{Use: "remove " + l.Text("name_arg"), Short: l.Text("cloud_account_remove_short"), Args: arguments(1, 1)}
    remove.RunE = func(cmd *cobra.Command, args []string) error {
        if err := o.Store.RemoveCloudAccount(cmd.Context(), args[0]); err != nil {
            return err
        }
        c, err := o.Store.Load()
        result(cloudAccounts{Accounts: c.Cloud.Accounts, Default: c.Cloud.DefaultAccount})
        return err
    }
    root.AddCommand(list, add, remove)
    return root
}

// cloudPlanFlags returns a resolver so Changed, including explicit zero/empty
// values, is evaluated only after Cobra parses this invocation.
func cloudPlanFlags(cmd *cobra.Command, o Options, cfg config.Global, l *i18n.Localizer) func(*cobra.Command, ...cloud.Profile) (cloud.Plan, error) {
    stringsFlags := []string{"profile", "gpu-vendor", "min-gpu-memory", "min-memory", "min-disk", "max-compute-hourly-price", "currency", "wait", "poll-interval", "image", "private-image", "gpu-spec"}
    for _, name := range stringsFlags {
        cmd.Flags().String(name, "", l.Text("cloud_flag_"+strings.ReplaceAll(name, "-", "_")))
    }
    for _, name := range []string{"gpu-count", "min-cpus", "gpu-spec-id", "cuda-from", "expand-system-disk", "expand-data-disk"} {
        cmd.Flags().Int(name, 0, l.Text("cloud_flag_"+strings.ReplaceAll(name, "-", "_")))
        // The effective defaults live in profile resolution, not in pflag.
        cmd.Flags().Lookup(name).DefValue = ""
    }
    cmd.Flags().StringSlice("gpu-model", nil, l.Text("cloud_flag_gpu_model"))
    cmd.Flags().StringSlice("region", nil, l.Text("cloud_flag_region"))
    cmd.Flags().Lookup("gpu-model").DefValue = ""
    cmd.Flags().Lookup("region").DefValue = ""
    return func(cmd *cobra.Command, defaults ...cloud.Profile) (cloud.Plan, error) {
        _, file, err := config.Discover(cmd.Context(), o.CWD)
        if err != nil {
            return cloud.Plan{}, err
        }
        profile, _ := cmd.Flags().GetString("profile")
        if profile != "" {
            _, global := cfg.Resources[profile]
            _, project := file.Resources[profile]
            if !global && !project {
                return cloud.Plan{}, fault.New("BAD_CONFIG", "cloud_profile_unknown", nil)
            }
        }
        explicit := cloud.Profile{}
        for _, field := range []struct {
            flag string
            dest **string
        }{
            {"gpu-vendor", &explicit.GPUVendor}, {"max-compute-hourly-price", &explicit.MaxComputeHourlyPrice}, {"currency", &explicit.Currency}, {"wait", &explicit.WaitTimeout}, {"poll-interval", &explicit.PollInterval},
            {"image", &explicit.AutoDL.Image}, {"private-image", &explicit.AutoDL.PrivateImageUUID}, {"gpu-spec", &explicit.AutoDL.GPUSpecUUID},
        } {
            if cmd.Flags().Changed(field.flag) {
                v, _ := cmd.Flags().GetString(field.flag)
                *field.dest = &v
            }
        }
        for _, field := range []struct {
            flag string
            dest **int
        }{
            {"gpu-spec-id", &explicit.AutoDL.GPUSpecID}, {"gpu-count", &explicit.GPUCount}, {"min-cpus", &explicit.MinCPUs}, {"cuda-from", &explicit.AutoDL.CUDAFrom}, {"expand-system-disk", &explicit.AutoDL.ExpandSystemDiskGB}, {"expand-data-disk", &explicit.AutoDL.ExpandDataDiskGB},
        } {
            if cmd.Flags().Changed(field.flag) {
                v, _ := cmd.Flags().GetInt(field.flag)
                *field.dest = &v
            }
        }
        for _, field := range []struct {
            flag string
            dest **int
        }{{"min-gpu-memory", &explicit.MinGPUMemoryGiB}, {"min-memory", &explicit.MinMemoryGiB}, {"min-disk", &explicit.MinDiskGiB}} {
            if cmd.Flags().Changed(field.flag) {
                value, _ := cmd.Flags().GetString(field.flag)
                if !strings.HasSuffix(value, "GiB") {
                    return cloud.Plan{}, fault.New("BAD_CONFIG", "cloud_config", nil)
                }
                n, err := strconv.Atoi(strings.TrimSuffix(value, "GiB"))
                if err != nil {
                    return cloud.Plan{}, fault.New("BAD_CONFIG", "cloud_config", nil)
                }
                *field.dest = &n
            }
        }
        if cmd.Flags().Changed("gpu-model") {
            explicit.GPUModels, _ = cmd.Flags().GetStringSlice("gpu-model")
            if explicit.GPUModels == nil {
                explicit.GPUModels = []string{}
            }
        }
        if cmd.Flags().Changed("region") {
            explicit.Regions, _ = cmd.Flags().GetStringSlice("region")
            if explicit.Regions == nil {
                explicit.Regions = []string{}
            }
        }
        layers := append(defaults, cfg.Cloud.Defaults, cfg.Resources["default"], cfg.Resources[profile], file.Resources["default"], file.Resources[profile], explicit)
        return cloud.Resolve(cfg.Cloud.Safety, layers...)
    }
}

// DefaultCloudService keeps concrete local state next to the existing index.
func DefaultCloudService(store *config.Store, open cloud.OpenProvider) *cloud.Service {
    return &cloud.Service{Open: open, Receipts: cloud.Receipts{Dir: filepath.Join(filepath.Dir(store.IndexFile), "cloud")}}
}
