package cli

import (
    "context"
    "fmt"
    "sort"
    "strings"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
)

func (c *invocation) chooseCloudInstance(ctx context.Context, selection *client.CloudSelect, id string) (string, error) {
    if id != "" {
        return id, nil
    }
    if c.policy.Batch {
        return "", fault.Invalid("instance", "missing_argument", "cloud_instance_required")
    }
    r := c.app.Resources
    if selection.Account == nil && r.Settings.Cloud.DefaultAccount == "" {
        names := make([]string, 0, len(r.Settings.Cloud.Accounts))
        for n := range r.Settings.Cloud.Accounts {
            names = append(names, n)
        }
        sort.Strings(names)
        if len(names) == 0 {
            return "", fault.New("BAD_CONFIG", "cloud_account_unknown", nil)
        }
        name := names[0]
        if len(names) > 1 {
            choices := []interaction.Choice{}
            for _, n := range names {
                choices = append(choices, interaction.Choice{Label: n, Value: n})
            }
            v, err := c.input.Select(ctx, c.output.locale.Text("cloud_wizard_account"), choices, "")
            if err != nil {
                return "", err
            }
            name = v
        }
        selection.Account = &name
    }
    page, err := r.CloudList(ctx, *selection, 100)
    if err != nil && len(page.Instances) == 0 {
        return "", err
    }
    if len(page.Instances) == 0 {
        return "", fault.Invalid("instance", "missing_argument", "cloud_instance_required")
    }
    summary := cloudCoverage(c.output.locale, page.Examined, page.Complete)
    if err != nil {
        summary += "\n" + c.output.error(err).Message
    }
    choices := []interaction.Choice{}
    for _, i := range page.Instances {
        label := fmt.Sprintf("%s · %s/%s · %s", i.Ref.ID, i.Ref.Account, i.Ref.Product, c.output.locale.Text("cloud_status_"+i.Status))
        if len(i.Aliases) > 0 {
            label += " · " + strings.Join(i.Aliases, ", ")
        }
        choices = append(choices, interaction.Choice{Value: i.Ref.ID, Label: label})
    }
    selection.Account, selection.Product = &page.Account, &page.Product
    c.grammar.Cloud.Account, c.grammar.Cloud.Product = selection.Account, selection.Product
    return c.input.SelectWithSummary(ctx, c.output.locale.Text("cloud_wizard_instance"), summary, choices, "")
}
