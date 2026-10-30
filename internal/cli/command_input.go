package cli

// interactiveInput permits command-local forms only after explicit -i opt-in.
// Native SSH authentication and streams keep their independent TTY contracts.
func (c *invocation) interactiveInput() bool {
    return c.ui && !c.policy.Batch && !c.output.json
}
