package interaction

import (
    "context"
    "errors"
    "testing"
)

func TestNativeOwnershipIsSerialAndCancellationDoesNotReplay(t *testing.T) {
    session := New(Options{})
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    entered, release := make(chan struct{}), make(chan struct{})
    first := make(chan error, 1)
    marker := errors.New("native result")
    go func() {
        first <- session.WithNative(context.Background(), nil, func() error {
            close(entered)
            <-release
            return marker
        })
    }()
    <-entered
    called := false
    cancel()
    if err := session.WithNative(ctx, nil, func() error { called = true; return nil }); !errors.Is(err, context.Canceled) || called {
        t.Fatal("canceled operation ran", called, err)
    }
    close(release)
    if err := <-first; !errors.Is(err, marker) {
        t.Fatal(err)
    }
    if err := session.WithNative(context.Background(), nil, func() error { called = true; return nil }); err != nil || !called {
        t.Fatal("input owner was not released", called, err)
    }
}
