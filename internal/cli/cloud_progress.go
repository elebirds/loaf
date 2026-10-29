package cli

import (
    "context"
    "encoding/json"
    "fmt"
    "sort"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/cloud"
)

type cloudFeedback struct {
    ctx             context.Context
    call            *invocation
    mode            string
    cancel          context.CancelFunc
    live            *interaction.LiveProgress
    started         time.Time
    lastStage       string
    observation     *cloud.SearchResult
    request         string
    instance        *cloud.Ref
    polls, attempts int
    lastLine        time.Time
    plan            *cloud.Plan
    account, target string
    phase           string
    phaseAt         time.Time
    probes          int
    failureKind     string
}

func (f *cloudFeedback) emit(e cloud.Event) error {
    c := f.call
    mode := f.mode
    if mode == "none" || mode == "auto" && c.output.json {
        return nil
    }
    if e.At.IsZero() {
        e.At = time.Now().UTC()
    }
    stage := e.Type
    if stage == "change" || stage == "heartbeat" {
        stage = "waiting"
    }
    if e.Plan == nil && e.Observation != nil {
        e.Plan = &e.Observation.Plan
    }
    if e.ProbeAttempts > 0 {
        f.probes = e.ProbeAttempts
    }
    f.failureKind = e.FailureKind
    if stage == "poll" {
        stage = "waiting"
        if e.Code != "" && e.Code != "NO_STOCK" {
            stage = "retrying"
        }
    }
    if mode == "json" {
        return json.NewEncoder(c.output.err).Encode(struct {
            SchemaVersion int    `json:"schema_version"`
            Type          string `json:"type"`
            Operation     string `json:"operation"`
            Stage         string `json:"stage"`
            cloud.Event
        }{1, "progress", strings.TrimPrefix(c.output.command, "cloud "), stage, e})
    }
    if f.started.IsZero() {
        f.started = time.Now()
    }
    if e.Plan != nil {
        f.plan = e.Plan
        if e.Account != "" {
            f.account = e.Account
        }
        if e.Target != "" {
            f.target = e.Target
        }
    }
    if e.Observation != nil {
        f.observation = e.Observation
    }
    if e.RequestID != "" {
        f.request = e.RequestID
    }
    if e.Instance != nil {
        f.instance = e.Instance
    }
    if e.PollCount > 0 {
        f.polls = e.PollCount
    }
    if e.Attempts > 0 {
        f.attempts = e.Attempts
    }
    if e.Target != "" {
        f.target = e.Target
    }
    if f.instance != nil {
        f.account = f.instance.Account
    }
    if f.request == "" && c.output.command == "cloud bind" {
        f.request = c.grammar.Cloud.Bind.Request
    }
    phase := stage
    switch stage {
    case "authorized", "waiting", "retrying":
        phase = "waiting"
    case "allocation_attempt":
        phase = "creating"
    case "created", "starting", "copying", "connecting":
        phase = "connecting"
    }
    if f.phase != phase {
        f.phase, f.phaseAt = phase, time.Now()
    }
    frame := f.frame(stage, e.NextCheckAt)
    if mode == "auto" {
        if f.live == nil {
            live, err := c.input.StartProgress(f.ctx, f.cancel, frame)
            if err != nil {
                return err
            }
            f.live = live
        }
        if f.live != nil {
            return f.live.Update(frame)
        }
    }
    if stage != f.lastStage || time.Since(f.lastLine) >= time.Minute {
        line := frame.Document.Title
        if scope := frame.Document.Context; scope != "" {
            line += " · " + scope
        }
        if f.phase == "waiting" && f.polls > 0 {
            line += " · " + c.output.locale.Text("cloud_ui_poll", map[string]any{"Count": f.polls, "Value": f.attempts})
        }
        _, err := fmt.Fprintln(c.output.err, line)
        f.lastStage, f.lastLine = stage, time.Now()
        return err
    }
    return nil
}

func (f *cloudFeedback) frame(stage string, next *time.Time) interaction.ProgressFrame {
    l := f.call.output.locale
    product := ""
    if f.plan != nil {
        product = f.plan.Product
    } else if f.instance != nil {
        product = f.instance.Product
    }
    d := interaction.Document{Title: l.Text("cloud_ui_stage_" + stage), Context: cloudScope(l, f.account, product, f.target)}
    if f.call.output.cloudWizard {
        current := 5
        if f.phase == "connecting" {
            current = 6
        }
        d.Context = withCloudRoadmap(d.Context, l, current)
    }
    frame := interaction.ProgressFrame{Document: d, StartedAt: f.phaseAt, ElapsedLabel: l.Text("cloud_ui_elapsed"), NextLabel: l.Text("cloud_next_check"), Footer: l.Text("cloud_ui_progress_keys"), DetailFooter: l.Text("cloud_ui_progress_back"), DetailTitle: l.Text("cloud_ui_details")}
    if next != nil {
        frame.NextAt = *next
    }
    if f.phase == "waiting" {
        if f.call.editAcquisition != nil && f.instance == nil {
            requested := f.call.editAcquisition
            frame.Edit = func() { requested.Store(true); f.cancel() }
            frame.Footer = l.Text("cloud_ui_edit_keys")
        }
        frame.StartedAt = f.started
        if f.plan != nil {
            for _, row := range cloudPlanRows(l, *f.plan, f.call.output.regionCatalog) {
                if row.Label != l.Text("cloud_ui_label_wait") {
                    frame.Document.Rows = append(frame.Document.Rows, row)
                }
            }
        }
        if f.polls > 0 {
            frame.Document.Body = l.Text("cloud_ui_poll", map[string]any{"Count": f.polls, "Value": f.attempts})
        }
        if stage == "retrying" {
            frame.Document.Body = l.Text("cloud_ui_read_retry")
        }
        if f.observation != nil {
            if frame.Document.Body != "" {
                frame.Document.Body += "\n"
            }
            frame.Document.Body += cloudCoverage(l, f.observation.Examined, f.observation.Complete)
            if f.observation.ScanOffset > 0 {
                frame.Document.Body += "\n" + l.Text("cloud_ui_scan_window", map[string]any{"Count": f.observation.ScanOffset + 1})
            }
            counts := map[string]int{}
            for _, reasons := range f.observation.Rejected {
                if len(reasons) > 0 {
                    counts[reasons[0]]++
                }
            }
            keys := []string{}
            for key := range counts {
                keys = append(keys, key)
            }
            sort.Strings(keys)
            primary := ""
            for _, key := range keys {
                if counts[key] > counts[primary] {
                    primary = key
                }
            }
            if primary != "" {
                frame.Document.Body += "\n" + l.Text("cloud_ui_blocker", map[string]any{"Value": l.Text("cloud_reject_" + primary), "Count": counts[primary]})
            }
        }
    } else if f.phase == "creating" {
        frame.Document.Body = l.Text("cloud_ui_creating_notice")
    } else if f.phase == "connecting" {
        frame.Document.Body = l.Text("cloud_ui_instance_saved")
        if f.plan != nil {
            frame.Document.Body = l.Text("cloud_ui_instance_created")
        }
        frame.Document.Body += "\n" + l.Text("cloud_ui_billing_active")
        if f.failureKind != "" && f.failureKind != "operation_failed" {
            frame.Document.Body += "\n" + l.Text("cloud_failure_"+f.failureKind)
        }
    }
    details := []string{}
    if f.probes > 0 {
        details = append(details, l.Text("cloud_probe_count", map[string]any{"Count": f.probes}))
    }
    if f.request != "" {
        details = append(details, l.Text("cloud_ui_label_request")+"  "+f.request)
    }
    if f.instance != nil {
        details = append(details, l.Text("cloud_ui_label_instance")+"  "+f.instance.ID)
    }
    if f.plan != nil {
        image := f.plan.AutoDL.Image
        if f.plan.AutoDL.PrivateImageUUID != "" {
            image = f.plan.AutoDL.PrivateImageUUID
        }
        if image != "" {
            details = append(details, l.Text("cloud_ui_label_image_id")+"  "+image)
        }
    }
    if f.phase == "waiting" && f.observation != nil {
        counts := map[string]int{}
        for _, reasons := range f.observation.Rejected {
            if len(reasons) > 0 {
                counts[reasons[0]]++
            }
        }
        keys := []string{}
        for k := range counts {
            keys = append(keys, k)
        }
        sort.Strings(keys)
        if len(keys) > 0 {
            details = append(details, "", l.Text("cloud_ui_rejections"))
        }
        for _, k := range keys {
            details = append(details, fmt.Sprintf("%s  %d", l.Text("cloud_reject_"+k), counts[k]))
        }
        if !f.observation.ObservedAt.IsZero() {
            details = append(details, "", l.Text("cloud_observed", map[string]any{"Value": f.observation.ObservedAt.Local().Format("01-02 15:04:05")}))
        }
    }
    frame.Detail = strings.Join(details, "\n")
    if frame.Detail == "" {
        frame.Footer = l.Text("cloud_ui_cancel")
    }
    return frame
}

func (f *cloudFeedback) close() error {
    if f.live != nil {
        return f.live.Close()
    }
    return nil
}
