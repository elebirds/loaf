package cli

import (
    "fmt"
    "io"
    "strings"

    "github.com/elebirds/loaf/internal/i18n"
    execution "github.com/elebirds/loaf/internal/run"
)

func printPreview(out io.Writer, r execution.RunPreview, l *i18n.Localizer) {
    fmt.Fprintln(out, l.Text("run_preview_heading"))
    fmt.Fprintln(out, l.Text("run_preview_target", map[string]any{"Target": r.Target, "Value": provenanceText(r.Provenance["target"], l)}))
    fmt.Fprintln(out, l.Text("run_preview_command", map[string]any{"Value": fmt.Sprintf("%q", r.Args)}))
    fmt.Fprintln(out, l.Text("run_preview_setup", map[string]any{"Value": r.SetupPresent}))
    fmt.Fprintln(out, l.Text("run_preview_location", map[string]any{"Root": r.RemoteBase, "Remote": r.RemoteDirectory}))
    fmt.Fprintln(out, l.Text("run_preview_pull", map[string]any{"Root": r.Pull.BaseDir, "Value": r.Pull.Checksum}))
    fmt.Fprintln(out, l.Text("run_preview_environment", map[string]any{"Value": r.EnvironmentKeys}))
    fmt.Fprintln(out, l.Text("run_preview_policies", map[string]any{"Millis": r.WaitMillis, "Count": r.Tail, "Mode": r.Source.Policy.Mode, "Paths": r.Outputs}))
    fmt.Fprintln(out, l.Text("run_preview_source", map[string]any{"Count": len(r.Source.Files), "Bytes": r.Source.Bytes, "Root": r.Root}))
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
