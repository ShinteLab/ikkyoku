# ikkyoku（Wails3 アプリ）

`ikkyoku` のデスクトップアプリ本体。**独立したネストモジュール**（`module ikkyoku`）で、
ルートの `github.com/ShinteLab/ikkyoku` を `replace` で相対参照している。

⚠️ **ここに置くのは「Wails の口が要るもの」だけ。** フロントに公開する Service は
ルート側の `app/`、ガイド枠の幾何は `guide/` にある。ここに残っているのは
main・`CaptureService`（ウィンドウと HWND を触る）・ウィンドウ／Win32 まわり。

## ビルド

```powershell
npm --prefix frontend install     # 初回のみ
wails3 generate bindings -ts -i   # ⚠️ クローン直後・worktree では先に必要
wails3 build                      # frontend ビルド〜bindings 生成〜go build まで一括
wails3 dev                        # 開発モード

task model:copy                   # 配布用: suteme/dist → recognize/model
task build:embed                  # 配布用: 認識器を焼き込んだ exe
```

⚠️ **`frontend/bindings/` は生成物で git に入っていない。** クローン直後や
git worktree では存在しないので、`npm run build` や `tsc` を単独で打つ前に
一度生成すること（`wails3 build` / `wails3 dev` は自動で生成する）。

## ウィンドウ（4 枚）

同じフロントを URL クエリで出し分けている（`frontend/src/main.ts`）。

| ウィンドウ | URL | 役割 |
|---|---|---|
| 枠 | `/?window=frame` | 盤に重ねる透過ウィンドウ。**「どこを撮るか」の定義そのもの** |
| メイン画面 | `/?window=main` | アプリ本体（5 タブ）。**閉じるとアプリが終了する** |
| 評価値グラフ | `/?window=evalgraph` | 解析タブから切り離したグラフ。**閉じるとドックに戻る** |
| 解析の列 | `/?window=study` | 解析タブから切り離した候補手・手順。同上 |

## 設計判断はどこに書いてあるか

**このディレクトリの詳細は `../../CLAUDE.md` の「GUI アプリ(Wails3)」節。**
透過・Frameless・キャプチャ領域の決め方・ウィンドウ状態の永続化・ペインの切り離し・
踏んだ罠（`app.Run()` の前の `Show()` は効かない、など）は全部そちらにまとめてある。
