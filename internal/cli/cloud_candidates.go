package cli

import (
    "context"
    "fmt"
    "sort"
    "strings"

    "github.com/charmbracelet/x/ansi"
    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/cloud"
)

func (d *cloudDraft) pickCandidate(ctx context.Context, c *invocation) (cloudTransition, error) {
    l := c.output.locale
    order := "price"
    var page cloud.SearchResult
    reload := true
    for {
        if reload {
            err := c.cloudBusy(ctx, l.Text("cloud_loading_stock"), d.scope(c), func(work context.Context) error { var e error; page, e = d.search(work, c); return e })
            if err != nil {
                return cloudStay, err
            }
            reload = false
        }
        if len(page.Matches) == 0 {
            choices := []interaction.Choice{{Label: l.Text("cloud_mode_auto"), Value: "auto"}, {Label: l.Text("cloud_nav_back"), Value: "back"}, {Label: l.Text("cloud_nav_cancel"), Value: "cancel"}}
            if page.HasMore {
                choices = append(choices, interaction.Choice{Label: l.Text("cloud_candidates_more"), Value: "more"})
            }
            choice, err := d.selectChoice(ctx, c, "cloud_no_match", choices, "back")
            if err != nil {
                return cloudStay, err
            }
            switch choice {
            case "cancel":
                return cloudCancel, nil
            case "auto":
                d.mode = "auto"
                return d.candidate(ctx, c)
            case "more":
                d.offset = page.NextOffset
                reload = true
                continue
            default:
                return cloudBack, nil
            }
        }
        candidates := append([]cloud.Offer{}, page.Matches...)
        sortCandidates(candidates, order)
        title := "cloud_wizard_host"
        if d.plan.Product == "pro" {
            title = "cloud_wizard_spec"
        }
        choice := &interaction.Field{Title: l.Text(title)}
        if d.selected != nil {
            choice.Value = d.selected.ID
        }
        for _, offer := range candidates {
            choice.Choices = append(choice.Choices, interaction.Choice{Label: candidateRow(offer, d.regions, l.Text("cloud_price_unknown")), Value: offer.ID})
        }
        choice.Detail = func(id string) string {
            for _, offer := range candidates {
                if offer.ID == id {
                    return l.Text("cloud_candidates_columns") + "\n" + offer.ID + " · " + offer.Region + "\n" + offerDetail(offer, l)
                }
            }
            return ""
        }
        nav := d.navField(c)
        nav.Choices = append(nav.Choices, interaction.Choice{Label: l.Text("cloud_candidates_refresh"), Value: "refresh"}, interaction.Choice{Label: l.Text("cloud_candidates_sort"), Value: "sort"})
        if page.HasMore {
            nav.Choices = append(nav.Choices, interaction.Choice{Label: l.Text("cloud_candidates_more"), Value: "more"})
        }
        if err := c.input.PageWithSummary(ctx, l.Text("cloud_step_candidate"), d.scope(c)+"\n"+cloudCoverage(l, page.Examined, page.Complete), choice, nav); err != nil {
            return cloudStay, err
        }
        switch nav.Value {
        case "cancel":
            return cloudCancel, nil
        case "back":
            return cloudBack, nil
        case "refresh":
            reload = true
            continue
        case "more":
            d.offset = page.NextOffset
            reload = true
            continue
        case "sort":
            options := []interaction.Choice{}
            for _, value := range []string{"price", "cpu", "memory"} {
                options = append(options, interaction.Choice{Label: l.Text("cloud_candidates_sort_" + value), Value: value})
            }
            var err error
            order, err = d.selectChoice(ctx, c, "cloud_candidates_sort", options, order)
            if err != nil {
                return cloudStay, err
            }
            continue
        }
        for _, offer := range candidates {
            if offer.ID == choice.Value {
                d.chooseCandidate(offer)
                break
            }
        }
        d.in.Plan.Explicit.Continuous = cloudPointer(false)
        d.in.Plan.Explicit.WaitTimeout = nil
        return cloudNext, nil
    }
}

func sortCandidates(offers []cloud.Offer, order string) {
    sort.SliceStable(offers, func(i, j int) bool {
        a, b := offers[i], offers[j]
        if order == "cpu" || order == "memory" {
            av, bv := a.CPUs, b.CPUs
            if order == "memory" {
                av, bv = a.MemoryGiB, b.MemoryGiB
            }
            if av != nil && bv == nil {
                return true
            }
            if av == nil && bv != nil {
                return false
            }
            if av != nil && bv != nil && *av != *bv {
                return *av > *bv
            }
        } else {
            av, bv := a.ComputeHourlyPrice, b.ComputeHourlyPrice
            if av != nil && bv == nil {
                return true
            }
            if av == nil && bv != nil {
                return false
            }
            if av != nil && bv != nil {
                if cloud.Less(*av, *bv) {
                    return true
                }
                if cloud.Less(*bv, *av) {
                    return false
                }
            }
        }
        return a.ID < b.ID
    })
}

func candidateCell(value string, width int) string {
    value = ansi.Truncate(value, width, "…")
    return value + strings.Repeat(" ", max(0, width-ansi.StringWidth(value)))
}

func candidateRow(o cloud.Offer, regions []cloud.CatalogItem, unknown string) string {
    price := unknown
    if o.ComputeHourlyPrice != nil {
        price = "¥" + o.ComputeHourlyPrice.Amount
    }
    region := o.Region
    for _, r := range regions {
        if r.ID == o.Region {
            region = r.Name
            break
        }
    }
    integer := func(n *int) string {
        if n == nil {
            return "?"
        }
        return fmt.Sprint(*n)
    }
    return candidateCell(price, 8) + " " + candidateCell(fmt.Sprintf("%s ×%d", candidateCell(o.Model, 14), o.GPUCount), 18) + " " + candidateCell(integer(o.GPUMemoryGiB), 4) + " " + candidateCell(integer(o.CPUs), 4) + " " + candidateCell(integer(o.MemoryGiB), 5) + " " + candidateCell(region, 10) + " " + candidateCell(o.ID, 8)
}
