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
    Lang     *string        `help:"flag_lang"`
    JSON     bool           `help:"flag_json"`
    Trace    bool           `help:"flag_trace"`
    Target   *string        `help:"flag_target"`
    HelpFlag bool           `name:"help" short:"h" help:"flag_help"`
    Add      addCommand     `cmd:"" help:"add_short"`
    Use      useCommand     `cmd:"" help:"use_short"`
    Check    checkCommand   `cmd:"" help:"check_short"`
    Version  struct{}       `cmd:"" help:"version_short"`
    Sync     syncCommand    `cmd:"" help:"sync_short"`
    Targets  optionalName   `cmd:"" help:"targets_short"`
    Remove   requiredName   `cmd:"" help:"remove_short"`
    Run      runCommand     `cmd:"" help:"run_short"`
    PS       psCommand      `cmd:"" name:"ps" help:"ps_short"`
    Logs     logsCommand    `cmd:"" help:"logs_short"`
    Stop     stopCommand    `cmd:"" help:"stop_short"`
    Inspect  requiredID     `cmd:"" help:"inspect_short"`
    Outputs  outputsCommand `cmd:"" help:"outputs_short"`
    Wait     waitCommand    `cmd:"" help:"wait_short"`
    Pull     pullCommand    `cmd:"" help:"pull_short"`
    Exec     execCommand    `cmd:"" help:"exec_short"`
    SSH      struct{}       `cmd:"" name:"ssh" help:"interactive_short"`
    Forward  forwardCommand `cmd:"" help:"forward_short"`
    Clean    cleanCommand   `cmd:"" help:"clean_short"`
    Cloud    cloudCommand   `cmd:"" help:"cloud_short"`
    Help     helpCommand    `cmd:"" help:"help_short"`
}

type requiredName struct {
    Name string `arg:"" help:"name_arg"`
}
type optionalName struct {
    Name string `arg:"" optional:"" help:"targets_name_arg"`
}
type requiredID struct {
    ID string `arg:"" help:"run_id_arg"`
}
type optionalID struct {
    ID string `arg:"" optional:"" help:"cloud_request_arg"`
}
type helpCommand struct {
    Command []string `arg:"" optional:"" sep:"none" help:"command_arg"`
}
type addCommand struct {
    Name      string `arg:"" help:"name_arg"`
    SSH       string `help:"flag_ssh"`
    RemoteDir string `default:"~/.local/share/loaf/projects" help:"flag_remote"`
    Replace   bool   `help:"flag_replace"`
}
type useCommand struct {
    Name string `arg:"" help:"name_arg"`
}
type checkCommand struct {
    Runner  bool          `help:"flag_runner"`
    Timeout time.Duration `default:"20s" help:"flag_timeout"`
    Targets csvList       `help:"flag_check_targets"`
    Report  *string       `help:"flag_check_report"`
}
type syncCommand struct {
    DryRun           bool     `help:"flag_dry_run"`
    Mode             *string  `help:"flag_sync_mode"`
    RespectGitignore *bool    `help:"flag_respect_gitignore"`
    Exclude          []string `sep:"none" help:"flag_sync_exclude"`
}
type runCommand struct {
    DryRun bool     `help:"flag_run_dry"`
    Name   string   `help:"flag_run_name"`
    Tag    []string `sep:"none" help:"flag_run_tag"`
    Wait   *string  `help:"flag_wait"`
    Argv   []string `arg:"" optional:"" passthrough:"partial" sep:"none"`
}
type psCommand struct {
    ID     string   `arg:"" optional:"" help:"run_id_arg"`
    Wait   string   `default:"0s" help:"flag_ps_wait"`
    Limit  *int     `help:"flag_limit"`
    Status []string `sep:"none" help:"ps_flag_status"`
    Local  bool     `help:"flag_ps_local"`
    Name   *string  `help:"ps_flag_name"`
    Tag    []string `sep:"none" help:"ps_flag_tag"`
    Since  *string  `help:"ps_flag_since"`
    Until  *string  `help:"ps_flag_until"`
}
type logsCommand struct {
    ID             string         `arg:"" optional:"" help:"run_id_arg"`
    Tail           *int           `help:"flag_tail"`
    Follow         bool           `short:"f" help:"flag_follow"`
    Resume         *string        `help:"flag_log_resume"`
    Reconnect      bool           `help:"flag_log_reconnect"`
    ResumeFile     *string        `help:"flag_resume_file"`
    AckCursor      *string        `help:"flag_ack_cursor"`
    RecoveryBudget *time.Duration `help:"flag_recovery_budget"`
}
type stopCommand struct {
    ID    string  `arg:"" optional:"" help:"run_id_arg"`
    Grace *string `help:"flag_stop_grace"`
}
type outputsCommand struct {
    ID            string `arg:"" optional:"" help:"run_id_arg"`
    Depth         int    `default:"2" help:"flag_outputs_depth"`
    Limit         int    `default:"200" help:"flag_outputs_limit"`
    PrepareRunner bool   `help:"flag_outputs_prepare"`
}
type waitCommand struct {
    IDs     []string      `arg:"" optional:"" sep:"none" help:"clean_ids_arg"`
    Tag     []string      `sep:"none" help:"flag_run_tag"`
    Timeout time.Duration `default:"30m" help:"flag_wait_timeout"`
}
type pullCommand struct {
    IDs         []string `arg:"" optional:"" sep:"none" help:"clean_ids_arg"`
    Path        []string `sep:"none" help:"flag_pull_path"`
    To          *string  `help:"flag_pull_to"`
    Checksum    *bool    `help:"flag_pull_checksum"`
    Into        *string  `help:"flag_pull_into"`
    Concurrency *int     `help:"flag_pull_concurrency"`
}
type execCommand struct {
    CWD  *string  `name:"cwd" help:"exec_flag_cwd"`
    Argv []string `arg:"" optional:"" passthrough:"partial" sep:"none"`
}
type forwardCommand struct {
    Port      int  `arg:"" help:"forward_port_arg"`
    LocalPort *int `help:"forward_flag_local_port"`
}
type cleanCommand struct {
    IDs    []string `arg:"" sep:"none" help:"clean_ids_arg"`
    DryRun bool     `help:"clean_flag_dry_run"`
    Yes    bool     `short:"y" help:"clean_flag_yes"`
}

type cloudCommand struct {
    Account         *string              `help:"cloud_flag_account"`
    Product         *string              `help:"cloud_flag_product"`
    Login           loginCommand         `cmd:"" help:"cloud_login_short"`
    Logout          requiredName         `cmd:"" help:"cloud_logout_short"`
    List            cloudListCommand     `cmd:"" help:"cloud_list_short"`
    Info            cloudInstanceCommand `cmd:"" help:"cloud_info_short"`
    Credentials     credentialsCommand   `cmd:"" help:"cloud_credentials_short"`
    Start           powerCommand         `cmd:"" help:"cloud_start_short"`
    Stop            powerCommand         `cmd:"" help:"cloud_stop_short"`
    Search          searchCommand        `cmd:"" help:"cloud_search_short"`
    Watch           searchCommand        `cmd:"" help:"cloud_watch_short"`
    Acquire         acquireCommand       `cmd:"" help:"cloud_acquire_short"`
    Bind            bindCommand          `cmd:"" help:"cloud_bind_short"`
    Requests        optionalID           `cmd:"" help:"cloud_requests_short"`
    Reconcile       reconcileCommand     `cmd:"" help:"cloud_reconcile_short"`
    AccountCommands accountsCommand      `cmd:"" name:"account" help:"cloud_account_short"`
}
type cloudInstanceCommand struct {
    ID string `arg:"" help:"cloud_instance_arg"`
}

type cloudListCommand struct {
    Limit int `default:"100" help:"cloud_flag_limit"`
}
type credentialsCommand struct {
    ID           string `arg:"" help:"cloud_instance_arg"`
    ShowPassword bool   `help:"cloud_flag_password"`
}
type powerCommand struct {
    ID   string        `arg:"" help:"cloud_instance_arg"`
    Yes  bool          `help:"cloud_flag_yes"`
    Wait time.Duration `help:"cloud_flag_power_wait"`
}
type ResourceFlags struct {
    Profile               string  `help:"cloud_flag_profile"`
    GPUVendor             *string `name:"gpu-vendor" help:"cloud_flag_gpu_vendor"`
    GPUModel              csvList `name:"gpu-model" help:"cloud_flag_gpu_model"`
    GPUCount              *int    `name:"gpu-count" help:"cloud_flag_gpu_count"`
    MinGPUMemory          *string `name:"min-gpu-memory" help:"cloud_flag_min_gpu_memory"`
    MinCPUs               *int    `name:"min-cpus" help:"cloud_flag_min_cpus"`
    MinMemory             *string `help:"cloud_flag_min_memory"`
    MinDisk               *string `help:"cloud_flag_min_disk"`
    Region                csvList `help:"cloud_flag_region"`
    MaxComputeHourlyPrice *string `help:"cloud_flag_max_compute_hourly_price"`
    Currency              *string `help:"cloud_flag_currency"`
    Wait                  *string `help:"cloud_flag_wait"`
    PollInterval          *string `help:"cloud_flag_poll_interval"`
    Image                 *string `help:"cloud_flag_image"`
    PrivateImage          *string `help:"cloud_flag_private_image"`
    GPUSpec               *string `name:"gpu-spec" help:"cloud_flag_gpu_spec"`
    GPUSpecID             *int    `name:"gpu-spec-id" help:"cloud_flag_gpu_spec_id"`
    CUDAFrom              *int    `name:"cuda-from" help:"cloud_flag_cuda_from"`
    ExpandSystemDisk      *int    `help:"cloud_flag_expand_system_disk"`
    ExpandDataDisk        *int    `help:"cloud_flag_expand_data_disk"`
}
type searchCommand struct {
    ResourceFlags `embed:""`
    Limit         int `default:"100" help:"cloud_flag_limit"`
}
type acquireCommand struct {
    Name          string `arg:"" help:"name_arg"`
    ResourceFlags `embed:""`
    Limit         int           `default:"100" help:"cloud_flag_limit"`
    CloneFrom     *string       `help:"cloud_flag_clone_from"`
    Yes           bool          `help:"cloud_flag_yes"`
    AllowUnquoted bool          `help:"cloud_flag_unquoted"`
    RemoteDir     string        `default:"~/.local/share/loaf/projects" help:"flag_remote"`
    ReadyTimeout  time.Duration `default:"2m" help:"cloud_flag_ready"`
}
type bindCommand struct {
    Name         string        `arg:"" help:"name_arg"`
    Instance     string        `help:"cloud_flag_instance"`
    Request      string        `help:"cloud_flag_request"`
    RemoteDir    *string       `help:"flag_remote"`
    Replace      bool          `help:"flag_replace"`
    ReadyTimeout time.Duration `default:"2m" help:"cloud_flag_ready"`
}
type reconcileCommand struct {
    ID         string `arg:"" help:"cloud_required_request_arg"`
    Instance   string `help:"cloud_flag_instance"`
    NoInstance bool   `help:"cloud_flag_no_instance"`
    Yes        bool   `help:"cloud_flag_yes"`
}
type accountsCommand struct {
    List   struct{}          `cmd:"" default:"1" help:"cloud_account_list_short"`
    Add    accountAddCommand `cmd:"" help:"cloud_account_add_short"`
    Remove requiredName      `cmd:"" help:"cloud_account_remove_short"`
}
type accountAddCommand struct {
    Name       string `arg:"" help:"name_arg"`
    Auth       string `default:"session" help:"cloud_flag_auth"`
    TokenEnv   string `help:"cloud_flag_token_env"`
    AppVersion string `help:"cloud_flag_app_version"`
    Replace    bool   `help:"cloud_flag_account_replace"`
    Default    bool   `help:"cloud_flag_default"`
}
type loginCommand struct {
    Name             string  `arg:"" help:"name_arg"`
    Username         string  `help:"cloud_flag_username"`
    CredentialStore  *string `help:"cloud_flag_credential_store"`
    PhoneArea        *string `help:"cloud_flag_phone_area"`
    PasswordStdin    bool    `help:"cloud_flag_password_stdin"`
    Prompt           bool    `help:"cloud_flag_prompt_password"`
    Verify           bool    `help:"cloud_flag_verify"`
    RememberPassword bool    `default:"true" help:"cloud_flag_remember"`
    Replace          bool    `help:"cloud_flag_account_replace"`
    Default          bool    `help:"cloud_flag_default"`
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
