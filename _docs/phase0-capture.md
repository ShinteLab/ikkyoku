# Phase 0: なぜ Chrome 拡張ではなくネイティブなのか

**ここは検証の全文。** 結論の要約は `AGENTS.md` の「経緯」にある。

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
