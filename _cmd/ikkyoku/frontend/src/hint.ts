// 設定の説明をホバーで出す（2026-09-12）。
//
// **設定タブの項目の説明は、以前は全部その場に書いてあった。** 1 つずつは要る文章
// なのだが、並ぶと**画面が説明で埋まって、設定そのものが探しにくくなる**
// （折りたたみを入れても、開いた中はやはり説明のほうが長かった）。そこで
// **項目にホバーしたときだけ出す**形にした。
//
// ⚠️ **文章を消したのではない。** マークアップは今までどおり項目の隣に置いたまま、
// **見えなくしているだけ**（`.setting-note.is-hint`）。ここがその中身を読んで
// 吹き出しに写す。**読み上げにも残す**ため `display: none` ではなく面積を潰す
// 隠し方にしてあり、`aria-describedby` で項目に結び付けている。
//
// ⚠️ **吹き出しは `position: fixed` で body に置く。** 設定タブは
// `overflow: auto` なので、項目の中に絶対配置すると**下のほうの項目で吹き出しが
// 切り落とされる**。画面の座標で置いて、入り切らなければ上へ回す。
//
// ⚠️ **「?」の印を消さないこと。** ホバーで出るものは、出ることが分からないと
// 誰も触らない。印があるから「ここに説明がある」と読める。

// 吹き出しは 1 つだけ使い回す（同時に 2 つ出ることは無い）。
let bubble: HTMLDivElement | null = null;
let timer = 0;
// 今どの項目のものを出しているか（同じ項目へ二度入ったときに作り直さないため）。
let current: HTMLElement | null = null;

const ensureBubble = (): HTMLDivElement => {
  if (!bubble) {
    bubble = document.createElement("div");
    bubble.className = "hint-bubble";
    bubble.setAttribute("role", "tooltip");
    bubble.hidden = true;
    document.body.appendChild(bubble);
  }
  return bubble;
};

const hide = () => {
  window.clearTimeout(timer);
  current = null;
  if (bubble) {
    bubble.hidden = true;
  }
};

// 画面の座標で置く。**既定は項目の下**で、入り切らなければ上へ回す。
// 横は項目の左端に揃え、右にはみ出すぶんだけ戻す。
const place = (trigger: HTMLElement) => {
  const b = ensureBubble();
  const r = trigger.getBoundingClientRect();
  const gap = 6;
  const margin = 8;
  b.style.maxWidth = `${Math.min(420, window.innerWidth - margin * 2)}px`;
  // 高さを測るために先に出す（hidden のままでは 0 になる）。
  b.hidden = false;
  const bw = b.offsetWidth;
  const bh = b.offsetHeight;
  let left = r.left;
  if (left + bw > window.innerWidth - margin) {
    left = window.innerWidth - margin - bw;
  }
  let top = r.bottom + gap;
  if (top + bh > window.innerHeight - margin) {
    const above = r.top - gap - bh;
    top = above >= margin ? above : Math.max(margin, window.innerHeight - margin - bh);
  }
  b.style.left = `${Math.max(margin, left)}px`;
  b.style.top = `${top}px`;
};

const show = (trigger: HTMLElement, note: HTMLElement) => {
  if (current === trigger) {
    return;
  }
  current = trigger;
  const b = ensureBubble();
  b.innerHTML = note.innerHTML;
  place(trigger);
};

// 設定タブの中の `data-hint` を持つ項目に、ホバーで出る説明を結び付ける。
//
// ⚠️ **対になる文章は `data-hint` が指す id の要素**（`getElementById`）。
// **見つからなかったら印も付けない** —— 「?」があるのに何も出ないほうが悪い。
export const attachHints = (root: ParentNode) => {
  for (const trigger of root.querySelectorAll<HTMLElement>("[data-hint]")) {
    const id = trigger.dataset.hint!;
    const note = document.getElementById(id);
    if (!note) {
      continue;
    }
    // ⚠️ **`aria-describedby` は「操作するもの」に付ける。** 見出しの span は
    // フォーカスを受けないので、そこに付けても読み上げられない。
    const owner =
      trigger.closest("label.setting")?.querySelector<HTMLElement>("input, select") ??
      trigger.closest("summary") ??
      trigger.closest(".setting-group")?.querySelector<HTMLElement>("input, select") ??
      trigger;
    owner.setAttribute("aria-describedby", id);
    // ホバーを拾う範囲は**行ごと**（見出しの字だけだと狙いにくい）。
    // 吹き出しの位置は見出しに合わせる。
    const zone: HTMLElement =
      trigger.closest("label.setting") ?? trigger.closest("summary") ?? trigger;
    // 「ここに説明がある」ことが読めるように印を付ける。
    // ⚠️ **文字を足すのではなく要素で足す** —— textContent を書き換える処理
    // （フォント名など）があるので、混ぜると印まで消える。
    if (!trigger.querySelector(".hint-mark")) {
      const mark = document.createElement("span");
      mark.className = "hint-mark";
      mark.setAttribute("aria-hidden", "true");
      mark.textContent = "?";
      trigger.appendChild(mark);
    }
    // **少し待ってから出す。** 項目の上を通り過ぎるだけで出ると煩わしい。
    zone.addEventListener("pointerenter", () => {
      window.clearTimeout(timer);
      timer = window.setTimeout(() => show(trigger, note), 220);
    });
    zone.addEventListener("pointerleave", hide);
    // キーボードで辿ったときも出す（**待たない**。自分で合わせて止めた操作なので）。
    owner.addEventListener("focus", () => show(trigger, note));
    owner.addEventListener("blur", hide);
  }
};

// ⚠️ **スクロールとタブの切り替えで消すこと。** 出したまま下へ流れると、
// 関係の無い項目の横に説明が残る。
export const hideHint = hide;

document.addEventListener("scroll", hide, true);
window.addEventListener("resize", hide);
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape") {
    hide();
  }
});
