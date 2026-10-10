package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"shelfmark/internal/notices"
)

// desktopConsent records agreement to Microsoft's separate runtime terms before
// launching WebView2. The server is already listening, so the full terms can be
// read in a browser while this native dialog is open. Declining keeps HTTP mode.
func desktopConsent(programDir, url string) error {
	terms, err := notices.Read("licenses/WebView2-Fixed-Version.html")
	if err != nil {
		return err
	}
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(terms))
	path := filepath.Join(programDir, "webview2-license-acceptance.txt")
	if accepted, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(accepted)) == fingerprint {
		return nil
	}
	message, err := windows.UTF16PtrFromString("Shelfmark uses Microsoft WebView2 for its desktop window. This runtime has separate Microsoft license terms.\n\nRead the full terms before agreeing:\n" + url + "/api/webview2-license\n\nWebView2 includes Microsoft Defender SmartScreen, which collects and sends information to Microsoft as described at https://aka.ms/privacy and https://learn.microsoft.com/en-us/microsoft-edge/privacy-whitepaper#smartscreen.\n\nChoose Yes to agree to the Microsoft terms and open the desktop window. Choose No to continue with HTTP only. You can copy this dialog with Ctrl+C.")
	if err != nil {
		return err
	}
	title, _ := windows.UTF16PtrFromString("Shelfmark — WebView2 license agreement")
	messageBox := windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")
	if err := messageBox.Find(); err != nil {
		return fmt.Errorf("show WebView2 agreement: %w", err)
	}
	// MB_YESNO | MB_ICONQUESTION | MB_DEFBUTTON2: consent is never preselected.
	result, _, callErr := messageBox.Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x124)
	if result == 0 {
		return fmt.Errorf("show WebView2 agreement: %w", callErr)
	}
	if result != 6 {
		return fmt.Errorf("WebView2 license agreement declined")
	}
	if err := os.WriteFile(path, []byte(fingerprint+"\n"), 0o600); err != nil {
		return fmt.Errorf("save WebView2 agreement: %w", err)
	}
	return nil
}
