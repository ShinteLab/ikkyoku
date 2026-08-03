// ikkyoku のフロントエンド エントリポイント。
//
// このアプリはウィンドウを2つ持つ(main.go 参照)。同一バンドルを URL クエリで
// 出し分ける(wails3 skill advanced.md の「マルチウィンドウは URL クエリで画面分岐」)。
//
//   - ?window=frame … 盤面に重ねる透過ガイド枠。ガイド枠を描くだけで、
//     クリックできるUIは一切置かない(ここに置いた要素はキャプチャに写り込むため)。
//   - ?window=panel … 「撮る」ボタン・保存先・直近のサムネイルを表示する操作パネル。
//     ガイド枠の外(別ウィンドウ)にあるので、キャプチャ領域には絶対に入らない。
import { mountFrame } from "./frame";
import { mountPanel } from "./panel";

const params = new URLSearchParams(window.location.search);
const windowName = params.get("window") ?? "panel";

const app = document.getElementById("app")!;

if (windowName === "frame") {
  document.title = "ikkyoku";
  document.documentElement.classList.add("is-frame");
  mountFrame(app);
} else {
  document.title = "ikkyoku - 操作パネル";
  document.documentElement.classList.add("is-panel");
  mountPanel(app);
}
