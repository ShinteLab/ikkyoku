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
  onPick(): void;
}

export interface PopupOptions {
  // label は読み上げ用（`aria-label`）。
  label: string;
  items: PopupItem[];
  // focus は初期フォーカスを当てる項目（既定は先頭）。
  //
  // ⚠️ **消す操作では「やめる」に当てること** —— Enter の連打で消えてしまわないように。
  focus?: number;
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
  const box = document.createElement("div");
  box.className = "popup-menu";
  box.setAttribute("role", "menu");
  box.setAttribute("aria-label", opts.label);

  let handle: PopupHandle;
  const buttons = opts.items.map((item) => {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "popup-btn" + (item.kind ? ` is-${item.kind}` : "");
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
    b.appendChild(document.createTextNode(item.label));
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
  box.append(...buttons);
  // メニューの上の右クリックで webview の既定メニューを重ねない。
  box.addEventListener("contextmenu", (e) => e.preventDefault());

  // ⚠️ **`position: fixed` で body に置く**（盤や手順の箱に入れると、はみ出した
  // ぶんが切られるうえ、スクロールで一緒に動く）。
  document.body.appendChild(box);
  const r = box.getBoundingClientRect();
  const m = 8;
  // ⚠️ **押した場所の真上に項目を置かないこと**（角を 2px ずらす）。真上だと、
  // 成る/成らずのように左クリックで開くもので**ダブルクリックの 2 回目が
  // そのまま先頭の項目を押す**。
  const d = 2;
  const place = (at: number, size: number, limit: number) => {
    let p = at + d;
    if (p + size > limit - m) {
      // はみ出すなら反対側へ開く（押した場所を右下の角にする）。
      p = at - d - size;
    }
    return Math.min(Math.max(p, m), limit - size - m);
  };
  box.style.left = `${place(x, r.width, window.innerWidth)}px`;
  box.style.top = `${place(y, r.height, window.innerHeight)}px`;
  buttons[opts.focus ?? 0]?.focus();

  const onKey = (e: KeyboardEvent) => {
    if (e.key === "Escape") {
      handle.close();
      return;
    }
    // 矢印キーで項目を移る（端から端へ回る）。Enter / Space はボタンそのものが受ける。
    const step = { ArrowDown: 1, ArrowUp: -1 }[e.key];
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
