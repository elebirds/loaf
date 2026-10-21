package cli

import (
    "bufio"
    "fmt"
    "io"
    "strings"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
    "github.com/spf13/cobra"
)

type loginResult struct {
    Account    string         `json:"account"`
    Identity   cloud.Identity `json:"identity"`
    Store      string         `json:"credential_store"`
    Remembered bool           `json:"password_remembered"`
    Standard   bool           `json:"standard"`
    Pro        *bool          `json:"pro,omitempty"`
}
type logoutResult struct {
    Account string `json:"account"`
    Status  string `json:"status"`
}

func passwordLine(r *bufio.Reader) ([]byte, error) {
    line, err := r.ReadString('\n')
    if err != nil && err != io.EOF {
        return nil, err
    }
    if len(line) > 4096 {
        return nil, fault.New("BAD_CONFIG", "cloud_password_required", nil)
    }
    line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
    if line == "" {
        return nil, fault.New("BAD_CONFIG", "cloud_password_required", nil)
    }
    return []byte(line), nil
}

func newCloudLogin(o Options, cfg config.Global, l *i18n.Localizer, result func(any)) *cobra.Command {
    cmd := &cobra.Command{Use: "login " + l.Text("name_arg"), Short: l.Text("cloud_login_short"), Args: arguments(1, 1)}
    var username, area, source string
    var stdin, prompt, verify, remember, replace, makeDefault bool
    cmd.Flags().StringVar(&username, "username", "", l.Text("cloud_flag_username"))
    cmd.Flags().StringVar(&source, "credential-store", "", l.Text("cloud_flag_credential_store"))
    cmd.Flags().StringVar(&area, "phone-area", "+86", l.Text("cloud_flag_phone_area"))
    cmd.Flags().BoolVar(&stdin, "password-stdin", false, l.Text("cloud_flag_password_stdin"))
    cmd.Flags().BoolVar(&prompt, "prompt", false, l.Text("cloud_flag_prompt_password"))
    cmd.Flags().BoolVar(&verify, "verify", false, l.Text("cloud_flag_verify"))
    cmd.Flags().BoolVar(&remember, "remember-password", true, l.Text("cloud_flag_remember"))
    cmd.Flags().BoolVar(&replace, "replace", false, l.Text("cloud_flag_account_replace"))
    cmd.Flags().BoolVar(&makeDefault, "default", false, l.Text("cloud_flag_default"))
    cmd.RunE = func(cmd *cobra.Command, args []string) error {
        if !config.ValidName(args[0]) || o.Auth == nil {
            return fault.New("BAD_CONFIG", "cloud_config", nil)
        }
        if stdin && prompt {
            return fault.New("BAD_CONFIG", "invalid_flags", nil)
        }
        jsonMode, _ := cmd.Flags().GetBool("json")
        interactive := o.Interactive && !jsonMode
        var previous *cloud.Account
        if old, ok := cfg.Cloud.Accounts[args[0]]; ok {
            previous = &old
        }
        if !cmd.Flags().Changed("credential-store") {
            if previous != nil && previous.Credential.Key != "" {
                source = previous.Credential.Store
            } else {
                source = cfg.Cloud.CredentialStore
            }
        }
        if source == "" && !cmd.Flags().Changed("credential-store") {
            source = "keyring"
        }
        if !cloud.ValidCredentialStore(source) {
            return fault.New("BAD_CONFIG", "cloud_config", nil)
        }
        in := cloud.LoginInput{Username: username, PhoneArea: area}
        if previous != nil && previous.Credential.Key != "" && !stdin && !prompt {
            saved, err := o.Auth.Read(cmd.Context(), previous.Credential)
            if err != nil {
                return cloud.Failure(err)
            }
            if username == "" || username == saved.Username {
                in.Username, in.Digest, in.DeviceID = saved.Username, saved.Digest, saved.DeviceID
                if !cmd.Flags().Changed("phone-area") {
                    in.PhoneArea = saved.PhoneArea
                }
            }
        }
        reader := bufio.NewReader(io.LimitReader(o.In, 8194))
        if in.Username == "" {
            if !interactive {
                return fault.New("BAD_CONFIG", "cloud_username_required", nil)
            }
            fmt.Fprint(o.Err, l.Text("cloud_username_prompt"))
            line, err := reader.ReadString('\n')
            if err != nil && err != io.EOF {
                return err
            }
            in.Username = strings.TrimSpace(line)
        }
        if in.Username == "" || len(in.Username) > 512 || strings.ContainsAny(in.Username, "\x00\r\n") {
            return fault.New("BAD_CONFIG", "cloud_username_required", nil)
        }
        if in.Digest == "" {
            var err error
            if stdin {
                in.Password, err = passwordLine(reader)
            } else {
                if !interactive || o.ReadPassword == nil {
                    return fault.New("BAD_CONFIG", "cloud_password_required", nil)
                }
                fmt.Fprint(o.Err, l.Text("cloud_password_prompt"))
                in.Password, err = o.ReadPassword(cmd.Context())
                fmt.Fprintln(o.Err)
            }
            defer clear(in.Password)
            if err != nil {
                return err
            }
            if len(in.Password) == 0 || len(in.Password) > 4096 {
                return fault.New("BAD_CONFIG", "cloud_password_required", nil)
            }
        }
        if verify {
            if !interactive || o.ReadPassword == nil {
                return fault.New("BAD_CONFIG", "cloud_verification_required", nil)
            }
            fmt.Fprint(o.Err, l.Text("cloud_verification_prompt"))
            code, err := o.ReadPassword(cmd.Context())
            fmt.Fprintln(o.Err)
            if err != nil {
                return err
            }
            in.Code = string(code)
            clear(code)
        }
        defaultProduct, _ := cmd.Flags().GetString("product")
        _, session, err := o.Auth.SignIn(cmd.Context(), in, previous, cloud.LoginOptions{Store: source, Replace: replace, Remember: remember}, func(a cloud.Account) error {
            if cmd.Flags().Changed("product") {
                a.DefaultProduct = defaultProduct
            }
            return o.Store.SaveLogin(cmd.Context(), args[0], a, previous, makeDefault)
        })
        if err != nil {
            return cloud.Failure(err)
        }
        result(loginResult{Account: args[0], Store: source, Identity: session.Identity, Remembered: remember, Standard: session.Standard, Pro: session.Pro})
        return nil
    }
    return cmd
}

func newCloudLogout(o Options, cfg config.Global, l *i18n.Localizer, result func(any)) *cobra.Command {
    return &cobra.Command{Use: "logout " + l.Text("name_arg"), Short: l.Text("cloud_logout_short"), Args: arguments(1, 1), RunE: func(cmd *cobra.Command, args []string) error {
        a, ok := cfg.Cloud.Accounts[args[0]]
        if !ok || o.Auth == nil {
            return fault.New("BAD_CONFIG", "cloud_account_unknown", nil)
        }
        if err := o.Auth.Forget(cmd.Context(), a.Credential); err != nil {
            return cloud.Failure(err)
        }
        result(logoutResult{Account: args[0], Status: "logged_out"})
        return nil
    }}
}
