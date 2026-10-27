package cli

import (
    "fmt"
    "io"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
)

func printPreview(out io.Writer, r execution.RunPreview, l *i18n.Localizer) {
    fmt.Fprintln(out, l.Text("run_preview_heading"))
    fmt.Fprintln(out, l.Text("run_preview_target", map[string]any{"Target": r.Target, "Value": provenanceText(r.Provenance["target"], l)}))
    fmt.Fprintln(out, l.Text("run_preview_command", map[string]any{"Value": fmt.Sprintf("%q", r.Args)}))
    fmt.Fprintln(out, l.Text("run_preview_source", map[string]any{"Count": len(r.Source.Files), "Bytes": r.Source.Bytes, "Root": r.Root}))
    fmt.Fprintln(out, l.Text("preview_outputs", map[string]any{"Paths": strings.Join(r.Outputs, ", ")}))
    fmt.Fprintln(out, l.Text("run_preview_setup", map[string]any{"Value": booleanText(r.SetupPresent, l)}))
    remote := r.RemoteDirectory
    if remote == "" {
        remote = l.Text("preview_new_location")
    }
    fmt.Fprintln(out, l.Text("run_preview_location", map[string]any{"Root": r.RemoteBase, "Remote": remote}))
    fmt.Fprintln(out, l.Text("run_preview_pull", map[string]any{"Root": r.Pull.BaseDir, "Value": booleanText(r.Pull.Checksum, l)}))
    fmt.Fprintln(out, l.Text("run_preview_environment", map[string]any{"Value": r.EnvironmentKeys}))
    fmt.Fprintln(out, l.Text("preview_wait", map[string]any{"Value": (time.Duration(r.WaitMillis) * time.Millisecond).String()}))
    fmt.Fprintln(out, l.Text("preview_policies", map[string]any{"Count": r.Tail, "Mode": l.Text("sync_mode_" + r.Source.Policy.Mode)}))
    fmt.Fprintln(out, l.Text("preview_provenance"))
    for _, field := range []string{"command", "wait", "logs.tail", "sync.mode", "sync.respect_gitignore", "sync.exclude", "outputs", "pull.base_dir", "pull.checksum"} {
        fmt.Fprintf(out, "  %s: %s\n", field, provenanceText(r.Provenance[field], l))
    }
    if r.Source.Policy.Mode == "update" {
        fmt.Fprintln(out, l.Text("sync_update_warning"))
    }
    fmt.Fprintln(out, l.Text("run_preview_not_snapshot"))
}

func provenanceText(sources []string, l *i18n.Localizer) string {
    labels := make([]string, len(sources))
    for i, source := range sources {
        labels[i] = l.Text("preview_source_" + source)
    }
    return strings.Join(labels, " + ")
}

func booleanText(value bool, l *i18n.Localizer) string {
    if value {
        return l.Text("yes")
    }
    return l.Text("no")
}
