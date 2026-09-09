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
import { StudyService } from "../bindings/github.com/ShinteLab/ikkyoku/app";
import type { StudyState } from "../bindings/github.com/ShinteLab/ikkyoku/app/models";
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
  // step は**今の手順を 1 手進む / 戻る**（十字キーの上下。2026-08-18）。
  // `-1` で戻り、`+1` で進む。
  //
  // ⚠️ **辿るのは「今の経路」**（`StudyState.line`）で、**画面に並んでいる行の
  // 順ではない**。リストには枝も一緒に並んでいるので、行の順で動かすと
  // **上下キーを押しただけで別の枝へ移る**（盤の局面が飛ぶ）。枝へ移るのは
  // 手順リストを押す操作。
  //
  // ⚠️ **端では何もしない**（開始局面より前・経路の終わりより先）。回り込ませると
  // 押しっぱなしで一周してしまい、どこに居るのか分からなくなる。
  //
  // ⚠️ **手順は 1 手も消さない**（`GoTo` と同じ。見る位置を動かすだけ）。
  step(delta: number): void;
  // showHint は候補手を盤の上に重ねて出す（**移動元 → 移動先の矢印**。打ちは
  // 打つ駒を打ち先に薄く置く）。`null` で消す。
  //
  // ⚠️ **見せるだけで、局面も手順も動かさない。** 解析の候補手は**エンジンが
  // 読んだ枝**であって本譜ではないので、押しただけで手順が伸びる場所にしない
  // （左クリックが「選ぶだけ」であることの延長で、選んだ手を盤に見せる）。
  //
  // ⚠️ **渡すのは USI**（`Line.moves[0]`）。座標は**今の局面の合法手**から
  // 引き当てるので、**呼び出し側で USI を解釈しないこと。**
  showHint(usi: string | null): void;
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
  // onState は Go 側から返ってきた局面。盤・SFEN・駒台の表示は呼び出し側が持つ。
  onState(state: StudyState): void;
  // onError は手が通らなかったときの理由。
  onError(message: string): void;
  // onMenu は**盤の右クリック**（駒を掴んでいないとき）。盤まわりの表示の
  // 入り切りをその場で聞くための口で、中身は呼び出し側が組む（`mainscreen.ts`）。
  //
  // ⚠️ **掴んでいるときは呼ばれない** —— 右クリックはまず「掴んだ駒を離す」
  // （2026-08-29 の約束）。指す先を探している最中にメニューが出ると、
  // **やめる操作がメニューを閉じる操作に化ける。**
  onMenu?(x: number, y: number): void;
}

export function mountStudyBoard(opts: StudyBoardOptions): StudyBoardHandle {
  const { stage, handSlots, onState, onError, onMenu } = opts;

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

  // ---- 候補手の重ね表示（2026-08-14）--------------------------------------
  //
  // **解析の候補手を押したら、その手を盤の上に矢印で出す**（移動元 → 移動先）。
  // 打ちは矢印にできない（移動元が盤の外）ので、**打つ駒を打ち先に薄く置く**。
  //
  // ⚠️ **局面は動かさない。** ここに出るのは**エンジンが読んだ枝**であって
  // 本譜ではないので、**見せるだけ**（辿るのは盤で駒を動かす操作／右クリックの
  // 「手順を追加」）。候補手の左クリックが「選ぶだけ」なのと同じ約束。
  //
  // ⚠️ **矢印は盤の上に置くので `pointer-events: none`。** 塞ぐと、
  // 光った移動先を押せなくなる（矢印の先はまさにそのマス）。
  const hintSvg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  hintSvg.setAttribute("class", "study-hint");
  // マス 1 つ = 1 の座標系にしておくと、盤の大きさが変わっても描き直さなくてよい
  // （伸び縮みは viewBox が吸収する）。**盤は正方形なので歪まない。**
  hintSvg.setAttribute("viewBox", "0 0 9 9");
  hintSvg.setAttribute("preserveAspectRatio", "none");
  stage.appendChild(hintSvg);

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
      hintSvg.style.display = "none";
      return;
    }
    grid.style.display = "";
    grid.style.left = `${topLeft.x - base.left}px`;
    grid.style.top = `${topLeft.y - base.top}px`;
    grid.style.width = `${w}px`;
    grid.style.height = `${h}px`;
    // ⚠️ **矢印はグリッドと同じ矩形に重ねる**（別々に測らないこと。ずれると
    // 矢印だけ 1 マス外れる）。中身は viewBox の座標なので描き直しは要らない。
    hintSvg.style.display = "";
    hintSvg.style.left = grid.style.left;
    hintSvg.style.top = grid.style.top;
    hintSvg.style.width = grid.style.width;
    hintSvg.style.height = grid.style.height;
  };

  const observer = new ResizeObserver(() => layoutGrid());
  observer.observe(stage);
  window.addEventListener("resize", layoutGrid);

  // ---- 状態 ---------------------------------------------------------------

  let state: StudyState | null = null;
  let pick: Pick | null = null;
  const legalMoves = (): LegalMove[] => state?.legal ?? [];

  // 重ね表示している候補手（USI）。**画面だけの状態**（局面も手順も動かない）。
  let hint: string | null = null;

  // drawHint は候補手を盤の上に描く。
  //
  // ⚠️ **USI をここで解釈しないこと。** 座標の読み替えは Go 側（`ikkyoku/legal`）の
  // 1 か所に閉じてあり（`core/usi` の内部 x は筋番号ではない、という落とし穴がある）、
  // **ここで同じ変換を書くと 3 か所目になる** —— 間違えても「矢印が 1 マスずれる」
  // という、画面を見ても正しいのか分からない壊れ方をする。
  // **今の局面の合法手（`state.legal`）から同じ USI を引く**のが正しい引き当て方で、
  // 候補手の 1 手目は必ずその中にある（無ければ描かない）。
  const drawHint = () => {
    hintSvg.replaceChildren();
    const usi = hint;
    if (!usi || !state?.loaded) {
      return;
    }
    const m = legalMoves().find((x) => x.usi === usi);
    if (!m) {
      return;
    }
    // 局面の座標 → 見た目の座標（視点の反転）。**読み替えはここだけ**
    // （`applyFlip` と同じ考え方で、他の場所に「反転しているなら…」を散らさない）。
    const vx = (file: number) => (flipped ? 8 - file : file) + 0.5;
    const vy = (rank: number) => (flipped ? 8 - rank : rank) + 0.5;
    const ns = "http://www.w3.org/2000/svg";
    const add = <K extends keyof SVGElementTagNameMap>(
      name: K, attrs: Record<string, string | number>,
    ): SVGElementTagNameMap[K] => {
      const el = document.createElementNS(ns, name);
      for (const [k, v] of Object.entries(attrs)) {
        el.setAttribute(k, String(v));
      }
      hintSvg.appendChild(el);
      return el;
    };

    const tx = vx(m.toFile);
    const ty = vy(m.toRank);

    if (m.drop >= 0) {
      // 打ちは矢印にできない（移動元が盤の外）ので、**打つ駒を打ち先に薄く置く**。
      //
      // ⚠️ **駒の文字は USI からそのまま取る**（"P*5e" の "P"）。駒台の駒と同じ
      // ShogiSFEN フォントで描くので、**盤に並んでいる駒と同じ字**になる。
      // ⚠️ **回すのは「奥の側」の駒**（後手ではない）。視点を反転すると入れ替わる
      // ——駒台の駒（`.stock-chip`）と同じ規則。
      const black = state.turn === 1;
      const letter = black ? usi[0].toUpperCase() : usi[0].toLowerCase();
      add("rect", {
        class: "study-hint-drop",
        x: tx - 0.44, y: ty - 0.44, width: 0.88, height: 0.88, rx: 0.08,
      });
      // ⚠️ **大きさは盤の駒に揃える**（core/web の FONT_SIZE 44 / CELL 56）。
      // **向こうが変わったらここも直す**（グリッドの MARGIN / CELL と同じ約束）。
      const t = add("text", {
        class: "study-hint-piece",
        x: tx, y: ty, "text-anchor": "middle", "dominant-baseline": "central",
        "font-size": 44 / 56,
      });
      if (black === flipped) {
        t.setAttribute("transform", `rotate(180 ${tx} ${ty})`);
      }
      t.textContent = letter;
      return;
    }

    const fx = vx(m.fromFile);
    const fy = vy(m.fromRank);
    // 移動元は枠で囲む（「この駒が動く」）。矢印の根本だけだと、どの駒の話か
    // 分かりにくい局面がある（駒が密集しているところ）。
    add("rect", {
      class: "study-hint-from",
      x: fx - 0.44, y: fy - 0.44, width: 0.88, height: 0.88, rx: 0.08,
    });

    // 矢印。**根本と先端をマスの内側で少し詰める**（1 マス動く手でも、
    // 矢印が駒を覆い隠さないように）。
    const dx = tx - fx;
    const dy = ty - fy;
    const len = Math.hypot(dx, dy) || 1;
    const ux = dx / len;
    const uy = dy / len;
    const head = 0.3;
    const x0 = fx + ux * 0.26;
    const y0 = fy + uy * 0.26;
    const x1 = tx - ux * 0.2;
    const y1 = ty - uy * 0.2;
    add("line", {
      class: "study-hint-line",
      x1: x0, y1: y0, x2: x1 - ux * head * 0.8, y2: y1 - uy * head * 0.8,
    });
    // 先端（三角）。
    const px = -uy;
    const py = ux;
    const bx = x1 - ux * head;
    const by = y1 - uy * head;
    add("polygon", {
      class: "study-hint-head",
      points: [
        `${x1},${y1}`,
        `${bx + px * 0.22},${by + py * 0.22}`,
        `${bx - px * 0.22},${by - py * 0.22}`,
      ].join(" "),
    });
    // 成る手は先端に「成」を添える（同じ移動先に成/不成が並ぶので、
    // **どちらの候補なのかが矢印だけでは読めない**）。
    if (m.promote) {
      const p = add("text", {
        class: "study-hint-promote",
        x: tx + 0.3, y: ty - 0.3, "text-anchor": "middle",
        "dominant-baseline": "central", "font-size": 0.4,
      });
      p.textContent = "成";
    }
  };

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
    // 駒を掴んでいるあいだは、**動かせないマスを暗くする**（2026-08-29）。
    // 光らせるのではなく暗幕を敷くのは CSS 側（.study-grid.is-picking）の仕事で、
    // ここが持つのは「今掴んでいるか」だけ。
    grid.classList.toggle("is-picking", !!pick);
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
    // ⚠️ **候補手の重ね表示もここで描き直す**（描き直しの入口を 1 本にする）。
    // 別の経路を作ると、盤だけ新しくて矢印が前の局面のまま、という食い違いが出る。
    drawHint();
  };

  // dropReason は駒台の駒を打てない理由。**押したときにも同じ文言を出す**
  // （ツールチップだけだとホバーしないと読めない）。
  const dropReason = (mine: boolean) =>
    mine
      ? "打てる場所がありません（二歩・打ち歩詰め・行き所のない駒）"
      : `今は${state?.turnLabel ?? "相手の番"}です`;

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

  // 掴んだ駒は**右クリックで離せる**（2026-08-29）。
  //
  // 掴み直しは「同じ駒をもう一度押す」でもできるが、**指す先を探しているあいだ
  // 目は盤の別のところを見ている**ので、やめるのに掴んだ駒まで戻るのは 1 手多い。
  // 訂正タブ（`editor.ts`）で掴んだ駒を右クリックで離せるのと同じ約束に揃える。
  //
  // ⚠️ **受けるのは盤と駒台だけ。** `window` で拾うと、**手順リストの右クリック
  // （手を消す・分岐にする）まで巻き添えで掴んだ駒を落とす**（あちらは盤とは
  // 別の操作で、掴んだままメニューを開くのは普通に起きる）。
  //
  // ⚠️ **掴んでいなくても既定のメニューは止める**（訂正タブの盤と同じ）。
  // webview の既定メニューは盤の上では意味が無く、出ると「右クリックは
  // 何か別のもの」に見える。
  const cancelPick = (e: MouseEvent) => {
    if (ask) {
      return;
    }
    e.preventDefault();
    if (!pick) {
      // 掴んでいないなら**メニュー**（2026-09-10）。⚠️ **順番を入れ替えないこと** ——
      // 掴んでいるときは「離す」が先で、メニューは出さない。
      onMenu?.(e.clientX, e.clientY);
      return;
    }
    pick = null;
    onError(""); // 前の操作の理由を残さない
    paint();
  };
  grid.addEventListener("contextmenu", cancelPick);
  handSlots.black.addEventListener("contextmenu", cancelPick);
  handSlots.white.addEventListener("contextmenu", cancelPick);

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
    // ⚠️ **重ね表示している候補手も捨てる。** あれは**前の局面**でエンジンが
    // 読んだ手なので、盤が進んだあとも残っていると**今の局面の候補として読まれる**
    // （解析結果を局面が変わったら消すのと同じ理由）。
    hint = null;
    // ⚠️ **手順は呼び出し側が描く**（2026-09-08 に切り出した。`movelist.ts`）。
    onState(next);
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
        paint();
        return;
      }
      state = next;
      pick = null;
      layoutGrid();
      paint();
    },
    relayout() {
      layoutGrid();
      paint();
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
    step(delta: number) {
      if (!state?.loaded || !delta) {
        return;
      }
      // **今の経路の中での位置**（`line` は根から今の枝の終わりまで通っている）。
      const line = state.line ?? [];
      const at = line.indexOf(state.currentId ?? 0);
      if (at < 0) {
        return;
      }
      const to = line[at + (delta < 0 ? -1 : 1)];
      // ⚠️ **端では何もしない**（`undefined` は「その先が無い」）。
      if (to === undefined) {
        return;
      }
      void run(() => StudyService.GoTo(to));
    },
    showHint(usi: string | null) {
      const next = usi || null;
      if (next === hint) {
        return;
      }
      hint = next;
      drawHint();
    },
  };
}
