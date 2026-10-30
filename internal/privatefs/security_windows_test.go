package privatefs

import (
    "os"
    "path/filepath"
    "testing"

    "golang.org/x/sys/windows"
)

func TestRejectPublicACLWithoutRepair(t *testing.T) {
    dir := filepath.Join(t.TempDir(), "private")
    if err := MkdirAll(dir); err != nil {
        t.Fatal(err)
    }
    file, err := Create(filepath.Join(dir, "credential"))
    if err != nil {
        t.Fatal(err)
    }
    file.WriteString("retained")
    file.Close()
    descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
    if err != nil {
        t.Fatal(err)
    }
    acl, _, err := descriptor.DACL()
    if err != nil {
        t.Fatal(err)
    }
    if err := windows.SetNamedSecurityInfo(file.Name(), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
        t.Fatal(err)
    }
    if err := Check(file.Name(), false); err == nil {
        t.Fatal("Everyone access accepted")
    }
    if _, err := Open(file.Name(), true); err == nil {
        t.Fatal("insecure existing file repaired")
    }
    data, err := os.ReadFile(file.Name())
    if err != nil || string(data) != "retained" {
        t.Fatal(string(data), err)
    }
}

func TestRejectPublicInheritedChildACL(t *testing.T) {
    directory := filepath.Join(t.TempDir(), "private")
    if err := MkdirAll(directory); err != nil {
        t.Fatal(err)
    }
    user, err := windows.GetCurrentProcessToken().GetTokenUser()
    if err != nil {
        t.Fatal(err)
    }
    descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICIIO;FR;;;WD)")
    if err != nil {
        t.Fatal(err)
    }
    acl, _, err := descriptor.DACL()
    if err != nil {
        t.Fatal(err)
    }
    if err := windows.SetNamedSecurityInfo(directory, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
        t.Fatal(err)
    }
    if err := Check(directory, true); err == nil {
        t.Fatal("public child grants would expose SQLite WAL files")
    }
}
