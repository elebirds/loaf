package main

import (
    "context"
    "os"

    "github.com/elebirds/loaf/internal/cli/interaction"
)

func terminalPassword(ctx context.Context) ([]byte, error) {
    return interaction.ReadTerminalSecret(ctx, os.Stdin, 4096)
}
