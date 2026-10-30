package cli

import "github.com/alecthomas/kong"

// uxIHubCommand recognizes only a bare interactive entry. Default-command
// flags, explicit subcommands and output/control flags retain direct semantics.
// 00 calls this after help handling, before dispatch; JSON is never a hub.
func uxIHubCommand(parser *kong.Kong, args []string, early bootstrapOptions) string {
    if !early.interactive || early.json || early.help || early.version || (early.command != "notify" && early.command != "events") {
        return ""
    }
    traced, err := kong.Trace(parser, args)
    if err != nil || traced.Error != nil {
        return ""
    }
    for _, item := range traced.Path {
        if item.Flag != nil && !item.Resolved {
            switch item.Flag.Name {
            case "lang", "json", "interactive":
            default:
                return ""
            }
        }
    }
    return early.command
}
