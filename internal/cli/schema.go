package cli

import (
    "encoding/csv"
    "strings"
    "time"

    "github.com/alecthomas/kong"
)

// grammar is the sole declaration of commands, flags, arity and help identities.
// Bootstrap and help inspect Kong's compiled model; neither has its own flag list.
type grammar struct {
    Lang        *string        `help:"flag_lang"`
    JSON        bool           `help:"flag_json"`
    Trace       bool           `help:"flag_trace"`
    Target      *string        `help:"flag_target" config:"target"`
    HelpFlag    bool           `name:"help" short:"h" help:"flag_help"`
    VersionFlag bool           `name:"version" help:"flag_version"`
    Machine     machineCommand `cmd:"" group:"machines" help:"machine_short" example:"loaf machine"`
    Check       checkCommand   `cmd:"" group:"diagnostics" help:"check_short" example:"loaf check"`
    Version     struct{}       `cmd:"" group:"diagnostics" help:"version_short" example:"loaf version"`
    Run         runCommand     `cmd:"" group:"experiments" help:"run_short" example:"loaf run -- python3 train.py" usage-args:"run_args"`
    PS          psCommand      `cmd:"" name:"status" group:"experiments" help:"ps_short" example:"loaf status"`
    Logs        logsCommand    `cmd:"" group:"experiments" help:"logs_short" example:"loaf logs"`
    Stop        stopCommand    `cmd:"" group:"experiments" help:"stop_short" example:"loaf stop"`
    Inspect     requiredID     `cmd:"" group:"experiments" help:"inspect_short" example:"loaf inspect r_example"`
    Outputs     outputsCommand `cmd:"" group:"experiments" help:"outputs_short" example:"loaf outputs"`
    Wait        waitCommand    `cmd:"" group:"experiments" help:"wait_short" example:"loaf wait --tag experiment"`
    Pull        pullCommand    `cmd:"" group:"experiments" help:"pull_short" example:"loaf pull"`
    Exec        execCommand    `cmd:"" group:"machines" help:"exec_short" example:"loaf exec -- nvidia-smi" usage-args:"exec_args"`
    SSH         struct{}       `cmd:"" name:"ssh" group:"machines" help:"interactive_short" example:"loaf ssh"`
    Forward     forwardCommand `cmd:"" group:"machines" help:"forward_short" example:"loaf forward 6006"`
    Clean       cleanCommand   `cmd:"" group:"experiments" help:"clean_short" example:"loaf clean r_example --dry-run"`
    Sync        syncCommand    `cmd:"" group:"experiments" help:"sync_short" example:"loaf sync --dry-run"`
    Cloud       cloudCommand   `cmd:"" group:"resources" help:"cloud_short" example:"loaf cloud --help"`
    Queue       queueCommand   `cmd:"" group:"experiments" help:"queue_short" example:"loaf queue"`
    Events      eventsCommand  `cmd:"" group:"diagnostics" help:"events_short" example:"loaf events"`
    Notify      notifyCommand  `cmd:"" group:"diagnostics" help:"notify_short" example:"loaf notify"`
    Help        helpCommand    `cmd:"" group:"diagnostics" help:"help_short" example:"loaf help run"`
}

type machineCommand struct {
    List   struct{}     `cmd:"" default:"1" help:"machine_list_short" example:"loaf machine list"`
    Show   requiredName `cmd:"" help:"machine_show_short" example:"loaf machine show lab"`
    Add    addCommand   `cmd:"" help:"add_short" example:"loaf machine add lab --ssh lab-gpu"`
    Use    useCommand   `cmd:"" help:"use_short" example:"loaf machine use lab"`
    Remove requiredName `cmd:"" help:"remove_short" example:"loaf machine remove lab"`
}

type requiredName struct {
    Name string `arg:"" help:"name_arg" value-type:"name"`
}
type optionalName struct {
    Name string `arg:"" optional:"" help:"targets_name_arg" value-type:"name"`
}
type requiredID struct {
    ID string `arg:"" help:"run_id_arg" value-type:"run-id"`
}
type optionalID struct {
    ID string `arg:"" optional:"" help:"cloud_request_arg" value-type:"request-id"`
}
type helpCommand struct {
    Command []string `arg:"" optional:"" sep:"none" help:"command_arg"`
}
type addCommand struct {
    Name      string `arg:"" help:"name_arg" value-type:"name"`
    SSH       string `help:"flag_ssh"`
    RemoteDir string `default:"~/.local/share/loaf/projects" help:"flag_remote" value-type:"path"`
    Replace   bool   `help:"flag_replace"`
}
type useCommand struct {
    Name string `arg:"" help:"name_arg" value-type:"name"`
}
type checkCommand struct {
    Runner  bool          `help:"flag_runner"`
    Timeout time.Duration `default:"20s" help:"flag_timeout" value-type:"duration"`
    Targets csvList       `help:"flag_check_targets" value-type:"csv"`
    Report  *string       `help:"flag_check_report" value-type:"path"`
}
type syncCommand struct {
    DryRun           bool     `help:"flag_dry_run"`
    Mode             *string  `help:"flag_sync_mode" builtin-default:"mirror" config:"sync.mode"`
    RespectGitignore *bool    `help:"flag_respect_gitignore" builtin-default:"true" config:"sync.respect_gitignore"`
    Exclude          []string `sep:"none" help:"flag_sync_exclude" config:"sync.exclude"`
}
type runCommand struct {
    Notify []string `sep:"none" help:"flag_notify"`
    DryRun bool     `help:"flag_run_dry"`
    Name   string   `help:"flag_run_name"`
    Tag    []string `sep:"none" help:"flag_run_tag"`
    Wait   *string  `help:"flag_wait" builtin-default:"0s" value-type:"duration" config:"run.wait"`
    Argv   []string `arg:"" optional:"" passthrough:"partial" sep:"none"`
}
type psCommand struct {
    ID     string   `arg:"" optional:"" help:"run_id_arg" value-type:"run-id"`
    Wait   string   `default:"0s" help:"flag_ps_wait" builtin-default:"0s" value-type:"duration"`
    Limit  *int     `help:"flag_limit" builtin-default:"20" config:"ps.limit"`
    Status []string `sep:"none" help:"ps_flag_status"`
    Local  bool     `help:"flag_ps_local"`
    Name   *string  `help:"ps_flag_name"`
    Tag    []string `sep:"none" help:"ps_flag_tag"`
    Since  *string  `help:"ps_flag_since"`
    Until  *string  `help:"ps_flag_until"`
}
type logsCommand struct {
    ID             string         `arg:"" optional:"" help:"run_id_arg" value-type:"run-id"`
    Tail           *int           `help:"flag_tail" builtin-default:"100" config:"logs.tail"`
    Follow         bool           `short:"f" help:"flag_follow"`
    Resume         *string        `help:"flag_log_resume" value-type:"cursor"`
    Reconnect      bool           `help:"flag_log_reconnect"`
    ResumeFile     *string        `help:"flag_resume_file" value-type:"path"`
    AckCursor      *string        `help:"flag_ack_cursor" value-type:"cursor"`
    RecoveryBudget *time.Duration `help:"flag_recovery_budget" value-type:"duration"`
}
type stopCommand struct {
    ID    string  `arg:"" optional:"" help:"run_id_arg" value-type:"run-id"`
    Grace *string `help:"flag_stop_grace" builtin-default:"10s" value-type:"duration" config:"stop.grace"`
}
type outputsCommand struct {
    ID            string `arg:"" optional:"" help:"run_id_arg" value-type:"run-id"`
    Depth         int    `default:"2" help:"flag_outputs_depth"`
    Limit         int    `default:"200" help:"flag_outputs_limit"`
    PrepareRunner bool   `help:"flag_outputs_prepare"`
}
type waitCommand struct {
    IDs     []string      `arg:"" optional:"" sep:"none" help:"clean_ids_arg" value-type:"run-id"`
    Tag     []string      `sep:"none" help:"flag_run_tag"`
    Timeout time.Duration `default:"30m" help:"flag_wait_timeout" value-type:"duration"`
}
type pullCommand struct {
    IDs         []string `arg:"" optional:"" sep:"none" help:"clean_ids_arg" value-type:"run-id"`
    Path        []string `sep:"none" help:"flag_pull_path" value-type:"path"`
    To          *string  `help:"flag_pull_to" value-type:"path"`
    Checksum    *bool    `help:"flag_pull_checksum" builtin-default:"false" config:"pull.checksum"`
    Into        *string  `help:"flag_pull_into" value-type:"path"`
    Concurrency *int     `help:"flag_pull_concurrency"`
}
type execCommand struct {
    CWD  *string  `name:"cwd" help:"exec_flag_cwd" builtin-default:"" value-type:"path" config:"exec.cwd"`
    Argv []string `arg:"" optional:"" passthrough:"partial" sep:"none"`
}
type forwardCommand struct {
    Port      int  `arg:"" help:"forward_port_arg"`
    LocalPort *int `help:"forward_flag_local_port"`
}
type cleanCommand struct {
    IDs    []string `arg:"" sep:"none" help:"clean_ids_arg" value-type:"run-id"`
    DryRun bool     `help:"clean_flag_dry_run"`
    Yes    bool     `short:"y" help:"clean_flag_yes"`
}

type cloudCommand struct {
    Account         *string              `help:"cloud_flag_account" config:"cloud.default_account"`
    Product         *string              `help:"cloud_flag_product" config:"resources.product"`
    Login           loginCommand         `cmd:"" help:"cloud_login_short" example:"loaf cloud login main"`
    Logout          requiredName         `cmd:"" help:"cloud_logout_short" example:"loaf cloud logout main"`
    List            cloudListCommand     `cmd:"" help:"cloud_list_short" example:"loaf cloud list"`
    Info            cloudInstanceCommand `cmd:"" help:"cloud_info_short" example:"loaf cloud info instance-id"`
    Credentials     credentialsCommand   `cmd:"" help:"cloud_credentials_short" example:"loaf cloud credentials instance-id"`
    Start           powerCommand         `cmd:"" help:"cloud_start_short" example:"loaf cloud start instance-id"`
    Stop            powerCommand         `cmd:"" help:"cloud_stop_short" example:"loaf cloud stop instance-id"`
    Search          searchCommand        `cmd:"" help:"cloud_search_short" example:"loaf cloud search"`
    Watch           searchCommand        `cmd:"" help:"cloud_watch_short" example:"loaf cloud watch --wait 10m"`
    Acquire         acquireCommand       `cmd:"" help:"cloud_acquire_short" example:"loaf cloud acquire gpu"`
    Bind            bindCommand          `cmd:"" help:"cloud_bind_short" example:"loaf cloud bind gpu --instance instance-id"`
    Requests        optionalID           `cmd:"" help:"cloud_requests_short" example:"loaf cloud requests"`
    Reconcile       reconcileCommand     `cmd:"" help:"cloud_reconcile_short" example:"loaf cloud reconcile request-id --instance instance-id"`
    AccountCommands accountsCommand      `cmd:"" name:"account" help:"cloud_account_short" example:"loaf cloud account"`
}
type cloudInstanceCommand struct {
    ID string `arg:"" help:"cloud_instance_arg" value-type:"instance-id"`
}

type cloudListCommand struct {
    Limit int `default:"100" help:"cloud_flag_limit"`
}
type credentialsCommand struct {
    ID           string `arg:"" help:"cloud_instance_arg" value-type:"instance-id"`
    ShowPassword bool   `help:"cloud_flag_password"`
}
type powerCommand struct {
    ID   string        `arg:"" help:"cloud_instance_arg" value-type:"instance-id"`
    Yes  bool          `help:"cloud_flag_yes"`
    Wait time.Duration `help:"cloud_flag_power_wait" value-type:"duration"`
}
type ResourceFlags struct {
    Profile               string  `help:"cloud_flag_profile"`
    GPUVendor             *string `name:"gpu-vendor" help:"cloud_flag_gpu_vendor" config:"resources.gpu_vendor"`
    GPUModel              csvList `name:"gpu-model" help:"cloud_flag_gpu_model" config:"resources.gpu_models" value-type:"csv"`
    GPUCount              *int    `name:"gpu-count" help:"cloud_flag_gpu_count" builtin-default:"1" config:"resources.gpu_count"`
    MinGPUMemory          *string `name:"min-gpu-memory" help:"cloud_flag_min_gpu_memory" config:"resources.min_gpu_memory_gib" value-type:"gib"`
    MinCPUs               *int    `name:"min-cpus" help:"cloud_flag_min_cpus" config:"resources.min_cpus"`
    MinMemory             *string `help:"cloud_flag_min_memory" config:"resources.min_memory_gib" value-type:"gib"`
    MinDisk               *string `help:"cloud_flag_min_disk" config:"resources.min_disk_gib" value-type:"gib"`
    Region                csvList `help:"cloud_flag_region" config:"resources.regions" value-type:"csv"`
    MaxComputeHourlyPrice *string `help:"cloud_flag_max_compute_hourly_price" config:"resources.max_compute_hourly_price" value-type:"decimal"`
    Currency              *string `help:"cloud_flag_currency" builtin-default:"CNY" config:"resources.currency"`
    Wait                  *string `help:"cloud_flag_wait" builtin-default:"0s" config:"resources.wait_timeout" value-type:"duration"`
    PollInterval          *string `help:"cloud_flag_poll_interval" builtin-default:"10s" config:"resources.poll_interval" value-type:"duration"`
    Image                 *string `help:"cloud_flag_image" config:"resources.autodl.image"`
    PrivateImage          *string `help:"cloud_flag_private_image" config:"resources.autodl.private_image_uuid"`
    GPUSpec               *string `name:"gpu-spec" help:"cloud_flag_gpu_spec" config:"resources.autodl.gpu_spec_uuid"`
    GPUSpecID             *int    `name:"gpu-spec-id" help:"cloud_flag_gpu_spec_id" config:"resources.autodl.gpu_spec_id"`
    CUDAFrom              *int    `name:"cuda-from" help:"cloud_flag_cuda_from" config:"resources.autodl.cuda_from"`
    ExpandSystemDisk      *int    `help:"cloud_flag_expand_system_disk" config:"resources.autodl.expand_system_disk_gb"`
    ExpandDataDisk        *int    `help:"cloud_flag_expand_data_disk" config:"resources.autodl.expand_data_disk_gb"`
}
type searchCommand struct {
    ResourceFlags `embed:""`
    Limit         int `default:"100" help:"cloud_flag_limit"`
}
type acquireCommand struct {
    Notify        []string `sep:"none" help:"flag_notify"`
    Name          string   `arg:"" help:"name_arg" value-type:"name"`
    ResourceFlags `embed:""`
    Limit         int           `default:"100" help:"cloud_flag_limit"`
    CloneFrom     *string       `help:"cloud_flag_clone_from"`
    Yes           bool          `help:"cloud_flag_yes"`
    AllowUnquoted bool          `help:"cloud_flag_unquoted"`
    RemoteDir     string        `default:"~/.local/share/loaf/projects" help:"flag_remote" value-type:"path"`
    ReadyTimeout  time.Duration `default:"2m" help:"cloud_flag_ready" value-type:"duration"`
}
type bindCommand struct {
    Name         string        `arg:"" help:"name_arg" value-type:"name"`
    Instance     string        `help:"cloud_flag_instance"`
    Request      string        `help:"cloud_flag_request"`
    RemoteDir    *string       `help:"flag_remote" value-type:"path"`
    Replace      bool          `help:"flag_replace"`
    ReadyTimeout time.Duration `default:"2m" help:"cloud_flag_ready" value-type:"duration"`
}
type reconcileCommand struct {
    ID         string `arg:"" help:"cloud_required_request_arg" value-type:"request-id"`
    Instance   string `help:"cloud_flag_instance"`
    NoInstance bool   `help:"cloud_flag_no_instance"`
    Yes        bool   `help:"cloud_flag_yes"`
}
type accountsCommand struct {
    List   struct{}          `cmd:"" default:"1" help:"cloud_account_list_short" example:"loaf cloud account list"`
    Add    accountAddCommand `cmd:"" help:"cloud_account_add_short" example:"loaf cloud account add main --token-env AUTODL_TOKEN"`
    Remove requiredName      `cmd:"" help:"cloud_account_remove_short" example:"loaf cloud account remove main"`
}
type accountAddCommand struct {
    Name       string `arg:"" help:"name_arg" value-type:"name"`
    Auth       string `default:"session" help:"cloud_flag_auth"`
    TokenEnv   string `help:"cloud_flag_token_env"`
    AppVersion string `help:"cloud_flag_app_version"`
    Replace    bool   `help:"cloud_flag_account_replace"`
    Default    bool   `help:"cloud_flag_default"`
}
type loginCommand struct {
    Name             string  `arg:"" help:"name_arg" value-type:"name"`
    Username         string  `help:"cloud_flag_username"`
    CredentialStore  *string `help:"cloud_flag_credential_store" config:"cloud.credential_store"`
    PhoneArea        *string `help:"cloud_flag_phone_area"`
    PasswordStdin    bool    `help:"cloud_flag_password_stdin"`
    Prompt           bool    `help:"cloud_flag_prompt_password"`
    Verify           bool    `help:"cloud_flag_verify"`
    RememberPassword bool    `default:"true" help:"cloud_flag_remember"`
    Replace          bool    `help:"cloud_flag_account_replace"`
    Default          bool    `help:"cloud_flag_default"`
}

type eventsCommand struct {
    List eventListCommand `cmd:"" default:"1" help:"events_list_short" example:"loaf events list"`
}

type eventListCommand struct {
    Source  string `default:"local" enum:"local,resources,project,queue" help:"flag_event_source"`
    Subject string `help:"flag_event_subject"`
    Cursor  string `help:"flag_event_cursor" value-type:"cursor"`
    Limit   int    `default:"100" help:"flag_event_limit"`
}

type notifyCommand struct {
    Destination   destinationCommand   `cmd:"" help:"notify_destination_short" example:"loaf notify destination --help"`
    Subscribe     subscribeCommand     `cmd:"" help:"notify_subscribe_short" example:"loaf notify subscribe run:r_example --destination research"`
    Subscriptions struct{}             `cmd:"" help:"notify_subscriptions_short" example:"loaf notify subscriptions"`
    Deliveries    deliveryListCommand  `cmd:"" default:"1" help:"notify_deliveries_short" example:"loaf notify deliveries"`
    Serve         notifyServeCommand   `cmd:"" help:"notify_serve_short" example:"loaf notify serve --once"`
    Channel       notifyChannelCommand `cmd:"" help:"notify_channel_short" example:"loaf notify channel --socket /private/channel.sock --session research"`
}

type destinationCommand struct {
    List struct{}              `cmd:"" default:"1" help:"notify_destination_list_short" example:"loaf notify destination list"`
    Add  destinationAddCommand `cmd:"" help:"notify_destination_add_short" example:"loaf notify destination add research --kind codex --session thread-id"`
}

type destinationAddCommand struct {
    Name     string `arg:"" help:"name_arg" value-type:"name"`
    Kind     string `required:"" enum:"codex,claude-channel,claude-resume,webhook" help:"flag_destination_kind"`
    Session  string `help:"flag_destination_session"`
    Endpoint string `help:"flag_destination_endpoint"`
    CWD      string `name:"cwd" help:"flag_destination_cwd" value-type:"path"`
    Replace  bool   `help:"flag_destination_replace"`
}

type subscribeCommand struct {
    Subject     string   `arg:"" help:"event_subject_arg"`
    Destination []string `required:"" sep:"none" help:"flag_subscription_destination"`
    Source      string   `default:"project" enum:"local,resources,project,queue" help:"flag_event_source"`
    Replay      bool     `default:"true" help:"flag_subscription_replay"`
}

type deliveryListCommand struct {
    Limit int `default:"100" help:"flag_event_limit"`
}

type notifyServeCommand struct {
    Once     bool          `help:"flag_notify_once"`
    Interval time.Duration `default:"5s" help:"flag_notify_interval" value-type:"duration"`
}

type notifyChannelCommand struct {
    Socket  string `required:"" help:"flag_channel_socket" value-type:"path"`
    Session string `required:"" help:"flag_channel_session"`
}

type queueCommand struct {
    List   queueListCommand   `cmd:"" default:"1" help:"queue_list_short" example:"loaf queue list"`
    Enable queueEnableCommand `cmd:"" help:"queue_enable_short" example:"loaf queue enable --slots 1"`
    Submit queueSubmitCommand `cmd:"" help:"queue_submit_short" example:"loaf queue submit -- python3 train.py" usage-args:"run_args"`
    Show   queueIDCommand     `cmd:"" help:"queue_show_short" example:"loaf queue show q_example"`
    Cancel queueIDCommand     `cmd:"" help:"queue_cancel_short" example:"loaf queue cancel q_example"`
    Pause  struct{}           `cmd:"" help:"queue_pause_short" example:"loaf queue pause"`
    Resume struct{}           `cmd:"" help:"queue_resume_short" example:"loaf queue resume"`
    Clean  queueCleanCommand  `cmd:"" help:"queue_clean_short" example:"loaf queue clean q_example --dry-run"`
}

type queueListCommand struct {
    Limit int `default:"100" help:"flag_event_limit"`
}
type queueEnableCommand struct {
    Slots int `default:"1" help:"flag_queue_slots"`
}
type queueSubmitCommand struct {
    DryRun bool     `help:"flag_queue_dry_run"`
    Name   string   `help:"flag_run_name"`
    Tag    []string `sep:"none" help:"flag_run_tag"`
    Notify []string `sep:"none" help:"flag_notify"`
    Argv   []string `arg:"" optional:"" passthrough:"partial" sep:"none" help:"run_args"`
}
type queueIDCommand struct {
    ID string `arg:"" help:"queue_id_arg" value-type:"request-id"`
}
type queueCleanCommand struct {
    IDs    []string `arg:"" sep:"none" help:"queue_ids_arg" value-type:"request-id"`
    DryRun bool     `help:"flag_queue_clean_dry_run"`
    Yes    bool     `short:"y" help:"clean_flag_yes"`
}

// csvList retains explicit empty replacement and the existing CSV flag grammar.
type csvList struct {
    Values  []string
    Present bool
}

func (v *csvList) Decode(ctx *kong.DecodeContext) error {
    token, err := ctx.Scan.PopValue("list")
    if err != nil {
        return err
    }
    text, ok := token.Value.(string)
    if !ok {
        return nil
    }
    v.Present = true
    if text == "" {
        if v.Values == nil {
            v.Values = []string{}
        }
        return nil
    }
    items, err := csv.NewReader(strings.NewReader(text)).Read()
    if err != nil {
        return err
    }
    v.Values = append(v.Values, items...)
    return nil
}
