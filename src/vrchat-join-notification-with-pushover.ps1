#requires -Version 5.1
Param([switch]$open_settings)

<#
VRChat Join Notifier (fixed tray pulse + single Settings window + safe string ops)
- FIX: Tray pulse no longer throws "$frame/$t not set" after ps2exe (use $global: state only)
- FIX: Removed "-f" format operator to avoid "$f?" mis-parsing in some ps2exe builds
- Single Settings window (no duplicates)
- Tray "Restart Monitoring" animates icon (pulse) a few seconds and shows toast/balloon
- Tails newest VRChat log (output_log_*.txt or Player.log); auto-switches when a newer file appears
- Notifies once when YOU join (OnJoinedRoom) and once per OTHER join (OnPlayerJoined) in the same session
- Only while VRChat.exe is running
- Single-process (Mutex + named Event). Secondary launch signals "open settings" to primary and exits
- Windows Toast via BurntToast with balloon fallback
- Pushover push
#>

Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
[System.Windows.Forms.Application]::EnableVisualStyles()
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# ---------------- App constants ----------------
$AppName        = 'System Notification'
$ConfigFileName = 'config.json'
$AppLogName     = 'notifier.log'
$POUrl          = 'https://api.pushover.net/1/messages.json'

$EnDash = [char]0x2013
$EmDash = [char]0x2014
$JoinSeparatorChars = [char[]]@('-',':','|',$EnDash,$EmDash)
$JoinSeparatorString = -join $JoinSeparatorChars
$JoinSeparatorOnlyPattern = '^[\-:|\u2013\u2014]+$'

$DefaultInstallDir   = Join-Path $env:LOCALAPPDATA 'VRChatJoinNotificationWithPushover'
$DefaultVRChatLogDir = Join-Path ($env:LOCALAPPDATA -replace '\\Local$', '\LocalLow') 'VRChat\VRChat'

# ---------------- Cooldown (anti-spam) ----------------
$NotifyCooldownSeconds = 10
$SessionFallbackGraceSeconds = 30 # allow quick OnJoinedRoom confirmations to reuse fallback session
$SessionFallbackMaxContinuationSeconds = 4 # require OnJoinedRoom to arrive quickly after fallback joins to reuse
$script:LastNotified = @{}
$script:SessionId = 0         # increments on each detected session
$script:SeenPlayers = @{}     # join key -> first seen time (per session)
$script:LocalUserId = $null   # tracks the local player's userId when known (lowercase)
$script:SessionLastJoinAt = $null
$script:SessionReady = $false # true once current session started
$script:SessionSource = ''    # remember how the session started
$script:PendingRoom = $null   # upcoming room/world info (if detected)
$script:PendingSelfJoin = $null # pending self join metadata
$script:SessionStartedAt = $null

# ---------------- Globals ----------------
$global:Cfg = $null
$global:TrayIcon = $null
$global:TrayMenu = $null
$global:HostForm = $null
$global:FollowJob = $null

# Only-one Settings window
$global:SettingsForm = $null

# Tray pulse animation state (ALL GLOBAL to be safe under ps2exe)
$global:IconIdle    = $null
$global:IconPulseA  = $null
$global:IconPulseB  = $null
$global:PulseTimer  = $null
$global:PulseStopAt = Get-Date
$global:PulseFrame  = $false
$global:IdleTooltip = $AppName

# Single-instance control
$script:IsPrimary = $false
$script:Mutex     = $null
$script:OpenEvt   = $null

# Toast availability
$script:ToastReady = $false

# ---------------- Session helpers ----------------
function Reset-SessionState{
  $script:SessionReady = $false
  $script:SessionSource = ''
  $script:SeenPlayers = @{}
  $script:LocalUserId = $null
  $script:SessionStartedAt = $null
  $script:SessionLastJoinAt = $null
  $script:PendingRoom = $null
  $script:PendingSelfJoin = $null
}
function Ensure-SessionReady([string]$Reason){
  if($script:SessionReady){ return $false }
  if([string]::IsNullOrWhiteSpace($Reason)){ $Reason = 'unknown trigger' }
  $script:SessionId++
  $script:SessionReady = $true
  $script:SessionSource = $Reason
  $script:SeenPlayers = @{}
  $script:SessionStartedAt = Get-Date
  $script:SessionLastJoinAt = $null
  $roomDesc = $null
  if($script:PendingRoom){
    $pendingWorld = $script:PendingRoom.World
    $pendingInstance = $script:PendingRoom.Instance
    if(-not [string]::IsNullOrWhiteSpace($pendingWorld)){
      $roomDesc = $pendingWorld
      if(-not [string]::IsNullOrWhiteSpace($pendingInstance)){ $roomDesc += ":" + $pendingInstance }
    }
  }

  $logMessage = "Session {0} started ({1})" -f $script:SessionId,$Reason
  if($roomDesc){ $logMessage += " [" + $roomDesc + "]" }
  $logMessage += "."
  Write-AppLog $logMessage

  if($global:TrayIcon){
    $tip = $AppName
    if($roomDesc){ $tip = "$AppName $EmDash $roomDesc" }
    $global:IdleTooltip = $tip
    try{ $global:TrayIcon.Text = $tip }catch{}
  }
  return $true
}

# ---------------- Helpers ----------------
function Ensure-Dir($Path){ if(-not(Test-Path $Path)){ New-Item -ItemType Directory -Path $Path | Out-Null } }
function Get-LauncherPath{
  $arg0=[Environment]::GetCommandLineArgs()[0]
  if($arg0 -and (Split-Path $arg0 -Leaf).ToLower().EndsWith('.exe')){ return $arg0 }
  if($PSCommandPath){ return $PSCommandPath }
  return $arg0
}
function Write-AppLog($Message){
  try{
    if(-not $global:Cfg){ return }
    if([string]::IsNullOrWhiteSpace($global:Cfg.InstallDir)){ return }
    Ensure-Dir $global:Cfg.InstallDir
    $stamp=(Get-Date).ToString('yyyy-MM-dd HH:mm:ss')
    Add-Content -Path (Join-Path $global:Cfg.InstallDir $AppLogName) -Value ("[$stamp] " + $Message)
  }catch{}
}
function Normalize-JoinName([string]$Name){
  if([string]::IsNullOrWhiteSpace($Name)){ return $null }
  $clean = [regex]::Replace($Name,'[\u200B-\u200D\uFEFF]','')
  $clean = $clean.Trim()
  $clean = $clean -replace '\u3000',' '
  $clean = $clean.Trim([char]34).Trim([char]39).Trim()
  $clean = $clean -replace '\|\|','|'
  if([string]::IsNullOrWhiteSpace($clean)){ return $null }
  if($clean.Length -gt 160){ $clean = $clean.Substring(0,160).Trim() }
  if($clean -match $JoinSeparatorOnlyPattern){ return $null }
  return $clean
}
function Is-PlaceholderName([string]$Name){
  if([string]::IsNullOrWhiteSpace($Name)){ return $true }
  $trimmed = $Name.Trim().ToLowerInvariant()
  return @('player','you','someone','a player').Contains($trimmed)
}
function Get-ShortHash([string]$Text){
  if([string]::IsNullOrEmpty($Text)){ return '' }
  try{
    $md5=[System.Security.Cryptography.MD5]::Create()
    try{
      $bytes=[System.Text.Encoding]::UTF8.GetBytes($Text)
      $hash=$md5.ComputeHash($bytes)
      return ($hash[0..3] | ForEach-Object { $_.ToString('x2') }) -join ''
    }finally{
      if($md5){ $md5.Dispose() }
    }
  }catch{ return '' }
}
function Is-VRChatRunning {
  try { @(Get-Process -Name 'VRChat' -ErrorAction SilentlyContinue).Count -gt 0 } catch { $false }
}

# ---------------- Single process ----------------
function Get-InstanceNames {
  $src = [System.IO.Path]::GetFullPath((Get-Command -Name ([Environment]::GetCommandLineArgs()[0])).Source)
  $bytes = [System.Text.Encoding]::UTF8.GetBytes($src)
  $md5   = [System.Security.Cryptography.MD5]::Create()
  $hash  = ($md5.ComputeHash($bytes) | ForEach-Object { $_.ToString('x2') }) -join ''
  @{ Mutex="Global\VRCJN_MUTEX_$hash"; Event="Global\VRCJN_OPENSETTINGS_$hash" }
}
function Init-SingleInstance{
  $names = Get-InstanceNames
  $created=$false
  $script:Mutex = New-Object System.Threading.Mutex($true,$names.Mutex,[ref]$created)
  $script:IsPrimary = $created

  $dummy=$false
  $script:OpenEvt = New-Object System.Threading.EventWaitHandle($false,[System.Threading.EventResetMode]::AutoReset,$names.Event,[ref]$dummy)

  if(-not $script:IsPrimary){
    try{ ([System.Threading.EventWaitHandle]::OpenExisting($names.Event)).Set() | Out-Null }catch{}
    [Environment]::Exit(0)
  }

  [AppDomain]::CurrentDomain.add_ProcessExit({
    try { if ($script:Mutex)     { $script:Mutex.ReleaseMutex() | Out-Null } } catch {}
    try { if ($global:PulseTimer){ $global:PulseTimer.Stop(); $global:PulseTimer.Dispose() } } catch {}
    try { if ($global:TrayIcon)  { $global:TrayIcon.Visible=$false; $global:TrayIcon.Dispose() } } catch {}
    try { if ($global:HostForm)  { $global:HostForm.Close(); $global:HostForm.Dispose() } } catch {}
  })
}

# ---------------- Notifications ----------------
function Ensure-ToastProvider{
  if($script:ToastReady){ return $true }
  try{
    if(Get-Module -ListAvailable -Name BurntToast | Out-Null){
      Import-Module BurntToast -ErrorAction SilentlyContinue
      $script:ToastReady=$true; return $true
    }
  }catch{}
  return $false
}
function Offer-InstallBurntToast{
  try{
    $ans=[System.Windows.Forms.MessageBox]::Show(
      "To show native Windows notifications, the free 'BurntToast' module is recommended.`r`nInstall now?",
      'Enable Windows Toasts','YesNo','Information')
    if($ans -eq 'Yes'){
      Set-PSRepository -Name PSGallery -InstallationPolicy Trusted -ErrorAction SilentlyContinue
      Install-Module BurntToast -Scope CurrentUser -Force -AllowClobber -ErrorAction Stop
      Import-Module BurntToast -ErrorAction Stop
      $script:ToastReady=$true; return $true
    }
  }catch{ Write-AppLog ("BurntToast install/import failed: " + $_.Exception.Message) }
  return $false
}
function Show-Notification($Title,$Body){
  if(Ensure-ToastProvider -or Offer-InstallBurntToast){
    try{
      $exe=Get-LauncherPath; $arg="--open-settings"
      $hasNew = $null -ne (Get-Command -Name New-BTContent -ErrorAction SilentlyContinue)
      if($hasNew){
        $content = New-BTContent -Text $Title, $Body -Launch ("`"$exe`" " + $arg)
        if($null -ne (Get-Command -Name New-BTButton -ErrorAction SilentlyContinue)){
          $button = New-BTButton -Content 'Open Settings' -Arguments ("`"$exe`" " + $arg)
          Submit-BTNotification -Content $content -Button $button | Out-Null
        } else { Submit-BTNotification -Content $content | Out-Null }
      } else {
        $action = New-BTAction -Content 'Open Settings' -Arguments ("`"$exe`" " + $arg)
        New-BurntToastNotification -Text $Title, $Body -Actions $action | Out-Null
      }
      return
    }catch{ Write-AppLog ("Toast failed, fallback to balloon: " + $_.Exception.Message) }
  }
  Init-Tray
  $global:TrayIcon.BalloonTipTitle=$Title
  $global:TrayIcon.BalloonTipText =$Body
  $global:TrayIcon.ShowBalloonTip(4000)
}
function Notify-All{
  param(
    $key,
    $title,
    $body,
    [bool]$Desktop = $true
  )
  $now=Get-Date
  if($script:LastNotified.ContainsKey($key)){
    if(($now - $script:LastNotified[$key]).TotalSeconds -lt $NotifyCooldownSeconds){
      Write-AppLog ("Suppressed '" + $key + "' within cooldown."); return
    }
  }
  $script:LastNotified[$key]=$now
  if($Desktop){ Show-Notification $title $body }
  Send-Pushover $title $body
}

# ---------------- Config ----------------
function Load-Config{
  $global:Cfg=[ordered]@{
    InstallDir    = $DefaultInstallDir
    VRChatLogDir  = $DefaultVRChatLogDir
    PushoverUser  = ''
    PushoverToken = ''
  }
  try{
    Ensure-Dir $DefaultInstallDir
    $path=Join-Path $DefaultInstallDir $ConfigFileName
    if(Test-Path $path){
      $json=Get-Content $path -Raw | ConvertFrom-Json
      foreach($k in 'InstallDir','VRChatLogDir','PushoverUser','PushoverToken'){
        if($json.PSObject.Properties.Name -contains $k){ $global:Cfg[$k]=$json.$k }
      }
    }
  }catch{ Show-Notification $AppName ("Failed to load settings: " + $_.Exception.Message) }
}
function Save-Config{
  try{
    Ensure-Dir $global:Cfg.InstallDir
    ($global:Cfg | ConvertTo-Json -Depth 3) | Set-Content -Path (Join-Path $global:Cfg.InstallDir $ConfigFileName) -Encoding UTF8
    Write-AppLog "Settings saved."
  }catch{
    [System.Windows.Forms.MessageBox]::Show("Failed to save settings:`r`n" + $_.Exception.Message,'Error','OK','Error') | Out-Null
  }
}

# ---------------- Pushover ----------------
function Send-Pushover($Title,$Message){
  if([string]::IsNullOrWhiteSpace($global:Cfg.PushoverUser) -or [string]::IsNullOrWhiteSpace($global:Cfg.PushoverToken)){
    Write-AppLog "Pushover not configured; skipping."; return
  }
  try{
    $body=@{ token=$global:Cfg.PushoverToken; user=$global:Cfg.PushoverUser; title=$Title; message=$Message; priority=0 }
    $resp=Invoke-RestMethod -Method Post -Uri $POUrl -Body $body -TimeoutSec 20
    Write-AppLog ("Pushover sent: " + $resp.status)
  }catch{ Write-AppLog ("Pushover error: " + $_.Exception.Message) }
}

# ---------------- Log selection helpers ----------------
function Score-LogFile([System.IO.FileInfo]$f){
  $a = $f.LastWriteTimeUtc
  $b = $f.CreationTimeUtc
  $best = $a
  if($b -gt $best){ $best = $b }
  $m = [regex]::Match($f.Name,'^output_log_(\d{4})-(\d{2})-(\d{2})_(\d{2})-(\d{2})-(\d{2})\.txt$', 'IgnoreCase')
  if($m.Success){
    try{
      $dt = Get-Date -Year $m.Groups[1].Value -Month $m.Groups[2].Value -Day $m.Groups[3].Value `
                     -Hour $m.Groups[4].Value -Minute $m.Groups[5].Value -Second $m.Groups[6].Value
      $dt = $dt.ToUniversalTime()
      if($dt -gt $best){ $best = $dt }
    }catch{}
  }
  return $best
}
function Get-NewestLogPath{
  if(-not(Test-Path $global:Cfg.VRChatLogDir)){ return $null }
  $files=@()
  $files += Get-ChildItem -Path $global:Cfg.VRChatLogDir -Filter 'output_log_*.txt' -File -ErrorAction SilentlyContinue
  $player = Join-Path $global:Cfg.VRChatLogDir 'Player.log'
  if(Test-Path $player){ $files += Get-Item $player }
  if(-not $files){ return $null }
  ($files | Sort-Object @{Expression={ Score-LogFile $_ } ; Descending=$true } | Select-Object -First 1).FullName
}

# ---------------- Log follower (job) ----------------
function Stop-Follow{
  if($global:FollowJob){
    try{ Stop-Job $global:FollowJob -Force -ErrorAction SilentlyContinue | Out-Null }catch{}
    try{ Remove-Job $global:FollowJob -Force -ErrorAction SilentlyContinue | Out-Null }catch{}
    $global:FollowJob=$null
  }
}
function Start-Follow{
  Stop-Follow
  $firstLog=Get-NewestLogPath
  if(-not $firstLog){ Write-AppLog ("No VRChat logs under " + $global:Cfg.VRChatLogDir); return }
  Write-AppLog ("Following: " + $firstLog)

  $global:FollowJob = Start-Job -ScriptBlock {
    param($InitialLogPath,$LogDir)

    $EmDash = [char]0x2014
    $EnDash = [char]0x2013
    $JoinSeparatorChars = [char[]]@('-',':','|',$EnDash,$EmDash)
    $JoinSeparatorString = -join $JoinSeparatorChars
    $JoinSeparatorOnlyPattern = '^[\-:|\u2013\u2014]+$'

    function Normalize-LogPath([string]$path){
      if([string]::IsNullOrWhiteSpace($path)){ return $null }
      try{ return [System.IO.Path]::GetFullPath($path) }catch{ return $path }
    }
    function Score-LogFile([System.IO.FileInfo]$f){
      $a = $f.LastWriteTimeUtc
      $b = $f.CreationTimeUtc
      $best = $a
      if($b -gt $best){ $best = $b }
      $m = [regex]::Match($f.Name,'^output_log_(\d{4})-(\d{2})-(\d{2})_(\d{2})-(\d{2})-(\d{2})\.txt$', 'IgnoreCase')
      if($m.Success){
        try{
          $dt = Get-Date -Year $m.Groups[1].Value -Month $m.Groups[2].Value -Day $m.Groups[3].Value `
                         -Hour $m.Groups[4].Value -Minute $m.Groups[5].Value -Second $m.Groups[6].Value
          $dt = $dt.ToUniversalTime()
          if($dt -gt $best){ $best = $dt }
        }catch{}
      }
      return $best
    }
    function Get-Newest([string]$dir){
      $files=@()
      $files += Get-ChildItem -Path $dir -Filter 'output_log_*.txt' -File -ErrorAction SilentlyContinue
      $player = Join-Path $dir 'Player.log'
      if(Test-Path $player){ $files += Get-Item $player }
      if(-not $files){ return $null }
      ($files | Sort-Object @{Expression={ Score-LogFile $_ } ; Descending=$true } | Select-Object -First 1).FullName
    }
    function Normalize-JoinFragment([string]$text){
      if([string]::IsNullOrWhiteSpace($text)){ return $null }
      $tmp = [regex]::Replace($text,'[\u200B-\u200D\uFEFF]','')
      $tmp = $tmp.Trim()
      $tmp = $tmp -replace '\u3000',' '
      $tmp = $tmp.Trim([char]34).Trim([char]39).Trim()
      $tmp = $tmp.TrimStart($JoinSeparatorChars).Trim()
      if([string]::IsNullOrWhiteSpace($tmp)){ return $null }
      if($tmp.Length -gt 160){ $tmp = $tmp.Substring(0,160).Trim() }
      if($tmp -match $JoinSeparatorOnlyPattern){ return $null }
      return $tmp
    }
    function Parse-PlayerEventLine([string]$line,[string]$eventToken = 'OnPlayerJoined'){
      if([string]::IsNullOrWhiteSpace($line)){ return $null }

      $needle = 'onplayerjoined'
      if(-not [string]::IsNullOrWhiteSpace($eventToken)){
        $needle = $eventToken.ToLowerInvariant()
      }

      $lowerLineForSearch = $line.ToLowerInvariant()
      $idx = $lowerLineForSearch.IndexOf($needle)
      if($idx -lt 0){ return $null }

      $after = $line.Substring($idx + $needle.Length)
      $after = [regex]::Replace($after,'[\u200B-\u200D\uFEFF]','')
      $after = $after.Trim()
      while($after.Length -gt 0 -and $JoinSeparatorString.Contains($after[0])){ $after = $after.Substring(1).TrimStart() }

      $placeholder = $null
      if(-not [string]::IsNullOrWhiteSpace($after)){
        $candidate = $after
        $candidate = ([regex]::Split($candidate,'(?i)\b(displayName|name|userId)\b')[0])
        $candidate = ($candidate.Split('(')[0]).Split('[')[0]
        $candidate = ($candidate.Split('{')[0]).Split('<')[0]
        $candidate = Normalize-JoinFragment $candidate
        if(Is-PlaceholderName $candidate){ $placeholder = $candidate }
      }

      $displayName = $null
      $m = [regex]::Match($after,'(?i)displayName\s*[:=]\s*(?<name>[^,\]\)]+)')
      if($m.Success){ $displayName = Normalize-JoinFragment $m.Groups['name'].Value }

      if(-not $displayName){
        $m = [regex]::Match($after,'(?i)\bname\s*[:=]\s*(?<name>[^,\]\)]+)')
        if($m.Success){ $displayName = Normalize-JoinFragment $m.Groups['name'].Value }
      }

      $userId = $null
      $m = [regex]::Match($after,'(?i)\(usr_[^\)\s]+\)')
      if($m.Success){ $userId = $m.Value.Trim('(',')').Trim() }
      if(-not $userId){
        $m = [regex]::Match($after,'(?i)userId\s*[:=]\s*(usr_[0-9a-f\-]+)')
        if($m.Success){ $userId = $m.Groups[1].Value }
      }

      if(-not $displayName){
        $tmp = $after
        if($userId){
          $tmp = $tmp -replace [regex]::Escape('(' + $userId + ')'), ''
        }
        $tmp = [regex]::Replace($tmp,'\(usr_[^\)]*\)','')
        $tmp = [regex]::Replace($tmp,'\(userId[^\)]*\)','')
        $tmp = [regex]::Replace($tmp,'\[[^\]]*\]','')
        $tmp = [regex]::Replace($tmp,'\{[^\}]*\}','')
        $tmp = [regex]::Replace($tmp,'<[^>]*>','')
        $tmp = $tmp -replace '\|\|','|'
        $displayName = Normalize-JoinFragment $tmp
      }

      if(-not $displayName -and $userId){ $displayName = $userId }

      return [pscustomobject]@{ Name=$displayName; UserId=$userId; Placeholder=$placeholder }
    }

    # Detects world/instance transitions for the local player even when VRChat logs
    # use localized phrases (e.g. Japanese) or alternative wording.
    function Parse-RoomTransitionLine([string]$line){
      if([string]::IsNullOrWhiteSpace($line)){ return $null }

      $clean = [regex]::Replace($line,'[\u200B-\u200D\uFEFF]','')
      $clean = $clean.Trim()
      if([string]::IsNullOrWhiteSpace($clean)){ return $null }

      $lower = $clean.ToLowerInvariant()

      $jpRoomKey = -join @([char]0x30EB,[char]0x30FC,[char]0x30E0)
      $jpInstanceKey = -join @([char]0x30A4,[char]0x30F3,[char]0x30B9,[char]0x30BF,[char]0x30F3,[char]0x30B9)
      $jpJoinTerm = -join @([char]0x53C2,[char]0x52A0)
      $jpCreateTerm = -join @([char]0x4F5C,[char]0x6210)
      $jpEnterRoomTerm = -join @([char]0x5165,[char]0x5BA4)
      $jpMoveTerm = -join @([char]0x79FB,[char]0x52D5)
      $jpEnterHallTerm = -join @([char]0x5165,[char]0x5834)

      $indicators = @(
        'joining or creating room',
        'entering room',
        'joining room',
        'creating room',
        'created room',
        'rejoining room',
        're-joining room',
        'reentering room',
        're-entering room',
        'joining instance',
        'creating instance',
        'entering instance'
      )

      $matched = $false
      foreach($indicator in $indicators){
        if($lower.Contains($indicator)){
          $matched = $true
          break
        }
      }

      if(-not $matched){
        $jpSets = @(
          @{ Key=$jpRoomKey; Terms=@($jpJoinTerm,$jpCreateTerm,$jpEnterRoomTerm,$jpMoveTerm,$jpEnterHallTerm) },
          @{ Key=$jpInstanceKey; Terms=@($jpJoinTerm,$jpCreateTerm,$jpEnterRoomTerm,$jpMoveTerm,$jpEnterHallTerm) }
        )
        foreach($set in $jpSets){
          if($clean.Contains($set.Key)){
            foreach($term in $set.Terms){
              if($clean.Contains($term)){
                $matched = $true
                break
              }
            }
          }
          if($matched){ break }
        }
      }

      if(-not $matched){
        if($clean -match '(?i)\bwrld_[0-9a-f\-]+\b'){
          $hasJapaneseRoomWord = $clean.Contains($jpRoomKey) -or $clean.Contains($jpInstanceKey)

          if($lower.Contains('room') -or $lower.Contains('instance') -or $hasJapaneseRoomWord){
            $matched = $true
          }
        }
      }

      if(-not $matched){ return $null }

      $worldId = ''
      $instanceId = ''

      $worldMatch = [regex]::Match($clean,'wrld_[0-9a-f\-]+','IgnoreCase')
      if($worldMatch.Success){
        $worldId = $worldMatch.Value

        $afterWorld = ''
        try{ $afterWorld = $clean.Substring($worldMatch.Index + $worldMatch.Length) }catch{ $afterWorld = '' }

        if(-not [string]::IsNullOrWhiteSpace($afterWorld)){
          $afterWorld = $afterWorld.TrimStart(':',' ','`t','-')
          if(-not [string]::IsNullOrWhiteSpace($afterWorld)){
            $instMatch = [regex]::Match($afterWorld,'^[^\s,]+')
            if($instMatch.Success){ $instanceId = $instMatch.Value }
          }
        }
      }

      if([string]::IsNullOrWhiteSpace($instanceId)){
        $instAlt = [regex]::Match($clean,'(?i)instance\s*[:=]\s*([^\s,]+)')
        if($instAlt.Success){ $instanceId = $instAlt.Groups[1].Value }
      }

      return [pscustomobject]@{ World=$worldId; Instance=$instanceId; RawLine=$clean }
    }

    $reSelf = [regex]'(?i)\[Behaviour\].*OnJoinedRoom\b'
    $reJoin = [regex]'(?i)\[Behaviour\].*OnPlayerJoined\b'
    $reLeave = [regex]'(?i)\[Behaviour\].*OnPlayerLeft\b'

    $logPath = Normalize-LogPath $InitialLogPath
    $lastSize = 0L
    if($logPath){
      $info = Get-Item -LiteralPath $logPath -ErrorAction SilentlyContinue
      if($info){ $lastSize = $info.Length } else { $lastSize = 0L }
    }

    while($true){
      $maybe = Get-Newest $LogDir
      if($maybe){
        $maybe = Normalize-LogPath $maybe
        $isSame = $false
        if($maybe -and $logPath){
          $isSame = [string]::Equals($maybe,$logPath,[System.StringComparison]::OrdinalIgnoreCase)
        }
        if(-not $isSame){
          $logPath = $maybe
          $info = $null
          if($logPath){ $info = Get-Item -LiteralPath $logPath -ErrorAction SilentlyContinue }
          if($info){ $lastSize = $info.Length } else { $lastSize = 0L }
          if($logPath){ Write-Output ("SWITCHED||" + $logPath) }
          Start-Sleep -Milliseconds 200
          continue
        }
      }

      if(-not $logPath -or -not(Test-Path $logPath)){ Start-Sleep -Milliseconds 800; continue }

      $info = Get-Item -LiteralPath $logPath -ErrorAction SilentlyContinue
      if(-not $info){ Start-Sleep -Milliseconds 800; continue }

      if($info.Length -lt $lastSize){ $lastSize = 0L }

      $fs=[System.IO.File]::Open($info.FullName,[System.IO.FileMode]::Open,[System.IO.FileAccess]::Read,[System.IO.FileShare]::ReadWrite)
      try{
        $sr=New-Object System.IO.StreamReader($fs)
        $fs.Seek($lastSize,[System.IO.SeekOrigin]::Begin) | Out-Null
        while(-not $sr.EndOfStream){
          $line=$sr.ReadLine()

          if([string]::IsNullOrWhiteSpace($line)){ continue }

          $lowerLine = $line.ToLowerInvariant()

          if($lowerLine.Contains('onleftroom')){
            $safeLine = $line -replace '\|\|','|'
            Write-Output ("ROOM_EVENT||LEFT||||" + $safeLine)
            continue
          }

          $roomEvent = Parse-RoomTransitionLine $line
          if($roomEvent){
            $safeWorld = $roomEvent.World -replace '\|\|','|'
            $safeInstance = $roomEvent.Instance -replace '\|\|','|'
            $safeLine = $roomEvent.RawLine -replace '\|\|','|'
            Write-Output ("ROOM_EVENT||ENTER||" + $safeWorld + "||" + $safeInstance + "||" + $safeLine)
            continue
          }

          if($reSelf.IsMatch($line)){
            Write-Output ("SELF_JOIN||" + $line)
            continue
          }
          if($reLeave.IsMatch($line)){
            $parsedLeave = Parse-PlayerEventLine $line 'OnPlayerLeft'
            $leaveName = ''
            $leaveUserId = ''
            if($parsedLeave){
              if($parsedLeave.Name){ $leaveName = $parsedLeave.Name }
              if($parsedLeave.UserId){ $leaveUserId = $parsedLeave.UserId }
            }
            $safeLeaveName = ($leaveName -replace '\|\|','|')
            $safeLeaveUser = ($leaveUserId -replace '\|\|','|')
            Write-Output ("PLAYER_LEAVE||" + $safeLeaveName + "||" + $safeLeaveUser + "||" + $line)
            continue
          }

          if($reJoin.IsMatch($line)){
            $parsed = Parse-PlayerEventLine $line 'OnPlayerJoined'
            $name = ''
            $userId = ''
            $placeholder = ''
            if($parsed){
              if($parsed.Name){ $name = $parsed.Name }
              if($parsed.UserId){ $userId = $parsed.UserId }
              if($parsed.Placeholder){ $placeholder = $parsed.Placeholder }
            }
            $safeName = ($name -replace '\|\|','|')
            $safeUser = ($userId -replace '\|\|','|')
            $safePlaceholder = ($placeholder -replace '\|\|','|')
            Write-Output ("PLAYER_JOIN||" + $safeName + "||" + $safeUser + "||" + $safePlaceholder + "||" + $line)
            continue
          }
        }
        $lastSize=$fs.Length
      }finally{ $sr.Close(); $fs.Close() }

      Start-Sleep -Milliseconds 600
    }
  } -ArgumentList $firstLog,$global:Cfg.VRChatLogDir
}
function Process-FollowOutput {
  if (-not $global:FollowJob) { return }
  try{
    $out = Receive-Job -Id $global:FollowJob.Id -ErrorAction SilentlyContinue
    if(-not $out){ return }
    foreach($s in $out){
      if($s -isnot [string]){ continue }

      if($s.StartsWith('SWITCHED||')){
        $p=$s.Substring(10)
        Write-AppLog ("Switching to newest log: " + $p)
        Reset-SessionState
        if($global:TrayIcon){
          $global:IdleTooltip = $AppName
          try{ $global:TrayIcon.Text = $global:IdleTooltip }catch{}
        }
        continue
      }

      if($s.StartsWith('ROOM_EVENT||')){
        $parts=$s.Split('||',5)
        $eventType=''
        if($parts.Length -ge 2){ $eventType = $parts[1] }
        $worldId=''
        if($parts.Length -ge 3){ $worldId = $parts[2] }
        $instanceId=''
        if($parts.Length -ge 4){ $instanceId = $parts[3] }
        $rawRoomLine=''
        if($parts.Length -ge 5){ $rawRoomLine = $parts[4] }

        if($eventType -eq 'LEFT'){
          if($script:SessionReady){
            Write-AppLog ("Session " + $script:SessionId + " ended (OnLeftRoom detected).")
          }else{
            Write-AppLog 'OnLeftRoom detected.'
          }
          Reset-SessionState
          if($global:TrayIcon){
            $global:IdleTooltip = $AppName
            try{ $global:TrayIcon.Text = $global:IdleTooltip }catch{}
          }
          continue
        }

        if($eventType -eq 'ENTER'){
          Reset-SessionState
          $roomInfo = [pscustomobject]@{ World=$worldId; Instance=$instanceId; Raw=$rawRoomLine }
          $script:PendingRoom = $roomInfo
          $roomDesc = $null
          if(-not [string]::IsNullOrWhiteSpace($worldId)){
            $roomDesc = $worldId
            if(-not [string]::IsNullOrWhiteSpace($instanceId)){ $roomDesc += ":" + $instanceId }
          }
          if($roomDesc){
            Write-AppLog ("Room transition detected: " + $roomDesc)
          }elseif(-not [string]::IsNullOrWhiteSpace($rawRoomLine)){
            Write-AppLog ("Room transition detected: " + $rawRoomLine)
          }else{
            Write-AppLog 'Room transition detected.'
          }
          if($global:TrayIcon){
            $tip = $AppName
            if($roomDesc){ $tip = "$AppName $EmDash $roomDesc" }
            $global:IdleTooltip = $tip
            try{ $global:TrayIcon.Text = $tip }catch{}
          }
          continue
        }

        continue
      }

      if(-not (Is-VRChatRunning)) { continue }

      if($s.StartsWith('SELF_JOIN||')){
        $selfParts = $s.Split('||',2)
        $rawSelfLine = ''
        if($selfParts.Length -ge 2){ $rawSelfLine = $selfParts[1] }
        $now = Get-Date
        $reuseFallback = $false
        $elapsedSinceFallback = $null
        $lastJoinGap = $null
        $fallbackJoinCount = 0

        if($script:SessionReady -and ($script:SessionSource -eq 'OnPlayerJoined fallback')){
          $fallbackJoinCount = $script:SeenPlayers.Count

          if($script:SessionStartedAt){
            try{ $elapsedSinceFallback = $now - $script:SessionStartedAt }catch{ $elapsedSinceFallback = $null }
          }

          if($fallbackJoinCount -gt 0){
            $lastJoinAt = $script:SessionLastJoinAt
            if(-not $lastJoinAt){
              try{ $lastJoinAt = ($script:SeenPlayers.Values | Sort-Object -Descending | Select-Object -First 1) }catch{ $lastJoinAt = $null }
            }
            if($lastJoinAt){
              try{ $lastJoinGap = $now - $lastJoinAt }catch{ $lastJoinGap = $null }
            }
          }

          $withinGrace = ($null -ne $elapsedSinceFallback -and $elapsedSinceFallback.TotalSeconds -lt $SessionFallbackGraceSeconds)
          $withinJoinGap = $false
          if($withinGrace){
            if($fallbackJoinCount -le 0){
              $withinJoinGap = $true
            }elseif($lastJoinGap){
              $withinJoinGap = ($lastJoinGap.TotalSeconds -le $SessionFallbackMaxContinuationSeconds)
            }
          }

          if($withinGrace -and $withinJoinGap){
            $reuseFallback = $true
            $script:SessionSource = 'OnJoinedRoom'

            $logParts = @()
            if($lastJoinGap){
              $gapSeconds = [Math]::Round([Math]::Max(0,$lastJoinGap.TotalSeconds),1)
              $logParts += ("last join gap " + $gapSeconds + 's')
            }elseif($fallbackJoinCount -gt 0){
              $logParts += 'last join gap unknown'
            }
            if($fallbackJoinCount -gt 0){ $logParts += ("tracked players " + $fallbackJoinCount) }

            $detail = ''
            if($logParts.Count -gt 0){ $detail = ' (' + ($logParts -join '; ') + ')' }
            Write-AppLog ("Session " + $script:SessionId + " confirmed by OnJoinedRoom." + $detail)
          }
        }

        if(-not $reuseFallback){
          if($script:SessionReady -and ($script:SessionSource -eq 'OnPlayerJoined fallback')){
            $detailParts = @()
            if($null -ne $elapsedSinceFallback){
              $seconds = [Math]::Round([Math]::Max(0,$elapsedSinceFallback.TotalSeconds),1)
              $detailParts += ("after " + $seconds + 's')
            }
            if($fallbackJoinCount -gt 0){
              if($lastJoinGap){
                $gapSeconds = [Math]::Round([Math]::Max(0,$lastJoinGap.TotalSeconds),1)
                $detailParts += ("last join gap " + $gapSeconds + 's')
              }else{
                $detailParts += 'last join gap unavailable'
              }
              $detailParts += ("tracked players " + $fallbackJoinCount)
            }
            $detail = ''
            if($detailParts.Count -gt 0){ $detail = ' (' + ($detailParts -join '; ') + ')' }
            Write-AppLog ("Session " + $script:SessionId + " fallback expired" + $detail + "; starting new session for OnJoinedRoom.")
          }
          $pendingRoomInfo = $script:PendingRoom
          Reset-SessionState
          if($pendingRoomInfo){ $script:PendingRoom = $pendingRoomInfo }
          [void](Ensure-SessionReady('OnJoinedRoom'))
        }

        $selfName = $null
        $selfUserId = $null
        $selfPlaceholder = $null
        if(-not [string]::IsNullOrWhiteSpace($rawSelfLine)){
          $parsedSelf = Parse-PlayerEventLine $rawSelfLine 'OnJoinedRoom'
          if($parsedSelf){
            if($parsedSelf.Name){ $selfName = Normalize-JoinName $parsedSelf.Name }
            if($parsedSelf.UserId){
              $selfUserId = $parsedSelf.UserId
              $userKey = $selfUserId.ToLowerInvariant()
              if(-not $script:LocalUserId -or $script:LocalUserId -ne $userKey){
                $script:LocalUserId = $userKey
                Write-AppLog ("Learned local userId from OnJoinedRoom event: " + $selfUserId)
              }
            }
            if($parsedSelf.Placeholder){ $selfPlaceholder = Normalize-JoinName $parsedSelf.Placeholder }
          }
        }
        if(-not $selfName -and $selfUserId){ $selfName = $selfUserId }
        $displayName = if($selfName){ $selfName } else { 'You' }
        $placeholderLabel = $selfPlaceholder
        if([string]::IsNullOrWhiteSpace($placeholderLabel)){ $placeholderLabel = 'Player' }
        elseif($placeholderLabel.ToLowerInvariant() -eq 'you'){ $placeholderLabel = 'Player' }
        if($displayName.ToLowerInvariant() -eq 'you' -and $selfName){ $displayName = $selfName }
        $messageName = $displayName
        if(-not [string]::IsNullOrWhiteSpace($placeholderLabel)){
          if([string]::IsNullOrWhiteSpace($messageName)){ $messageName = $placeholderLabel }
          else{ $messageName = $messageName + '(' + $placeholderLabel + ')' }
        }
        $message = $messageName + ' joined your instance.'
        Notify-All ("self:" + $script:SessionId) $AppName $message
        $script:PendingSelfJoin = [pscustomobject]@{
          SessionId = $script:SessionId
          Placeholder = $placeholderLabel
          Timestamp = $now
        }
        continue
      }

      if($s.StartsWith('PLAYER_LEAVE||')){
        if(-not $script:SessionReady){ continue }
        $parts=$s.Split('||',4)
        $rawName = ''
        $rawUserId = ''
        $rawLine = ''
        if($parts.Length -ge 2){ $rawName = $parts[1] }
        if($parts.Length -ge 4){
          $rawUserId = $parts[2]
          $rawLine = $parts[3]
        }elseif($parts.Length -ge 3){
          $rawLine = $parts[2]
        }

        $name = Normalize-JoinName $rawName

        $userId = $null
        if(-not [string]::IsNullOrWhiteSpace($rawUserId)){
          $tmpUser = [regex]::Replace($rawUserId,'[\u200B-\u200D\uFEFF]','').Trim()
          if(-not [string]::IsNullOrWhiteSpace($tmpUser)){ $userId = $tmpUser }
        }

        $userKey = $null
        if($userId){ $userKey = $userId.ToLowerInvariant() }

        $isPlaceholder = Is-PlaceholderName $name
        if($isPlaceholder -and $userKey){
          if(-not $script:LocalUserId){
            $script:LocalUserId = $userKey
            Write-AppLog ("Learned local userId from leave event: " + $userId)
          }elseif($script:LocalUserId -eq $userKey){
            $name = $userId
            $isPlaceholder = $false
          }
        }

        if(-not $name -and $userId){ $name = $userId; $isPlaceholder = $false }
        if(-not $name){ $name = 'Unknown VRChat user' }

        $removedCount = 0
        if($userKey){
          $keyPrefix = "join:{0}:{1}" -f $script:SessionId,$userKey
          $keysToRemove = @()
          foreach($existingKey in @($script:SeenPlayers.Keys)){
            if($existingKey.StartsWith($keyPrefix)){
              $keysToRemove += $existingKey
            }
          }
          foreach($keyToRemove in $keysToRemove){
            if($script:SeenPlayers.ContainsKey($keyToRemove)){
              $null = $script:SeenPlayers.Remove($keyToRemove)
              $removedCount++
            }
          }
        }

        $logLine = "Session {0}: player left '{1}'" -f $script:SessionId,$name
        if($userId){ $logLine += " (" + $userId + ")" }
        if($removedCount -gt 0){
          $logLine += ' [cleared join tracking]'
        }
        $logLine += '.'
        Write-AppLog $logLine
        continue
      }

      if($s.StartsWith('PLAYER_JOIN||')){
        if(-not $script:SessionReady){ [void](Ensure-SessionReady('OnPlayerJoined fallback')) }
        if(-not $script:SessionReady){ continue }
        $parts=$s.Split('||',5)
        $rawName = ''
        $rawUserId = ''
        $rawPlaceholder = ''
        $rawLine = ''
        if($parts.Length -ge 2){ $rawName = $parts[1] }
        if($parts.Length -ge 3){ $rawUserId = $parts[2] }
        if($parts.Length -ge 5){
          $rawPlaceholder = $parts[3]
          $rawLine = $parts[4]
        }elseif($parts.Length -ge 4){
          $rawLine = $parts[3]
        }

        $name = Normalize-JoinName $rawName
        $originalName = $name
        $placeholder = Normalize-JoinName $rawPlaceholder

        $userId = $null
        if(-not [string]::IsNullOrWhiteSpace($rawUserId)){
          $tmpUser = [regex]::Replace($rawUserId,'[\u200B-\u200D\uFEFF]','').Trim()
          if(-not [string]::IsNullOrWhiteSpace($tmpUser)){ $userId = $tmpUser }
        }

        $userKey = $null
        if($userId){ $userKey = $userId.ToLowerInvariant() }

        $eventTime = Get-Date
        $script:SessionLastJoinAt = $eventTime

        if($userKey -and $script:LocalUserId -and $userKey -eq $script:LocalUserId){
          Write-AppLog ("Skipping join for known local userId '" + $userId + "'.")
          $script:PendingSelfJoin = $null
          continue
        }

        $pendingSelf = $script:PendingSelfJoin
        if($pendingSelf -and $pendingSelf.SessionId -eq $script:SessionId){
          $pendingPlaceholder = $pendingSelf.Placeholder
          if($pendingPlaceholder){ $pendingPlaceholder = Normalize-JoinName $pendingPlaceholder }
          $pendingLower = $null
          if($pendingPlaceholder){ $pendingLower = $pendingPlaceholder.ToLowerInvariant() }
          $eventPlaceholderLower = $null
          if($placeholder){ $eventPlaceholderLower = $placeholder.ToLowerInvariant() }
          elseif(Is-PlaceholderName $originalName){ $eventPlaceholderLower = $originalName.ToLowerInvariant() }
          $ageOk = $false
          if($pendingSelf.Timestamp){
            try{ $ageOk = (($eventTime - $pendingSelf.Timestamp).TotalSeconds -lt 10) }
            catch{ $ageOk = $false }
          }
          if($ageOk -and $pendingLower -and ($pendingLower -eq 'player' -or $pendingLower -eq 'you')){
            if($pendingLower -eq $eventPlaceholderLower -or (-not $eventPlaceholderLower -and (Is-PlaceholderName $originalName))){
              if($userKey -and -not $script:LocalUserId){ $script:LocalUserId = $userKey }
              $script:PendingSelfJoin = $null
              Write-AppLog 'Skipping join matched pending self event.'
              continue
            }
          }
        }

        $isPlaceholder = Is-PlaceholderName $name
        $wasPlaceholder = $isPlaceholder
        $isFallbackSession = ($script:SessionSource -eq 'OnPlayerJoined fallback')
        if($wasPlaceholder -and $userKey){
          if($isFallbackSession -and -not $script:LocalUserId){
            $script:LocalUserId = $userKey
            Write-AppLog ("Learned local userId from join event: " + $userId)
            Write-AppLog ("Skipping initial local join placeholder for userId '" + $userId + "'.")
            $script:PendingSelfJoin = $null
            continue
          }
          if($script:LocalUserId -and $script:LocalUserId -eq $userKey){
            Write-AppLog ("Skipping local join placeholder for userId '" + $userId + "'.")
            $script:PendingSelfJoin = $null
            continue
          }
        }

        if(-not $name -and $userId){
          $name = $userId
          $isPlaceholder = $false
        }elseif($isPlaceholder -and $userId){
          $name = $userId
          $isPlaceholder = $false
        }

        if(-not $name){ $name = 'Unknown VRChat user' }

        $keyBase = if($userKey){ $userKey } else { $name.ToLowerInvariant() }
        $hashSuffix = ''
        if(-not $userId -and -not [string]::IsNullOrWhiteSpace($rawLine)){
          $hashSuffix = Get-ShortHash $rawLine
        }

        $joinKey = "join:{0}:{1}" -f $script:SessionId,$keyBase
        if($hashSuffix){ $joinKey += ":" + $hashSuffix }

        if(-not $script:SeenPlayers.ContainsKey($joinKey)){
          $script:SeenPlayers[$joinKey]=$eventTime
          $placeholderForMessage = $placeholder
          if([string]::IsNullOrWhiteSpace($placeholderForMessage) -and $wasPlaceholder){
            $placeholderForMessage = $originalName
          }
          if([string]::IsNullOrWhiteSpace($placeholderForMessage)){ $placeholderForMessage = 'Someone' }
          elseif($placeholderForMessage.ToLowerInvariant() -eq 'you'){ $placeholderForMessage = 'Player' }
          $messageName = $name
          if([string]::IsNullOrWhiteSpace($messageName)){ $messageName = $placeholderForMessage }
          else{ $messageName = $messageName + '(' + $placeholderForMessage + ')' }
          $desktopNotification = $true
          if($wasPlaceholder -and [string]::IsNullOrWhiteSpace($userId)){
            $placeholderLower = ''
            if(-not [string]::IsNullOrWhiteSpace($placeholderForMessage)){
              $placeholderLower = $placeholderForMessage.Trim().ToLowerInvariant()
            }
            if($placeholderLower -eq 'a player'){ $desktopNotification = $false }
          }
          $message = $messageName + ' joined your instance.'
          Notify-All $joinKey $AppName $message $desktopNotification

          $logLine = "Session {0}: player joined '{1}'" -f $script:SessionId,$name
          if($userId){ $logLine += " (" + $userId + ")" }
          $logLine += '.'
          Write-AppLog $logLine
        }
        continue
      }
    }
  }catch{ Write-AppLog ("Receive-Job error: " + $_.Exception.Message) }
}

# ---------------- Startup shortcut ----------------
function Get-StartupFolder{ [Environment]::GetFolderPath('Startup') }
function Get-StartupShortcutPath{ Join-Path (Get-StartupFolder) 'VRChatJoinNotifier.lnk' }
function Add-Startup{
  try{
    $startup=Get-StartupFolder; Ensure-Dir $startup
    $launcher=Get-LauncherPath
    $lnk=Get-StartupShortcutPath
    $wsh=New-Object -ComObject WScript.Shell
    $sc=$wsh.CreateShortcut($lnk)
    if($launcher.ToLower().EndsWith('.exe')){ $sc.TargetPath=$launcher; $sc.Arguments='' }
    else{
      $sc.TargetPath="$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe"
      $sc.Arguments="-WindowStyle Hidden -ExecutionPolicy Bypass -File `"$launcher`""
    }
    $sc.WorkingDirectory=Split-Path $launcher -Parent
    $ico=Join-Path (Split-Path $launcher -Parent) 'vrchat_join_notification\notification.ico'
    if(Test-Path $ico){ $sc.IconLocation=$ico }
    $sc.Save()
    Show-Notification $AppName 'Added to Startup.'
    return $true
  }catch{
    Show-Notification $AppName ("Failed to add Startup: " + $_.Exception.Message)
    return $false
  }
}
function Remove-Startup{
  try{
    $lnk=Get-StartupShortcutPath
    if(Test-Path $lnk){ Remove-Item $lnk -Force }
    Show-Notification $AppName 'Removed from Startup.'
    return $true
  }catch{
    Show-Notification $AppName ("Failed to remove Startup: " + $_.Exception.Message)
    return $false
  }
}

function Exit-App{
  Stop-Follow
  try{ if($global:PulseTimer){ $global:PulseTimer.Stop(); $global:PulseTimer.Dispose(); $global:PulseTimer=$null } }catch{}
  try{ if($global:TrayIcon){ $global:TrayIcon.Visible=$false; $global:TrayIcon.Dispose(); $global:TrayIcon=$null } }catch{}
  try{ if($global:HostForm){ $global:HostForm.Close(); $global:HostForm.Dispose(); $global:HostForm=$null } }catch{}
  try{ if($global:SettingsForm -and (-not $global:SettingsForm.IsDisposed)){ $global:SettingsForm.Close() } }catch{}
  try{ if($script:Mutex){ $script:Mutex.ReleaseMutex() | Out-Null } }catch{}
  [System.Windows.Forms.Application]::Exit()
  [Environment]::Exit(0)
}

# ---------------- Settings GUI (single instance) ----------------
function Show-SettingsForm{
  if($global:SettingsForm -and (-not $global:SettingsForm.IsDisposed)){
    if(-not $global:SettingsForm.Visible){ $global:SettingsForm.Show() }
    $global:SettingsForm.WindowState='Normal'
    $global:SettingsForm.TopMost=$true
    $global:SettingsForm.Activate(); $global:SettingsForm.Focus(); $global:SettingsForm.BringToFront()
    $tmpTimer = New-Object System.Windows.Forms.Timer
    $tmpTimer.Interval=200
    $tmpTimer.Add_Tick({ param($s,$e) $global:SettingsForm.TopMost=$false; $s.Stop(); $s.Dispose() })
    $tmpTimer.Start()
    return
  }

  $form=New-Object System.Windows.Forms.Form
  $form.Text='VRChat Join Notifier (Windows)'
  $form.Size=New-Object System.Drawing.Size(760,320)
  $form.StartPosition='CenterScreen'
  $form.MinimumSize=$form.Size

  $lblInstall=New-Object System.Windows.Forms.Label
  $lblInstall.Text='Install Folder (logs/cache):'
  $lblInstall.Location=New-Object System.Drawing.Point(12,12)
  $lblInstall.AutoSize=$true

  $txtInstall=New-Object System.Windows.Forms.TextBox
  $txtInstall.Location=New-Object System.Drawing.Point(12,32)
  $txtInstall.Size=New-Object System.Drawing.Size(600,24)
  $txtInstall.Text=$global:Cfg.InstallDir

  $btnBrowseInstall=New-Object System.Windows.Forms.Button
  $btnBrowseInstall.Text='Browse...'
  $btnBrowseInstall.Location=New-Object System.Drawing.Point(624,30)
  $btnBrowseInstall.Size=New-Object System.Drawing.Size(110,28)
  $btnBrowseInstall.Add_Click({ param($sender,$e)
    $dlg=New-Object System.Windows.Forms.FolderBrowserDialog
    if($dlg.ShowDialog() -eq 'OK'){ $txtInstall.Text=$dlg.SelectedPath }
  })

  $lblVR=New-Object System.Windows.Forms.Label
  $lblVR.Text='VRChat Log Folder:'
  $lblVR.Location=New-Object System.Drawing.Point(12,72)
  $lblVR.AutoSize=$true

  $txtVR=New-Object System.Windows.Forms.TextBox
  $txtVR.Location=New-Object System.Drawing.Point(12,92)
  $txtVR.Size=New-Object System.Drawing.Size(600,24)
  $txtVR.Text=$global:Cfg.VRChatLogDir

  $btnBrowseVR=New-Object System.Windows.Forms.Button
  $btnBrowseVR.Text='Browse...'
  $btnBrowseVR.Location=New-Object System.Drawing.Point(624,90)
  $btnBrowseVR.Size=New-Object System.Drawing.Size(110,28)
  $btnBrowseVR.Add_Click({ param($sender,$e)
    $dlg=New-Object System.Windows.Forms.FolderBrowserDialog
    if($dlg.ShowDialog() -eq 'OK'){ $txtVR.Text=$dlg.SelectedPath }
  })

  $lblUser=New-Object System.Windows.Forms.Label
  $lblUser.Text='Pushover User Key:'
  $lblUser.Location=New-Object System.Drawing.Point(12,126)
  $lblUser.AutoSize=$true

  $txtUser=New-Object System.Windows.Forms.TextBox
  $txtUser.Location=New-Object System.Drawing.Point(12,146)
  $txtUser.Size=New-Object System.Drawing.Size(300,24)
  $txtUser.UseSystemPasswordChar=$true
  if([string]::IsNullOrWhiteSpace($global:Cfg.PushoverUser)){
    $txtUser.Text=''
  }else{
    $txtUser.Text='*****'
  }

  $lblToken=New-Object System.Windows.Forms.Label
  $lblToken.Text='Pushover API Token:'
  $lblToken.Location=New-Object System.Drawing.Point(324,126)
  $lblToken.AutoSize=$true

  $txtToken=New-Object System.Windows.Forms.TextBox
  $txtToken.Location=New-Object System.Drawing.Point(324,146)
  $txtToken.Size=New-Object System.Drawing.Size(410,24)
  $txtToken.UseSystemPasswordChar=$true
  if([string]::IsNullOrWhiteSpace($global:Cfg.PushoverToken)){
    $txtToken.Text=''
  }else{
    $txtToken.Text='*****'
  }

  $updateConfigFromForm = {
    $global:Cfg.InstallDir   = $txtInstall.Text
    $global:Cfg.VRChatLogDir = $txtVR.Text
    if($txtUser.Text -ne '*****'){  $global:Cfg.PushoverUser  = $txtUser.Text }
    if($txtToken.Text -ne '*****'){ $global:Cfg.PushoverToken = $txtToken.Text }
  }

  $btnSaveRestart=New-Object System.Windows.Forms.Button
  $btnSaveRestart.Text='Save & Restart Monitoring'
  $btnSaveRestart.Location=New-Object System.Drawing.Point(12,180)
  $btnSaveRestart.Size=New-Object System.Drawing.Size(280,32)
  $btnSaveRestart.Add_Click({
    & $updateConfigFromForm
    Save-Config
    Start-Follow
    Show-Notification $AppName 'Settings saved & monitoring restarted.'
  })

  $btnStart=New-Object System.Windows.Forms.Button
  $btnStart.Text='Start Monitoring'
  $btnStart.Location=New-Object System.Drawing.Point(308,180)
  $btnStart.Size=New-Object System.Drawing.Size(180,32)
  $btnStart.Add_Click({
    Start-Follow
    Show-Notification $AppName 'Monitoring started.'
  })

  $btnStop=New-Object System.Windows.Forms.Button
  $btnStop.Text='Stop Monitoring'
  $btnStop.Location=New-Object System.Drawing.Point(500,180)
  $btnStop.Size=New-Object System.Drawing.Size(180,32)
  $btnStop.Add_Click({
    Stop-Follow
    Show-Notification $AppName 'Monitoring stopped.'
  })

  $btnAddStartup=New-Object System.Windows.Forms.Button
  $btnAddStartup.Text='Add to Startup'
  $btnAddStartup.Location=New-Object System.Drawing.Point(12,220)
  $btnAddStartup.Size=New-Object System.Drawing.Size(200,32)

  $btnRemoveStartup=New-Object System.Windows.Forms.Button
  $btnRemoveStartup.Text='Remove from Startup'
  $btnRemoveStartup.Location=New-Object System.Drawing.Point(220,220)
  $btnRemoveStartup.Size=New-Object System.Drawing.Size(260,32)

  $btnSave=New-Object System.Windows.Forms.Button
  $btnSave.Text='Save'
  $btnSave.Location=New-Object System.Drawing.Point(488,220)
  $btnSave.Size=New-Object System.Drawing.Size(110,32)
  $btnSave.Add_Click({
    & $updateConfigFromForm
    Save-Config
    Show-Notification $AppName 'Settings saved.'
  })

  $btnQuit=New-Object System.Windows.Forms.Button
  $btnQuit.Text='Quit'
  $btnQuit.Location=New-Object System.Drawing.Point(624,220)
  $btnQuit.Size=New-Object System.Drawing.Size(110,32)
  $btnQuit.Add_Click({ Exit-App })

  $updateStartupButtons = {
    $exists = Test-Path (Get-StartupShortcutPath)
    $btnAddStartup.Enabled = -not $exists
    $btnRemoveStartup.Enabled = $exists
  }

  $btnAddStartup.Add_Click({
    if(Add-Startup){ & $updateStartupButtons }
  })

  $btnRemoveStartup.Add_Click({
    if(Remove-Startup){ & $updateStartupButtons }
  })

  $form.Controls.AddRange(@(
    $lblInstall,$txtInstall,$btnBrowseInstall,
    $lblVR,$txtVR,$btnBrowseVR,
    $lblUser,$txtUser,$lblToken,$txtToken,
    $btnSaveRestart,$btnStart,$btnStop,
    $btnAddStartup,$btnRemoveStartup,$btnSave,$btnQuit
  ))

  & $updateStartupButtons

  $form.Add_Shown({ param($sender,$e) $sender.Activate() })
  $form.Add_FormClosed({ param($sender,$e) $global:SettingsForm=$null })

  $global:SettingsForm = $form
  [void]$form.ShowDialog()
}

# ---------------- Tray & pulse animation (ALL GLOBAL STATE) ----------------
function Init-Tray{
  if($global:TrayIcon){ return }
  $owner = New-Object System.Windows.Forms.Form
  $owner.ShowInTaskbar=$false; $owner.FormBorderStyle='FixedToolWindow'
  $owner.Opacity=0; $owner.WindowState='Minimized'
  $owner.Size=New-Object System.Drawing.Size(0,0); $owner.StartPosition='Manual'
  $owner.Location=New-Object System.Drawing.Point(-2000,-2000)
  $owner.Add_Shown({ param($sender,$e) $sender.Hide() })
  $owner.Show(); $global:HostForm=$owner

  $ni=New-Object System.Windows.Forms.NotifyIcon
  $ni.Visible=$true; $ni.Text=$AppName

  $launcherDir=Split-Path (Get-LauncherPath) -Parent
  $icoPath=Join-Path $launcherDir 'vrchat_join_notification\notification.ico'
  if(Test-Path $icoPath){
    try{ $ni.Icon = New-Object System.Drawing.Icon($icoPath) } catch { $ni.Icon=[System.Drawing.SystemIcons]::Information }
  } else { $ni.Icon=[System.Drawing.SystemIcons]::Information }
  $global:IconIdle   = $ni.Icon
  $global:IconPulseA = [System.Drawing.SystemIcons]::Application
  $global:IconPulseB = [System.Drawing.SystemIcons]::Information

  $menu=New-Object System.Windows.Forms.ContextMenuStrip
  $global:TrayMenu=$menu
  $menu.Items.Add('Settings...').Add_Click({ param($s,$e) Show-SettingsForm }) | Out-Null
  $menu.Items.Add('Restart Monitoring').Add_Click({ param($s,$e)
      Start-Follow
      Start-TrayPulse -Message 'Restarting monitor...' -Seconds 2.5 -IntervalMs 150
      Show-Notification $AppName 'Monitoring restarted.'
    }) | Out-Null
  [void]$menu.Items.Add('-')
  $menu.Items.Add('Exit').Add_Click({ param($s,$e) Exit-App }) | Out-Null

  $ni.ContextMenuStrip=$menu
  $ni.add_MouseUp({ param($sender,$e)
    if ($e.Button -eq [System.Windows.Forms.MouseButtons]::Right) {
      $m = $sender.ContextMenuStrip
      if($m){ $m.Show([System.Windows.Forms.Cursor]::Position) }
    }
  })
  $ni.add_DoubleClick({ Show-SettingsForm })
  $ni.add_BalloonTipClicked({ Show-SettingsForm })

  $global:TrayIcon=$ni
  $global:IdleTooltip=$AppName
}

function Start-TrayPulse{
  param(
    [string]$Message = 'Working...',
    [double]$Seconds = 2.0,
    [int]$IntervalMs = 120
  )
  if(-not $global:TrayIcon){ return }

  # Stop previous pulse if any
  if($global:PulseTimer){
    try{ $global:PulseTimer.Stop(); $global:PulseTimer.Dispose() }catch{}
    $global:PulseTimer=$null
  }

  $global:TrayIcon.Text = "$AppName $EmDash $Message"
  $global:PulseStopAt = (Get-Date).AddSeconds($Seconds)
  $global:PulseFrame = $false

  $global:PulseTimer = New-Object System.Windows.Forms.Timer
  $global:PulseTimer.Interval = [Math]::Max(60,$IntervalMs)
  $global:PulseTimer.Add_Tick({
    param($sender,$e)
    if((Get-Date) -ge $global:PulseStopAt){
      try{
        if($global:TrayIcon){ $global:TrayIcon.Icon = $global:IconIdle; $global:TrayIcon.Text = $global:IdleTooltip }
      }catch{}
      $sender.Stop(); $sender.Dispose(); $global:PulseTimer=$null
      return
    }
    $global:PulseFrame = -not $global:PulseFrame
    try{
      if($global:TrayIcon){
        if($global:PulseFrame){
          $global:TrayIcon.Icon = $global:IconPulseA
        }else{
          $global:TrayIcon.Icon = $global:IconPulseB
        }
      }
    }catch{}
  })
  $global:PulseTimer.Start()
}

# ---------------- Main loop ----------------
function Load-And-Start{
  Load-Config
  Init-Tray
  if($open_settings){ Show-SettingsForm }
  if([string]::IsNullOrWhiteSpace($global:Cfg.PushoverUser) -or [string]::IsNullOrWhiteSpace($global:Cfg.PushoverToken)){
    Show-SettingsForm
  }else{
    Start-Follow
  }
}
function Main{
  try{
    Init-SingleInstance
    Load-And-Start
    while($true){
      try{
        [System.Windows.Forms.Application]::DoEvents()
        if($script:OpenEvt.WaitOne(0)){ Show-SettingsForm }  # external "open settings" signal
        Process-FollowOutput
      }catch{}
      Start-Sleep -Milliseconds 150
    }
  }catch{
    [System.Windows.Forms.MessageBox]::Show("Unhandled error:`r`n" + $_.Exception.Message,$AppName,'OK','Error') | Out-Null
    [Environment]::Exit(1)
  }
}

Main

# Build to EXE (Windows PowerShell)
Install-Module ps2exe -Scope CurrentUser -Force
Invoke-ps2exe -InputFile .\vrchat-join-notification-with-pushover.ps1 -OutputFile .\vrchat-join-notification-with-pushover.exe `
  -Title 'VRChat Join Notifier' -IconFile .\vrchat_join_notification\notification.ico -NoConsole -STA -x64
#>

