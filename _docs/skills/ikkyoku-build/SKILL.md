---
name: ikkyoku-build
description: ikkyoku（一局）をビルドする・動かす・配る。go build / go test の打ち場所、Wails3 の bindings 生成（クローン直後と worktree で必ず要る）、frontend の npm、認識器を焼き込んだ配布ビルド、バージョンの上げ方（_cmd/version.go）と常用場所への配置（local:deploy）、git worktree で replace が解決できないときのジャンクション。「ビルドが通らない」「Cannot find module '../bindings/...'」「TS2307 が延々と出る」「配布用の exe を作る」「worktree で ../suteme が見つからない」「バージョンを上げる」「local:deploy」ときに使う。
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
⚠️ **`bindings/ikkyoku/` と `bindings/github.com/ShinteLab/ikkyoku/` は別物** —— 前者は
`_cmd/ikkyoku`（package main。今は `CaptureService` だけ）、後者はルートモジュールの
各パッケージ（`app` / `guide` / `position` / …）。
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

詳しくは `recognize/AGENTS.md` の「認識器の読み込み元は 3 通り」。

- `Taskfile.yml` の `includes` から `ios` / `android` を外してある（デスクトップ専用なので。`build/ios`・`build/android`・`build/docker` も削除済み）

## バージョンを上げる（`_cmd/version.go`）

**唯一の正は `_cmd/ikkyoku/version`**（1 行）。exe には `//go:embed version` で焼き込まれ、
起動時にログへ出る（`ikkyoku を起動します version=...`）。**リポジトリのルートで**:

```powershell
go run _cmd/version.go -print    # 今のバージョンを出すだけ
go run _cmd/version.go 0.2.0     # 指定した値にする
go run _cmd/version.go -bump     # patch / minor / major を対話で選ぶ（Enter = patch）
go run _cmd/version.go           # version の値で config.yml / package.json を揃え直す
go run _cmd/version.go -auto     # v+今のバージョンのタグがあれば patch を上げる（無ければ揃え直すだけ）
```

- ⚠️ **`version` も `config.yml` の `info.version` も `package.json` も手で書き換えない。**
  ずれると exe に焼き込まれる値と、ファイルのプロパティに出る値が食い違う
- ⚠️ **`build/windows/info.json` などは `version.go` が書かない。** `wails3 update build-assets` で
  作り直す。**ふだんは下の `local:deploy` がやる**ので手で打たなくてよい
- ⚠️ **タグはまだ 1 つも無い**（2026-10-04）。`-auto` はタグ `v<バージョン>` を見て上げる。
  最初の `local:deploy` で `v0.1.0` が付き、次の deploy から自動で patch が上がる

## 常用場所へ配る（`local:deploy`）

**CI でビルドしないので、バージョンの伝播・ビルド・コミット・タグを 1 コマンドでやる。**
`_cmd/ikkyoku/Taskfile.local.yml` は**人ごとのファイル**（コピー先が違う）なので git に入れていない。
**雛形は `references/Taskfile.local.yml`**。コピーして、要ればコピー先（`DEPLOY_DEST`）を直す:

```powershell
Copy-Item _docs\skills\ikkyoku-build\references\Taskfile.local.yml _cmd\ikkyoku\
cd _cmd\ikkyoku
wails3 task local:deploy                               # 既定は D:/Program Files/ikkyoku
wails3 task local:deploy DEPLOY_DEST="D:/tmp/ikkyoku"  # コピー先を変える（-- の後ろの引数は渡らない）
wails3 task local:build-info                           # ビルド情報の反映だけ（bump・コミット・タグはしない）
```

やること（順に）: 起動中の ikkyoku を止める → **タグ `v<今のバージョン>` があれば patch を上げる** →
`version.go` で揃え直し → `update build-assets` → **`build:embed`**（認識器を焼き込む） →
**ビルド情報のファイルだけをコミット** → `v<バージョン>` のタグ（push しない） → **exe だけ**をコピー。

- ⚠️ **`Taskfile.yml` の `local` の include から `optional: true` を外さない。** 無い環境で
  `wails3 build` / `wails3 dev` まで落ちる。⚠️ **`Taskfile.yml` 側から `local:` のタスクを呼ばない**（同じ理由）
- ⚠️ **ビルドは `wails3 build` ではなく `build:embed`**（`task: :build:embed`。先頭の `:` は
  `Taskfile.yml` 側を指す）。`wails3 build` の exe は認識器のデータが隣に無いと盤面を読めない
- ⚠️ **コピーするのは exe だけ**（`bin/*` にしない）。ログは exe の隣に出るので、`bin` の exe を
  試しに起動していると、そのログでコピー先の同じ日のログを上書きする。設定と棚は
  `os.UserConfigDir()/ikkyoku` にあるので、exe を置き換えても消えない
- ⚠️ **コミットは列挙したファイルだけ**（`git commit -- <paths>`）。作業中の変更は巻き込まないが、
  **exe には入る**。リリースとして残すなら、先にコミットしてから deploy する
- ⚠️ **同じコミットで deploy し直しても patch が上がる**（直前の deploy がタグを打っているため）。
  コピーだけやり直したいなら `bin\ikkyoku.exe` を手でコピーする
- ⚠️ **ずっと `update build-assets` が走っていなかった**（2026-10-04 まで）。`info.json` などは
  テンプレートのまま（会社名 `My Company`・説明 `My Product Description` = タスクマネージャの表示名）。
  **最初の deploy でアプリの情報に置き換わり、それがコミットされる**（スクラッチで作り直して、
  差分がアプリ情報の行だけなのを確かめた。CLI は beta.26。`build/ios/` も作られるが .gitignore 済み）。
  タスクマネージャに古い名前が残るのは Windows のキャッシュ（スキル `wails3` の pitfalls.md 14）

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

ルート（`github.com/ShinteLab/ikkyoku`）と `_cmd/ikkyoku`（Wails アプリ）は別の go.mod。
ワークスペースに並んでいる状態では、どちらも `replace` の相対パスで兄弟を引く:

```
go.mod                    replace .../suteme => ../suteme,       .../core => ../core,       .../engine => ../engine
                                  .../kicho  => ../kicho
_cmd/ikkyoku/go.mod       replace .../suteme => ../../../suteme, .../core => ../../../core, .../engine => ../../../engine
                                  .../kicho  => ../../../kicho
```

- **`_cmd/ikkyoku` にも同じ replace が要る。** path 置換されたモジュール自身の replace は
  無視される（`ikkyoku` を `../../` で参照している以上、`ikkyoku/go.mod` の replace は効かない）
- ⚠️ **kicho が相対 replace で引く依存（今は `core`）も、2 つの go.mod 両方に要る。**
  Go はメインモジュール以外の replace を読まないので、kicho に依存が増えるたびに書き足す。
  取りこぼしは kicho の `.\check-consumers.ps1` が見る（⚠️ `go.work` は replace より
  優先されるので、ビルドが通っても取りこぼしは検出できない）
- ⚠️ **kicho の公開 API を変えたら kicho 側で `.\check-consumers.ps1` を流すこと** ——
  kicho で `go build ./...` を通しても ikkyoku はコンパイルされない
  （2026-09-07 に向こうが `Store()` を閉じて公開 API を `Library` に集めた）
- ⚠️ **`kicho` はルートモジュールが使う**（`app/kifuservice.go`）ので、**go.mod は 2 つとも require する**
- ⚠️ **`go mod tidy` の後は require 行が消えていないか確認すること。** `_cmd` 配下は
  `go build ./...` の走査対象外なので、そこだけが必要とする依存は消される。
  今は該当が無い（ルートパッケージ自体が `screenshot` と `hotkey` を使っている）。
  **`_cmd` 専用の依存を足すときに効く**
- ⚠️ **`golang.org/x/image` の `// indirect` を外さないこと**（`core/shogifont` 経由で、
  `piecefont` が直に import しているわけではない）
- **exe の大きさ**: `go build` だけの素の exe が **36.9MB**（kicho 以前は 19.2MB。
  sqlite と goja が効いている）

## PureGo を維持する

新しい依存を足すときは、Windows で `CGO_ENABLED=0` のままビルドが通るか
**実際に確認してから**採用すること（`engine` が PureGo、`kicho` が
`modernc.org/sqlite` を使っているのも同じ理由）。

## 関連

| | |
|---|---|
| スキル `ikkyoku-verify` | ビルドしたあと**実機で何を押すか** |
| スキル `wails3` | Wails3 全般。⚠️ **alpha2.117 前提なので beta.26 と食い違う** |
| `_cmd/ikkyoku/AGENTS.md`（索引）→ `_docs/cmd-windows.md` / `_docs/cmd-frame.md` | ウィンドウ・Frameless・Win32 まわりの制約 |
