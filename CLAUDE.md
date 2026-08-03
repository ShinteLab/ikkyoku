# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 位置づけ

将棋のオンライン中継の画面から盤面を取り込むための、Go 製ネイティブ画面キャプチャツール。
親ディレクトリの `../TODO.md` に構想全体（Phase 0〜6）がある。**作業前に必ず読むこと。**
このディレクトリは Phase 1（取り込み層）に対応する。

**もともとは Chrome 拡張だった。** Phase 0 の検証で ABEMA が Widevine DRM により
`<video>` の画素を保護しており、Chrome 自身のキャプチャ API（`captureVisibleTab` /
`drawImage`）はいずれも黒フレームになることが分かった。一方 OS レベルの画面キャプチャは
DRM を素通りする。そのため入口を Chrome 拡張からネイティブアプリに切り替えた。
検証当時のコード（`_probe-chrome/`）は Phase 0 完了後に削除済み。検証結果は
`../TODO.md` 冒頭の「Phase 0 の検証結果」の表に記録してあるので、経緯を確認したいときは
そちらと本ファイル・`README.md` を参照。

## モジュール / 位置づけ

独立した Go モジュール `github.com/ShinteLab/ikkyoku`。**PureGo（cgo なし）を維持すること。**
このプロジェクト群は PureGo が方針（engine が PureGo、kicho が `modernc.org/sqlite` を
使っているのも同じ理由）。新しい依存を足すときは、Windows で `CGO_ENABLED=0` のまま
ビルドが通るか実際に確認してから採用すること。

現時点の依存:

- `github.com/kbinani/screenshot` — 画面キャプチャ。Windows は GDI 直呼びで cgo 不要
- `golang.design/x/hotkey` — グローバルホットキー。Windows は `RegisterHotKey` を
  `golang.org/x/sys/windows` 経由で直呼びしており cgo 不要（cgo が要るのは macOS 側の実装のみ。
  `go.mod` に `golang.design/x/mainthread` が入っていないのはそのため。macOS の
  「main thread でイベントを処理する」制約は Windows には無い）

親ディレクトリの `CLAUDE.md` にある通り、ルートに go.mod は無いので `go` コマンドは
必ずこの `ikkyoku/` ディレクトリで実行すること。`core`/`engine`/`suteme` への依存は
**まだ無い**（このツールは盤面認識をしないため）。将来 Phase 2 以降で `suteme` を
呼ぶ層を足すときは、依存の向き（`ikkyoku → suteme`、逆参照しない）を守ること。

## 設計制約（`../TODO.md` より。必ず守ること）

- **状態を持たない。** 履歴に依存する処理を書かない。1 回のキャプチャは他のキャプチャと独立。
  「前回のキャプチャ」を参照するコードを足さないこと
- **将棋のロジックを書かない。** SFEN も盤面表現も駒も出てこない。
  仕様は `core`、認識は `suteme` の担当。このツールは「画面の指定領域を PNG にする」だけ
- **PureGo を維持する。** 上記参照

## ファイル構成

| ファイル | 役割 |
|---|---|
| `capture.go` | `Region` / `DisplayInfo` / `ListDisplays` / `Capture` など、キャプチャの中核 |
| `region.go` | `ParseRegion`（`"x,y,width,height"` 文字列 → `Region`） |
| `save.go` | `SavePNG` / `DefaultOutDir` / タイムスタンプ式ファイル名生成 |
| `config.go` | `Config` の JSON 読み書き（`encoding/json` のみ、標準ライブラリで完結） |
| `hotkey.go` | `ParseHotkey`（`"alt+s"` 文字列 → `golang.design/x/hotkey` の修飾子・キー） |
| `_cmd/ikkyoku/` | Wails3 GUI アプリ(独立したネストモジュール)。下記「GUI アプリ(Wails3)」参照 |

**ディレクトリ名は `ikkyoku`、モジュール名は `ikkyoku-app`。** `kicho` が
「ディレクトリ `_cmd/kicho`・モジュール名 `kicho-app`」という構成なので、それに揃えてある。
紛らわしいので混同しないこと。

## GUI アプリ(Wails3)

`_cmd/ikkyoku/` に置いてある。数値で座標を指定する方式（盤面に枠を合わせる操作では
実用的ではない）の代わりに、画面に重ねる透過ウィンドウで領域を視覚的に合わせられるようにしたもの。
**現状 `ikkyoku` は「ルートパッケージ（ライブラリ）＋この GUI アプリ」の 2 つだけの構成で、
CLI は無い。**

- **独立したネストモジュール**（`kicho/_cmd/kicho`・`prokishi/_cmd/prokishi-server` と同じ構成）。
  `module ikkyoku-app`、`replace github.com/ShinteLab/ikkyoku => ../../` でルートパッケージを参照する
- **Wails のバージョンは手元の CLI に追従している。** このディレクトリを作った時点の CLI は
  `v3.0.0-beta.3` だった（`wails3 skill` が前提にしている `alpha2.117` より新しい）。
  `wails3 init` の `-t vanilla` フラグは **beta.3 では無視され、常に React テンプレートが
  生成される**（実機で確認したバグ）。そのため `wails3 init -t vanilla` の出力を
  React 抜きの vanilla TypeScript に手作業で作り直してある。将来 CLI を上げたときは
  この制約が直っているか確認すること
- Wails 依存は `_cmd/ikkyoku/` にのみ置く（ルートパッケージ `ikkyoku` は触らない）。
  `Capture` / `SavePNG` / `DefaultOutDir` / `ParseHotkey` / `DefaultHotkey` を
  そのまま import して使っており、ロジックを再実装していない

### ウィンドウ構成(2枚)

将棋盤に重ねる透過ウィンドウの中に操作用の UI を置くと、その UI 自体がキャプチャに
写り込んでしまう。これを避けるため **ウィンドウを 2 枚に分けてある**（`main.go`）。

| ウィンドウ | URL | 役割 |
|---|---|---|
| ガイド枠(`frame`) | `/?window=frame` | 透過。2px のガイド枠だけを描く。クリック可能な要素は一切置かない |
| 操作パネル(`panel`) | `/?window=panel` | 不透明の通常ウィンドウ。「撮る」ボタン・保存先パス・直近のサムネイルを表示 |

同じフロントバンドルを URL クエリで出し分ける(wails3 skill `advanced.md` の
「マルチウィンドウは URL クエリで画面分岐」パターン)。フロント側は `frontend/src/main.ts` が
`?window=` を見て `frame.ts` / `panel.ts` のどちらかをマウントする。

### 透過の実装

`WebviewWindowOptions.BackgroundType: application.BackgroundTypeTransparent` +
`BackgroundColour: application.NewRGBA(0, 0, 0, 0)` をガイド枠ウィンドウに設定している。
**`application.WindowsWindow{ BackgroundType: ... }` ではない**（`WindowsWindow` 構造体には
`BackgroundType` フィールドが無い。実際の Wails v3 ソース(`webview_window_options.go`)で
確認済み。`BackgroundType`/`BackgroundColour` は `WebviewWindowOptions` 直下のフィールド）。
`Frameless` は `false` のまま(ユーザー要望どおりタイトルバー・枠を持つ通常ウィンドウ)。
Frameless に依存する透過実装ではないことを Wails 本体のソース(`webview_window_windows.go`)で
確認している。

フロント側(`frame.ts` / `style.css`)は `html.is-frame` に `background: transparent` を指定し、
`.capture-guide` という `position: fixed; inset: 0; border: 2px solid ...` の要素だけを描く。
`pointer-events: none` にしてあるので、クリック・ドラッグは常にネイティブウィンドウ側へ通る。

### キャプチャ領域の決定(物理ピクセル・HWND 直接取得)

**キャプチャ領域は「クライアント領域を、ガイド枠の太さぶん内側にオフセットした矩形」**という
決定論的な方式にしてある(`captureservice.go` の `captureRegion`)。「撮る直前に枠を消して
少し待って撮る」というタイミング依存の方式は採っていない。

- ガイド枠の太さは Go 側 `captureservice.go` の `guideBorderPx` と、フロント側
  `frontend/src/frame.ts` の `GUIDE_BORDER_PX` の 2 箇所に定数として存在する。
  **両者は必ず一致させること**(どちらかだけ変えると枠が写り込む、または枠の内側が余る)
- 座標は**物理ピクセル**で取得する。`kbinani/screenshot` は物理ピクセルで動くが、
  Wails の `Window.Position()`/`Size()` は **DIP(論理ピクセル)** を返すため、
  マルチモニタでスケーリング(150% 等)が混在する環境ではそのまま使うとずれる
- **`Window.NativeWindow()` で HWND(`unsafe.Pointer`)を取得し、Windows API の
  `GetClientRect` + `ClientToScreen` を `golang.org/x/sys/windows` 経由で直接呼んで
  クライアント領域のスクリーン座標を物理ピクセルで得ている**(`clientrect_windows.go`)。
  per-monitor DPI aware なプロセスでは `GetClientRect` 自体が物理ピクセルを返すため、
  DPI 変換もタイトルバー・枠の厚みの計算も不要になる
- ガイド枠の太さ(CSS px 指定)を物理ピクセルのオフセットに変換するためだけに、
  `GetDpiForWindow(hwnd)/96` のスケール係数を使っている(`GetDpiForWindow` が無い
  環境では 1.0 にフォールバック)
- **cgo は使っていない**(`golang.org/x/sys/windows` の `NewLazySystemDLL`/`NewProc` 経由)。
  `ikkyoku` ルートパッケージが PureGo 方針のため、GUI 側も踏襲している
- Windows 以外は `clientrect_other.go`(`!windows` ビルドタグ)でエラーを返すだけの
  スタブにしてあり、ビルド自体は壊れないようにしてある。**実装・動作確認は Windows のみ**

### ホットキー

既定 `Alt+S`。実際の登録は `golang.design/x/hotkey` ではなく **Wails 標準の
`app.GlobalShortcut.Register`** を使っている(`main.go`)。理由は `hotkey.go` のコメントに
書いてあるとおり、GlobalShortcut はウィンドウが非フォーカスでも確実に発火する経路として
wails3 skill(`tray-hotkey.md`)で保証されており、二重にホットキー実装を持ち込むと
競合のリスクがあるため。一方で「alt+s」のような文字列の妥当性検証・既定値の一元化は
既存の `ikkyoku.ParseHotkey` / `ikkyoku.DefaultHotkey` を単一のソースとして再利用し
(`hotkeyAccelerator` 関数)、「有効なホットキー文字列」の定義をルートパッケージに
一本化してある。

### ウィンドウ状態の永続化

ガイド枠ウィンドウの位置・サイズだけを `os.UserConfigDir()/ikkyoku/app-window.json` に
保存・復元する(`windowstate.go`)。**ルートパッケージの `Config`(`config.json`)とは
あえて別ファイルにしてある**。ルートパッケージに Wails 依存(`application` パッケージの
`ScreenNearestDipPoint` 等)を持ち込まないための分離。

- 未設定(初回起動)の判定にはセンチネル値 `-32000` を使う。マルチモニタで左/上に
  モニタがあると座標が負になりうるため、`0` や `< 0` では判定できない(wails3 skill
  `window-state.md`)
- 終了時の保存は `events.Common.WindowClosing` の **`RegisterHook`**(`OnWindowEvent` ではない)
  で行っている。デフォルトの破棄用リスナーは並列 goroutine で走るため、hook(同期・
  listener より前に実行される)でないと破棄とレースして `Position()`/`Size()` が
  不正な値を返しうる(wails3 skill `tray-hotkey.md` の hook/listener 順序の説明、
  `window-state.md` の該当セクション)
- マルチモニタのクランプ(`clampToScreen`)は `events.Common.WindowRuntimeReady` で行う
  (`ScreenNearestDipPoint` は `app.Run()` 前は `nil` を返すため)
- 操作パネルの位置・サイズは永続化していない(最小実装。毎回ガイド枠ウィンドウの
  近く(上、はみ出すなら下)に出す)

### 実機での検証結果(2026-08-04 / ディスプレイ 0: 2560x1440)

**GUI 自身のガイド枠を画面いっぱいに広げてキャプチャし、画像を目視して確認した。**
「画面全体を撮って目視で GUI の見た目を検証する」という手法自体は有効なので、今後も使える:

```powershell
Start-Process .\_cmd\ikkyoku\bin\ikkyoku.exe
# ガイド枠ウィンドウを画面いっぱいにドラッグ・リサイズしてから撮る
```

検証済み:

- **クライアント領域の透過** … 動作する。ABEMA の中継映像が枠の中に透けて見える
- **キャプチャ領域とガイド枠の一致** … 一致する。撮れた PNG に**赤いガイド枠は写り込まない**
  (`guideBorderPx` ぶんのオフセットが正しく効いている)
- **`Alt+S` の非フォーカス時の発火** … 動作する。フォアグラウンドが PowerShell の状態で
  発火してキャプチャが保存されることを確認
- **操作パネルがガイド枠に重ならないこと** … 動作する(下記の修正後)

検証で見つかって直した不具合:

- **ガイド枠ウィンドウに `AlwaysOnTop` が無く、中継ウィンドウの後ろに回っていた。**
  中継の「上」に重ねて位置合わせをする道具なので必須。操作パネルにだけ付いていた
- **初回起動時、ガイド枠と操作パネルが両方 `WindowCentered` になり重なっていた。**
  重なるとパネルごと写り込む。`WindowRuntimeReady` で枠の位置を読み、パネルを枠の外へ
  退避させるようにした

**まだ未検証**:

- **4K モニタ(ディスプレイ 1、150% スケーリング想定)での物理ピクセル一致。**
  検証したのはディスプレイ 0 のみ。`GetClientRect`+`ClientToScreen` を使っているので
  理屈上は合うはずだが、実測はしていない
- ウィンドウ位置・サイズの保存・復元(次回起動時に前回位置へ戻るか)
- `GetDpiForWindow` が無い環境(Windows 10 未満)でのフォールバック

### コマンド

```powershell
cd ikkyoku\_cmd\ikkyoku
npm --prefix frontend install         # 初回のみ
wails3 generate bindings -ts -i       # Go の Service/Model を変えたら必ず実行
wails3 dev                            # 開発モード
wails3 build                          # frontend ビルド〜bindings 生成〜go build まで一括
go build -o bin\ikkyoku.exe .         # Go だけを素早く確認したいとき(frontend/dist が要る)
```

- `wails3 generate bindings` は Taskfile(`build/Taskfile.yml` の `generate:bindings`)と
  同じ `-ts -i` を付けること(wails3 skill pitfalls.md 12 の「フラグの食い違いで
  `wails3 dev` の 1 回目だけ失敗する」問題を避けるため)
- `Taskfile.yml` の `includes` から `ios`/`android` を外してある(デスクトップ専用のため。
  `build/ios`・`build/android`・`build/docker` ディレクトリも削除済み)

## アーキテクチャ

- **ルートパッケージ（`ikkyoku`）はライブラリとして完結させる。** GUI 固有の関心事
  （Wails のイベント・ウィンドウ管理・ホットキー登録）は `_cmd/ikkyoku/` 側に置く。
  ルートパッケージは GUI に依存せず、そのまま import して使う
- Windows の `golang.design/x/hotkey` は内部で `runtime.LockOSThread()` した専用の
  goroutine を起動し、`RegisterHotKey` のメッセージループを回す。呼び出し側
  （このツール）が追加でスレッド管理をする必要はない（現状 GUI 側のホットキー登録は
  Wails 標準の `app.GlobalShortcut` を使っており、この仕組み自体は直接は呼んでいない。
  「ホットキー」節を参照）

## よく使うコマンド

```powershell
cd ikkyoku
go build ./...                          # ルートパッケージのみ（_cmd はアンダースコア始まりで対象外）
go vet ./...
go test ./...
```

`_cmd/ikkyoku/`（Wails3 GUI アプリ）は独立したネストモジュールなので、上記の
`./...` には含まれない。ビルド・確認は「GUI アプリ(Wails3)」節のコマンドを使うこと。

**`go mod tidy` の実行後は require 行が消えていないか確認すること**
（親 `CLAUDE.md` に書かれている `_cmd` 配下が `go build ./...` の走査対象外になる落とし穴）。
現状はルートパッケージ自体が `screenshot` と `hotkey` の両方を使っているため、
`_cmd` 側だけが依存する形にはなっていない。この構成を崩すとき（`_cmd` 専用の依存を
追加するとき）は特に注意すること。

## テスト方針

画面キャプチャそのもの（`Capture`）はテストしない（実行環境の画面に依存するため）。
テストを書いてあるのは環境に依存しない部分:

- `region_test.go` — `ParseRegion` の文字列パース、`Region.String()` との往復
- `save_test.go` — タイムスタンプ式ファイル名生成、`SavePNG` の保存先ディレクトリ自動作成
- `config_test.go` — `Config` の JSON 読み書きの往復、ファイル未存在時の扱い
- `hotkey_test.go` — `ParseHotkey` の文字列パース

## 開発上の約束

親ディレクトリの `../CLAUDE.md` に準じる。実装は Sonnet サブエージェントに委譲し、
メイン側は計画・指示・レビューに徹する運用。ドキュメント（README.md / CLAUDE.md）は
日本語で書く。コード中のコメントも日本語。
