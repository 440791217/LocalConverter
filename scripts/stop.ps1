param([string]$Address = 'http://127.0.0.1:18090')
$ErrorActionPreference = 'Stop'
$uri = [uri]$Address
if ($uri.Scheme -ne 'http' -or !$uri.IsLoopback -or $uri.UserInfo -or $uri.AbsolutePath -ne '/' -or $uri.Query -or $uri.Fragment) { throw 'Use a loopback LocalConverter base address.' }
$base = $Address.TrimEnd('/')
$health = Invoke-RestMethod ($base + '/health') -TimeoutSec 5
if ($health.service -ne 'localconverter') { throw 'Target is not LocalConverter.' }
$page = Invoke-WebRequest ($base + '/debug') -TimeoutSec 5
$token = ([regex]::Match($page.Content, 'name="localconverter-token" content="([a-f0-9]+)"')).Groups[1].Value
if (!$token) { throw 'Local diagnostic credential not found; no process was terminated.' }
Invoke-RestMethod ($base + '/shutdown') -Method Post -Headers @{Authorization = 'Bearer ' + $token} -TimeoutSec 5
