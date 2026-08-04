// ikkyoku のフロントエンド エントリポイント。
//
// このアプリはウィンドウを2つ持つ(main.go 参照)。同一バンドルを URL クエリで
// 出し分ける(wails3 skill advanced.md の「マルチウィンドウは URL クエリで画面分岐」)。
//
//   - ?window=frame … 盤面に重ねる透過ガイド枠。Frameless なので、上部のツールバー
//     だけが操作可能な領域。ツールバーはキャプチャ領域の外にあるので写り込まない。
//   - ?window=main  … アプリ本体。撮った画像・保存先・(将来は)認識結果と設定。
//     起動時は非表示で、最初のキャプチャで現れる。閉じるとアプリが終了する。

// ⚠️ この素の import を消さないこと。
// Frameless ウィンドウのドラッグ移動(--wails-draggable: drag)とリサイズ(ウィンドウ端の
// 検出)は、@wailsio/runtime の drag.js がページ全体の mousedown/mousemove を監視すること
// で成立している。名前付き import(`import { Window } from ...`)だけでは副作用が
// 有効化されないことがあるため、エントリポイントで一度素の import を実行しておく
// (wails3 skill frameless.md「前提: main.tsx で @wailsio/runtime を import していないと
// ドラッグが動かない」)。これが無いと枠ウィンドウが一切動かせなくなる。
import "@wailsio/runtime";

import { mountFrame } from "./frame";
import { mountMainScreen } from "./mainscreen";

const params = new URLSearchParams(window.location.search);
const windowName = params.get("window") ?? "main";

const app = document.getElementById("app")!;

if (windowName === "frame") {
  document.title = "ikkyoku";
  document.documentElement.classList.add("is-frame");
  mountFrame(app);
} else {
  document.title = "ikkyoku";
  document.documentElement.classList.add("is-main");
  mountMainScreen(app);
}
