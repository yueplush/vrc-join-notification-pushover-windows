//go:build windows

package app

// HideConsoleWindow hides the console window when running the Windows binary.
func HideConsoleWindow() {
	hideConsoleWindow()
}
