package app

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf16"
)

// DesktopNotifier triggers local notifications. On Windows it uses PowerShell to
// display modern toast notifications, mirroring the behaviour of the Python
// implementation.
type DesktopNotifier struct {
	logger     *AppLogger
	powershell string
}

func NewDesktopNotifier(logger *AppLogger) *DesktopNotifier {
	return &DesktopNotifier{
		logger:     logger,
		powershell: findPowerShell(),
	}
}

// Send dispatches the notification asynchronously so the UI remains responsive.
func (n *DesktopNotifier) Send(title, message string) {
	if n == nil {
		return
	}
	go n.sendInternal(title, message)
}

func (n *DesktopNotifier) sendInternal(title, message string) {
	if runtime.GOOS == "windows" && n.sendWindowsToast(title, message) {
		return
	}
	if n.logger != nil {
		n.logger.Logf("Notification: %s - %s", title, message)
	}
}

func (n *DesktopNotifier) sendWindowsToast(title, message string) bool {
	if n.powershell == "" {
		return false
	}
	script := buildWindowsToastScript(title, message)
	utf16Script := utf16.Encode([]rune(script))
	buf := bytes.NewBuffer(nil)
	for _, code := range utf16Script {
		_ = binary.Write(buf, binary.LittleEndian, code)
	}
	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())
	cmd := exec.Command(n.powershell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encoded)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Run(); err != nil {
		if n.logger != nil {
			n.logger.Logf("PowerShell toast error: %v", err)
		}
		return false
	}
	return true
}

func buildWindowsToastScript(title, message string) string {
	safeTitle := title
	if strings.TrimSpace(safeTitle) == "" {
		safeTitle = AppName
	}
	safeMessage := message
	script := `
$Title = %s
$Message = %s
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null
$Template = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$TextNodes = $Template.GetElementsByTagName("text")
if ($TextNodes.Count -gt 0) { $TextNodes.Item(0).AppendChild($Template.CreateTextNode($Title)) | Out-Null }
if ($TextNodes.Count -gt 1) { $TextNodes.Item(1).AppendChild($Template.CreateTextNode($Message)) | Out-Null }
$Toast = [Windows.UI.Notifications.ToastNotification]::new($Template)
$Toast.Tag = "vrchat-join"
$Toast.Group = "vrchat-join"
$Notifier = [Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier(%s)
$Notifier.Show($Toast)
`
	return fmtSprintf(script, jsonString(safeTitle), jsonString(safeMessage), jsonString(AppName))
}

func fmtSprintf(format string, args ...interface{}) string {
	return strings.TrimSpace(fmt.Sprintf(format, args...))
}

func jsonString(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func findPowerShell() string {
	candidates := []string{"powershell.exe", "pwsh.exe", "powershell", "pwsh"}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	return ""
}

// PushoverClient performs HTTPS requests against the Pushover API when the user
// supplied API token and user key are present in the configuration.
type PushoverClient struct {
	cfg    *AppConfig
	logger *AppLogger
}

func NewPushoverClient(cfg *AppConfig, logger *AppLogger) *PushoverClient {
	return &PushoverClient{cfg: cfg, logger: logger}
}

// Send transmits the push notification asynchronously.
func (p *PushoverClient) Send(title, message string) {
	if p == nil || p.cfg == nil {
		return
	}
	token := strings.TrimSpace(p.cfg.PushoverToken)
	user := strings.TrimSpace(p.cfg.PushoverUser)
	if token == "" || user == "" {
		if p.logger != nil {
			p.logger.Log("Pushover not configured; skipping.")
		}
		return
	}
	go p.sendInternal(token, user, title, message)
}

func (p *PushoverClient) sendInternal(token, user, title, message string) {
	payload := url.Values{
		"token":    {token},
		"user":     {user},
		"title":    {title},
		"message":  {message},
		"priority": {"0"},
	}
	resp, err := http.PostForm(PoURL, payload)
	if err != nil {
		if p.logger != nil {
			p.logger.Logf("Pushover error: %v", err)
		}
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if p.logger == nil {
		return
	}
	var parsed struct {
		Status int      `json:"status"`
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		p.logger.Log("Pushover sent; response parsing failed.")
		return
	}
	if parsed.Status == 1 {
		p.logger.Log("Pushover sent: 1")
	} else if len(parsed.Errors) > 0 {
		p.logger.Logf("Pushover rejected: %s", strings.Join(parsed.Errors, "; "))
	} else {
		p.logger.Logf("Pushover responded with status %d", parsed.Status)
	}
}
