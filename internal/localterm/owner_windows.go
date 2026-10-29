//go:build windows

package localterm

import "os"

// On Windows a directory's access is its ACL, which its mode bits don't
// show; the daemon's own pipes carry the user's default security.

func privateDir(os.FileInfo) bool { return true }

func ownedByOther(os.FileInfo) bool { return false }
