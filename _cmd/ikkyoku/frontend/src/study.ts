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
import { openPopup, type PopupHandle } from "./popup";

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
  // setFlip は視点（手前が先手 / 手前が後手）を切り替える。
  //
  // ⚠️ **表示だけの反転。局面には一切効かない。** `StudyService` を呼ばないこと ——
  // 盤を裏から見ているだけなので、SFEN も手番も指す手（USI）も変わらない。
  // ⚠️ **見え方とモデルが反対になる**ので、重ねるグリッドは
  // 「見た目の位置 → 局面のマス」を読み替える。
  setFlip(flip: boolean): void;
  // foldForkAt は「その手が分かれ道の 1 本なら、分かれた手をまとめて畳む」。
  //
  // **読み筋を足した直後に呼ぶ**（`AddLine` が返した最初の節点）。⚠️ 読み筋は
  // 15 手ぶら下がることがあるので、畳まないと**もう 1 本の候補が画面の外**に出て、
  // **その手で何を指したのかを見比べられない**（分岐を見る意味が薄れる）。
  //
  // ⚠️ **分かれていない（1 本しかない）ときは畳まないこと** —— 足したばかりの
  // 読み筋がいきなり消えると、何が起きたのか分からない。
  foldForkAt(id: number): void;
  // ⚠️ **「1手戻す」は無くなった**（2026-08-13。分岐を入れる前段）。手順を短く
  // するのは**手順リストの右クリック**だけで、消える範囲は「押した手とその先」。
  // `Undo` は「今どこを見ているか」に依存していて、戻って見ている最中に押すと
  // **何が消えるのか画面から読めなかった。**
  // ⚠️ **USI の手を直接指す入口は持たない**（2026-08-12 に外した）。
  // 解析結果の候補手を押して指せるようにしていたが、あれは**エンジンが読んだ枝**で
  // あって本譜ではないので、押しただけで手順が伸びる場所にしない
  // （`TODO.md` の「本譜のロック」。戻すなら先に本譜と枝の区別を決めること）。
  // 今この盤で手が伸びるのは**駒をクリックして動かしたとき**だけ。
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
  // engineOf は**その手を挙げたエンジン**の見た目（`Node.Sources` の 1 件）。
  //
  // **誰が言った手なのかを手の後ろに色の丸で出す**（2026-08-14）。枝は
  // 「エンジンがそう読んだ」だけの手なので、**本譜と同じ見た目で並ぶと
  // どれが誰の読み筋か分からない。**
  //
  // ⚠️ **色をここで決めないこと**（評価値グラフの折れ線と**同じ色**でなければ
  // 意味が無い）。解決は `mainscreen.ts` の 1 か所。
  engineOf(id: string): { color: string; label: string };
}

export function mountStudyBoard(opts: StudyBoardOptions): StudyBoardHandle {
  const { stage, handSlots, movesPanel, onState, onError, engineOf } = opts;

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

  // 視点（表示だけの反転）。**局面には効かない。**
  //
  // ⚠️ グリッドの DOM は**見た目の順**に並んでいるので、`dataset` に入れる
  // rank/file のほうを局面の座標へ読み替える（`editor.ts` と同じ考え方）。
  // こうしておくと、掴む・光らせる・指すの処理は視点を一切知らずに済む
  // （合法手 `legal.Move` の座標は当然モデル側なので、そのまま突き合わせられる）。
  let flipped = false;
  const applyFlip = () => {
    for (let i = 0; i < cells.length; i++) {
      const vr = Math.floor(i / 9);
      const vf = i % 9;
      cells[i].dataset.rank = String(flipped ? 8 - vr : vr);
      cells[i].dataset.file = String(flipped ? 8 - vf : vf);
    }
  };

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
  // 畳んである手（**画面だけの状態**。局面も手順も変わらないので Go には持たせない）。
  //
  // ⚠️ **根が入れ替わったら捨てること。** 節点の id は木ごとに 1 から振り直すので、
  // 前の対局の畳み方が**関係の無い手を隠す**。
  const collapsed = new Set<number>();
  let lastRoot = "";
  // 最後に描いたときの「今見ている手」。**道筋を開くのは、ここが変わったときだけ。**
  let lastAt = -1;

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

  // 手順（棋譜）。**盤の右に縦のツリービューで積む。クリックでその局面へ戻れる。**
  //
  // ⚠️ **一直線ではない**（2026-08-13）。枝は 1 段字下げして、**分かれた手の
  // すぐ下**に出す（Go 側の Nodes() が既にその順で返す。**フロントで並べ替えない**）。
  //
  // ⚠️ **戻っても手順は消さない**（進め直せる）。消えるのは**右クリックで
  // 消したとき**だけ（別の手を指しても、もう消えない —— 枝が生える）。
  //
  // ⚠️ **左クリック＝戻る / 右クリック＝その手以下を消す**（2026-08-13）。
  // 左を消す操作にしない —— **戻って見る**のは手順リストの主な用途なので、
  // それを潰すと評価値グラフからしか戻れなくなる。
  const renderMoves = () => {
    movesPanel.replaceChildren();
    if (!state?.loaded) {
      return;
    }
    const at = state.currentId ?? 0;
    const rows = state.nodes ?? [];

    // ---- 折り畳み（2026-08-13）-------------------------------------------
    //
    // ⚠️ **畳むのは「その手にぶら下がった枝」だけ。** 続き（同じ深さで伸びる
    // 手順）は畳まない —— あれはその手の枝ではなく**同じ 1 本の続き**なので、
    // 畳むと本譜が丸ごと消える。
    //
    // 判定は**深さ**で行う: 親より深い子＝枝の入口。
    const depthOf = new Map<number, number>([[0, 0]]);
    const parentOf = new Map<number, number>([[0, -1]]);
    for (const n of rows) {
      depthOf.set(n.id, n.depth);
      parentOf.set(n.id, n.parent);
    }
    // ⚠️ **見ている局面が変わったときだけ、その道筋を開く。**
    // 畳んだ中の手へ飛んだら（評価値グラフから、など）見えるようにするための処理。
    //
    // ⚠️ **描き直しのたびに開かないこと**（2026-08-13 に踏んだ）。**畳む操作自体が
    // 描き直し**なので、毎回開くと**今いる枝を含む手を畳めない**（押しても何も
    // 起きないように見え、「別の手を選んでからでないと畳めない」になる）。
    // **畳んだ結果、今見ている手が隠れるのは正しい**（盤には出ているし、開けば戻る）。
    if (at !== lastAt) {
      lastAt = at;
      for (let n = at; n > 0; n = parentOf.get(n) ?? -1) {
        const up = parentOf.get(n) ?? -1;
        if (up >= 0 && (depthOf.get(n) ?? 0) > (depthOf.get(up) ?? 0)) {
          collapsed.delete(up);
        }
      }
    }
    const hidden = (id: number): boolean => {
      for (let n = id; n > 0; ) {
        const up = parentOf.get(n) ?? -1;
        if (up < 0) {
          return false;
        }
        // 畳んである手の「枝の入口」を通ったなら隠れている。
        if (collapsed.has(up) && (depthOf.get(n) ?? 0) > (depthOf.get(up) ?? 0)) {
          return true;
        }
        n = up;
      }
      return false;
    };
    // 枝を持つ手にだけ +/- を出す（続きしか無い手には出さない）。
    const hasBranch = (id: number) =>
      rows.some((n) => n.parent === id && n.depth > (depthOf.get(id) ?? 0));
    // ⚠️ **変化の始まりには区切りを出す。** 同じ深さの変化が続けて並ぶと
    // （９七角の続きが 2 本、など）、**どこで次の変化に変わったのか**が
    // 手数の戻り方でしか分からない。**本譜側には出さない**（線が増えるだけ）。
    const isFork = (n: { parent: number; main: boolean }) =>
      !n.main && rows.filter((x) => x.parent === n.parent).length > 1;
    // 手数と手を別の要素にして、**手数の桁を揃える**（縦に並ぶので、揃っていないと
    // 何手目を見ているのかが読み取りにくい）。
    const chip = (
      num: string, label: string, id: number, title: string,
      o?: {
        depth?: number; main?: boolean; parent?: number; fork?: boolean;
        sources?: string[]; hand?: boolean;
      },
    ) => {
      // ⚠️ **行は chip とトグルの 2 つ**（ボタンの中にボタンは置けない）。
      // 字下げは**行のほう**に付ける（chip に付けると、トグルだけ左に残る）。
      const row = document.createElement("div");
      row.className = o?.fork ? "move-row is-fork" : "move-row";
      row.style.setProperty("--move-depth", String(o?.depth ?? 0));

      const toggle = document.createElement("button");
      toggle.type = "button";
      toggle.className = "move-toggle";
      if (hasBranch(id)) {
        const shut = collapsed.has(id);
        toggle.textContent = shut ? "+" : "−";
        toggle.title = shut ? "枝を開く" : "枝を畳む";
        toggle.setAttribute("aria-expanded", String(!shut));
        toggle.addEventListener("click", () => {
          if (collapsed.has(id)) {
            collapsed.delete(id);
          } else {
            collapsed.add(id);
          }
          // ⚠️ **Go は呼ばない**（畳むのは見た目だけ。局面も手順も変わらない）。
          renderMoves();
        });
      } else {
        // ⚠️ **枝が無くても場所は空けておくこと。** 出したり消したりすると、
        // 手の頭が行ごとに左右へずれて読みにくい。
        toggle.classList.add("is-empty");
        toggle.tabIndex = -1;
        toggle.setAttribute("aria-hidden", "true");
      }
      row.appendChild(toggle);

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
      // 自分で盤に指した手（2026-08-14）。**黒い丸で、目立たなくてよい** ——
      // 読みたいのは「エンジンが挙げた手（色）」のほうで、これは
      // **後から「これは自分で入れた手だ」と分かればよい**という程度の印。
      // ⚠️ **棋譜（KIF / URL）の手には出ない**（Go 側が印を付けない）。
      // あちらは**実際に現れた指し手**で、自分で試しに指した手とは別物。
      if (o?.hand) {
        const dot = document.createElement("span");
        dot.className = "move-source is-hand";
        dot.title = "自分で指した手";
        b.appendChild(dot);
      }
      // ⚠️ **手の後ろに出す**（前に置くと、手数と手のあいだに割り込んで
      // **縦に並んだ手の頭が揃わなくなる**）。
      // ⚠️ **複数出しうる** —— 同じ手を 2 つのエンジンが挙げるのは普通で、
      // **その一致が一番読みたいもの**。1 つに丸めないこと。
      // ⚠️ **自分で指した手にエンジンの丸が並ぶこともある**（エンジンが挙げた手を
      // 自分でも指したとき）。**どちらも本当**なので、片方に丸めないこと。
      for (const src of o?.sources ?? []) {
        const e = engineOf(src);
        const dot = document.createElement("span");
        dot.className = "move-source";
        dot.style.background = e.color;
        // ⚠️ **色だけにしないこと**（色が読めなくても誰の手かは分かるように）。
        dot.title = `${e.label} が挙げた手`;
        b.appendChild(dot);
      }
      b.classList.toggle("is-current", id === at);
      // ⚠️ **枝は見た目で分かるようにする**（字下げ + 色）。同じ手数の手が
      // 何行も並ぶので、**どれが本譜か**が分からないと読めない。
      b.classList.toggle("is-branch", o?.main === false);
      // ⚠️ **id と親は消す範囲を出すのに要る**（`.is-doomed` を付ける相手を辿る鍵）。
      // ⚠️ **GoTo に渡すのも id**（手数ではない。枝があると同じ手数が何個もある）。
      b.dataset.id = String(id);
      b.dataset.parent = String(o?.parent ?? -1);
      b.addEventListener("click", () => void run(() => StudyService.GoTo(id)));
      row.appendChild(b);
      return row;
    };
    movesPanel.appendChild(chip("", "開始局面", 0, "採ったときの局面に戻ります"));
    for (const m of rows) {
      // 畳んである枝の中は出さない（**畳むのは見た目だけ**なので、手順は生きている）。
      if (hidden(m.id)) {
        continue;
      }
      movesPanel.appendChild(
        // ⚠️ **出す数字と GoTo に渡す値は別物。** m.number は根からの手数で、
        // 画面に出すのは棋譜の手数（＝根の手数を足したもの）。撮った 41 手目の
        // 局面を根にすると、この 2 つは 40 ずれる。**渡すのは m.id。**
        chip(String((state.first ?? 0) + m.number), m.text || m.usi, m.id,
          `${m.usi} までの局面に戻ります` +
            (m.main ? "" : "（枝）") + "（右クリックでこの手から下を消します）",
          {
            depth: m.depth, main: m.main, parent: m.parent, fork: isFork(m),
            sources: m.sources ?? [], hand: m.hand ?? false,
          }),
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
  let ask: PopupHandle | null = null;

  const closeAsk = () => {
    ask?.close();
    ask = null;
  };

  // askPromote は押した場所で「成る / 成らず」を聞く。
  // 選ばずに閉じたら onPick は呼ばれない（＝指さない）。
  const askPromote = (x: number, y: number, onPick: (promote: boolean) => void) => {
    closeAsk();
    ask = openPopup(x, y, {
      label: "成るかどうか",
      // **「成る」を先に置いて初期フォーカスにする**（成るほうが圧倒的に多い）。
      items: [
        { label: "成る", kind: "primary", onPick: () => onPick(true) },
        { label: "成らず", onPick: () => onPick(false) },
      ],
    });
  };

  // ---- 手順を消す（手順リストの右クリック）--------------------------------
  //
  // ⚠️ **「1手戻す」の代わり**（2026-08-13。分岐を入れる前段）。消えるのは
  // **押した手とその先**で、`Undo` と違って「今どこを見ているか」に依存しない。
  //
  // ⚠️ **右クリックでいきなり消す UI を出さないこと**（2026-08-14）。
  // まず**メニュー**（「以降の手を削除」）を出し、**押してから**「本当に消すか」を
  // 聞く。右クリックは誤爆しやすいのに、いきなり赤いボタンが指の下に出ると
  // **その勢いで押せてしまう**。候補手の右クリック（「手順を追加」）とも形が揃う。
  //
  // ⚠️ **聞くほうを省かないこと。** 消えるのは**1 手ではなく、そこから下の全部**
  // （解析結果も一緒に消える）。**メニューは「何をするか」、確認は「何が消えるか」**で
  // 役割が違う。
  // ⚠️ **聞いているあいだ、消える範囲を赤く光らせる**（`.is-doomed`）——
  // 「その手以下が消える」は文字で言うより見せたほうが早い。
  // ⚠️ **光らせるのは確認のときだけ**（メニューの段では光らせない）。
  // 赤は「これから消える」の合図なので、まだ選んでいない段で出すと意味が薄れる。

  // markDoomed はその手とその子孫を「消える」見た目にする（0 で全部戻す）。
  //
  // ⚠️ **子孫は親を辿って決めること。** 字下げ（depth）では見分けられない ——
  // **本譜の続きは親と同じ深さのまま**だからで、深さで切ると本譜側だけ
  // 消えないように見える。
  const markDoomed = (from: number) => {
    const chips = [...movesPanel.querySelectorAll<HTMLElement>(".move-chip")];
    const parent = new Map<number, number>();
    for (const el of chips) {
      parent.set(Number(el.dataset.id ?? "0"), Number(el.dataset.parent ?? "-1"));
    }
    const doomed = (id: number): boolean => {
      for (let n = id; n > 0; n = parent.get(n) ?? -1) {
        if (n === from) {
          return true;
        }
      }
      return false;
    };
    for (const el of chips) {
      el.classList.toggle(
        "is-doomed", from > 0 && doomed(Number(el.dataset.id ?? "0")));
    }
  };

  // askDrop は押した場所で「ここから消す」を聞く。
  const askDrop = (x: number, y: number, id: number, label: string, count: number) => {
    closeAsk();
    markDoomed(id);
    ask = openPopup(x, y, {
      label: "手順を消す",
      // ⚠️ **初期フォーカスは「やめる」**（成る/成らずと逆）。こちらは消す操作なので、
      // Enter の連打で消えてしまわないほうを既定にする。
      focus: 1,
      items: [
        {
          // **何が何手消えるかを文言に出す**（押す前に読めること）。
          label: count > 1 ? `${label} から下を消す（${count}手）` : `${label} を消す`,
          kind: "danger",
          onPick: () => void run(() => StudyService.DropFrom(id)),
        },
        { label: "やめる", onPick: () => {} },
      ],
      // **消える範囲の色も一緒に落とす**（残ると、消していないのに消えた顔をする）。
      onClose: () => markDoomed(0),
    });
  };

  // askDropMenu は右クリックで最初に出すメニュー。
  //
  // ⚠️ **ここでは消さない**（押すと確認が出る）。⚠️ **`danger` にしないこと** ——
  // 赤くするのは「押したら消える」ボタンだけで、メニューの段で赤いと
  // **確認が出ることに気づかず身構える**。
  const askDropMenu = (x: number, y: number, id: number, label: string, count: number) => {
    closeAsk();
    ask = openPopup(x, y, {
      label: "手順",
      items: [{
        label: "以降の手を削除",
        onPick: () => askDrop(x, y, id, label, count),
      }],
    });
  };

  // 手順リストの右クリック。**チップの上でだけ受ける**（列の余白では既定のまま）。
  movesPanel.addEventListener("contextmenu", (e) => {
    const chip = (e.target as HTMLElement | null)?.closest<HTMLElement>(".move-chip");
    if (!chip) {
      return;
    }
    // ⚠️ **webview の既定メニューを止める**（訂正タブの盤と同じ）。
    e.preventDefault();
    const id = Number(chip.dataset.id ?? "0");
    // 「開始局面」は手ではないので消せない（消したいなら 1 手目を押す）。
    if (id < 1 || !state?.loaded) {
      return;
    }
    // ⚠️ **消えるのは「その手 + 子孫」**（ぶら下がった枝も全部）。字下げでは
    // 数えられないので、親を辿って数える（markDoomed と同じ理由）。
    const parent = new Map<number, number>();
    for (const n of state.nodes ?? []) {
      parent.set(n.id, n.parent);
    }
    const count = (state.nodes ?? []).filter((n) => {
      for (let x = n.id; x > 0; x = parent.get(x) ?? -1) {
        if (x === id) {
          return true;
        }
      }
      return false;
    }).length;
    const label = chip.querySelector<HTMLElement>(".move-text")?.textContent ?? "この手";
    askDropMenu(e.clientX, e.clientY, id, label, count);
  });

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
      // ⚠️ **根が変わったら畳み方を捨てる**（id が振り直されるため）。
      if ((next.rootSfen ?? "") !== lastRoot) {
        lastRoot = next.rootSfen ?? "";
        collapsed.clear();
        // ⚠️ **道筋を開く判定もやり直す**（別の木なので、前の位置と比べる意味が無い）。
        lastAt = -1;
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
    foldForkAt(id: number) {
      if (!state?.loaded || id <= 0) {
        return;
      }
      const rows = state.nodes ?? [];
      const me = rows.find((n) => n.id === id);
      if (!me) {
        return;
      }
      const upDepth = me.parent === 0 ? 0 : rows.find((n) => n.id === me.parent)?.depth ?? 0;
      // 兄弟＝同じ親から**下がって**出ている手（本譜の続きは同じ深さなので入らない）。
      const sibs = rows.filter((n) => n.parent === me.parent && n.depth > upDepth);
      if (sibs.length < 2) {
        return;
      }
      for (const sib of sibs) {
        // 続きを持つものだけ畳む（1 手だけの枝は畳んでも何も変わらない）。
        if (rows.some((n) => n.parent === sib.id)) {
          collapsed.add(sib.id);
        }
      }
      renderMoves();
    },
    setFlip(next: boolean) {
      if (next === flipped) {
        return;
      }
      flipped = next;
      // ⚠️ **掴んでいる駒は捨てる。** 光っていた「動かせる位置」は反転前の
      // 見た目に付いていたので、そのまま残すと**押した場所と指す手が食い違う**。
      pick = null;
      applyFlip();
      layoutGrid();
      paint();
    },
  };
}
