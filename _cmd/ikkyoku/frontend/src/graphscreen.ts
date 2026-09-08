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

import {
  SettingsService,
  StudyService,
} from "../bindings/github.com/ShinteLab/ikkyoku/app";
import { mountEvalPane } from "./evalgraphpane";

// 設定に無いエンジンの色（`mainscreen.ts` と同じ値にすること）。
const UNKNOWN_ENGINE_COLOR = "#b8c0d0";

export function mountGraphScreen(root: HTMLElement) {
  root.innerHTML = `
    <div class="graph-screen">
      <div class="eval-graph-head">
        <span class="field-label">評価値</span>
        <select id="eval-graph-range" class="eval-graph-range"
                title="横軸の範囲。「全て」は指した手が全部見える範囲、「自由入力」は書いたとおりの手数です">
          <option value="all" selected>全て</option>
          <option value="custom">自由入力</option>
        </select>
        <span id="eval-graph-fields" class="eval-graph-fields" hidden>
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
        <button id="graph-dock" class="ghost-btn" type="button"
                title="評価値グラフをメイン画面の中へ戻します（窓を閉じても同じです）">ドックに戻す</button>
      </div>
      <div id="eval-graph" class="eval-graph"
           title="押すとその局面に戻ります（手順は消えません）。横にドラッグするとその範囲に絞ります"></div>
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
    } catch {
      // 読めなくても既定の色で描ける（設計原則3）。
    }
  };

  const pane = mountEvalPane({
    host: q<HTMLElement>("#eval-graph"),
    range: q<HTMLSelectElement>("#eval-graph-range"),
    from: q<HTMLInputElement>("#eval-graph-from"),
    to: q<HTMLInputElement>("#eval-graph-to"),
    fields: q<HTMLElement>("#eval-graph-fields"),
    legend: q<HTMLElement>("#eval-graph-legend"),
    readout: q<HTMLElement>("#eval-graph-readout"),
    colorOf: (id) => colors.get(id) ?? UNKNOWN_ENGINE_COLOR,
    // **押したらその局面へ戻る**（手順のチップと同じ操作。手順は消さない）。
    // ⚠️ **メイン画面を直に触らない。** `GoTo` が `study:changed` を出すので、
    // 盤も手順もあちらが自分で追随する（**それが連動の土台**）。
    onSeek: (id) => void StudyService.GoTo(id).catch(() => {}),
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

  q<HTMLButtonElement>("#graph-dock").addEventListener("click", () => {
    void SettingsService.SetEvalGraphDetached(false);
  });

  refresh();
}
