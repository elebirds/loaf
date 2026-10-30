package cli

import "github.com/alecthomas/kong"

// uxInteractive is explicit permission for Loaf forms, not native terminal or
// authentication capability. CallPolicy.Batch keeps its transport contract.
func (c *invocation) uxInteractive() bool {
    return c.ui && !c.policy.Batch && !c.output.json
}

// uxBareEntry distinguishes a navigation entry from an explicitly configured
// default subcommand through Kong's resolved path, never by comparing values.
func uxBareEntry(parser *kong.Kong, args []string, early bootstrapOptions, command string) bool {
    if !early.interactive || early.command != command || early.json || early.help || early.version {
        return false
    }
    traced, err := kong.Trace(parser, args)
    if err != nil || traced.Error != nil {
        return false
    }
    for _, item := range traced.Path {
        if item.Flag == nil || item.Resolved {
            continue
        }
        switch item.Flag.Name {
        case "lang", "json", "interactive":
        case "target":
            if command != "queue" {
                return false
            }
        default:
            return false
        }
    }
    return true
}
