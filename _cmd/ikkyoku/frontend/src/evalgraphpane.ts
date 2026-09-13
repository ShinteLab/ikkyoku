// 評価値グラフの「ペイン」一式（2026-09-08）。
//
// `evalgraph.ts`（**描く側**）に、**Go から点を取り直す**ぶんを足したもの。
//
// ⚠️ **ドックしたペイン（`mainscreen.ts`）と、切り離した窓（`graphscreen.ts`）の
// 両方がこれを使う。** 片方だけ直すと、**置き場所を変えただけで挙動が変わる**。
//
// ⚠️ **点はここにも溜めない。** 持っているのは Go 側（`StudyService.Evals`）で、
// **手順を切ったときにどこまで捨てるかを知っているのはあちらだけ**。
import { StudyService } from "../bindings/github.com/ShinteLab/ikkyoku/app";
import type { EvalGraph } from "../bindings/github.com/ShinteLab/ikkyoku/app/models";
import { mountEvalGraph, type EvalGraphOptions, type EvalMode } from "./evalgraph";

export interface EvalPaneHandle {
  // render は手元の値をそのまま描く（普段は refresh を使う）。
  render(graph: EvalGraph | null): void;
  // refresh は Go から点を取り直して描く。
  //
  // **呼ぶのは「局面が変わったとき」と「解析が 1 つ終わったとき」。**
  refresh(): void;
  // refreshSoon は途中経過から呼ぶ側。**間引く。**
  //
  // ⚠️ **`analyze:info` のたびに取り直さないこと** —— 深さが 1 つ進むたびに、
  // しかもエンジンの数だけ届くので、そのまま往復させると読み筋の更新より頻繁になる。
  // ⚠️ **かといって `analyze:done` だけにもできない** —— 考える秒数が「無制限」の
  // ときは**止めるまで done が来ない**ので、その手の点がいつまでも出ない。
  refreshSoon(): void;
  // relayout は測り直して描き直す（隠れているあいだは測れないため）。
  relayout(): void;
  // setMode は縦軸（評価値 / 勝率）を設定から入れ直す。
  //
  // ⚠️ **ドック側と切り離した窓で同じ設定を読むこと** —— 置き場所を変えただけで
  // 縦軸が変わると、**どちらが本当の軸か**が分からなくなる。
  setMode(mode: EvalMode): void;
  // setActive は「今このペインが出ているか」。
  //
  // ⚠️ **切り離しているあいだ、ドック側は描かないこと**（その逆も同じ）。
  // 見えていない側まで取り直すと、**イベント 1 回につき往復が 2 回**になる。
  setActive(on: boolean): void;
}

// 途中経過からの取り直しを間引く間隔。
const SOON_MS = 1000;

export function mountEvalPane(opts: EvalGraphOptions): EvalPaneHandle {
  const ui = mountEvalGraph(opts);
  // 待っているタイマー（0 なら待っていない）。
  let timer = 0;
  let active = true;

  const clear = () => {
    if (timer !== 0) {
      window.clearTimeout(timer);
      timer = 0;
    }
  };

  const refresh = () => {
    clear();
    if (!active) {
      return;
    }
    void (async () => {
      try {
        ui.render(await StudyService.Evals());
      } catch {
        // 取れなくてもグラフが古いままになるだけ。**盤も解析も止めない**（設計原則3）。
      }
    })();
  };

  return {
    render: (g) => ui.render(g),
    refresh,
    refreshSoon: () => {
      if (timer !== 0 || !active) {
        return;
      }
      timer = window.setTimeout(() => {
        timer = 0;
        refresh();
      }, SOON_MS);
    },
    relayout: () => ui.relayout(),
    setMode: (m) => ui.setMode(m),
    setActive: (on) => {
      if (active === on) {
        return;
      }
      active = on;
      if (on) {
        // ⚠️ **出したら取り直すこと。** 隠れているあいだの変更を受け取っていないので、
        // そのままだと**畳む前の古い折れ線**が出る。
        refresh();
      } else {
        clear();
      }
    },
  };
}
