//go:build windows

package ndcli

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// checkPrivate on Windows refuses a file another account owns: its token
// would not be yours alone, and its address could send your token
// elsewhere. The default place, the user's own profile folder (%AppData%),
// is closed to other accounts; the mode bits mean nothing here. A file an
// elevated prompt made may belong to the Administrators group: that counts
// as yours when this process runs elevated too.
func checkPrivate(path string, _ os.FileInfo) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("%w: %s: cannot read its owner: %v", ErrConfigOpen, path, err)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return fmt.Errorf("%w: %s has no owner", ErrConfigOpen, path)
	}
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("%w: %s: who runs this: %v", ErrConfigOpen, path, err)
	}
	if owner.Equals(user.User.Sid) {
		return nil
	}
	if admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid); err == nil && owner.Equals(admins) && token.IsElevated() {
		return nil
	}
	return fmt.Errorf("%w: %s belongs to another account (%s)", ErrConfigOpen, path, owner.String())
}
