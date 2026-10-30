package cli

import (
    "context"
    "encoding/json"
    "os"
    "reflect"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/i18n"
)

type publishedRegions struct {
    Items  []cloud.CatalogItem
    Groups []struct {
        DataCenter string `json:"data_center"`
        Name       string
        IDs        []string
    }
}

func publishedRegionFixture(t *testing.T) publishedRegions {
    t.Helper()
    data, err := os.ReadFile("testdata/autodl-region-groups.json")
    if err != nil {
        t.Fatal(err)
    }
    var fixture publishedRegions
    if err := json.Unmarshal(data, &fixture); err != nil {
        t.Fatal(err)
    }
    return fixture
}

func TestRegionGroupsMatchPublishedWebConfiguration(t *testing.T) {
    fixture := publishedRegionFixture(t)
    l, _ := i18n.New("zh-Hans", nil)
    menu := newRegionMenu(fixture.Items, nil, l)
    if len(fixture.Items) != 19 || len(menu.choices) != len(fixture.Groups) || len(menu.choices) != 10 {
        t.Fatal("raw region signs were exposed as area choices", len(fixture.Items), menu.choices)
    }
    actual := regionGroups(fixture.Items)
    for i, group := range fixture.Groups {
        choice := menu.choices[i]
        if choice.Label != group.Name || actual[i].dataCenter != group.DataCenter || !reflect.DeepEqual(menu.expand([]string{choice.Value}), group.IDs) {
            t.Fatal("console group/request mismatch", group, choice, menu.expand([]string{choice.Value}))
        }
    }
}

func regionFixture() []cloud.CatalogItem {
    // The public common directory groups these region_sign values under the
    // same name/data_center; their IDs remain valid independent CLI inputs.
    return []cloud.CatalogItem{
        {ID: "west-B", Name: "西北B区", DataCenter: "westDC3"},
        {ID: "bj-B1", Name: "北京B区", DataCenter: "beijingDC2"},
        {ID: "west-C", Name: "西北B区", DataCenter: "westDC3"},
        {ID: "west-D", Name: "西北B区", DataCenter: "westDC3"},
        {ID: "west-E", Name: "西北B区", DataCenter: "westDC3"},
        {ID: "bj-B2", Name: "北京B区", DataCenter: "beijingDC2"},
    }
}

func TestRegionMenuGroupsDisplayAreasAndExpandsAllCanonicalIDs(t *testing.T) {
    l, _ := i18n.New("zh-Hans", nil)
    menu := newRegionMenu(regionFixture(), nil, l)
    if len(menu.choices) != 2 || menu.choices[0].Label != "西北B区" || menu.choices[1].Label != "北京B区" || len(menu.selected) != 0 {
        t.Fatal(menu)
    }
    want := []string{"west-B", "west-C", "west-D", "west-E"}
    if got := menu.expand([]string{menu.choices[0].Value}); !reflect.DeepEqual(got, want) {
        t.Fatal(got)
    }
    if got := cloudRegionSummary(regionFixture(), want); got != "西北B区" {
        t.Fatal(got)
    }
}

func TestRegionMenuKeepsExactPrefillAndMissingCatalogValues(t *testing.T) {
    l, _ := i18n.New("zh-Hans", nil)
    for _, current := range [][]string{{}, {"west-C"}, {"west-C", "retained-exact"}, {"west-B", "west-C", "west-D", "west-E"}} {
        menu := newRegionMenu(regionFixture(), current, l)
        if got := menu.expand(menu.selected); !reflect.DeepEqual(got, current) {
            t.Fatal("continuing widened or dropped exact prefill", current, got)
        }
    }
    menu := newRegionMenu(regionFixture(), []string{"west-C"}, l)
    if !strings.Contains(menu.choices[0].Label, "全部") || !strings.Contains(menu.choices[1].Label, "west-C") {
        t.Fatal(menu.choices)
    }
    if got := cloudRegionSummary(regionFixture(), []string{"west-C"}); got != "西北B区 · west-C" {
        t.Fatal(got)
    }
    if got := menu.expand(append(menu.selected, menu.choices[0].Value)); len(got) != 4 {
        t.Fatal("explicit group choice must expand without duplicate IDs", got)
    }
}

func TestRegionMenuDefaultsAreEditableAndNeverOverrideExplicitScope(t *testing.T) {
    fixture := publishedRegionFixture(t)
    publicIDs := []string{}
    for _, group := range fixture.Groups {
        if group.DataCenter != "westDC2" {
            publicIDs = append(publicIDs, group.IDs...)
        }
    }
    l, _ := i18n.New("zh-Hans", nil)
    for _, tc := range []struct {
        name      string
        current   []string
        want      []string
        defaulted bool
    }{
        {name: "unset", want: publicIDs, defaulted: true},
        {name: "explicit-unrestricted", current: []string{}, want: []string{}},
        {name: "enterprise", current: []string{"west-X"}, want: []string{"west-X"}},
        {name: "exact-subset", current: []string{"west-C"}, want: []string{"west-C"}},
    } {
        t.Run(tc.name, func(t *testing.T) {
            menu := newRegionMenu(fixture.Items, tc.current, l)
            if got := menu.expand(menu.selected); !reflect.DeepEqual(got, tc.want) || menu.defaulted != tc.defaulted {
                t.Fatal(menu.defaulted, got, tc.want)
            }
            hasEnterprise := false
            for _, choice := range menu.choices {
                hasEnterprise = hasEnterprise || choice.Label == "西北企业区"
            }
            if !hasEnterprise {
                t.Fatal("enterprise choice was hidden", menu.choices)
            }
        })
    }
    menu := newRegionMenu(fixture.Items, nil, l)
    enterprise := ""
    for _, choice := range menu.choices {
        if choice.Label == "西北企业区" {
            enterprise = choice.Value
        }
    }
    if enterprise == "" || len(menu.expand(append(menu.selected, enterprise))) != len(fixture.Items) {
        t.Fatal("enterprise opt-in did not reach the exact scope", menu)
    }
    cleared := menu.expand(nil)
    reopened := newRegionMenu(fixture.Items, cleared, l)
    if cleared == nil || reopened.defaulted || len(reopened.selected) != 0 {
        t.Fatal("clearing and revisiting reapplied defaults", reopened)
    }
    unknown := newRegionMenu(regionFixture(), nil, l)
    if unknown.defaulted || len(unknown.selected) != 0 {
        t.Fatal("missing visibility metadata was treated as public", unknown)
    }
}

func TestRegionMenuDoesNotMergeUnknownOrDifferentDataCenters(t *testing.T) {
    l, _ := i18n.New("en", nil)
    items := []cloud.CatalogItem{{ID: "a", Name: "Same", DataCenter: "dc-a"}, {ID: "b", Name: "Same", DataCenter: "dc-b"}, {ID: "c", Name: "Same"}, {ID: "d", Name: "Same"}, {ID: "a", Name: "Same", DataCenter: "dc-a"}}
    menu := newRegionMenu(items, nil, l)
    if len(menu.choices) != 4 {
        t.Fatal(menu.choices)
    }
    labels := map[string]bool{}
    for _, choice := range menu.choices {
        if labels[choice.Label] || len(menu.expand([]string{choice.Value})) != 1 {
            t.Fatal("ambiguous or merged distinct scope", choice)
        }
        labels[choice.Label] = true
    }
}

func TestRegionMenuUsesWebDataCenterIdentityRatherThanDisplayName(t *testing.T) {
    l, _ := i18n.New("zh-Hans", nil)
    items := []cloud.CatalogItem{{ID: "west-B", Name: "西北B区", DataCenter: "westDC3"}, {ID: "west-C", Name: "西北 B 区", DataCenter: "westDC3"}}
    menu := newRegionMenu(items, nil, l)
    if len(menu.choices) != 1 || menu.choices[0].Label != "西北B区" || !reflect.DeepEqual(menu.expand([]string{menu.choices[0].Value}), []string{"west-B", "west-C"}) {
        t.Fatal(menu)
    }
}

type regionCloud struct {
    *uxCloud
    received []string
    regions  []cloud.CatalogItem
    catalogs int
}

func (p *regionCloud) Catalog(ctx context.Context, in cloud.CatalogInput) (cloud.CatalogPage, error) {
    p.catalogs++
    if in.Category == "region" {
        items := p.regions
        if items == nil {
            items = regionFixture()
        }
        return cloud.CatalogPage{Category: "region", Items: items, Complete: true, Examined: len(items)}, nil
    }
    return p.uxCloud.Catalog(ctx, in)
}

func (p *regionCloud) Search(_ context.Context, plan cloud.Plan, _ int) (cloud.InventoryPage, error) {
    p.received = append([]string{}, plan.Requirements.Regions...)
    return cloud.InventoryPage{Offers: []cloud.Offer{}, Complete: true}, nil
}

func TestRegionWizardSelectionReachesSearchWithoutGroupKeys(t *testing.T) {
    for _, exact := range []bool{false, true} {
        o, base := uxFixture(t)
        p := &regionCloud{uxCloud: base}
        o.Cloud.Open = func(context.Context, string, cloud.Account, string) (cloud.Provider, error) { return p, nil }
        selectRegion := "1"
        want := []string{"west-B", "west-C", "west-D", "west-E"}
        if exact {
            want = []string{"west-C"}
            if err := o.Config.SaveCloudProfile(context.Background(), "default", cloud.Profile{Regions: want}, false); err != nil {
                t.Fatal(err)
            }
            selectRegion = ""
        }
        o.Interactive = true
        o.In = strings.NewReader(strings.Join([]string{"1", "1", "", selectRegion, "", "", "3"}, "\n") + "\n")
        code, _, text := run(t, o, "-i", "--lang", "zh-Hans", "cloud", "acquire")
        if code == 0 || p.creates != 0 || !reflect.DeepEqual(p.received, want) {
            t.Fatal(exact, code, p.received, text)
        }
        if !exact && (strings.Count(text, "西北B区") != 1 || strings.Count(text, "北京B区") != 1) {
            t.Fatal("wizard leaked duplicate area names", text)
        }
    }
}

func TestRegionWizardDefaultsCanBeKeptOverriddenOrCleared(t *testing.T) {
    fixture := publishedRegionFixture(t)
    publicIDs := []string{}
    for _, group := range fixture.Groups {
        if group.DataCenter != "westDC2" {
            publicIDs = append(publicIDs, group.IDs...)
        }
    }
    for _, tc := range []struct {
        name, answer  string
        prefill, want []string
    }{
        {name: "keep-defaults", want: publicIDs},
        {name: "choose-enterprise", answer: "9", want: []string{"west-X"}},
        {name: "clear-defaults", answer: "0", want: []string{}},
        {name: "keep-explicit-unrestricted", prefill: []string{}, want: []string{}},
        {name: "keep-enterprise-prefill", prefill: []string{"west-X"}, want: []string{"west-X"}},
    } {
        t.Run(tc.name, func(t *testing.T) {
            o, base := uxFixture(t)
            p := &regionCloud{uxCloud: base, regions: fixture.Items}
            o.Cloud.Open = func(context.Context, string, cloud.Account, string) (cloud.Provider, error) { return p, nil }
            if tc.prefill != nil {
                if err := o.Config.SaveCloudProfile(context.Background(), "default", cloud.Profile{Regions: tc.prefill}, false); err != nil {
                    t.Fatal(err)
                }
            }
            o.Interactive = true
            o.In = strings.NewReader(strings.Join([]string{"1", "1", "", tc.answer, "", "", "3"}, "\n") + "\n")
            code, _, text := run(t, o, "-i", "--lang", "zh-Hans", "cloud", "acquire")
            if code == 0 || p.creates != 0 || !reflect.DeepEqual(p.received, tc.want) {
                t.Fatal(code, p.received, tc.want, text)
            }
            if tc.prefill == nil && (!strings.Contains(text, "* 1. 西北B区") || !strings.Contains(text, "  9. 西北企业区")) {
                t.Fatal("defaults were not visible and editable", text)
            }
        })
    }
}

func TestRegionAgentRequestsKeepUnrestrictedAndEnterpriseScopes(t *testing.T) {
    for _, command := range []string{"search", "acquire"} {
        for _, enterprise := range []bool{false, true} {
            o, base := uxFixture(t)
            p := &regionCloud{uxCloud: base}
            o.Cloud.Open = func(context.Context, string, cloud.Account, string) (cloud.Provider, error) { return p, nil }
            args := []string{"cloud", command}
            want := []string{}
            if command == "acquire" {
                args = append(args, "gpu", "--image", "base-1", "--max-compute-hourly-price", "3", "--yes")
            }
            if enterprise {
                args = append(args, "--region", "west-X")
                want = []string{"west-X"}
            }
            args = append(args, "--json")
            code, out, stderr := run(t, o, args...)
            wantCode := 0
            if command == "acquire" {
                wantCode = 1 // The read-only fixture has no stock.
            }
            if !reflect.DeepEqual(p.received, want) || p.catalogs != 0 || p.creates != 0 || stderr != "" || code != wantCode {
                t.Fatal(args, code, p.received, p.catalogs, out, stderr)
            }
        }
    }
}
