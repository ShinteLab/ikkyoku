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
  // play は USI の手をそのまま指す（解析結果の候補手を押したとき）。
  //
  // **盤の操作と同じ入口を通す** —— 手順の記録も表示の更新も 1 か所で済む。
  // 合法かどうかは Go 側が言うので、**呼ぶ前に確かめなくてよい**。
  play(usi: string): void;
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
    //
    // ⚠️ **押せない駒は「押せない」と分かるようにすること**（2026-08-12）。
    // 打ち先が無い駒（二歩になる歩・行き所のない香桂）は押しても何も光らないので、
    // **見た目が同じだと壊れているのか駄目な手なのか区別が付かない**。
    // ⚠️ **薄くするのは手番側の駒だけ。** 相手の駒が押せないのは手番から明らかで、
    // そこまで薄くすると「今どちらの番か」の手掛かりまで薄れる。
    for (const [black, slot] of [
      [true, handSlots.black],
      [false, handSlots.white],
    ] as const) {
      const mine = loaded && state?.turn === (black ? 1 : 2);
      for (const chip of slot.querySelectorAll<HTMLElement>(".stock-chip")) {
        const piece = Number(chip.dataset.piece);
        const canDrop = loaded && legalMoves().some((m) => m.drop === piece);
        chip.classList.toggle("is-source", pick?.from === "hand" && pick.piece === piece);
        chip.classList.toggle("is-pickable", canDrop);
        chip.classList.toggle("is-unpickable", !!mine && !canDrop);
        // ⚠️ **元の見出し（駒種と枚数）は dataset に持っておくこと。**
        // title に直接足すと、描き直すたびに理由が積み重なる。
        const base = chip.dataset.label ?? "";
        chip.title = canDrop || !loaded ? base : `${base}／${dropReason(!!mine)}`;
      }
    }
  };

  // dropReason は駒台の駒を打てない理由。**押したときにも同じ文言を出す**
  // （ツールチップだけだとホバーしないと読めない）。
  const dropReason = (mine: boolean) =>
    mine
      ? "打てる場所がありません（二歩・打ち歩詰め・行き所のない駒）"
      : `今は${state?.turnLabel ?? "相手の番"}です`;

  // 手順（棋譜）。**盤の右に縦のリストで積む。クリックでその局面へ戻れる。**
  //
  // ⚠️ **戻っても手順は消さない**（進め直せる）。消えるのは、戻った先で
  // 別の手を指したときだけ（Go 側の Study.Play が捨てる）。
  const renderMoves = () => {
    movesPanel.replaceChildren();
    if (!state?.loaded) {
      return;
    }
    const ply = state.ply ?? 0;
    // 手数と手を別の要素にして、**手数の桁を揃える**（縦に並ぶので、揃っていないと
    // 何手目を見ているのかが読み取りにくい）。
    const chip = (num: string, label: string, n: number, title: string) => {
      const b = document.createElement("button");
      b.type = "button";
      b.className = "move-chip";
      b.title = title;
      const i = document.createElement("span");
      i.className = "move-num";
      i.textContent = num;
      const t = document.createElement("span");
      t.className = "move-text";
      t.textContent = label;
      b.append(i, t);
      b.classList.toggle("is-current", n === ply);
      b.addEventListener("click", () => void run(() => StudyService.GoTo(n)));
      return b;
    };
    movesPanel.appendChild(chip("", "開始局面", 0, "採ったときの局面に戻ります"));
    for (const m of state.moves ?? []) {
      movesPanel.appendChild(
        // ⚠️ **出す数字と `GoTo` に渡す値は別物。** `m.number` は根からの手数
        // （`GoTo` の引数）で、画面に出すのは棋譜の手数（＝根の手数を足したもの）。
        // 撮った 41 手目の局面を根にすると、この 2 つは 40 ずれる。
        chip(String((state.first ?? 0) + m.number), m.text || m.usi, m.number,
          `${m.usi} までの局面に戻ります`),
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

  // ---- 成る / 成らず を聞くダイアログ -------------------------------------
  //
  // ⚠️ **`window.confirm` は使わない。** あれは画面の真ん中に出るうえ、
  // 「OK / キャンセル」という**この選択とは無関係な語**でしか聞けない
  // （「キャンセル＝不成」は読み取れない）。**指した場所で「成る / 成らず」を
  // そのまま聞く**ほうが、盤から目を離さずに選べる。
  //
  // ⚠️ **どちらも指せる手なので、片方に丸めないこと**（「成らず」を選べないと
  // 実戦の手順が辿れなくなる）。**閉じただけなら指さない**（誤操作の取り消し）。

  // ask は開いているダイアログ。**開いているあいだは盤の操作を止める。**
  let ask: { close: () => void } | null = null;

  const closeAsk = () => {
    ask?.close();
    ask = null;
  };

  // askPromote は押した場所で「成る / 成らず」を聞く。
  // 選ばずに閉じたら onPick は呼ばれない（＝指さない）。
  const askPromote = (x: number, y: number, onPick: (promote: boolean) => void) => {
    closeAsk();
    const box = document.createElement("div");
    box.className = "promote-ask";
    box.setAttribute("role", "dialog");
    box.setAttribute("aria-label", "成るかどうか");

    const button = (label: string, promote: boolean, primary: boolean) => {
      const b = document.createElement("button");
      b.type = "button";
      b.className = primary ? "promote-btn is-primary" : "promote-btn";
      b.textContent = label;
      b.addEventListener("click", () => {
        closeAsk();
        onPick(promote);
      });
      return b;
    };
    // **「成る」を先に置いて初期フォーカスにする**（成るほうが圧倒的に多い）。
    const yes = button("成る", true, true);
    box.append(yes, button("成らず", false, false));

    // ⚠️ **`position: fixed` で body に置く**（盤の箱に入れると、はみ出したぶんが
    // 切られる）。押した場所の右下に出し、画面から出るなら内側へ寄せる。
    document.body.appendChild(box);
    const r = box.getBoundingClientRect();
    const m = 8;
    box.style.left = `${Math.min(Math.max(x + 12, m), window.innerWidth - r.width - m)}px`;
    box.style.top = `${Math.min(Math.max(y + 12, m), window.innerHeight - r.height - m)}px`;
    yes.focus();

    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        closeAsk();
      }
    };
    const onOutside = (e: Event) => {
      if (!box.contains(e.target as Node | null)) {
        closeAsk();
      }
    };
    // ⚠️ **今の click が終わってから外側の監視を始める。** 同じフレームで付けると、
    // ダイアログを開いたこのクリックがそのまま「外側」として届いて即座に閉じる。
    const timer = window.setTimeout(() => {
      document.addEventListener("pointerdown", onOutside, true);
    }, 0);
    document.addEventListener("keydown", onKey, true);

    ask = {
      close: () => {
        window.clearTimeout(timer);
        document.removeEventListener("pointerdown", onOutside, true);
        document.removeEventListener("keydown", onKey, true);
        box.remove();
      },
    };
  };

  // play は移動先が決まったときに 1 手指す。
  //
  // at は押した場所（成る / 成らずを聞くダイアログをそこに出す）。
  const play = (dests: LegalMove[], at: { x: number; y: number }) => {
    if (dests.length === 0) {
      return;
    }
    const promote = dests.find((m) => m.promote);
    const stay = dests.find((m) => !m.promote);
    const go = (move: string) => {
      pick = null;
      void run(() => StudyService.Play(move));
    };
    if (promote && stay) {
      // ⚠️ **選ぶまで掴んだままにしておく**（光ったまま待つ）。ここで pick を
      // 落とすと、閉じただけのときに選択が消えて掴み直しになる。
      askPromote(at.x, at.y, (yes) => go(yes ? promote.usi : stay.usi));
      return;
    }
    go(dests[0].usi);
  };

  grid.addEventListener("click", (e) => {
    // ダイアログが開いているあいだは盤を触らせない（裏で別の手を指してしまう）。
    if (!state?.loaded || ask) {
      return;
    }
    const el = (e.target as HTMLElement)?.closest<HTMLElement>(".study-cell");
    if (!el) {
      return;
    }
    const rank = Number(el.dataset.rank);
    const file = Number(el.dataset.file);

    // ⚠️ **掴んでいる駒をもう一度押したら解除**（2026-08-12）。これが無いと、
    // 掴み直しとして同じマスを選び直すので**光ったまま消せない**
    // （駒台の駒は前から解除できていたので、盤だけ挙動が違っていた）。
    if (pick?.from === "cell" && pick.rank === rank && pick.file === file) {
      pick = null;
      onError("");
      paint();
      return;
    }

    // 掴んでいるなら、まず「そこへ指せるか」を見る。
    const dests = movesFromPick().filter((m) => m.toRank === rank && m.toFile === file);
    if (dests.length > 0) {
      play(dests, { x: e.clientX, y: e.clientY });
      return;
    }
    // 指せないところを押したら、掴み直し（掴めない駒なら解除）。
    pick = canPickCell(rank, file) ? { from: "cell", rank, file } : null;
    onError(""); // 前の操作の理由を残さない
    paint();
  });

  // 駒台の駒をクリックすると、**打てる位置が光る**。
  // ⚠️ **ここはイベント委譲**（駒台の中身は局面が変わるたびに作り直される）。
  for (const [black, slot] of [
    [true, handSlots.black],
    [false, handSlots.white],
  ] as const) {
    slot.addEventListener("click", (e) => {
      if (!state?.loaded || ask) {
        return;
      }
      const chip = (e.target as HTMLElement)?.closest<HTMLElement>(".stock-chip");
      if (!chip) {
        return;
      }
      const piece = Number(chip.dataset.piece);
      // 同じ駒をもう一度押したら解除。
      if (pick?.from === "hand" && pick.piece === piece) {
        pick = null;
        onError("");
        paint();
        return;
      }
      // ⚠️ **打てない駒は「なぜ押せないか」を出す**（2026-08-12）。黙って何も
      // 起きないと、壊れているのか駄目な手なのかが分からない（実際に分からなかった）。
      if (!legalMoves().some((m) => m.drop === piece)) {
        pick = null;
        paint();
        onError(dropReason(state?.turn === (black ? 1 : 2)));
        return;
      }
      pick = { from: "hand", piece };
      onError(""); // 前の操作の理由を残さない
      paint();
    });
  }

  // ---- 外向き -------------------------------------------------------------

  // show は Go から返った局面を呼び出し側にも渡して描く。
  const show = (next: StudyState) => {
    // 局面が変わったのだから、開いたままのダイアログはもう別の手の話。
    closeAsk();
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
      closeAsk();
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
    play(usi: string) {
      // 成る / 成らずは聞かない。**候補手はどちらかに決まった手**として届く
      // （エンジンが返す USI に "+" が入っているかどうかがその答え）。
      closeAsk();
      pick = null;
      void run(() => StudyService.Play(usi));
    },
  };
}
