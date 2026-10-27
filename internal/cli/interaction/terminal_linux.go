package interaction

import "golang.org/x/sys/unix"

const readTermios = unix.TCGETS
const writeTermios = unix.TCSETS
const flushTermios = unix.TCSETSF
