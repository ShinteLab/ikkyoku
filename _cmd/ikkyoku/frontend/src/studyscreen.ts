// 切り離した**盤の右の列**の窓（2026-09-08。`?window=study`）。
//
// **盤の大きさに依存しない大きさで見たい**というのが切り離しの目的
// （`--board-size` は右の列の幅を引いている）。
//
// ⚠️ **中身はドックしたときと同じ**（`sidepane.ts`）。ここが持つのは
// **窓としての体裁**（幕・「ドックに戻す」）と、**イベントの配線**だけ。
//
// ⚠️ **盤に効くものは窓をまたいで送る**（`study:hint` / `study:scores` /
// `study:busy`）。フロントの `Events.Emit` は Go を経由して**全部の窓へ**配られる
// （`EmitEvent` → `dispatchEventToWindows`）ので、メイン画面がそれを受けて描く。
// ⚠️ **届かなくても壊れないこと**（設計原則3）—— 盤の矢印・勝率バー・幕が
// 更新されなくなるだけで、盤も手順も解析も動く。
import { Events } from "@wailsio/runtime";
import { FiMinimize2 } from "react-icons/fi";

import {
  SettingsService,
  StudyService,
} from "../bindings/github.com/ShinteLab/ikkyoku/app";
import type { StudyState } from "../bindings/github.com/ShinteLab/ikkyoku/app/models";
import { iconMarkup } from "./icon";
import { mountSidePane, type SidePaneHandle } from "./sidepane";

export function mountStudyScreen(root: HTMLElement) {
  root.innerHTML = `
    <div class="study-screen">
      <div id="study-side" class="study-side"></div>
      <!-- 連続解析のあいだ被せる幕。⚠️ **こちらにも要る** —— 1 手ずつ局面を
           動かしている最中に手順を触ると、自分の操作と連続解析が同じ局面を取り合う。 -->
      <div id="batch-veil" class="veil" hidden>
        <div class="veil-box">
          <p id="batch-veil-note" class="veil-note">連続解析中…</p>
          <button id="batch-veil-cancel" class="veil-btn" type="button">解析をキャンセル</button>
        </div>
      </div>
    </div>
  `;

  const q = <T extends HTMLElement>(sel: string) => root.querySelector<T>(sel)!;
  const veil = q<HTMLElement>("#batch-veil");
  const veilNote = q<HTMLElement>("#batch-veil-note");

  // ⚠️ **「ドックに戻す」は解析の行の右端に入れる**（2026-09-09）。
  // 窓の一番上に見出しの行を作ると、そのぶん**候補手と手順の取り分が減る**
  // —— 切り離すのは大きく見たいからなので、行を積むのは逆行する。
  // ⚠️ **戻す入口はこれと「窓を閉じる」の 2 つ。** どちらも同じ
  // `SettingsService.SetStudyPaneDetached(false)` を通るので食い違わない。
  // ⚠️ **アイコンだけなので、意味は `aria-label` と `title` が持つ**
  // （手順の「再読み込み」と同じ。**`title` を空にしないこと** —— 文字が無いぶん、
  // 何のボタンかはこれでしか読めない）。
  const dock = document.createElement("button");
  dock.id = "side-dock";
  dock.className = "icon-btn";
  dock.type = "button";
  dock.innerHTML = iconMarkup(FiMinimize2);
  dock.setAttribute("aria-label", "ドックに戻す");
  dock.title = "ドックに戻す: 解析の列をメイン画面の中へ戻します（窓を閉じても同じです）";
  dock.addEventListener("click", () => {
    void SettingsService.SetStudyPaneDetached(false);
  });

  // ⚠️ **`pane` は自分の options から参照するので `let` で先に置く**
  // （マウントし終わるまで中身は無いが、呼ばれるのはそのあと）。
  let pane: SidePaneHandle;

  pane = mountSidePane({
    host: q<HTMLElement>("#study-side"),
    // ⚠️ **窓のときだけ置く**（ドック側に戻す相手は居ない）。
    action: dock,
    // 手順の操作で局面が変わった。**盤は別の窓**なので、ここで描くのは自分だけ
    // （盤は `study:changed` を受けて自分で追随する）。
    onState: (st) => pane.render(st),
    // 候補手を選んだ → **盤の矢印**。窓をまたぐ唯一の「見せるだけ」の連動。
    onHint: (usi) => void Events.Emit("study:hint", usi),
    // 勝率バーは盤の上にある（＝別の窓）。
    onScores: (scores) => void Events.Emit("study:scores", scores),
    // 幕は**両方の窓**に要る（盤も触らせない）。
    onBusy: (on, note) => {
      veil.hidden = !on;
      veilNote.textContent = note;
      if (on) {
        // **キーボードでも止められるように**、出したらフォーカスを移す。
        q<HTMLButtonElement>("#batch-veil-cancel").focus();
      }
      void Events.Emit("study:busy", { on, note });
    },
    // 設定を書き換えた（色・候補手の本数）。⚠️ **設定タブは別の窓**なので、
    // 描き直しは向こうに任せる（こちらは自分の写しを入れ直すだけ）。
    onSettings: (s) => {
      pane.setEngines(s.engines ?? [], s.engineColors ?? [], s.analyzeSeconds);
      void Events.Emit("settings:changed", null);
    },
  });

  q<HTMLButtonElement>("#batch-veil-cancel").addEventListener("click", () => pane.cancelBatch());
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !veil.hidden) {
      pane.cancelBatch();
    }
  });

  // ---- 連動 ---------------------------------------------------------------
  //
  // ⚠️ **局面は持たない。** どちらの窓から押しても変わるのは Go 側の 1 つの手順で、
  // **両方の窓が同じイベントで追随する。**
  Events.On("study:changed", (event: { data: StudyState }) => {
    pane.render(event.data ?? null);
  });

  const load = () => {
    void (async () => {
      try {
        const s = await SettingsService.Settings();
        pane.setEngines(s.engines ?? [], s.engineColors ?? [], s.analyzeSeconds);
      } catch {
        // 読めなくても既定の色と本数で動く（設計原則3）。
      }
      try {
        pane.render(await StudyService.State());
      } catch {
        /* 局面が取れなければ空のまま。 */
      }
    })();
  };

  // ⚠️ **設定はメイン画面でも変わる**（設定タブ）。向こうが知らせてくるので拾う。
  Events.On("settings:changed", load);
  // ⚠️ **隠れているあいだの変更を受け取っていない**ので、出てきたら取り直す
  // （ドックに戻して出し直すと、この窓は隠れていただけで生きている）。
  document.addEventListener("visibilitychange", () => {
    if (!document.hidden) {
      load();
    }
  });

  load();
}
