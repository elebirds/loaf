package config

import (
    "errors"
    "strings"

    "github.com/elebirds/loaf/internal/fault"
    "github.com/pelletier/go-toml/v2"
)

// configFailure keeps parser locations, never source excerpts or field values.
func configFailure(file, message string, cause error) error {
    f := fault.Wrap("BAD_CONFIG", message, cause)
    loc := &fault.Location{File: file}
    var decode *toml.DecodeError
    if errors.As(cause, &decode) {
        loc.Line, loc.Column = decode.Position()
        loc.Field = strings.Join(decode.Key(), ".")
    }
    f.Input = &fault.Input{Reason: "invalid_config", Location: loc}
    return f
}

func locateConfig(file string, err error) error {
    if err == nil {
        return nil
    }
    var f *fault.Error
    if !errors.As(err, &f) {
        return configFailure(file, "config_read", err)
    }
    if f.Input != nil && f.Input.Location != nil {
        return err
    }
    copy := *f
    loc := &fault.Location{File: file}
    switch f.Message {
    case "invalid_wait":
        loc.Field = "run.wait"
    case "invalid_tail":
        loc.Field = "logs.tail"
    case "invalid_stop_grace":
        loc.Field = "stop.grace"
    case "ps_invalid_limit":
        loc.Field = "ps.limit"
    case "invalid_remote":
        loc.Field = "remote_dir"
    case "sync_mode":
        loc.Field = "sync.mode"
    case "sync_exclude":
        loc.Field = "sync.exclude"
    }
    copy.Input = &fault.Input{Reason: "invalid_config", Location: loc}
    return &copy
}
