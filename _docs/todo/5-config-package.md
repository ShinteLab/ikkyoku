# ルートパッケージの役割と `config.go`（**判断待ち**）

2026-11-13 の全体の見直しで出た点。**コードを直す前にユーザーの判断が要る。** 判断が出たら、下の A か B を実施して
この文書を消し、`TODO.md` 7 の行を消す。

## 何が食い違っているか

ルートの `AGENTS.md` はルートパッケージ（`ikkyoku`）を「画面の指定領域を画像にする層」「状態を持たない」
「将棋の仕様を書かない」と書いている。実際にはアプリ全体の設定がここにある。

- `config.go` 1,408 行（型 10 個・関数 42 個）・`config_test.go` 993 行。中身はエンジンの登録と option・駒の字・
  テーマ・棚の場所・追従の設定・suteme の送信先・ログの細かさ
- ルートのほかのファイル（`capture.go` / `region.go` / `save.go` / `hotkey.go` / `diff.go` / `piecesim.go`）は
  合わせて約 600 行で、こちらは文書どおり「撮る・測る」だけ

誰がルートの何を使っているか（2026-11-13。テストを除く）:

| 使う側 | 使っているもの |
|---|---|
| `app` | **設定だけ**（`Config` 50 回・`EngineEntry` 42 回・`Normalize…` / `Default…` / `EngineOption` など） |
| `_cmd/ikkyoku` | 撮る側（`Region` / `Capture` / `SavePNG` / `ParseHotkey` …）と設定（`Config`・`SutemeSource…`） |
| `guide` | `Region` だけ |
| `followcam` | 撮る・測る（`Region` / `Capture` / `SavePNGAs` / `FrameDiff` / `CellDiff` / `PieceSimilarity`） |

動作に害は出ていない。問題は**読むときの見通し**だけ: 「ルートは将棋を知らない」と読んだあとに、同じパッケージで
エンジンの ID の採番（`NextEngineID`）や詰将棋エンジンの設定に出会う。

## 選択肢

### A: 文書を実態に合わせる（**すすめ**）

コードは動かさない。ルートの `AGENTS.md` の次の 3 か所を「ルートは**撮る層と設定**の 2 つを持つ」に直す。

- 「設計制約」の「ルートパッケージ（`ikkyoku`）は状態を持たない」「将棋の仕様を書かない」—— 撮る層の話だと分かる書き方に
- 「ファイル構成」の `config.go` の行 —— 今も正しいが、「撮る層とは別の関心」と一言
- 「テスト方針」—— 「`config_test.go` が一番の歯止め」は残す

手間は小さく、壊すものが無い。

### B: 設定を `ikkyoku/config` パッケージへ出す

`config.go` と `config_test.go` を `config/` へ移し、`ikkyoku.Config` を `config.Config` に置き換える。

引き換え:

- `app` で 150 か所以上、`_cmd/ikkyoku` で 15 か所ほどの置き換え（機械的）
- ⚠️ **bindings のパスが変わる。** 設定の型は今 `frontend/bindings/github.com/ShinteLab/ikkyoku/models` に出ていて、
  フロントが import している（例: `mainscreen.ts` の `EngineColorOption`）。移すと `…/ikkyoku/config/models` になり、
  TS の import を直す必要がある
- ⚠️ `config.go` が撮る層の値を使っている箇所（`DefaultHotkey` など）があれば、`config → ikkyoku` の依存が残る。
  逆向き（`ikkyoku → config`）は作らないこと。先に `grep` で確かめる
- 設定ファイルの中身（JSON のキー）は変えない —— 利用者の設定が読めなくなる

得るものは「パッケージの名前と中身が合う」ことだけで、機能も試しやすさも変わらない。

## 判断の材料

- **`config_test.go` は歯止めとして一番重い**（設定の読み違いは画面に何も出ないまま振る舞いが変わる）。どちらを選んでも
  テストは 1 本も書き換えない・消さない
- B をやるなら、`_docs/todo/3-mainscreen-split.md`（設定タブの切り出し）より**前**がよい（import を二度直さずに済む）
- 迷ったら A。B は「設定が増えてルートがさらに読みにくくなった」ときにやればよい

## 確かめ（どちらでも）

- ルートで `GOWORK=off go build ./... && go vet ./... && go test ./...`
- B のときは加えて: `_cmd/ikkyoku` で `go vet . && go test -skip Clipboard .`・`wails3 generate bindings -ts -i`・
  `frontend` で `npx tsc --noEmit`。実機で見てほしいこと: 既存の設定ファイルがそのまま読めること（エンジンの一覧・駒の字・
  「無制限（0）」の考える秒数が残っていること）
