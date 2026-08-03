# _probe-chrome（Phase 0 検証の遺物）

**これは役目を終えた検証用の遺物であり、現行の実装ではない。**

`ikkyoku` の Phase 0（画素が取れるかの検証）で実際に使った Chrome 拡張（Manifest V3）
一式をそのまま置いてある。ディレクトリ名をアンダースコア始まりにしてあるのは
Go のビルド対象外にするため（`go build ./...` は `_` 始まりのディレクトリを見ない）。

現行の実装は `ikkyoku/` 直下の Go 製ネイティブキャプチャツール。
このディレクトリの拡張は**もう動かさない**。中身の詳細は各ファイルのコメントを参照。

## 何を検証したか

対象: [ABEMA 将棋中継](https://abema.tv/now-on-air/shogi)。

盤面情報を安い順に 4 経路で診断した。

1. 通信の傍受（WebSocket / fetch / XHR フック）
2. DOM / Canvas オーバーレイの走査
3. `chrome.tabs.captureVisibleTab` によるタブ全体キャプチャ
4. `<video>` → canvas `drawImage` によるキャプチャ

## 分かったこと（結論）

- ABEMA は盤面データを通信でも DOM でも流していない（映像に焼き込んで配信している）。
  経路 1, 2 は空振り
- `<video>` → canvas の `drawImage` は**真っ黒**。Widevine DRM (EME) による保護がかかっている
- `chrome.tabs.captureVisibleTab` も同様に不可（黒フレーム）
- **しかし OS レベルの画面キャプチャ（Win+Shift+S 等）では盤面が普通に写る。**
  Widevine L3 の保護は Chrome 自身のキャプチャ API に対しては効くが、
  OS の画面キャプチャは素通りする

## この結論が意味すること

**入口は Chrome 拡張ではなくネイティブアプリが正しい。**

- DRM に関係なく動く。放送局ごとのアダプタが要らない。画面に映ってさえいれば同じ経路で扱える
- Go で書けるので `core` / `engine` / `suteme` を直接 import できる。WASM も gRPC-web も不要
- MV3 の制約（service worker の寿命・offscreen document・CSP・MAIN world）が全部消える

この判断に基づき、`ikkyoku/` 直下は Go のネイティブ画面キャプチャツールとして
作り直した。詳細は `../README.md` と `../CLAUDE.md` を参照。

## このディレクトリの構成（参考）

| ファイル | 役割（検証当時） |
|---|---|
| `manifest.json` | MV3 マニフェスト。host_permissions は `https://abema.tv/*` のみ |
| `background.js` | service worker。`captureVisibleTab` の実行、通信傍受ログの中継 |
| `content/main-world-hook.js` | MAIN world で `WebSocket`/`fetch`/`XMLHttpRequest` をフック |
| `content/bridge.js` | isolated world。MAIN world との橋渡し、DOM 走査、video キャプチャ |
| `sidepanel/` | 4 診断を並べて表示する side panel UI |

診断の見方・アーキテクチャの詳細な説明はコード中のコメントに残したまま、
更新はしていない（検証当時のスナップショットとして保存）。
