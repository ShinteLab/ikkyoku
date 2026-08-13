// 押した場所に出す小さなダイアログ（成る/成らず・手順を消す・手順を追加）。
//
// ⚠️ **`window.confirm` を使わないこと。** あれは画面の真ん中に出るうえ、
// 「OK / キャンセル」という**その選択とは無関係な語**でしか聞けない
// （「キャンセル＝不成」は読み取れない）。**押した場所でそのまま聞く**ほうが、
// 盤や手順から目を離さずに選べる。
//
// ⚠️ **3 か所で同じ作りにしてある**（位置決め・Esc・外側クリック・初期フォーカス）。
// **4 つめを作るときもここを使うこと** —— コピーすると、外側クリックの
// タイミングのような**間違えやすいところだけが食い違う**。

// PopupItem はダイアログの選択肢 1 つ。
export interface PopupItem {
  label: string;
  // kind は見た目。**danger は元に戻せない操作**（消す）に使う。
  kind?: "primary" | "danger";
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

// openPopup は (x, y) の右下に小さなダイアログを出す。
//
// **選ばずに閉じたら `onPick` は呼ばれない**（誤操作の取り消し）。
export function openPopup(x: number, y: number, opts: PopupOptions): PopupHandle {
  const box = document.createElement("div");
  box.className = "popup-menu";
  box.setAttribute("role", "dialog");
  box.setAttribute("aria-label", opts.label);

  let handle: PopupHandle;
  const buttons = opts.items.map((item) => {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "popup-btn" + (item.kind ? ` is-${item.kind}` : "");
    b.textContent = item.label;
    b.addEventListener("click", () => {
      handle.close();
      item.onPick();
    });
    return b;
  });
  box.append(...buttons);

  // ⚠️ **`position: fixed` で body に置く**（盤や手順の箱に入れると、はみ出した
  // ぶんが切られるうえ、スクロールで一緒に動く）。画面から出るなら内側へ寄せる。
  document.body.appendChild(box);
  const r = box.getBoundingClientRect();
  const m = 8;
  box.style.left = `${Math.min(Math.max(x + 12, m), window.innerWidth - r.width - m)}px`;
  box.style.top = `${Math.min(Math.max(y + 12, m), window.innerHeight - r.height - m)}px`;
  buttons[opts.focus ?? 0]?.focus();

  const onKey = (e: KeyboardEvent) => {
    if (e.key === "Escape") {
      handle.close();
    }
  };
  const onOutside = (e: Event) => {
    if (!box.contains(e.target as Node | null)) {
      handle.close();
    }
  };
  // ⚠️ **今のイベントが終わってから外側の監視を始める。** 同じフレームで付けると、
  // ダイアログを開いたこのクリックがそのまま「外側」として届いて即座に閉じる。
  const timer = window.setTimeout(() => {
    document.addEventListener("pointerdown", onOutside, true);
  }, 0);
  document.addEventListener("keydown", onKey, true);

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
      box.remove();
      opts.onClose?.();
    },
  };
  return handle;
}
