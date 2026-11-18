// 押した場所で 1 行の文字を聞く（2026-11-19。指摘の理由。手順リストの「指摘する…」・棋譜タブの詳細）。
//
// 出し方は `popup.ts` に揃える（押した場所を左上の角にして、はみ出すなら反対側へ・Esc と外側クリックでやめる）。
// ⚠️ **`<dialog>` の `showModal` で出すこと** —— 棋譜タブの詳細はモーダルなので、body に置いた
// `position: fixed` の箱ではその下に隠れて押せない（モーダルの外は inert になる）。
// ⚠️ **`window.prompt` を使わないこと**（`window.confirm` と同じ理由。画面の真ん中に出る）。

export interface AskTextOptions {
  // label は確定のボタンの字と読み上げ用（`aria-label`）。
  label: string;
  placeholder?: string;
  // value は最初から入れておく文字。
  value?: string;
}

// askText は 1 行を聞いて、確定なら前後の空白を落とした文字、やめたら null を返す。
// ⚠️ **空では確定できない**（ボタンも Enter も効かない）。
export function askText(x: number, y: number, opts: AskTextOptions): Promise<string | null> {
  return new Promise((resolve) => {
    const dlg = document.createElement("dialog");
    dlg.className = "ask-text";
    dlg.setAttribute("aria-label", opts.label);

    const input = document.createElement("input");
    input.type = "text";
    input.className = "ask-text-input";
    input.spellcheck = false;
    input.autocomplete = "off";
    input.placeholder = opts.placeholder ?? "";
    input.value = opts.value ?? "";

    const ok = document.createElement("button");
    ok.type = "button";
    ok.className = "ghost-btn is-primary";
    ok.textContent = opts.label;

    dlg.append(input, ok);
    document.body.appendChild(dlg);

    let result: string | null = null;
    const sync = () => {
      ok.disabled = input.value.trim() === "";
    };
    const accept = () => {
      const v = input.value.trim();
      if (v === "") return;
      result = v;
      dlg.close();
    };
    sync();
    input.addEventListener("input", sync);
    input.addEventListener("keydown", (e) => {
      // ⚠️ **変換中の Enter は確定であって決定ではない**（`isComposing`）。
      if (e.key === "Enter" && !e.isComposing) {
        e.preventDefault();
        accept();
      }
    });
    ok.addEventListener("click", accept);
    // 外側（::backdrop）を押したらやめる。押した点が箱の外なら dialog 自身に届く。
    dlg.addEventListener("pointerdown", (e) => {
      if (e.target !== dlg) return;
      const r = dlg.getBoundingClientRect();
      if (e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom) {
        dlg.close();
      }
    });
    dlg.addEventListener("contextmenu", (e) => e.preventDefault());
    // Esc は `<dialog>` が自前で閉じる（close が来る）。
    dlg.addEventListener("close", () => {
      dlg.remove();
      resolve(result);
    });

    dlg.showModal();
    // 位置は出してから測る（`popup.ts` と同じく角を 2px ずらし、はみ出すなら反対側へ）。
    const m = 8;
    const d = 2;
    const { width: w, height: h } = dlg.getBoundingClientRect();
    let left = x + d;
    if (left + w > window.innerWidth - m) left = x - d - w;
    let top = y + d;
    if (top + h > window.innerHeight - m) top = y - d - h;
    dlg.style.left = `${Math.min(Math.max(left, m), window.innerWidth - w - m)}px`;
    dlg.style.top = `${Math.min(Math.max(top, m), window.innerHeight - h - m)}px`;
    input.focus();
    input.select();
  });
}
