//go:build windows

package app

// Message box flag aliases for callers outside the package.
const (
	MBOK          = mbOK
	MBIconInfo    = mbIconInformation
	MBIconWarning = mbIconWarning
	MBIconError   = mbIconError
)

// ShowMessage displays a modal Windows message box.
func ShowMessage(text, title string, flags uint32) {
	messageBox(0, text, title, flags)
}
