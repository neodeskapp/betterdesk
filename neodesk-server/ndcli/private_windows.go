//go:build windows

package ndcli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The access rights that let someone read the file or change it: its
// data, its access list or its owner, also through the generic rights.
const othersMayNot = 0x1 | 0x2 | 0x4 | // FILE_READ_DATA, FILE_WRITE_DATA, FILE_APPEND_DATA
	windows.WRITE_DAC | windows.WRITE_OWNER |
	windows.GENERIC_READ | windows.GENERIC_WRITE | windows.GENERIC_ALL

// makePrivate gives the file an access list of its own, not the folder's:
// the account that runs this, SYSTEM and Administrators, each with full
// control. It is chmod 600 for Windows.
func makePrivate(f *os.File) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	var entries []windows.EXPLICIT_ACCESS
	for _, sid := range []*windows.SID{user.User.Sid, system, admins} {
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Trustee:           windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeValue: windows.TrusteeValueFromSID(sid)},
		})
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(f.Name(), windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

// checkPrivate on Windows refuses a file another account owns, and a file
// whose access list lets another account read or change it: its token
// would not be yours alone, and its address could send your token
// elsewhere (L431). The default place, the user's own profile folder
// (%AppData%), usually lists only the account, SYSTEM and Administrators; the mode
// bits mean nothing here. A file an elevated prompt made may belong to the
// Administrators group: that counts as yours when this process runs
// elevated too. An entry for a local account that no longer exists (its
// SID maps to no name) opens the file to nobody and is passed over: a
// profile folder can still carry one from a deleted account.
func checkPrivate(path string, _ os.FileInfo) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("%w: %s: cannot read its owner and access list: %v", ErrConfigOpen, path, err)
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
	admins, _ := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	system, _ := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	ownerRights, _ := windows.CreateWellKnownSid(windows.WinCreatorOwnerRightsSid)
	switch {
	case owner.Equals(user.User.Sid):
	case admins != nil && owner.Equals(admins) && token.IsElevated():
	default:
		return fmt.Errorf("%w: %s belongs to another account (%s)", ErrConfigOpen, path, owner.String())
	}

	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("%w: %s: cannot read its access list: %v", ErrConfigOpen, path, err)
	}
	if dacl == nil {
		// No access list at all lets everyone do everything
		return fmt.Errorf("%w: %s has no access list, so every account may read it", ErrConfigOpen, path)
	}
	trusted := func(sid *windows.SID) bool {
		for _, mine := range []*windows.SID{user.User.Sid, owner, system, admins, ownerRights} {
			if mine != nil && sid.Equals(mine) {
				return true
			}
		}
		return false
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("%w: %s: cannot read its access list: %v", ErrConfigOpen, path, err)
		}
		// Entries that deny something, or that only pass on to files made
		// inside (a folder's), give nobody this file
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		if uint32(ace.Mask)&othersMayNot == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if trusted(sid) {
			continue
		}
		name := sid.String()
		account, domain, _, err := sid.LookupAccount("")
		if errors.Is(err, windows.ERROR_NONE_MAPPED) && strings.HasPrefix(name, "S-1-5-21-") {
			continue
		}
		if err == nil && account != "" {
			if domain != "" {
				account = domain + `\` + account
			}
			name = account + " (" + sid.String() + ")"
		}
		return fmt.Errorf("%w: %s can also be read or changed by %s: remove that entry in its Properties, Security", ErrConfigOpen, path, name)
	}
	return nil
}
