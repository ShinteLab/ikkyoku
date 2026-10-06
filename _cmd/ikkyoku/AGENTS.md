# _cmd/ikkyoku の AGENTS.md

**Wails3 GUI アプリ。ここに置くのは「Wails の口が要るもの」だけ。**
Service は `ikkyoku/app`、枠の幾何は `ikkyoku/guide`、画面は `frontend/src/AGENTS.md`。
⚠️ **残っているのは main・`CaptureService`・ウィンドウ／Win32 まわりだけ。**

## ⚠️ 触る前に読む文書（**自動では読まれない**）

**このディレクトリの制約は、話題ごとに `_docs/cmd-*.md` へ分けてある**（2026-09-28。
1 枚で 656 行あった）。**触るものから文書を引き、開いてから直すこと。**

| 触るもの | 読む文書 |
|---|---|
| ウィンドウの生成・起動時に出す窓・Frameless・窓の位置と大きさ（`main.go` / `window.go`）・**メイン画面が真っ黒になる**・**起動で黙って消えない**（`fatal.go` / `issues.go`） | `_docs/cmd-windows.md` |
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
  ⚠️ **2026-10-04 時点では CLI も `go.mod` も `v3.0.0-beta.26`**（2026-09-08 時点は
  beta.16。この節の「手元の CLI は beta.3」は古い記述だった）。**下に出てくる beta.3 の話は
  そのとき確かめたもの**で、beta.16 でも beta.26 でも再確認はしていない。
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
    **他の 9 つとは関数フックでしか繋がっていない**ので、ここだけ独立している
  - ⚠️ **`main` から呼ぶ口は `//wails:ignore` を付けて公開する**
    （`SettingsService.Config` / `KifuService.Open` / `Close` /
    `AnalyzeService.Close`）。**付けないと bindings に出てフロント API になる**

| `_cmd/ikkyoku/` のファイル | 役割 |
|---|---|
| `main.go` | ウィンドウ 2 枚の生成・**Service の登録**・フック登録・起動時の自動フィット・終了時の後始末。⚠️ **Service の実体は `ikkyoku/app`**（`application.NewService()` は任意のパッケージの値を取れる）。⚠️ **import は別名にしてある** —— パッケージ名 `app` が `application.App` の変数 `app` とぶつかる。**main しか使わない小物も畳んである**（`/shinte-web/` の配信・ホットキーのアクセラレータ変換・ファイル選択ダイアログ） |
| `captureservice.go` | Wails にバインドする Service。**ウィンドウと HWND を触るのでここに残っている**（枠の表示/非表示・素通し・自分を隠して撮る・メイン画面の塗り潰し・終了(`Quit`)・認識の呼び出し・ガイド枠の自動フィット `FitFrame`・**画像ファイルの読み込み `OpenImage`**）。⚠️ **撮る（`Capture`）と読み込む（`loadImage`）は `deliver` で合流する** —— そこから先（サムネイル・`capture:shot` / `capture:done`・認識）は 1 本だけ。⚠️ **寸法と幾何は `ikkyoku/guide`**（`captureRegion` はそれを使って HWND の矩形から領域を出すだけ） |
| `celldiff.go` | **追従の速い経路の撮る側**（2026-10-07）。先端とぴったり合った 1 枚（`SetCellBase`）と今の 1 枚をマスごとに比べ、変わったマスの番号だけを返す（`CaptureResult.Cells`）。手を決めるのは `StudyService.FollowCells`。制約は `_docs/cmd-capture.md` の「速い経路」 |
| `enginepriority.go` | **追っているあいだ盤の読みを優先する**（設定 `followFirst`。2026-10-07）の判断。外部エンジンの優先度を下げる・戻す（Win32 は `native_windows.go` の `setEnginePriority`）。制約は `_docs/cmd-capture.md` |
| `window.go` | ウィンドウの位置・サイズ。**永続化**（`app-window.json`・既定値・画面内へのクランプ）と**追跡**（動くたびに記録。⚠️ **終了時には `Position()` を読めない**）の 2 つ。⚠️ **5 枚ぶん**（枠・メイン画面・**評価値グラフ**・**候補手**・**手順**）。⚠️ **古いファイルには `graph` / `side` / `moves` が無い**ので既定に倒すこと（倒さないと大きさ 0 の窓が出る） |
| `native_windows.go` | **Win32 の直呼び**（`golang.org/x/sys/windows` の LazyProc。**cgo を使わないための層**）。①ウィンドウの矩形 ②枠の素通し（`WS_EX_TRANSPARENT`）とカーソル ③画像を CF_DIB でクリップボードへ ④**メッセージボックス**（`fatal.go` が使う。WebView2 にも Wails にも頼らない）。⑤**重なりの順**（`placeBehind`。切り離した窓をメイン画面のすぐ後ろへ回す。`registerPaneRaise`）。⚠️ **ここに判断を書かないこと** —— 付け外しの判断は `captureservice.go` の `watchCursor`、寸法は `ikkyoku/guide` |
| `fatal.go` | **続けられなくなったときの最後の口**（2026-10-04）。Wails の致命的なエラー（`Options.ErrorHandler`）と main の panic を**OS のメッセージボックス**で出してから終わる。⚠️ **箱にするのは `*application.FatalError` だけ**（致命的でないエラーも同じ口に来る）。⚠️ **起動はできるが足りないものはここに来ない**（`issues.go` → ⚠）。`_docs/cmd-windows.md` の「起動で黙って消えない」 |
| `issues.go` | **起動はできたが足りないもの**を `app.IssueService` へ載せる（認識器・棋譜データベース。設定・ログ・異常終了・控え・メイン画面の保険は `main.go` が直に載せる）。⚠️ **直ったら消すこと**・⚠️ **ホットキーの登録失敗は載せない**（2026-08-10 の決定）・⚠️ **文言に「棚」と書かない** |
| `native_other.go` | 上のスタブ（Windows 以外）。**呼ばれたらエラーを返すだけ。** ⚠️ **関数を足したら両方に足すこと** |
| `embedmodel_on.go` / `embedmodel_off.go` / `model/` | **認識器の焼き込み**（2026-10-04 に `recognize` から移した）。`-tags embedmodel` のときだけ `model/`（手元のモデル）を `go:embed` し、`main` が `recognize.SetEmbedded` で渡す。`model/` の中身は suteme の配布用の書き出し（`go run ./_cmd/suteme-training -export -gzip -out <ここの model>`）。⚠️ **`model/` はフォルダごと git に入れない**（`.gitignore`）・⚠️ **ビルドで suteme を見ない**（入れ替えは suteme で書き出すか `build:embed MODEL_DIR=`。`build/model.ps1`）。理由は `_docs/design-capture.md` の「決定: 認識器は 3 段」 |
| `version` | **アプリのバージョン（唯一の正）**。`main.go` が `//go:embed` で焼き込み、起動時にログへ出す。⚠️ **手で書き換えない** —— ルートで `go run _cmd/version.go` を使う（`config.yml` と `package.json` へも伝播させる。手順はスキル `ikkyoku-build`） |
| `Taskfile.local.yml` | **git に入れない**個人用の `local:deploy`（バージョンの伝播 → 焼き込みビルド → ビルド情報のコミット → タグ → 常用場所へコピー）。雛形と手順はスキル `ikkyoku-build`。⚠️ **`Taskfile.yml` の include の `optional: true` を外さない**（無い環境で全部のタスクが落ちる） |
| `devbuild.go` / `prodbuild.go` | **ログをターミナルにも出すか**（`consoleLog`）と、**起動ログの版に `+dev` を付けるか**（`devBuild` → `appversion.go`。開発ビルドは patch を 1 つ上げて名乗る）。ビルドタグ `production` が無い（＝ `wails3 dev`）ときだけ出す。⚠️ **判定はビルドタグで**（配る exe は `-H windowsgui` で標準エラーの行き先が無い）。ログ本体は `log/AGENTS.md` |
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
- `celldiff_test.go` — **速い経路の撮る側**（2026-10-07）。⚠️ 歯止めは —— **比べる相手が無ければ使わないこと**・
  **ふるいが「変化して止まった」と言った 1 枚でしか使わないこと**（保険の 1 枚は答え合わせの役）・
  **変わったマスの番号が塗り替えたマスだけになること**
- `fatal_test.go` — **続けられないときの箱の文面**（`fatalMessage`）と **⚠ の合わせ方**
  （`syncRecognizerIssue` / `syncKifuDBIssue`）。⚠️ 歯止めは —— **WebView2 が原因なら
  名指しすること**（利用者が自分で直せる唯一のもの）・**panic のスタックを箱に全部載せない
  こと**・**直ったら ⚠ から消えること**・**帯の判定データは駒種の推論器と別の問題として
  出すこと**（直す場所を間違えないように）
- `embedmodel_test.go`（`-tags embedmodel` のときだけ）— **焼き込んだ `model/` が実際に認識器として
  組み立てられること**。go:embed はファイルがあれば通るので、中身が壊れていても（gzip でない・
  版が違う）ビルドでは気づけない。**配布ビルドの前に `go test -tags embedmodel -skip Clipboard .`**
- `recognizersource_test.go` — **認識器の 3 段**（2026-10-04。`recognizerOrder` と `loadRecognizer`）。
  ⚠️ 歯止めは —— **`auto` を「焼き込み優先」にしないこと**（開発中に焼き込みへ倒れると、
  **学習データを更新しても反映されない**という最も気づきにくい事故になる）・**置いてあるのに
  読めない段は Skipped に残して下へ落ちること**・**既定の置き場所が空なのは Skipped に入れない
  こと**・**どれも読めなければ認識器を外すこと**。落ち方の確かめは焼き込みの無いビルドでだけ走る
  （焼き込みがあると最後の段が埋まる）
