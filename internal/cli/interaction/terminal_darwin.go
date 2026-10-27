package interaction

import "golang.org/x/sys/unix"

const readTermios = unix.TIOCGETA
const writeTermios = unix.TIOCSETA
const flushTermios = unix.TIOCSETAF
