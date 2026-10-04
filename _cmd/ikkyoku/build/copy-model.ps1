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

if (-not $Dist) {
    # 1 つめは 6 つを並べた通常の構成、2 つめはワークツリー
    # (<ikkyoku>/.claude/worktrees/<name>) から実行した場合。
    $workspace = Split-Path -Parent $root
    $wtWorkspace = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $root)))
    $candidates = @((Join-Path $workspace 'suteme\dist'), (Join-Path $wtWorkspace 'suteme\dist'))
    $Dist = $candidates | Where-Object { Test-Path $_ } | Select-Object -First 1
}
if (-not $Dist -or -not (Test-Path $Dist)) {
    throw "suteme の dist/ が見つかりません。-Dist <パス> で指定してください。"
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
    throw "$Dist に training_data_v*.bin がありません。suteme の学習サーバで「配布用に書き出す」を実行してください。"
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
