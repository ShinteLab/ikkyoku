# `CaptureService` と `main()` を役割ごとのファイルに分ける

2026-11-13 の全体の見直しで出た改善点。**第 1 段と第 2 段は済んだ**（2026-11-13）。残りは任意の第 3 段だけで、
やらないと決めたらこの文書と `TODO.md` 7 の行を消す（`_cmd/ikkyoku/AGENTS.md` のファイル表は直してある）。

## 何が問題か

`_cmd/ikkyoku/captureservice.go` は 1,625 行（追従の撮る側を `followcam` へ出したあと。2026-11-13）で、
1 つの型に 5 つの役割が同居している。`main.go` は 1,081 行で、`main()` が 349 行ある。

2026-11-13 時点の `captureservice.go` の並び（行番号は動くので関数名で探すこと）:

| 役割 | 関数 | 行 |
|---|---|---|
| 認識器の読み込み（3 段） | `applyRecognizerDir` / `applyRecognizerModelDir` / `applyRecognizerSource` / `ReloadRecognizer` / `Recognizer` / `loadRecognizer` / `recognizerOrder` | 221〜408 |
| ウィンドウ（出す・隠す・素通し・帯） | `bind` / `revealMain` / `markFrameReady` / `revealIfStuck` / `placeMainBesideFrame` / `HideFrame` / `ShowMain` / `Quit` / `RepairMain` / `requireFrame` / `ShowFrame` / `FrameVisible` / `emitFrameVisible` / `applyClickThrough` / `watchCursor` / `stepCursor` / `setMouseThrough` / `SetFrameFooter` / `Layout` | 409〜898 |
| 撮る・画像ファイル | `Capture` / `noteShot` / `SetFollowShot` / `applyFollowDebug` / `OpenImage` / `loadImage` / `deliver` / `CopyImage` / `captureRegion` / `followRegion` | 899〜1159・1543〜 |
| 自動フィット | `FitFrame` / `findBoard` / `looksLikePartOfBoard` / `frameDisplay` / `captureWithoutSelf` | 1160〜1353 |
| 切り離した窓 | `applyEvalGraphDetached` / `initPaneDetached` / `SetStudyTabActive` / `syncPaneWindows` / `applyStudyPaneDetached` / `applyMovePaneDetached` / `maskWindows` | 1354〜1542 |

## 進め方

### 第 1 段: ファイルを分ける（型はそのまま。**済んだ**。`capture_recognizer.go` / `capture_windows.go` / `capture_fit.go` / `capture_panes.go`）

同じ `CaptureService` のまま、メソッドを役割ごとのファイルへ移す（Go は同じパッケージなら型のメソッドを複数のファイルに置ける）。
bindings もフロントの呼び出しも 1 行も変わらない。

- `captureservice.go` — 型・`NewCaptureService`・撮る・画像ファイル
- `capture_recognizer.go` — 認識器の読み込み
- `capture_windows.go` — ウィンドウ
- `capture_fit.go` — 自動フィット
- `capture_panes.go` — 切り離した窓

構造体のフィールド（約 25 個）も、役割ごとに並べ直してコメントで区切る。コメントは消さない。

### 第 2 段: `main()` を分ける（**済んだ**。`initLog` から `watchMainReveal` までの関数を順に呼ぶ形。型 `services`）

`main()`（96〜445 行）を、やっていることの順に関数へ割る（設定を読む → Service を作る → フックを繋ぐ → ウィンドウを作る →
走らせる）。**繋ぐ順番を変えない** —— フックの nil チェックで動いている箇所があり、順番が変わると黙って効かなくなる。

### 第 3 段（任意。**やる前にユーザーに聞く**）: Wails の要らない役割を外へ出す

認識器の読み込み（`loadRecognizer` / `recognizerOrder`）が Wails に触れていなければ、`followcam` と同じ要領で
`recognize` の側へ出せる。`recognizersource_test.go` がルートの `go test ./...` に入る。
**確かめた**（2026-11-13）: `capture_recognizer.go` は `application` / `s.app` / `s.wins` に触れていない。触れているのは
`config`・`recognize`・`s.mu`（設定の 3 つと直近の結果を守る）と `onRecognizer`（⚠ を合わせるフック）だけ。
出すなら焼き込み（`embedmodel_on.go` / `recognize.SetEmbedded`）の受け渡しがどうなっているかも読む
（`_docs/design-capture.md` の「決定: 認識器は 3 段」）。

## やらないこと

- ⚠️ **`CaptureService` を複数の Service に割らない。** メソッドは `CaptureService.X` としてフロントの API になっている
  （`frontend/src` から 16 個のメソッドを呼んでいる）。割ると TS の呼び出しを全部直すことになり、得るものが少ない
- ⚠️ **ロックを分けない**（第 1 段では）。`mu` は 1 つのまま

## 崩さないこと

- ⚠️ **追従の状態を `CaptureService` に足さない**（`followcam` の `captureFollow` へ）・
  ⚠️ **`mu` を持ったまま `followcam.Camera` を呼ばない**（`captureservice.go` の `follow` フィールドのコメント）
- ⚠️ **`main` から呼ぶ口は `//wails:ignore` を付けて公開する**（付けないと bindings に出る）
- ⚠️ **撮る（`Capture`）と読み込む（`loadImage`）は `deliver` で合流する**（イベントを 2 系統にしない。`_docs/cmd-capture.md`）
- ⚠️ **起動で黙って消えない**（`_docs/cmd-windows.md`）。`main()` を割るときに、足りないものを ⚠ に出す箇所を落とさない
- `native_other.go` はスタブ。**関数を足したら `native_windows.go` と両方に足す**

## 先に読むもの

`_cmd/ikkyoku/AGENTS.md`（索引）→ 触る役割に応じて `_docs/cmd-windows.md` / `_docs/cmd-frame.md` / `_docs/cmd-capture.md`。

## 確かめ

- `_cmd/ikkyoku` で `GOWORK=off go vet . && go test -skip Clipboard .`（⚠️ クリップボードのテストは流さない ——
  ユーザーのクリップボードを書き換える）
- `wails3 generate bindings -ts -i` のあと、`frontend` で `npx tsc --noEmit`
- 第 1 段は移すだけなので、`git diff --stat` の増減がほぼ釣り合うこと（増えるのは import とファイルの先頭のコメントだけ）
- 実機で見てほしいこと（第 2 段）: 起動して枠とメイン画面が出ること・設定を壊したときに ⚠ が出ること・終了して窓の位置が残ること
