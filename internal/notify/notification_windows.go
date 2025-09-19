//go:build windows

package notify

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

func sendToast(title, message string) error {
	titleArg := quoteForPowerShell(title)
	messageArg := quoteForPowerShell(message)
	script := fmt.Sprintf(`
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null
$template = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$textNodes = $template.GetElementsByTagName('text')
$textNodes.Item(0).AppendChild($template.CreateTextNode(%s)) | Out-Null
$textNodes.Item(1).AppendChild($template.CreateTextNode(%s)) | Out-Null
$toast = [Windows.UI.Notifications.ToastNotification]::new($template)
$toast.Tag = 'VRChatJoinNotificationWithPushover'
$notifier = [Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('VRChatJoinNotificationWithPushover.App')
$notifier.Show($toast)
`, titleArg, messageArg)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("toast failed: %w", err)
	}
	return nil
}

func quoteForPowerShell(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
