package main

import (
    "context"
    "strconv"

    "github.com/elebirds/loaf/internal/cloud"
    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/ssh"
)

func cloudConnection(open cloud.OpenProvider) ssh.ResolveConnection {
    return func(ctx context.Context, target config.Target) (config.Target, string, error) {
        b := target.Cloud
        p, err := open(ctx, b.Ref.Account, b.Settings, b.Ref.Product)
        if err != nil {
            return config.Target{}, "", cloud.Failure(err)
        }
        connection, err := p.Connection(ctx, b.Ref)
        if err != nil {
            return config.Target{}, "", cloud.Failure(err)
        }
        target.Cloud = nil
        target.Destination = connection.User + "@" + connection.Host
        target.Args = append([]string{"-p", strconv.Itoa(connection.Port)}, target.Args...)
        return target, connection.Password, nil
    }
}
