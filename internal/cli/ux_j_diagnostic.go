package cli

import (
    "strings"

    "github.com/alecthomas/kong"
)

// uxJInputIssues collects domain-established local semantic issues in order.
// Only declared argument names and stable reasons can enter the public list;
// values and raw configuration never do. It does not infer missing effective
// values from flags alone, or replace the original fault/code/exit status.
func uxJInputIssues(node *kong.Node, candidates ...inputIssue) ([]inputIssue, error) {
    issues := []inputIssue{}
    seen := map[inputIssue]bool{}
    for _, candidate := range candidates {
        switch candidate.Reason {
        case "missing_argument", "invalid_type", "invalid_value", "out_of_range", "conflicting_arguments":
        default:
            return nil, uxJCommandError()
        }
        fields := strings.Split(candidate.Argument, "/")
        for i, field := range fields {
            if node == nil || field == "" {
                return nil, uxJCommandError()
            }
            if flag := findFlag(node, field); flag != nil {
                fields[i] = "--" + flag.Name
                continue
            }
            found := false
            for _, arg := range node.Positional {
                found = found || field == arg.Name || field == arg.Tag.Get("value-type")
                if field == "--" && arg.Tag.Has("passthrough") {
                    found = true
                }
            }
            if !found {
                return nil, uxJCommandError()
            }
        }
        candidate.Argument = strings.Join(fields, "/")
        if !seen[candidate] {
            issues = append(issues, candidate)
            seen[candidate] = true
        }
    }
    return issues, nil
}
