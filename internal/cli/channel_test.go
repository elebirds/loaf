package cli

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "reflect"
    "strings"
    "testing"

    "github.com/elebirds/loaf/internal/client"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/i18n"
)

type fakeChannelNotify struct {
    fakeNotify
    input      client.ChannelInput
    streams    client.Streams
    context    context.Context
    received   string
    response   string
    diagnostic string
    calls      int
    err        error
}

func (f *fakeChannelNotify) Channel(ctx context.Context, in client.ChannelInput, streams client.Streams) error {
    f.calls++
    f.input, f.streams, f.context = in, streams, ctx
    input, err := io.ReadAll(streams.In)
    if err != nil {
        return err
    }
    f.received = string(input)
    if _, err = io.WriteString(streams.Out, f.response); err != nil {
        return err
    }
    if _, err = io.WriteString(streams.Err, f.diagnostic); err != nil {
        return err
    }
    return f.err
}

func TestNotifyChannelUsesOnlyNativeStreams(t *testing.T) {
    const request = "{\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"initialize\"}\n"
    const response = "{\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{}}\n"
    o := fixture(t)
    input := io.NopCloser(strings.NewReader(request))
    out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
    o.In, o.Out, o.Err = input, out, stderr
    n := &fakeChannelNotify{response: response, diagnostic: "native diagnostic\n"}
    o.Notify = n
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    code := execute(ctx, []string{"notify", "channel", "--socket", "/private/research.sock", "--session", "research"}, o)
    if code != 0 || out.String() != response || stderr.String() != n.diagnostic || n.calls != 1 || n.received != request {
        t.Fatal(code, out.String(), stderr.String(), n)
    }
    if n.context != ctx || n.streams.In != input || n.streams.Out != out || n.streams.Err != stderr || n.input != (client.ChannelInput{Socket: "/private/research.sock", SessionID: "research"}) {
        t.Fatalf("native context/streams/input changed: %+v", n)
    }
    if n.serveCalls != 0 {
        t.Fatal("channel started a Bridge")
    }
}

func TestNotifyChannelRejectsOutputModesWithoutProtocolPollution(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        l, _ := i18n.New(lang, nil)
        for _, mode := range []string{"--json", "--trace"} {
            o := fixture(t)
            n := &fakeChannelNotify{}
            o.Notify = n
            args := []string{"--lang", lang, mode, "notify", "channel", "--socket", "/private/research.sock", "--session", "research"}
            code, out, stderr := run(t, o, args...)
            if code != 2 || out != "" || stderr != l.Text("notify_channel_native")+"\n" || n.calls != 0 {
                t.Fatal(args, code, out, stderr, n.calls)
            }
        }
    }
}

func TestNotifyChannelInputErrorsStayOnStderr(t *testing.T) {
    for _, args := range [][]string{
        {"notify", "channel", "--json"},
        {"notify", "channel", "--socket", "/private/research.sock"},
        {"notify", "channel", "--socket", "/private/research.sock", "--session", "research", "--target", "lab"},
        {"notify", "channel", "--socket", "/private/research.sock", "--session", "research", "--bad-flag", "--json"},
    } {
        o := fixture(t)
        n := &fakeChannelNotify{}
        o.Notify = n
        code, out, stderr := run(t, o, args...)
        if code != 2 || out != "" || stderr == "" || n.calls != 0 {
            t.Fatal(args, code, out, stderr, n.calls)
        }
    }
}

func TestNotifyChannelOptionalCapabilityAndFailure(t *testing.T) {
    args := []string{"notify", "channel", "--socket", "/private/research.sock", "--session", "research"}
    o := fixture(t)
    for _, notify := range []client.NotificationService{nil, &fakeNotify{}} {
        o.Notify = notify
        code, out, stderr := run(t, o, args...)
        if code != 1 || out != "" || !strings.Contains(stderr, "scripts/build.sh") {
            t.Fatal(code, out, stderr)
        }
    }
    const response = "{\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{}}\n"
    n := &fakeChannelNotify{response: response, err: fault.Wrap("NOTIFY_FAILED", "async_failed", errors.New("private endpoint diagnostic"))}
    o.Notify = n
    code, out, stderr := run(t, o, args...)
    l, _ := i18n.New("en", nil)
    if code != 1 || out != response || stderr != l.Text("async_failed")+"\n" {
        t.Fatal(code, out, stderr)
    }
}

func TestNotifyChannelHelpHasRequiredBindingAndNativeConstraints(t *testing.T) {
    for _, lang := range []string{"en", "zh-Hans"} {
        l, _ := i18n.New(lang, nil)
        o := fixture(t)
        n := &fakeChannelNotify{}
        o.Notify = n
        for _, args := range [][]string{
            {"--lang", lang, "help", "notify", "channel", "--json"},
            {"--lang", lang, "notify", "channel", "--help", "--json"},
        } {
            code, out, stderr := run(t, o, args...)
            var result struct {
                Data helpResult `json:"data"`
            }
            if json.Unmarshal([]byte(out), &result) != nil || code != 0 || stderr != "" || n.calls != 0 {
                t.Fatal(args, code, out, stderr)
            }
            required := []string{}
            for _, flag := range result.Data.Flags {
                if flag.Required {
                    required = append(required, flag.Name)
                }
                if (flag.Name == "json" || flag.Name == "trace") && flag.Description != l.Text("notify_channel_native") {
                    t.Fatal(flag)
                }
            }
            if result.Data.Command != "loaf notify channel" || !reflect.DeepEqual(required, []string{"socket", "session"}) {
                t.Fatalf("%+v", result.Data)
            }
        }
    }
}
