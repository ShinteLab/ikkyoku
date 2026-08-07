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
//   盤   → 駒台 外して、その側の持ち駒にする（**外すのと先後を決めるのが 1 操作**）
//   駒台 → 盤   打つ（その側の駒台にある駒だけ）
//   盤   → 駒箱 先後を決めずに外す（**認識が作った余計な駒を消す操作**）
//   駒台 ⇄ 駒台 持ち主を変える
//   クリック     成/不成
//   右クリック   先後の反転（認識は駒の向きを外す）
//
// **駒台は 2 つ（先手・後手）、駒箱は「まだ先後を決めていない駒」。** 盤に無い駒は
// 駒数保存則からどちらかの駒台にあるはずだが、**どちらかは盤面からは決まらない**
// （設計原則5）。決めるまで駒箱に居座り、決まるまで SFEN は組み上がらない。
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
  // 盤のマスから
  | { from: "cell"; rank: number; file: number }
  // 駒箱の見本から（在庫を見ずに置く）
  | { from: "stock"; piece: number; black: boolean }
  // 駒台から（その側が持っている駒）
  | { from: "hand"; piece: number; black: boolean };

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
  // handSlots は駒台を置く場所。**後手は盤の左上、先手は右下**(実際の将棋盤と
  // 同じ並び)。位置は CSS(.board-with-hands のグリッド)が決めるので、
  // ここは中身を入れるだけ。
  handSlots: { black: HTMLElement; white: HTMLElement };
  // panel は駒箱と訂正ツールバーを置く場所。
  panel: HTMLElement;
  // onState は操作のたびに呼ばれる。盤・SFEN・警告の表示は呼び出し側（mainscreen）が持つ。
  onState(state: EditState | null): void;
  // onError は操作が通らなかったときの理由（「移動元が空マスです」など）。
  onError(message: string): void;
}

export function mountEditor(opts: EditorOptions): EditorHandle {
  const { stage, panel, handSlots, onState, onError } = opts;

  // ---- 盤に重ねるグリッド -------------------------------------------------
  //
  // ⚠️ **SVG の実寸を測って置く。** 以前は「箱に対する割合(inset: 5%)」で重ねていたが、
  // それは**箱と SVG が必ず同じ大きさである**ことに依存していて、ウィンドウの高さを
  // 変えると崩れた（SVG は `max-width` と固有サイズ 560 で決まるので、箱のほうだけが
  // 先に変わる瞬間がある）。**駒の見た目とクリック領域がずれるという最悪の壊れ方**を
  // するので、箱の大きさを当てにしないこと。
  //
  // 測るのは SVG の描画領域そのもので、そこから外周（MARGIN/全体 = 28/560）を除いた
  // 内側が 9x9 のマス。この比率だけは core/web の定数（CELL=56 / MARGIN=28）に
  // 由来するので、**向こうが変わったらここも直す**。
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

  // 盤の SVG 座標（core/web の shogi-board.js）。マス目は (MARGIN, MARGIN) から
  // CELL*9 の正方形。**向こうが変わったらここも直す。**
  const SVG_MARGIN = 28;
  const SVG_CELL = 56;

  // グリッドを盤のマス目に合わせる。**サイズが変わりうる場面すべてで呼ぶこと**
  // （ウィンドウのリサイズ・盤の描き直し・訂正モードの切り替え）。
  //
  // ⚠️ **SVG の要素の矩形ではなく、SVG 座標を画面座標へ変換して使う**
  // （`getScreenCTM`）。`<shogi-board>` の SVG は `preserveAspectRatio` が既定
  // （`xMidYMid meet`）なので、**要素の箱が正方形でないと絵は箱の中で中央寄せになり、
  // 左右に余白ができる**。箱の左端から測ると、その余白のぶんだけ横にずれる
  // （実際にそうなっていた）。CTM なら余白も拡大率もまとめて解決される。
  const layoutGrid = () => {
    const svg = stage.querySelector("shogi-board")?.shadowRoot?.querySelector("svg");
    const ctm = svg?.getScreenCTM();
    if (!svg || !ctm) {
      // まだ <shogi-board> が読み込まれていない。描き直しのときに再度呼ばれる。
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
    const base = stage.getBoundingClientRect();
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

  // 盤の大きさは「ウィンドウの高さ」でも変わる（CSS の min(...) に 100vh が入る）ので、
  // 箱の観測だけでは足りない。ResizeObserver は箱、resize はウィンドウを見る。
  const observer = new ResizeObserver(() => layoutGrid());
  observer.observe(stage);
  window.addEventListener("resize", layoutGrid);

  panel.innerHTML = `
    <div class="edit-bar">
      <button id="edit-toggle" class="ghost-btn" type="button" aria-pressed="false">訂正する</button>
      <span class="edit-hint">
        盤 ⇄ 駒台をドラッグ（外すと同時に持ち主が決まる） / 駒箱は先後未決の置き場 /
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

  // 駒台は盤の脇（後手=左上 / 先手=右下）に置く。**訂正ツールバーの中ではない。**
  // 盤との位置関係そのものが「どちらの駒台か」の説明になるので、離さないこと。
  const handZone = (black: boolean) => {
    const zone = document.createElement("div");
    zone.className = "hand-zone";
    zone.dataset.black = String(black);
    zone.innerHTML =
      `<span class="hand-zone-label">${black ? "先手" : "後手"}の駒台</span>` +
      `<div class="hand-chips"></div>`;
    (black ? handSlots.black : handSlots.white).appendChild(zone);
    return zone;
  };

  const toggle = panel.querySelector<HTMLButtonElement>("#edit-toggle")!;
  const resetBtn = panel.querySelector<HTMLButtonElement>("#edit-reset")!;
  const body = panel.querySelector<HTMLDivElement>("#edit-body")!;
  const rail = panel.querySelector<HTMLDivElement>("#stock-rail")!;
  const handZones = [handZone(true), handZone(false)];
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
    // 駒台は訂正中だけ出す。**盤の脇に空の箱を常設しない**(訂正していないときは
    // 駒台の中身は盤面タブの「駒台」行に文字で出ている)。
    handSlots.black.hidden = !editing;
    handSlots.white.hidden = !editing;
    // 駒台の出し入れで盤の位置が動く（グリッドの並びが変わる）。
    layoutGrid();
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
    const inv = next.inventory ?? [];
    renderRail(inv);
    renderHands(inv);
    // onState が盤の sfen 属性を書き換える（= <shogi-board> が SVG を描き直す）ので、
    // グリッドの位置合わせはそのあと。
    onState(next);
    layoutGrid();
  }

  // ---- 駒箱と駒台 ---------------------------------------------------------
  //
  // 駒箱は 1 行 1 駒種。**出すのは盤上の枚数ではなく「残り」**（存在するはずの
  // 総数 − 盤上）で、正なら盤の外にあるはずの枚数、負なら認識が作った余計な駒。
  // 駒台に割り振ったぶんは駒箱から減り、**残っているのが「先後未決」の駒**。
  function renderRail(inv: Stock[]) {
    rail.replaceChildren();
    for (const s of inv) {
      const row = document.createElement("div");
      row.className = "stock-row";
      if (s.rest < 0) {
        row.classList.add("is-over");
      } else if (s.unassigned > 0) {
        row.classList.add("is-unassigned");
      }

      // 見本。**在庫を見ずに置ける**ので、残りが 0 でも 過剰でもドラッグできる
      // （間違った駒を外す前に正しい駒を置きたい場面があるため）。
      for (const black of [true, false]) {
        const chip = document.createElement("div");
        chip.className = black ? "stock-chip" : "stock-chip is-white";
        chip.draggable = true;
        chip.dataset.piece = String(s.piece);
        chip.dataset.black = String(black);
        chip.textContent = black ? s.letter : s.letter.toLowerCase();
        chip.title = `${black ? "先手" : "後手"}の${s.name}を盤に置く（在庫を見ません）`;
        row.appendChild(chip);
      }

      const count = document.createElement("span");
      count.className = "stock-count";
      if (s.rest < 0) {
        count.textContent = `${-s.rest}枚多い`;
        count.title =
          `${s.name}が上限（${s.limit}枚）より多く盤にあります。` +
          `余分を駒箱か駒台へドラッグして外してください`;
      } else {
        count.textContent = s.unassigned > 0 ? `未決 ${s.unassigned}` : `${s.rest}`;
        count.title =
          `${s.name}: 上限 ${s.limit} / 盤上 ${s.black + s.white} / ` +
          `駒台 先${s.handBlack} 後${s.handWhite} / 先後未決 ${s.unassigned}`;
      }
      row.appendChild(count);

      // 未割り当ての駒。**どちらの駒台かは盤面からは決まらない**ので、
      // ここから駒台へドラッグして人が決める。
      const pool = document.createElement("div");
      pool.className = "stock-pool";
      for (let i = 0; i < s.unassigned; i++) {
        const chip = document.createElement("div");
        chip.className = "pool-chip";
        chip.draggable = true;
        chip.dataset.piece = String(s.piece);
        chip.textContent = s.letter;
        chip.title = `${s.name}（どちらの駒台か未決）。先手か後手の駒台へドラッグしてください`;
        pool.appendChild(chip);
      }
      row.appendChild(pool);
      rail.appendChild(row);
    }
  }

  // 駒台。**盤に無い駒のうち、持ち主が決まったもの**を並べる。
  // ここからは盤へ打てるし、反対側の駒台へ移せる。
  //
  // **枚数は数字ではなく駒そのものの数で見せる**（歩 5 枚なら駒を 5 枚並べる）。
  // 「歩5」と書くより、実際の駒台と同じで一目で分かるため。
  function renderHands(inv: Stock[]) {
    for (const zone of handZones) {
      const black = zone.dataset.black === "true";
      const chips = zone.querySelector<HTMLDivElement>(".hand-chips")!;
      chips.replaceChildren();
      let total = 0;
      for (const s of inv) {
        const n = black ? s.handBlack : s.handWhite;
        for (let i = 0; i < n; i++) {
          total++;
          const chip = document.createElement("div");
          chip.className = black ? "stock-chip" : "stock-chip is-white";
          chip.draggable = true;
          chip.dataset.piece = String(s.piece);
          chip.dataset.hand = "true";
          chip.textContent = black ? s.letter : s.letter.toLowerCase();
          chip.title = `${black ? "先手" : "後手"}の${s.name}（${n}枚）`;
          chips.appendChild(chip);
        }
      }
      zone.classList.toggle("is-empty", total === 0);
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
    } else if (data.from === "hand") {
      // 駒台から打つ。**その側の駒台に無ければ Go 側で弾かれる。**
      void apply(() => PositionService.FromHand(rank, file, data.piece, data.black));
    } else {
      // 駒箱の見本からは常に不成で置く（成っているかはクリックで切り替える）。
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

  // 駒箱。見本と「先後未決の駒」のドラッグ元であり、
  // **盤から先後を決めずに外すドロップ先**でもある。
  rail.addEventListener("dragstart", (e) => {
    const target = e.target as HTMLElement | null;
    const sample = target?.closest<HTMLElement>(".stock-chip");
    const pooled = target?.closest<HTMLElement>(".pool-chip");
    if (!editing || (!sample && !pooled)) {
      e.preventDefault();
      return;
    }
    if (sample) {
      const black = sample.dataset.black === "true";
      setDrag(e, { from: "stock", piece: Number(sample.dataset.piece), black });
      e.dataTransfer?.setDragImage(makeGhost(sample.textContent ?? "", black), 20, 20);
      return;
    }
    // 未決の駒。持ち主が決まっていないので、置き先の駒台が先後を決める。
    // 盤に落とされたときは先手の駒として置く（見本と同じ扱い。右クリックで直せる）。
    setDrag(e, { from: "stock", piece: Number(pooled!.dataset.piece), black: true });
    e.dataTransfer?.setDragImage(makeGhost(pooled!.textContent ?? "", true), 20, 20);
  });

  rail.addEventListener("dragend", dropGhost);
  makeDropZone(rail, (data) => {
    // 盤から: 先後を決めずに外す。駒台から: 持ち主を未決に戻す。
    if (data.from === "cell") {
      return PositionService.Remove(data.rank, data.file);
    }
    if (data.from === "hand") {
      return setHandDelta(data.piece, data.black, -1);
    }
    return null;
  });

  // 駒台（先手・後手）。**外すのと先後を決めるのが 1 操作**になるドロップ先。
  for (const zone of handZones) {
    const black = zone.dataset.black === "true";

    zone.addEventListener("dragstart", (e) => {
      const chip = (e.target as HTMLElement)?.closest<HTMLElement>(".stock-chip");
      if (!editing || !chip) {
        e.preventDefault();
        return;
      }
      setDrag(e, { from: "hand", piece: Number(chip.dataset.piece), black });
      e.dataTransfer?.setDragImage(makeGhost(chip.textContent ?? "", black), 20, 20);
    });
    zone.addEventListener("dragend", dropGhost);

    makeDropZone(zone, (data) => {
      if (data.from === "cell") {
        return PositionService.ToHand(data.rank, data.file, black);
      }
      if (data.from === "hand") {
        // 反対側の駒台から。**同じ側なら何もしない。**
        if (data.black === black) {
          return null;
        }
        return moveBetweenHands(data.piece, data.black, black);
      }
      // 駒箱の見本・未決の駒から: この側の駒台に 1 枚足す。
      return setHandDelta(data.piece, black, +1);
    });
  }

  // makeDropZone はドロップ先の共通処理（ハイライトと dragover の許可）。
  // handler が null を返したら何もしない。
  function makeDropZone(el: HTMLElement, handler: (data: Drag) => Promise<EditState> | null) {
    el.addEventListener("dragover", (e) => {
      if (!editing || !e.dataTransfer?.types.includes(DRAG_TYPE)) {
        return;
      }
      e.preventDefault();
      e.dataTransfer.dropEffect = "move";
      el.classList.add("is-over");
    });
    el.addEventListener("dragleave", (e) => {
      // 中の要素へ移っただけの dragleave では消さない。
      if (!el.contains(e.relatedTarget as Node | null)) {
        el.classList.remove("is-over");
      }
    });
    el.addEventListener("drop", (e) => {
      if (!editing) {
        return;
      }
      e.preventDefault();
      el.classList.remove("is-over");
      const data = readDrag(e);
      if (!data) {
        return;
      }
      const p = handler(data);
      if (p) {
        void apply(() => p);
      }
    });
  }

  // 駒台の枚数を 1 増減する。**枚数は状態から読み直す**（フロントで数えない）。
  function setHandDelta(piece: number, black: boolean, delta: number): Promise<EditState> | null {
    const s = state?.inventory?.find((x) => x.piece === piece);
    if (!s) {
      return null;
    }
    const now = black ? s.handBlack : s.handWhite;
    const next = now + delta;
    if (next < 0) {
      return null;
    }
    return PositionService.SetHand(piece, black, next);
  }

  // 片方の駒台からもう片方へ 1 枚移す。**先に減らしてから増やす**
  // （増やす側が「相手側に割り振り済み」で弾かれないように）。
  async function moveBetweenHands(piece: number, from: boolean, to: boolean): Promise<EditState> {
    const s = state?.inventory?.find((x) => x.piece === piece);
    const fromNow = s ? (from ? s.handBlack : s.handWhite) : 0;
    const toNow = s ? (to ? s.handBlack : s.handWhite) : 0;
    await PositionService.SetHand(piece, from, Math.max(fromNow - 1, 0));
    return PositionService.SetHand(piece, to, toNow + 1);
  }

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
