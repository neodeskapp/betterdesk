package servermgmt

import "io/fs"

// owner is 0 and 0 on Windows, as Node's stat gave it there.
func owner(fs.FileInfo) (uid, gid int64) { return 0, 0 }
