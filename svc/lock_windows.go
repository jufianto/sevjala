package svc

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// lockedSDDL: owner Administrators; SYSTEM and Administrators full control,
// inherited by children; parent ACEs blocked.
const lockedSDDL = "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

// LockDataDir refuses a dir not owned by Administrators or SYSTEM, since a
// user could have pre-created it with a planted config (decisions.md D-010).
func LockDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if !trusted(owner) {
		return fmt.Errorf("refusing %s: owned by %s, not Administrators or SYSTEM; remove it or take ownership as an administrator first", dir, account(owner))
	}
	want, err := windows.SecurityDescriptorFromString(lockedSDDL)
	if err != nil {
		return err
	}
	dacl, _, err := want.DACL()
	if err != nil {
		return err
	}
	admins, _, err := want.Owner()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		admins, nil, dacl, nil)
}

func CheckDataDirLocked(dir string) error {
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if !trusted(owner) {
		return fmt.Errorf("%s is owned by %s", dir, account(owner))
	}
	ctrl, _, err := sd.Control()
	if err != nil {
		return err
	}
	if ctrl&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("%s inherits permissions from its parent", dir)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if dacl == nil {
		return fmt.Errorf("%s has no access list (everyone has access)", dir)
	}
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return err
		}
		if sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)); !trusted(sid) {
			return fmt.Errorf("%s grants access to %s", dir, account(sid))
		}
	}
	return nil
}

func trusted(s *windows.SID) bool {
	return s.IsWellKnown(windows.WinBuiltinAdministratorsSid) || s.IsWellKnown(windows.WinLocalSystemSid)
}

func account(s *windows.SID) string {
	acc, dom, _, err := s.LookupAccount("")
	if err != nil {
		return s.String()
	}
	return dom + `\` + acc
}
