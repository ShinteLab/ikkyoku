// 押した場所に出すメニュー（右クリックのメニュー・成る/成らず・手順を消す確認など）。
//
// **見た目は OS のコンテキストメニューに揃える**（2026-10-02）—— 項目を**縦に**並べ、
// 押した場所を左上の角にして出す。以前は成る/成らずの 2 択のダイアログとして作った
// 形（ボタンを横に並べる）のまま、右クリックのメニューまでこれに乗せていたので、
// 項目が 3 つ 4 つになると横に長い帯になって読みにくかった。
//
// ⚠️ **`window.confirm` を使わないこと。** あれは画面の真ん中に出るうえ、
// 「OK / キャンセル」という**その選択とは無関係な語**でしか聞けない
// （「キャンセル＝不成」は読み取れない）。**押した場所でそのまま聞く**ほうが、
// 盤や手順から目を離さずに選べる。
//
// **例外は成る / 成らずだけ**（2026-11-25）—— 指したあとの駒の字を**横に**並べて聞く（`row` / `piece`）。
// 文字の項目ではなく駒 2 つなので、縦に揃えた理由（横に長い帯になる）が当てはまらない。
//
// ⚠️ **どこで出すものも同じ作りにしてある**（位置決め・Esc・外側クリック・初期フォーカス・
// 矢印キー）。**次に作るときもここを使うこと** —— コピーすると、外側クリックの
// タイミングのような**間違えやすいところだけが食い違う**。

// PopupItem はメニューの項目 1 つ。
export interface PopupItem {
  label: string;
  // kind は見た目。**danger は元に戻せない操作**（消す）に使う（赤い字）。
  // **primary は既定の選択肢**（太字。OS のメニューの既定項目と同じ）。
  kind?: "primary" | "danger";
  // swatch は行の頭に出す色の丸（`#rrggbb`）。**色を選ばせるときだけ。**
  //
  // ⚠️ **色だけの行にしないこと**（label は必ず出す）。色の名前が無いと、
  // 読み上げでも「今どれを選んでいるのか」でも区別が付かない。
  swatch?: string;
  // checked は**入り切りする行**（チェックの印を頭に出す）。`undefined` なら
  // ただの行。⚠️ **印の場所は入っていなくても空けておくこと** —— 詰めると
  // 押すたびに文字が横へ動き、同じ行をもう一度押すのに狙い直すことになる。
  checked?: boolean;
  // note は行の右端に添える薄い字（連続解析の秒数ごとの所要時間など）。
  // **選ぶ材料を行の中に置くためのもの**で、押した結果は label が言う。
  note?: string;
  // piece は**駒の字で選ばせるとき**の SFEN の字（"S" / "+S"。成る / 成らず。2026-11-25）。
  // 盤と同じ駒のフォントで大きく書き、label はその下に小さく添える（`row` と組で使う）。
  //
  // ⚠️ **label を消さないこと**（「成 / 不成」。成った字を知らない人の手掛かりで、読み上げの名前）。
  piece?: string;
  onPick(): void;
}

export interface PopupOptions {
  // label は読み上げ用（`aria-label`）。
  label: string;
  // title は項目の上に出す見出し（押せない 1 行。**何を選ぶのか**が項目だけで読めないとき）。
  // ⚠️ **フォーカスの巡回に入れない**（矢印キーで止まると Enter で何も起きない）。
  title?: string;
  items: PopupItem[];
  // focus は初期フォーカスを当てる項目（既定は先頭）。
  //
  // ⚠️ **消す操作では「やめる」に当てること** —— Enter の連打で消えてしまわないように。
  focus?: number;
  // row は**項目を横に並べる**（2026-11-25。駒の字で選ばせる成る / 成らずだけ）。
  // 矢印キーは左右で移る（上下でも移る）。
  //
  // ⚠️ **文字の項目には使わないこと**（2026-10-02 に縦に揃えた理由。項目が増えると横に長い帯になる）。
  row?: boolean;
  // pieceWhite は駒の字に**後手の書体の切り替え**（王/玉・馬/左馬）を当てるか。
  // ⚠️ **向きは回さない**（選ぶための字なので、盤の向きによらず正立で読ませる）。
  pieceWhite?: boolean;
  // onClose は閉じたときに呼ばれる（選んだかどうかに関わらず）。
  onClose?(): void;
}

export interface PopupHandle {
  close(): void;
}

// openPopup は (x, y) を左上の角にしてメニューを出す（右や下がはみ出すなら
// 反対側へ開く。OS のコンテキストメニューと同じ）。
//
// **選ばずに閉じたら `onPick` は呼ばれない**（誤操作の取り消し）。
export function openPopup(x: number, y: number, opts: PopupOptions): PopupHandle {
  return open({ left: x, top: y, right: x, bottom: y }, 2, false, opts);
}

// openPopupBelow は**ボタンの真下**にメニューを出す（2026-10-10。連続解析の秒数）。
// 下に全部が収まらなくても、**数行ぶん空いていれば真下に出して中でスクロール**させる
// （今の値の行が見えるところまで送る）。真下がほとんど空いていないときだけ真上に出す。
//
// ⚠️ **「収まらなければ真上」にしないこと**（2026-10-10 に実機で踏んだ）—— 秒数の一覧は
// 11 行あり、真上に丸ごと出すと一覧の上端が窓の上のほうまで伸びて、**ボタンから離れて
// 出たように見える**（押した点を渡す `openPopup` だった頃とほとんど変わらなかった）。
export function openPopupBelow(anchor: Element, opts: PopupOptions): PopupHandle {
  return open(anchor.getBoundingClientRect(), 0, true, opts);
}

// belowRows は `openPopupBelow` が真下に出すのに要る行数（これだけ見えればスクロールで足りる）。
const belowRows = 4;

interface Anchor {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

// open は anchor の外側にメニューを置く。dx は横のずらし（点のときだけ。下の d と同じ理由）。
// preferBelow は**収まらなくても真下に出す**か（`openPopupBelow`）。
function open(at: Anchor, dx: number, preferBelow: boolean, opts: PopupOptions): PopupHandle {
  const box = document.createElement("div");
  box.className = "popup-menu" + (opts.row ? " is-row" : "");
  box.setAttribute("role", "menu");
  box.setAttribute("aria-label", opts.label);

  let handle: PopupHandle;
  const buttons = opts.items.map((item) => {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "popup-btn" + (item.kind ? ` is-${item.kind}` : "") + (item.piece ? " is-piece" : "");
    b.setAttribute("role", "menuitem");
    if (item.checked !== undefined) {
      // ⚠️ **`role`/`aria-checked` も付けること**（読み上げでは印が見えない）。
      b.setAttribute("role", "menuitemcheckbox");
      b.setAttribute("aria-checked", item.checked ? "true" : "false");
      const mark = document.createElement("span");
      mark.className = "popup-check";
      mark.textContent = item.checked ? "✓" : "";
      mark.setAttribute("aria-hidden", "true");
      b.appendChild(mark);
    }
    if (item.swatch) {
      const dot = document.createElement("span");
      dot.className = "popup-swatch";
      dot.style.background = item.swatch;
      b.appendChild(dot);
    }
    if (item.piece) {
      // 駒の字と、その下に小さく label。⚠️ **字は `textContent`**（"+S" を書体のリガチャが全にする）。
      const glyph = document.createElement("span");
      glyph.className = "popup-piece" + (opts.pieceWhite ? " is-white" : "");
      glyph.textContent = item.piece;
      glyph.setAttribute("aria-hidden", "true");
      const cap = document.createElement("span");
      cap.className = "popup-piece-label";
      cap.textContent = item.label;
      b.append(glyph, cap);
    } else {
      b.appendChild(document.createTextNode(item.label));
    }
    if (item.note) {
      const note = document.createElement("span");
      note.className = "popup-note";
      note.textContent = item.note;
      b.appendChild(note);
    }
    b.addEventListener("click", () => {
      handle.close();
      item.onPick();
    });
    // ⚠️ **ホバーでフォーカスを移すこと**（OS のメニューと同じく、マウスと
    // 矢印キーで「今どれか」の印を 1 つにする）。移さないと、初期フォーカスの
    // 行とカーソルの下の行の 2 つが光って、Enter でどちらが選ばれるか読めない。
    b.addEventListener("pointermove", () => {
      if (document.activeElement !== b) {
        b.focus();
      }
    });
    return b;
  });
  if (opts.title) {
    const head = document.createElement("div");
    head.className = "popup-title";
    head.textContent = opts.title;
    box.appendChild(head);
  }
  box.append(...buttons);
  // メニューの上の右クリックで webview の既定メニューを重ねない。
  box.addEventListener("contextmenu", (e) => e.preventDefault());

  // ⚠️ **`position: fixed` で body に置く**（盤や手順の箱に入れると、はみ出した
  // ぶんが切られるうえ、スクロールで一緒に動く）。
  document.body.appendChild(box);
  const m = 8;
  // ⚠️ **押した場所の真上に項目を置かないこと**（角を 2px ずらす）。真上だと、
  // 成る/成らずのように左クリックで開くもので**ダブルクリックの 2 回目が
  // そのまま先頭の項目を押す**。
  const d = 2;
  // 縦: 下 → 上 → どちらにも収まらなければ**広いほうに出して中でスクロール**させる。
  // `openPopupBelow` は、真下に数行ぶん空いていれば上を試さずに真下でスクロールさせる。
  // ⚠️ **窓の端へ押し出さないこと**（2026-10-10）—— 押し出すと押した場所から離れて出る。
  const h = box.getBoundingClientRect().height;
  const below = window.innerHeight - m - (at.bottom + d);
  const above = at.top - d - m;
  const row = buttons[0]?.getBoundingClientRect().height ?? 0;
  // 横に並べたときは行が 1 つ（見出しの高さの見積もりは縦のときだけ要る）。
  const head = opts.row ? 0 : h - row * buttons.length;
  const roomy = preferBelow && below >= Math.min(h, head + row * belowRows);
  let top: number;
  if (h <= below) {
    top = at.bottom + d;
  } else if (!roomy && h <= above) {
    top = at.top - d - h;
  } else {
    const room = roomy ? below : Math.max(below, above, 0);
    const inner = box.clientWidth;
    box.style.maxHeight = `${room}px`;
    box.style.overflowY = "auto";
    // スクロールバーのぶん幅を足す（足さないと項目の右端が切れる。`box-sizing: border-box`）。
    const bar = inner - box.clientWidth;
    if (bar > 0) {
      box.style.width = `${box.getBoundingClientRect().width + bar}px`;
    }
    top = roomy || below >= above ? at.bottom + d : at.top - d - room;
  }
  // 横: 左端を揃え、はみ出すなら右端を揃える（押した場所を右の角にする）。
  const w = box.getBoundingClientRect().width;
  let left = at.left + dx;
  if (left + w > window.innerWidth - m) {
    left = at.right - dx - w;
  }
  left = Math.min(Math.max(left, m), window.innerWidth - w - m);
  box.style.left = `${left}px`;
  box.style.top = `${Math.max(top, m)}px`;
  buttons[opts.focus ?? 0]?.focus();

  const onKey = (e: KeyboardEvent) => {
    if (e.key === "Escape") {
      handle.close();
      return;
    }
    // 矢印キーで項目を移る（端から端へ回る）。Enter / Space はボタンそのものが受ける。
    // 横に並べたときは左右でも移る。
    const keys: Record<string, number> = opts.row
      ? { ArrowDown: 1, ArrowUp: -1, ArrowRight: 1, ArrowLeft: -1 }
      : { ArrowDown: 1, ArrowUp: -1 };
    const step: number | undefined = keys[e.key];
    if (step === undefined && e.key !== "Home" && e.key !== "End") {
      return;
    }
    e.preventDefault();
    if (buttons.length === 0) {
      return;
    }
    const now = buttons.indexOf(document.activeElement as HTMLButtonElement);
    const n = buttons.length;
    const next =
      e.key === "Home" ? 0
      : e.key === "End" ? n - 1
      : now < 0 ? (step! > 0 ? 0 : n - 1)
      : (now + step! + n) % n;
    buttons[next].focus();
  };
  const onOutside = (e: Event) => {
    if (!box.contains(e.target as Node | null)) {
      handle.close();
    }
  };
  // ウィンドウを離れた・大きさが変わったら閉じる（OS のメニューと同じ。
  // 置いた場所が意味を失うため）。
  const onLeave = () => handle.close();
  // ⚠️ **今のイベントが終わってから外側の監視を始める。** 同じフレームで付けると、
  // メニューを開いたこのクリックがそのまま「外側」として届いて即座に閉じる。
  const timer = window.setTimeout(() => {
    document.addEventListener("pointerdown", onOutside, true);
  }, 0);
  document.addEventListener("keydown", onKey, true);
  window.addEventListener("blur", onLeave);
  window.addEventListener("resize", onLeave);

  let closed = false;
  handle = {
    close: () => {
      if (closed) {
        return;
      }
      closed = true;
      window.clearTimeout(timer);
      document.removeEventListener("pointerdown", onOutside, true);
      document.removeEventListener("keydown", onKey, true);
      window.removeEventListener("blur", onLeave);
      window.removeEventListener("resize", onLeave);
      box.remove();
      opts.onClose?.();
    },
  };
  return handle;
}
