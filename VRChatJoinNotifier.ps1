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
$AppName        = 'VRChat Join Notifier'
$ConfigFileName = 'config.json'
$AppLogName     = 'notifier.log'
$POUrl          = 'https://api.pushover.net/1/messages.json'

$DefaultInstallDir   = Join-Path $env:LOCALAPPDATA 'VRChatJoinNotifier'
$DefaultVRChatLogDir = Join-Path ($env:LOCALAPPDATA -replace '\\Local$', '\LocalLow') 'VRChat\VRChat'

# ---------------- Cooldown (anti-spam) ----------------
$NotifyCooldownSeconds = 10
$script:LastNotified = @{}
$script:SessionId = 0         # increments on OnJoinedRoom
$script:SeenPlayers = @{}     # name -> first seen time (per session)

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
function Notify-All($key,$title,$body){
  $now=Get-Date
  if($script:LastNotified.ContainsKey($key)){
    if(($now - $script:LastNotified[$key]).TotalSeconds -lt $NotifyCooldownSeconds){
      Write-AppLog ("Suppressed '" + $key + "' within cooldown."); return
    }
  }
  $script:LastNotified[$key]=$now
  Show-Notification $title $body
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
  $best = if ($a -gt $b) { $a } else { $b }
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

    function Score-LogFile([System.IO.FileInfo]$f){
      $a = $f.LastWriteTimeUtc
      $b = $f.CreationTimeUtc
      $best = if ($a -gt $b) { $a } else { $b }
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

    $reSelf = [regex]'(?i)^\s*\[Behaviour\]\s*OnJoinedRoom\b'
    $reJoin = [regex]'(?i)^\s*\[Behaviour\]\s*OnPlayerJoined(?:\s*[:\-]?\s*(?<name>.+))?$'

    $logPath = $InitialLogPath
    $lastSize=(Get-Item -LiteralPath $logPath -ErrorAction SilentlyContinue).Length
    if(-not $lastSize){ $lastSize=0L }

    while($true){
      $maybe = Get-Newest $LogDir
      if($maybe -and $maybe -ne $logPath){
        $logPath = $maybe
        $lastSize=(Get-Item -LiteralPath $logPath -ErrorAction SilentlyContinue).Length
        if(-not $lastSize){ $lastSize=0L }
        Write-Output ("SWITCHED||" + $logPath)
      }

      if(-not(Test-Path $logPath)){ Start-Sleep -Milliseconds 800; continue }

      $fs=[System.IO.File]::Open($logPath,[System.IO.FileMode]::Open,[System.IO.FileAccess]::Read,[System.IO.FileShare]::ReadWrite)
      try{
        $sr=New-Object System.IO.StreamReader($fs)
        $fs.Seek($lastSize,[System.IO.SeekOrigin]::Begin) | Out-Null
        while(-not $sr.EndOfStream){
          $line=$sr.ReadLine()

          if($reSelf.IsMatch($line)){
            Write-Output ("SELF_JOIN||" + $line)
            continue
          }
          $m=$reJoin.Match($line)
          if($m.Success){
            $name=$m.Groups['name'].Value.Trim()
            if(-not $name){ $name='Someone' }
            Write-Output ("PLAYER_JOIN||" + $name + "||" + $line)
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
    $out = Receive-Job -Id $global:FollowJob.Id -Keep -ErrorAction SilentlyContinue
    foreach($s in $out){
      if($s -isnot [string]){ continue }

      if($s.StartsWith('SWITCHED||')){
        $p=$s.Substring(10)
        Write-AppLog ("Switching to newest log: " + $p)
        continue
      }

      if(-not (Is-VRChatRunning)) { continue }

      if($s.StartsWith('SELF_JOIN||')){
        $script:SessionId++
        $script:SeenPlayers = @{}
        Notify-All ("self:" + $script:SessionId) $AppName 'You joined an instance.'
        Write-AppLog ("Session " + $script:SessionId + " started (OnJoinedRoom).")
        continue
      }

      if($s.StartsWith('PLAYER_JOIN||')){
        if($script:SessionId -le 0){ continue } # ignore before room
        $parts=$s.Split('||',3)
        $name = if($parts.Length -ge 2 -and $parts[1]){ $parts[1] } else { 'Someone' }
        if(-not $script:SeenPlayers.ContainsKey($name)){
          $script:SeenPlayers[$name]=(Get-Date)
          Notify-All ("join:" + $script:SessionId + ":" + $name) $AppName ($name + " joined your instance.")
          Write-AppLog ("Session " + $script:SessionId + ": player joined '" + $name + "'.")
        }
        continue
      }
    }
  }catch{ Write-AppLog ("Receive-Job error: " + $_.Exception.Message) }
}

# ---------------- Startup shortcut ----------------
function Get-StartupFolder{ [Environment]::GetFolderPath('Startup') }
function Add-Startup{
  try{
    $startup=Get-StartupFolder; Ensure-Dir $startup
    $launcher=Get-LauncherPath
    $lnk=Join-Path $startup 'VRChatJoinNotifier.lnk'
    $wsh=New-Object -ComObject WScript.Shell
    $sc=$wsh.CreateShortcut($lnk)
    if($launcher.ToLower().EndsWith('.exe')){ $sc.TargetPath=$launcher; $sc.Arguments='' }
    else{
      $sc.TargetPath="$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe"
      $sc.Arguments="-WindowStyle Hidden -ExecutionPolicy Bypass -File `"$launcher`""
    }
    $sc.WorkingDirectory=Split-Path $launcher -Parent
    $ico=Join-Path (Split-Path $launcher -Parent) 'notification.ico'
    if(Test-Path $ico){ $sc.IconLocation=$ico }
    $sc.Save()
    Show-Notification $AppName 'Added to Startup.'
  }catch{ Show-Notification $AppName ("Failed to add Startup: " + $_.Exception.Message) }
}
function Remove-Startup{
  try{
    $lnk=Join-Path (Get-StartupFolder) 'VRChatJoinNotifier.lnk'
    if(Test-Path $lnk){ Remove-Item $lnk -Force }
    Show-Notification $AppName 'Removed from Startup.'
  }catch{ Show-Notification $AppName ("Failed to remove Startup: " + $_.Exception.Message) }
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
  $form.Text="$AppName – Settings"
  $form.Size=New-Object System.Drawing.Size(700,260)
  $form.StartPosition='CenterScreen'

  $lblInstall=New-Object System.Windows.Forms.Label
  $lblInstall.Text='Install Folder (logs/cache):'
  $lblInstall.Location=New-Object System.Drawing.Point(12,12)
  $lblInstall.AutoSize=$true

  $txtInstall=New-Object System.Windows.Forms.TextBox
  $txtInstall.Location=New-Object System.Drawing.Point(12,32)
  $txtInstall.Size=New-Object System.Drawing.Size(560,22)
  $txtInstall.Text=$global:Cfg.InstallDir

  $btnBrowseInstall=New-Object System.Windows.Forms.Button
  $btnBrowseInstall.Text='Browse...'
  $btnBrowseInstall.Location=New-Object System.Drawing.Point(580,30)
  $btnBrowseInstall.Size=New-Object System.Drawing.Size(90,24)
  $btnBrowseInstall.Add_Click({ param($sender,$e)
    $dlg=New-Object System.Windows.Forms.FolderBrowserDialog
    if($dlg.ShowDialog() -eq 'OK'){ $txtInstall.Text=$dlg.SelectedPath }
  })

  $lblVR=New-Object System.Windows.Forms.Label
  $lblVR.Text='VRChat Log Folder:'
  $lblVR.Location=New-Object System.Drawing.Point(12,60)
  $lblVR.AutoSize=$true

  $txtVR=New-Object System.Windows.Forms.TextBox
  $txtVR.Location=New-Object System.Drawing.Point(12,80)
  $txtVR.Size=New-Object System.Drawing.Size(560,22)
  $txtVR.Text=$global:Cfg.VRChatLogDir

  $btnBrowseVR=New-Object System.Windows.Forms.Button
  $btnBrowseVR.Text='Browse...'
  $btnBrowseVR.Location=New-Object System.Drawing.Point(580,78)
  $btnBrowseVR.Size=New-Object System.Drawing.Size(90,24)
  $btnBrowseVR.Add_Click({ param($sender,$e)
    $dlg=New-Object System.Windows.Forms.FolderBrowserDialog
    if($dlg.ShowDialog() -eq 'OK'){ $txtVR.Text=$dlg.SelectedPath }
  })

  $lblUser=New-Object System.Windows.Forms.Label
  $lblUser.Text='Pushover User Key:'
  $lblUser.Location=New-Object System.Drawing.Point(12,110)
  $lblUser.AutoSize=$true

  $txtUser=New-Object System.Windows.Forms.TextBox
  $txtUser.Location=New-Object System.Drawing.Point(12,130)
  $txtUser.Size=New-Object System.Drawing.Size(300,22)
  $txtUser.UseSystemPasswordChar=$true
  $txtUser.Text= (if([string]::IsNullOrWhiteSpace($global:Cfg.PushoverUser)) {''} else {'*****'})

  $lblToken=New-Object System.Windows.Forms.Label
  $lblToken.Text='Pushover API Token:'
  $lblToken.Location=New-Object System.Drawing.Point(340,110)
  $lblToken.AutoSize=$true

  $txtToken=New-Object System.Windows.Forms.TextBox
  $txtToken.Location=New-Object System.Drawing.Point(340,130)
  $txtToken.Size=New-Object System.Drawing.Size(330,22)
  $txtToken.UseSystemPasswordChar=$true
  $txtToken.Text= (if([string]::IsNullOrWhiteSpace($global:Cfg.PushoverToken)) {''} else {'*****'})

  $btnAddStartup=New-Object System.Windows.Forms.Button
  $btnAddStartup.Text='Add to Startup'
  $btnAddStartup.Location=New-Object System.Drawing.Point(12,170)
  $btnAddStartup.Size=New-Object System.Drawing.Size(120,28)
  $btnAddStartup.Add_Click({ param($sender,$e) Add-Startup })

  $btnRemoveStartup=New-Object System.Windows.Forms.Button
  $btnRemoveStartup.Text='Remove from Startup'
  $btnRemoveStartup.Location=New-Object System.Drawing.Point(142,170)
  $btnRemoveStartup.Size=New-Object System.Drawing.Size(160,28)
  $btnRemoveStartup.Add_Click({ param($sender,$e) Remove-Startup })

  $btnSave=New-Object System.Windows.Forms.Button
  $btnSave.Text='Save'
  $btnSave.Location=New-Object System.Drawing.Point(540,170)
  $btnSave.Size=New-Object System.Drawing.Size(60,28)
  $btnSave.Add_Click({ param($sender,$e)
    $global:Cfg.InstallDir   = $txtInstall.Text
    $global:Cfg.VRChatLogDir = $txtVR.Text
    if($txtUser.Text -ne '*****'){  $global:Cfg.PushoverUser  = $txtUser.Text }
    if($txtToken.Text -ne '*****'){ $global:Cfg.PushoverToken = $txtToken.Text }
    Save-Config
    Start-Follow
    Show-Notification $AppName 'Settings saved & monitoring restarted.'
  })

  $btnClose=New-Object System.Windows.Forms.Button
  $btnClose.Text='Close'
  $btnClose.Location=New-Object System.Drawing.Point(610,170)
  $btnClose.Size=New-Object System.Drawing.Size(60,28)
  $btnClose.Add_Click({ param($sender,$e) ($sender.FindForm()).Close() })

  $form.Controls.AddRange(@(
    $lblInstall,$txtInstall,$btnBrowseInstall,
    $lblVR,$txtVR,$btnBrowseVR,
    $lblUser,$txtUser,$lblToken,$txtToken,
    $btnAddStartup,$btnRemoveStartup,$btnSave,$btnClose
  ))

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
  $icoPath=Join-Path $launcherDir 'notification.ico'
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
      Start-TrayPulse -Message 'Restarting monitor…' -Seconds 2.5 -IntervalMs 150
      Show-Notification $AppName 'Monitoring restarted.'
    }) | Out-Null
  [void]$menu.Items.Add('-')
  $menu.Items.Add('Exit').Add_Click({ param($s,$e)
      Stop-Follow
      try{
        if($global:TrayIcon){ $global:TrayIcon.Visible=$false; $global:TrayIcon.Dispose() }
        if($global:HostForm){ $global:HostForm.Close(); $global:HostForm.Dispose() }
        if($script:Mutex){ $script:Mutex.ReleaseMutex() | Out-Null }
      }catch{}
      [System.Windows.Forms.Application]::Exit(); [Environment]::Exit(0)
    }) | Out-Null

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
    [string]$Message = 'Working…',
    [double]$Seconds = 2.0,
    [int]$IntervalMs = 120
  )
  if(-not $global:TrayIcon){ return }

  # Stop previous pulse if any
  if($global:PulseTimer){
    try{ $global:PulseTimer.Stop(); $global:PulseTimer.Dispose() }catch{}
    $global:PulseTimer=$null
  }

  $global:TrayIcon.Text = $AppName + " — " + $Message
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
        $global:TrayIcon.Icon = (if($global:PulseFrame){ $global:IconPulseA } else { $global:IconPulseB })
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

<# Build to EXE (Windows PowerShell)
Install-Module ps2exe -Scope CurrentUser -Force
Invoke-ps2exe -InputFile .\VRChatJoinNotifier.ps1 -OutputFile .\VRChatJoinNotifier.exe `
  -Title 'VRChat Join Notifier' -IconFile .\notification.ico -NoConsole -STA -x64
#>
