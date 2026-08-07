// 訂正 UI（Phase 5 の必須コンポーネント）。
//
// **「存在するはずの駒」を中心に回す。** 将棋の駒は先後合わせて枚数が決まっているので、
// 「盤上に何枚あるか」ではなく「**あと何枚あるはずか**」を駒箱に出す。認識は
// **余計な駒を作る**（実測で香が 11 枚）ので、過剰は駒箱側に負の数（"+2 多い"）として
// 出して、盤から外す先を用意する。
//
// 操作はドラッグ＆ドロップが主:
//
//   駒箱 → 盤   置く（在庫が尽きていても置ける。**余計な駒を外す前に正しい駒を
//               置けないと詰む**ため。上限超過は警告と駒箱の赤い数字に出る）
//   盤   → 盤   動かす（移動先の駒は置き換わる。取るのではない）
//   盤   → 駒箱 外す（**認識が作った余計な駒を消す操作**）
//   クリック     成/不成
//   右クリック   先後の反転（認識は駒の向きを外す）
//
// **盤の描画は `<shogi-board>`（core/web）のまま。** 訂正用に盤を描き直さない
// （描画が 2 実装になると、訂正中と確定後で見た目が変わる）。当たり判定は
// 透明な 9x9 のグリッドを**その上に重ねて**取る。重ね方は座標を持たない
// パーセント指定なので、盤の表示サイズが変わっても付いてくる。
//
// **状態は Go 側（PositionService）が持つ。** ここは操作を送って、返ってきた
// EditState をそのまま描くだけ。フロントに局面の写しを持つと、ずれたときに
// どちらが本当か分からなくなる。
import { PositionService } from "../bindings/ikkyoku-app";
import type { EditState, EditCell } from "../bindings/ikkyoku-app/models";
import type { Stock } from "../bindings/github.com/ShinteLab/ikkyoku/position/models";

// 手番。Go 側（position.Turn）と同じ値。
const TURN_UNKNOWN = 0;
const TURN_BLACK = 1;
const TURN_WHITE = 2;

// ドラッグの中身。dataTransfer に JSON で載せる。
type Drag =
  | { from: "cell"; rank: number; file: number }
  | { from: "stock"; piece: number; black: boolean };

const DRAG_TYPE = "application/x-ikkyoku-piece";

const RANK_KANJI = ["一", "二", "三", "四", "五", "六", "七", "八", "九"];
const cellLabel = (rank: number, file: number) => `${9 - file}${RANK_KANJI[rank] ?? "?"}`;

export interface EditorHandle {
  // load は認識結果（盤面部分の SFEN）を読み込んで訂正を始める。撮るたびに呼ぶ。
  load(boardSFEN: string): Promise<void>;
  // clear は局面が無い状態に戻す（撮る前の表示）。
  clear(): void;
}

export interface EditorOptions {
  // stage は <shogi-board> を包む要素。ここにグリッドを重ねる。
  stage: HTMLElement;
  // panel は駒箱と訂正ツールバーを置く場所。
  panel: HTMLElement;
  // onState は操作のたびに呼ばれる。盤・SFEN・警告の表示は呼び出し側（mainscreen）が持つ。
  onState(state: EditState | null): void;
  // onError は操作が通らなかったときの理由（「移動元が空マスです」など）。
  onError(message: string): void;
}

export function mountEditor(opts: EditorOptions): EditorHandle {
  const { stage, panel, onState, onError } = opts;

  // ---- 盤に重ねるグリッド -------------------------------------------------
  //
  // <shogi-board> の SVG は 560x560 で、盤の中身は外周 28px を除いた内側
  // （core/web の MARGIN=28 / CELL=56 / 9 マス）。**同じ比率で重ねれば表示サイズに
  // 依存しない**ので、px を測らずに済む（5% = 28/560、90% = 504/560）。
  // 盤が 560px より大きくならないことは CSS 側（--board-max）で担保している。
  const grid = document.createElement("div");
  grid.className = "edit-grid";
  const cells: HTMLDivElement[] = [];
  for (let rank = 0; rank < 9; rank++) {
    for (let file = 0; file < 9; file++) {
      const cell = document.createElement("div");
      cell.className = "edit-cell";
      cell.dataset.rank = String(rank);
      cell.dataset.file = String(file);
      grid.appendChild(cell);
      cells.push(cell);
    }
  }
  stage.appendChild(grid);

  panel.innerHTML = `
    <div class="edit-bar">
      <button id="edit-toggle" class="ghost-btn" type="button" aria-pressed="false">訂正する</button>
      <span class="edit-hint">
        駒箱から盤へドラッグして置く / 盤から駒箱へドラッグして外す /
        クリックで成・不成 / 右クリックで先後
      </span>
      <span class="spacer"></span>
      <button id="edit-reset" class="ghost-btn" type="button" hidden
              title="訂正を捨てて、認識したときの盤面に戻します">認識結果に戻す</button>
    </div>
    <div id="edit-body" class="edit-body" hidden>
      <div class="edit-meta">
        <span class="field-label">手番</span>
        <div class="turn-group" role="group" aria-label="手番">
          <button class="turn-btn" type="button" data-turn="1">先手番</button>
          <button class="turn-btn" type="button" data-turn="2">後手番</button>
          <button class="turn-btn" type="button" data-turn="0">不明</button>
        </div>
        <span class="field-label">手数</span>
        <input id="edit-movenum" class="movenum" type="number" min="0" step="1" value="0"
               title="0 なら不明。撮った 1 枚からは分からないのが普通です" />
        <span class="note">盤面からは決まりません</span>
      </div>
      <div id="stock-rail" class="stock-rail"></div>
    </div>
  `;

  const toggle = panel.querySelector<HTMLButtonElement>("#edit-toggle")!;
  const resetBtn = panel.querySelector<HTMLButtonElement>("#edit-reset")!;
  const body = panel.querySelector<HTMLDivElement>("#edit-body")!;
  const rail = panel.querySelector<HTMLDivElement>("#stock-rail")!;
  const moveNum = panel.querySelector<HTMLInputElement>("#edit-movenum")!;
  const turnBtns = Array.from(panel.querySelectorAll<HTMLButtonElement>(".turn-btn"));

  let state: EditState | null = null;
  let editing = false;

  const setEditing = (on: boolean) => {
    editing = on && !!state?.loaded;
    toggle.classList.toggle("is-active", editing);
    toggle.setAttribute("aria-pressed", String(editing));
    toggle.textContent = editing ? "訂正をやめる" : "訂正する";
    body.hidden = !editing;
    stage.classList.toggle("is-editing", editing);
    grid.classList.toggle("is-active", editing);
  };

  // 操作の結果を反映する。**失敗しても状態は返ってくる**ので、まず描いてから
  // 理由を出す（画面が古いまま残るほうが分かりにくい）。
  const apply = async (op: () => Promise<EditState>) => {
    try {
      render(await op());
    } catch (err) {
      // Wails は Go の error を例外にして返すが、状態は同時に取れないので取り直す。
      try {
        render(await PositionService.State());
      } catch {
        /* 取れないなら描き直さない。理由だけ出す。 */
      }
      onError(String(err instanceof Error ? err.message : err));
    }
  };

  function render(next: EditState) {
    state = next;
    if (!next.loaded) {
      setEditing(false);
      toggle.disabled = true;
      resetBtn.hidden = true;
      onState(null);
      return;
    }
    toggle.disabled = false;
    resetBtn.hidden = !next.dirty;

    for (let i = 0; i < cells.length; i++) {
      const c: EditCell | undefined = next.cells?.[i];
      const el = cells[i];
      const rank = Number(el.dataset.rank);
      const file = Number(el.dataset.file);
      const empty = c?.empty ?? true;
      el.classList.toggle("is-empty", empty);
      el.draggable = !empty;
      el.dataset.mark = c?.mark ?? "";
      el.title = `${cellLabel(rank, file)} ${c?.name ?? "空"}`;
    }

    for (const b of turnBtns) {
      b.classList.toggle("is-active", Number(b.dataset.turn) === next.turn);
    }
    if (document.activeElement !== moveNum) {
      moveNum.value = String(next.moveNumber ?? 0);
    }
    renderRail(next.inventory ?? []);
    onState(next);
  }

  // ---- 駒箱 ---------------------------------------------------------------
  //
  // 1 行 1 駒種。**出すのは盤上の枚数ではなく「残り」**（存在するはずの総数 − 盤上）。
  // 正なら駒台にあるはずの枚数、負なら認識が作った余計な駒。
  function renderRail(inv: Stock[]) {
    rail.replaceChildren();
    for (const s of inv) {
      const row = document.createElement("div");
      row.className = "stock-row";
      if (s.rest < 0) {
        row.classList.add("is-over");
      }

      // ドラッグ元は先手・後手の 2 つ。**枚数は共有**（上限が先後合計なので）。
      for (const black of [true, false]) {
        const chip = document.createElement("div");
        chip.className = black ? "stock-chip" : "stock-chip is-white";
        chip.draggable = true;
        chip.dataset.piece = String(s.piece);
        chip.dataset.black = String(black);
        chip.textContent = black ? s.letter : s.letter.toLowerCase();
        chip.title = `${black ? "先手" : "後手"}の${s.name}を盤に置く`;
        row.appendChild(chip);
      }

      const count = document.createElement("span");
      count.className = "stock-count";
      if (s.rest < 0) {
        count.textContent = `${-s.rest}枚多い`;
        count.title = `${s.name}が上限（${s.limit}枚）より多く盤にあります。余分を駒箱へドラッグして外してください`;
      } else {
        count.textContent = `${s.rest}`;
        count.title = `${s.name}は残り ${s.rest} 枚（上限 ${s.limit}・盤上 ${s.black + s.white}）`;
      }
      row.appendChild(count);

      // 駒台の先後の割り振り。**盤面からは決まらない**ので人が決める。
      // 残りが 0 のときは出さない（割り振る対象が無い）。
      const split = document.createElement("div");
      split.className = "stock-split";
      if (s.rest > 0) {
        const dec = document.createElement("button");
        dec.type = "button";
        dec.className = "split-btn";
        dec.textContent = "◂";
        dec.title = "先手の駒台を減らす（後手へ）";
        dec.disabled = s.handBlack <= 0;
        dec.addEventListener("click", () => {
          void apply(() => PositionService.SetHandBlack(s.piece, s.handBlack - 1));
        });

        const label = document.createElement("span");
        label.className = "split-label";
        label.textContent = `先${s.handBlack} / 後${s.rest - s.handBlack}`;

        const inc = document.createElement("button");
        inc.type = "button";
        inc.className = "split-btn";
        inc.textContent = "▸";
        inc.title = "先手の駒台を増やす（後手から）";
        inc.disabled = s.handBlack >= s.rest;
        inc.addEventListener("click", () => {
          void apply(() => PositionService.SetHandBlack(s.piece, s.handBlack + 1));
        });

        split.append(dec, label, inc);
      }
      row.appendChild(split);
      rail.appendChild(row);
    }
  }

  // ---- ドラッグ＆ドロップ -------------------------------------------------

  // ドラッグ中のゴースト。透明なマスをそのまま掴むと何も見えないので、
  // 駒文字を描いた要素を一時的に作って setDragImage に渡す。
  let ghost: HTMLElement | null = null;
  const makeGhost = (mark: string, black: boolean) => {
    const g = document.createElement("div");
    g.className = black ? "drag-ghost" : "drag-ghost is-white";
    g.textContent = mark;
    document.body.appendChild(g);
    ghost = g;
    return g;
  };
  const dropGhost = () => {
    ghost?.remove();
    ghost = null;
  };

  const setDrag = (e: DragEvent, data: Drag) => {
    e.dataTransfer?.setData(DRAG_TYPE, JSON.stringify(data));
    e.dataTransfer!.effectAllowed = "move";
  };
  const readDrag = (e: DragEvent): Drag | null => {
    const raw = e.dataTransfer?.getData(DRAG_TYPE);
    if (!raw) {
      return null;
    }
    try {
      return JSON.parse(raw) as Drag;
    } catch {
      return null;
    }
  };

  grid.addEventListener("dragstart", (e) => {
    const el = (e.target as HTMLElement)?.closest<HTMLElement>(".edit-cell");
    if (!editing || !el || el.classList.contains("is-empty")) {
      e.preventDefault();
      return;
    }
    const rank = Number(el.dataset.rank);
    const file = Number(el.dataset.file);
    const mark = el.dataset.mark ?? "";
    setDrag(e, { from: "cell", rank, file });
    e.dataTransfer?.setDragImage(makeGhost(mark, mark === mark.toUpperCase()), 20, 20);
    el.classList.add("is-dragging");
  });

  grid.addEventListener("dragend", (e) => {
    (e.target as HTMLElement)?.classList.remove("is-dragging");
    dropGhost();
  });

  grid.addEventListener("dragover", (e) => {
    if (!editing || !e.dataTransfer?.types.includes(DRAG_TYPE)) {
      return;
    }
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    const el = (e.target as HTMLElement)?.closest<HTMLElement>(".edit-cell");
    for (const c of cells) {
      c.classList.toggle("is-over", c === el);
    }
  });

  grid.addEventListener("dragleave", (e) => {
    (e.target as HTMLElement)?.closest<HTMLElement>(".edit-cell")?.classList.remove("is-over");
  });

  grid.addEventListener("drop", (e) => {
    if (!editing) {
      return;
    }
    e.preventDefault();
    for (const c of cells) {
      c.classList.remove("is-over");
    }
    const el = (e.target as HTMLElement)?.closest<HTMLElement>(".edit-cell");
    const data = readDrag(e);
    if (!el || !data) {
      return;
    }
    const rank = Number(el.dataset.rank);
    const file = Number(el.dataset.file);
    if (data.from === "cell") {
      void apply(() => PositionService.Move(data.rank, data.file, rank, file));
    } else {
      // 駒箱からは常に不成で置く（成っているかはクリックで切り替える）。
      void apply(() => PositionService.Place(rank, file, data.piece, data.black, false));
    }
  });

  // クリックで成/不成。空マスでは何もしない。
  grid.addEventListener("click", (e) => {
    const el = (e.target as HTMLElement)?.closest<HTMLElement>(".edit-cell");
    if (!editing || !el || el.classList.contains("is-empty")) {
      return;
    }
    void apply(() =>
      PositionService.TogglePromoted(Number(el.dataset.rank), Number(el.dataset.file)),
    );
  });

  // 右クリックで先後の反転。**認識は駒の向きを外す**ので、これが要る。
  grid.addEventListener("contextmenu", (e) => {
    const el = (e.target as HTMLElement)?.closest<HTMLElement>(".edit-cell");
    if (!editing || !el || el.classList.contains("is-empty")) {
      return;
    }
    e.preventDefault();
    void apply(() =>
      PositionService.FlipSide(Number(el.dataset.rank), Number(el.dataset.file)),
    );
  });

  // 駒箱側。盤から外すドロップ先でもある。
  rail.addEventListener("dragstart", (e) => {
    const chip = (e.target as HTMLElement)?.closest<HTMLElement>(".stock-chip");
    if (!editing || !chip) {
      e.preventDefault();
      return;
    }
    const piece = Number(chip.dataset.piece);
    const black = chip.dataset.black === "true";
    setDrag(e, { from: "stock", piece, black });
    e.dataTransfer?.setDragImage(makeGhost(chip.textContent ?? "", black), 20, 20);
  });

  rail.addEventListener("dragend", dropGhost);

  rail.addEventListener("dragover", (e) => {
    if (!editing || !e.dataTransfer?.types.includes(DRAG_TYPE)) {
      return;
    }
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    rail.classList.add("is-over");
  });

  rail.addEventListener("dragleave", () => rail.classList.remove("is-over"));

  rail.addEventListener("drop", (e) => {
    if (!editing) {
      return;
    }
    e.preventDefault();
    rail.classList.remove("is-over");
    const data = readDrag(e);
    // 駒箱から駒箱へのドロップは何もしない（元から盤に無い）。
    if (data?.from === "cell") {
      void apply(() => PositionService.Remove(data.rank, data.file));
    }
  });

  // ---- 手番・手数・トグル -------------------------------------------------

  toggle.addEventListener("click", () => setEditing(!editing));

  resetBtn.addEventListener("click", () => {
    void apply(() => PositionService.Reset());
  });

  for (const b of turnBtns) {
    b.addEventListener("click", () => {
      const turn = Number(b.dataset.turn);
      if (turn !== TURN_UNKNOWN && turn !== TURN_BLACK && turn !== TURN_WHITE) {
        return;
      }
      void apply(() => PositionService.SetTurn(turn));
    });
  }

  moveNum.addEventListener("change", () => {
    const n = Number(moveNum.value);
    if (!Number.isInteger(n) || n < 0) {
      moveNum.value = String(state?.moveNumber ?? 0);
      return;
    }
    void apply(() => PositionService.SetMoveNumber(n));
  });

  return {
    async load(boardSFEN: string) {
      await apply(() => PositionService.Load(boardSFEN));
    },
    clear() {
      state = null;
      setEditing(false);
      toggle.disabled = true;
      resetBtn.hidden = true;
      onState(null);
    },
  };
}
