package cli

import (
    "context"
    "reflect"
    "sort"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
)

// chooseScope edits only the pending acquisition. It neither saves a default nor
// opens an account-management workflow.
func (d *cloudDraft) chooseScope(ctx context.Context, c *invocation) (bool, error) {
    names := make([]string, 0, len(c.app.Resources.Settings.Cloud.Accounts))
    for name := range c.app.Resources.Settings.Cloud.Accounts {
        names = append(names, name)
    }
    sort.Strings(names)
    choices := make([]interaction.Choice, 0, len(names))
    for _, name := range names {
        choices = append(choices, interaction.Choice{Label: name, Value: name})
    }
    current := ""
    if d.in.Select.Account != nil {
        current = *d.in.Select.Account
    }
    name, err := c.input.Select(ctx, c.output.locale.Text("cloud_wizard_account"), choices, current)
    if err != nil {
        return false, err
    }
    product := c.app.Resources.Settings.Cloud.Accounts[name].DefaultProduct
    if name == current && d.in.Select.Product != nil {
        product = *d.in.Select.Product
    }
    product, err = c.input.Select(ctx, c.output.locale.Text("cloud_hub_product"), []interaction.Choice{
        {Label: c.output.locale.Text("cloud_ui_product_standard"), Value: "standard"},
        {Label: c.output.locale.Text("cloud_ui_product_pro"), Value: "pro"},
    }, product)
    if err != nil {
        return false, err
    }
    next := client.CloudSelect{Account: &name, Product: &product}
    changed := !reflect.DeepEqual(next, d.in.Select)
    d.in.Select = next
    return changed, nil
}
