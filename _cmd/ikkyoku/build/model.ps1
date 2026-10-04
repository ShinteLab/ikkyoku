# 焼き込む認識器(手元の recognize/model/)を扱う。使い方は 3 つ(2026-10-04):
#
#   -Update            手元を作り直す。suteme に書き出させて(-export)取り込む     task model:update
#   -Update -Dist <d>  手元を作り直す。指定した suteme の dist/ から取り込む       task model:update SUTEME_DIST=...
#   -Use <dir>         置いてある配布モデル(3 ファイル)を手元に写す               task build:embed MODEL_DIR=...
#   -Check             手元にモデルがあるかを確かめ、何を焼き込むかを出す         task build:embed の頭
#
# ⚠️ **ビルド(build:embed / local:deploy)は手元をそのまま焼き込む。suteme は見ない。**
# 以前はビルドのたびに suteme/dist を持ってきていたので、**何が焼き込まれるかがビルドした
# 瞬間の suteme の状態で決まっていた**(学習し直すと意図せず入り、書き出し直し忘れると古いまま)。
# 焼き込むモデルは「今回はこれを配る」と決めるもので、**入れ替えるのは model:update か
# MODEL_DIR を指定したときだけ**にした。
#
# **ikkyoku のリポジトリにはデータを置いていない**(recognize/model/ は .gitignore)。
# 10MB 級のバイナリを、学習し直すたびにコミットすることになるため。
#
# 手元の形は**配布モデルと同じ 3 ファイル**(predictor.bin.gz / strip.bin.gz / source.txt)。
# 配って置いてもらうモデル(%LOCALAPPDATA%\ikkyoku\model)と同じものなので、-Use で
# そのまま焼き込めるし、手元を zip にすればそのまま配れる。
#
# **gzip で持つ**(実測: 生 24.8MB → 10.6MB)。suteme 側の読み込み口が io.Reader を
# 取るので、展開したファイルを置く必要は無い(recognize/embedded.go)。
#
# ⚠️ **suteme の dist/ は `training.ExportCompact`(学習サーバの「配布用に書き出す」・
# `suteme-training -export`)が作るもの。** リポジトリ直下の training_data_v*.bin(全件)ではなく、
# 間引いた配布セットを配ること。
[CmdletBinding()]
param(
    [switch]$Update,
    # -Update で取り込む suteme の dist/。既定はワークスペースに 6 つ並べた構成
    # (shinte/ikkyoku と shinte/suteme が隣同士)の suteme に書き出させる。
    [string]$Dist,
    # 手元に写す配布モデルのディレクトリ。
    [string]$Use,
    [switch]$Check
)

$ErrorActionPreference = 'Stop'

# このスクリプトは <ikkyoku>/_cmd/ikkyoku/build/ に居る。
$appDir = Split-Path -Parent $PSScriptRoot
$root = Split-Path -Parent (Split-Path -Parent $appDir)
$modelDir = Join-Path $root 'recognize\model'

# 配布モデルのファイル名(recognize.PackPredictorFile ほか)。**焼き込み側は版を含まない名前**
# (training_data_v8 → predictor)。版が上がるたびに go:embed の行を書き換えることになるため。
# どの版かは source.txt に残す(recognize.EmbeddedSource)。
$packFiles = @('predictor.bin.gz', 'strip.bin.gz', 'source.txt')

function Read-Source([string]$dir) {
    $p = Join-Path $dir 'source.txt'
    if (Test-Path $p) { return (Get-Content -Path $p -Raw -Encoding UTF8).Trim([char]0xFEFF, ' ', "`r", "`n") }
    return ''
}

function Write-Lines([string[]]$lines) {
    $lines | ForEach-Object { Write-Host $_ }
}

# ---- -Update: suteme から作り直す ------------------------------------------

# dist/ を suteme に書き出させる(`suteme-training -export`)。成功したら true。
# **データディレクトリには suteme の場所を渡す** —— 学習データ(*.bin)は gitignore なので、
# suteme の worktree には無い(ここでの suteme は ikkyoku の隣の本体のチェックアウト)。
function Invoke-SutemeExport([string]$suteme) {
    Write-Host "suteme に配布セット(dist/)を書き出させます: $suteme"
    Write-Host '  (go run ./_cmd/suteme-training -export)'
    Push-Location $suteme
    try {
        & go run ./_cmd/suteme-training -export $suteme
        return ($LASTEXITCODE -eq 0)
    } catch {
        Write-Host "  書き出せませんでした: $_"
        return $false
    } finally {
        Pop-Location
    }
}

# 取り込めないときに「何をすればいいか」を出して止まる。
function Stop-NoDist([string[]]$searched, [string]$suteme, [bool]$explicit) {
    $lines = @('', 'suteme の配布セット(dist/)を用意できませんでした。手元のモデルはこれから作ります。', '')
    $lines += '  探した場所:'
    $lines += ($searched | ForEach-Object { "    $_" })
    $lines += ''
    if ($suteme) {
        $lines += '  suteme に書き出させようとしましたが、できませんでした(上に理由が出ています)。'
        $lines += '  学習データ(training_data_v*.bin)が suteme のリポジトリにあるか確かめて、手で書き出してください:'
        $lines += "    1. cd $suteme"
        $lines += '       go run ./_cmd/suteme-training -export'
        $lines += '    2. もう一度 wails3 task model:update'
    } elseif ($explicit) {
        $lines += '  指定した場所(SUTEME_DIST)にありません。dist/ は suteme のリポジトリの中に、'
        $lines += '  go run ./_cmd/suteme-training -export で作られます。'
    } else {
        $lines += '  suteme のリポジトリが ikkyoku の隣に見つかりません(shinte/ikkyoku と shinte/suteme を並べる構成が前提)。'
        $lines += '  別の場所にあるなら、その dist/ を指してください:'
        $lines += '    wails3 task model:update SUTEME_DIST=D:/path/to/suteme/dist'
    }
    $lines += ''
    Write-Lines $lines
    exit 1
}

function Update-FromSuteme([string]$dist) {
    $explicit = [bool]$dist
    $suteme = $null
    if ($explicit) {
        $searched = @($dist)
        $parent = Split-Path -Parent $dist
        if ($parent -and (Test-Path (Join-Path $parent 'go.mod'))) { $suteme = $parent }
        # **指定した dist/ はそのまま使う**(中身を選んで書き出したものかもしれない)。
        # 無いときだけ、書き出せるなら書き出させる。
        if (-not (Test-Path $dist)) {
            if ($suteme -and (Invoke-SutemeExport $suteme)) { $dist = Join-Path $suteme 'dist' }
            else { Stop-NoDist $searched $suteme $true }
        }
    } else {
        # 1 つめは 6 つを並べた通常の構成、2 つめはワークツリー
        # (<ikkyoku>/.claude/worktrees/<name>) から実行した場合。
        $workspace = Split-Path -Parent $root
        $wtWorkspace = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $root)))
        $sutemes = @((Join-Path $workspace 'suteme'), (Join-Path $wtWorkspace 'suteme'))
        $searched = $sutemes | ForEach-Object { Join-Path $_ 'dist' }
        $suteme = $sutemes | Where-Object { Test-Path (Join-Path $_ 'go.mod') } | Select-Object -First 1
        # ⚠️ **作り直すときは毎回書き出させる。** model:update は「手元を今の学習データにする」
        # 操作なので、古い dist/ を黙って使わない。
        if ($suteme -and (Invoke-SutemeExport $suteme)) { $dist = Join-Path $suteme 'dist' }
        else { Stop-NoDist $searched $suteme $false }
    }

    # **学習データの版は決め打ちしない**。以前は training_data_v7.bin と書いてあり、
    # suteme の配布セットが v8 になったところで通らなくなっていた。
    # dist にある training_data_v*.bin のうち版がいちばん大きいものを使う。
    $train = Get-ChildItem -Path $dist -Filter 'training_data_v*.bin' |
        Where-Object { $_.Name -match '^training_data_v(\d+)\.bin$' } |
        Sort-Object { [int]([regex]::Match($_.Name, '\d+').Value) } -Descending |
        Select-Object -First 1
    if (-not $train) {
        Stop-NoDist @("$dist (training_data_v*.bin がありません)") (Split-Path -Parent $dist) $explicit
    }
    Write-Host ("{0} を取り込みます({1:yyyy-MM-dd HH:mm} の書き出し)" -f $train.FullName, $train.LastWriteTime)

    New-Item -ItemType Directory -Force -Path $modelDir | Out-Null
    $pairs = @(
        @{ From = $train.Name;         To = 'predictor.bin.gz' },
        @{ From = 'strip_data_v1.bin'; To = 'strip.bin.gz' }
    )
    $names = @()
    foreach ($p in $pairs) {
        $src = Join-Path $dist $p.From
        if (-not (Test-Path $src)) {
            Stop-NoDist @("$src がありません") (Split-Path -Parent $dist) $explicit
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
        Write-Host ("  {0} -> {1}  {2:N0} -> {3:N0} bytes" -f $p.From, $p.To, (Get-Item $src).Length, (Get-Item $dst).Length)
        $names += $p.From
    }

    # 出所(画面とログに出る。焼き込むと元のファイル名が残らないため)。
    # ⚠️ **末尾の「(yyyy-mm-dd)」を崩さないこと** —— 配布モデルと焼き込みの新旧の比較に使う
    # (recognize.sourceDate)。日付は書き出した日(学習データの写しを作った日)。
    $stamp = $train.LastWriteTime.ToString('yyyy-MM-dd')
    Set-Content -Path (Join-Path $modelDir 'source.txt') -Encoding utf8 `
        -Value ("suteme/dist {0} ({1})" -f ($names -join ' + '), $stamp)
    Write-Host "手元のモデルを作り直しました: $modelDir"
}

# ---- -Use: 配布モデルを手元に写す ------------------------------------------

function Use-Pack([string]$pack) {
    if (-not (Test-Path (Join-Path $pack 'predictor.bin.gz'))) {
        Write-Lines @('', "MODEL_DIR に配布モデルがありません: $pack",
            '  配布モデルは predictor.bin.gz / strip.bin.gz / source.txt の 3 ファイルを置いたフォルダです',
            '  (wails3 task model:update で作った recognize/model/ や、%LOCALAPPDATA%\ikkyoku\model と同じ形)。', '')
        exit 1
    }
    New-Item -ItemType Directory -Force -Path $modelDir | Out-Null
    # ⚠️ **3 つとも揃えて写すこと。** 無いものを前の手元のまま残すと、別のモデルの判定器が混ざる
    # (駒種の推論器と盤の縁の判定器は 1 組。recognize/predictor.go)。go:embed は 3 つとも要るので、
    # 判定器が無ければ空のファイルを置く(読む側が「判定器なし」として扱う)。
    foreach ($f in $packFiles) {
        $src = Join-Path $pack $f
        $dst = Join-Path $modelDir $f
        if (Test-Path $src) {
            Copy-Item -Path $src -Destination $dst -Force
        } elseif ($f -eq 'source.txt') {
            Set-Content -Path $dst -Encoding utf8 -Value ("配布モデル {0}" -f $pack)
        } else {
            Set-Content -Path $dst -Value $null -NoNewline
            Write-Host "  ⚠ $f がありません(盤の位置が 1 マス滑ることがあります)"
        }
    }
    Write-Host "配布モデルを手元に写しました: $pack -> $modelDir"
}

# ---- -Check: 何を焼き込むかを出す ------------------------------------------

function Test-Local {
    $missing = $packFiles | Where-Object { -not (Test-Path (Join-Path $modelDir $_)) }
    if ($missing) {
        Write-Lines @('', "手元に焼き込むモデルがありません: $modelDir",
            ('  無いもの: ' + ($missing -join ' / ')), '',
            '  どちらかで入れてください:',
            '    wails3 task model:update                         suteme の学習データから作る',
            '    wails3 task build:embed MODEL_DIR=<フォルダ>      置いてある配布モデルを焼き込む',
            '  (local:deploy にも MODEL_DIR=<フォルダ> で渡せます)', '')
        exit 1
    }
    $src = Read-Source $modelDir
    $date = (Get-Item (Join-Path $modelDir 'predictor.bin.gz')).LastWriteTime
    Write-Host ("焼き込むモデル: {0}" -f $src)
    Write-Host ("  {0}(手元に入れた日時 {1:yyyy-MM-dd HH:mm})" -f $modelDir, $date)
}

if ($Update) { Update-FromSuteme $Dist }
if ($Use) { Use-Pack $Use }
if ($Check) { Test-Local }
if (-not ($Update -or $Use -or $Check)) {
    Write-Host '使い方: model.ps1 -Update [-Dist <suteme の dist>] | -Use <配布モデル> [-Check] | -Check'
    exit 2
}
