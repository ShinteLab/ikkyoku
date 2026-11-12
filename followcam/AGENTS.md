# followcam の AGENTS.md

**中継を追う 1 回の「撮る側」**（2026-11-13 に `_cmd/ikkyoku` の `CaptureService` から移した。理由は
`_docs/design-follow.md` の「第 3 段」）。撮る・ふるい・速い経路の変わったマス・81 マスの読み・録画と、
追う 1 回の持ち物（`captureFollow`）。`app` の中継セッションが `app.FollowCamera` として使う。

- ⚠️ **局面を知らない層**。「先端と合っているか」「駒が動いたか」をここで判断しない（決めるのは `app`。
  `SetCellBase` / `CellsUnmoved` / `CellsUnclear` / `ForgetUnmoved` は言われたとおり覚えるだけ）
- ⚠️ **Wails も `app` も import しない**（依存はルート・`recognize`・`log`）。ウィンドウが要るものは
  差し込み口（`Camera.Region` / `Corrected` / `Session` / `BeforeRead` / `AfterAnchor` / `AfterEnd`）で受ける。
  ⚠️ **差し込み口は `Camera.mu` を持たずに呼ぶ**（先で `CaptureService` のロックを取る）・⚠️ **どれも nil で動くこと**
- ⚠️ **追従の状態を足すなら `captureFollow`**（`BeginFollow` で作り直し、`EndFollow` で丸ごと捨てる）。
  追う前から入るもの（設定 `debug`・差し込み口）は `Camera` に直に持つ

## ⚠️ 触る前に読む文書（**自動では読まれない**）

制約は `_docs/cmd-capture.md` にある（`_cmd/ikkyoku` にあったときのまま。枠の札など画面の話と同じ文書）。

| 触るもの | 読む節 |
|---|---|
| `gate.go`（ふるい・盤の有無）・`camera.go`（`CaptureQuiet` / `readQuiet`・持ち物）・`anchor.go`（追う盤）・`record.go`（録画） | 「中継を追っているかは枠に出す」の後半（`captureFollow` から下） |
| `celldiff.go`（変わったマス・`ReadCells` / `ComparePieces`） | 「速い経路: 変わったマスだけ返す」 |

中継セッションの側の約束は `_docs/app-follow.md`、調整とデバッグはスキル `ikkyoku-follow`。

| ファイル | 役割 |
|---|---|
| `camera.go` | 型（`Shot` / `CellPiece` / `CellPair` / `Camera`）・持ち物 `captureFollow`・`BeginFollow` / `EndFollow`・`CaptureQuiet` / `RecognizeQuiet` / `readQuiet` |
| `gate.go` | 画素差分のふるい（`gate`）・盤の有無のふるい（`detectGate`）・変わり目だけのログ（`noteQuiet`） |
| `celldiff.go` | 速い経路の撮る側（`cellsOf` / `fastCells`）・`ReadCells`・`ComparePieces`・比べる相手の出し入れ |
| `anchor.go` | 追う盤（マスタ）を決める（`AnchorBoard` / `PickAnchor`）。訂正した 1 枚 `ShotAnchor` を持つのは `CaptureService` |
| `record.go` | 録画（`SaveFollowFrame` / `SaveFollowMiss`・置き場所 `RecordRoot`・古い録画を消す `RemoveOldRecordings`） |

## テスト

⚠️ **ここに書いてあるのは「何の歯止めか」。消すときはその歯止めが要らなくなったのかを先に確かめること。**

- `anchor_test.go` — **追う盤（マスタ）の決め方**（2026-10-09。`PickAnchor`）。⚠️ 歯止めは —— **訂正してから追うなら
  押した瞬間の 1 枚ではなく訂正した 1 枚にすること**（盤が映っていなくても始める）・**今の根を採った 1 枚のときだけ使うこと**・
  **撮ったあとに枠が動いていたら使わないこと**
- `record_name_test.go` — **録画のファイル名**（手数の 0 詰め・推測の印・打つ手の `*` を `@` にすること）
- `gate_test.go` — **画素差分のふるい**（`gate`）。⚠️ **歯止めは 3 つ** ——
  **変わっていない周で認識を呼ばないこと**（ふるいの目的そのもの）・
  **変化したら止まってから読むこと**（動いている最中は**棋士の手が被った盤**を
  認識 1 枚ぶんかけて読むことになる）・**止まらなくてもいつかは読むこと**
  （盤に重なるテロップで**永久に認識しない**という壊れ方を作らない）
  ⚠️ **盤の有無のふるい（`detectGate`）の歯止めも同じファイル** ——
  **実際の検出は見ない**（`suteme` の仕事）。見ているのは**通す側の約束**で、
  **追う盤が無い / 枠が動いた / 見送りが続いた**ときに通すこと
- `record_test.go` — **中継の録画**（2026-10-11）。⚠️ 歯止めは —— **設定「中継の録画とログを残す（デバッグ）」が
  切ってあれば 1 枚も残さないこと**（追っている途中で切っても次の 1 枚から）・**1 週間より前の録画だけ消し、録画の名前で
  ないものは消さないこと**
- `camera_test.go` — **撮る側の持ち物の一生**（2026-10-11）。⚠️ 歯止めは —— **追い始めで作り直し、止めたら丸ごと捨てること**・
  **設定 `debug`（追う前から入るもの）は捨てないこと**・**`AfterEnd` は持ち物を捨てたあとに呼ぶこと**（訂正した 1 枚を捨てないことは `_cmd/ikkyoku` の `anchor_test.go`）
- `celldiff_test.go` — **速い経路の撮る側**（2026-10-07）。⚠️ 歯止めは —— **比べる相手が無ければ使わないこと**・
  **ふるいが「変化して止まった」と言った 1 枚でしか使わないこと**（保険の 1 枚は答え合わせの役）・
  **変わったマスの番号が塗り替えたマスだけになること**。変わったマスの組で「止まった」と見なすふるい（`gateReadCells`）も
  ここ —— **盤全体が動き続けても組が 2 枚続けて同じなら速い経路に入ること**（ゲーム画面の演出）・**組が毎周変わる
  あいだは待つこと**・⚠️ **決まらなかった組（`cellsFailed`）では見なさず、それでも待つ上限で読むこと**・
  **比べる相手を替えたら組を忘れること**・⚠️ **何も変わっていない組が続くのは「変わっていない」に数え、保険で読むこと**・
  ⚠️ **説明の付かない組（`CellsUnclear`）は盤全体が止まっていても待つ上限まで待つこと**・⚠️ **81 マスで「何も指していない」と
  出た組（`CellsUnmoved`）は「変わっていない」に数えること**（どちらも 2026-10-08）。
  **`ComparePieces`**（2026-10-07。成・不成の証拠）も —— **比べる相手が無ければ -1・同じ駒なら 1 に近いこと**
