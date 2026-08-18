// 訂正 UI（Phase 5 の必須コンポーネント）。**訂正タブの中身。**
//
// ⚠️ **訂正モードのトグルは無い（2026-08-10）。訂正タブに居ること自体が訂正モード。**
// 以前は盤面タブ 1 枚の中で「訂正する ⇄ 盤面を確定する」を切り替えており、
// **同じ盤に「自由編集」と「確定した局面」の 2 つの意味**が乗っていた。タブを
// 分けたので、出口は「この局面を解析する」（＝解析タブへ写しを渡す）だけになった。
// **ここにモードを戻さないこと。**
//
// **「存在するはずの駒」を中心に回す。** 将棋の駒は先後合わせて枚数が決まっているので、
// 「盤上に何枚あるか」ではなく「**あと何枚あるはずか**」を駒箱に出す。認識は
// **余計な駒を作る**（実測で香が 11 枚）ので、過剰は駒箱側に負の数（"+2 多い"）として
// 出して、盤から外す先を用意する。
//
// 操作はドラッグ＆ドロップが主:
//
//   足りない駒 → 盤   置く（先手の駒として置く。右クリックで後手に）
//   足りない駒をクリック  掴む。**盤を押すたびに置く**（掴んだまま。Esc で離す）
//   盤 → 盤外        外す（**どこにも落とさなければ捨てる**。先後は決めない）
//   盤 → 盤           動かす（**移動先に駒があれば入れ替わる**。取るのではない）
//   盤 → 駒台         外して、その側の持ち駒にする（**外すのと先後を決めるのが 1 操作**）
//   駒台 → 盤         打つ（その側の駒台にある駒だけ）
//   盤 → 足りない駒   先後を決めずに外す（**認識が作った余計な駒を消す操作**）
//   駒台 ⇄ 駒台       持ち主を変える
//   右クリック        1 マスを回す（先手不成 → 先手成 → 後手不成 → 後手成 → …）
//
// **先後と成/不成を左右のクリックに分けない。** マスに対してやりたいことはこの
// 4 通りしかないので、1 つの操作で回すほうが覚えることが少ない。
//
// **置き場は 3 つ: 先手の駒台・後手の駒台・「足りない駒」。** 盤に無い駒は駒数保存則から
// どちらかの駒台にあるはずだが、**どちらかは盤面からは決まらない**（設計原則5）ので、
// 決まるまで「足りない駒」に居る。決まるまで SFEN は組み上がらない。
//
// **駒種ごとの見本（駒箱）は置かない。** 置きたい駒は必ず「足りない駒」として現れる
// （認識が駒種を間違えていれば、正しいほうの駒が足りなくなる）ので、見本は重複になる。
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

// 掴んでいるものの中身。**JS の変数に持つだけ**（dataTransfer は使わない。下記）。
type Drag =
  // 盤のマスから
  | { from: "cell"; rank: number; file: number }
  // 駒箱の見本から（在庫を見ずに置く）
  | { from: "stock"; piece: number; black: boolean }
  // 駒台から（その側が持っている駒）
  | { from: "hand"; piece: number; black: boolean };

const RANK_KANJI = ["一", "二", "三", "四", "五", "六", "七", "八", "九"];
const cellLabel = (rank: number, file: number) => `${9 - file}${RANK_KANJI[rank] ?? "?"}`;

export interface EditorHandle {
  // load は認識結果（盤面部分の SFEN）を読み込んで訂正を始める。撮るたびに呼ぶ。
  load(boardSFEN: string): Promise<void>;
  // clear は局面が無い状態に戻す（撮る前の表示）。
  clear(): void;
  // relayout は盤に重ねるグリッドを置き直す。
  //
  // ⚠️ **訂正タブを開いた瞬間に呼ぶこと。** グリッドの位置は `getScreenCTM()` で
  // 測っているが、**`display: none` の中では CTM が取れない**（隠れているパネルの
  // 中では null が返る）。タブで隠している以上、開いたときに測り直さないと
  // グリッドが出ないか、前回の大きさのまま残って**1 マスずれたところを編集する**。
  relayout(): void;
}

export interface EditorOptions {
  // stage は <shogi-board> を包む要素。ここにグリッドを重ねる。
  stage: HTMLElement;
  // handSlots は駒台と「足りない駒」を置く場所。**後手は盤の左上、先手は右下**
  // (実際の将棋盤と同じ並び)で、足りない駒は**先手の駒台の上**。
  // 位置は CSS(.board-with-hands のグリッド)が決めるので、ここは中身を入れるだけ。
  handSlots: { black: HTMLElement; white: HTMLElement; missing: HTMLElement };
  // panel は訂正ツールバーを置く場所。
  panel: HTMLElement;
  // viewHost は「目線」のボタンを置く場所（**盤のすぐ下**）。
  //
  // ⚠️ **手番と同じ行に並べないこと**（2026-08-18 に `.edit-meta` から出した）。
  // 「目線」と「手番」は**別の事実**（どちら側から写したか / どちらが指す番か）
  // なのに、同じ見た目のボタンが隣り合っていると同じものの選択肢に見える。
  viewHost: HTMLElement;
  // resetButton は「認識結果に戻す」。**訂正した内容を捨てる操作**なので、盤の近くの
  // ツールバーではなく右上に離してある(押し間違い対策)。置き場所は呼び出し側が持つ。
  resetButton: HTMLButtonElement;
  // onState は操作のたびに呼ばれる。盤・SFEN・警告の表示は呼び出し側（mainscreen）が持つ。
  onState(state: EditState | null): void;
  // onConfirm は「この局面を解析する」が押されたときに呼ばれる。
  //
  // **訂正タブの唯一の出口。** ここから先（確定した局面を解析タブへ渡す・タブを
  // 移る）は呼び出し側（mainscreen）の仕事で、**この UI は確定の可否を判定しない**
  // （手番や駒台の先後が未決かどうかは Go 側の StudyService.Adopt が言う。
  // フロントで同じ判定を書くと 2 か所に散る）。
  onConfirm(): void;
  // onError は操作が通らなかったときの理由（「移動元が空マスです」など）。
  onError(message: string): void;
}

export function mountEditor(opts: EditorOptions): EditorHandle {
  const { stage, panel, viewHost, handSlots, resetButton: resetBtn, onState, onConfirm, onError } =
    opts;

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

  // ---- 視点（**訂正タブでは反転しない**。2026-08-18） ---------------------
  //
  // ⚠️ **訂正タブに視点の切り替えは無い。** 訂正は**撮った画像と見比べて直す**作業
  // なので、盤は**撮ったとおり**（上向きに写っている側が手前）で固定する。裏から
  // 眺める選択肢があると、**すぐ下の「目線」と混同する**（あちらは局面そのものの
  // 向きの話）。反転が要るのは解析タブのほうで、そちらには残してある。
  //
  // 読み替えの仕掛け（`applyFlip` / `nearSide`）は**そのまま残す** —— 駒台の見出しや
  // 駒の向きがこれを見ており、`flipped` が動かないだけで意味は変わらない。
  //
  // ⚠️ **局面には一切効かない。** 反転するのは `<shogi-board>` の絵（`flip` 属性）と
  // 駒台の置き場所だけで、`PositionService` が持つ盤・先後・手番・SFEN は動かない。
  // **取り込みで反転しないという約束**（撮った画像と盤面が一致していること。
  // 学習データのラベルは画素と一致していなければならない）と両立するのはこのため。
  //
  // ⚠️ **そのぶん「見た目の位置」と「局面のマス」が反対になる。** グリッドの DOM は
  // **見た目の順**（左上から右下）に並んでいるので、`dataset` に入れる rank/file の
  // ほうを局面の座標に読み替える。こうしておくと、掴む・置く・回すの処理は
  // 視点を一切知らずに済む（読み替えが 1 か所に閉じる）。
  //
  // ⚠️ **反転すると座標の表示も変わる**（筋が左から 1・2・…、段が下から一・二・…
  // なので **左下が 1一**）。ツールチップ（`cellLabel`）は dataset の局面座標から
  // 作っているので、読み替えさえ正しければ自動で付いてくる。
  const flipped = false;
  const applyFlip = () => {
    for (let i = 0; i < cells.length; i++) {
      const vr = Math.floor(i / 9);
      const vf = i % 9;
      cells[i].dataset.rank = String(flipped ? 8 - vr : vr);
      cells[i].dataset.file = String(flipped ? 8 - vf : vf);
    }
  };
  // ⚠️ **一度は通すこと。** 今は `flipped` が動かないので結果は「見た目の順のまま」に
  // なるが、**読み替えの入口をここ 1 か所に保っておく**（グリッドの DOM の順と
  // 局面のマスを結ぶのはこの関数だけ、という前提を崩さない）。
  applyFlip();

  // グリッドの位置の基準は「箱」であることを、CSS 任せにせずここでも保証する。
  // **CSS が効いていないと基準がページ全体になり、グリッドが丸ごとずれる**
  // （コメントの閉じ忘れで .board-stage のルールが丸ごと捨てられていて、実際に踏んだ）。
  if (getComputedStyle(stage).position === "static") {
    stage.style.position = "relative";
  }

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
    // 基準は**実際に効いている位置の基準**（offsetParent）。stage を決め打ちにすると、
    // 何かの拍子に stage が基準でなくなったときに丸ごとずれる。
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

  // 盤の大きさは「ウィンドウの高さ」でも変わる（CSS の min(...) に 100vh が入る）ので、
  // 箱の観測だけでは足りない。ResizeObserver は箱、resize はウィンドウを見る。
  const observer = new ResizeObserver(() => layoutGrid());
  observer.observe(stage);
  window.addEventListener("resize", layoutGrid);

  // ⚠️ **「訂正する」のトグルは置かない。** 訂正タブに居ること自体が訂正モード。
  // ここに要るのは出口（＝確定して解析タブへ渡す）だけ。
  panel.innerHTML = `
    <div class="edit-bar">
      <button id="edit-confirm" class="ghost-btn is-primary" type="button">この局面を解析する</button>
      <span class="edit-hint">
        盤 ⇄ 駒台をドラッグ（外すと同時に持ち主が決まる） /
        <strong>「足りない駒」をクリックすると掴んだまま連続で置ける</strong>（残り 0 でも置ける。Esc で離す） /
        盤の外へ放ると外れる /
        右クリックで先後と成・不成を切り替え
      </span>
    </div>
    <div id="edit-body" class="edit-body">
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
    </div>
  `;

  // 目線（2026-08-18 に手番の行から盤の下へ出した）。**撮った画像がどちら側から
  // 写したものか。**
  //
  // ⚠️ **盤の絵は 1 マスも動かない。** 訂正は「見えているものを直す」作業で、
  // 学習ラベルも画素と一致していなければならない。効くのは
  // **解析へ渡す局面を 180 度回すかどうか**だけ。
  //
  // ⚠️ **手番と並べないこと。** 別の事実なのに、同じ見た目のボタンが隣り合うと
  // 同じものの選択肢に見える（実際に紛らわしかった）。**盤の下に 1 つだけ**置き、
  // **押すたびに入れ替わる**形にする（状態は 2 つしかないので選択肢を並べなくてよい）。
  //
  // ⚠️ **ボタンの横に注記を出さないこと**（2026-08-18 に外した）。後手目線のときだけ
  // 文字が増えると、**出たり消えたりするたびにボタンの位置がずれて押しづらい**。
  // 「解析へは反転して送信」は**SFEN の後ろ**に添えてある（mainscreen の #sfen-note）。
  viewHost.innerHTML = `<button id="edit-near" class="ghost-btn" type="button"></button>`;

  // 駒台は盤の脇（後手=左上 / 先手=右下）に置く。**訂正ツールバーの中ではない。**
  // 盤との位置関係そのものが「どちらの駒台か」の説明になるので、離さないこと。
  const handZone = (black: boolean) => {
    const zone = document.createElement("div");
    zone.className = "hand-zone";
    zone.dataset.black = String(black);
    // 手番のマーク（2026-08-13）。**駒台の外側の角に絶対配置**なので、
    // 高さを 1px も食わない（盤が小さくならない）。
    // ⚠️ **常に両側に置いて、手番側だけ光らせる。** 手番側にだけ出すと、
    // 切り替えるたびに駒台の中身が動く（出たり消えたりするものを作らない）。
    // ⚠️ **見出しは「手前 / 奥」**（2026-08-18。以前は「先手 / 後手」）。
    // 訂正タブは**撮った画像を直す面**なので、後手目線では手前に写っているのが
    // 後手 —— そこに「先手の駒台」と書いてあると、**画面が嘘をつく**。
    // 位置なら目線に関わらず必ず正しい。文字は `showHandLabels` が入れる
    // （**表示視点を反転すると手前と奥が入れ替わる**ので、作るときには決まらない）。
    zone.innerHTML =
      `<span class="turn-mark">${black ? "▲" : "△"}</span>` +
      `<span class="hand-zone-label"></span>` +
      `<div class="hand-chips"></div>`;
    (black ? handSlots.black : handSlots.white).appendChild(zone);
    return zone;
  };

  // 足りない駒（盤にも駒台にも無い＝**まだ先後を決めていない**駒）。
  // **先手の駒台の上**に置く。駒種ごとの見本を並べる駒箱は廃した
  // （置きたい駒は「足りない駒」として必ずここに出るので、見本は要らない）。
  const missing = document.createElement("div");
  missing.className = "missing-zone";
  missing.innerHTML =
    `<span class="hand-zone-label">足りない駒</span><div class="missing-chips"></div>`;
  handSlots.missing.appendChild(missing);
  const missingChips = missing.querySelector<HTMLDivElement>(".missing-chips")!;

  // nearSide は「その駒台が画面の手前にあるか」。**表示視点で入れ替わる**
  // （盤を裏から眺めると、上向きの駒の駒台が奥へ回る）。
  const nearSide = (black: boolean) => black !== flipped;

  // showHandLabels は駒台の見出しを位置で言い直す（手前 / 奥）。
  // ⚠️ **表示視点を切り替えたら呼ぶこと** —— 呼ばないと、入れ替わった駒台に
  // 前の見出しが残る（**位置で説明しているのに位置と食い違う**）。
  const showHandLabels = () => {
    for (const zone of handZones) {
      const label = zone.querySelector<HTMLElement>(".hand-zone-label")!;
      label.textContent = `${nearSide(zone.dataset.black === "true") ? "手前" : "奥"}の駒台`;
    }
  };

  const confirmBtn = panel.querySelector<HTMLButtonElement>("#edit-confirm")!;
  const body = panel.querySelector<HTMLDivElement>("#edit-body")!;
  const handZones = [handZone(true), handZone(false)];
  const moveNum = panel.querySelector<HTMLInputElement>("#edit-movenum")!;
  const turnBtns = Array.from(panel.querySelectorAll<HTMLButtonElement>(".turn-btn[data-turn]"));
  const nearBtn = viewHost.querySelector<HTMLButtonElement>("#edit-near")!;
  // 目線のボタン 1 つぶんの見た目。**文字がそのまま今の状態**で、押すと反対になる
  // （何が起きるかはツールチップが言う）。
  const paintNearButton = (nearWhite: boolean) => {
    nearBtn.textContent = nearWhite ? "目線: 後手" : "目線: 先手";
    nearBtn.title = nearWhite
      ? "撮った画像は後手目線（後手が手前に写っている）。押すと先手目線に戻ります。盤は変わらず、解析へ渡すときに上下を反転します"
      : "撮った画像は先手目線（先手が手前に写っている）。押すと後手目線になります。盤は変わりません";
    nearBtn.setAttribute("aria-pressed", String(nearWhite));
  };
  // ⚠️ **一度は通すこと。** 駒台の見出し（「手前の駒台」）と目線のボタンの文字は
  // ここで入れているので、通さないと見出しの無い駒台と空のボタンが出る。
  paintNearButton(false);
  showHandLabels();

  let state: EditState | null = null;
  // editable は「局面を読み込んでいるか」。**訂正モードのフラグではない**
  // （訂正タブに居ること自体が訂正モードなので、モードは無い）。
  // 撮る前は盤も駒台も無いので、ドラッグ類を全部止めるために要る。
  let editable = false;

  // syncLoaded は局面の有無だけで見た目を決める。
  //
  // **駒台も「足りない駒」も、読み込んでいれば常に出す。** 以前は訂正モードの
  // 出入りで畳んでいたが、モードが無くなったので出しっぱなしでよい
  // （駒台は局面の一部、足りない駒は訂正タブ専用の置き場）。
  const syncLoaded = () => {
    editable = !!state?.loaded;
    if (!editable && armed) {
      setArmed(null);
    }
    confirmBtn.disabled = !editable;
    confirmBtn.title = editable
      ? "この局面を解析タブへ渡します（あとから訂正タブに戻って直せます）"
      : "まだ局面がありません";
    body.hidden = !editable;
    stage.classList.toggle("is-editing", editable);
    grid.classList.toggle("is-active", editable);
    handSlots.black.hidden = !editable;
    handSlots.white.hidden = !editable;
    handSlots.missing.hidden = !editable;
    // 駒台や足りない駒の出し入れで盤の位置が動く（グリッドの並びが変わる）。
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
      syncLoaded();
      resetBtn.hidden = true;
      onState(null);
      return;
    }
    syncLoaded();
    resetBtn.hidden = !next.dirty;

    paintCells(next);

    for (const b of turnBtns) {
      b.classList.toggle("is-active", Number(b.dataset.turn) === next.turn);
    }
    paintNearButton(!!next.nearWhite);
    // ⚠️ **手番の値は 1 つ（`EditState.turn`）。** ボタンとマークは同じ値を
    // 2 か所に描くだけで、**更新経路もここ 1 本**にする（片方だけ更新する道を
    // 作ると、どちらが本当の手番か分からなくなる）。
    //
    // ⚠️ **マークに渡すのは `seenTurn`（見た目の手番）。** ▲/△ は
    // **上向きに写っている側**に付いているので、後手目線では `turn` と逆になる
    // （後手番なら、上向きに見えている手前の側が指す）。
    // **`turn` を渡さないこと** —— 後手目線のときだけ光る側が入れ替わる。
    showTurnMarks(next.seenTurn ?? next.turn, next.turnLabel ?? "");
    if (document.activeElement !== moveNum) {
      moveNum.value = String(next.moveNumber ?? 0);
    }
    const inv = next.inventory ?? [];
    renderMissing(inv);
    renderHands(inv);
    // チップは作り直されているので、掴んでいる印を付け直す。
    //
    // ⚠️ **残りが 0 になっても離さないこと**（2026-08-18 に一度入れて戻した）。
    // 「足りない駒」の数が正しいのは**認識が完璧なときだけ**で、先に駒台を
    // 埋めてから盤を直すと**残り 0 のまま盤に足したい**場面が普通に来る。
    // そこで勝手に離れると、1 枚置くたびに掴み直すことになって使えない
    // （`Place` が在庫を見ないのと同じ話。設計原則3・4）。
    // **離すのは人が離したときだけ。**
    paintArmed();
    // onState が盤の sfen 属性を書き換える（= <shogi-board> が SVG を描き直す）ので、
    // グリッドの位置合わせはそのあと。
    onState(next);
    layoutGrid();
  }

  // paintCells は 9x9 のマスに中身（駒・掴めるか・ツールチップ）を入れる。
  //
  // ⚠️ **`cells` の並びは見た目の順、`EditState.cells` の並びは局面の順**なので、
  // **配列の添字で突き合わせないこと**（反転すると 1 マスどころか盤ごと裏返る）。
  // 突き合わせるのは `dataset` に入れた局面座標のほう（`applyFlip` が入れる）。
  function paintCells(next: EditState) {
    for (const el of cells) {
      const rank = Number(el.dataset.rank);
      const file = Number(el.dataset.file);
      const c: EditCell | undefined = next.cells?.[rank * 9 + file];
      const empty = c?.empty ?? true;
      el.classList.toggle("is-empty", empty);
      el.dataset.mark = c?.mark ?? "";
      el.title = `${cellLabel(rank, file)} ${c?.name ?? "空"}`;
    }
  }

  // showTurnMarks は駒台の角のマークを手番に合わせる。
  //
  // ⚠️ **訂正タブには「不明」がある**（手番は盤面からは決まらない。設計原則5）。
  // そのときは**どちらも光らせない** —— 先手に倒すと、決めていない手番が
  // 決まったように見える。**理由はツールチップに出す**（光っていない駒台が
  // 2 つ並んでいるだけでは、未決なのか壊れているのか分からない）。
  //
  // ⚠️ **駒台の名前（▲先手 / △後手）は「見たとおり」のまま。** 後手目線なら
  // そこに写っているのは実際には後手の駒だが、**訂正タブは撮った画像を直す面**
  // なので、盤の駒も駒台も見えているとおりに呼ぶ（学習ラベルもこの向き）。
  // ⚠️ **そのぶんツールチップで側の名前を言わないこと** —— 「▲側が指す」のに
  // 「後手番」を選んでいる、という組み合わせが後手目線では普通に起きるので、
  // **「この側が指します」と手番の名前を並べて出す**（どちらも本当）。
  function showTurnMarks(seenTurn: number, turnLabel: string) {
    for (const zone of handZones) {
      const black = zone.dataset.black === "true";
      const mark = zone.querySelector<HTMLElement>(".turn-mark")!;
      const mine = seenTurn === (black ? TURN_BLACK : TURN_WHITE);
      const name = nearSide(black) ? "手前" : "奥";
      mark.classList.toggle("is-active", mine);
      mark.title = mine
        ? `この側が指します（${turnLabel}）`
        : seenTurn === TURN_UNKNOWN
          ? `${name}の駒台（手番はまだ決まっていません）`
          : `${name}の駒台（今は反対側の番・${turnLabel}）`;
    }
  }

  // ---- 足りない駒と駒台 ---------------------------------------------------
  //
  // 「足りない駒」＝ **存在するはずなのに盤にも駒台にも無い駒**（先後未決）。
  // 駒数保存則からどちらかの駒台にあるはずだが、**どちらかは盤面からは決まらない**
  // ので、決まるまでここに置く（設計原則5）。決めるのは駒台へのドラッグ。
  //
  // **駒種は常に全部（表のみ）並べる。** 足りている駒は非活性の見た目にするだけで、
  // **掴めなくはしない**（在庫が尽きていても置ける。設計原則3・4）。並びが毎回同じに
  // なるので、どこを掴めばよいかが変わらないのも利点。枚数はバッジで出す。
  function renderMissing(inv: Stock[]) {
    missingChips.replaceChildren();
    for (const s of inv) {
      // 縦 1 列。**並びは Inventory の順（歩香桂銀金角飛王）のまま**で、
      // 足りていても消さない（位置が動くと、どこを掴むかが毎回変わる）。
      const row = document.createElement("div");
      row.className = "missing-row";

      const chip = document.createElement("div");
      chip.className = "stock-chip is-missing";
      // **足りていても掴める。** 上限で止めると「余計な駒を外す前に正しい駒を
      // 置けない」という詰みが起きる（設計原則3・4）。見た目だけ非活性にする。
      chip.dataset.piece = String(s.piece);
      chip.textContent = s.letter;
      chip.classList.toggle("is-spare", s.unassigned <= 0);

      const count = document.createElement("span");
      count.className = "missing-count";
      count.textContent = s.unassigned > 0 ? String(s.unassigned) : "";

      row.title =
        s.unassigned > 0
          ? `${s.name} ${s.unassigned}枚（どちらの駒台か未決）。` +
            `クリックすると掴んだままになり、盤を押すたびに置けます（Esc で離す）。` +
            `駒台へドラッグすると持ち主が決まり、盤へドラッグすると先手の駒として置きます`
          : `${s.name}は足りています。それでも盤にも駒台にも置けます` +
            `（置くと多すぎる警告が出ます）`;
      row.append(chip, count);
      missingChips.appendChild(row);
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
      // 並びは駒台に置く順（歩香桂銀金角飛王。Go 側の Inventory がその順で返す）。
      // **駒が 180 度回っている側は逆順に並べる。** そちら側から読んだときに
      // 同じ並びに見えるのがこの向きだから。
      //
      // ⚠️ **回っているのは「後手」ではなく「奥の側」。** 視点を反転すると
      // 手前が後手になるので、**逆順にする相手も入れ替わる**（`black === flipped`）。
      // 片方だけ直すと、反転したときに駒台の並びだけ上下逆に読むことになる。
      for (const s of black === flipped ? [...inv].reverse() : inv) {
        const n = black ? s.handBlack : s.handWhite;
        for (let i = 0; i < n; i++) {
          total++;
          const chip = document.createElement("div");
          chip.className = black ? "stock-chip" : "stock-chip is-white";
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

  // ---- ドラッグ（**pointer events で自前に持つ**） -------------------------
  //
  // ⚠️ **HTML5 のネイティブ DnD（`draggable` + `dragstart`/`drop`）を使わないこと。**
  // 2026-08-08 と 2026-08-13 の「メイン画面が真っ黒になって触れなくなる」は、
  // **どちらもこの訂正タブでドラッグしている最中に始まっている**
  // （掴めない/ドロップできない → 禁止カーソル → そのまま黒くなる）。
  //
  // Windows の Chromium はネイティブ DnD を OLE の `DoDragDrop` で動かしており、
  // **ブラウザプロセスの UI スレッドでモーダルなループが回る**。そこが抜けられなく
  // なると、**同じ WebView2 環境にぶら下がっている 2 枚とも**巻き添えで固まる
  // （実際に main と frame の心拍が同じ秒に止まり、Go 側の `Eval`/`Focus` も
  // そのスレッドへ行くので以後ずっと失敗した）。**こちらから直せる場所が無い。**
  // 経緯は CLAUDE.md の「メイン画面が真っ黒になって触れなくなる」。
  //
  // pointer events は**レンダラの中で完結する**ので、そのループに入らない。
  // 引き換えに、掴んだ駒の追従描画と落とし先の判定は自前になる（下記）。
  //
  // ⚠️ **落とし先は `elementFromPoint` で引く。** ネイティブ DnD と違って
  // `dragover` が飛んでこないので、**ゴーストは必ず `pointer-events: none`**
  // にしておくこと（自分自身を掴んでしまい、どこにも落とせなくなる）。

  // 掴んだ駒の絵。透明なマスをそのまま掴むと何も見えないので、駒文字を描いた
  // 要素を作ってカーソルに追従させる。**位置は `left`/`top` に直接入れる。**
  let ghost: HTMLElement | null = null;
  const GHOST_GRAB = 20; // ゴーストの中で掴んでいる点（40px の中心）

  const makeGhost = (mark: string, black: boolean) => {
    const g = document.createElement("div");
    g.className = black ? "drag-ghost" : "drag-ghost is-white";
    g.textContent = mark;
    document.body.appendChild(g);
    ghost = g;
  };
  const dropGhost = () => {
    ghost?.remove();
    ghost = null;
  };
  const moveGhost = (x: number, y: number) => {
    if (ghost) {
      ghost.style.left = `${x - GHOST_GRAB}px`;
      ghost.style.top = `${y - GHOST_GRAB}px`;
    }
  };

  // 落とし先（盤のマス以外）。`makeDropZone` が積む。
  type Zone = { el: HTMLElement; handler: (data: Drag) => Promise<EditState> | null };
  const zones: Zone[] = [];

  // 掴んでいるもの。null なら掴んでいない。
  //
  // ⚠️ **`pointerId` を必ず見ること。** 途中で別のポインタ（2 本目の指・
  // ペンとマウス）が上がっても、掴んでいるものを取り違えないため。
  //
  // ⚠️ **`moved` が「ドラッグかクリックか」を分ける**（2026-08-18）。押した場所から
  // `DRAG_SLOP` 動くまではクリックのつもりとして扱う（＝掴んだまま連続で置く操作の
  // 入口）。**離した時点でしか決まらない**ので、ゴーストも `is-dragging` も
  // 動き始めてから出す（クリックのたびに駒の絵がちらつかないように）。
  let drag: {
    data: Drag;
    src: HTMLElement;
    pointerId: number;
    x0: number;
    y0: number;
    mark: string;
    black: boolean;
    moved: boolean;
  } | null = null;

  // クリックとドラッグを分ける距離。**手数で分けないこと** —— 押したまま
  // 数 px 揺れるのは普通なので、そこで拾うと連続配置が始められない。
  const DRAG_SLOP = 4;

  // ---- 掴んだまま連続で置く（2026-08-18）------------------------------------
  //
  // **「足りない駒」をクリックすると掴んだ状態になり、盤のマスを押すたびに置く。**
  // 歩が 5 枚足りないときにドラッグを 5 回やるのが一番だるかった。掴みっぱなしなら
  // **「掴みに戻る」手間が消える**（行き先は毎回違うマスなので、一括では置けない）。
  //
  // ⚠️ **ドラッグは今までどおり残すこと。** 1 枚だけ動かすならドラッグのほうが速い。
  // ⚠️ **掴んでいることが画面に出ていること**（チップの強調 + カーソルに付いてくる
  // 駒の絵）—— モードは見えていないと「なぜ駒が増えるのか」が分からなくなる。
  let armed: { piece: number; letter: string } | null = null;
  // 最後に見たポインタの位置（掴んだ瞬間にゴーストを出す場所）。
  let lastX = 0;
  let lastY = 0;

  const clearOver = () => {
    for (const c of cells) {
      c.classList.remove("is-over");
    }
    for (const z of zones) {
      z.el.classList.remove("is-over");
    }
  };

  // targetAt はその画面座標にある落とし先を返す。**盤のマスが優先。**
  function targetAt(x: number, y: number): { cell?: HTMLElement; zone?: Zone } {
    const el = document.elementFromPoint(x, y) as HTMLElement | null;
    if (!el) {
      return {};
    }
    const cell = el.closest<HTMLElement>(".edit-cell");
    if (cell) {
      return { cell };
    }
    for (const z of zones) {
      if (z.el.contains(el)) {
        return { zone: z };
      }
    }
    return {};
  }

  // beginDrag は掴む。**左ボタンだけ**（右クリックはマスを回す操作なので、
  // ここで拾うと `contextmenu` が来なくなる）。
  function beginDrag(e: PointerEvent, src: HTMLElement, data: Drag, mark: string, black: boolean) {
    if (!editable || e.button !== 0 || drag) {
      return;
    }
    // 既定のテキスト選択・画像ドラッグを止める（**ネイティブ DnD の入口を塞ぐ**）。
    e.preventDefault();
    // ⚠️ **ここではまだ「掴んだ」ことにしない**（クリックかもしれない）。
    // ゴーストと `is-dragging` は `DRAG_SLOP` 動いてから出す。
    drag = {
      data, src, pointerId: e.pointerId,
      x0: e.clientX, y0: e.clientY, mark, black, moved: false,
    };
    // ⚠️ **捕まえておくこと。** 盤の外・ウィンドウの外まで引くのが普通の操作なので、
    // 取らないと途中で追従が切れる（**離した瞬間を取りこぼすと掴んだまま残る**）。
    try {
      src.setPointerCapture(e.pointerId);
    } catch {
      // 捕まえられなくても window で拾えるので続ける。
    }
  }

  // endDrag は掴んでいる状態を畳む。**落としたときも、やめたときも通る。**
  function endDrag() {
    drag?.src.classList.remove("is-dragging");
    const keepGhost = !!armed && !drag?.moved;
    drag = null;
    // ⚠️ **掴んだままのときはゴーストを消さないこと**（クリックのたびに
    // 駒の絵が消えて出ると、掴んでいるのかどうかが読めない）。
    if (!keepGhost) {
      dropGhost();
    }
    clearOver();
  }

  // setArmed は掴んでいるものを決める（null で離す）。
  //
  // ⚠️ **見た目もここで 1 か所にまとめる**（チップの強調とカーソルの駒の絵）。
  // 散らすと、離したのに絵だけ残るような食い違いが起きる。
  function setArmed(next: { piece: number; letter: string } | null) {
    armed = next;
    dropGhost();
    paintArmed();
    if (!armed) {
      clearOver();
    }
    if (armed) {
      // 掴んだ駒は**先手の駒として**置く（「足りない駒」からのドラッグと同じ規約）。
      makeGhost(armed.letter, true);
      moveGhost(lastX, lastY);
    }
  }

  // paintArmed はチップの強調を付け直す。
  // ⚠️ **描き直しのたびに呼ぶこと**（`renderMissing` がチップを作り直すので、
  // 呼ばないと掴んでいるのに強調だけ消える）。
  function paintArmed() {
    for (const chip of missingChips.querySelectorAll<HTMLElement>(".stock-chip")) {
      chip.classList.toggle("is-armed", !!armed && Number(chip.dataset.piece) === armed.piece);
    }
  }

  // toggleArmed は「足りない駒」のクリック。**同じ駒をもう一度押したら離す。**
  //
  // ⚠️ **残り枚数で掴めるかを決めないこと。** 足りている駒でも掴める
  // （そうしないと、余計な駒を外す前に正しい駒を置けずに詰む）。
  function toggleArmed(piece: number) {
    if (!editable) {
      return;
    }
    if (armed?.piece === piece) {
      setArmed(null);
      return;
    }
    const s = state?.inventory?.find((x) => x.piece === piece);
    if (!s) {
      return;
    }
    setArmed({ piece, letter: s.letter });
  }

  // dropArmedAt は掴んでいるものを、その座標の落とし先へ置く。
  //
  // ⚠️ **落とし先はドラッグと同じ経路を通すこと**（`dropOnCell` / zone の handler）。
  // クリック用に別の分岐を書くと、ドラッグと結果が食い違う。
  function dropArmedAt(x: number, y: number) {
    if (!armed || !editable) {
      return;
    }
    const data: Drag = { from: "stock", piece: armed.piece, black: true };
    const t = targetAt(x, y);
    if (t.cell) {
      dropOnCell(t.cell, data);
      return;
    }
    if (t.zone) {
      const p = t.zone.handler(data);
      if (p) {
        void apply(() => p);
        return;
      }
    }
    // 盤でも駒台でもないところを押した = 置くのをやめる（掴んだものを戻す）。
    setArmed(null);
  }

  // 捕まえた（`setPointerCapture`）あとも window までは上がってくるので、
  // 移動と離しは 1 か所で受ける。**掴む側の要素ごとに書かないこと。**
  window.addEventListener("pointermove", (e) => {
    // 掴んでいるものの絵はカーソルに付いてくる。**掴んでいない間も位置は覚える**
    // （チップを押した瞬間に、その場所へゴーストを出すため）。
    lastX = e.clientX;
    lastY = e.clientY;
    if (armed && !drag) {
      moveGhost(e.clientX, e.clientY);
      // 押したらどこへ入るかを光らせる（ドラッグ中と同じ見え方に揃える）。
      const t = targetAt(e.clientX, e.clientY);
      clearOver();
      (t.cell ?? t.zone?.el)?.classList.add("is-over");
      return;
    }
    if (!drag || e.pointerId !== drag.pointerId) {
      return;
    }
    if (!drag.moved) {
      if (Math.hypot(e.clientX - drag.x0, e.clientY - drag.y0) < DRAG_SLOP) {
        return; // まだクリックかもしれない
      }
      drag.moved = true;
      // ⚠️ **引き始めたら掴んでいたものは離す。** 2 つ同時に持っていることに
      // なると、落とした駒がどちらなのか分からなくなる。
      setArmed(null);
      drag.src.classList.add("is-dragging");
      makeGhost(drag.mark, drag.black);
    }
    moveGhost(e.clientX, e.clientY);
    const t = targetAt(e.clientX, e.clientY);
    clearOver();
    // ⚠️ **落とし先が無いときは「足りない駒」を光らせる。** 盤外へ捨てると
    // そこへ入る（`discard`）ので、**行き先が見えていないと消えたように見える**。
    (t.cell ?? t.zone?.el ?? (discardable(drag.data) ? missing : null))?.classList.add("is-over");
  });

  window.addEventListener("pointerup", (e) => {
    lastX = e.clientX;
    lastY = e.clientY;
    if (!drag || e.pointerId !== drag.pointerId) {
      // 掴んだものを置く経路。**空マスを押したときはここに来る**
      // （空マスではドラッグが始まらないので `drag` が無い）。
      dropArmedAt(e.clientX, e.clientY);
      return;
    }
    const data = drag.data;
    const moved = drag.moved;
    // ⚠️ **先に畳んでから落とす。** 落とし先の判定に `elementFromPoint` を使うので、
    // ゴーストが残っていると自分を拾う。
    endDrag();
    if (!moved) {
      // ここまで来たら**クリック**。「足りない駒」なら掴む/離す、
      // それ以外なら（掴んでいれば）そこへ置く。
      if (data.from === "stock") {
        toggleArmed(data.piece);
      } else {
        dropArmedAt(e.clientX, e.clientY);
      }
      return;
    }
    const t = targetAt(e.clientX, e.clientY);
    if (t.cell) {
      dropOnCell(t.cell, data);
      return;
    }
    // ⚠️ **落とし先が無ければ「盤外に捨てる」**（2026-08-18）。以前は何も
    // 起きなかったので、駒を消すのに**「足りない駒」の枠までドラッグする**
    // 必要があった —— 消すのは訂正で一番よく使う操作（認識は余計な駒を作る）
    // なので、放り出す動作でできるほうが速い。**行き先は「足りない駒」と同じ**
    // （先後を決めずに外すだけ）なので、間違えても掴み直せば戻る。
    const p = t.zone ? t.zone.handler(data) : discard(data);
    if (p) {
      void apply(() => p);
    }
  });

  // 掴んだまま取り消せること。**盤外で離すのは「捨てる」**なので、
  // 取り消したいときは Esc（とポインタの中断）。同じ入口へ寄せてある。
  window.addEventListener("pointercancel", (e) => {
    if (drag && e.pointerId === drag.pointerId) {
      endDrag();
    }
  });
  window.addEventListener("keydown", (e) => {
    if (e.key !== "Escape") {
      return;
    }
    if (drag) {
      endDrag();
    }
    // 掴んだままの状態も Esc で離す（**やめる手段を 1 つに揃える**）。
    setArmed(null);
  });

  // dropOnCell は盤のマスへ落としたときの振り分け。
  function dropOnCell(el: HTMLElement, data: Drag) {
    if (!editable) {
      return;
    }
    const rank = Number(el.dataset.rank);
    const file = Number(el.dataset.file);
    if (data.from === "cell") {
      // 同じマスへ戻しただけなら何もしない（掴み直しの取り消し）。
      if (data.rank === rank && data.file === file) {
        return;
      }
      void apply(() => PositionService.Move(data.rank, data.file, rank, file));
    } else if (data.from === "hand") {
      // 駒台から打つ。**その側の駒台に無ければ Go 側で弾かれる。**
      void apply(() => PositionService.FromHand(rank, file, data.piece, data.black));
    } else {
      // 足りない駒からは常に不成で置く（成/不成はクリックで切り替える）。
      void apply(() => PositionService.Place(rank, file, data.piece, data.black, false));
    }
  }

  grid.addEventListener("pointerdown", (e) => {
    const el = (e.target as HTMLElement)?.closest<HTMLElement>(".edit-cell");
    if (!el || el.classList.contains("is-empty")) {
      return;
    }
    const mark = el.dataset.mark ?? "";
    beginDrag(
      e,
      el,
      { from: "cell", rank: Number(el.dataset.rank), file: Number(el.dataset.file) },
      mark,
      mark === mark.toUpperCase(),
    );
  });

  // 右クリックで 1 マスを回す（先手不成 → 先手成 → 後手不成 → 後手成 → …）。
  //
  // **先後と成/不成を左右のクリックに分けない。** マスに対してやりたいことは
  // この 4 通りしかないので、1 つの操作で回したほうが「どっちがどっちだったか」を
  // 覚えずに済む。認識は**駒の向きも "+" も外す**ので、どちらも同じ頻度で要る。
  grid.addEventListener("contextmenu", (e) => {
    // ⚠️ **掴んでいるあいだは「離す」だけ**（マスは回さない）。掴んだまま
    // 右クリックしたときにマスまで回ると、何が起きたのか読めない。
    if (armed) {
      e.preventDefault();
      setArmed(null);
      return;
    }
    const el = (e.target as HTMLElement)?.closest<HTMLElement>(".edit-cell");
    if (!editable || !el || el.classList.contains("is-empty")) {
      return;
    }
    e.preventDefault();
    void apply(() =>
      PositionService.CycleCell(Number(el.dataset.rank), Number(el.dataset.file)),
    );
  });

  // 足りない駒。**先後未決の駒のドラッグ元**であり、
  // **盤から先後を決めずに外すドロップ先**でもある。
  missing.addEventListener("pointerdown", (e) => {
    const chip = (e.target as HTMLElement)?.closest<HTMLElement>(".stock-chip");
    if (!chip) {
      return;
    }
    // 持ち主が決まっていないので、置き先の駒台が先後を決める。
    // 盤に落とされたときは先手の駒として置く（右クリックで後手に直せる）。
    // ⚠️ **視点で既定を変えないこと**（表示だけの反転が局面の中身に効いてしまう）。
    beginDrag(e, chip, { from: "stock", piece: Number(chip.dataset.piece), black: true },
      chip.textContent ?? "", true);
  });

  // discard は「足りない駒」へ戻す操作。**枠に落としても、盤外へ放り出しても同じ。**
  //
  // ⚠️ **2 か所で別々に書かないこと** —— 枠と盤外で結果が違うと、消したつもりの
  // 駒がどこへ行ったのか分からなくなる。
  function discard(data: Drag) {
    // 盤から: 先後を決めずに外す。駒台から: 持ち主を未決に戻す。
    if (data.from === "cell") {
      return PositionService.Remove(data.rank, data.file);
    }
    if (data.from === "hand") {
      return setHandDelta(data.piece, data.black, -1);
    }
    return null;
  }

  // discardable は「盤外へ放ると変化があるか」。**足りない駒から掴んだものは
  // 既にそこに居る**ので、放っても何も起きない（光らせもしない）。
  function discardable(data: Drag) {
    return data.from !== "stock";
  }

  makeDropZone(missing, discard);

  // 駒台（先手・後手）。**外すのと先後を決めるのが 1 操作**になるドロップ先。
  for (const zone of handZones) {
    const black = zone.dataset.black === "true";

    zone.addEventListener("pointerdown", (e) => {
      const chip = (e.target as HTMLElement)?.closest<HTMLElement>(".stock-chip");
      if (!chip) {
        return;
      }
      beginDrag(e, chip, { from: "hand", piece: Number(chip.dataset.piece), black },
        chip.textContent ?? "", black);
    });

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
      // 足りない駒から: この側の駒台に 1 枚足す（＝持ち主が決まる）。
      return setHandDelta(data.piece, black, +1);
    });
  }

  // makeDropZone は盤のマス以外の落とし先を登録する。
  // handler が null を返したら何もしない。
  //
  // ⚠️ **登録するだけ**（listener は付けない）。落とし先の判定は
  // `pointerup` の `targetAt` が 1 か所でやる —— ネイティブ DnD と違って
  // `dragover`/`drop` が飛んでこないので、**受け取る側では決められない**。
  function makeDropZone(el: HTMLElement, handler: (data: Drag) => Promise<EditState> | null) {
    zones.push({ el, handler });
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

  // 訂正タブの唯一の出口。**可否の判定はここでしない**（Go 側の
  // StudyService.Adopt が言う。フロントで同じ判定を書くと 2 か所に散る）。
  confirmBtn.addEventListener("click", () => onConfirm());

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

  // ⚠️ **今の値は `state` から読む**（自前で覚えない）。返ってくる `EditState` が
  // 唯一の値で、2 か所に持つと食い違う。
  nearBtn.addEventListener("click", () => {
    const next = !state?.nearWhite;
    void apply(() => PositionService.SetViewpoint(next));
  });

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
      syncLoaded();
    },
    clear() {
      state = null;
      syncLoaded();
      resetBtn.hidden = true;
      onState(null);
    },
    relayout: layoutGrid,
  };
}
