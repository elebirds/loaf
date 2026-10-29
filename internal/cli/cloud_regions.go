package cli

import (
    "strings"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/i18n"
)

type regionGroup struct {
    label, dataCenter string
    ids               []string
}

// regionGroups follows the console's common-directory grouping by data_center,
// retaining the first display name. Without that metadata, exact IDs stay separate.
func regionGroups(items []cloud.CatalogItem) []regionGroup {
    groups := []regionGroup{}
    type groupKey struct{ dataCenter, id string }
    positions := map[groupKey]int{}
    seen := map[string]bool{}
    for _, item := range items {
        if seen[item.ID] {
            continue
        }
        seen[item.ID] = true
        label := item.Name
        if label == "" {
            label = item.ID
        }
        key := groupKey{dataCenter: item.DataCenter}
        if item.DataCenter == "" {
            key.id = item.ID
        }
        index, exists := positions[key]
        if !exists {
            index = len(groups)
            positions[key] = index
            groups = append(groups, regionGroup{label: label, dataCenter: item.DataCenter})
        }
        groups[index].ids = append(groups[index].ids, item.ID)
    }
    names := map[string]int{}
    for _, group := range groups {
        names[group.label]++
    }
    for i := range groups {
        if names[groups[i].label] > 1 {
            suffix := groups[i].dataCenter
            if suffix == "" {
                suffix = groups[i].ids[0]
            }
            groups[i].label += " · " + suffix
        }
    }
    return groups
}

type regionMenu struct {
    choices   []interaction.Choice
    selected  []string
    members   map[string][]string
    defaulted bool
}

// newRegionMenu preselects known public regions only for a new, unset form.
// Explicit empty lists stay unrestricted; saved exact subsets stay exact.
func newRegionMenu(items []cloud.CatalogItem, current []string, l *i18n.Localizer) regionMenu {
    menu := regionMenu{members: map[string][]string{}}
    if current == nil {
        for _, item := range items {
            if item.Public != nil && *item.Public {
                current = append(current, item.ID)
            }
        }
        menu.defaulted = len(current) > 0
    }
    selected, known := map[string]bool{}, map[string]bool{}
    for _, id := range current {
        selected[id] = true
    }
    for _, group := range regionGroups(items) {
        chosen := []string{}
        for _, id := range group.ids {
            known[id] = true
            if selected[id] {
                chosen = append(chosen, id)
            }
        }
        key, label := "region:"+group.ids[0], group.label
        partial := len(chosen) > 0 && len(chosen) < len(group.ids)
        if partial {
            label = l.Text("cloud_region_all", map[string]any{"Value": label})
        }
        menu.choices = append(menu.choices, interaction.Choice{Label: label, Value: key})
        menu.members[key] = group.ids
        if len(chosen) == len(group.ids) {
            menu.selected = append(menu.selected, key)
        } else if partial {
            key = "exact:" + group.ids[0]
            label = l.Text("cloud_region_exact", map[string]any{"Value": group.label, "Region": strings.Join(chosen, ", ")})
            menu.choices = append(menu.choices, interaction.Choice{Label: label, Value: key})
            menu.members[key] = chosen
            menu.selected = append(menu.selected, key)
        }
    }
    for _, id := range current {
        if known[id] {
            continue
        }
        // A bounded catalog may omit a saved exact value. Keep it selectable
        // rather than silently dropping it or turning the scope into all regions.
        known[id] = true
        key := "exact:" + id
        menu.choices = append(menu.choices, interaction.Choice{Label: id, Value: key})
        menu.members[key] = []string{id}
        menu.selected = append(menu.selected, key)
    }
    return menu
}

func (m regionMenu) expand(keys []string) []string {
    ids := []string{}
    seen := map[string]bool{}
    for _, key := range keys {
        for _, id := range m.members[key] {
            if !seen[id] {
                seen[id] = true
                ids = append(ids, id)
            }
        }
    }
    return ids
}

func cloudRegionSummary(items []cloud.CatalogItem, current []string) string {
    remaining := map[string]bool{}
    for _, id := range current {
        remaining[id] = true
    }
    labels := []string{}
    for _, group := range regionGroups(items) {
        chosen := []string{}
        for _, id := range group.ids {
            if remaining[id] {
                chosen = append(chosen, id)
                delete(remaining, id)
            }
        }
        if len(chosen) == len(group.ids) {
            labels = append(labels, group.label)
        } else if len(chosen) > 0 {
            labels = append(labels, group.label+" · "+strings.Join(chosen, ", "))
        }
    }
    for _, id := range current {
        if remaining[id] {
            labels = append(labels, id)
            delete(remaining, id)
        }
    }
    return strings.Join(labels, " / ")
}
