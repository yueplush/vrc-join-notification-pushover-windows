#requires -Version 5.1
Param(
    [switch]$OpenSettings
)

<#+
VRChat Join Notification with Pushover (Windows, rewritten)

This PowerShell build mirrors the Python/Tk Linux application:
- WinForms configuration window that matches the Linux layout.
- Tray icon with quick actions and live status text.
- Log follower with the same session-tracking heuristics as the Python port.
- Optional Pushover pushes.
- JSON settings stored next to the local cache/log directory.
#>

Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
[System.Windows.Forms.Application]::EnableVisualStyles()
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# ------------------------------- Constants -------------------------------
$AppName        = 'VRChat Join Notification with Pushover'
$ConfigFileName = 'config.json'
$AppLogName     = 'notifier.log'
$POUrl          = 'https://api.pushover.net/1/messages.json'
$IconFileName   = 'notification.ico'

$NotifyCooldownSeconds                 = 10
$SessionFallbackGraceSeconds           = 30
$SessionFallbackMaxContinuationSeconds = 4

$JoinSeparatorChars = [char[]]@('-', ':', '|', [char]0x2013, [char]0x2014)
$JoinSeparatorOnlyPattern = '^[\-:|\u2013\u2014]+$'

# ------------------------------- Globals ---------------------------------
$script:Config = $null
$script:LoadError = $null
$script:EventQueue = [System.Collections.Concurrent.ConcurrentQueue[object[]]]::new()
$script:MonitorThread = $null
$script:MonitorTokenSource = $null
$script:LoggerLock = New-Object System.Object
$script:IsQuitting = $false
$script:TrayIcon = $null

$script:Session = [ordered]@{
    SessionId          = 0
    Ready              = $false
    Source             = ''
    SeenPlayers        = @{}
    PendingRoom        = $null
    SessionStartedAt   = $null
    SessionLastJoinAt  = $null
    SessionLastJoinRaw = $null
    PendingSelfJoin    = $null
    LastNotified       = @{}
    LocalUserId        = $null
}

$script:Controls = @{}
$script:SingleInstanceMutex = $null
$script:HasMutexOwnership = $false

# ----------------------------- Helper utils ------------------------------
function Expand-PathSafe {
    Param([string]$Path)
    if([string]::IsNullOrWhiteSpace($Path)){ return '' }
    $expanded = [Environment]::ExpandEnvironmentVariables($Path.Trim())
    try {
        return [System.IO.Path]::GetFullPath($expanded)
    } catch {
        return $expanded
    }
}

function Ensure-Dir {
    Param([string]$Path)
    if([string]::IsNullOrWhiteSpace($Path)){ return }
    if(-not (Test-Path -Path $Path -PathType Container)){
        New-Item -Path $Path -ItemType Directory -Force | Out-Null
    }
}

function Acquire-SingleInstance {
    Param([string]$Name)
    if([string]::IsNullOrWhiteSpace($Name)){ return $true }
    try {
        $createdNew = $false
        $script:SingleInstanceMutex = New-Object System.Threading.Mutex($true, $Name, [ref]$createdNew)
        $script:HasMutexOwnership = $createdNew
        return $createdNew
    } catch {
        $script:SingleInstanceMutex = $null
        $script:HasMutexOwnership = $false
        return $true
    }
}

function Release-SingleInstance {
    if(-not $script:SingleInstanceMutex){ return }
    try {
        if($script:HasMutexOwnership){
            $script:SingleInstanceMutex.ReleaseMutex()
        }
    } catch {}
    try {
        $script:SingleInstanceMutex.Dispose()
    } catch {}
    $script:SingleInstanceMutex = $null
    $script:HasMutexOwnership = $false
}

function Get-DefaultInstallDir {
    $root = [System.IO.Path]::Combine([Environment]::GetFolderPath('LocalApplicationData'), 'VRChatJoinNotificationWithPushover')
    return Expand-PathSafe $root
}

function Get-LocalLowFolder {
    $local = [Environment]::GetFolderPath('LocalApplicationData')
    if($local -match '\\Local$'){
        return Expand-PathSafe ($local -replace '\\Local$', '\\LocalLow')
    }
    $user = [Environment]::GetFolderPath('UserProfile')
    return Expand-PathSafe ([System.IO.Path]::Combine($user, 'AppData', 'LocalLow'))
}

function Guess-VRChatLogDir {
    $candidates = @(
        [System.IO.Path]::Combine((Get-LocalLowFolder), 'VRChat', 'VRChat'),
        [System.IO.Path]::Combine([Environment]::GetFolderPath('MyDocuments'), 'VRChat', 'VRChat')
    )
    foreach($candidate in $candidates){
        $expanded = Expand-PathSafe $candidate
        if(Test-Path -Path $expanded -PathType Container){
            return $expanded
        }
    }
    return Expand-PathSafe $candidates[0]
}

function Load-AppConfig {
    $installDir = Get-DefaultInstallDir
    Ensure-Dir $installDir
    $configPath = Join-Path $installDir $ConfigFileName
    $data = $null
    $loadError = $null
    $firstRun = $true
    if(Test-Path $configPath){
        try {
            $raw = Get-Content -Path $configPath -Raw -Encoding UTF8
            if($raw){
                $data = $raw | ConvertFrom-Json -ErrorAction Stop
            }
            $firstRun = $false
        } catch {
            $loadError = "Failed to load settings: $($_.Exception.Message)"
            $data = $null
        }
    }
    $install = $installDir
    $logDir = Guess-VRChatLogDir
    $user = ''
    $token = ''
    if($data){
        if($data.PSObject.Properties['InstallDir']){ $install = Expand-PathSafe([string]$data.InstallDir) }
        if($data.PSObject.Properties['VRChatLogDir']){ $logDir = Expand-PathSafe([string]$data.VRChatLogDir) }
        if($data.PSObject.Properties['PushoverUser']){ $user = [string]$data.PushoverUser }
        if($data.PSObject.Properties['PushoverToken']){ $token = [string]$data.PushoverToken }
    }
    $cfg = [pscustomobject]@{
        InstallDir    = $install
        VRChatLogDir  = $logDir
        PushoverUser  = $user
        PushoverToken = $token
        FirstRun      = $firstRun
    }
    return ,@($cfg, $loadError)
}

function Save-AppConfig {
    Param([pscustomobject]$Config)
    if(-not $Config){ return }
    if([string]::IsNullOrWhiteSpace($Config.InstallDir)){
        throw 'Install directory is required.'
    }
    Ensure-Dir $Config.InstallDir
    $payload = [ordered]@{
        InstallDir   = $Config.InstallDir
        VRChatLogDir = $Config.VRChatLogDir
    }
    if(-not [string]::IsNullOrWhiteSpace($Config.PushoverUser)){
        $payload['PushoverUser'] = $Config.PushoverUser
    }
    if(-not [string]::IsNullOrWhiteSpace($Config.PushoverToken)){
        $payload['PushoverToken'] = $Config.PushoverToken
    }
    $json = ($payload | ConvertTo-Json -Depth 5)
    $configPath = Join-Path $Config.InstallDir $ConfigFileName
    Set-Content -Path $configPath -Value $json -Encoding UTF8
    $Config.FirstRun = $false
}

function Write-AppLog {
    Param([string]$Message)
    if(-not $script:Config){ return }
    if([string]::IsNullOrWhiteSpace($script:Config.InstallDir)){ return }
    $timestamp = (Get-Date).ToString('yyyy-MM-dd HH:mm:ss')
    $line = "[$timestamp] $Message"
    try {
        Ensure-Dir $script:Config.InstallDir
        $logPath = Join-Path $script:Config.InstallDir $AppLogName
        [System.Threading.Monitor]::Enter($script:LoggerLock)
        try {
            Add-Content -Path $logPath -Value $line -Encoding UTF8
        } finally {
            [System.Threading.Monitor]::Exit($script:LoggerLock)
        }
    } catch {
        # ignore logging errors
    }
}

function Enqueue-Event {
    Param(
        [Parameter(Mandatory=$true)][string]$Type,
        [Parameter(ValueFromRemainingArguments=$true)][object[]]$Args
    )
    $payload = [object[]]@($Type)
    if($Args){ $payload += $Args }
    $script:EventQueue.Enqueue($payload)
}

function Invoke-UIThread {
    Param(
        [Parameter(Mandatory=$true)][ScriptBlock]$Action,
        [object[]]$Arguments
    )
    if(-not $Action){ return }
    if(-not $Arguments){ $Arguments = @() }
    $form = $null
    if($script:Controls -and $script:Controls.ContainsKey('Form')){
        $form = $script:Controls.Form
    }
    if($form -and -not $form.IsDisposed){
        try {
            if($form.InvokeRequired){
                $form.BeginInvoke($Action, $Arguments) | Out-Null
                return
            }
        } catch {}
    }
    if($Arguments.Length){
        & $Action @Arguments
    } else {
        & $Action
    }
}

function Strip-ZeroWidth {
    Param([string]$Text)
    if([string]::IsNullOrEmpty($Text)){ return '' }
    return [regex]::Replace($Text, '[\u200B-\u200D\uFEFF]', '')
}

function Normalize-JoinFragment {
    Param([string]$Text)
    $clean = Strip-ZeroWidth($Text)
    if(-not $clean){ return '' }
    $clean = $clean -replace '\u3000', ' '
    $clean = $clean.Trim()
    $clean = $clean.Trim('"').Trim("'").Trim()
    $clean = $clean -replace '\|\|', '|'
    while($clean.Length -gt 0 -and $JoinSeparatorChars -contains $clean[0]){
        $clean = $clean.Substring(1).TrimStart()
    }
    if($clean.Length -gt 160){ $clean = $clean.Substring(0,160).Trim() }
    if([string]::IsNullOrEmpty($clean)){ return '' }
    if($clean -match $JoinSeparatorOnlyPattern){ return '' }
    return $clean
}

function Normalize-JoinName {
    Param([string]$Text)
    $clean = Normalize-JoinFragment($Text)
    if([string]::IsNullOrEmpty($clean)){ return '' }
    return $clean
}

function Is-PlaceholderName {
    Param([string]$Name)
    if([string]::IsNullOrWhiteSpace($Name)){ return $true }
    $trimmed = $Name.Trim().ToLowerInvariant()
    return @('player','you','someone','a player').Contains($trimmed)
}

function Get-ShortHash {
    Param([string]$Text)
    if([string]::IsNullOrEmpty($Text)){ return '' }
    try {
        $md5 = [System.Security.Cryptography.MD5]::Create()
        try {
            $bytes = [System.Text.Encoding]::UTF8.GetBytes($Text)
            $hash = $md5.ComputeHash($bytes)
            return ($hash[0..3] | ForEach-Object { $_.ToString('x2') }) -join ''
        } finally {
            $md5.Dispose()
        }
    } catch {
        return ''
    }
}

function Parse-PlayerEventLine {
    Param(
        [string]$Line,
        [string]$EventToken = 'OnPlayerJoined'
    )
    if([string]::IsNullOrWhiteSpace($Line)){ return $null }
    $lower = $Line.ToLowerInvariant()
    $needle = $EventToken.ToLowerInvariant()
    $index = $lower.IndexOf($needle)
    if($index -lt 0){ return $null }
    $after = Strip-ZeroWidth($Line.Substring($index + $needle.Length)).Trim()
    while($after.Length -gt 0 -and $JoinSeparatorChars -contains $after[0]){
        $after = $after.Substring(1).TrimStart()
    }
    $placeholder = ''
    if($after){
        $match = [regex]::Match($after, '(?i)\b(displayName|name|userId)\b')
        if($match.Success){
            $candidate = $after.Substring(0, $match.Index)
        } else {
            $candidate = $after
        }
        foreach($splitChar in @('(', '[', '{', '<')){
            $parts = $candidate.Split($splitChar)
            if($parts.Length -gt 0){ $candidate = $parts[0] }
        }
        $candidate = Normalize-JoinFragment($candidate)
        if(Is-PlaceholderName($candidate)){
            $placeholder = $candidate
        }
    }
    $displayName = ''
    $match = [regex]::Match($after, '(?i)displayName\s*[:=]\s*([^,\]\)]+)')
    if($match.Success){
        $displayName = Normalize-JoinFragment($match.Groups[1].Value)
    }
    if(-not $displayName){
        $match = [regex]::Match($after, '(?i)\bname\s*[:=]\s*([^,\]\)]+)')
        if($match.Success){
            $displayName = Normalize-JoinFragment($match.Groups[1].Value)
        }
    }
    $userId = ''
    $match = [regex]::Match($after, '(?i)\(usr_[^\)\s]+\)')
    if($match.Success){
        $userId = $match.Value.Trim('(', ')', ' ')
    }
    if(-not $userId){
        $match = [regex]::Match($after, '(?i)userId\s*[:=]\s*(usr_[0-9a-f\-]+)')
        if($match.Success){
            $userId = $match.Groups[1].Value
        }
    }
    if(-not $displayName){
        $tmp = $after
        if($userId){
            $tmp = [regex]::Replace($tmp, [regex]::Escape("($userId)"), '', 'IgnoreCase')
        }
        $tmp = [regex]::Replace($tmp, '(?i)\(usr_[^\)]*\)', '')
        $tmp = [regex]::Replace($tmp, '(?i)\(userId[^\)]*\)', '')
        $tmp = [regex]::Replace($tmp, '\[[^\]]*\]', '')
        $tmp = [regex]::Replace($tmp, '\{[^\}]*\}', '')
        $tmp = [regex]::Replace($tmp, '<[^>]*>', '')
        $tmp = $tmp -replace '\|\|', '|'
        $displayName = Normalize-JoinFragment($tmp)
    }
    if(-not $displayName -and $userId){
        $displayName = $userId
    }
    $safe = Strip-ZeroWidth($Line).Replace('||','|').Trim()
    return [pscustomobject]@{
        name        = $displayName
        user_id     = $userId
        placeholder = $placeholder
        raw_line    = $safe
    }
}

function Parse-RoomTransitionLine {
    Param([string]$Line)
    if([string]::IsNullOrWhiteSpace($Line)){ return $null }
    $clean = Strip-ZeroWidth($Line).Trim()
    if(-not $clean){ return $null }
    $lower = $clean.ToLowerInvariant()
    $indicators = @(
        'joining or creating room', 'entering room', 'joining room', 'creating room',
        'created room', 'rejoining room', 're-joining room', 'reentering room',
        're-entering room', 'joining instance', 'creating instance', 'entering instance'
    )
    $matched = $false
    foreach($indicator in $indicators){
        if($lower.Contains($indicator)){ $matched = $true; break }
    }
    if(-not $matched){
        $jpSets = @(
            @{ key = 'ルーム'; terms = @('参加','作成','入室','移動','入場') },
            @{ key = 'インスタンス'; terms = @('参加','作成','入室','移動','入場') }
        )
        foreach($jp in $jpSets){
            if($clean.Contains($jp.key)){
                foreach($term in $jp.terms){
                    if($clean.Contains($term)){ $matched = $true; break }
                }
            }
            if($matched){ break }
        }
    }
    if(-not $matched){
        if([regex]::IsMatch($clean, '(?i)wrld_[0-9a-f\-]+')){
            if($lower.Contains('room') -or $lower.Contains('instance') -or $clean.Contains('インスタンス') -or $clean.Contains('ルーム')){
                $matched = $true
            }
        }
    }
    if(-not $matched){ return $null }
    $world = ''
    $instance = ''
    $match = [regex]::Match($clean, '(?i)wrld_[0-9a-f\-]+')
    if($match.Success){
        $world = $match.Value
        $after = $clean.Substring($match.Index + $match.Length).TrimStart(':',' ','`t','-')
        if($after){
            $instMatch = [regex]::Match($after, '^[^\s,]+')
            if($instMatch.Success){ $instance = $instMatch.Value }
        }
    }
    if(-not $instance){
        $instAlt = [regex]::Match($clean, '(?i)instance\s*[:=]\s*([^\s,]+)')
        if($instAlt.Success){ $instance = $instAlt.Groups[1].Value }
    }
    return [pscustomobject]@{
        world    = $world
        instance = $instance
        raw_line = $clean
    }
}

function Is-VRChatRunning {
    try {
        return @(Get-Process -Name 'VRChat' -ErrorAction SilentlyContinue).Count -gt 0
    } catch {
        return $false
    }
}

function Score-LogFile {
    Param([string]$Path)
    try {
        $info = Get-Item -LiteralPath $Path -ErrorAction Stop
    } catch {
        return 0.0
    }
    $best = [double]([DateTimeOffset]$info.LastWriteTimeUtc).ToUnixTimeSeconds()
    $created = [double]([DateTimeOffset]$info.CreationTimeUtc).ToUnixTimeSeconds()
    if($created -gt $best){ $best = $created }
    $match = [regex]::Match([System.IO.Path]::GetFileName($Path), 'output_log_(\d{4})-(\d{2})-(\d{2})_(\d{2})-(\d{2})-(\d{2})\.txt$', [System.Text.RegularExpressions.RegexOptions]::IgnoreCase)
    if($match.Success){
        try {
            $dt = Get-Date -Year $match.Groups[1].Value -Month $match.Groups[2].Value -Day $match.Groups[3].Value -Hour $match.Groups[4].Value -Minute $match.Groups[5].Value -Second $match.Groups[6].Value
            $stamp = [double]([DateTimeOffset]$dt.ToUniversalTime()).ToUnixTimeSeconds()
            if($stamp -gt $best){ $best = $stamp }
        } catch {}
    }
    return $best
}

function Get-NewestLogPath {
    Param([string]$LogDir)
    if([string]::IsNullOrWhiteSpace($LogDir)){ return $null }
    if(-not (Test-Path -Path $LogDir -PathType Container)){ return $null }
    try {
        $candidates = Get-ChildItem -Path $LogDir -File -ErrorAction Stop | Where-Object {
            $_.Name -ieq 'player.log' -or $_.Name -like 'output_log_*'
        }
    } catch {
        return $null
    }
    if(-not $candidates){ return $null }
    $sorted = $candidates | Sort-Object { Score-LogFile $_.FullName } -Descending
    return $sorted[0].FullName
}
function Reset-SessionState {
    $script:Session.Ready = $false
    $script:Session.Source = ''
    $script:Session.SeenPlayers = @{}
    $script:Session.SessionStartedAt = $null
    $script:Session.SessionLastJoinAt = $null
    $script:Session.SessionLastJoinRaw = $null
    $script:Session.PendingRoom = $null
    $script:Session.PendingSelfJoin = $null
    $script:Session.LocalUserId = $null
}

function Ensure-SessionReady {
    Param([string]$Reason)
    if($script:Session.Ready){ return $false }
    if([string]::IsNullOrWhiteSpace($Reason)){ $Reason = 'unknown trigger' }
    $script:Session.SessionId++
    $script:Session.Ready = $true
    $script:Session.Source = $Reason
    $script:Session.SeenPlayers = @{}
    $script:Session.SessionStartedAt = [DateTime]::UtcNow
    $script:Session.SessionLastJoinAt = $null
    $script:Session.SessionLastJoinRaw = $null
    $roomDesc = $null
    if($script:Session.PendingRoom){
        $world = [string]$script:Session.PendingRoom.world
        $instance = [string]$script:Session.PendingRoom.instance
        if($world){
            $roomDesc = $world
            if($instance){ $roomDesc += ":$instance" }
        }
    }
    $message = "Session $($script:Session.SessionId) started ($Reason)"
    if($roomDesc){ $message += " [$roomDesc]" }
    $message += '.'
    Write-AppLog $message
    return $true
}

function Notify-All {
    Param(
        [string]$Key,
        [string]$Title,
        [string]$Message,
        [bool]$Desktop = $true
    )
    $now = [DateTime]::UtcNow
    $previous = $null
    if($script:Session.LastNotified.ContainsKey($Key)){
        $previous = $script:Session.LastNotified[$Key]
    }
    if($previous){
        if(($now - $previous).TotalSeconds -lt $NotifyCooldownSeconds){
            Write-AppLog "Suppressed '$Key' within cooldown."
            return
        }
    }
    $script:Session.LastNotified[$Key] = $now
    if($Desktop){ Send-DesktopNotification $Title $Message }
    Send-PushoverNotification $Title $Message
}

function Handle-LogSwitch {
    Param([string]$Path)
    Write-AppLog "Switching to newest log: $Path"
    Reset-SessionState
}

function Handle-RoomEnter {
    Param([pscustomobject]$Info)
    $world = [string]$Info.world
    $instance = [string]$Info.instance
    $raw = [string]$Info.raw_line
    $script:Session.PendingRoom = $Info
    if($world){
        $desc = $world
        if($instance){ $desc += ":$instance" }
        Write-AppLog "Room transition detected: $desc"
    } elseif($raw){
        Write-AppLog "Room transition detected: $raw"
    } else {
        Write-AppLog 'Room transition detected.'
    }
}

function Handle-RoomLeft {
    if($script:Session.Ready){
        Write-AppLog "Session $($script:Session.SessionId) ended (OnLeftRoom detected.)"
    } else {
        Write-AppLog 'OnLeftRoom detected.'
    }
    Reset-SessionState
}

function Handle-SelfJoin {
    Param([string]$RawLine)
    if(-not (Is-VRChatRunning)){
        Write-AppLog 'Ignored self join while VRChat is not running.'
        return
    }
    $now = [DateTime]::UtcNow
    $reuseFallback = $false
    $elapsedSinceFallback = $null
    $lastJoinGap = $null
    $fallbackJoinCount = 0
    if($script:Session.Ready -and $script:Session.Source -eq 'OnPlayerJoined fallback'){
        $fallbackJoinCount = $script:Session.SeenPlayers.Count
        if($script:Session.SessionStartedAt){
            $elapsedSinceFallback = $now - $script:Session.SessionStartedAt
        }
        if($fallbackJoinCount -gt 0){
            $lastJoin = $script:Session.SessionLastJoinAt
            if(-not $lastJoin -and $script:Session.SeenPlayers.Count -gt 0){
                $lastJoin = ($script:Session.SeenPlayers.Values | Sort-Object -Descending | Select-Object -First 1)
            }
            if($lastJoin){ $lastJoinGap = $now - $lastJoin }
        }
        $withinGrace = $false
        if($elapsedSinceFallback){
            $withinGrace = $elapsedSinceFallback.TotalSeconds -lt $SessionFallbackGraceSeconds
        }
        $withinJoinGap = $false
        if($withinGrace){
            if($fallbackJoinCount -le 0){
                $withinJoinGap = $true
            } elseif($lastJoinGap){
                $withinJoinGap = $lastJoinGap.TotalSeconds -le $SessionFallbackMaxContinuationSeconds
            }
        }
        if($withinGrace -and $withinJoinGap){
            $reuseFallback = $true
            $script:Session.Source = 'OnJoinedRoom'
            $details = @()
            if($lastJoinGap){ $details += "last join gap $([Math]::Round([Math]::Max(0.0,$lastJoinGap.TotalSeconds),1))s" }
            elseif($fallbackJoinCount -gt 0){ $details += 'last join gap unknown' }
            if($fallbackJoinCount -gt 0){ $details += "tracked players $fallbackJoinCount" }
            $detailText = if($details){ ' (' + ($details -join '; ') + ')' } else { '' }
            Write-AppLog "Session $($script:Session.SessionId) confirmed by OnJoinedRoom.$detailText"
        }
    }
    if(-not $reuseFallback){
        $details = @()
        if($elapsedSinceFallback){
            $details += "after $([Math]::Round([Math]::Max(0.0,$elapsedSinceFallback.TotalSeconds),1))s"
        }
        if($fallbackJoinCount -gt 0){
            if($lastJoinGap){
                $details += "last join gap $([Math]::Round([Math]::Max(0.0,$lastJoinGap.TotalSeconds),1))s"
            } else {
                $details += 'last join gap unavailable'
            }
            $details += "tracked players $fallbackJoinCount"
        }
        if($script:Session.Ready -and $script:Session.Source -eq 'OnPlayerJoined fallback'){
            $detailText = if($details){ ' (' + ($details -join '; ') + ')' } else { '' }
            Write-AppLog "Session $($script:Session.SessionId) fallback expired$detailText; starting new session for OnJoinedRoom."
        }
        $pending = $script:Session.PendingRoom
        Reset-SessionState
        if($pending){ $script:Session.PendingRoom = $pending }
        Ensure-SessionReady 'OnJoinedRoom'
    }
    $parsedName = ''
    $parsedUser = ''
    $parsedPlaceholder = ''
    if($RawLine){
        $parsed = Parse-PlayerEventLine -Line $RawLine -EventToken 'OnJoinedRoom'
        if($parsed){
            $parsedName = Normalize-JoinName $parsed.name
            $parsedUser = ([string]$parsed.user_id).Trim()
            $parsedPlaceholder = Normalize-JoinName $parsed.placeholder
        }
    }
    if($parsedUser){
        $lower = $parsedUser.ToLowerInvariant()
        if(-not $script:Session.LocalUserId -or $script:Session.LocalUserId -ne $lower){
            $script:Session.LocalUserId = $lower
            Write-AppLog "Learned local userId from OnJoinedRoom event: $parsedUser"
        }
    }
    $displayName = if($parsedName){ $parsedName } elseif($parsedUser){ $parsedUser } else { 'You' }
    $placeholderLabel = if($parsedPlaceholder){ $parsedPlaceholder } else { 'Player' }
    if(($placeholderLabel).ToLowerInvariant() -eq 'you'){ $placeholderLabel = 'Player' }
    if($displayName.ToLowerInvariant() -eq 'you' -and $parsedName){ $displayName = $parsedName }
    $messageBase = $displayName
    if($placeholderLabel){
        if(-not $messageBase){ $messageBase = $placeholderLabel }
        else { $messageBase = "$messageBase($placeholderLabel)" }
    }
    if(-not $messageBase){ $messageBase = 'You' }
    $message = "$messageBase joined your instance."
    $key = "self:$($script:Session.SessionId)"
    Notify-All $key $AppName $message
    $script:Session.PendingSelfJoin = @{
        session_id = $script:Session.SessionId
        placeholder = $placeholderLabel
        timestamp = $now
    }
}

function Handle-PlayerJoin {
    Param(
        [string]$Name,
        [string]$UserId,
        [string]$RawLine,
        [string]$Placeholder
    )
    if(-not (Is-VRChatRunning)){
        Write-AppLog 'Ignored player join while VRChat is not running.'
        return
    }
    if(-not $script:Session.Ready){ Ensure-SessionReady 'OnPlayerJoined fallback' }
    if(-not $script:Session.Ready){ return }
    $eventTime = [DateTime]::UtcNow
    $script:Session.SessionLastJoinAt = $eventTime
    $script:Session.SessionLastJoinRaw = $RawLine
    $cleanName = Normalize-JoinName $Name
    $originalName = $cleanName
    $cleanPlaceholder = Normalize-JoinName $Placeholder
    $cleanUser = ([string]$UserId).Trim()
    $userKey = if($cleanUser){ $cleanUser.ToLowerInvariant() } else { '' }
    if($userKey -and $script:Session.LocalUserId -and $userKey -eq $script:Session.LocalUserId){
        Write-AppLog "Skipping join for known local userId '$cleanUser'."
        $script:Session.PendingSelfJoin = $null
        return
    }
    $pendingSelf = $script:Session.PendingSelfJoin
    if($pendingSelf -and $pendingSelf.session_id -eq $script:Session.SessionId){
        $pendingPlaceholder = Normalize-JoinName ([string]$pendingSelf.placeholder)
        $pendingLower = if($pendingPlaceholder){ $pendingPlaceholder.ToLowerInvariant() } else { '' }
        $eventPlaceholderLower = if($cleanPlaceholder){ $cleanPlaceholder.ToLowerInvariant() } else { '' }
        if(-not $eventPlaceholderLower -and (Is-PlaceholderName $originalName)){
            $eventPlaceholderLower = $originalName.ToLowerInvariant()
        }
        $timestamp = $pendingSelf.timestamp
        $ageOk = $false
        if($timestamp -and $timestamp -is [DateTime]){
            $ageOk = ($eventTime - $timestamp).TotalSeconds -lt 10
        }
        if(
            $ageOk -and $pendingLower -in @('player','you') -and (
                $pendingLower -eq $eventPlaceholderLower -or (
                    -not $eventPlaceholderLower -and (Is-PlaceholderName $originalName)
                )
            )
        ){
            if($userKey -and -not $script:Session.LocalUserId){
                $script:Session.LocalUserId = $userKey
            }
            $script:Session.PendingSelfJoin = $null
            Write-AppLog 'Skipping join matched pending self event.'
            return
        }
    }
    $wasPlaceholder = Is-PlaceholderName $cleanName
    $isFallback = $script:Session.Source -eq 'OnPlayerJoined fallback'
    if($wasPlaceholder -and $userKey){
        if($isFallback -and -not $script:Session.LocalUserId){
            $script:Session.LocalUserId = $userKey
            Write-AppLog "Learned local userId from join event: $cleanUser"
            Write-AppLog "Skipping initial local join placeholder for userId '$cleanUser'."
            $script:Session.PendingSelfJoin = $null
            return
        }
        if($script:Session.LocalUserId -and $script:Session.LocalUserId -eq $userKey){
            Write-AppLog "Skipping local join placeholder for userId '$cleanUser'."
            $script:Session.PendingSelfJoin = $null
            return
        }
    }
    if(-not $cleanName -and $cleanUser){
        $cleanName = $cleanUser
        $wasPlaceholder = $false
    } elseif($wasPlaceholder -and $cleanUser){
        $cleanName = $cleanUser
        $wasPlaceholder = $false
    }
    if(-not $cleanName){ $cleanName = 'Unknown VRChat user' }
    $keyBase = if($userKey){ $userKey } else { $cleanName.ToLowerInvariant() }
    $hashSuffix = ''
    if(-not $cleanUser -and $RawLine){ $hashSuffix = Get-ShortHash $RawLine }
    $joinKey = "join:$($script:Session.SessionId):$keyBase"
    if($hashSuffix){ $joinKey += ":$hashSuffix" }
    if($script:Session.SeenPlayers.ContainsKey($joinKey)){ return }
    $script:Session.SeenPlayers[$joinKey] = $eventTime
    $placeholderForMessage = $cleanPlaceholder
    if(-not $placeholderForMessage -and $wasPlaceholder){ $placeholderForMessage = $originalName }
    if(-not $placeholderForMessage){ $placeholderForMessage = 'Someone' }
    elseif($placeholderForMessage.ToLowerInvariant() -eq 'you'){ $placeholderForMessage = 'Player' }
    $messageName = $cleanName
    if($cleanName){ $messageName = "$cleanName($placeholderForMessage)" }
    else { $messageName = $placeholderForMessage }
    $desktopNotification = $true
    if($wasPlaceholder -and -not $cleanUser){
        $placeholderLower = ($placeholderForMessage).Trim().ToLowerInvariant()
        if($placeholderLower -eq 'a player'){ $desktopNotification = $false }
    }
    $message = "$messageName joined your instance."
    Notify-All $joinKey $AppName $message $desktopNotification
    $logLine = "Session $($script:Session.SessionId): player joined '$cleanName'"
    if($cleanUser){ $logLine += " ($cleanUser)" }
    $logLine += '.'
    Write-AppLog $logLine
}

function Handle-PlayerLeft {
    Param(
        [string]$Name,
        [string]$UserId,
        [string]$RawLine
    )
    $cleanName = Normalize-JoinName $Name
    $cleanUser = ([string]$UserId).Trim()
    $userKey = if($cleanUser){ $cleanUser.ToLowerInvariant() } else { '' }
    $isPlaceholder = Is-PlaceholderName $cleanName
    if($isPlaceholder -and $userKey){
        if(-not $script:Session.LocalUserId){
            $script:Session.LocalUserId = $userKey
            Write-AppLog "Learned local userId from leave event: $cleanUser"
        } elseif($script:Session.LocalUserId -eq $userKey){
            $cleanName = $cleanUser
            $isPlaceholder = $false
        }
    }
    if(-not $cleanName -and $cleanUser){
        $cleanName = $cleanUser
        $isPlaceholder = $false
    } elseif($isPlaceholder -and $cleanUser){
        $cleanName = $cleanUser
        $isPlaceholder = $false
    }
    if(-not $cleanName){ $cleanName = 'Unknown VRChat user' }
    $removedCount = 0
    if($userKey){
        $prefix = "join:$($script:Session.SessionId):$userKey"
        $keysToRemove = @()
        foreach($key in $script:Session.SeenPlayers.Keys){
            if($key.StartsWith($prefix)){ $keysToRemove += $key }
        }
        foreach($key in $keysToRemove){
            $script:Session.SeenPlayers.Remove($key) | Out-Null
            $removedCount++
        }
    }
    $logLine = "Session $($script:Session.SessionId): player left '$cleanName'"
    if($cleanUser){ $logLine += " ($cleanUser)" }
    if($removedCount){ $logLine += ' [cleared join tracking]' }
    $logLine += '.'
    Write-AppLog $logLine
}

function Send-DesktopNotification {
    Param([string]$Title, [string]$Message)
    if(-not $script:TrayIcon){ return }
    $notifyAction = {
        param($notifyTitle, $notifyMessage)
        if(-not $script:TrayIcon){ return }
        try {
            $script:TrayIcon.BalloonTipTitle = $notifyTitle
            $script:TrayIcon.BalloonTipText = $notifyMessage
            $script:TrayIcon.BalloonTipIcon = [System.Windows.Forms.ToolTipIcon]::Info
            $script:TrayIcon.ShowBalloonTip(5000)
        } catch {}
    }.GetNewClosure()
    Invoke-UIThread -Action $notifyAction -Arguments @($Title, $Message)
}

function Send-PushoverNotification {
    Param([string]$Title, [string]$Message)
    $token = ([string]$script:Config.PushoverToken).Trim()
    $user = ([string]$script:Config.PushoverUser).Trim()
    if([string]::IsNullOrEmpty($token) -or [string]::IsNullOrEmpty($user)){ return }
    try {
        $payload = @{ token = $token; user = $user; title = $Title; message = $Message; priority = '0' }
        $state = [pscustomobject]@{ Body = $payload; Uri = $POUrl }
        $callback = [System.Action[object]]{
            Param($state)
            try {
                $client = New-Object System.Net.Http.HttpClient
                try {
                    $content = New-Object System.Net.Http.FormUrlEncodedContent($state.Body)
                    $response = $client.PostAsync($state.Uri, $content).GetAwaiter().GetResult()
                    $raw = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
                    try {
                        $json = $raw | ConvertFrom-Json -ErrorAction Stop
                        $status = if($json.PSObject.Properties['status']){ $json.status } else { '?' }
                        Write-AppLog "Pushover sent: $status"
                    } catch {
                        Write-AppLog "Pushover response: $raw"
                    }
                } finally {
                    $client.Dispose()
                }
            } catch {
                Write-AppLog "Pushover failed: $($_.Exception.Message)"
            }
        }
        [System.Threading.Tasks.Task]::Factory.StartNew($callback, $state) | Out-Null
    } catch {
        Write-AppLog "Failed to queue Pushover request: $($_.Exception.Message)"
    }
}
function Process-LogLine {
    Param([string]$Line)
    if([string]::IsNullOrWhiteSpace($Line)){ return }
    $safe = Strip-ZeroWidth($Line).Replace('||','|').Trim()
    if(-not $safe){ return }
    $lower = $safe.ToLowerInvariant()
    if($lower.Contains('onleftroom')){
        Enqueue-Event 'room_left' $safe
        return
    }
    $roomEvent = Parse-RoomTransitionLine $safe
    if($roomEvent){
        Enqueue-Event 'room_enter' $roomEvent
        return
    }
    if([regex]::IsMatch($safe, '(?i)\[Behaviour\].*OnJoinedRoom\b')){
        Enqueue-Event 'self_join' $safe
        return
    }
    if([regex]::IsMatch($safe, '(?i)\[Behaviour\].*OnPlayerLeft\b')){
        $parsed = Parse-PlayerEventLine -Line $safe -EventToken 'OnPlayerLeft'
        if(-not $parsed){
            $parsed = [pscustomobject]@{ name=''; user_id=''; placeholder=''; raw_line=$safe }
        }
        Enqueue-Event 'player_left' $parsed
        return
    }
    if([regex]::IsMatch($safe, '(?i)\[Behaviour\].*OnPlayerJoined\b')){
        $parsed = Parse-PlayerEventLine -Line $safe -EventToken 'OnPlayerJoined'
        if(-not $parsed){
            $parsed = [pscustomobject]@{ name=''; user_id=''; placeholder=''; raw_line=$safe }
        }
        Enqueue-Event 'player_join' $parsed
        return
    }
}

function Follow-LogFile {
    Param(
        [string]$Path,
        [string]$LogDir,
        $Token
    )
    $normalized = [System.IO.Path]::GetFullPath($Path)
    Enqueue-Event 'log_switch' $normalized
    Enqueue-Event 'monitor_status' "Watching $normalized"
    try {
        $lastSize = 0
        try {
            $lastSize = (Get-Item -LiteralPath $normalized -ErrorAction Stop).Length
        } catch {
            $lastSize = 0
        }
        $fileMode = [System.IO.FileMode]::Open
        $fileAccess = [System.IO.FileAccess]::Read
        $fileShare = [System.IO.FileShare]::ReadWrite
        while(-not $Token.IsCancellationRequested){
            try {
                $fs = New-Object System.IO.FileStream($normalized, $fileMode, $fileAccess, $fileShare)
                try {
                    $reader = New-Object System.IO.StreamReader($fs, [System.Text.Encoding]::UTF8, $true)
                    $reader.BaseStream.Seek($lastSize, [System.IO.SeekOrigin]::Begin) | Out-Null
                    while(-not $Token.IsCancellationRequested){
                        $position = $reader.BaseStream.Position
                        $line = $reader.ReadLine()
                        if($line -ne $null){
                            $lastSize = $reader.BaseStream.Position
                            Process-LogLine $line
                            continue
                        }
                        if($Token.WaitHandle.WaitOne(600)){
                            return
                        }
                        try {
                            $currentSize = (Get-Item -LiteralPath $normalized -ErrorAction Stop).Length
                        } catch {
                            Start-Sleep -Milliseconds 600
                            break
                        }
                        if($currentSize -lt $lastSize){
                            $reader.DiscardBufferedData()
                            $reader.BaseStream.Seek(0, [System.IO.SeekOrigin]::Begin) | Out-Null
                            $lastSize = 0
                            continue
                        }
                        $reader.BaseStream.Seek($position, [System.IO.SeekOrigin]::Begin) | Out-Null
                        $newest = Get-NewestLogPath $LogDir
                        if($newest -and [System.IO.Path]::GetFullPath($newest) -ne $normalized){
                            return
                        }
                    }
                } finally {
                    $reader.Dispose()
                }
            } catch {
                Write-AppLog "Failed reading log '$normalized': $($_.Exception.Message)"
                Enqueue-Event 'error' "Log read error: $($_.Exception.Message)"
                if($Token.WaitHandle.WaitOne(2000)){ return }
            } finally {
                if($fs){ $fs.Dispose() }
            }
        }
    } finally {
        Enqueue-Event 'monitor_status' 'Stopped'
    }
}

function Monitor-Loop {
    Param($Token)
    try {
        Enqueue-Event 'monitor_status' 'Running'
        $lastDirWarning = [DateTime]::UtcNow.AddSeconds(-60)
        $lastNoFileWarning = [DateTime]::UtcNow.AddSeconds(-60)
        while(-not $Token.IsCancellationRequested){
            $logDir = $script:Config.VRChatLogDir
            if([string]::IsNullOrWhiteSpace($logDir) -or -not (Test-Path $logDir -PathType Container)){
                if(([DateTime]::UtcNow - $lastDirWarning).TotalSeconds -gt 10){
                    Enqueue-Event 'status' "Waiting for VRChat log directory at $logDir"
                    $lastDirWarning = [DateTime]::UtcNow
                }
                if($Token.WaitHandle.WaitOne(1000)){ break }
                continue
            }
            $newest = Get-NewestLogPath $logDir
            if(-not $newest){
                if(([DateTime]::UtcNow - $lastNoFileWarning).TotalSeconds -gt 10){
                    Enqueue-Event 'status' "No log files found in $logDir"
                    $lastNoFileWarning = [DateTime]::UtcNow
                }
                if($Token.WaitHandle.WaitOne(1000)){ break }
                continue
            }
            Follow-LogFile $newest $logDir $Token
        }
    } catch {
        Write-AppLog "Monitor loop error: $($_.Exception.Message)"
        Enqueue-Event 'error' "Monitor error: $($_.Exception.Message)"
    } finally {
        Enqueue-Event 'monitor_status' 'Stopped'
    }
}

function Get-SessionDescription {
    if($script:Session.Ready){
        $source = if($script:Session.Source){ $script:Session.Source } else { 'unknown' }
        return "Session $($script:Session.SessionId) – $source"
    }
    return 'No active session'
}

function Update-TrayState {
    $monitoring = ($script:MonitorThread -and $script:MonitorThread.IsAlive)
    $statusText = if($monitoring){ 'Monitoring' } else { 'Stopped' }
    $tooltip = "$AppName – $statusText"
    if($script:Session.Ready){
        $tooltip += "`n" + (Get-SessionDescription)
    }
    if($script:TrayIcon){
        try {
            $text = if($tooltip.Length -gt 63){ $tooltip.Substring(0,63) } else { $tooltip }
            $script:TrayIcon.Text = $text
        } catch {}
    }
}

function Set-Status {
    Param([string]$Text)
    if($script:Controls.Status){
        $script:Controls.Status.Text = $Text
        Update-StatusLabelWidths
    }
}

function Set-MonitorStatus {
    Param([string]$Text)
    if($script:Controls.MonitorStatus){
        $script:Controls.MonitorStatus.Text = $Text
        Update-StatusLabelWidths
    }
}

function Set-SessionLabel {
    if($script:Controls.Session){
        $script:Controls.Session.Text = Get-SessionDescription
        Update-StatusLabelWidths
    }
}

function Set-LastEvent {
    Param([string]$Text)
    if($script:Controls.LastEvent){
        $script:Controls.LastEvent.Text = $Text
        Update-StatusLabelWidths
    }
}

function Set-CurrentLog {
    Param([string]$Path)
    if($script:Controls.CurrentLog){
        $script:Controls.CurrentLog.Text = if($Path){ $Path } else { '(none)' }
        Update-StatusLabelWidths
    }
}

function Handle-Event {
    Param([object[]]$Event)
    if(-not $Event){ return }
    $etype = [string]$Event[0]
    switch($etype){
        'status' {
            Set-Status ([string]$Event[1])
        }
        'error' {
            Set-Status ([string]$Event[1])
        }
        'monitor_status' {
            Set-MonitorStatus ([string]$Event[1])
        }
        'log_switch' {
            $path = [string]$Event[1]
            Set-CurrentLog $path
            Handle-LogSwitch $path
            Set-SessionLabel
            Set-LastEvent 'Switched to new log file.'
        }
        'room_enter' {
            $info = $Event[1]
            Handle-RoomEnter $info
            $desc = $info.world
            if($info.world -and $info.instance){ $desc = "$($info.world):$($info.instance)" }
            if(-not $desc){ $desc = $info.raw_line }
            if(-not $desc){ $desc = '(unknown room)' }
            Set-LastEvent "Room transition detected: $desc"
        }
        'room_left' {
            Handle-RoomLeft
            Set-SessionLabel
            Set-LastEvent 'Left current room.'
        }
        'self_join' {
            Handle-SelfJoin ([string]$Event[1])
            Set-SessionLabel
            Set-LastEvent 'OnJoinedRoom detected.'
        }
        'player_join' {
            $info = $Event[1]
            Handle-PlayerJoin $info.name $info.user_id $info.raw_line $info.placeholder
            Set-SessionLabel
            $display = if($info.name){ $info.name } elseif($info.user_id){ $info.user_id } else { 'Unknown VRChat user' }
            Set-LastEvent "Player joined: $display"
        }
        'player_left' {
            $info = $Event[1]
            Handle-PlayerLeft $info.name $info.user_id $info.raw_line
            Set-SessionLabel
            $display = if($info.name){ $info.name } elseif($info.user_id){ $info.user_id } else { 'Unknown VRChat user' }
            Set-LastEvent "Player left: $display"
        }
    }
    Update-TrayState
}

function Process-Events {
    while($true){
        $event = $null
        if(-not $script:EventQueue.TryDequeue([ref]$event)){ break }
        Handle-Event $event
    }
}

function Update-ConfigFromForm {
    if(-not $script:Controls){ return }
    if($script:Controls.Install){ $script:Config.InstallDir = Expand-PathSafe $script:Controls.Install.Text }
    if($script:Controls.LogDir){ $script:Config.VRChatLogDir = Expand-PathSafe $script:Controls.LogDir.Text }
    if($script:Controls.POUser){ $script:Config.PushoverUser = $script:Controls.POUser.Text.Trim() }
    if($script:Controls.POToken){ $script:Config.PushoverToken = $script:Controls.POToken.Text.Trim() }
}
function Start-Monitoring {
    if($script:MonitorThread -and $script:MonitorThread.IsAlive){ return }
    Update-ConfigFromForm
    Ensure-Dir $script:Config.InstallDir
    $script:MonitorTokenSource = New-Object System.Threading.CancellationTokenSource
    $token = $script:MonitorTokenSource.Token
    $start = New-Object System.Threading.ParameterizedThreadStart({ param($ct) Monitor-Loop $ct })
    $script:MonitorThread = New-Object System.Threading.Thread($start)
    $script:MonitorThread.IsBackground = $true
    $script:MonitorThread.Start($token)
    Set-MonitorStatus 'Starting...'
    Write-AppLog 'Monitoring started.'
    Update-TrayState
}

function Stop-Monitoring {
    if(-not $script:MonitorThread){ return }
    try {
        if($script:MonitorTokenSource){ $script:MonitorTokenSource.Cancel() }
        if($script:MonitorThread.IsAlive){ $script:MonitorThread.Join(3000) | Out-Null }
    } catch {}
    $script:MonitorThread = $null
    $script:MonitorTokenSource = $null
    Set-MonitorStatus 'Stopped'
    Update-TrayState
    Write-AppLog 'Monitoring stopped.'
}

function Restart-Monitoring {
    Stop-Monitoring
    Start-Monitoring
}

function Get-LauncherPath {
    $arg0 = [Environment]::GetCommandLineArgs()[0]
    if($arg0 -and (Split-Path $arg0 -Leaf).ToLower().EndsWith('.exe')){ return $arg0 }
    if($PSCommandPath){ return $PSCommandPath }
    return $arg0
}

function Get-AppIcon {
    $candidates = @()
    $addCandidates = {
        param([string]$BasePath)
        if(-not $BasePath){ return }
        $candidates += Join-Path $BasePath $IconFileName
        $candidates += Join-Path (Join-Path $BasePath 'vrchat_join_notification') $IconFileName
        $candidates += Join-Path (Join-Path $BasePath 'src') $IconFileName
        $candidates += Join-Path (Join-Path (Join-Path $BasePath 'src') 'vrchat_join_notification') $IconFileName
    }
    if($PSScriptRoot){ & $addCandidates $PSScriptRoot }
    $launcherDir = Split-Path (Get-LauncherPath) -Parent
    if($launcherDir){ & $addCandidates $launcherDir }
    foreach($candidate in $candidates){
        if($candidate -and (Test-Path $candidate)){
            try { return New-Object System.Drawing.Icon($candidate) } catch {}
        }
    }
    return [System.Drawing.SystemIcons]::Information
}

function Get-StartupShortcutPath {
    $startup = [Environment]::GetFolderPath('Startup')
    return Join-Path $startup 'VRChat Join Notification with Pushover.lnk'
}

function Update-StartupButtons {
    $exists = Test-Path (Get-StartupShortcutPath)
    if($script:Controls.AddStartup){ $script:Controls.AddStartup.Enabled = -not $exists }
    if($script:Controls.RemoveStartup){ $script:Controls.RemoveStartup.Enabled = $exists }
}

function Add-ToStartup {
    try {
        $shortcutPath = Get-StartupShortcutPath
        $shell = New-Object -ComObject WScript.Shell
        $shortcut = $shell.CreateShortcut($shortcutPath)
        $launcher = Get-LauncherPath
        $shortcut.TargetPath = $launcher
        $shortcut.WorkingDirectory = Split-Path $launcher -Parent
        $shortcut.IconLocation = "$launcher,0"
        $shortcut.Save()
        Set-Status 'Added to startup.'
        Write-AppLog "Startup entry created at $shortcutPath"
        Send-DesktopNotification $AppName 'Added to Windows startup.'
    } catch {
        $msg = "Failed to add to startup: $($_.Exception.Message)"
        Set-Status $msg
        Write-AppLog $msg
        Send-DesktopNotification $AppName $msg
    } finally {
        Update-StartupButtons
    }
}

function Remove-FromStartup {
    try {
        $shortcutPath = Get-StartupShortcutPath
        if(Test-Path $shortcutPath){ Remove-Item $shortcutPath -Force }
        Set-Status 'Removed from startup.'
        Write-AppLog "Startup entry removed from $shortcutPath"
        Send-DesktopNotification $AppName 'Removed from Windows startup.'
    } catch {
        $msg = "Failed to remove from startup: $($_.Exception.Message)"
        Set-Status $msg
        Write-AppLog $msg
        Send-DesktopNotification $AppName $msg
    } finally {
        Update-StartupButtons
    }
}

function Save-Only {
    try {
        Update-ConfigFromForm
        Save-AppConfig $script:Config
        Set-Status 'Settings saved.'
    } catch {
        $msg = "Failed to save settings: $($_.Exception.Message)"
        Set-Status $msg
        Write-AppLog $msg
    }
}

function Save-And-Restart {
    try {
        Update-ConfigFromForm
        Save-AppConfig $script:Config
        Set-Status 'Settings saved. Restarting monitor...'
        Restart-Monitoring
    } catch {
        $msg = "Failed to save settings: $($_.Exception.Message)"
        Set-Status $msg
        Write-AppLog $msg
    }
}

function Quit-App {
    if($script:IsQuitting){ return }
    $script:IsQuitting = $true
    Stop-Monitoring
    if($script:TrayIcon){
        try {
            $script:TrayIcon.Visible = $false
            $script:TrayIcon.Dispose()
        } catch {}
        $script:TrayIcon = $null
    }
    [System.Windows.Forms.Application]::Exit()
}

function Show-Window {
    if($script:Controls.Form){
        $form = $script:Controls.Form
        $form.Show()
        $form.WindowState = [System.Windows.Forms.FormWindowState]::Normal
        $form.Activate()
    }
}

function Hide-Window {
    if($script:Controls.Form -and -not $script:IsQuitting){
        $script:Controls.Form.Hide()
        Set-Status 'Settings window hidden. Use the tray icon to reopen or quit.'
    }
}

function Update-StatusLabelWidths {
    if(-not $script:Controls.Form){ return }
    $labels = $script:Controls.StatusLabels
    if(-not $labels){ return }

    $available = $null
    if($script:Controls.StatusTable){
        try {
            $columnWidths = $script:Controls.StatusTable.GetColumnWidths()
            if($columnWidths.Length -gt 1){
                $available = $columnWidths[1] - 6
            }
        } catch {}
    }

    if(-not $available -or $available -lt 120){
        $form = $script:Controls.Form
        $layoutPadding = 0
        if($script:Controls.Layout){
            $layoutPadding = $script:Controls.Layout.Padding.Left + $script:Controls.Layout.Padding.Right
        }
        $groupPadding = 0
        if($script:Controls.StatusGroup){
            $groupPadding = $script:Controls.StatusGroup.Padding.Left + $script:Controls.StatusGroup.Padding.Right
        }
        $available = [Math]::Max(120, $form.ClientSize.Width - $layoutPadding - $groupPadding - 180)
    }

    foreach($label in $labels){
        if($label){
            $label.MaximumSize = New-Object System.Drawing.Size($available, 0)
        }
    }
}

function Build-UI {
    Param([pscustomobject]$Config)
    $form = New-Object System.Windows.Forms.Form
    $form.Text = "$AppName (Windows)"
    $form.StartPosition = 'CenterScreen'
    $form.AutoScaleMode = [System.Windows.Forms.AutoScaleMode]::Font
    $form.AutoScaleDimensions = New-Object System.Drawing.SizeF(96, 96)
    $form.ClientSize = New-Object System.Drawing.Size(620, 320)

    $layout = New-Object System.Windows.Forms.TableLayoutPanel
    $layout.Dock = 'Fill'
    $layout.Margin = New-Object System.Windows.Forms.Padding(0)
    $layout.Padding = New-Object System.Windows.Forms.Padding(12, 12, 12, 10)
    $layout.ColumnCount = 4
    $layout.RowCount = 6
    [void]$layout.ColumnStyles.Add((New-Object System.Windows.Forms.ColumnStyle([System.Windows.Forms.SizeType]::AutoSize)))
    [void]$layout.ColumnStyles.Add((New-Object System.Windows.Forms.ColumnStyle([System.Windows.Forms.SizeType]::Percent, 50)))
    [void]$layout.ColumnStyles.Add((New-Object System.Windows.Forms.ColumnStyle([System.Windows.Forms.SizeType]::Percent, 50)))
    [void]$layout.ColumnStyles.Add((New-Object System.Windows.Forms.ColumnStyle([System.Windows.Forms.SizeType]::AutoSize)))
    foreach($i in 0..4){
        [void]$layout.RowStyles.Add((New-Object System.Windows.Forms.RowStyle([System.Windows.Forms.SizeType]::AutoSize)))
    }
    [void]$layout.RowStyles.Add((New-Object System.Windows.Forms.RowStyle([System.Windows.Forms.SizeType]::Percent, 100)))

    $labelInstall = New-Object System.Windows.Forms.Label
    $labelInstall.Text = 'Install Folder (logs/cache):'
    $labelInstall.AutoSize = $true
    $labelInstall.Margin = New-Object System.Windows.Forms.Padding(0, 0, 8, 2)
    $layout.Controls.Add($labelInstall, 0, 0)

    $installBox = New-Object System.Windows.Forms.TextBox
    $installBox.Text = $Config.InstallDir
    $installBox.Dock = 'Fill'
    $installBox.Margin = New-Object System.Windows.Forms.Padding(0, 0, 4, 2)
    $layout.Controls.Add($installBox, 1, 0)
    [void]$layout.SetColumnSpan($installBox, 2)

    $browseInstall = New-Object System.Windows.Forms.Button
    $browseInstall.Text = 'Browse…'
    $browseInstall.AutoSize = $true
    $browseInstall.Margin = New-Object System.Windows.Forms.Padding(4, 0, 0, 2)
    $layout.Controls.Add($browseInstall, 3, 0)
    $browseInstall.Add_Click({
        $dialog = New-Object System.Windows.Forms.FolderBrowserDialog
        $dialog.SelectedPath = $installBox.Text
        if($dialog.ShowDialog() -eq 'OK'){
            $installBox.Text = $dialog.SelectedPath
        }
    })

    $labelLog = New-Object System.Windows.Forms.Label
    $labelLog.Text = 'VRChat Log Folder:'
    $labelLog.AutoSize = $true
    $labelLog.Margin = New-Object System.Windows.Forms.Padding(0, 6, 8, 2)
    $layout.Controls.Add($labelLog, 0, 1)

    $logBox = New-Object System.Windows.Forms.TextBox
    $logBox.Text = $Config.VRChatLogDir
    $logBox.Dock = 'Fill'
    $logBox.Margin = New-Object System.Windows.Forms.Padding(0, 0, 4, 2)
    $layout.Controls.Add($logBox, 1, 1)
    [void]$layout.SetColumnSpan($logBox, 2)

    $browseLog = New-Object System.Windows.Forms.Button
    $browseLog.Text = 'Browse…'
    $browseLog.AutoSize = $true
    $browseLog.Margin = New-Object System.Windows.Forms.Padding(4, 0, 0, 2)
    $layout.Controls.Add($browseLog, 3, 1)
    $browseLog.Add_Click({
        $dialog = New-Object System.Windows.Forms.FolderBrowserDialog
        $dialog.SelectedPath = $logBox.Text
        if($dialog.ShowDialog() -eq 'OK'){
            $logBox.Text = $dialog.SelectedPath
        }
    })

    $labelPO = New-Object System.Windows.Forms.Label
    $labelPO.Text = 'Pushover Credentials:'
    $labelPO.AutoSize = $true
    $labelPO.Margin = New-Object System.Windows.Forms.Padding(0, 10, 8, 2)
    $layout.Controls.Add($labelPO, 0, 2)

    $poPanel = New-Object System.Windows.Forms.TableLayoutPanel
    $poPanel.ColumnCount = 4
    $poPanel.Dock = 'Fill'
    $poPanel.Margin = New-Object System.Windows.Forms.Padding(0, 4, 0, 2)
    [void]$poPanel.ColumnStyles.Add((New-Object System.Windows.Forms.ColumnStyle([System.Windows.Forms.SizeType]::AutoSize)))
    [void]$poPanel.ColumnStyles.Add((New-Object System.Windows.Forms.ColumnStyle([System.Windows.Forms.SizeType]::Percent, 50)))
    [void]$poPanel.ColumnStyles.Add((New-Object System.Windows.Forms.ColumnStyle([System.Windows.Forms.SizeType]::AutoSize)))
    [void]$poPanel.ColumnStyles.Add((New-Object System.Windows.Forms.ColumnStyle([System.Windows.Forms.SizeType]::Percent, 50)))

    $poUserLabel = New-Object System.Windows.Forms.Label
    $poUserLabel.Text = 'User Key:'
    $poUserLabel.AutoSize = $true
    $poUserLabel.Margin = New-Object System.Windows.Forms.Padding(0, 0, 6, 0)
    $poPanel.Controls.Add($poUserLabel, 0, 0)

    $poUserBox = New-Object System.Windows.Forms.TextBox
    $poUserBox.Text = $Config.PushoverUser
    $poUserBox.UseSystemPasswordChar = $true
    $poUserBox.Dock = 'Fill'
    $poUserBox.Margin = New-Object System.Windows.Forms.Padding(0, 0, 8, 0)
    $poPanel.Controls.Add($poUserBox, 1, 0)

    $poTokenLabel = New-Object System.Windows.Forms.Label
    $poTokenLabel.Text = 'API Token:'
    $poTokenLabel.AutoSize = $true
    $poTokenLabel.Margin = New-Object System.Windows.Forms.Padding(8, 0, 6, 0)
    $poPanel.Controls.Add($poTokenLabel, 2, 0)

    $poTokenBox = New-Object System.Windows.Forms.TextBox
    $poTokenBox.Text = $Config.PushoverToken
    $poTokenBox.UseSystemPasswordChar = $true
    $poTokenBox.Dock = 'Fill'
    $poTokenBox.Margin = New-Object System.Windows.Forms.Padding(0, 0, 0, 0)
    $poPanel.Controls.Add($poTokenBox, 3, 0)

    $layout.Controls.Add($poPanel, 1, 2)
    [void]$layout.SetColumnSpan($poPanel, 3)

    $buttonPanel = New-Object System.Windows.Forms.TableLayoutPanel
    $buttonPanel.ColumnCount = 3
    $buttonPanel.Dock = 'Fill'
    $buttonPanel.AutoSize = $true
    $buttonPanel.AutoSizeMode = 'GrowAndShrink'
    $buttonPanel.Margin = New-Object System.Windows.Forms.Padding(0, 12, 0, 0)
    $buttonPanel.Padding = New-Object System.Windows.Forms.Padding(0)
    $buttonPanel.RowCount = 1
    [void]$buttonPanel.RowStyles.Add((New-Object System.Windows.Forms.RowStyle([System.Windows.Forms.SizeType]::AutoSize)))
    foreach($i in 0..2){ [void]$buttonPanel.ColumnStyles.Add((New-Object System.Windows.Forms.ColumnStyle([System.Windows.Forms.SizeType]::Percent, 33.3333))) }

    $startBtn = New-Object System.Windows.Forms.Button
    $startBtn.Text = 'Start Monitoring'
    $startBtn.Dock = 'Fill'
    $startBtn.Margin = New-Object System.Windows.Forms.Padding(3, 0, 3, 0)
    $buttonPanel.Controls.Add($startBtn, 0, 0)
    $startBtn.Add_Click({ Start-Monitoring })

    $stopBtn = New-Object System.Windows.Forms.Button
    $stopBtn.Text = 'Stop Monitoring'
    $stopBtn.Dock = 'Fill'
    $stopBtn.Margin = New-Object System.Windows.Forms.Padding(3, 0, 3, 0)
    $buttonPanel.Controls.Add($stopBtn, 1, 0)
    $stopBtn.Add_Click({ Stop-Monitoring })

    $saveRestart = New-Object System.Windows.Forms.Button
    $saveRestart.Text = 'Save && Restart Monitoring'
    $saveRestart.Dock = 'Fill'
    $saveRestart.Margin = New-Object System.Windows.Forms.Padding(3, 0, 3, 0)
    $buttonPanel.Controls.Add($saveRestart, 2, 0)
    $saveRestart.Add_Click({ Save-And-Restart })

    $layout.Controls.Add($buttonPanel, 0, 3)
    [void]$layout.SetColumnSpan($buttonPanel, 4)

    $extraPanel = New-Object System.Windows.Forms.TableLayoutPanel
    $extraPanel.ColumnCount = 4
    $extraPanel.Dock = 'Fill'
    $extraPanel.AutoSize = $true
    $extraPanel.AutoSizeMode = 'GrowAndShrink'
    $extraPanel.Margin = New-Object System.Windows.Forms.Padding(0, 8, 0, 0)
    $extraPanel.RowCount = 1
    [void]$extraPanel.RowStyles.Add((New-Object System.Windows.Forms.RowStyle([System.Windows.Forms.SizeType]::AutoSize)))
    foreach($i in 0..3){ [void]$extraPanel.ColumnStyles.Add((New-Object System.Windows.Forms.ColumnStyle([System.Windows.Forms.SizeType]::Percent, 25))) }

    $addStartup = New-Object System.Windows.Forms.Button
    $addStartup.Text = 'Add to Startup'
    $addStartup.Dock = 'Fill'
    $addStartup.Margin = New-Object System.Windows.Forms.Padding(3, 0, 3, 0)
    $extraPanel.Controls.Add($addStartup, 0, 0)
    $addStartup.Add_Click({ Add-ToStartup })

    $removeStartup = New-Object System.Windows.Forms.Button
    $removeStartup.Text = 'Remove from Startup'
    $removeStartup.Dock = 'Fill'
    $removeStartup.Margin = New-Object System.Windows.Forms.Padding(3, 0, 3, 0)
    $extraPanel.Controls.Add($removeStartup, 1, 0)
    $removeStartup.Add_Click({ Remove-FromStartup })

    $saveBtn = New-Object System.Windows.Forms.Button
    $saveBtn.Text = 'Save'
    $saveBtn.Dock = 'Fill'
    $saveBtn.Margin = New-Object System.Windows.Forms.Padding(3, 0, 3, 0)
    $extraPanel.Controls.Add($saveBtn, 2, 0)
    $saveBtn.Add_Click({ Save-Only })

    $quitBtn = New-Object System.Windows.Forms.Button
    $quitBtn.Text = 'Quit'
    $quitBtn.Dock = 'Fill'
    $quitBtn.Margin = New-Object System.Windows.Forms.Padding(3, 0, 3, 0)
    $extraPanel.Controls.Add($quitBtn, 3, 0)
    $quitBtn.Add_Click({ Quit-App })

    $layout.Controls.Add($extraPanel, 0, 4)
    [void]$layout.SetColumnSpan($extraPanel, 4)

    $statusGroup = New-Object System.Windows.Forms.GroupBox
    $statusGroup.Text = 'Status'
    $statusGroup.Dock = 'Fill'
    $statusGroup.Margin = New-Object System.Windows.Forms.Padding(0, 12, 0, 0)
    $statusGroup.Padding = New-Object System.Windows.Forms.Padding(10, 10, 10, 10)

    $statusTable = New-Object System.Windows.Forms.TableLayoutPanel
    $statusTable.Dock = 'Fill'
    $statusTable.ColumnCount = 2
    $statusTable.AutoSize = $true
    $statusTable.AutoSizeMode = 'GrowAndShrink'
    $statusTable.Margin = New-Object System.Windows.Forms.Padding(0)
    $statusTable.Padding = New-Object System.Windows.Forms.Padding(0)
    [void]$statusTable.ColumnStyles.Add((New-Object System.Windows.Forms.ColumnStyle([System.Windows.Forms.SizeType]::AutoSize)))
    [void]$statusTable.ColumnStyles.Add((New-Object System.Windows.Forms.ColumnStyle([System.Windows.Forms.SizeType]::Percent, 100)))

    $addStatusRow = {
        param([System.Windows.Forms.TableLayoutPanel]$Table, [string]$LabelText)
        $row = $Table.RowCount
        $Table.RowCount++
        [void]$Table.RowStyles.Add((New-Object System.Windows.Forms.RowStyle([System.Windows.Forms.SizeType]::AutoSize)))
        $label = New-Object System.Windows.Forms.Label
        $label.Text = $LabelText
        $label.AutoSize = $true
        $label.Margin = New-Object System.Windows.Forms.Padding(0, 0, 6, 3)
        $Table.Controls.Add($label, 0, $row)
        $value = New-Object System.Windows.Forms.Label
        $value.Text = ''
        $value.AutoSize = $true
        $value.Margin = New-Object System.Windows.Forms.Padding(0, 0, 0, 3)
        $value.MaximumSize = New-Object System.Drawing.Size(520, 0)
        $Table.Controls.Add($value, 1, $row)
        return $value
    }

    $statusValue = & $addStatusRow $statusTable 'Status:'
    $monitorValue = & $addStatusRow $statusTable 'Monitoring:'
    $currentLogValue = & $addStatusRow $statusTable 'Current log:'
    $sessionValue = & $addStatusRow $statusTable 'Session:'
    $lastEventValue = & $addStatusRow $statusTable 'Last event:'

    $statusGroup.Controls.Add($statusTable)
    $layout.Controls.Add($statusGroup, 0, 5)
    [void]$layout.SetColumnSpan($statusGroup, 4)

    $form.Controls.Add($layout)
    $form.PerformLayout()

    $preferred = $layout.GetPreferredSize((New-Object System.Drawing.Size(0, 0)))
    if(-not $preferred.Width -or -not $preferred.Height){
        $preferred = $layout.PreferredSize
    }
    $initialWidth = [Math]::Max(620, $preferred.Width)
    $initialHeight = [Math]::Max(320, $preferred.Height)
    $form.ClientSize = New-Object System.Drawing.Size($initialWidth, $initialHeight)
    $form.MinimumSize = $form.Size

    $form.Add_FormClosing({ param($sender,$e)
        if(-not $script:IsQuitting){
            $e.Cancel = $true
            Hide-Window
        }
    })

    $form.Add_Resize({
        if($form.WindowState -eq [System.Windows.Forms.FormWindowState]::Minimized -and -not $script:IsQuitting){
            Hide-Window
        } else {
            Update-StatusLabelWidths
        }
    })

    $script:Controls = @{
        Form          = $form
        Install       = $installBox
        LogDir        = $logBox
        POUser        = $poUserBox
        POToken       = $poTokenBox
        Status        = $statusValue
        MonitorStatus = $monitorValue
        CurrentLog    = $currentLogValue
        Session       = $sessionValue
        LastEvent     = $lastEventValue
        AddStartup    = $addStartup
        RemoveStartup = $removeStartup
        Layout        = $layout
        StatusGroup   = $statusGroup
        StatusTable   = $statusTable
        StatusLabels  = @($statusValue, $monitorValue, $currentLogValue, $sessionValue, $lastEventValue)
    }

    Update-StatusLabelWidths

    $form.Add_Shown({ Update-StatusLabelWidths })

    $timer = New-Object System.Windows.Forms.Timer
    $timer.Interval = 200
    $timer.Add_Tick({ Process-Events })
    $timer.Start()

    return $form
}

function Build-Tray {
    $icon = Get-AppIcon
    $tray = New-Object System.Windows.Forms.NotifyIcon
    $tray.Icon = $icon
    $tray.Visible = $true
    $tray.Text = $AppName

    $menu = New-Object System.Windows.Forms.ContextMenuStrip
    $openItem = $menu.Items.Add('Open Settings')
    $openItem.Add_Click({ Show-Window })
    $menu.Items.Add('-') | Out-Null
    $startItem = $menu.Items.Add('Start Monitoring')
    $startItem.Add_Click({ Start-Monitoring })
    $stopItem = $menu.Items.Add('Stop Monitoring')
    $stopItem.Add_Click({ Stop-Monitoring })
    $restartItem = $menu.Items.Add('Save && Restart Monitoring')
    $restartItem.Add_Click({ Save-And-Restart })
    $menu.Items.Add('-') | Out-Null
    $quitItem = $menu.Items.Add('Quit')
    $quitItem.Add_Click({ Quit-App })

    $tray.ContextMenuStrip = $menu
    $tray.Add_DoubleClick({ Show-Window })

    $script:TrayIcon = $tray
}

function Apply-StartupState {
    if($script:LoadError){
        Set-Status $script:LoadError
        Show-Window
        return
    }
    if($script:Config.FirstRun){
        Set-Status 'Welcome! Configure your install and log folders, optionally add Pushover keys, then click Save & Restart Monitoring.'
        Show-Window
        return
    }
    if($script:Config.PushoverUser -and $script:Config.PushoverToken){
        Start-Monitoring
        if($OpenSettings){
            Show-Window
        } else {
            Hide-Window
        }
        return
    }
    Set-Status 'Optional: add your Pushover credentials, then click Save & Restart Monitoring when ready.'
    Show-Window
}

function Main {
    $result = Load-AppConfig
    $script:Config = $result[0]
    $script:LoadError = $result[1]
    $form = Build-UI $script:Config
    Update-StartupButtons
    Build-Tray
    Update-TrayState
    Apply-StartupState
    if($script:LoadError){
        [System.Windows.Forms.MessageBox]::Show($script:LoadError, $AppName, 'OK', 'Error') | Out-Null
    }
    [System.Windows.Forms.Application]::Run($form)
}

$mutexName = 'Global\\VRChatJoinNotificationWithPushover'
if(-not (Acquire-SingleInstance -Name $mutexName)){
    [System.Windows.Forms.MessageBox]::Show("$AppName is already running.", $AppName, 'OK', 'Information') | Out-Null
    Release-SingleInstance
    return
}

try {
    Main
} finally {
    Release-SingleInstance
}
