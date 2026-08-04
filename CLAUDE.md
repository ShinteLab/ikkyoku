# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## このプロジェクトが目指すもの（**最初に読むこと**）

**`ikkyoku` は「将棋中継を観ながら、その場で盤面を解析して遊ぶ」という構想そのものを
実現する場所。** 現状はまだキャプチャしかできていないが、**キャプチャツールではない。**

> オンライン中継の画面から盤面を割り出し、エンジンで解析して、
> **「次善手を選んだらどう転ぶか」を対話的に辿れるようにする。**

名前は「それもまた一局」から。最善手を唯一の答えとして示すのではなく、別の選択も
一つの局として辿らせる、という思想が名前になっている。**この思想が機能の判断基準になる。**

### 全体像

```
中継画面 ──キャプチャ──→ 盤面認識 ──→ 局面の矯正 ──→ エンジン解析 ──→ 検討ツリー
  (ikkyoku)              (suteme)      (ikkyoku)      (engine)      (ikkyoku)
                                          ↑
                                    仕様は core
```

| Phase | 内容 | 状態 |
|---|---|---|
| 0 | 中継から盤面を取れるか検証 | **完了**。OS レベルの画面キャプチャ一本と確定（下記） |
| 1 | 取り込み層（透過ウィンドウで領域指定 → PNG） | **完了** |
| 2 | 盤面認識（`suteme`）。持ち駒の認識も要る | **ikkyoku 側は完了・今ここ**。`suteme.LoadSFEN` の実装待ち（下記） |
| 3 | 局面矯正層（駒数保存則・静的合法性・手番の決定） | 未着手。**ikkyoku に入る** |
| 3.5 | 棋譜組み立て層（局面を日和見的に繋ぐ。**任意**） | 未着手 |
| 4 | エンジン接続（`engine` を直接 import） | 未着手 |
| 5 | 検討 UI（訂正・MultiPV・分岐ツリー） | 未着手。**ikkyoku に入る** |
| 6 | リアルタイムモード（盤面固定の放送のみ。**任意**） | 未着手 |

### 絶対に壊してはいけない設計原則

構想の根幹。**「便利そうだから」で崩さないこと。** 崩すと使い物にならなくなる。

1. **どの層も履歴に依存しない。**
   履歴は「あれば精度が上がるヒント」であって「動作条件」ではない。これを外すと
   **「最初から観戦していないと動かない」「CM 中に盤面が進んだら迷子になる」**という
   致命的な欠陥が出る。原則を守れば**迷子という状態自体が存在しない**（失う状態が無いため）

2. **スナップショットが基本。リアルタイム追従はオプション。**
   多くの放送は盤面を映し続けない（解説者・検討盤面・寄り・カメラ切り替え）。
   リアルタイムを基本にすると大半が異常系の処理になる。**ユーザーが撮る瞬間を決める**方式なら、
   「今、盤面が映っているか」の判定を人間が無料でやってくれる

3. **段階的に劣化すること。**
   認識が弱くても、棋譜が繋がらなくても、**最悪「撮ったこの 1 局面を解析する」は必ず成立する。**
   棋譜が取れるのはおまけ

4. **訂正 UI は必須。** 1 駒間違ってもユーザーが直せるので、認識器は完璧でなくてよい。
   これがあるおかげで認識の要求水準が「正しくあれ」から「惜しいところまで行け」に下がる

5. **手番は局面から決まらない。** 盤面だけ見ても先後は判定できず、手数も無いので導出もできない。
   UI のトグル・画面の手番表示・直前スナップショットとの差分、の順で安い手段から当てる

### 責務の線引き（**混同しないこと**）

| 置き場所 | 責務 |
|---|---|
| `core` | 将棋の**仕様**（SFEN / USI / KIF）。ここに一本化する |
| `suteme` | **画像 → 盤面**。それだけ。ステートレス・冪等。棋譜も手番も手数も知らない |
| `engine` | 合法手生成・探索 |
| **`ikkyoku`** | **アプリ本体。上記を束ねる。** キャプチャ・局面矯正・検討 UI・棋譜組み立て |

- **「ikkyoku には将棋のロジックを書かない」は誤読。** 書かないのは**仕様**（SFEN 変換など）と
  **認識器**であって、それらを使う**アプリケーションのロジックは ikkyoku に書く。**
  Phase 3 の局面矯正層も Phase 5 の検討 UI も、core にも suteme にも置けないのでここに入る
- 依存の向きは `ikkyoku → core / suteme / engine`。**逆参照はしない**
- 現時点ではまだキャプチャしかしていないので、`core`/`suteme`/`engine` への依存は**無い**

### 構想の全文

ワークスペース（`ShinteLab/shinte`）の `TODO.md` に、図と検討経緯を含む全文がある。
**`ikkyoku` は独立したリポジトリなので、単体で clone した場合そのファイルは存在しない。**
その場合はこの節が構想に関する唯一の情報源になる。**この節を薄くしないこと。**

## Phase 2: suteme への接続（**ikkyoku 側は実装済み**）

撮った画像を `suteme` に渡し、返った SFEN で盤面を描くところまで繋いである
（`recognize` パッケージ → `CaptureService.Capture` → メイン画面）。

### ⚠️ 現状 `suteme.LoadSFEN` は未実装のスタブ

```go
func LoadSFEN(img image.Image) (string, error) {
	return "", fmt.Errorf("not implemented")
}
```

**そのため実際には盤面が出ない。** ikkyoku 側の配線・描画は完成していて、
`suteme` 側が実装された時点で動き出す。**ikkyoku 側で回避実装を書かないこと**
（認識器は suteme の責務。責務の線引き参照）。

`suteme` には `DetectBoard` + `NewKNN`(k-NN 認識器) + `RecognizeBoard` という
実装済みの部品が揃っているので、`LoadSFEN` はそれらを繋ぐだけで書けるはず。
ただし k-NN は `training_data_v2.json`（約 3.6MB、`suteme` のカレントディレクトリ前提）を
必要とするため、**suteme 側で embed するなど「パス無しで使える」形にする必要がある**
（ikkyoku がデータファイルの置き場所を知る設計にはしたくない）。

### 使う API（`github.com/ShinteLab/suteme`）

| 関数 | 用途 | 状態 |
|---|---|---|
| `LoadSFEN(img image.Image) (string, error)` | **画像 → SFEN 盤面文字列。これが本命の継ぎ目** | **スタブ** |
| `ValidatePieces(sfenBoard string) *PieceValidation` | **駒数保存則の検証**。`Warnings` と `HandTotal` を返す | 実装済み |
| `DetectBoard(img image.Image) *BoardRegion` | 画像中の盤の矩形を検出 | 実装済み |
| `Analyze(img image.Image) *AnalyzeResult` | エッジ画像 + 盤領域。`DrawBoard` でデバッグ描画できる | 実装済み |

### 分かっていること（設計に効く）

- **`ValidatePieces` が駒数保存則で持ち駒を逆算する。**
  盤上の駒数から「駒台にあるはずの枚数」(`HandTotal`) を出す。つまり
  **駒台を画像認識しなくても持ち駒が埋まる。** ただし **先後の割り振りは付かない**
  （`HandTotal` のコメントに「先後不明」とある）。Phase 3 の設計原則
  「手番は局面から決まらない」と同根の制約で、**どちらも UI で人間が決めるのが最も安い**
- `PieceValidation.Warnings` がそのまま「ここが怪しい」の提示に使える。
  **矯正層の第一歩は自前で書くのではなく、これを UI に出すこと**
- **`DetectBoard` はガイド枠の自動フィットに転用できる。** 撮った画像から盤の矩形が出るなら、
  ユーザーが手で合わせた枠を補正できる。Phase 1 の洗練としても効く

### ikkyoku 側の実装（済み）

- `recognize` パッケージ（ルートモジュール）が `LoadSFEN` + `ValidatePieces` を呼ぶ。
  **認識器はここに書かない**（suteme の責務）。一方、Phase 3 の局面矯正層はここに入る
- `CaptureService.Capture` が撮った直後に呼び、`CaptureResult` に
  `sfen` / `warnings` / `handTotal` / `recognizeError` を載せて返す
- **認識に失敗してもキャプチャは成功として扱う**（設計原則3）。PNG の保存は済んでおり、
  盤が出ない代わりに理由が UI に出るだけ。`recognizeError` がその理由
- メイン画面が SFEN・駒台の推定枚数（先後不明）・警告を出し、`<shogi-board>` で盤を描く

#### `<shogi-board>` の配信（ファイルをコピーしない）

`core/web` は共有資産を `web.Assets`（embed）で公開している。ikkyoku は
`AssetOptions.Middleware` で `/shinte-web/` に流しているだけ（`shinteweb.go`）。
suteme の学習用サーバと同じパス・同じ方式。フロントは実行時に
`import("/shinte-web/shogi-board.js")` する（バンドル対象ではないので `@vite-ignore` 付き。
パスを変数に逃がしてあるのは tsc がリテラルを解決しようとするため）。

**npm 依存(`@shinte/web`)にはしていない。** private パッケージで `file:` 参照になり、
相対パスが git worktree で壊れるうえ node_modules 共有の問題も絡む。Go 側は既に core に
依存しているので、embed を配信するほうが単純で確実。

#### ⚠️ 駒文字フォントはドキュメント側に登録し直す必要がある

`<shogi-board>` は `@font-face`(ShogiSFEN) を **Shadow DOM 内の style にしか持っていない**が、
**Chromium は shadow root 内の `@font-face` を無視する**（フォントはドキュメント単位で解決される）。
そのままだと駒がラテン文字（`l`/`n`/`s`/`g`/`k`…、後手は 180 度回転）で描画される。
`mainscreen.ts` の `registerBoardFont()` が `font.js` の `FONT_DATA_URL` を
ドキュメントの `<style>` に登録し直して回避している（**フォントの実体は core のまま**）。
**本来は `core/web` 側で登録するのが筋なので、直ったらこの関数は消せる。**

同じ理由で、`shogi-board` に `hidden` を付けても消えない
（`:host { display: inline-block }` が UA の `display:none` に勝つ）。
外側から `shogi-board[hidden] { display: none }` を当てて打ち消している。

### 想定される躓き

- **`suteme` の認識精度はまだ十分ではない。**
  ABEMA の CG 盤は毎回同じ描画なので本来はテンプレートマッチでほぼ解けるはずだが、
  現行の `suteme` がその前提でチューニングされているとは限らない。
  **精度が出なくても Phase 2 の失敗ではない。「今どれくらいか」が分かれば成功**
- **撮り溜めた PNG がそのまま `suteme` の学習データになる。**
  訂正 UI（Phase 5）で人間が直した結果が正解ラベルになるので、
  訂正 UI は認識精度の改善サイクルの一部でもある
- 実盤のカメラ映像（対局室の俯瞰）と CG 盤は**難易度が桁違い**。
  同じパイプラインに押し込まず、まず CG 盤で成立させること

## 経緯: なぜ Chrome 拡張ではなくネイティブなのか

**もともとは Chrome 拡張だった。** Phase 0 の検証で ABEMA が Widevine DRM により
`<video>` の画素を保護しており、Chrome 自身のキャプチャ API（`captureVisibleTab` /
`drawImage`）はいずれも黒フレームになることが分かった。一方 OS レベルの画面キャプチャは
DRM を素通りする。そのため入口を Chrome 拡張からネイティブアプリに切り替えた。
検証当時のコード（`_probe-chrome/`）は Phase 0 完了後に削除済み。

検証結果は以下のとおり。**この結論は再検証しなくてよい**（2026-08-04 / ABEMA 将棋中継で実測）:

| 経路 | 結果 |
|---|---|
| 通信の傍受（WebSocket / fetch / XHR） | **不可**。中継系は盤面データを流していない |
| DOM / Canvas の走査 | **不可**。同上 |
| `<video>` → canvas の `drawImage` | **不可**。真っ黒（Widevine DRM） |
| `chrome.tabs.captureVisibleTab` | **不可**。同上 |
| **OS レベルの画面キャプチャ** | **可**。盤面が普通に写る |

この結果、**放送局ごとのアダプタも供給元の抽象化（`SourceAdapter`）も不要**になった。
画面に映ってさえいれば ABEMA でも YouTube でもキャプチャボードでも同じ経路で扱える。
また Go ネイティブになったことで、`suteme` / `engine` を直接 import できる
（WASM も gRPC-web も Native Messaging も不要。この検討は全部消えた）。

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
- `github.com/ShinteLab/suteme` — 盤面認識（`recognize` パッケージが使う）。
  `core` も間接的に入る
- `github.com/ShinteLab/core` — `_cmd/ikkyoku` が `core/web` の embed（`<shogi-board>`）を配信する

ワークスペース（`ShinteLab/shinte`）に置いた場合、そのルートに go.mod は無いので
`go` コマンドは必ずこの `ikkyoku/` ディレクトリで実行すること。

依存の向きは `ikkyoku → core / suteme / engine`。**逆参照しない。**
ワークスペースに並んでいる状態では `replace` の相対パス参照になる（他のサブプロジェクトと同じ運用）:

```
go.mod                    replace .../suteme => ../suteme,       .../core => ../core
_cmd/ikkyoku/go.mod       replace .../suteme => ../../../suteme, .../core => ../../../core
```

`_cmd/ikkyoku` にも同じ replace が要る。**path 置換されたモジュール自身の replace は
無視される**ため（`ikkyoku` を `../../` で参照している以上、`ikkyoku/go.mod` の replace は効かない）。

### ⚠️ git worktree で作業するときは replace が解決できない

`replace` は go.mod からの相対パスなので、`ikkyoku/.claude/worktrees/<名前>/` で作業すると
`../suteme` が `ikkyoku/.claude/worktrees/suteme` を指してしまい解決できない。
**worktree 側に合わせて replace を書き換えないこと**（本来の配置で壊れる）。
代わりに、ジャンクションを置いてパスを成立させる:

```powershell
$w = 'D:\Go\Projects\shinte\ikkyoku\.claude\worktrees'
New-Item -ItemType Junction -Path (Join-Path $w 'suteme') -Target 'D:\Go\Projects\shinte\suteme'
New-Item -ItemType Junction -Path (Join-Path $w 'core')   -Target 'D:\Go\Projects\shinte\core'
```

`.gitignore` が `.*` を無視するので git には見えない。
`_cmd/ikkyoku` 側の `../../../suteme` も同じジャンクションで解決される。

**消すときは `Remove-Item -Recurse` を使わないこと**（参照先の中身まで消しうる）。
`[System.IO.Directory]::Delete($path, $false)` で reparse point だけを消す。

## 設計制約（必ず守ること）

冒頭の「絶対に壊してはいけない設計原則」がプロジェクト全体の話。ここはコードを書くときの話。

- **ルートパッケージ（`ikkyoku`）は状態を持たない。**
  1 回のキャプチャは他のキャプチャと完全に独立。「前回のキャプチャ」を参照するコードを
  足さないこと。Phase 6（リアルタイムモード）で前局面をヒントに使う場合も、
  **ヒントを全部無効にしても動くこと**を保てる形にする
- **ルートパッケージに将棋の仕様を書かない。** SFEN 変換・駒の表現は `core` の担当。
  ここは「画面の指定領域を画像にする」層。
  **ただし ikkyoku というプロジェクト全体としては将棋のロジックを扱う**
  （局面矯正層・検討 UI。冒頭の「責務の線引き」参照）。混同しないこと
- **ルートパッケージに Wails 依存を持ち込まない。** GUI 固有の関心事は `_cmd/ikkyoku/` に置く
- **PureGo を維持する。** 下記参照

## ファイル構成

| ファイル | 役割 |
|---|---|
| `capture.go` | `Region` / `DisplayInfo` / `ListDisplays` / `Capture` など、キャプチャの中核 |
| `region.go` | `ParseRegion`（`"x,y,width,height"` 文字列 → `Region`） |
| `save.go` | `SavePNG` / `DefaultOutDir` / タイムスタンプ式ファイル名生成 |
| `config.go` | `Config` の JSON 読み書き（`encoding/json` のみ、標準ライブラリで完結） |
| `hotkey.go` | `ParseHotkey`（`"alt+s"` 文字列 → `golang.design/x/hotkey` の修飾子・キー） |
| `recognize/` | 画像 → 盤面。`suteme` を呼ぶだけ。**認識器はここに書かない**。Phase 3 の局面矯正層はここに入る |
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

| `_cmd/ikkyoku/` のファイル | 役割 |
|---|---|
| `main.go` | ウィンドウ 2 枚の生成・フック登録・ホットキー登録 |
| `captureservice.go` | Wails にバインドする Service。**寸法定数の唯一のソース**・`captureRegion`・枠の表示/非表示・認識の呼び出し |
| `shinteweb.go` | `core/web` の embed を `/shinte-web/` で配信する AssetServer ミドルウェア |
| `geometry.go` | ウィンドウ位置の追跡（終了時に `Position()` を読めないため） |
| `windowstate.go` | `app-window.json` の読み書き・既定値・画面内へのクランプ |
| `clientrect_windows.go` | HWND からクライアント矩形を物理ピクセルで取得（`clientrect_other.go` はスタブ） |
| `hotkey.go` | ホットキー文字列 → Wails のアクセラレータ表記 |
| `frontend/src/main.ts` | エントリ。**素の `import "@wailsio/runtime"`** と `?window=` による画面分岐 |
| `frontend/src/frame.ts` | 枠（ツールバー + ガイド枠） |
| `frontend/src/mainscreen.ts` | メイン画面 |

### ウィンドウ構成(2枚)

将棋盤に重ねる透過ウィンドウの中に操作用の UI を置くと、その UI 自体がキャプチャに
写り込んでしまう。これを避けるため **ウィンドウを 2 枚に分けてある**（`main.go`）。

| ウィンドウ | URL | 役割 |
|---|---|---|
| 枠(`frame`) | `/?window=frame` | Frameless・透過。上部のツールバー(撮る/✕)とガイド枠だけを描く。**「どこを撮るか」の定義そのもの** |
| メイン画面(`main`) | `/?window=main` | アプリ本体。撮った画像・保存先・(将来は)認識結果と設定。**起動時は非表示** |

**メインは枠ではなくメイン画面。** 枠は位置合わせが済めば用済みになりうる道具で、
アプリの寿命を握る画面ではない。

- 起動直後は**枠だけ**が見える。メイン画面は `Hidden: true` で作られ、
  **最初のキャプチャで現れる**（`revealMain`）。2 回目以降は前面に出し直さない
  （観戦中にフォーカスを奪わないため）
- **枠は閉じても破棄せず隠すだけ。** 領域の定義を生かしたままにするため。
  隠れていても HWND は生きているので、**枠が非表示のまま `Alt+S` で同じ領域が撮れる**
  （むしろ隠したほうがツールバーやガイド枠が写り込む余地が原理的に無くなる）
- **メイン画面を閉じるとアプリが終了する。** 枠は隠れて生き続けるので、
  ここで明示的に `app.Quit()` しないとプロセスが残る
- 枠を隠した結果として可視ウィンドウが 0 枚になると操作不能になるため、
  `HideFrame` はメイン画面が未表示なら先に出す
- **キャプチャ領域の基準は枠のまま。これは移せない**（領域は枠のクライアント矩形そのもの）

同じフロントバンドルを URL クエリで出し分ける(wails3 skill `advanced.md` の
「マルチウィンドウは URL クエリで画面分岐」パターン)。フロント側は `frontend/src/main.ts` が
`?window=` を見て `frame.ts` / `mainscreen.ts` のどちらかをマウントする。

### 透過と Frameless

`WebviewWindowOptions.BackgroundType: application.BackgroundTypeTransparent` +
`BackgroundColour: application.NewRGBA(0, 0, 0, 0)` を枠ウィンドウに設定している。
**`application.WindowsWindow{ BackgroundType: ... }` ではない**（`WindowsWindow` 構造体には
`BackgroundType` フィールドが無い。実際の Wails v3 ソース(`webview_window_options.go`)で
確認済み。`BackgroundType`/`BackgroundColour` は `WebviewWindowOptions` 直下のフィールド）。

`Frameless: true`。OS のタイトルバーの代わりに、フロント側が上部にツールバーを描く。
`Windows.DisableFramelessWindowDecorations: true` も付けている（既定では DWM のフレームを
クライアント領域へ延ばして影・角丸を残すが、中継映像に重ねる透過ウィンドウなので
影も角丸も邪魔で、透過領域に DWM が描く余地も残したくない）。

#### Frameless の移動とリサイズ(**ここを踏むと枠が一切動かせなくなる**)

Frameless にすると移動もリサイズも OS 任せでなくなる。

- **移動** … ツールバーに CSS の `--wails-draggable: drag`。カスタムプロパティは継承するので、
  ボタン側は `no-drag` に戻す
- **リサイズ** … Wails ランタイムがウィンドウ端 5px（角は +10px）を座標で検出する。
  `DisableResize` は `false` のままにしておくこと
- **⚠️ どちらも `frontend/src/main.ts` の素の `import "@wailsio/runtime"` が前提。**
  ランタイムの `drag.js` がページ全体の mousedown/mousemove を監視して初めて成立する仕組みで、
  **これが無いと `--wails-draggable` を書いても何も起きない**
  （wails3 skill `frameless.md` の冒頭。名前付き import だけでは副作用が有効化されないことがある）

フロント側(`frame.ts` / `style.css`)は `html.is-frame` に `background: transparent` を指定し、
ツールバー（不透明）とガイド枠（`border` だけの要素）を縦に積む。ガイド枠は
`pointer-events: none` だが、**リサイズの端検出は座標だけで要素を見ていない**ので影響しない。

### キャプチャ領域の決定(物理ピクセル・HWND 直接取得)

**キャプチャ領域は「クライアント領域から、ツールバーとガイド枠を除いた内側の矩形」**という
決定論的な方式にしてある(`captureservice.go` の `captureRegion`)。「撮る直前に枠を消して
少し待って撮る」というタイミング依存の方式は採っていない。

```
┌──────────────────────────┐ ← 端 5px = リサイズ(Wails ランタイムが座標で検出)
│ ツールバー(toolbarHeightPx)│ ← 撮る/✕。ドラッグ移動もここ
├──────────────────────────┤
│ ┌──────────────────────┐ │ ← ガイド枠(guideBorderPx)
│ │   ここを撮る(透過)    │ │
│ └──────────────────────┘ │
└──────────────────────────┘
```

- **寸法の唯一のソースは Go 側**(`captureservice.go` の `guideBorderPx` /
  `toolbarHeightPx`)。フロントは起動時に `CaptureService.Layout()` を呼び、
  CSS 変数(`--guide-border` / `--toolbar-height`)に流し込んでから描く。
  **フロントに既定値を書かないこと。** ずれると「見えている枠」と「実際に撮れる領域」が
  食い違い、枠が写り込む。取得に失敗したときは黙って描かず、ツールバーにエラーを出す
- Frameless なので**クライアント領域はウィンドウ全体と一致する**(Wails が `WM_NCCALCSIZE` で
  標準フレームを外す)。タイトルバー・枠の厚みを別途足し引きする必要はない
- **CSS px → 物理ピクセルの変換は切り上げる**(`scaleUp`)。丸めで 1px ずれたとき、
  内側に食い込む(盤が 1px 欠ける)のは実害が無いが、外側にはみ出すと赤い枠が写り込むため
- **⚠️ `framelessBottomPaddingPx = 1` を引くこと。** Wails は Frameless のとき
  `WM_NCCALCSIZE` で `rgrc.Bottom += 1` と `setPadding(edge.Rect{Bottom: 1})` を行っている
  (リサイズ時のちらつき回避)。結果として**クライアント領域の最下 1 行には WebView が
  描画されず**、ガイド枠の下辺が `GetClientRect` の下端より 1px 上に来る。これを引かないと
  撮った画像の最下行に赤い枠が 1px 写り込む(実測で確認 → 修正済み)。
  この余白のせいで**枠ウィンドウはクライアント高さがウィンドウ高さより 1px 大きい**
  (`480x421` client / `480x420` window)という妙な状態になる。ウィンドウ判別に
  「client == window なら Frameless」という判定を使わないこと
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

枠とメイン画面**両方**の位置・サイズを `os.UserConfigDir()/ikkyoku/app-window.json` に
保存・復元する(`windowstate.go` / `geometry.go`)。**ルートパッケージの `Config`(`config.json`)とは
あえて別ファイルにしてある**。ルートパッケージに Wails 依存(`application` パッケージの
`ScreenNearestDipPoint` 等)を持ち込まないための分離。

- 未設定(初回起動)の判定にはセンチネル値 `-32000` を使う。マルチモニタで左/上に
  モニタがあると座標が負になりうるため、`0` や `< 0` では判定できない(wails3 skill
  `window-state.md`)
- **⚠️ 終了時に `Position()`/`Size()` を読んではいけない。** `WindowClosing` の時点では
  破棄が進行しており、**不正な値が返る**(wails3 skill `window-state.md`「特に Frameless で
  発生しやすい」。このアプリでも実測で確認し、枠を動かしても保存値が追従しない不具合が出た)。
  代わりに `geometryTracker`(`geometry.go`)が `WindowDidMove` / `WindowDidResize` の
  たびに位置を記録しておき、**終了時にはその記録を保存する**。
  スキルが挙げている回避策(フロントの✕から Go の `Quit()` を呼ぶ)は自前の✕しか無い
  ウィンドウ向けで、**ネイティブのタイトルバーを持つメイン画面には使えない**
  (Alt+F4・OS シャットダウンを自前の経路に通せないため)
- 保存の起点はメイン画面の `WindowClosing` の **`RegisterHook`**(`OnWindowEvent` ではない)。
  デフォルトの破棄用リスナーは並列 goroutine で走るため、hook(同期・listener より前に実行)
  でないと破棄とレースする(wails3 skill `tray-hotkey.md` の hook/listener 順序の説明)。
  **枠は閉じられないので、保存経路はここ 1 本だけ**
- 枠の `WindowClosing` hook は `e.Cancel()` + `Hide()`。ツールバーの自前✕は
  `WindowClosing` を通らない(wails3 skill `pitfalls.md`)ので、そちらは JS から
  `CaptureService.HideFrame()` を呼んで同じ入口に合流させている
- マルチモニタのクランプ(`clampToScreen`)は `events.Common.WindowRuntimeReady` で行う
  (`ScreenNearestDipPoint` は `app.Run()` 前は `nil` を返すため)
- **旧フォーマット(枠 1 枚ぶんのフラットな JSON)との互換は取っていない。**
  ツールバーが増えて同じウィンドウサイズでも撮れる領域が変わったので、
  旧い座標を復元しても位置合わせはやり直しになるため

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

#### 追加検証(Frameless 化・メイン画面の格上げ後)

**アプリを起動 → HWND から矩形を直接取得 → その矩形をスクリーンショットから切り出して
ピクセル値を調べる**、という手順で写り込みを厳密に確認した。目視より確実なので今後も使える
(検証用の PowerShell は使い捨てにしたが、`GetClientRect`+`ClientToScreen`+`GetDpiForWindow` を
呼ぶだけ)。撮った PNG の外周 2px をスキャンして赤(`#E93D3D` 系)が 0 個であることを確認している。

- **ツールバー・ガイド枠の写り込み** … 無し。四辺すべて内容のみ(`476x384`)
- **枠のクライアント矩形が Frameless でウィンドウ全体と一致すること** … 確認
  (メイン画面は `704x481` client / `720x520` window なのに対し、枠は `480x421`/`480x420`)
- **起動直後は枠だけが見えること** … 確認(可視ウィンドウ 1 枚)
- **最初のキャプチャでメイン画面が出ること** … 確認(枠の下に非重複で配置される)
- **枠を閉じてもアプリが生き残ること** … 確認(`WM_CLOSE` を送ってもプロセス継続・枠は非表示)
- **メイン画面を閉じるとアプリが終了すること** … 確認
- **ウィンドウ位置・サイズの保存・復元** … 確認(枠を `300,200` へ動かして終了 → 保存値が
  `300,200` → 再起動でその位置に復元)。**これは以前「未検証」だった項目で、実際に壊れていた**
  (終了時に `Position()` を読んでいたため。上記「ウィンドウ状態の永続化」参照)
- **`CGO_ENABLED=0` でのビルド** … 通る(PureGo 方針の維持を確認)

**まだ未検証**:

- **ツールバーのドラッグ移動と、ウィンドウ端のリサイズ。**
  実際にマウスを操作しないと判定できず、自動では確認していない。
  特に**ガイド枠が 2px のままでリサイズ領域を塞がないか**は要確認
  (wails3 skill `frameless.md` は「ルート要素に 5px のパディングを設けること」としているが、
  これは alpha2.117 前提の記述で、**beta.3 のランタイム実装(`drag.ts`)を読む限り
  リサイズの端検出は座標だけで要素を見ていない**ため、塞がれない可能性が高い)
- **4K モニタ(150% スケーリング想定)での物理ピクセル一致。**
  検証したのは 100% のモニタのみ。`GetClientRect`+`ClientToScreen` を使っているので
  理屈上は合うはずだが、実測はしていない。
  なお**検証用スクリプトを PowerShell で書くときは注意**: PowerShell は DPI 非対応プロセスなので
  座標が仮想化され、per-monitor DPI aware な Wails アプリと座標系が食い違いうる
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
