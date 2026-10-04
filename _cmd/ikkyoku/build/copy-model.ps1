# 配布ビルド用に、suteme の配布セット(dist/)を ikkyoku へ持ってくる。
#
# **ikkyoku のリポジトリにはデータを置いていない**(recognize/model/ は .gitignore)。
# 20MB 級のバイナリを、学習し直すたびにコミットすることになるため。
# 焼き込みビルド(-tags embedmodel)の前にこれを一度走らせる。
#
#   task model:copy
#   wails3 build -tags embedmodel
#
# **gzip で持つ**(実測: 生 23.2MB → 9.9MB)。suteme 側の読み込み口が io.Reader を
# 取るので、展開したファイルを置く必要は無い(recognize/embedded.go)。
#
# ⚠️ **suteme の dist/ は `training.ExportCompact`(学習サーバの「配布用に書き出す」)が
# 作るもの。** リポジトリ直下の training_data_v7.bin(全件)ではなく、
# 間引いた配布セットを配ること。
[CmdletBinding()]
param(
    # suteme の配布セットの場所。既定はワークスペースに 6 つ並べた構成
    # (shinte/ikkyoku と shinte/suteme が隣同士)。
    [string]$Dist
)

$ErrorActionPreference = 'Stop'

# このスクリプトは <ikkyoku>/_cmd/ikkyoku/build/ に居る。
$appDir = Split-Path -Parent $PSScriptRoot
$root = Split-Path -Parent (Split-Path -Parent $appDir)
$modelDir = Join-Path $root 'recognize\model'

# 見つからないときに「何をすればいいか」を出して止まる(2026-10-04)。
# **dist/ は suteme の学習サーバが書き出すもの**で、ここで作れるものではない。
# 以前は「-Dist <パス> で指定してください」とだけ出していたが、task から呼ぶと
# -Dist は渡せず(wails3 task は -- の後ろを渡さない)、作り方も分からなかった。
function Stop-NoDist([string[]]$searched, [string]$suteme) {
    $lines = @('', 'suteme の配布セット(dist/)がありません。焼き込むデータはここから持ってきます。', '')
    $lines += '  探した場所:'
    $lines += ($searched | ForEach-Object { "    $_" })
    $lines += ''
    if ($suteme) {
        $lines += '  dist/ は suteme の学習サーバで書き出します:'
        $lines += "    1. cd $suteme"
        $lines += '       go run ./_cmd/suteme-training'
        $lines += '    2. ブラウザで http://localhost:8080 を開き、「履歴」タブの下の「配布用に書き出す」→「書き出す」'
        $lines += '       (サーバを起動したまま別のターミナルで  Invoke-RestMethod -Method Post http://localhost:8080/api/export  でもよい)'
        $lines += '    3. もう一度 task model:copy'
    } elseif ($Dist) {
        $lines += '  指定した場所(SUTEME_DIST)にありません。dist/ は suteme のリポジトリの中に、'
        $lines += '  学習サーバ(go run ./_cmd/suteme-training)の「履歴」タブ →「配布用に書き出す」で作られます。'
    } else {
        $lines += '  suteme のリポジトリが ikkyoku の隣に見つかりません(shinte/ikkyoku と shinte/suteme を並べる構成が前提)。'
        $lines += '  別の場所にあるなら、その dist/ を指してください:'
        $lines += '    wails3 task model:copy SUTEME_DIST=D:/path/to/suteme/dist'
    }
    $lines += ''
    $lines | ForEach-Object { Write-Host $_ }
    exit 1
}

$searched = @()
$sutemeFound = $null
if ($Dist) {
    $searched = @($Dist)
    # 指定した dist/ の親が suteme のリポジトリなら、書き出し方をそのまま案内できる。
    $parent = Split-Path -Parent $Dist
    if ($parent -and (Test-Path (Join-Path $parent 'go.mod'))) { $sutemeFound = $parent }
} else {
    # 1 つめは 6 つを並べた通常の構成、2 つめはワークツリー
    # (<ikkyoku>/.claude/worktrees/<name>) から実行した場合。
    $workspace = Split-Path -Parent $root
    $wtWorkspace = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $root)))
    $sutemes = @((Join-Path $workspace 'suteme'), (Join-Path $wtWorkspace 'suteme'))
    $searched = $sutemes | ForEach-Object { Join-Path $_ 'dist' }
    $Dist = $searched | Where-Object { Test-Path $_ } | Select-Object -First 1
    $sutemeFound = $sutemes | Where-Object { Test-Path (Join-Path $_ 'go.mod') } | Select-Object -First 1
}
if (-not $Dist -or -not (Test-Path $Dist)) {
    Stop-NoDist $searched $sutemeFound
}

# suteme 側のファイル名 → 焼き込み側の名前。
# **焼き込み側は版を含まない名前にしてある**(training_data_v8 → predictor)。
# 版が上がるたびに go:embed の行を書き換えることになるため。
# どの版を焼いたかは source.txt に残す(recognize.EmbeddedSource)。
#
# **学習データの版は決め打ちしない**(2026-10-04)。以前は training_data_v7.bin と書いてあり、
# suteme の配布セットが v8 になったところで model:copy が通らなくなっていた。
# dist にある training_data_v*.bin のうち版がいちばん大きいものを使う。
$train = Get-ChildItem -Path $Dist -Filter 'training_data_v*.bin' |
    Where-Object { $_.Name -match '^training_data_v(\d+)\.bin$' } |
    Sort-Object { [int]([regex]::Match($_.Name, '\d+').Value) } -Descending |
    Select-Object -First 1
if (-not $train) {
    # dist/ はあるが中身が無い(書き出しの途中で止まった など)。作り方は同じ。
    Stop-NoDist @("$Dist (training_data_v*.bin がありません)") (Split-Path -Parent $Dist)
}
$pairs = @(
    @{ From = $train.Name;            To = 'predictor.bin.gz' },
    @{ From = 'strip_data_v1.bin';    To = 'strip.bin.gz' }
)

New-Item -ItemType Directory -Force -Path $modelDir | Out-Null

$names = @()
foreach ($p in $pairs) {
    $src = Join-Path $Dist $p.From
    if (-not (Test-Path $src)) {
        throw "$src がありません。suteme の学習サーバで「配布用に書き出す」を実行してください。"
    }
    $dst = Join-Path $modelDir $p.To
    $in = [System.IO.File]::OpenRead($src)
    try {
        $out = [System.IO.File]::Create($dst)
        try {
            $gz = New-Object System.IO.Compression.GZipStream($out, [System.IO.Compression.CompressionLevel]::Optimal)
            try { $in.CopyTo($gz) } finally { $gz.Dispose() }
        } finally { $out.Dispose() }
    } finally { $in.Dispose() }
    $raw = (Get-Item $src).Length
    $packed = (Get-Item $dst).Length
    Write-Host ("{0} -> {1}  {2:N0} -> {3:N0} bytes" -f $p.From, $p.To, $raw, $packed)
    $names += $p.From
}

# 出所(画面とログに出る。焼き込むと元のファイル名が残らないため)。
$stamp = Get-Date -Format 'yyyy-MM-dd'
Set-Content -Path (Join-Path $modelDir 'source.txt') -Encoding utf8 `
    -Value ("suteme/dist {0} ({1})" -f ($names -join ' + '), $stamp)
Write-Host "焼き込み用のデータを $modelDir に置きました。"
