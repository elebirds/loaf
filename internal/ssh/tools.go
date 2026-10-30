package ssh

// Tools is one platform's compatible SSH/rsync installation. Windows also uses
// that installation's cygpath at the local path boundary.
type Tools struct{ SSH, Rsync, Cygpath string }
