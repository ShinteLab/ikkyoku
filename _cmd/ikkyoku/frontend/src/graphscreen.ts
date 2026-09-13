// 切り離した評価値グラフの窓（2026-09-08）。
//
// **ペインのままだと盤の大きさに効く**（`--board-size` がグラフの高さを引いている）
// ので、盤を好きな大きさにしたい人のために窓へ出せるようにした。
//
// ⚠️ **描くものはドックしたペインと同じ**（`evalgraphpane.ts`）。ここが持つのは
// **窓としての体裁**（見出しの行・ドックに戻すボタン）と、**イベントの配線**だけ。
//
// ⚠️ **状態は持たない。** 点は `StudyService.Evals()`、色は `SettingsService`。
// **窓が増えても真実は Go 側に 1 つ**、という線引きを崩さないこと。
import { Events } from "@wailsio/runtime";
import { FiMinimize2 } from "react-icons/fi";

import {
  SettingsService,
  StudyService,
} from "../bindings/github.com/ShinteLab/ikkyoku/app";
import { iconMarkup } from "./icon";
import { mountEvalPane } from "./evalgraphpane";
import type { EvalMode } from "./evalgraph";

// 設定に無いエンジンの色（`mainscreen.ts` と同じ値にすること）。
const UNKNOWN_ENGINE_COLOR = "#b8c0d0";

export function mountGraphScreen(root: HTMLElement) {
  root.innerHTML = `
    <div class="graph-screen">
      <div class="eval-graph-head">
        <!-- 縦軸（2026-09-13）。⚠️ **ドック側と同じ作りにすること**
             （置き場所を変えただけで操作が変わらないこと）。
             切り替えると設定に保存され、メイン画面のグラフも同じ軸になる。 -->
        <span id="eval-graph-mode" class="eval-graph-mode" role="group" aria-label="縦軸">
          <button type="button" data-mode="eval" class="is-on" aria-pressed="true"
                  title="評価値（センチポーン）そのまま。目盛りは等間隔で、±3000 で頭打ちです">評価値</button>
          <button type="button" data-mode="scaled" aria-pressed="false"
                  title="圧縮: ±500 までで上下半分、±3000（まぁ勝ち）までで 8 割、±30000（詰み・必至）までを端に畳みます。目盛りの数字は評価値のままです">圧縮</button>
          <button type="button" data-mode="winrate" aria-pressed="false"
                  title="先手の勝率（0〜100%）で見ます。評価値からの変換で、定数は設定タブ">勝率</button>
        </span>
        <label class="eval-graph-all"
               title="全て: 指した手が全部見える範囲にします。外すと右の欄の手数がそのまま横軸になります">
          <input id="eval-graph-all" type="checkbox" checked />
          <span>全て</span>
        </label>
        <!-- 横軸の範囲の欄。⚠️ **「全て」のときも隠さないこと**（2026-09-10）——
             今どの範囲を見ているのかが画面から読めなくなる。「全て」のあいだは
             触れなくして、実際の範囲（根の手数〜最終手）を書き込む。
             ⚠️ **「自由入力」という札は要らない** —— チェックを外せば
             触れるようになる欄そのものが、そう言っている。 -->
        <span id="eval-graph-fields" class="eval-graph-fields">
          <input id="eval-graph-from" class="eval-graph-num" type="number"
                 min="0" max="999" step="1" value="1" title="左端の手数" />
          <span class="eval-graph-dash">-</span>
          <input id="eval-graph-to" class="eval-graph-num" type="number"
                 min="1" max="999" step="1" value="150" title="右端の手数" />
        </span>
        <span id="eval-graph-legend" class="eval-graph-legend"></span>
        <span id="eval-graph-readout" class="note eval-graph-readout"></span>
        <!-- ⚠️ **戻す入口はここと「窓を閉じる」の 2 つ。** どちらも同じ
             SettingsService.SetEvalGraphDetached(false) を通るので食い違わない。 -->
        <!-- ⚠️ アイコンだけなので、意味は aria-label と title が持つ
             （解析の列の窓と同じアイコンにすること。2 つの窓で作法を揃える）。 -->
        <button id="graph-dock" class="icon-btn" type="button" aria-label="ドックに戻す"
                title="ドックに戻す: 評価値グラフをメイン画面の中へ戻します（窓を閉じても同じです）"></button>
      </div>
      <div id="eval-graph" class="eval-graph"
           title="押すとその局面に戻ります（手順は消えません）。横にドラッグするとその範囲に絞ります"></div>
      <!-- 連続解析のあいだ被せる幕。⚠️ **この窓にも要る**（2026-09-09 に踏んだ）——
           ⚠️ template literal の中なので、コメントにバッククォートを使わないこと
           （文字列がそこで切れる）。
           **点を押すと GoTo が飛ぶ**ので、1 手ずつ局面を動かしている最中に
           押されると**自分の操作と連続解析が同じ局面を取り合って壊れる。**
           ⚠️ **止める口を中に置くこと**（幕は下を全部塞ぐ。ここには「停止」も
           手順も無いので、出口が無いと窓を閉じるしかなくなる）。 -->
      <div id="batch-veil" class="veil" hidden>
        <div class="veil-box">
          <p id="batch-veil-note" class="veil-note">連続解析中…</p>
          <button id="batch-veil-cancel" class="veil-btn" type="button">解析をキャンセル</button>
        </div>
      </div>
    </div>
  `;

  const q = <T extends HTMLElement>(id: string) => root.querySelector<T>(id)!;

  // 折れ線の色（**エンジンごと**）。⚠️ **フロントに色の表を持たないこと** ——
  // 既定色の解決も選べる色の一覧も Go 側で、ここは受け取った写しを引くだけ。
  const colors = new Map<string, string>();
  const syncColors = async () => {
    try {
      const s = await SettingsService.Settings();
      colors.clear();
      for (const e of s.engines ?? []) {
        colors.set(e.id, e.color);
      }
      // ⚠️ **縦軸も設定から受け取ること**（2026-09-13）。この窓とメイン画面で
      // 別の軸になると、**どちらが本当の軸か**が分からなくなる。
      pane.setMode(s.evalGraphAxis as EvalMode);
    } catch {
      // 読めなくても既定の色・既定の軸で描ける（設計原則3）。
    }
  };

  const pane = mountEvalPane({
    host: q<HTMLElement>("#eval-graph"),
    all: q<HTMLInputElement>("#eval-graph-all"),
    from: q<HTMLInputElement>("#eval-graph-from"),
    to: q<HTMLInputElement>("#eval-graph-to"),
    mode: q<HTMLElement>("#eval-graph-mode"),
    legend: q<HTMLElement>("#eval-graph-legend"),
    readout: q<HTMLElement>("#eval-graph-readout"),
    colorOf: (id) => colors.get(id) ?? UNKNOWN_ENGINE_COLOR,
    // **押したらその局面へ戻る**（手順のチップと同じ操作。手順は消さない）。
    // ⚠️ **メイン画面を直に触らない。** `GoTo` が `study:changed` を出すので、
    // 盤も手順もあちらが自分で追随する（**それが連動の土台**）。
    onSeek: (id) => void StudyService.GoTo(id).catch(() => {}),
    // 縦軸を切り替えたら保存し、**メイン画面にも知らせる**（設定タブの表示と
    // ドック側のグラフが追随する）。⚠️ **あちらの画面を直に触らないこと** ——
    // 真実は Go 側の設定 1 つで、**どちらの窓も同じイベントで追随する。**
    onMode: (m) => {
      void (async () => {
        try {
          await SettingsService.SetEvalGraphAxis(m);
          await Events.Emit("settings:changed", null);
        } catch {
          // 保存できなくてもこの窓はその軸のまま（次の起動で戻るだけ）。
        }
      })();
    },
  });

  // ⚠️ **取り直す口は 1 本にまとめること**（色と点がばらばらに更新されると、
  // 折れ線と凡例で色が食い違う瞬間ができる）。
  const refresh = () => {
    void syncColors();
    pane.refresh();
  };

  // ---- 連動 ---------------------------------------------------------------
  //
  // ⚠️ **この窓は局面を持たない。** メイン画面で手を辿ってもここで押しても、
  // 変わるのは Go 側の 1 つの手順で、**どちらの窓も同じイベントで追随する**。
  Events.On("study:changed", refresh);
  // ⚠️ **設定の変更にも追随すること**（色・縦軸）。メイン画面の設定タブで
  // 色を変えても、この窓は `study:changed` を受け取らない。
  Events.On("settings:changed", refresh);
  // 解析の途中経過。**間引く**（深さが 1 つ進むたびに、しかもエンジンの数だけ届く）。
  Events.On("analyze:info", () => pane.refreshSoon());
  Events.On("analyze:done", refresh);
  Events.On("analyze:failed", refresh);

  // ⚠️ **窓の大きさが変わったら測り直すこと**（`clientWidth` で描いている）。
  window.addEventListener("resize", () => pane.relayout());
  // ⚠️ **隠れているあいだは測れない**ので、出てきた瞬間にも測り直す
  // （ドックに戻して出し直すと、この窓は隠れていただけで生きている）。
  document.addEventListener("visibilitychange", () => {
    if (!document.hidden) {
      pane.relayout();
      refresh();
    }
  });

  // ---- 連続解析の幕 --------------------------------------------------------
  //
  // ⚠️ **状態は持たない。** 走っているかを知っているのは**解析の列**（`sidepane.ts`）
  // なので、そちらが `study:busy` で知らせてくる。**ここで数えないこと。**
  // ⚠️ **出しているのは「今使われている側の列」だけ**（ドックならメイン画面、
  // 切り離していればその窓）。隠れているほうも配ると、**1 手ごとに幕が瞬く。**
  const veil = q<HTMLElement>("#batch-veil");
  const veilNote = q<HTMLElement>("#batch-veil-note");
  Events.On("study:busy", (event: { data: { on: boolean; note: string } }) => {
    veil.hidden = !event.data?.on;
    veilNote.textContent = event.data?.note ?? "";
  });
  // ⚠️ **止めるのは持ち主に頼むこと**（`study:cancel`）。連続解析を持っているのは
  // 解析の列で、**この窓は状態を持たない**。直に `AnalyzeService.Stop()` を呼ぶと、
  // 今の 1 手が止まるだけで**次の手が始まる**（止めたのに止まらない）。
  const cancel = q<HTMLButtonElement>("#batch-veil-cancel");
  cancel.addEventListener("click", () => void Events.Emit("study:cancel", null));
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !veil.hidden) {
      e.preventDefault();
      void Events.Emit("study:cancel", null);
    }
  });

  const dock = q<HTMLButtonElement>("#graph-dock");
  dock.innerHTML = iconMarkup(FiMinimize2);
  dock.addEventListener("click", () => {
    void SettingsService.SetEvalGraphDetached(false);
  });

  refresh();
}
