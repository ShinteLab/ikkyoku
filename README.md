# ikkyoku（一局）

将棋のオンライン中継（対象: [ABEMA 将棋中継](https://abema.tv/now-on-air/shogi)）の画面から
盤面を取り込むための、Go 製ネイティブ画面キャプチャツール。

構想全体は親ディレクトリの [`../TODO.md`](../TODO.md) を参照。ここは Phase 1（取り込み層）に
対応する。**盤面認識はまだしない。撮って PNG に保存するだけ**のツール。

## 名前の由来

「それもまた一局」から。対局中に悪手を指しても、それはそれで一局として成立する
（＝失敗した手順も含めて受け止める）という考え方をツールの名前に借りている。

## なぜ Chrome 拡張ではなくネイティブなのか（経緯）

最初のプロトタイプは Chrome 拡張（MV3）だった。[`_probe-chrome/`](_probe-chrome/) に
その検証コードと結論を残してある。要点だけ書くと以下の通り。

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

詳しい検証内容は `_probe-chrome/README.md` を参照。

## これは何をするツールか（そして何をしないか）

- **すること**: 画面の指定領域を PNG にして保存する。それだけ
- **しないこと**: 盤面認識（→ `suteme` の担当）、SFEN/USI/棋譜の扱い（→ `core` の担当）、
  局面の履歴管理・棋譜組み立て。**将棋のロジックは一切出てこない**
- **状態を持たない**: 1 回のキャプチャは他のキャプチャと完全に独立している。
  「前回撮った画像」を参照する処理は書かない

これは `../TODO.md` の「どの層も履歴に依存しない」「suteme は棋譜の仕組みを持たない」という
原則を、取り込み層であるこのツールでも踏襲したもの。

## インストール・ビルド

```powershell
cd ikkyoku
go build -o _dist\ikkyoku.exe ./_cmd/ikkyoku
```

PureGo（cgo なし）でビルドできる。`$env:CGO_ENABLED='0'` でも問題なくビルドが通ることを
確認済み。

## 使い方

```powershell
# 接続されているディスプレイの一覧（番号・解像度・座標）を表示する
.\_dist\ikkyoku.exe -list

# 単発モード: 起動して即座に1枚撮って終了する（動作確認用）
.\_dist\ikkyoku.exe -once

# 常駐モード（既定）: ホットキー(既定 Alt+S)を押すたびにキャプチャする。Ctrl+C で終了
.\_dist\ikkyoku.exe

# ディスプレイ番号を指定
.\_dist\ikkyoku.exe -display 1

# 矩形領域を直接指定（x,y,width,height）
.\_dist\ikkyoku.exe -region 100,200,1280,720

# 保存先を変更（既定は os.UserConfigDir()/ikkyoku/captures）
.\_dist\ikkyoku.exe -out D:\capture

# ホットキーを変更（既定 alt+s）
.\_dist\ikkyoku.exe -hotkey ctrl+shift+s
```

- キャプチャ領域が未指定なら**プライマリディスプレイ全体**になる
- 保存のたびに `saved=<パス> region=<x,y,w,h> size=<WxH>` の形式で 1 行標準出力する
- ファイル名はキャプチャ時刻のタイムスタンプ（例 `20260804-193045.png`）
- 設定（ディスプレイ番号・領域・保存先）は `os.UserConfigDir()/ikkyoku/config.json` に
  保存・読み込みできる。**フラグ指定が優先**され、フラグが無い項目だけ設定ファイルの値を使う

## 採用ライブラリと cgo 確認

このプロジェクト群は PureGo 方針（`../CLAUDE.md` 参照）。以下 2 つを採用前に
`CGO_ENABLED=0` で Windows ビルドが通ることを実際に確認した。

| 用途 | ライブラリ | cgo |
|---|---|---|
| 画面キャプチャ | `github.com/kbinani/screenshot` | 不要（Windows は GDI 直呼び） |
| グローバルホットキー | `golang.design/x/hotkey` | 不要（Windows は `RegisterHotKey` を x/sys 経由で直呼び。cgo が要るのは macOS 側の実装のみ） |

ホットキー登録が失敗する環境（他アプリと競合等）向けに、標準入力で Enter を押すたびに
キャプチャするフォールバックも用意してある（自動的に切り替わる。`-no-hotkey-fallback` で禁止可）。

## GUI アプリ（Wails3）

CLI は `-region x,y,width,height` を数値で指定する必要があり実用的ではないため、
画面の上に重ねる「透過した枠」で範囲を指定できる GUI アプリを `_cmd/ikkyoku-app/` に用意した。
ウィンドウを将棋盤の上にドラッグ・リサイズして合わせ、ボタンかホットキー(既定 `Alt+S`)で撮る。

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
  次回起動時に復元する(`os.UserConfigDir()/ikkyoku/app-window.json`。CLI 用の
  `Config`(`config.json`)とは別ファイル)

ビルド:

```powershell
cd ikkyoku\_cmd\ikkyoku-app
npm --prefix frontend install    # 初回のみ
wails3 build                     # frontend のビルド〜bindings 生成〜go build まで一括
# または
wails3 dev                       # 開発モード
```

`wails3 build` で `bin\ikkyoku-app.exe` が生成される。詳細な設計判断は
`ikkyoku/CLAUDE.md` の「GUI アプリ(Wails3)」節を参照。

## コード構成

- ルート（`github.com/ShinteLab/ikkyoku`）: ライブラリ。`Capture` / `ListDisplays` /
  `SavePNG` / `Config` の読み書き等。CLI・GUI どちらにも依存しないので、両方から
  直接 import して使う
- `_cmd/ikkyoku/`: CLI エントリポイント。フラグ処理とキャプチャ処理を分離してあり、
  本体ロジックはルートパッケージ呼び出しに徹する
- `_cmd/ikkyoku-app/`: Wails3 GUI アプリ(独立したネストモジュール)。上記参照

## 開発上の約束

`../CLAUDE.md` 全体の方針に加え、このディレクトリ固有の制約は `CLAUDE.md` を参照。
