//go:build !windows

package localterm

import (
	"os"
	"syscall"
)

// privateDir reports whether only its owner can reach dir.
func privateDir(fi os.FileInfo) bool { return fi.Mode().Perm()&0o077 == 0 }

// ownedByOther reports whether another user owns dir.
func ownedByOther(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) != os.Getuid()
}
