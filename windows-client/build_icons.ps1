Add-Type -AssemblyName System.Drawing

$srcPath = Join-Path (Get-Location) 'mac-box.png'
if (-not (Test-Path $srcPath)) {
    Write-Error "mac-box.png not found"
    exit 1
}

$srcImg = [System.Drawing.Image]::FromFile($srcPath)
$srcBmp = New-Object System.Drawing.Bitmap $srcImg

# 盒子主体的紧凑裁剪框
$cropX = 160
$cropY = 100
$cropW = 702
$cropH = 796
$cropRect = New-Object System.Drawing.Rectangle $cropX, $cropY, $cropW, $cropH

$sizes = @(256, 128, 64, 48, 32, 24, 16)
$bitmaps = @{}

foreach ($s in $sizes) {
    $canvas = New-Object System.Drawing.Bitmap $s, $s, ([System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $g = [System.Drawing.Graphics]::FromImage($canvas)
    $g.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
    $g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::HighQuality
    $g.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
    $g.CompositingQuality = [System.Drawing.Drawing2D.CompositingQuality]::HighQuality
    $g.Clear([System.Drawing.Color]::Transparent)

    # 4% 呼吸边距，确保立体投影和微光晕不被切边，同时占据 92% 画布
    $padding = [Math]::Max(1, [int]($s * 0.04))
    $availW = $s - ($padding * 2)
    $availH = $s - ($padding * 2)

    $scale = [Math]::Min(($availW / $cropW), ($availH / $cropH))
    $drawW = [int]($cropW * $scale)
    $drawH = [int]($cropH * $scale)
    $drawX = [int](($s - $drawW) / 2)
    $drawY = [int](($s - $drawH) / 2)

    $destRect = New-Object System.Drawing.Rectangle $drawX, $drawY, $drawW, $drawH
    $g.DrawImage($srcBmp, $destRect, $cropRect, [System.Drawing.GraphicsUnit]::Pixel)
    $g.Dispose()

    $bitmaps[$s] = $canvas
}

$srcImg.Dispose()
$srcBmp.Dispose()

# 保存 UI 高清 PNG
$bitmaps[256].Save((Join-Path (Get-Location) 'ui\icon.png'), [System.Drawing.Imaging.ImageFormat]::Png)
$bitmaps[256].Save((Join-Path (Get-Location) 'ui\icon-256.png'), [System.Drawing.Imaging.ImageFormat]::Png)
$bitmaps[64].Save((Join-Path (Get-Location) 'ui\icon-64.png'), [System.Drawing.Imaging.ImageFormat]::Png)
$bitmaps[32].Save((Join-Path (Get-Location) 'ui\icon-32.png'), [System.Drawing.Imaging.ImageFormat]::Png)

# 打包生成 Windows ICO
$icoOrder = @(256, 128, 64, 48, 32, 24, 16)
$items = New-Object System.Collections.ArrayList

foreach ($sz in $icoOrder) {
    $ms = New-Object System.IO.MemoryStream
    $bitmaps[$sz].Save($ms, [System.Drawing.Imaging.ImageFormat]::Png)
    $bytes = $ms.ToArray()
    $ms.Dispose()
    [void]$items.Add(@{ Size = $sz; Bytes = $bytes })
}

$icoPath = Join-Path (Get-Location) 'app.ico'
$fs = [System.IO.File]::Create($icoPath)
$bw = New-Object System.IO.BinaryWriter $fs

$count = $items.Count
$bw.Write([uint16]0)
$bw.Write([uint16]1)
$bw.Write([uint16]$count)

$dataOffset = 6 + ($count * 16)

foreach ($entry in $items) {
    $sz = $entry.Size
    $bytes = $entry.Bytes
    $wByte = if ($sz -ge 256) { [byte]0 } else { [byte]$sz }
    $hByte = if ($sz -ge 256) { [byte]0 } else { [byte]$sz }

    $bw.Write($wByte)
    $bw.Write($hByte)
    $bw.Write([byte]0)
    $bw.Write([byte]0)
    $bw.Write([uint16]1)
    $bw.Write([uint16]32)
    $bw.Write([uint32]$bytes.Length)
    $bw.Write([uint32]$dataOffset)

    $dataOffset += $bytes.Length
}

foreach ($entry in $items) {
    $bw.Write($entry.Bytes)
}

$bw.Flush()
$bw.Dispose()
$fs.Dispose()

foreach ($k in $bitmaps.Keys) {
    $bitmaps[$k].Dispose()
}

Copy-Item $icoPath (Join-Path (Get-Location) 'ui\app.ico') -Force
Copy-Item $icoPath (Join-Path (Get-Location) 'ui\favicon.ico') -Force

Write-Host "Icons generated successfully!"
Get-Item $icoPath, 'ui\icon.png', 'ui\favicon.ico' | Format-Table Name, Length, LastWriteTime
