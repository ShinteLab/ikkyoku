// 解析タブの寸法と切り離し（2026-11-13 に `mainscreen.ts` から切り出した）。
//
// 評価値グラフの高さ（横のスプリットバーと折り畳み）・盤と解析の列の幅（縦のスプリットバー）・
// 列の折り畳みとアニメーション・評価値グラフと右の列の別ウィンドウへの切り離し。
// **先に `_docs/ui/layout.md` を読むこと**（ここの ⚠️ はどれも実際に踏んだもの）。
//
// ⚠️ **盤の大きさは式で決まる**（`style.css` の `--board-size`）。ここが書き換えるのは
// CSS 変数（`--eval-graph-h` / `--eval-graph-pad` / `--study-side-min` / `--study-side-max`）と
// `#panel-study` の class（`is-side-collapsed` / `is-side-detached` / `is-animating`）だけで、
// 大きさはその式から付いてくる。⚠️ **`is-toprow-empty` は `mainscreen.ts` の `applyStudyTopRow`** が持つ。
//
// ⚠️ **受け渡しは `StudyLayoutOptions` / `StudyLayoutHandle` だけ**（`sidepane.ts` と同じ型）。
// 盤と側の列とグラフには**測り直し（`relayout`）と出す面（`setParts` / `setActive`）しか頼まない。**
// ⚠️ **切り離しの状態の持ち主は Go 側**（設定と `graph:detached` / `side:detached` / `moves:detached`）。
// ここは写しで、切り替えは必ず `SettingsService` を通す。
import { Events } from "@wailsio/runtime";
import {
  FiChevronDown,
  FiChevronLeft,
  FiChevronRight,
  FiChevronUp,
  FiExternalLink,
} from "react-icons/fi";
import { SettingsService } from "../bindings/github.com/ShinteLab/ikkyoku/app";
import type { EvalPaneHandle } from "./evalgraphpane";
import { iconMarkup } from "./icon";
import type { SidePaneHandle } from "./sidepane";
import type { StudyBoardHandle } from "./study";

export interface StudyLayoutOptions {
  // root はメイン画面（`#panel-study` と、バー・トグル・「切り離す」のボタンを引く）。
  root: HTMLElement;
  // studyStage は盤の実寸を測る相手（`.board-stage`）。⚠️ **式の定数を JS に写さず、測って決める。**
  studyStage: HTMLElement;
  // studySide は盤の右の列（`#study-side`）。
  studySide: HTMLElement;
  // evalGraphRow は評価値グラフの行（`#eval-graph-row`）。
  evalGraphRow: HTMLElement;
  // studyLoaded は解析タブに局面があるか。⚠️ **他の要素の `hidden` から読まないこと**（持ち主は `mainscreen.ts`）。
  studyLoaded(): boolean;
  // 大きさが変わったら測り直させる相手（盤に重ねたグリッドとグラフ）。
  studyBoardUI: StudyBoardHandle;
  evalGraphUI: EvalPaneHandle;
  // sidePane はドック側の右の列（切り離したとき出す面と、自動解析の持ち主を切り替える）。
  sidePane: SidePaneHandle;
  // 候補手の面 / 手順の面の「切り離す」ボタン（置き場所は側の列。`mainscreen.ts` が作って渡す）。
  studySplitDetach: HTMLElement;
  studyMovesDetach: HTMLElement;
}

export interface StudyLayoutHandle {
  // settle は縦のスプリットバーの遊びを消す（盤の大きさが変わりうる場面で呼ぶ。タブを開いた瞬間も）。
  settle(): void;
  // showPanes は局面の有無に合わせて、右の列・縦のバー・評価値グラフの行を出し入れする。
  showPanes(loaded: boolean): void;
  // sideDetached は**候補手の面**を切り離しているか（連続解析と候補手の持ち主が向こうの窓か）。
  sideDetached(): boolean;
  // 設定から切り離しの状態を受け取る（起動直後の形はここで決まる）。
  applyGraphDetached(on: boolean): void;
  applySideDetached(on: boolean): void;
  applyMovesDetached(on: boolean): void;
}

export function mountStudyLayout(opts: StudyLayoutOptions): StudyLayoutHandle {
  const { root } = opts;
  const {
    studyStage, studySide, evalGraphRow, studyBoardUI, evalGraphUI, sidePane,
    studySplitDetach, studyMovesDetach,
  } = opts;

  // ---- 評価値グラフの高さ（折り畳み + スプリットバー。2026-08-13）-----------
  //
  // ⚠️ **持っている値は「グラフの高さ」1 つだけ。** 折り畳みは**高さ 0** で表す。
  // 「畳んでいるか」の真偽値を別に持つと、**畳んでいるのに高さがある**という
  // 食い違いが起きうる（「外部エンジンを使う」の真偽値を持たないのと同じ話）。
  //
  // ⚠️ **書き込む先は `:root` の `--eval-graph-h` 1 か所。** 盤の大きさ
  // （`#panel-study` の `--board-size`）がこれを引いているので、**グラフを縮めた
  // ぶんだけ盤が自動で大きくなる**。⚠️ **カスタムプロパティは下へしか継承しない**
  // ので、グラフの箱に直接高さを書かないこと（盤の式から読めなくなる）。
  //
  // **その場かぎりの値**（config.json には持たない。視点と同じ扱い）。
  const evalGraphSplit = root.querySelector<HTMLDivElement>("#eval-graph-split")!;
  const evalGraphToggle = root.querySelector<HTMLButtonElement>("#eval-graph-toggle")!;
  // 既定値は style.css の `--eval-graph-h` と同じにすること（起動直後に
  // JS が書き込むまでは CSS 側の値が出ているので、食い違うと初回だけ跳ねる）。
  // ⚠️ **この値はグラフの箱の高さそのものではない**（2026-08-14）。
  // `.eval-graph` は**ここから 38px 詰めた高さ**で描くので、見えるグラフは
  // 116px（＝今までと同じ）になる。
  const EVAL_GRAPH_DEFAULT = 154;
  // 詰めるぶん（38px）。**style.css の `.eval-graph` の calc と同じ値にすること。**
  const EVAL_GRAPH_CHROME = 38;
  // これより低いと折れ線が読めないので、ここが「畳んでいない」ときの下限。
  // ⚠️ **見えるグラフのほうで 48px を確保する**（詰めるぶんを足しておく）。
  const EVAL_GRAPH_MIN = 48 + EVAL_GRAPH_CHROME;
  // ⚠️ **下限より下へドラッグしたら畳む**（0 にする）。下限で止めると、
  // ドラッグだけでは畳めないのに「一番下まで下げた」ようには見える。
  // ⚠️ **こちらも詰めるぶんを足す**（掴んでいる位置＝この値なので、見えている
  // グラフが 32px を切ったところで畳む、という手応えに揃える）。
  const EVAL_GRAPH_SNAP = 32 + EVAL_GRAPH_CHROME;
  let evalGraphH = EVAL_GRAPH_DEFAULT;
  // 畳む前の高さ。**畳んで開き直したときに元の高さへ戻すため**に覚えておく
  // （既定に戻すと、せっかく広げたのが畳むたびに失われる）。
  let evalGraphOpenH = EVAL_GRAPH_DEFAULT;
  // 評価値グラフを別ウィンドウへ切り離しているか（下の「別ウィンドウへの切り離し」）。
  // ⚠️ **null は「まだ決まっていない」。** 起動直後の 1 回だけは、値が同じでも
  // 通してレイアウトを整える必要がある。
  // ⚠️ **`setEvalGraphH` より前で宣言すること** —— あちらが読む。
  let graphDetached: boolean | null = null;

  // 上限は窓の高さから決める。**盤が潰れるところまで伸ばさせない**
  // （`#panel-study` はスクロールしないので、伸ばしすぎると盤がはみ出す）。
  // 360px は「タブの行 + 余白 + グラフの見出し + 勝率バー + 最低限の盤」の見積もり。
  const evalGraphMax = () => Math.max(EVAL_GRAPH_MIN, window.innerHeight - 360);

  const setEvalGraphH = (px: number) => {
    const next =
      px < EVAL_GRAPH_SNAP ? 0 : Math.min(Math.max(px, EVAL_GRAPH_MIN), evalGraphMax());
    if (next === evalGraphH) {
      return;
    }
    evalGraphH = next;
    if (next > 0) {
      evalGraphOpenH = next;
    }
    // ⚠️ **`documentElement` に入れること**（`:root`）。盤の式が読む先はここ。
    // ⚠️ **切り離しているあいだは 0 のまま**（2026-10-05 に踏んだ）。覚える高さは
    // 変えてよいが、盤の式に書き戻すと**行が無いのにそのぶん盤が小さくなり、
    // 盤の下に何も無い帯が残る**（窓のリサイズが上限で詰め直す経路から来る）。
    // 0 を入れるのは `applyGraphDetached` で、戻すときにここの値を入れ直す。
    if (graphDetached !== true) {
      document.documentElement.style.setProperty("--eval-graph-h", `${next}px`);
    }
    const open = next > 0;
    evalGraphRow.classList.toggle("is-collapsed", !open);
    evalGraphToggle.innerHTML = iconMarkup(open ? FiChevronDown : FiChevronUp);
    evalGraphToggle.setAttribute("aria-expanded", String(open));
    evalGraphToggle.title = open
      ? "評価値グラフを畳みます（そのぶん盤が大きくなります）"
      : "評価値グラフを開きます";
    evalGraphSplit.setAttribute("aria-valuenow", String(next));
    // ⚠️ **測り直しを 2 つとも落とさないこと。** グラフは `clientHeight` で
    // 描いており、**盤は式で大きさが変わる**ので重ねたグリッドがずれる。
    evalGraphUI.relayout();
    studyBoardUI.relayout();
    // ⚠️ **盤の高さの上限が動いたので、横の遊びも取り直す。** グラフを畳むと
    // 盤は縦に大きくなれるようになり、**そのぶん横の下限も下がる**。
    settleStudySide();
  };

  evalGraphToggle.addEventListener("click", () => {
    // ⚠️ **ドラッグ側（`setEvalGraphH` を毎フレーム呼ぶ経路）では付けないこと。**
    // 高さは変数 1 つなので、こちらは起点を作る小細工が要らない。
    beginAnimation();
    setEvalGraphH(evalGraphH > 0 ? 0 : evalGraphOpenH);
  });
  // ⚠️ **押してもドラッグが始まらないようにする**（縦のバーのトグルと同じ）。
  // 止めないと「掴んだ」と解釈されて、離すまで高さが動き続ける。
  evalGraphToggle.addEventListener("pointerdown", (e) => e.stopPropagation());

  // ドラッグ。**上へ引くと高くなる**（境目そのものを掴んでいる感覚に合わせる）。
  // ⚠️ **pointer capture を取ること** —— 掴んだまま盤の上やウィンドウの外へ
  // 出ることが普通にあるので、取らないと途中で追従が切れる。
  evalGraphSplit.addEventListener("pointerdown", (e) => {
    e.preventDefault();
    cancelAnimation();
    evalGraphSplit.setPointerCapture(e.pointerId);
    evalGraphSplit.classList.add("is-dragging");
    const startY = e.clientY;
    const startH = evalGraphH;
    const onMove = (ev: PointerEvent) => setEvalGraphH(startH - (ev.clientY - startY));
    const onUp = () => {
      evalGraphSplit.classList.remove("is-dragging");
      evalGraphSplit.removeEventListener("pointermove", onMove);
      evalGraphSplit.removeEventListener("pointerup", onUp);
      evalGraphSplit.removeEventListener("pointercancel", onUp);
    };
    evalGraphSplit.addEventListener("pointermove", onMove);
    evalGraphSplit.addEventListener("pointerup", onUp);
    evalGraphSplit.addEventListener("pointercancel", onUp);
  });

  // キーボードでも動かせるようにする（`role="separator"` の作法）。
  // ⚠️ **畳んだ状態からの ↑ は「開く」にすること** —— 8px 足しても
  // スナップの下限に届かず、押しても何も起きないように見える。
  evalGraphSplit.addEventListener("keydown", (e) => {
    const step = e.shiftKey ? 32 : 8;
    if (e.key === "ArrowUp") {
      e.preventDefault();
      setEvalGraphH(evalGraphH === 0 ? evalGraphOpenH : evalGraphH + step);
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      setEvalGraphH(evalGraphH - step);
    }
  });

  // 窓が低くなると上限も下がる。**はみ出したままにしないこと。**
  // ⚠️ **横の遊びも取り直す**（窓の幅が変われば盤の上限に張り付く一点も動く）。
  // ⚠️ **窓が狭くて盤が潰れたら、幅の指定を捨てて取り直す** —— 盤が読めない
  // 大きさのまま残るより、まず盤を成立させる。
  // ⚠️ **最小化（窓の大きさ 0）では何もしないこと**（2026-10-05）。上限が
  // 下限まで落ちるので、**最小化して戻しただけでグラフが 86px に縮んだまま**になる。
  window.addEventListener("resize", () => {
    if (window.innerWidth === 0 || window.innerHeight === 0) {
      return;
    }
    if (evalGraphH > 0) {
      setEvalGraphH(evalGraphH);
    }
    // ⚠️ **畳んでいるときは触らない**（0 を上書きすると、窓を動かしただけで
    // 勝手に開く）。畳んでいれば列は幅を取っていないので、そもそも起きない。
    // ⚠️ **両方とも切り離しているときも触らない**（2026-10-05）。列が無いのに
    // 幅を予約すると、**そのぶん盤が小さいまま**になる（`applyPanes` の 0 を上書きする）。
    if (!bothDetached() && studySideW > 0 && boardW() > 0 && boardW() < STUDY_BOARD_MIN) {
      studySideW = STUDY_SIDE_MIN;
      studySideOpenW = STUDY_SIDE_MIN;
      rawSide(STUDY_SIDE_MIN);
    }
    settleStudySide();
  });

  // ⚠️ **一度は通すこと**（ボタンのアイコンと `aria-*` はここで入れている）。
  // ⚠️ **`setEvalGraphH` は値が同じなら何もしない**ので、既定値そのものは通らない。
  evalGraphToggle.innerHTML = iconMarkup(FiChevronDown);
  evalGraphToggle.title = "評価値グラフを畳みます（そのぶん盤が大きくなります）";
  evalGraphSplit.setAttribute("aria-valuenow", String(evalGraphH));

  // ---- 別ウィンドウへの切り離し（2026-09-08）--------------------------------
  //
  // **ペインのままだと盤の大きさに効く**（`--board-size` がグラフの高さを引いている）
  // ので、盤を好きな大きさにしたい人のために窓へ出せるようにした。
  //
  // ⚠️ **畳む（高さ 0）とは別の状態。** 畳むのは「今は見ない」、切り離しは
  // 「**別の窓で見る**」。**畳んだ高さは覚えたまま**なので、戻せば元の高さで出る。
  //
  // ⚠️ **状態を持っているのは Go 側**（設定 `evalGraphDetached`）。ここが持つのは
  // 写しだけで、**切り替えは必ず `SettingsService.SetEvalGraphDetached` を通す** ——
  // グラフ窓を閉じる操作も同じ口を通るので、**どちらから切り替えても食い違わない。**
  const evalGraphDetach = root.querySelector<HTMLButtonElement>("#eval-graph-detach")!;
  evalGraphDetach.innerHTML = iconMarkup(FiExternalLink);

  // 切り離しているとき、盤から**余分に引く**量。**実測で決めた値**
  // （窓 4 通り × 畳み方で測り、**盤の下に 5px 残る**ところ。`--eval-graph-pad`
  // の表も読むこと）。行ごと消えるので、畳んだとき（20px）より小さい。
  // ⚠️ **0 にしないこと** —— 実測で盤の下端が 3px はみ出す。
  // ⚠️ **畳んだ行の高さ（バー 8 + gap 2 + 逃げ 6 = 16px）を変えたら測り直すこと。**
  const EVAL_GRAPH_PAD_DETACHED = "8px";

  // ⚠️ **`graphDetached` は上（`setEvalGraphH` の前）で宣言している。**
  const applyGraphDetached = (on: boolean) => {
    if (graphDetached === on) {
      return;
    }
    graphDetached = on;
    // ⚠️ **行ごと消すこと**（バーも見出しも）。同じ値を 2 か所に描かない。
    evalGraphRow.hidden = on || !opts.studyLoaded();
    // ⚠️ **盤の式が読む値も切り替えること。** 切り離したのに高さが残っていると、
    // **そのぶん盤が小さいまま**になる（何も無い余白ができる）。
    document.documentElement.style.setProperty(
      "--eval-graph-h",
      on ? "0px" : `${evalGraphH}px`,
    );
    if (on) {
      document.documentElement.style.setProperty(
        "--eval-graph-pad",
        EVAL_GRAPH_PAD_DETACHED,
      );
    } else {
      // ⚠️ **消すこと**（値を書き戻さない）。CSS 側は畳む/開くに追随する式なので、
      // inline が残ると**畳んでも引き算が変わらない**。
      document.documentElement.style.removeProperty("--eval-graph-pad");
    }
    // ⚠️ **見えていない側は取り直さない**（イベント 1 回で往復が 2 回になる）。
    evalGraphUI.setActive(!on);
    // 盤の大きさが変わったので、重ねたグリッドと横の遊びを取り直す。
    studyBoardUI.relayout();
    evalGraphUI.relayout();
    settleStudySide();
  };

  evalGraphDetach.addEventListener("click", () => {
    void SettingsService.SetEvalGraphDetached(true);
  });

  // ⚠️ **戻すのは向こうから**（グラフ窓の「ドックに戻す」と、窓を閉じる操作）。
  // Go 側が `graph:detached` で知らせてくるので、**こちらから状態を作らない。**
  Events.On("graph:detached", (event: { data: boolean }) => {
    applyGraphDetached(!!event.data);
  });

  // ---- 盤の右の列の切り離し（2026-09-08）------------------------------------
  //
  // **作りは評価値グラフと同じ**（設定が状態を持ち、切り替えは必ず
  // `SettingsService.SetStudyPaneDetached` を通る）。**揃えておくこと。**

  // ⚠️ **2 つある**（2026-09-12。候補手 / 手順）。**1 つにまとめないこと** ——
  // 片方だけ外に出す使い方が普通で、**どちらを出すかはそのときの読み方で変わる。**
  //
  // ⚠️ **`sideDetached` が指すのは候補手の面だけ**（2026-09-12 に意味が狭まった）。
  // 名前は設定・イベント（`studyPaneDetached` / `side:detached`）と揃えてある。
  let sideDetached: boolean | null = null;
  let movesDetached: boolean | null = null;

  // 列そのものが要らなくなるのは**両方とも外に出したとき**だけ。
  const bothDetached = () => sideDetached === true && movesDetached === true;

  // applyPanes は列の見た目を今の 2 つの状態に合わせる（**書き換える場所はここ 1 つ**）。
  //
  // ⚠️ **「局面があるか」は `studyLoaded` から読むこと。** 他の要素の `hidden` から
  // 読まないこと —— 切り離すと**別の理由で `hidden` になる**ので、戻したときに
  // 「局面が無い」と誤読する（2026-09-09 に踏んだ）。
  const applyPanes = () => {
    const both = bothDetached();
    // ⚠️ **列ごと消すのは両方切り離したときだけ**（バーも）。
    panelStudy.classList.toggle("is-side-detached", both);
    studySide.hidden = both || !opts.studyLoaded();
    studySplit.hidden = both || !opts.studyLoaded();
    // ⚠️ **盤の式が読む幅も 0 にすること。** 列が出ていないのに幅を予約したままだと、
    // **そのぶん盤が小さいまま**になる（何も無い余白ができる）。
    rawSide(both ? 0 : studySideW);
    // ⚠️ **ドック側が出す面は「切り離していないほう」**（同じ値を 2 か所に描かない）。
    sidePane.setParts({ analyze: sideDetached !== true, moves: movesDetached !== true });
    // ⚠️ **使われていない側は自動解析をしないこと**（2026-09-08）。切り離しても
    // ドック側のペインは隠れたまま生きているので、**両方が起こし合う**。
    // ⚠️ **持ち主を決めるのは候補手の面だけ**（エンジンを掴んでいるのがあちら）——
    // 手順を切り離しても、解析はこちらが持ったまま。
    sidePane.setActive(sideDetached !== true);
    studyBoardUI.relayout();
    evalGraphUI.relayout();
    if (!both) {
      settleStudySide();
    }
  };

  const applySideDetached = (on: boolean) => {
    if (sideDetached === on) {
      return;
    }
    sideDetached = on;
    applyPanes();
  };

  const applyMovesDetached = (on: boolean) => {
    if (movesDetached === on) {
      return;
    }
    movesDetached = on;
    applyPanes();
  };

  studySplitDetach.addEventListener("click", () => {
    void SettingsService.SetStudyPaneDetached(true);
  });
  studyMovesDetach.addEventListener("click", () => {
    void SettingsService.SetMovePaneDetached(true);
  });

  Events.On("side:detached", (event: { data: boolean }) => {
    applySideDetached(!!event.data);
  });
  Events.On("moves:detached", (event: { data: boolean }) => {
    applyMovesDetached(!!event.data);
  });

  // ---- 盤と解析の列の幅（縦のスプリットバー。2026-08-13）--------------------
  //
  // ⚠️ **書き換えるのは `--study-side-min`**（＋掴んだあとは `--study-side-max`）。
  // 盤の大きさ（`--board-size`）が前者を引いているので、詰めれば盤が大きくなる。
  // ⚠️ **`#panel-study` の inline style に入れること** —— あの変数は
  // `#panel-study` 自身が定義しているので、`:root` へ書いても負ける
  // （`--eval-graph-h` などとは事情が違う）。
  //
  // ⚠️ **掴むまでは列が余りを全部もらう**（`.study-side` は `flex: 1 1 auto`）。
  // つまり**列の実幅は「窓幅 − 盤」で決まっていて、`--study-side-min` は
  // 盤の式の引き算としてしか効かない。** 盤は縦（＝評価値グラフの高さ）でも
  // 決まるので、**盤が高さで頭打ちになったところから先は列が 1px も動かない** ——
  // 広い窓では列が 600px を超えたまま狭められなかった（2026-09-12 に外した制限）。
  //
  // ⚠️ **掴んだら列の幅を固定する**（`lockSide`。`--study-side-max`）。そこから先は
  // **1px 引けば 1px 動く**。⚠️ **余ったぶんは盤の側に出る**（盤は中央へ寄る。
  // 盤が高さで頭打ちのあいだは、引いたぶんがそのまま余白になる）。
  //
  // ⚠️ **掴むまでは固定しないこと。** 既定を「余りを全部もらう」にしておかないと、
  // **起動のたびに列が下限（300px）で始まる**（幅は設定に持っていない）。
  //
  // ⚠️ **判定は「実際の盤の幅を測って」行う。式の定数を JS に写さないこと。**
  // `--board-size` の式（`110px` や `1.4`）を写すと、CSS を直したときに
  // **黙って食い違う**（画面では気づけない）。測れば式が変わっても付いてくる。
  const panelStudy = root.querySelector<HTMLElement>("#panel-study")!;
  const studySplit = root.querySelector<HTMLDivElement>("#study-split")!;
  // 盤をこれより小さくしてまで解析の列を広げない。
  const STUDY_BOARD_MIN = 240;
  // ⚠️ **解析の列をこれより詰めない。** 盤を優先して詰め切ると、窓が狭いときに
  // **解析の行が入らない幅まで潰れて読めなくなる**（`.study-side` の下限の話）。
  const STUDY_SIDE_MIN = 300;
  // ⚠️ **0 は「畳んでいる」。** 評価値グラフの高さと同じで、**真偽値を別に
  // 持たない**（畳んでいるのに幅がある、という食い違いを作らないため）。
  // ドラッグの下限は STUDY_SIDE_MIN なので、**0 になるのは畳んだときだけ**。
  let studySideW = STUDY_SIDE_MIN; // style.css の --study-side-min と同じ既定
  // 開き直すときの幅。**畳む前の幅に戻す**（既定に戻すと、広げたのが失われる）。
  let studySideOpenW = STUDY_SIDE_MIN;

  // 人が幅を決めたか（2026-09-12）。**決めたら列は余りをもらわなくなる。**
  // ⚠️ **一度決めたら戻さないこと** —— 窓を広げるたびに列が太るのは、
  // 幅を決めたあとの振る舞いとしては裏切りになる（広がるのは盤の側）。
  let sideLocked = false;

  // 盤の実寸（`.board-stage` の幅 = `--board-size`）。タブが隠れていれば 0。
  const boardW = () => studyStage.getBoundingClientRect().width;
  // ⚠️ **決めたあとは min と max の両方に入れること。** `min-width` だけでは
  // 列は余りをもらったままなので、**下げても狭くならない**（この節の冒頭）。
  const rawSide = (px: number) => {
    const v = `${Math.round(px)}px`;
    panelStudy.style.setProperty("--study-side-min", v);
    if (sideLocked) {
      panelStudy.style.setProperty("--study-side-max", v);
    }
  };

  // lockSide は列の幅を人が決めたことにする（**バーを掴んだ / キーで動かした**）。
  //
  // ⚠️ **固定する値は「画面に出ている幅」を測って決めること**（2026-09-12 に踏んだ）。
  // `.study-side` は **`min-width: 0`** なので、`--study-side-min` は
  // **盤の式の引き算としてしか効かず、列の実幅は「余り」で決まっている** ——
  // つまり `studySideW`（`settleStudySide` が式から逆算した値）と
  // **実幅は一致するとは限らない。** その差のぶん、**掴んだ瞬間に列が飛ぶ**
  // （固定したあとは両者が一致するので、**2 回目以降だけ綺麗**という形で出る）。
  //
  // ⚠️ **`settleStudySide` のあとに呼ぶこと。** あちらが `--study-side-min` を
  // 詰めていないと、**盤の式が実幅と食い違ったまま固定される。**
  const lockSide = () => {
    if (sideLocked) {
      return;
    }
    const shown = Math.round(studySide.getBoundingClientRect().width);
    sideLocked = true;
    // ⚠️ **測れないときは今の値のまま**（タブが隠れている等。0 で固定すると
    // 列が消える）。
    if (shown > 0) {
      studySideW = Math.max(shown, STUDY_SIDE_MIN);
      studySideOpenW = studySideW;
      studySplit.setAttribute("aria-valuenow", String(studySideW));
    }
    rawSide(studySideW);
  };

  // settleStudySide は「盤が上限に張り付いたまま取れる最大の幅」まで詰める。
  //
  // **見た目は 1px も動かない**（盤は上限のまま、右の列は余りをもらうので）。
  // これをやっておかないと、**ドラッグし始めても最初のうち何も動かない**
  // （遊びのぶんだけ空振りする）。
  //
  // ⚠️ **既に盤が縮んでいるときは触らないこと** —— それはユーザーが自分で
  // 列を広げた状態なので、勝手に戻すと設定を奪う。
  const settleStudySide = () => {
    // ⚠️ **切り離しているあいだは触らない**（2026-09-08）。列が出ていないので
    // 詰める相手が居ない（畳んでいるときと同じ扱い）。
    // ⚠️ **幅を決めたあとは触らないこと**（2026-09-12）。ここは「盤が上限に
    // 張り付いたまま取れる最大の幅まで詰める」処理なので、**人が決めた幅を
    // その最大値へ押し戻してしまう**（狭めた直後に元へ戻る）。
    if (sideLocked) {
      return;
    }
    // ⚠️ **見るのは `bothDetached()`**（2026-09-12）。`sideDetached` は
    // **候補手の面だけ**を指すようになったので、ここで見ると
    // **手順だけ残っている列でバーが死ぬ**（掴んでも 1px も動かない。実際に踏んだ）。
    if (bothDetached() || studySideW === 0 || boardW() <= 0) {
      return; // 両方とも切り離し / 畳んでいる / タブが隠れている（測れない）
    }
    // ⚠️ **折り畳みのアニメーション中は測らない。** 盤の幅が動いている最中なので、
    // 二分探索が途中の値を掴んで**でたらめな幅で確定する**。終わったら呼び直す。
    if (panelStudy.classList.contains("is-animating")) {
      return;
    }
    // **詰め切ったとき（＝列を下限まで狭めたとき）の盤**が、この窓で取れる上限。
    // ⚠️ **0 で測らないこと** —— 窓が狭いと盤は横で決まるので、0 まで詰めた
    // 大きさを上限にすると**解析の列を潰し切るまで詰めてしまう**。
    rawSide(STUDY_SIDE_MIN);
    const cap = boardW();
    rawSide(studySideW);
    if (studySideW > STUDY_SIDE_MIN && boardW() < cap - 0.5) {
      return; // 盤は既に横で決まっている＝ユーザーが自分で列を広げた側
    }
    // 上限に張り付いている最大の幅を二分探索で求める。
    let lo = STUDY_SIDE_MIN;
    let hi = Math.max(studySideW, window.innerWidth);
    for (let i = 0; i < 20; i++) {
      const mid = (lo + hi) / 2;
      rawSide(mid);
      if (boardW() >= cap - 0.5) {
        lo = mid;
      } else {
        hi = mid;
      }
    }
    studySideW = Math.round(lo);
    studySideOpenW = studySideW;
    rawSide(studySideW);
  };

  // setStudySideW は幅を変える。**効果が無い方向へは動かさない。**
  const setStudySideW = (px: number) => {
    // ⚠️ **`bothDetached()` で見ること**（`settleStudySide` と同じ ⚠️）。
    // 列が 1 つでも残っているなら、幅は今までどおり変えられなければならない。
    if (bothDetached() || studySideW === 0 || boardW() <= 0) {
      return; // 両方とも切り離し / 畳んでいるあいだは幅を変えない（戻すのはトグルの仕事）
    }
    const next = Math.max(Math.round(px), STUDY_SIDE_MIN);
    if (next === studySideW) {
      return;
    }
    const before = studySideW;
    rawSide(next);
    const afterBoard = boardW();
    // ⚠️ **広げすぎて盤が潰れるのは止める**（下限は `STUDY_BOARD_MIN`）。
    //
    // ⚠️ **「詰めても盤が大きくならないなら詰めない」は外した**（2026-09-12）。
    // あれは列が余りをもらう作りが前提で、**盤が高さで頭打ちになった時点で
    // 列がそれ以上狭められない**という壁になっていた（広い窓で 600px 超）。
    // 今は `lockSide` で列の幅そのものを持つので、**盤が伸びない範囲でも
    // 狭められる**（余ったぶんは盤の側の余白になり、盤は中央へ寄る）。
    if (next > before && afterBoard < STUDY_BOARD_MIN) {
      rawSide(before);
      return;
    }
    studySideW = next;
    studySideOpenW = next;
    studySplit.setAttribute("aria-valuenow", String(next));
    // 盤の大きさが変わったので、重ねたグリッドとグラフを測り直す。
    studyBoardUI.relayout();
    evalGraphUI.relayout();
  };

  // ⚠️ **掴む前に遊びを消しておく**（見た目は動かない）。これが無いと、
  // 右へ引き始めても盤の右端が動かない区間ができる。
  studySplit.addEventListener("pointerdown", (e) => {
    e.preventDefault();
    cancelAnimation();
    // ⚠️ **順番を変えないこと。** 遊びを消してから固定する（`lockSide` の ⚠️）。
    settleStudySide();
    lockSide();
    studySplit.setPointerCapture(e.pointerId);
    studySplit.classList.add("is-dragging");
    const startX = e.clientX;
    // ⚠️ **起点は `lockSide` のあとに読むこと** —— あちらが実測で
    // `studySideW` を入れ直すので、先に読むと**1 回目のドラッグだけ
    // 見た目とずれた位置を掴む。**
    const startW = studySideW;
    // **右へ引く = 盤を広げる = 右の列を詰める。**
    const onMove = (ev: PointerEvent) => setStudySideW(startW - (ev.clientX - startX));
    const onUp = () => {
      studySplit.classList.remove("is-dragging");
      studySplit.removeEventListener("pointermove", onMove);
      studySplit.removeEventListener("pointerup", onUp);
      studySplit.removeEventListener("pointercancel", onUp);
    };
    studySplit.addEventListener("pointermove", onMove);
    studySplit.addEventListener("pointerup", onUp);
    studySplit.addEventListener("pointercancel", onUp);
  });

  studySplit.addEventListener("keydown", (e) => {
    const step = e.shiftKey ? 32 : 8;
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") {
      return;
    }
    e.preventDefault();
    // ⚠️ **ドラッグと同じ順で**（遊びを消してから固定する）。キーでも
    // 固定しないと、**右キーで狭めたつもりが 1px も動かない。**
    settleStudySide();
    lockSide();
    setStudySideW(studySideW + (e.key === "ArrowLeft" ? step : -step));
  });

  studySplit.setAttribute("aria-valuenow", String(studySideW));

  // ---- 解析の列の折り畳み（2026-08-13）------------------------------------
  //
  // ⚠️ **畳むと列ごと消える**ので、トグルは**縦のスプリットバーの上**に置く
  // （列の中に置いたら戻す手段が無くなる。評価値グラフで見出しの行を残して
  // あるのと同じ話）。
  //
  // ⚠️ **畳んだら盤を中央に寄せる。** 左上を起点にしているのは右に列があるから
  // で、列が無くなれば寄せる理由も無くなる（盤だけがある画面で左に寄っていると、
  // 右の余白が「何かあるはず」に見える）。
  //
  // ⚠️ **畳んでいるあいだはドラッグしない**（`studySideW === 0` で弾く）。
  // 幅は 0 と STUDY_SIDE_MIN のあいだが飛んでいるので、ドラッグの続きにならない。
  const studySplitToggle = root.querySelector<HTMLButtonElement>("#study-split-toggle")!;

  const applyStudySideCollapsed = () => {
    const open = studySideW > 0;
    panelStudy.classList.toggle("is-side-collapsed", !open);
    // ⚠️ **`.study-side` の `hidden` は触らないこと** —— あちらは
    // 「局面があるか」を表しており（`showStudy`）、持ち主が 2 つになると
    // どちらが今の話か分からなくなる。畳むほうは class で消す。
    studySplitToggle.innerHTML = iconMarkup(open ? FiChevronRight : FiChevronLeft);
    studySplitToggle.setAttribute("aria-expanded", String(open));
    studySplitToggle.title = open
      ? "解析の列を畳みます（盤が中央に来ます）"
      : "解析の列を開きます";
    studySplit.setAttribute("aria-valuenow", String(studySideW));
    // 畳むあいだは幅の予約も外す（窓が狭いときに盤が取れる幅が増える）。
    rawSide(open ? studySideW : 0);
    studyBoardUI.relayout();
    evalGraphUI.relayout();
  };

  // ---- 折り畳みのアニメーション（2026-08-13）------------------------------
  //
  // ⚠️ **畳む/開くときだけ付ける。** 常時付けると、スプリットバーの**ドラッグが
  // 0.3s 遅れて追ってくる**（掴んでいる位置と盤がずれる）。
  //
  // ⚠️ **後始末（遊びの取り直しと測り直し）は終わってから。** 途中で測ると
  // 動いている最中の値を掴む。
  const ANIM_MS = 300;
  let animTimer = 0;
  const endAnimation = () => {
    window.clearTimeout(animTimer);
    animTimer = 0;
    panelStudy.classList.remove("is-animating");
    // 起点として入れた inline の max-width を外す（畳んだ側は CSS が 0 を持つ）。
    studySide.style.maxWidth = "";
    settleStudySide();
    studyBoardUI.relayout();
    evalGraphUI.relayout();
  };
  const beginAnimation = () => {
    panelStudy.classList.add("is-animating");
    window.clearTimeout(animTimer);
    animTimer = window.setTimeout(endAnimation, ANIM_MS + 40);
  };
  // ⚠️ **掴んだら即座に終わらせる。** 畳んだ直後にバーを掴むと、残りの 0.3s は
  // トランジションが効いたままで**ドラッグが遅れて追ってくる**。
  const cancelAnimation = () => {
    if (animTimer !== 0) {
      endAnimation();
    }
  };

  studySplitToggle.addEventListener("click", () => {
    const opening = studySideW === 0;
    studySideW = opening ? studySideOpenW : 0;
    if (opening) {
      // ⚠️ **開く幅は「開いてみないと分からない」**（列は余りをもらうので）。
      // ① いったん最終状態にして測り、② 見た目だけ畳んだ状態へ戻して起点にし、
      // ③ トランジションを入れてから最終状態へ、という順で動かす。
      // **①〜②は同じタスクの中なので、途中の状態は描かれない。**
      applyStudySideCollapsed();
      const target = studySide.getBoundingClientRect().width;
      panelStudy.classList.add("is-side-collapsed");
      studySide.style.maxWidth = "0px";
      void studySide.offsetWidth; // ここまでをレイアウトに反映させる
      beginAnimation();
      panelStudy.classList.remove("is-side-collapsed");
      studySide.style.maxWidth = `${target}px`;
    } else {
      // 畳むほうは起点が今の幅そのもの。
      studySide.style.maxWidth = `${studySide.getBoundingClientRect().width}px`;
      void studySide.offsetWidth;
      beginAnimation();
      applyStudySideCollapsed();
      studySide.style.maxWidth = "0px";
    }
  });
  // ⚠️ **押してもドラッグが始まらないようにする。** バーの上に載っているので、
  // 止めないと「掴んだ」と解釈されて、離すまで幅が動き続ける。
  studySplitToggle.addEventListener("pointerdown", (e) => e.stopPropagation());

  applyStudySideCollapsed();

  // showPanes は局面の有無に合わせて、右の列・縦のバー・評価値グラフの行を出し入れする
  // （`showStudy` から）。⚠️ **切り離しの出し入れと同じ式をここ 1 か所に持つ**（`applyPanes` / `applyGraphDetached`）。
  const showPanes = (loaded: boolean) => {
    // 側の列と、その境目のバー。
    // ⚠️ **切り離しているあいだは出さないこと**（2026-09-08）。別ウィンドウに
    // 出ているので、**同じ値を 2 か所に描くことになる**。
    // ⚠️ **消えるのは「両方とも切り離したとき」だけ**（2026-09-12）。片方だけなら
    // 列は残り、**残った面がその幅を全部もらう**（`sidePane.setParts`）。
    studySide.hidden = bothDetached() || !loaded;
    // ⚠️ **縦のスプリットバーも局面があるときだけ出す。** 局面が無いときは
    // 分ける相手（解析の列）が出ていないので、バーだけが宙に浮く。
    studySplit.hidden = bothDetached() || !loaded;
    // ⚠️ **評価値グラフは切り離しているあいだ出さないこと**（2026-09-08）。
    // 別ウィンドウに出ているので、**同じ値を 2 か所に描くことになる**。
    evalGraphRow.hidden = graphDetached === true || !loaded;
  };

  return {
    settle: settleStudySide,
    showPanes,
    sideDetached: () => sideDetached === true,
    applyGraphDetached,
    applySideDetached,
    applyMovesDetached,
  };
}
