// ikkyoku のフロントエンド エントリポイント。
//
// このアプリはウィンドウを2つ持つ(main.go 参照)。同一バンドルを URL クエリで
// 出し分ける(wails3 skill advanced.md の「マルチウィンドウは URL クエリで画面分岐」)。
//
//   - ?window=frame … 盤面に重ねる透過ガイド枠。Frameless なので、上部のツールバー
//     だけが操作可能な領域。ツールバーはキャプチャ領域の外にあるので写り込まない。
//   - ?window=main  … アプリ本体。撮った画像・保存先・(将来は)認識結果と設定。
//     起動時は非表示で、最初のキャプチャで現れる。閉じるとアプリが終了する。
//   - ?window=evalgraph … 切り離した評価値グラフ(2026-09-08)。**閉じてもアプリは
//     終わらない**(ドックに戻るだけ)。出すかどうかは設定 `evalGraphDetached`。

// ⚠️ この素の import を消さないこと。
// Frameless ウィンドウのドラッグ移動(--wails-draggable: drag)とリサイズ(ウィンドウ端の
// 検出)は、@wailsio/runtime の drag.js がページ全体の mousedown/mousemove を監視すること
// で成立している。名前付き import(`import { Window } from ...`)だけでは副作用が
// 有効化されないことがあるため、エントリポイントで一度素の import を実行しておく
// (wails3 skill frameless.md「前提: main.tsx で @wailsio/runtime を import していないと
// ドラッグが動かない」)。これが無いと枠ウィンドウが一切動かせなくなる。
import "@wailsio/runtime";

import { DiagService } from "../bindings/github.com/ShinteLab/ikkyoku/app";
import { mountFrame } from "./frame";
import { mountGraphScreen } from "./graphscreen";
import { mountMainScreen } from "./mainscreen";

const params = new URLSearchParams(window.location.search);
const windowName = params.get("window") ?? "main";

const app = document.getElementById("app")!;

// ---- 計測(心拍と例外) ------------------------------------------------------
//
// メイン画面が真っ黒になって何も触れなくなる現象を切り分けるためのもの
// (経緯は Go 側 diagservice.go)。**ここに置くのは、2 枚のウィンドウが同じ
// バンドルを共有していて、どちらでも同じ計測が要るから。**
//
// ⚠️ **心拍が「止まったかどうか」で手当てが正反対に変わる。**
//   - 黒いのに心拍が続く … JS は生きていて、WebView2 が不可視にされているだけ。
//     枠のメニューの「メイン画面を描き直す」で戻る
//   - 心拍が止まる       … レンダラ側が死んでいる。描き直しでは戻らない
//
// 間隔は Go 側の heartbeatInterval と揃えること。
//
// ⚠️ **隠れている窓ではこの間隔どおりには打たれない。** Chromium は隠れている・
// 最小化されているページのタイマーを間引き、setInterval は最悪 1 分に 1 回まで落ちる。
// それを「止まった」と読まないよう、Go 側の閾値は 90 秒に取ってあり、
// **document.hidden も一緒に送っている**(判定の材料にするのは向こう)。
const HEARTBEAT_MS = 5000;

// performance.memory は Chromium の非標準拡張なので型が無い。読めなければ 0
// (**心拍そのものが本命**で、ヒープは途切れる直前の値が分かれば十分)。
const heapMB = (): number => {
  const mem = (performance as { memory?: { usedJSHeapSize: number } }).memory;
  return mem ? Math.round(mem.usedJSHeapSize / 1048576) : 0;
};

const beat = () => void DiagService.Heartbeat(windowName, heapMB(), document.hidden);
window.setInterval(beat, HEARTBEAT_MS);
// 表示状態が変わった瞬間にも 1 回打つ。**間引きから復帰した直後の 1 打が要る**
// (最小化から戻ったのに心拍が来ない、が本当に来ていないのか間引きの残りなのかを
// 区別できるようにするため)。
document.addEventListener("visibilitychange", beat);
beat();

// フロントの例外を Go 側のログへ。**今まではどこにも出ていなかった**
// (DevTools を開いていない限り消える)ので、「例外は出ていない」を根拠にできなかった。
window.addEventListener("error", (e) => {
  void DiagService.ReportError(windowName, "error", String(e.message), e.error?.stack ?? "");
});
window.addEventListener("unhandledrejection", (e) => {
  const reason = e.reason as { message?: string; stack?: string } | undefined;
  void DiagService.ReportError(
    windowName,
    "unhandledrejection",
    reason?.message ?? String(e.reason),
    reason?.stack ?? "",
  );
});

if (windowName === "frame") {
  document.title = "ikkyoku";
  document.documentElement.classList.add("is-frame");
  mountFrame(app);
} else if (windowName === "evalgraph") {
  // ⚠️ **タイトルは窓の名前になる**（この窓だけ OS のタイトルバーを持つ）。
  document.title = "評価値グラフ - ikkyoku";
  document.documentElement.classList.add("is-graph");
  mountGraphScreen(app);
} else {
  document.title = "ikkyoku";
  document.documentElement.classList.add("is-main");
  mountMainScreen(app);
}
