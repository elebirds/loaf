package cli

import (
    "strings"

    "github.com/elebirds/loaf/internal/i18n"
)

// cloudRoadmap shows the complete acquisition route, including stages that do
// not collect input. A check marks completed choices, not a cloud-side mutation.
func cloudRoadmap(l *i18n.Localizer, current int) string {
    steps := []string{"mode", "compute", "candidate", "environment", "approval", "acquisition", "connection"}
    parts := make([]string, len(steps))
    for i, step := range steps {
        mark := "○ "
        if i < current {
            mark = "✓ "
        } else if i == current {
            mark = "● "
        }
        parts[i] = mark + l.Text("cloud_flow_"+step)
    }
    return strings.Join(parts, " → ")
}

func withCloudRoadmap(scope string, l *i18n.Localizer, current int) string {
    return scope + "\n\n" + cloudRoadmap(l, current)
}
