# Installs empirio, which starts coding agents on EmpirioLabs AI models.
#
#   irm https://empiriolabs.ai/install.ps1 | iex
#
# EMPIRIO_VERSION         install this version instead of the latest (for example 0.1.0)
# EMPIRIO_INSTALL_DIR     install here instead of %LOCALAPPDATA%\Programs\empirio
# EMPIRIO_NO_MODIFY_PATH  set to leave your PATH unchanged

& {
	$ErrorActionPreference = 'Stop'
	$ProgressPreference = 'SilentlyContinue'
	[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

	$releases = 'https://github.com/EmpirioLabs-AI/cli/releases'
	$arch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
	switch ($arch) {
		'AMD64' { $arch = 'amd64' }
		'ARM64' { $arch = 'arm64' }
		default { throw "empirio runs on 64-bit Windows (amd64 or arm64); this is $arch." }
	}
	if ($env:EMPIRIO_VERSION) {
		$version = $env:EMPIRIO_VERSION.TrimStart('v')
		if ($version -notmatch '^[0-9A-Za-z.-]+$') { throw 'EMPIRIO_VERSION must look like 0.1.0.' }
		$base = "$releases/download/v$version"
	} else {
		$base = "$releases/latest/download"
	}
	$asset = "empirio_windows_$arch.zip"
	$dir = if ($env:EMPIRIO_INSTALL_DIR) { $env:EMPIRIO_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\empirio' }

	$tmp = Join-Path ([IO.Path]::GetTempPath()) ('empirio-' + [guid]::NewGuid().ToString('N'))
	New-Item -ItemType Directory -Path $tmp | Out-Null
	try {
		Write-Host "Downloading empirio for windows/$arch..."
		$sums = Join-Path $tmp 'SHA256SUMS'
		Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS" -OutFile $sums
		Invoke-WebRequest -UseBasicParsing -Uri "$base/SHA256SUMS.sig" -OutFile "$sums.sig"
		# The release key that signs SHA256SUMS. Windows' own ssh-keygen checks
		# the signature; without it only the checksum is checked.
		$signers = Join-Path $tmp 'allowed_signers'
		Set-Content -Path $signers -Encoding ascii -Value 'release@empiriolabs.ai namespaces="empirio-release" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGBE/2Npm/ixxdwNX+ldPJnpzlh8sp/3T8nHD5LtoXKb'
		$keygen = Join-Path $env:SystemRoot 'System32\OpenSSH\ssh-keygen.exe'
		if (-not (Test-Path $keygen)) { $keygen = (Get-Command ssh-keygen -ErrorAction SilentlyContinue).Source }
		if ($keygen) {
			# The file goes to ssh-keygen byte for byte; a PowerShell pipe would re-encode it.
			$out = Join-Path $tmp 'verify.out'
			$p = Start-Process -FilePath $keygen -NoNewWindow -Wait -PassThru `
				-ArgumentList @('-Y', 'verify', '-f', "`"$signers`"", '-I', 'release@empiriolabs.ai', '-n', 'empirio-release', '-s', "`"$sums.sig`"") `
				-RedirectStandardInput $sums -RedirectStandardOutput $out -RedirectStandardError "$out.err"
			$said = (Get-Content -Raw $out, "$out.err" -ErrorAction SilentlyContinue) -join ''
			if ($p.ExitCode -eq 0) {
				Write-Host 'Release signature verified.'
			} elseif ($said -match 'unknown option|illegal option|usage:') {
				Write-Host 'note: this ssh-keygen cannot check signatures, so only the checksum is checked'
			} else {
				throw 'The release signature did not verify; nothing was installed.'
			}
		} else {
			Write-Host 'note: ssh-keygen is not installed, so only the checksum is checked'
		}
		Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile (Join-Path $tmp $asset)
		$expected = $null
		foreach ($line in Get-Content (Join-Path $tmp 'SHA256SUMS')) {
			$parts = $line.Trim() -split '\s+'
			if ($parts.Count -eq 2 -and $parts[1].TrimStart('*') -eq $asset) { $expected = $parts[0] }
		}
		if (-not $expected) { throw "$asset is not listed in SHA256SUMS; nothing was installed." }
		$actual = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $tmp $asset)).Hash
		if ($actual -ne $expected) { throw "Checksum mismatch for $asset; nothing was installed." }

		Expand-Archive -Path (Join-Path $tmp $asset) -DestinationPath (Join-Path $tmp 'x') -Force
		$new = Join-Path $tmp 'x\empirio.exe'
		if (-not (Test-Path $new)) { throw 'The download has no empirio.exe.' }
		New-Item -ItemType Directory -Force -Path $dir | Out-Null
		$target = Join-Path $dir 'empirio.exe'
		# A running program cannot be overwritten but can be renamed; empirio
		# deletes the old copy the next time it starts.
		if (Test-Path $target) {
			Remove-Item "$target.old" -Force -ErrorAction SilentlyContinue
			Move-Item $target "$target.old" -Force
		}
		Copy-Item $new $target -Force
		$installed = & $target --version
		Write-Host "Installed $installed to $target"

		$onPath = ($env:Path -split ';') | Where-Object { $_ -and ($_.TrimEnd('\') -ieq $dir.TrimEnd('\')) }
		if (-not $onPath) {
			if ($env:EMPIRIO_NO_MODIFY_PATH) {
				Write-Host "Add $dir to your PATH to run empirio."
			} else {
				# Edit the user's Path in place, keeping its type, so entries such
				# as %USERPROFILE%\bin stay unexpanded.
				$key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
				try {
					$current = $key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
					$kind = if ($current) { $key.GetValueKind('Path') } else { [Microsoft.Win32.RegistryValueKind]::ExpandString }
					$entries = @($current -split ';' | Where-Object { $_ })
					if (-not ($entries | Where-Object { $_.TrimEnd('\') -ieq $dir.TrimEnd('\') })) {
						$key.SetValue('Path', (($entries + $dir) -join ';'), $kind)
					}
				} finally {
					$key.Close()
				}
				# Tell running programs so new terminals see the change.
				if (-not ('EmpirioInstall.Env' -as [type])) {
					Add-Type -Namespace EmpirioInstall -Name Env -MemberDefinition @'
[DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Auto)]
public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam, uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);
'@
				}
				$result = [UIntPtr]::Zero
				[void][EmpirioInstall.Env]::SendMessageTimeout([IntPtr]0xffff, 0x1A, [UIntPtr]::Zero, 'Environment', 2, 5000, [ref]$result)
				$env:Path = "$dir;$env:Path"
				Write-Host "Added $dir to your PATH. New terminals will find empirio."
			}
		}
		Write-Host ''
		Write-Host "Run 'empirio' to sign in and start a coding agent."
	} finally {
		Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
	}
}
