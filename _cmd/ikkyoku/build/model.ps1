# 焼き込む認識器(手元のモデル: _cmd/ikkyoku/model/)を扱う。使い方は 2 つ(2026-10-04):
#
#   -Use <dir>   置いてある配布モデルを手元に写す                 task build:embed MODEL_DIR=...
#   -Check       手元にモデルがあるかを確かめ、何を焼き込むかを出す   task build:embed の頭
#
# **手元のモデルを作るのは suteme**(ikkyoku からは呼ばない):
#
#   cd <suteme>
#   go run ./_cmd/suteme-training -export -gzip -out <ikkyoku>/_cmd/ikkyoku/model
#
# ⚠️ **ビルド(build:embed / local:deploy)は手元をそのまま焼き込む。suteme は見ない。**
# 以前はビルドのたびに suteme/dist を持ってきていたので、何が焼き込まれるかがビルドした
# 瞬間の suteme の状態で決まっていた。**入れ替えるのは suteme で書き出したときと、
# MODEL_DIR を指定したときだけ。**
# ⚠️ **手元(model/)は git に入れない**(_cmd/ikkyoku/.gitignore。10MB 級を学習し直すたびに
# コミットすることになる)。
#
# 手元の形は**配布モデルと同じ**(suteme の配布用の書き出しそのもの: training_data_v*.bin(.gz) /
# strip_data_v1.bin(.gz) / export.json)。配って置いてもらうモデル(%LOCALAPPDATA%\ikkyoku\model)も
# 同じなので、-Use でそのまま焼き込めるし、手元を zip にすればそのまま配れる。
[CmdletBinding()]
param(
    # 手元に写す配布モデルのディレクトリ。
    [string]$Use,
    [switch]$Check
)

$ErrorActionPreference = 'Stop'

# このスクリプトは <ikkyoku>/_cmd/ikkyoku/build/ に居る。
$appDir = Split-Path -Parent $PSScriptRoot
$root = Split-Path -Parent (Split-Path -Parent $appDir)
$modelDir = Join-Path $appDir 'model'

# 配布モデルのファイル(suteme の配布用の書き出し)。版は名前に入っている(training_data_v8 など)。
$packPatterns = @('training_data_v*.bin', 'training_data_v*.bin.gz', 'strip_data_v*.bin', 'strip_data_v*.bin.gz', 'export.json')

function Write-Lines([string[]]$lines) {
    $lines | ForEach-Object { Write-Host $_ }
}

function Get-PackFiles([string]$dir) {
    if (-not (Test-Path $dir)) { return @() }
    $packPatterns | ForEach-Object { Get-ChildItem -Path $dir -Filter $_ -File -ErrorAction SilentlyContinue }
}

# suteme で手元を作るコマンド(ikkyoku の隣の suteme を見つけられれば、そのまま貼れる形で)。
function Get-ExportHint {
    $workspace = Split-Path -Parent $root
    $wtWorkspace = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $root)))
    $suteme = @((Join-Path $workspace 'suteme'), (Join-Path $wtWorkspace 'suteme')) |
        Where-Object { Test-Path (Join-Path $_ 'go.mod') } | Select-Object -First 1
    $cd = if ($suteme) { "cd $suteme" } else { 'cd <suteme のリポジトリ>' }
    return @("    $cd", "    go run ./_cmd/suteme-training -export -gzip -out `"$modelDir`"")
}

# ---- -Use: 配布モデルを手元に写す ------------------------------------------

function Use-Pack([string]$pack) {
    $files = @(Get-PackFiles $pack)
    if (-not ($files | Where-Object { $_.Name -like 'training_data_v*' })) {
        Write-Lines @('', "MODEL_DIR に配布モデルがありません: $pack",
            '  配布モデルは suteme の配布用の書き出し(training_data_v*.bin(.gz) / strip_data_v1.bin(.gz) / export.json)を',
            '  置いたフォルダです(手元の _cmd/ikkyoku/model/ や %LOCALAPPDATA%\ikkyoku\model と同じ形)。', '')
        exit 1
    }
    New-Item -ItemType Directory -Force -Path $modelDir | Out-Null
    # ⚠️ **手元を空にしてから写すこと。** 前のモデルのファイルが残ると、別のモデルの判定器や
    # 版違いの学習データが混ざる(駒種の推論器と盤の縁の判定器は 1 組。recognize/predictor.go)。
    # 手元は丸ごと git の外で、ここに置くのは配布モデルだけなので、中身は全部消してよい。
    Get-ChildItem -Path $modelDir -File | Remove-Item -Force
    foreach ($f in $files) {
        Copy-Item -Path $f.FullName -Destination $modelDir -Force
    }
    Write-Host "配布モデルを手元に写しました: $pack -> $modelDir"
}

# ---- -Check: 何を焼き込むかを出す ------------------------------------------

function Test-Local {
    $files = @(Get-PackFiles $modelDir)
    if (-not ($files | Where-Object { $_.Name -like 'training_data_v*' })) {
        $lines = @('', "手元に焼き込むモデルがありません: $modelDir", '',
            '  suteme で書き出してください(手元のモデルはこれで作ります):')
        $lines += Get-ExportHint
        $lines += @('', '  置いてある配布モデルを焼き込むなら:',
            '    wails3 task build:embed MODEL_DIR=<フォルダ>',
            '  (local:deploy にも MODEL_DIR=<フォルダ> で渡せます)', '')
        Write-Lines $lines
        exit 1
    }
    $info = Join-Path $modelDir 'export.json'
    $desc = ''
    if (Test-Path $info) {
        try {
            $j = Get-Content -Path $info -Raw -Encoding UTF8 | ConvertFrom-Json
            $desc = '{0:yyyy-MM-dd HH:mm} の書き出し・{1} サンプル' -f ([datetime]$j.date), $j.samples
        } catch {
            $desc = 'export.json を読めません'
        }
    } else {
        $desc = '書き出しの記録(export.json)がありません'
    }
    Write-Host ("焼き込むモデル: {0}" -f $desc)
    Write-Host ("  {0}: {1}" -f $modelDir, (($files | ForEach-Object { $_.Name }) -join ' / '))
    Write-Host '  作り直すなら(学習し直したあと):'
    Write-Lines (Get-ExportHint)
}

if ($Use) { Use-Pack $Use }
if ($Check) { Test-Local }
if (-not ($Use -or $Check)) {
    Write-Host '使い方: model.ps1 -Use <配布モデル> [-Check] | -Check'
    exit 2
}
