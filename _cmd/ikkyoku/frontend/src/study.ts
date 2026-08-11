// 解析タブの盤の操作（Phase 5「手を進める UI」）。
//
// ⚠️ **訂正タブ（editor.ts）とは別物。混ぜないこと。**
//
//                訂正タブ                      解析タブ（ここ）
//   何をする      認識の誤りを直す               手を選んで局面を進める
//   動かせる先    どこでも（合法性を問わない）    **合法手だけ**
//   手番          人が決める（未決でよい）       **1 手ごとに入れ替わる**
//   駒台          先後を人が割り振る             **取った駒が自動で載る**
//   操作          ドラッグ＆ドロップ             **クリック 2 回**
//   Go 側         PositionService                StudyService
//
// **操作はクリック 2 回。** 駒をクリックすると**動かせる位置が光り**、そこを
// クリックすると動く。ドラッグにしていないのは、こちらは「どこへ動かせるのか」を
// 見せることが目的だから（訂正はどこへでも置けるので見せるものが無い）。
//
// **合法手の一覧は Go 側が出す**（`StudyState.legal`。中身は `ikkyoku/legal` ＝
// engine の合法手生成）。⚠️ **フロントで将棋のルールを書かないこと。**
//
// **状態は Go 側（StudyService）が持つ。** ここは操作を送って、返ってきた
// StudyState をそのまま描くだけ（訂正タブと同じ方針）。フロントに持つのは
// 「今どの駒を掴んでいるか」という**画面だけの状態**に限る。
import { StudyService } from "../bindings/ikkyoku-app";
import type { StudyState } from "../bindings/ikkyoku-app/models";
import type { Move as LegalMove } from "../bindings/github.com/ShinteLab/ikkyoku/legal/models";

const RANK_KANJI = ["一", "二", "三", "四", "五", "六", "七", "八", "九"];
const cellLabel = (rank: number, file: number) => `${9 - file}${RANK_KANJI[rank] ?? "?"}`;

// 選んでいる駒。**画面だけの状態**（局面は Go 側が持つ）。
type Pick =
  // 盤の駒
  | { from: "cell"; rank: number; file: number }
  // 駒台の駒（打つ）
  | { from: "hand"; piece: number };

export interface StudyBoardHandle {
  // render は Go から返ってきた局面を描く。**選択は毎回解除する**
  // （局面が変わった以上、掴んでいた駒はもう同じ駒ではない）。
  render(state: StudyState | null): void;
  // relayout は盤に重ねるグリッドを置き直す。
  //
  // ⚠️ **解析タブを開いた瞬間に呼ぶこと。** 位置は `getScreenCTM()` で測るが、
  // **`display: none` の中では CTM が取れない**。訂正タブで何度も踏んだのと
  // 同じ落とし穴で、測り直さないと**1 マスずれたところを指す**。
  relayout(): void;
  // undo は 1 手戻す（**手順からも消す**。「指し間違えた」の取り消し）。
  // 戻って見るだけなら手順のチップを押す（そちらは手順を消さない）。
  undo(): void;
}

export interface StudyBoardOptions {
  // stage は <shogi-board> を包む要素。ここにグリッドを重ねる。
  stage: HTMLElement;
  // handSlots は駒台の置き場。**ここの駒をクリックすると打てる位置が光る。**
  handSlots: { black: HTMLElement; white: HTMLElement };
  // movesPanel は手順（棋譜）を並べる場所。
  movesPanel: HTMLElement;
  // onState は Go 側から返ってきた局面。盤・SFEN・駒台の表示は呼び出し側が持つ。
  onState(state: StudyState): void;
  // onError は手が通らなかったときの理由。
  onError(message: string): void;
}

export function mountStudyBoard(opts: StudyBoardOptions): StudyBoardHandle {
  const { stage, handSlots, movesPanel, onState, onError } = opts;

  // ---- 盤に重ねるグリッド -------------------------------------------------
  //
  // ⚠️ **訂正タブのグリッドを流用しない**（別のタブの別の盤なので、要素も別）。
  // 測り方だけは同じ（`getScreenCTM`）で、その理由も同じ ——
  // `<shogi-board>` の SVG は `preserveAspectRatio` が既定なので、箱の左端から
  // 測ると中央寄せの余白ぶんずれる。
  const grid = document.createElement("div");
  grid.className = "study-grid";
  const cells: HTMLDivElement[] = [];
  for (let rank = 0; rank < 9; rank++) {
    for (let file = 0; file < 9; file++) {
      const cell = document.createElement("div");
      cell.className = "study-cell";
      cell.dataset.rank = String(rank);
      cell.dataset.file = String(file);
      grid.appendChild(cell);
      cells.push(cell);
    }
  }
  stage.appendChild(grid);
  if (getComputedStyle(stage).position === "static") {
    stage.style.position = "relative";
  }

  // 盤の SVG 座標（core/web の shogi-board.js）。**向こうが変わったらここも直す。**
  const SVG_MARGIN = 28;
  const SVG_CELL = 56;

  const layoutGrid = () => {
    const svg = stage.querySelector("shogi-board")?.shadowRoot?.querySelector("svg");
    const ctm = svg?.getScreenCTM();
    if (!svg || !ctm) {
      grid.style.display = "none";
      return;
    }
    const at = (x: number, y: number) => {
      const p = svg.createSVGPoint();
      p.x = x;
      p.y = y;
      return p.matrixTransform(ctm);
    };
    const topLeft = at(SVG_MARGIN, SVG_MARGIN);
    const bottomRight = at(SVG_MARGIN + SVG_CELL * 9, SVG_MARGIN + SVG_CELL * 9);
    const base = (grid.offsetParent ?? stage).getBoundingClientRect();
    const w = bottomRight.x - topLeft.x;
    const h = bottomRight.y - topLeft.y;
    if (w <= 0 || h <= 0) {
      grid.style.display = "none";
      return;
    }
    grid.style.display = "";
    grid.style.left = `${topLeft.x - base.left}px`;
    grid.style.top = `${topLeft.y - base.top}px`;
    grid.style.width = `${w}px`;
    grid.style.height = `${h}px`;
  };

  const observer = new ResizeObserver(() => layoutGrid());
  observer.observe(stage);
  window.addEventListener("resize", layoutGrid);

  // ---- 状態 ---------------------------------------------------------------

  let state: StudyState | null = null;
  let pick: Pick | null = null;

  const legalMoves = (): LegalMove[] => state?.legal ?? [];

  // 掴んだ駒から指せる手。**光らせる先はこれ。**
  const movesFromPick = (): LegalMove[] => {
    // ⚠️ **一度 const に写す。** `pick` は書き換わる変数なので、コールバックの中では
    // 型の絞り込みが効かない（tsc が通らない）。
    const p = pick;
    if (!p) {
      return [];
    }
    if (p.from === "hand") {
      return legalMoves().filter((m) => m.drop === p.piece);
    }
    return legalMoves().filter(
      (m) => m.drop < 0 && m.fromRank === p.rank && m.fromFile === p.file,
    );
  };

  // そのマスから指せる手があるか（＝掴める駒か）。
  //
  // **盤の中身を見ないで合法手から判定する。** 手番側の駒しか合法手に出てこないので、
  // 「自分の駒か」の判定を別に書かなくてよい（**フロントで将棋を書かない**）。
  const canPickCell = (rank: number, file: number) =>
    legalMoves().some((m) => m.drop < 0 && m.fromRank === rank && m.fromFile === file);

  // ---- 描画 ---------------------------------------------------------------

  const paint = () => {
    const dests = movesFromPick();
    const loaded = !!state?.loaded;
    grid.classList.toggle("is-active", loaded);
    for (const el of cells) {
      const rank = Number(el.dataset.rank);
      const file = Number(el.dataset.file);
      const target = dests.some((m) => m.toRank === rank && m.toFile === file);
      const source = pick?.from === "cell" && pick.rank === rank && pick.file === file;
      el.classList.toggle("is-target", target);
      el.classList.toggle("is-source", !!source);
      el.classList.toggle("is-pickable", loaded && !pick && canPickCell(rank, file));
      el.title = cellLabel(rank, file);
    }
    // 駒台のほうも、掴んでいる駒が分かるようにする（盤と同じ見え方に揃える）。
    for (const slot of [handSlots.black, handSlots.white]) {
      for (const chip of slot.querySelectorAll<HTMLElement>(".stock-chip")) {
        const piece = Number(chip.dataset.piece);
        chip.classList.toggle("is-source", pick?.from === "hand" && pick.piece === piece);
        chip.classList.toggle(
          "is-pickable",
          loaded && legalMoves().some((m) => m.drop === piece),
        );
      }
    }
  };

  // 手順（棋譜）。**クリックでその局面へ戻れる。**
  //
  // ⚠️ **戻っても手順は消さない**（進め直せる）。消えるのは、戻った先で
  // 別の手を指したときだけ（Go 側の Study.Play が捨てる）。
  const renderMoves = () => {
    movesPanel.replaceChildren();
    if (!state?.loaded) {
      return;
    }
    const ply = state.ply ?? 0;
    const chip = (label: string, n: number, title: string) => {
      const b = document.createElement("button");
      b.type = "button";
      b.className = "move-chip";
      b.textContent = label;
      b.title = title;
      b.classList.toggle("is-current", n === ply);
      b.addEventListener("click", () => void run(() => StudyService.GoTo(n)));
      return b;
    };
    movesPanel.appendChild(chip("開始", 0, "採ったときの局面に戻ります"));
    for (const m of state.moves ?? []) {
      movesPanel.appendChild(
        chip(`${m.number}. ${m.text || m.usi}`, m.number, `${m.usi} までの局面に戻ります`),
      );
    }
    // 今見ている手が画面の外にあると、進めても手順が動いていないように見える。
    movesPanel.querySelector<HTMLElement>(".move-chip.is-current")?.scrollIntoView({
      block: "nearest",
      inline: "nearest",
    });
  };

  // ---- 操作 ---------------------------------------------------------------

  // run は Go を呼んで、返ってきた局面を描く。
  // **失敗しても状態は取り直して描く**（画面が古いまま残るほうが分かりにくい）。
  const run = async (op: () => Promise<StudyState>) => {
    try {
      show(await op());
    } catch (err) {
      try {
        show(await StudyService.State());
      } catch {
        /* 取れないなら描き直さない。理由だけ出す。 */
      }
      onError(String(err instanceof Error ? err.message : err));
    }
  };

  // play は移動先が決まったときに 1 手指す。
  //
  // ⚠️ **成りと不成の両方が指せるなら聞く。** どちらを選ぶかは人が決めることなので、
  // **片方に丸めないこと**（「成らず」を選べないと、実戦の手順が辿れなくなる）。
  const play = (dests: LegalMove[]) => {
    if (dests.length === 0) {
      return;
    }
    let move = dests[0].usi;
    const promote = dests.find((m) => m.promote);
    const stay = dests.find((m) => !m.promote);
    if (promote && stay) {
      move = window.confirm("成りますか？（キャンセルで不成）") ? promote.usi : stay.usi;
    }
    pick = null;
    void run(() => StudyService.Play(move));
  };

  grid.addEventListener("click", (e) => {
    if (!state?.loaded) {
      return;
    }
    const el = (e.target as HTMLElement)?.closest<HTMLElement>(".study-cell");
    if (!el) {
      return;
    }
    const rank = Number(el.dataset.rank);
    const file = Number(el.dataset.file);

    // 掴んでいるなら、まず「そこへ指せるか」を見る。
    const dests = movesFromPick().filter((m) => m.toRank === rank && m.toFile === file);
    if (dests.length > 0) {
      play(dests);
      return;
    }
    // 指せないところを押したら、掴み直し（掴めない駒なら解除）。
    pick = canPickCell(rank, file) ? { from: "cell", rank, file } : null;
    paint();
  });

  // 駒台の駒をクリックすると、**打てる位置が光る**。
  // ⚠️ **ここはイベント委譲**（駒台の中身は局面が変わるたびに作り直される）。
  for (const slot of [handSlots.black, handSlots.white]) {
    slot.addEventListener("click", (e) => {
      if (!state?.loaded) {
        return;
      }
      const chip = (e.target as HTMLElement)?.closest<HTMLElement>(".stock-chip");
      if (!chip) {
        return;
      }
      const piece = Number(chip.dataset.piece);
      // 同じ駒をもう一度押したら解除。打てない駒は掴まない。
      const same = pick?.from === "hand" && pick.piece === piece;
      const canDrop = legalMoves().some((m) => m.drop === piece);
      pick = same || !canDrop ? null : { from: "hand", piece };
      paint();
    });
  }

  // ---- 外向き -------------------------------------------------------------

  // show は Go から返った局面を呼び出し側にも渡して描く。
  const show = (next: StudyState) => {
    state = next;
    // **局面が変わったら選択は捨てる。** 掴んでいた駒はもう同じ駒ではない。
    pick = null;
    onState(next);
    renderMoves();
    // onState が盤の sfen 属性を書き換える（= SVG を描き直す）ので、
    // グリッドの位置合わせはそのあと。
    layoutGrid();
    paint();
  };

  return {
    render(next: StudyState | null) {
      if (!next) {
        state = null;
        pick = null;
        movesPanel.replaceChildren();
        paint();
        return;
      }
      state = next;
      pick = null;
      renderMoves();
      layoutGrid();
      paint();
    },
    relayout() {
      layoutGrid();
      paint();
    },
    undo() {
      // **判定は Go 側**（根から更に戻せないなら向こうがエラーを返す）。
      void run(() => StudyService.Undo());
    },
  };
}
