package interaction

import (
    "encoding/json"
    "image/color"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"

    tea "charm.land/bubbletea/v2"
    "charm.land/huh/v2"
    "github.com/charmbracelet/x/ansi"
)

func exportDocument(t *testing.T, name, value string) {
    t.Helper()
    if dir := os.Getenv("LOAF_UI_EXPORT"); dir != "" {
        if err := os.MkdirAll(dir, 0700); err != nil {
            t.Fatal(err)
        }
        if err := os.WriteFile(filepath.Join(dir, name+".ansi"), []byte(value), 0600); err != nil {
            t.Fatal(err)
        }
    }
}

func TestDocumentFitsChineseAndLongValues(t *testing.T) {
    d := Document{Title: "等待可用算力", Context: "test · demo · AutoDL 普通实例", Rows: []Row{{"算力", "vGPU-32GB / RTX 5090 / vGPU-48GB / RTX 5090 D × 1"}, {"计算价上限", "整机 ≤ ¥3 / 小时"}, {"环境", "PyTorch / 2.8.0 / 3.12(ubuntu22.04) / CUDA 12.8"}}, Body: "第 7 轮 · 申请尝试 0 次\n已检查 100 个候选 · 部分库存", Footer: "Tab 详情 · Ctrl+C 停止观察"}
    for _, width := range []int{44, 80, 120} {
        for _, dark := range []bool{true, false} {
            view := d.Render(width, true, dark)
            for _, line := range strings.Split(view, "\n") {
                if ansi.StringWidth(line) > width || strings.HasSuffix(ansi.Strip(line), " ") {
                    t.Fatal(width, line)
                }
            }
            if strings.Contains(view, "false") || !strings.Contains(ansi.Strip(view), "RTX 5090") {
                t.Fatal(view)
            }
        }
    }
}

func TestProgressDocumentReplacesOverviewWithDetailsAndClears(t *testing.T) {
    frame := ProgressFrame{Document: Document{Title: "等待可用算力", Context: "test · demo · AutoDL 普通实例", Rows: []Row{{"算力", "RTX 4090 × 1"}, {"计算价上限", "整机 ≤ ¥3 / 小时"}, {"环境", "PyTorch / 2.8 / Python 3.12 / CUDA 12.8"}}, Body: "第 7 轮 · 申请尝试 0 次\n已检查 100 个候选 · 部分库存"}, StartedAt: time.Now().Add(-84 * time.Second), NextAt: time.Now().Add(8 * time.Second), ElapsedLabel: "已等待", NextLabel: "下次查询", Footer: "Tab 详情 · Ctrl+C 停止观察", DetailTitle: "操作详情", Detail: "请求  c_fixture_request\n无库存  92", DetailFooter: "Tab 返回概览 · Ctrl+C 停止观察"}
    m := progressModel{frame: frame, width: 80, dark: true}
    overview := m.View().Content
    exportDocument(t, "progress-zh-Hans", overview)
    if !strings.Contains(ansi.Strip(overview), "已等待") || strings.Contains(overview, "c_fixture_request") {
        t.Fatal(overview)
    }
    m.details = true
    details := m.View().Content
    if strings.Contains(details, "RTX 4090") || !strings.Contains(details, "c_fixture_request") {
        t.Fatal(details)
    }
    connected, _ := m.Update(ProgressFrame{Document: Document{Title: "正在验证 SSH 连接"}, Detail: "instance"})
    if connected.(progressModel).details || !strings.Contains(ansi.Strip(connected.View().Content), "正在验证 SSH 连接") {
        t.Fatal("phase transition remained hidden behind details")
    }
    next, _ := m.Update(progressFinished{})
    if next.View().Content != "" {
        t.Fatal("completed progress remained in the transcript")
    }
}

func TestCompactFormUsesOnePageHeader(t *testing.T) {
    s := New(Options{Compact: true})
    f := s.pageForm("选择算力", "test · demo · AutoDL 普通实例", &Field{Title: "GPU 型号", Value: "4090", Choices: []Choice{{"RTX 4090", "4090"}, {"RTX 4090 D", "4090d"}, {"RTX 5090", "5090"}}}, &Field{Title: "GPU 卡数", Value: "1"}).
        WithTheme(huh.ThemeFunc(formTheme)).WithShowHelp(false).WithWidth(76).WithHeight(16)
    f.GetFocusedField().Focus()
    f.Update(tea.BackgroundColorMsg{Color: color.RGBA{R: 44, G: 44, B: 44, A: 255}})
    f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
    view := (formModel{form: f, compact: true, dark: true, footer: "Enter 继续 · Shift+Tab 返回 · Ctrl+C 取消", selectFooter: "↑↓ 选择 · / 搜索 · Enter 继续 · Ctrl+C 取消"}).View().Content
    if strings.Count(ansi.Strip(view), "选择算力") != 1 || !strings.Contains(ansi.Strip(view), "RTX 4090") || !strings.Contains(ansi.Strip(view), "RTX 5090") {
        t.Fatal(view)
    }
    exportDocument(t, "form-zh-Hans", view)
}

func TestSummaryConfirmationKeepsApprovalAboveInput(t *testing.T) {
    summary := Document{Title: "确认租用", Context: "gpu · demo · AutoDL 普通实例", Rows: []Row{{"算力", "RTX 4090 × 1"}, {"地区", "西北 B 区"}, {"计算价上限", "整机 ≤ ¥3 / 小时"}, {"环境", "PyTorch / 2.8.0 / Python 3.12 / CUDA 12.8"}, {"等待", "直到成功或取消"}}, Body: "仅授权一台。存储另计，不设总费用上限，需自行关机。"}
    for _, dark := range []bool{true, false} {
        value := ""
        f := summaryConfirmationForm(Confirmation{Title: "输入 yes 确认", Summary: &summary}, 80, &value, dark).
            WithTheme(huh.ThemeFunc(formTheme)).WithShowHelp(false).WithWidth(76)
        f.GetFocusedField().Focus()
        bg := color.RGBA{R: 44, G: 44, B: 44, A: 255}
        name := "approval-dark-zh-Hans"
        if !dark {
            bg = color.RGBA{R: 255, G: 255, B: 255, A: 255}
            name = "approval-light-zh-Hans"
        }
        f.Update(tea.BackgroundColorMsg{Color: bg})
        f.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
        view := (formModel{form: f, compact: true, dark: dark, footer: "Enter 继续 · Shift+Tab 返回 · Ctrl+C 取消"}).View().Content
        plain := ansi.Strip(view)
        if strings.Count(plain, "确认租用") != 1 || strings.Index(plain, "仅授权一台") > strings.Index(plain, "输入 yes 确认") {
            t.Fatal(view)
        }
        for _, line := range strings.Split(plain, "\n") {
            if ansi.StringWidth(line) > 80 {
                t.Fatal("approval overflows", line)
            }
        }
        exportDocument(t, name, view)
    }
}

func TestCanceledFormClearsTransientContent(t *testing.T) {
    m := formModel{form: huh.NewForm(huh.NewGroup(huh.NewInput().Title("Transient"))), compact: true}
    next, _ := m.Update(formCanceled{})
    if next.View().Content != "" {
        t.Fatal("canceled form remained in transcript")
    }
}

func TestShortChoicePageShowsEveryOption(t *testing.T) {
    for _, multiple := range []bool{false, true} {
        for _, columns := range []int{80, 120} {
            field := &Field{Multiple: multiple, Value: "auto", Values: []string{"auto"}, Choices: []Choice{{"选卡：从当前库存选择主机", "select"}, {"抢卡：按确认的条件自动获取", "auto"}}}
            s := New(Options{Compact: true})
            summary := "gpu · fixture · AutoDL 普通实例\n\n● 方式 → ○ 算力 → ○ 主机/条件 → ○ 环境/磁盘 → ○ 授权 → ○ 获取 → ○ 接入"
            f := s.pageForm("怎样获取机器？", summary, field).
                WithTheme(huh.ThemeFunc(formTheme)).WithShowHelp(false).WithWidth(min(columns, 96) - 4)
            f.GetFocusedField().Focus()
            f.Update(tea.BackgroundColorMsg{Color: color.RGBA{R: 44, G: 44, B: 44, A: 255}})
            f.Update(tea.WindowSizeMsg{Width: columns, Height: 24})
            view := (formModel{form: f, compact: true, dark: true, footer: "↑↓ 选择 · Enter 继续 · Ctrl+C 取消"}).View().Content
            plain := ansi.Strip(view)
            for _, option := range field.Choices {
                if !strings.Contains(plain, option.Label) {
                    t.Fatalf("multiple=%t width=%d: missing %q in\n%s", multiple, columns, option.Label, plain)
                }
            }
            if !multiple && columns == 80 {
                exportDocument(t, "mode-zh-Hans", view)
            }
        }
    }
}

func TestGroupedRegionPageRendersWebAreaChoicesOnce(t *testing.T) {
    // Consume the same public web configuration used by the CLI grouping test,
    // then render its expected choices through the production Huh constructor.
    data, err := os.ReadFile("../testdata/autodl-region-groups.json")
    if err != nil {
        t.Fatal(err)
    }
    var fixture struct {
        Items []struct {
            ID     string
            Public *bool
        }
        Groups []struct {
            Name string
            IDs  []string
        }
    }
    if err := json.Unmarshal(data, &fixture); err != nil {
        t.Fatal(err)
    }
    choices := []Choice{}
    selected := []string{}
    public := map[string]bool{}
    for _, item := range fixture.Items {
        public[item.ID] = item.Public != nil && *item.Public
    }
    for _, group := range fixture.Groups {
        key := "region:" + group.IDs[0]
        choices = append(choices, Choice{Label: group.Name, Value: key})
        allPublic := true
        for _, id := range group.IDs {
            allPublic = allPublic && public[id]
        }
        if allPublic {
            selected = append(selected, key)
        }
    }
    for _, columns := range []int{80, 120} {
        s := New(Options{Compact: true})
        summary := "gpu · fixture · AutoDL 普通实例\n\n✓ 方式 → ● 算力 → ○ 主机/条件 → ○ 环境/磁盘 → ○ 授权 → ○ 获取 → ○ 接入"
        regions := &Field{Title: "允许的地区", Description: "已预选公开地区；企业区可按需勾选。全部取消则不限地区。", Multiple: true, Choices: choices, Values: append([]string{}, selected...)}
        f := s.pageForm("选择算力", summary, regions).
            WithTheme(huh.ThemeFunc(formTheme)).WithShowHelp(false).WithWidth(min(columns, 96) - 4)
        f.GetFocusedField().Focus()
        f.Update(tea.BackgroundColorMsg{Color: color.RGBA{R: 44, G: 44, B: 44, A: 255}})
        f.Update(tea.WindowSizeMsg{Width: columns, Height: 24})
        view := (formModel{form: f, compact: true, dark: true, width: columns, footer: "↑↓ 移动 · 空格勾选 · / 搜索 · Enter 继续 · Ctrl+C 取消"}).View().Content
        plain := ansi.Strip(view)
        for _, choice := range choices[:7] {
            if strings.Count(plain, choice.Label) != 1 || !strings.Contains(plain, "✓ "+choice.Label) {
                t.Fatal(columns, "ambiguous or clipped region", choice.Label, plain)
            }
        }
        if columns == 80 {
            exportDocument(t, "regions-zh-Hans", view)
        }
        for range 8 {
            f.Update(tea.KeyPressMsg{Code: tea.KeyDown})
        }
        scrolled := (formModel{form: f, compact: true, dark: true, width: columns, footer: "↑↓ 移动 · 空格勾选 · / 搜索 · Enter 继续 · Ctrl+C 取消"}).View().Content
        if !strings.Contains(ansi.Strip(scrolled), "○ 西北企业区") {
            t.Fatal("enterprise region must remain visible and unchecked", columns, scrolled)
        }
        if columns == 80 {
            exportDocument(t, "regions-enterprise-zh-Hans", scrolled)
        }
        f.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
        if !strings.Contains(ansi.Strip(f.View()), "✓ 西北企业区") || len(regions.Values) != len(choices) {
            t.Fatal("Space could not include enterprise region", regions.Values, f.View())
        }
        for _, rendered := range []string{view, scrolled} {
            for _, line := range strings.Split(rendered, "\n") {
                if ansi.StringWidth(line) > columns {
                    t.Fatal("region defaults overflow the terminal", columns, line)
                }
            }
        }
    }
}

func TestShortReviewKeepsInputAndPagesDetails(t *testing.T) {
    s := New(Options{Compact: true})
    field := &Field{Title: "Confirm", Description: strings.Repeat("condition\n", 20), Value: ""}
    form := s.pageForm("Review", "account / product", field).WithTheme(huh.ThemeFunc(formTheme)).WithWidth(76).WithHeight(10)
    form.GetFocusedField().Focus()
    model := formModel{form: form, compact: true, height: 14, width: 80, headerTitle: "Review", headerSummary: "account / product", footer: "Enter"}
    view := ansi.Strip(model.View().Content)
    if !strings.Contains(view, ">") || !strings.Contains(view, "PgUp/PgDn") {
        t.Fatal(view)
    }
    next, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
    if next.(formModel).detailOffset != 3 {
        t.Fatal("details cannot page")
    }
    form.State = huh.StateCompleted
    if model.View().Content != "" {
        t.Fatal("completed review leaked into next screen")
    }
}

func TestProgressEditIsOnlyAnExplicitFrameAction(t *testing.T) {
    edits := 0
    m := progressModel{frame: ProgressFrame{Edit: func() { edits++ }}}
    model, _ := m.Update(tea.KeyPressMsg{Code: 'e'})
    if edits != 1 || !model.(progressModel).finished {
        t.Fatal(edits, model)
    }
    model, _ = (progressModel{}).Update(tea.KeyPressMsg{Code: 'e'})
    if edits != 1 || model.(progressModel).finished {
        t.Fatal("a creating/connecting frame allowed edit")
    }
}
