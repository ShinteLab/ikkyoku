---
name: ikkyoku-build
description: ikkyoku（一局）をビルドする・動かす・配る。go build / go test の打ち場所、Wails3 の bindings 生成（クローン直後と worktree で必ず要る）、frontend の npm、認識器を焼き込んだ配布ビルド、git worktree で replace が解決できないときのジャンクション。「ビルドが通らない」「Cannot find module '../bindings/...'」「TS2307 が延々と出る」「配布用の exe を作る」「worktree で ../suteme が見つからない」ときに使う。
---

# ikkyoku をビルドする

⚠️ **`go` コマンドはこの `ikkyoku/` ディレクトリで打つ。** ワークスペース
（`ShinteLab/shinte`）のルートに go.mod は無い。

⚠️ **モジュールは 2 つある。** ルート（`github.com/ShinteLab/ikkyoku`）と
`_cmd/ikkyoku`（Wails3 アプリ。**独立したネストモジュール**）。
`go build ./...` は**アンダースコア始まりのディレクトリを見ない**ので、
`_cmd/ikkyoku` は別に打つ。

## ふだんの確認

```powershell
cd ikkyoku
go build ./...      # app / guide / position / analyze / recognize / legal / piecefont / training / usi
go vet ./...
go test ./...
```

**普段直すのはこちら側**（Service は `app/`、枠の幾何は `guide/`）。
`_cmd/ikkyoku` は 7 ファイル・2,745 行しか無い。

```powershell
cd _cmd\ikkyoku
go test .           # clipboard の往復（⚠️ クリップボードの中身が置き換わる）と認識器の読み込み元
```

## Wails3 アプリ

```powershell
cd ikkyoku\_cmd\ikkyoku
npm --prefix frontend install         # 初回のみ
wails3 generate bindings -ts -i       # ⚠️ クローン直後にも要る（下記）／Service・Model を変えたら必ず
wails3 dev                            # 開発モード
wails3 build                          # frontend ビルド〜bindings 生成〜go build まで一括
go build -o bin\ikkyoku.exe .         # Go だけを素早く確認したいとき（frontend/dist が要る）
```

フロントだけを確かめるなら:

```powershell
cd _cmd\ikkyoku\frontend
npx tsc            # 型
node check-css.mjs # CSS のコメントの対応（閉じ忘れると .board-stage のルールが丸ごと消える）
npm run build      # 上の 2 つ + vite build
```

### ⚠️ `frontend/bindings/` は生成物で、git に入っていない

**クローン直後・worktree を作った直後は存在しない**（`.gitignore` 済み）。
**`npm run build` や `tsc` を打つ前に一度生成すること。**

忘れると `Cannot find module '../bindings/...'`（TS2307）が延々と出る。
⚠️ **パスの間違いと区別が付きにくい** —— 2026-09-04 に Service を `ikkyoku/app` へ
移して import 先が `bindings/ikkyoku-app` から
`bindings/github.com/ShinteLab/ikkyoku/app` などに変わったので、
**同じエラーが「生成していない」でも「パスが古い」でも出る**。
**まず生成してから疑うこと。**

- **`wails3 build` / `wails3 dev` は自動で生成する**（`build/Taskfile.yml` の
  `build:frontend` が `generate:bindings` に依存している）。手で打つ必要があるのは
  **`npm run build` / `npx tsc` を単独で走らせるとき**だけ
- **生成は決定論的。** bindings を丸ごと消して `wails3 generate bindings -ts -i` を
  打つと**バイト単位で同じものが戻る**ことを確認済み（2026-09-04）。
  消えていても慌てて git から戻さないこと
- `wails3 generate bindings` は Taskfile（`build/Taskfile.yml` の `generate:bindings`）と
  同じ `-ts -i` を付けること（wails3 skill pitfalls.md 12 の「フラグの食い違いで
  `wails3 dev` の 1 回目だけ失敗する」問題を避けるため）
- ⚠️ **Taskfile 側は `-clean=true` も付けている**（生成前に消す）。手で打つときは
  付かないので、**パッケージを移動・改名したときは古いディレクトリが残る**
  （実際 `bindings/ikkyoku-app/` が残った）。**移動したら手で消すこと**

### ⚠️ `//wails:ignore` を落とさない

`main` から呼ぶだけの口（`SettingsService.Config` / `KifuService.Open` / `Close` /
`AnalyzeService.Close`）には付けてある。**付けないと bindings に出てフロント API になる。**

## 配る exe（認識器を焼き込む）

```powershell
cd ikkyoku\_cmd\ikkyoku
task model:copy         # suteme/dist → recognize/model/*.gz + source.txt
task build:embed        # model:copy + wails3 のビルド（EXTRA_TAGS=embedmodel）
```

- ⚠️ **`wails3 build` は焼き込まない**（タグが付かない）。配るのは `task build:embed`
- ⚠️ **`BUILD_FLAGS` を上書きしないこと。** あちらには `-tags production` も
  `-H windowsgui`（コンソールを出さない）も入っている。タグを足す口は `EXTRA_TAGS`
- ⚠️ **`wails3 dev` と `wails3 package` に `-tags` は無い。** 開発モードで焼き込みを
  試すなら `task build:embed` した exe を直接起動する
- ⚠️ **`task model:copy` を忘れるとビルドが止まる**（`go:embed` がファイルを
  見つけられない）。**それが狙い** —— 古いデータや空のデータで配れてしまうより良い
- ⚠️ **ビルドが通ることでは足りない。** 中身が壊れていても `go:embed` は通るので、
  配布ビルドの前に `go test -tags embedmodel ./recognize/` で
  **実際に認識器として組み立てられること**を確かめる

詳しくは `recognize/CLAUDE.md` の「認識器の読み込み元は 3 通り」。

- `Taskfile.yml` の `includes` から `ios` / `android` を外してある（デスクトップ専用なので。`build/ios`・`build/android`・`build/docker` も削除済み）

## ⚠️ git worktree では replace が解決できない

`replace` は go.mod からの相対パスなので、`ikkyoku/.claude/worktrees/<名前>/` で作業すると
`../suteme` が `ikkyoku/.claude/worktrees/suteme` を指してしまい解決できない。
⚠️ **worktree 側に合わせて replace を書き換えないこと**（本来の配置で壊れる）。
代わりに、ジャンクションを置いてパスを成立させる:

```powershell
$w = 'D:\Go\Projects\shinte\ikkyoku\.claude\worktrees'
New-Item -ItemType Junction -Path (Join-Path $w 'suteme') -Target 'D:\Go\Projects\shinte\suteme'
New-Item -ItemType Junction -Path (Join-Path $w 'core')   -Target 'D:\Go\Projects\shinte\core'
New-Item -ItemType Junction -Path (Join-Path $w 'engine') -Target 'D:\Go\Projects\shinte\engine'
New-Item -ItemType Junction -Path (Join-Path $w 'kicho')  -Target 'D:\Go\Projects\shinte\kicho'
```

`.gitignore` が `.*` を無視するので git には見えない。
`_cmd/ikkyoku` 側の `../../../suteme` も同じジャンクションで解決される。

⚠️ **消すときは `Remove-Item -Recurse` を使わないこと**（参照先の中身まで消しうる）。
`[System.IO.Directory]::Delete($path, $false)` で reparse point だけを消す。

⚠️ **worktree には `frontend/node_modules` も `frontend/bindings` も無い。**
フロントを触る前に 2 つとも用意する（上記）。

## ⚠️ モジュールが 2 つあることの落とし穴

**`go mod tidy` の後に require 行が消えていないか確認すること**と、**kicho が相対 replace で引く依存を 2 つの go.mod 両方に書くこと** ——
どちらも `CLAUDE.md` の「モジュール / 位置づけ」に理由ごと書いてある。

## PureGo を維持する

新しい依存を足すときは、Windows で `CGO_ENABLED=0` のままビルドが通るか
**実際に確認してから**採用すること（`engine` が PureGo、`kicho` が
`modernc.org/sqlite` を使っているのも同じ理由）。

## 関連

| | |
|---|---|
| スキル `ikkyoku-verify` | ビルドしたあと**実機で何を押すか** |
| スキル `wails3` | Wails3 全般。⚠️ **alpha2.117 前提なので beta.16 と食い違う** |
| `_cmd/ikkyoku/CLAUDE.md` | ウィンドウ・Frameless・Win32 まわりの制約 |
