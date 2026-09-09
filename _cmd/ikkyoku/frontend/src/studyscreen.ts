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

  // この窓が今使われているか（＝**切り離しているか**）。
  //
  // ⚠️ **窓は切り離していなくても作られている**（`Hidden: true` で作って
  // `Show()` するだけ）ので、**中身は起動した時点から動いている。**
  // ⚠️ **使われていないあいだは窓をまたぐ知らせを出さないこと**（下の `tell`）。
  let sideDetached = false;

  // tell は**盤に効く知らせ**をメイン画面へ送る（`Events.Emit` は Go を経由して
  // 全部の窓へ配られる）。
  //
  // ⚠️ **使われていないあいだは出さないこと**（2026-09-09 に踏んだ）。隠れている
  // この窓にも `study:changed` は届き、描き直しのたびに `onBusy` が呼ばれる ——
  // そこで `study:busy { on: false }` を出していたので、**連続解析の最中に
  // メイン画面の幕が 1 手ごとに消えてはまた出る**（チカチカする）。
  // ⚠️ **ドックしているあいだの知らせはメイン画面が自分のコールバックで持っている**
  // ので、こちらから送る相手も居ない（同じ値を 2 経路で描かないこと）。
  const tell = (name: string, data: unknown) => {
    if (!sideDetached) {
      return;
    }
    void Events.Emit(name, data);
  };

  // 最後に描いた状態の版（`StudyState.rev`）。
  //
  // ⚠️ **メイン画面と同じ形にすること**（あちらの `studyRev`）。`study:changed` は
  // **メソッドの戻り値とは別の経路**で届くので、**順番が入れ替わりうる。**
  //
  // ⚠️ **捨てないと連続解析が一気に走り抜ける**（2026-09-09 に踏んだ）。古い局面が
  // 後から届くと、側の列は「局面が変わった」と読んで
  // **走っている解析を `AnalyzeService.Stop()` で打ち切る** —— 打ち切られた解析も
  // `analyze:done` を出すので、**連続解析はそれを「1 手ぶん終わった」と読んで
  // 次の手へ進む**。これが 1 手ごとに起きると、手数ぶんが数秒で流れる。
  // ⚠️ **戻り値で描いた版も控えること** —— 控えないと、同じ状態を
  // **戻り値とイベントの 2 回**描くことになる（描き直しが 2 倍になるだけでなく、
  // 上の食い違いの窓が 1 手ごとに開く）。
  let studyRev = 0;
  const showStudy = (st: StudyState | null) => {
    studyRev = Math.max(studyRev, st?.rev ?? 0);
    pane.render(st);
  };

  pane = mountSidePane({
    host: q<HTMLElement>("#study-side"),
    // ⚠️ **窓のときだけ置く**（ドック側に戻す相手は居ない）。
    action: dock,
    // 手順の操作で局面が変わった。**盤は別の窓**なので、ここで描くのは自分だけ
    // （盤は `study:changed` を受けて自分で追随する）。
    onState: (st) => showStudy(st),
    // 候補手を選んだ → **盤の矢印**。窓をまたぐ唯一の「見せるだけ」の連動。
    onHint: (usi) => tell("study:hint", usi),
    // 勝率バーは盤の上にある（＝別の窓）。
    onScores: (scores) => tell("study:scores", scores),
    // 幕は**両方の窓**に要る（盤も触らせない）。
    onBusy: (on, note) => {
      veil.hidden = !on;
      veilNote.textContent = note;
      if (on) {
        // **キーボードでも止められるように**、出したらフォーカスを移す。
        q<HTMLButtonElement>("#batch-veil-cancel").focus();
      }
      tell("study:busy", { on, note });
    },
    // 設定を書き換えた（色・候補手の本数）。⚠️ **設定タブは別の窓**なので、
    // 描き直しは向こうに任せる（こちらは自分の写しを入れ直すだけ）。
    onSettings: (s) => {
      pane.setEngines(s.engines ?? [], s.engineColors ?? [], s.analyzeSeconds);
      tell("settings:changed", null);
    },
  });

  // ⚠️ **隠れているあいだは「使われていない面」**（2026-09-09 に踏んだ）。
  //
  // **この窓は切り離していなくても作られている**（`Hidden: true` で作って、
  // 切り離したときに `Show()` するだけ。破棄しないのは位置と大きさを失わないため）。
  // つまり**中身は最初から動いていて、`study:changed` も届いている。**
  //
  // 何もしないと、**連続モード（既定で入）がここでも解析を起こす** ——
  // 手を 1 手進めるたびに**メイン画面とこの窓の両方が `AnalyzeService.Start` を呼び、
  // 互いの解析を打ち切り合う。** 連続解析では 1 手ごとにこれが起きるので、
  // **打ち切られた `analyze:done` で次の手へ進んで一気に走り抜け**、
  // 局面の違う探索が重なって、そのうち噛み合わなくなる。
  //
  // ⚠️ **既定は「使われていない」に倒すこと。** 設定が届く前に 1 手進んだだけでも
  // 上が起きるので、**出ていると分かってから起こす。**
  pane.setActive(false);

  // ⚠️ **見るのは「切り離しているか」の 1 つだけ。** `document.hidden` を条件に
  // 足さないこと —— 窓が他の窓に隠れただけでも下りることがあり、
  // **見ている目の前で連続解析が黙って止まる**（`setActive(false)` は止める）。
  // 切り離していないあいだ、この窓は `Hide()` されているので条件はこれで足りる。
  const applyActive = (detached: boolean) => {
    pane.setActive(detached);
  };
  // ⚠️ **切り離しの状態を持っているのは設定**（`Config.StudyPaneDetached`）。
  // Go 側が切り替えるたびに知らせてくるので、ここで作らないこと。
  Events.On("side:detached", (event: { data: boolean }) => {
    sideDetached = !!event.data;
    applyActive(sideDetached);
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
  //
  // ⚠️ **版が古いイベントは捨てること**（メイン画面と同じ。上の `studyRev`）。
  Events.On("study:changed", (event: { data: StudyState }) => {
    const st = event.data;
    if (!st || (st.rev ?? 0) <= studyRev) {
      return;
    }
    showStudy(st);
  });

  const load = () => {
    void (async () => {
      try {
        const s = await SettingsService.Settings();
        pane.setEngines(s.engines ?? [], s.engineColors ?? [], s.analyzeSeconds);
        // ⚠️ **起動直後の形は設定から読むこと。** Go 側は起動時にも
        // `side:detached` を出すが、**その時点でこの窓はまだ購読していない**
        // （グラフの窓で踏んだのと同じ罠）。
        sideDetached = !!s.studyPaneDetached;
        applyActive(sideDetached);
      } catch {
        // 読めなくても既定の色と本数で動く（設計原則3）。
      }
      try {
        showStudy(await StudyService.State());
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
