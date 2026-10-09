package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/windows"
)

func prepareRuntime(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, "webview2")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	// Fixed Version WebView2 120+ uses an AppContainer renderer on Windows 10.
	// Grant its package groups read/execute access to the extracted code only;
	// preserve existing permissions and never grant access to application data.
	current, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}

	var sids []*windows.SID
	var access []windows.EXPLICIT_ACCESS
	for _, name := range []string{"S-1-15-2-1", "S-1-15-2-2"} {
		sid, err := windows.StringToSid(name)
		if err != nil {
			return err
		}

		sids = append(sids, sid)
		access = append(access, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}

	updated, err := windows.BuildSecurityDescriptor(nil, nil, access, nil, current)
	runtime.KeepAlive(sids)
	if err != nil {
		return err
	}

	dacl, _, err := updated.DACL()
	if err != nil {
		return err
	}

	err = windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
	runtime.KeepAlive(updated)
	return err
}
