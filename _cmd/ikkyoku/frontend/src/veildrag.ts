// 幕（`.veil`）の箱をドラッグで動かせるようにする（2026-09-14）。
//
// **幕は真ん中に出す。邪魔ならユーザーが避ける。** 幕を薄くしてあるのは
// **下で盤と評価値グラフが 1 手ずつ進むのが見えている**ため（＝止めるかどうかの
// 判断材料）なのに、箱がその真上に載ると肝心のものが読めない。
// 2026-09-09 には**左の真ん中へ決め打ちで寄せて**いたが、
// ⚠️ **どこが邪魔かは窓の形と「今何を見ているか」で変わる**（盤なのか、
// 評価値グラフなのか）。**寄せ先を決め打ちにすると、そこが邪魔な人には直せない。**
//
// ⚠️ **3 つの窓が同じ幕を持っている**（メイン画面・切り離した候補手／手順・
// 評価値グラフ）。**写して 3 か所に置かないこと** —— ポインタの取り逃がしや
// 収め直しのような**間違えやすいところだけが食い違う**（`popup.ts` と同じ考え方）。

// enableVeilDrag は箱を掴んで動かせるようにする。
//
// veil は基準（`position: absolute; inset: 0`）、box はその中の箱。
//
// ⚠️ **置いた場所は覚えるが、設定ファイルには持たない**（その場かぎり。列の幅や
// グラフの高さと同じ扱い）。**覚えないと、幕が出るたびに避け直すことになる**ので
// 窓が生きているあいだは残す。
export function enableVeilDrag(veil: HTMLElement, box: HTMLElement): void {
  // 幕の左上から見た置き場所（px）。**null なら「まだ動かしていない」＝真ん中。**
  let at: { x: number; y: number } | null = null;

  // clamp は幕の中へ収め直す。
  //
  // ⚠️ **窓の大きさが変わったときにも呼ぶこと**（下の ResizeObserver）。
  // 呼ばないと、**窓を狭めた拍子に箱が幕の外へ出て、止める口ごと画面から消える**
  // （出口の無い幕にしない、という約束がここで崩れる）。
  const clamp = () => {
    if (!at) {
      return;
    }
    const v = veil.getBoundingClientRect();
    const b = box.getBoundingClientRect();
    // ⚠️ **畳んでいるあいだは何もしないこと。** 幕は `hidden`（`display: none`）で
    // 消えるので、**畳んだ瞬間にも ResizeObserver が 0×0 で飛んでくる。**
    // そこで収め直すと上限が 0 になり、**覚えていた place が左上へ潰れる**
    // （次に出したときに、避けたはずの箱が左上に戻っている）。
    if (v.width === 0 || v.height === 0) {
      return;
    }
    // ⚠️ **箱が幕より大きいときは 0 に倒す**（負の上限で右上へ吹き飛ばさない）。
    const maxX = Math.max(0, v.width - b.width);
    const maxY = Math.max(0, v.height - b.height);
    at = {
      x: Math.min(Math.max(at.x, 0), maxX),
      y: Math.min(Math.max(at.y, 0), maxY),
    };
    box.style.left = `${at.x}px`;
    box.style.top = `${at.y}px`;
  };

  box.addEventListener("pointerdown", (e) => {
    // 左ボタンだけ（右クリックは掴む操作ではない）。
    if (e.button !== 0) {
      return;
    }
    // ⚠️ **ボタンの上からは掴ませないこと。** ここは**止める口**で、
    // 押すつもりの 1px の動きが「動かした」になると**止められなくなる**。
    if ((e.target as HTMLElement | null)?.closest("button")) {
      return;
    }
    const v = veil.getBoundingClientRect();
    const b = box.getBoundingClientRect();
    // ⚠️ **掴んだ瞬間の見た目の位置から始めること。** ここまでは flex が真ん中に
    // 置いているので、`is-placed` を付けた途端に左上へ飛ぶ（**掴んだ場所と箱が
    // 離れる**）。先に今の位置を写してから絶対位置へ移す。
    at = { x: b.left - v.left, y: b.top - v.top };
    box.classList.add("is-placed");
    clamp();
    // 掴んだ点と箱の左上のずれ。**これが無いと、箱の左上がカーソルへ飛ぶ。**
    const dx = e.clientX - b.left;
    const dy = e.clientY - b.top;

    const move = (ev: PointerEvent) => {
      // ⚠️ **幕の矩形は毎回取り直すこと**（掴んでいる最中にスプリットバーで
      // 幕の大きさが変わることはないが、**窓の移動やスケーリングでずれる**）。
      const now = veil.getBoundingClientRect();
      at = { x: ev.clientX - dx - now.left, y: ev.clientY - dy - now.top };
      clamp();
    };
    const up = (ev: PointerEvent) => {
      box.releasePointerCapture(ev.pointerId);
      box.removeEventListener("pointermove", move);
      box.removeEventListener("pointerup", up);
      box.removeEventListener("pointercancel", up);
    };
    // ⚠️ **ポインタを掴むこと**（`setPointerCapture`）。掴まないと、素早く引いた
    // ときにカーソルが箱から外れて**そこで動きが止まる**（幕の上は別の要素）。
    box.setPointerCapture(e.pointerId);
    box.addEventListener("pointermove", move);
    box.addEventListener("pointerup", up);
    box.addEventListener("pointercancel", up);
    // ⚠️ **文字選択を始めさせない**（掴んで引くと文の途中から青くなる）。
    e.preventDefault();
  });

  // ⚠️ **幕の大きさが変わったら収め直す。** 幕を出した瞬間（`hidden` を外した
  // とき）もここを通るので、**前に置いた場所が今の窓に収まらなければ寄せ直される**。
  new ResizeObserver(clamp).observe(veil);
}
