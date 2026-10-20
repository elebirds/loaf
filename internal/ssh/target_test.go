package ssh

import (
    "os/exec"
    "reflect"
    "testing"
)

func TestConnectionParsing(t *testing.T) {
    for _, tc := range []struct {
        input, dest string
        args        []string
    }{
        {"lab-gpu", "lab-gpu", nil},
        {`ssh -p 12345 root@example.com`, "root@example.com", []string{"-p", "12345"}},
        {`ssh -J bastion -i '/tmp/a space/key' user@host`, "user@host", []string{"-J", "bastion", "-i", "/tmp/a space/key"}},
        {`ssh -F '/tmp/中文 config' host`, "host", []string{"-F", "/tmp/中文 config"}},
        {`ssh -p2222 user@[::1]`, "user@[::1]", []string{"-p", "2222"}},
        {`ssh -i /tmp/a\ space/key host`, "host", []string{"-i", "/tmp/a space/key"}},
        {`ssh -i "/tmp/a\\b/key" host`, "host", []string{"-i", `/tmp/a\b/key`}},
        {`ssh -i '~/.ssh/key' host`, "host", []string{"-i", "~/.ssh/key"}},
        {`ssh ssh://user@host:2222`, "ssh://user@host:2222", nil},
    } {
        got, err := ParseConnection(tc.input)
        if err != nil || got.Destination != tc.dest || !reflect.DeepEqual(got.Args, tc.args) {
            t.Errorf("%q: %+v %v", tc.input, got, err)
        }
    }
}

func TestRejectExecutableAndUnsafeInput(t *testing.T) {
    for _, s := range []string{`ssh host uname`, `ssh host; touch /tmp/no`, `ssh host | cat`, `ssh host > log`, `ssh $(hostname)`, `ssh "$HOST"`, `SSH=x ssh host`, `ssh host &`, `ssh -oProxyCommand=evil host`, `ssh -oStrictHostKeyChecking=no host`, `ssh -oUserKnownHostsFile=/dev/null host`, `ssh -L 6006:localhost:6006 host`, `ssh -p 99999 host`, `ssh ssh://user:password@host`, `ssh 'host;evil'`, `! ssh host`, `ssh host && true`} {
        if _, err := ParseConnection(s); err == nil {
            t.Errorf("accepted %q", s)
        }
    }
}

func TestRemoteQuoting(t *testing.T) {
    for _, s := range []string{"a space", "quote'and\"double", "中文;$(exit 3)", "line\nnext"} {
        got, err := exec.Command("/bin/sh", "-c", "printf %s "+Quote(s)).Output()
        if err != nil || string(got) != s {
            t.Errorf("%q: %q %v", s, got, err)
        }
    }
}

func FuzzConnection(f *testing.F) {
    for _, s := range []string{"ssh host", "ssh -p 2222 root@host", "ssh host; true", "'unterminated"} {
        f.Add(s)
    }
    f.Fuzz(func(t *testing.T, input string) {
        target, err := ParseConnection(input)
        if err == nil {
            if err := Validate(target); err != nil {
                t.Fatal(err)
            }
        }
    })
}
