package cli

import (
    "context"
    "fmt"
    "io"
    "slices"
    "strconv"
    "strings"

    "github.com/charmbracelet/x/ansi"
    "github.com/elebirds/loaf/internal/cli/interaction"
    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
)

// uxFResults consumes the local task selector; eligibility is still checked by
// OutputsList/Pull against each selected run's saved binding.
func (c *invocation) uxFResults(ctx context.Context) (any, error) {
    if !c.uxInteractive() {
        return nil, interaction.ErrNonInteractive
    }
    ids, err := c.uxDPickRuns(ctx, true)
    if err != nil || len(ids) == 0 {
        return nil, err
    }
    if len(ids) == 1 {
        return c.uxFOutputs(ctx, ids[0])
    }
    return nil, c.uxFDownload(ctx, client.PullInput{IDs: slices.Clone(ids), Policy: c.policy})
}

func (c *invocation) uxFDownload(ctx context.Context, in client.PullInput) error {
    l := c.output.locale
    defaults, err := c.app.Projects.PullPreferences(ctx)
    if err != nil {
        return err
    }
    multi := len(in.IDs) > 1 || in.Into != nil
    configured := in.To == nil && in.Into == nil
    if multi && in.Into == nil {
        parent := defaults.BaseDirectory
        in.Into = &parent
    }
    for {
        preview, err := c.app.Projects.PreviewPull(ctx, in)
        if err != nil {
            return err
        }
        source, mode := preview.DestinationSource, "ux_f_single_mode"
        if configured {
            source = defaults.BaseSource
        }
        if multi {
            mode = "ux_f_batch_mode"
        }
        summary := l.Text("ux_f_review_summary", map[string]any{"Count": len(preview.Runs), "Mode": l.Text(mode), "Source": l.Text("ux_f_source_" + source)})
        summary += fmt.Sprintf("\n%s · checksum=%t", c.uxFSelectionLabel(in.Paths), preview.Preferences.Policy.Checksum)
        if len(preview.Runs) == 1 {
            // The complete path is always available in the review document.
            summary += "\n" + uxFCompactPath(preview.Runs[0].Destination, c)
        } else {
            summary += "\n" + uxFCompactPath(*preview.Input.Into, c)
        }
        choices := []interaction.Choice{{Label: l.Text("ux_f_details"), Value: "details"}, {Label: l.Text("ux_f_download"), Value: "download"}, {Label: l.Text("ux_f_destination"), Value: "destination"}, {Label: l.Text("ux_f_checksum"), Value: "checksum"}}
        if multi {
            choices = append(choices, interaction.Choice{Label: l.Text("ux_f_browse_run"), Value: "browse"}, interaction.Choice{Label: l.Text("ux_f_common_roots"), Value: "paths"}, interaction.Choice{Label: l.Text("ux_f_all_outputs"), Value: "all"})
        }
        choices = append(choices, interaction.Choice{Label: l.Text("ux_f_back"), Value: "back"})
        action, err := c.input.SelectWithSummary(ctx, l.Text("ux_f_review"), summary, choices, "details")
        if err != nil {
            return err
        }
        switch action {
        case "details":
            if err := c.uxShowDocument(ctx, c.uxFReviewDocument(preview)); err != nil {
                return err
            }
        case "download":
            return c.uxFPerformPull(ctx, preview.Input)
        case "destination":
            kind := "ux_f_exact"
            if multi {
                kind = "ux_f_parent"
            }
            action, err := c.input.Select(ctx, l.Text("ux_f_destination"), []interaction.Choice{{Label: l.Text(kind), Value: "edit"}, {Label: l.Text("ux_f_preferences"), Value: "default"}, {Label: l.Text("ux_f_back"), Value: "back"}}, "back")
            if err != nil {
                return err
            }
            if action == "default" {
                in.To, in.Into, configured = nil, nil, true
                if multi {
                    parent := defaults.BaseDirectory
                    in.Into = &parent
                }
            } else if action == "edit" {
                current := preview.Runs[0].Destination
                if multi {
                    current = *preview.Input.Into
                }
                field := &interaction.Field{Title: l.Text(kind), Value: current, Validate: func(value string) error {
                    candidate := in
                    if multi {
                        candidate.Into = &value
                    } else {
                        candidate.To = &value
                    }
                    _, err := c.app.Projects.PreviewPull(ctx, candidate)
                    if err != nil {
                        return fmt.Errorf("%s", c.output.error(err).Message)
                    }
                    return nil
                }}
                if err := c.input.Page(ctx, l.Text("ux_f_destination"), field); err != nil {
                    return err
                }
                destination := field.Value
                if multi {
                    in.Into = &destination
                } else {
                    in.To = &destination
                }
                configured = false
            }
        case "checksum":
            initial := "inherit"
            if in.Checksum != nil {
                initial = strconv.FormatBool(*in.Checksum)
            }
            value, err := c.input.Select(ctx, l.Text("ux_f_checksum"), []interaction.Choice{{Label: l.Text("ux_f_inherit"), Value: "inherit"}, {Label: l.Text("ux_f_quick"), Value: "false"}, {Label: l.Text("ux_f_hash"), Value: "true"}}, initial)
            if err != nil {
                return err
            }
            in.Checksum = nil
            if value != "inherit" {
                enabled := value == "true"
                in.Checksum = &enabled
            }
        case "browse":
            id, err := c.uxFPickSelectedRun(ctx, in.IDs)
            if err != nil {
                return err
            }
            if id != "" {
                if err := c.uxFBrowse(ctx, id); err != nil {
                    return err
                }
            }
        case "paths":
            // Roots come from each run's immutable output selection, not the
            // current project configuration or arbitrary remote paths.
            all := in
            all.Paths = nil
            frozen, err := c.app.Projects.PreviewPull(ctx, all)
            if err != nil {
                return err
            }
            choices := uxFCommonRoots(frozen)
            if len(choices) == 0 {
                if err := c.uxShowDocument(ctx, interaction.Document{Body: l.Text("ux_f_no_common")}); err != nil {
                    return err
                }
                continue
            }
            paths, err := c.uxFSelectPaths(ctx, choices, in.Paths, l.Text("ux_f_common_help"))
            if err != nil {
                return err
            }
            if len(paths) == 0 {
                if err := c.uxShowDocument(ctx, interaction.Document{Body: l.Text("ux_f_selection_empty")}); err != nil {
                    return err
                }
                continue
            }
            in.Paths = paths
        case "all":
            in.Paths = nil
        default:
            return nil
        }
    }
}

func uxFCompactPath(path string, c *invocation) string {
    // Quoting prevents terminal controls in a local filename from becoming UI.
    quoted := strconv.Quote(path)
    width := max(20, interaction.Width(c.options.Err)-12)
    return ansi.Truncate(quoted, width, "…")
}

func uxFCommonRoots(preview client.PullPreview) []interaction.Choice {
    choices := []interaction.Choice{}
    if len(preview.Runs) == 0 {
        return choices
    }
    for _, root := range preview.Runs[0].Paths {
        shared := true
        for _, run := range preview.Runs[1:] {
            shared = shared && slices.Contains(run.Paths, root)
        }
        if shared {
            choices = append(choices, interaction.Choice{Label: strconv.Quote(root), Value: root})
        }
    }
    return choices
}

func (c *invocation) uxFReviewDocument(preview client.PullPreview) interaction.Document {
    l := c.output.locale
    mode := "ux_f_quick"
    if preview.Preferences.Policy.Checksum {
        mode = "ux_f_hash"
    }
    lines := []string{}
    for _, run := range preview.Runs {
        if len(lines) > 0 {
            lines = append(lines, "")
        }
        lines = append(lines, run.ID+" · "+strconv.Quote(run.Target), strconv.Quote(run.Destination))
        for _, path := range run.Paths {
            lines = append(lines, "  "+strconv.Quote(path))
        }
    }
    lines = append(lines, "", l.Text("ux_f_policy", map[string]any{"Mode": l.Text(mode), "Source": l.Text("preview_source_" + preview.Preferences.ChecksumSource)}), l.Text("ux_f_live_copy"))
    return interaction.Document{Title: l.Text("ux_f_details"), Body: strings.Join(lines, "\n")}
}

func (c *invocation) uxFPerformPull(ctx context.Context, in client.PullInput) error {
    original := in
    var retained *client.PullBatch
    for {
        var result client.PullResult
        cause := c.input.WithNative(ctx, nil, func() error {
            var err error
            result, err = c.app.Projects.Pull(ctx, in, c.uxFProgress)
            return err
        })
        if result.Batch != nil {
            batch := result.Batch
            if cause != nil && (batch.Selected == 0 || batch.Completed+batch.Failed+batch.Unexamined != batch.Selected) {
                // A local revalidation failure retains the direct command's
                // partial plan, not completed transfer rows. Never count its
                // zero-value rows as successful downloads.
                if retained == nil {
                    return cause
                }
                result.Batch = retained
            } else {
                retained = uxFMergePulls(retained, batch)
                result.Batch = retained
                if retained.Failed+retained.Unexamined == 0 {
                    cause = nil
                } else {
                    cause = fault.New("PULL_PARTIAL", "pull_batch_partial", nil)
                }
            }
        }
        if result.Single == nil && result.Batch == nil && retained != nil {
            result.Batch = retained
        }
        if ctx.Err() != nil {
            // Keep the actual completed/failed/unexamined results even when the
            // canceled input owner cannot open another page.
            if err := c.uxFWritePullResult(result, cause); err != nil {
                return err
            }
            return cause
        }
        if result.Single == nil && result.Batch == nil {
            return cause
        }
        next, err := c.uxFPullResult(ctx, original, result, cause)
        if err != nil || len(next.IDs) == 0 {
            return err
        }
        in = next
    }
}

// Retain successful rows from earlier attempts when only failed/unexamined
// rows are explicitly retried. Counts always cover the original frozen set.
func uxFMergePulls(previous, current *client.PullBatch) *client.PullBatch {
    if previous == nil {
        previous = &client.PullBatch{Selected: current.Selected, Runs: slices.Clone(current.Runs)}
    } else {
        for i, old := range previous.Runs {
            for _, row := range current.Runs {
                if old.Result.RunID == row.Result.RunID {
                    previous.Runs[i] = row
                    break
                }
            }
        }
    }
    previous.Completed, previous.Failed, previous.Unexamined = 0, 0, 0
    for _, row := range previous.Runs {
        switch {
        case row.Error == nil:
            previous.Completed++
        case uxFCode(row.Error) == "NOT_EXAMINED":
            previous.Unexamined++
        default:
            previous.Failed++
        }
    }
    return previous
}

func (c *invocation) uxFWritePullResult(result client.PullResult, cause error) error {
    var body strings.Builder
    if result.Single != nil {
        if err := printResult(&body, *result.Single, c.output.locale, cause == nil); err != nil {
            return err
        }
    }
    if result.Batch != nil {
        if err := printResult(&body, c.output.pulls(*result.Batch), c.output.locale, cause == nil); err != nil {
            return err
        }
    }
    _, err := io.WriteString(c.output.err, body.String())
    return err
}

func (c *invocation) uxFPullResult(ctx context.Context, in client.PullInput, result client.PullResult, cause error) (client.PullInput, error) {
    l := c.output.locale
    completed, failed, unexamined := 0, 0, 0
    retry := in
    retry.IDs = nil
    var data any
    if result.Single != nil {
        data = *result.Single
        if cause == nil {
            completed = 1
        } else {
            failed, retry.IDs = 1, slices.Clone(in.IDs)
        }
    } else {
        data = c.output.pulls(*result.Batch)
        completed, failed, unexamined = result.Batch.Completed, result.Batch.Failed, result.Batch.Unexamined
        for _, row := range result.Batch.Runs {
            if row.Error != nil {
                retry.IDs = append(retry.IDs, row.Result.RunID)
            }
        }
    }
    summary := l.Text("ux_f_result_summary", map[string]any{"Completed": completed, "Failed": failed, "Unexamined": unexamined})
    if cause != nil {
        failure := c.output.error(cause)
        summary += "\n" + failure.Code + ": " + failure.Message
    }
    finalSummary := summary
    location := ""
    if result.Single != nil {
        location = result.Single.Destination
    } else if in.Into != nil {
        location = *in.Into
    }
    if location != "" {
        summary += "\n" + uxFCompactPath(location, c)
        finalSummary += "\n" + strconv.Quote(location)
    }
    for {
        choices := []interaction.Choice{{Label: l.Text("ux_f_result_details"), Value: "details"}}
        if len(retry.IDs) > 0 {
            choices = append(choices, interaction.Choice{Label: l.Text("ux_f_retry"), Value: "retry"})
        }
        if completed > 0 && failed == 0 && unexamined == 0 && c.uxRouteAvailable("clean") {
            choices = append(choices, interaction.Choice{Label: l.Text("ux_f_clean"), Value: "clean"})
        }
        choices = append(choices, interaction.Choice{Label: l.Text("ux_f_back"), Value: "back"})
        action, err := c.input.SelectWithSummary(ctx, l.Text("ux_f_finished"), summary, choices, "details")
        if err != nil {
            return client.PullInput{}, err
        }
        switch action {
        case "details":
            if err := c.uxShowResult(ctx, data, cause); err != nil {
                return client.PullInput{}, err
            }
        case "retry":
            return retry, nil
        case "clean":
            data, err := c.uxNavigate(ctx, uxRoute{Kind: "clean", IDs: slices.Clone(in.IDs)})
            if data != nil || err != nil {
                if showErr := c.uxShowResult(ctx, data, err); showErr != nil {
                    return client.PullInput{}, showErr
                }
            }
        default:
            _, err := fmt.Fprintln(c.output.err, finalSummary)
            return client.PullInput{}, err
        }
    }
}
