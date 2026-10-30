package testutil

import (
    "golang.org/x/sys/windows"
    "testing"
)

// Public adds broad read access without changing the fixture's owner.
func Public(t testing.TB, path string) func() {
    t.Helper()
    prior, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
    if err != nil {
        t.Fatal(err)
    }
    descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;OW)(A;OICI;GR;;;WD)")
    if err != nil {
        t.Fatal(err)
    }
    set := func(sd *windows.SECURITY_DESCRIPTOR) {
        acl, _, err := sd.DACL()
        if err != nil {
            t.Fatal(err)
        }
        if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
            t.Fatal(err)
        }
    }
    set(descriptor)
    return func() { set(prior) }
}
