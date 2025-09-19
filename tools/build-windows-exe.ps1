param(
    [string]$PyInstaller = "pyinstaller"
)

$ErrorActionPreference = "Stop"

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot '..')
Push-Location $repoRoot
try {
    $iconPath = "src/vrchat_join_notification/notification.ico"
    $entryPoint = "src/vrchat_join_notification/app.py"
    $dataArg = "$iconPath;vrchat_join_notification"

    & $PyInstaller `
        --noconsole `
        --name "VRChatJoinNotificationWithPushover" `
        --icon $iconPath `
        --add-data $dataArg `
        $entryPoint
}
finally {
    Pop-Location
}
