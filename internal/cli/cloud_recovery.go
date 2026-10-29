package cli

import (
    "context"
    "errors"
    "fmt"
    "time"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/ssh"
)

func (c *invocation) recoverCloudBinding(ctx context.Context, binding client.CloudBinding, cause error) (client.CloudBinding, error) {
    if c.policy.Batch || binding.Instance.ID == "" || errors.Is(cause, context.Canceled) || ctx.Err() != nil {
        return binding, cause
    }
    for cause != nil && ctx.Err() == nil {
        var f *fault.Error
        if !errors.As(cause, &f) || f.Code != "CLOUD_NOT_READY" {
            return binding, cause
        }
        l := c.output.locale
        choices := []interaction.Choice{}
        if binding.Status == "bound" && ssh.FailureKind(cause) == "host_trust" && ssh.FailureReason(cause) != "host_key_changed" {
            choices = append(choices, interaction.Choice{Label: l.Text("cloud_recover_trust"), Value: "trust"})
        }
        choices = append(choices, interaction.Choice{Label: l.Text("cloud_recover_retry"), Value: "retry"}, interaction.Choice{Label: l.Text("cloud_recover_connection"), Value: "connection"}, interaction.Choice{Label: l.Text("cloud_recover_leave"), Value: "leave"})
        summary := cloudScope(l, binding.Instance.Account, binding.Instance.Product, binding.Target) + "\n" + l.Text("cloud_ui_billing_active")
        reason := ssh.FailureKind(cause)
        if reason != "operation_failed" && reason != "" {
            summary += "\n" + l.Text("cloud_failure_"+reason)
        }
        action, err := c.input.SelectWithSummary(ctx, l.Text("cloud_recover_title"), summary, choices, "leave")
        if err != nil {
            return binding, err
        }
        switch action {
        case "leave":
            return binding, cause
        case "trust":
            if err := c.app.Resources.ConfirmCloudTrust(ctx, binding, client.Streams{In: c.options.In, Out: c.options.Out, Err: c.options.Err}); err != nil {
                cause = err
                continue
            }
            if _, err := fmt.Fprintln(c.output.err, l.Text("cloud_trust_confirmed")); err != nil {
                return binding, err
            }
        case "connection":
            result, err := c.app.Resources.CloudBindingCredentials(ctx, binding)
            if err != nil {
                return binding, err
            }
            printCloud(c.output.err, result, l)
            continue
        }
        runCtx, cancel := context.WithCancel(ctx)
        feedback := &cloudFeedback{call: c, mode: "auto", ctx: runCtx, cancel: cancel}
        binding, cause = c.app.Resources.RetryCloudBinding(runCtx, binding, client.CloudBindInput{ReadyTimeout: 5 * time.Minute, Progress: feedback.emit})
        err = feedback.close()
        cancel()
        if err != nil {
            return binding, err
        }
    }
    return binding, cause
}
