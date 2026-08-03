# ikkyoku（一局）

将棋のオンライン中継（対象: [ABEMA 将棋中継](https://abema.tv/now-on-air/shogi)）を観ながら、
**その場で盤面を解析して遊ぶ**ためのデスクトップアプリ。

> 中継の画面から盤面を割り出し、エンジンで解析して、
> **「次善手を選んだらどう転ぶか」を対話的に辿れるようにする。**

**現状はまだ最初の段階**で、画面の指定領域を PNG に保存するところまでしかできていない。
盤面認識・エンジン解析・検討 UI はこれから。

## 名前の由来

**「それもまた一局」から。** 最善手を唯一の答えとして示すのではなく、別の選択も
一つの局として辿らせる、という思想をそのまま名前にしている。

## ロードマップ

| Phase | 内容 | 状態 |
|---|---|---|
| 0 | 中継から盤面を取れるか検証 | **完了**（下記「経緯」） |
| 1 | 取り込み層（透過ウィンドウで領域指定 → PNG） | **ほぼ完了**。今ここ |
| 2 | 盤面認識（`suteme`） | 未着手 |
| 3 | 局面矯正層（駒数保存則・静的合法性・手番の決定） | 未着手 |
| 4 | エンジン接続（`engine`） | 未着手 |
| 5 | 検討 UI（訂正・次善手の一覧・分岐ツリー） | 未着手 |
| 6 | リアルタイムモード（盤面を映し続ける放送のみ・任意） | 未着手 |

設計原則と各 Phase の詳細は [`CLAUDE.md`](CLAUDE.md) を参照。
ワークスペース（`ShinteLab/shinte`）に並べている場合は、図と検討経緯を含む全文が
[`../TODO.md`](../TODO.md) にある。

## なぜ Chrome 拡張ではなくネイティブなのか（経緯）

最初のプロトタイプは Chrome 拡張（MV3）だった（検証コードは Phase 0 完了後に削除済み。
結果は `../TODO.md` 冒頭の「Phase 0 の検証結果」の表に記録してある）。要点だけ書くと以下の通り。

- ABEMA は盤面データを通信でも DOM でも流していない（映像に焼き込んで配信している）
- `<video>` → canvas の `drawImage`、および `chrome.tabs.captureVisibleTab` はどちらも
  **真っ黒になる**。Widevine DRM (EME) による保護がかかっているため
- **しかし OS レベルの画面キャプチャ（Win+Shift+S 等）では盤面が普通に写る。**
  Widevine L3 の保護は Chrome 自身のキャプチャ API に対しては効くが、
  OS の画面キャプチャは素通りする

つまり、入口を Chrome 拡張ではなく **OS の画面キャプチャを使うネイティブアプリ**にすれば、
DRM を一切気にせず済む。副次的な利点として:

- 放送局ごとのアダプタが要らない。画面に映ってさえいれば同じ経路で扱える
- Go で書けるので `core` / `engine` / `suteme` を直接 import できる。WASM も gRPC-web も不要
- MV3 の制約（service worker の寿命・offscreen document・CSP・MAIN world）が全部消える

## 現時点でできること

- 画面の指定領域を PNG にして保存する。今はここまで

盤面認識は [`suteme`](https://github.com/ShinteLab/suteme)、将棋の仕様（SFEN/USI/KIF）は
[`core`](https://github.com/ShinteLab/core)、エンジンは
[`engine`](https://github.com/ShinteLab/engine) にある。`ikkyoku` はそれらを束ねて
アプリにする側で、局面の矯正や検討 UI はこのリポジトリに入る予定。

### 設計上の約束

- **履歴に依存しない。** 「最初から観戦していないと動かない」「CM 中に盤面が進んだら
  迷子になる」を避けるための根幹。1 回のキャプチャは他と完全に独立している
- **スナップショットが基本。** 多くの放送は盤面を映し続けない（解説者・検討盤面・
  カメラ切り替え）。撮る瞬間をユーザーが決める方式にすることで、
  「今、盤面が映っているか」の判定を人間がやってくれる
- **段階的に劣化する。** 認識が弱くても棋譜が繋がらなくても、
  「撮ったこの 1 局面を解析する」は必ず成立する

これは `../TODO.md` の「どの層も履歴に依存しない」「suteme は棋譜の仕組みを持たない」という
原則を、取り込み層であるこのツールでも踏襲したもの。

## インストール・ビルド・使い方

CLI は無い。ビルドと使い方は下記「GUI アプリ（Wails3）」を参照。

## 採用ライブラリと cgo 確認

このプロジェクト群は PureGo 方針（`../CLAUDE.md` 参照）。以下 2 つを採用前に
`CGO_ENABLED=0` で Windows ビルドが通ることを実際に確認した。

| 用途 | ライブラリ | cgo |
|---|---|---|
| 画面キャプチャ | `github.com/kbinani/screenshot` | 不要（Windows は GDI 直呼び） |
| グローバルホットキー | `golang.design/x/hotkey` | 不要（Windows は `RegisterHotKey` を x/sys 経由で直呼び。cgo が要るのは macOS 側の実装のみ） |

`golang.design/x/hotkey` は現状ルートパッケージの `ParseHotkey` / `DefaultHotkey`
（文字列パースと既定値）だけに使っている。GUI アプリ自体のホットキー登録は
Wails 標準の `app.GlobalShortcut` を使っており、この節のライブラリを直接呼んではいない
（詳細は `ikkyoku/CLAUDE.md` の「ホットキー」節を参照）。

## GUI アプリ（Wails3）

数値で座標を指定する方式は盤面に枠を合わせる操作では実用的ではないため、
画面の上に重ねる「透過した枠」で範囲を指定できる GUI アプリを `_cmd/ikkyoku/` に用意した。
ウィンドウを将棋盤の上にドラッグ・リサイズして合わせ、ボタンかホットキー(既定 `Alt+S`)で撮る。
**現状 `ikkyoku` は「ルートパッケージ（ライブラリ）＋この GUI アプリ」の 2 つだけの構成で、
CLI は無い。**

- **透過**: `application.BackgroundType: BackgroundTypeTransparent` +
  `BackgroundColour: NewRGBA(0,0,0,0)` でクライアント領域(WebView の中身)を透過させる。
  タイトルバー・枠は通常どおり残る(Frameless にはしていない)。フロントは 2px の
  ガイド枠だけを描き、それ以外は完全に透明にしてある
- **キャプチャ領域の決定**: 「クライアント領域を、ガイド枠の太さぶん内側にオフセットした矩形」
  という決定論的な方式。タイミング依存(枠を消して撮る)の方式は採っていない。
  座標は Windows API の `GetClientRect` + `ClientToScreen` を HWND
  (`Window.NativeWindow()`)から直接呼んで**物理ピクセル**で取得する。Wails の
  `Window.Position()`/`Size()` は DIP(論理ピクセル)を返すため、マルチモニタで
  スケーリングが混在する環境ではそのまま使うとずれる
- **操作パネルは別ウィンドウ**: 「撮る」ボタン・保存先パス・直近のサムネイルを表示する
  操作パネルは、ガイド枠のウィンドウとは別ウィンドウにしてある。ガイド枠側には
  クリックできる UI を一切置かない(置いた要素はそのままキャプチャに写り込むため)
- **ウィンドウ状態の永続化**: ガイド枠ウィンドウの位置・サイズは終了時に保存し、
  次回起動時に復元する(`os.UserConfigDir()/ikkyoku/app-window.json`。ルートパッケージの
  `Config`(`config.json`)とは別ファイル)

ビルド:

```powershell
cd ikkyoku\_cmd\ikkyoku
npm --prefix frontend install    # 初回のみ
wails3 build                     # frontend のビルド〜bindings 生成〜go build まで一括
# または
wails3 dev                       # 開発モード
```

`wails3 build` で `bin\ikkyoku.exe` が生成される。詳細な設計判断は
`ikkyoku/CLAUDE.md` の「GUI アプリ(Wails3)」節を参照。

## コード構成

- ルート（`github.com/ShinteLab/ikkyoku`）: ライブラリ。`Capture` / `ListDisplays` /
  `SavePNG` / `Config` の読み書き等。GUI アプリに依存しないので、GUI から
  直接 import して使う
- `_cmd/ikkyoku/`: Wails3 GUI アプリ(独立したネストモジュール、モジュール名は
  `ikkyoku-app`)。上記参照

## 開発上の約束

`../CLAUDE.md` 全体の方針に加え、このディレクトリ固有の制約は `CLAUDE.md` を参照。
