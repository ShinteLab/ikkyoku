# _cmd/ikkyoku の AGENTS.md

**Wails3 GUI アプリ。ここに置くのは「Wails の口が要るもの」だけ。**
Service は `ikkyoku/app`、枠の幾何は `ikkyoku/guide`、画面は `frontend/src/AGENTS.md`。
⚠️ **残っているのは main・`CaptureService`・ウィンドウ／Win32 まわりだけ。**

## ⚠️ 触る前に読む文書（**自動では読まれない**）

**このディレクトリの制約は、話題ごとに `_docs/cmd-*.md` へ分けてある**（2026-09-28。
1 枚で 656 行あった）。**触るものから文書を引き、開いてから直すこと。**

| 触るもの | 読む文書 |
|---|---|
| ウィンドウの生成・起動時に出す窓・Frameless・窓の位置と大きさ（`main.go` / `window.go`）・**メイン画面が真っ黒になる** | `_docs/cmd-windows.md` |
| 枠の透過・素通し・移動とリサイズ・**キャプチャ領域（物理ピクセル・HWND）**（`native_windows.go` / `captureservice.go`） | `_docs/cmd-frame.md`（寸法と幾何は `guide/AGENTS.md`） |
| 撮る・中継を追う（枠の札・ふるい・録画）・画像ファイル・撮った画像のパス・ホットキー（`captureservice.go`） | `_docs/cmd-capture.md`（調整とデバッグはスキル `ikkyoku-follow`） |

## GUI アプリ(Wails3)

`_cmd/ikkyoku/` に置いてある。数値で座標を指定する方式（盤面に枠を合わせる操作では
実用的ではない）の代わりに、画面に重ねる透過ウィンドウで領域を視覚的に合わせられるようにしたもの。
**現状 `ikkyoku` は「ルートパッケージ（ライブラリ）＋この GUI アプリ」の 2 つだけの構成で、
CLI は無い。**

- **独立したネストモジュール**（`kicho/_cmd/kicho`・`prokishi/_cmd/prokishi-server` と同じ構成）。
  `module ikkyoku`、`replace github.com/ShinteLab/ikkyoku => ../../` でルートパッケージを参照する
- **Wails のバージョンは手元の CLI に追従している。** このディレクトリを作った時点の CLI は
  `v3.0.0-beta.3` だった（`wails3 skill` が前提にしている `alpha2.117` より新しい）。
  ⚠️ **2026-09-08 時点では CLI も `go.mod` も `v3.0.0-beta.16`**（この節の
  「手元の CLI は beta.3」は古い記述だった）。**下に出てくる beta.3 の話は
  そのとき確かめたもの**で、beta.16 で再確認はしていない。
  `wails3 init` の `-t vanilla` フラグは **beta.3 では無視され、常に React テンプレートが
  生成される**（実機で確認したバグ）。そのため `wails3 init -t vanilla` の出力を
  React 抜きの vanilla TypeScript に手作業で作り直してある。将来 CLI を上げたときは
  この制約が直っているか確認すること
- **UI は素の DOM 操作で書く（React でアプリを組まない）。** ただしアイコンに
  `react-icons` を使っており、その描画のためだけに `react` / `react-dom` が入っている。
  react-icons が配るのは React コンポーネントで、SVG 文字列を取り出す公開 API が無いため、
  `frontend/src/icon.ts` が `react-dom/server` の `renderToStaticMarkup` で
  **マウント時に一度だけ**静的マークアップへ変換して `innerHTML` に埋める。
  **React の使用箇所はここだけ**（クライアント側の react-dom ランタイムは読み込まれない）。
  アイコンを増やすときも `iconMarkup()` を通すこと
- Wails 依存は `_cmd/ikkyoku/` にのみ置く（`ikkyoku` 側は触らない）。
  `Capture` / `SavePNG` / `DefaultOutDir` / `ParseHotkey` / `DefaultHotkey` を
  そのまま import して使っており、ロジックを再実装していない
- ⚠️ **ここに置くのは「Wails の口が要るもの」だけ**（2026-09-04）。
  **7,380 行 / 22 ファイル → 2,745 行 / 7 ファイル**まで減らしてある（2026-09-08 に
  切り離した窓のぶんで少し戻った）。Service は `ikkyoku/app`、
  枠の幾何は `ikkyoku/guide` にあり、**残っているのは main・`CaptureService`・
  ウィンドウ／Win32 まわりだけ**。
  - **Wails の口は関数で渡す。** `SettingsService.PickFile`（ダイアログ。実体は
    `dialog.go`）と `AnalyzeService.Emit`（イベント。`app.Event.Emit` の薄い包み）。
    ⚠️ **どちらも nil で動くこと**（設計原則3。ダイアログが無くてもパスは手で打てるし、
    イベントを捨てても解析は進む）
  - ⚠️ **`CaptureService` だけが残っているのは、ウィンドウと HWND を触るから**
    （枠の表示/非表示・素通し・自分を隠して撮る・メイン画面の塗り潰し）。
    **他の 8 つとは関数フックでしか繋がっていない**ので、ここだけ独立している
  - ⚠️ **`main` から呼ぶ口は `//wails:ignore` を付けて公開する**
    （`SettingsService.Config` / `KifuService.Open` / `Close` /
    `AnalyzeService.Close`）。**付けないと bindings に出てフロント API になる**

| `_cmd/ikkyoku/` のファイル | 役割 |
|---|---|
| `main.go` | ウィンドウ 2 枚の生成・**Service の登録**・フック登録・起動時の自動フィット・終了時の後始末。⚠️ **Service の実体は `ikkyoku/app`**（`application.NewService()` は任意のパッケージの値を取れる）。⚠️ **import は別名にしてある** —— パッケージ名 `app` が `application.App` の変数 `app` とぶつかる。**main しか使わない小物も畳んである**（`/shinte-web/` の配信・ホットキーのアクセラレータ変換・ファイル選択ダイアログ） |
| `captureservice.go` | Wails にバインドする Service。**ウィンドウと HWND を触るのでここに残っている**（枠の表示/非表示・素通し・自分を隠して撮る・メイン画面の塗り潰し・終了(`Quit`)・認識の呼び出し・ガイド枠の自動フィット `FitFrame`・**画像ファイルの読み込み `OpenImage`**）。⚠️ **撮る（`Capture`）と読み込む（`loadImage`）は `deliver` で合流する** —— そこから先（サムネイル・`capture:shot` / `capture:done`・認識）は 1 本だけ。⚠️ **寸法と幾何は `ikkyoku/guide`**（`captureRegion` はそれを使って HWND の矩形から領域を出すだけ） |
| `window.go` | ウィンドウの位置・サイズ。**永続化**（`app-window.json`・既定値・画面内へのクランプ）と**追跡**（動くたびに記録。⚠️ **終了時には `Position()` を読めない**）の 2 つ。⚠️ **5 枚ぶん**（枠・メイン画面・**評価値グラフ**・**候補手**・**手順**）。⚠️ **古いファイルには `graph` / `side` / `moves` が無い**ので既定に倒すこと（倒さないと大きさ 0 の窓が出る） |
| `native_windows.go` | **Win32 の直呼び**（`golang.org/x/sys/windows` の LazyProc。**cgo を使わないための層**）。①ウィンドウの矩形 ②枠の素通し（`WS_EX_TRANSPARENT`）とカーソル ③画像を CF_DIB でクリップボードへ。⚠️ **ここに判断を書かないこと** —— 付け外しの判断は `captureservice.go` の `watchCursor`、寸法は `ikkyoku/guide` |
| `native_other.go` | 上のスタブ（Windows 以外）。**呼ばれたらエラーを返すだけ。** ⚠️ **関数を足したら両方に足すこと** |
| `version` | **アプリのバージョン（唯一の正）**。`main.go` が `//go:embed` で焼き込み、起動時にログへ出す。⚠️ **手で書き換えない** —— ルートで `go run _cmd/version.go` を使う（`config.yml` と `package.json` へも伝播させる。手順はスキル `ikkyoku-build`） |
| `Taskfile.local.yml` | **git に入れない**個人用の `local:deploy`（バージョンの伝播 → 焼き込みビルド → ビルド情報のコミット → タグ → 常用場所へコピー）。雛形と手順はスキル `ikkyoku-build`。⚠️ **`Taskfile.yml` の include の `optional: true` を外さない**（無い環境で全部のタスクが落ちる） |
| `devbuild.go` / `prodbuild.go` | **ログをターミナルにも出すか**（`consoleLog`）。ビルドタグ `production` が無い（＝ `wails3 dev`）ときだけ出す。⚠️ **判定はビルドタグで**（配る exe は `-H windowsgui` で標準エラーの行き先が無い）。ログ本体は `log/AGENTS.md` |
| `frontend/src/*.ts` | **画面。⚠️ 一覧と制約は `_cmd/ikkyoku/frontend/src/AGENTS.md`**（そこから `_docs/ui/*.md` を引く） |

## テスト（`go test ./...`）

⚠️ **ここに書いてあるのは「何の歯止めか」。消すときはその歯止めが要らなくなったのかを先に確かめること。**

- `clipboard_roundtrip_windows_test.go` — クリップボードに載せた CF_DIB を読み返し、
  ヘッダ・ボトムアップの行順・BGR の並びを検証する。手で組み立てたバイト列なので、
  貼り付け先で初めて気づくより往復で確かめるほうが速い。
  **実行するとクリップボードの中身が置き換わる**（`cd _cmd\ikkyoku; go test .`）
- `followgate_test.go` — **画素差分のふるい**（`gate`）。⚠️ **歯止めは 3 つ** ——
  **変わっていない周で認識を呼ばないこと**（ふるいの目的そのもの）・
  **変化したら止まってから読むこと**（動いている最中は**棋士の手が被った盤**を
  2.1 秒かけて読むことになる）・**止まらなくてもいつかは読むこと**
  （盤に重なるテロップで**永久に認識しない**という壊れ方を作らない）
  ⚠️ **盤の有無のふるい（`detectGate`）の歯止めも同じファイル** ——
  **実際の検出は見ない**（`suteme` の仕事）。見ているのは**通す側の約束**で、
  **追う盤が無い / 枠が動いた / 見送りが続いた**ときに通すこと
- `recognizersource_test.go` — 認識器の読み込み元の解決（`resolveRecognizerSource`）。
  ⚠️ **`auto` を「焼き込み優先」にしない**歯止め —— 開発中（＝ディレクトリを
  指している状態）に焼き込みへ倒れると、**学習データを更新しても反映されない**
  という最も気づきにくい事故になる。**タグの有無どちらでも通る**ように、期待値を
  `recognize.EmbeddedAvailable()` で切り替えてある
