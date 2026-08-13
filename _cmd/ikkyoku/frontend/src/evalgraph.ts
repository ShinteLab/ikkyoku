// 評価値グラフ（解析タブ。2026-08-12）。
//
// **手順の 1 手ごとに「エンジンが出した最善手の評価値」を折れ線にする。**
// 「次善手を選んだらどう転ぶか」を辿るのがこのアプリの中心なので、
// **辿った結果がどう転んだか**が一目で分かる面が要る。
//
// ⚠️ **エンジンごとに別の折れ線。** 評価値はエンジンが違えば食い違うのが普通で、
// **その食い違いこそ見たいもの**（CLAUDE.md「エンジンをまたいで結果を合成しない」）。
// **平均も多数決も取らない。**
//
// **点は Go 側（StudyService.Evals）が持つ。** ここは描くだけで、
// **フロントに評価値を溜めないこと** —— 手順を切ったときにどこまで捨てるかを
// 知っているのは手順を持っている側だけで、2 か所に持つとどちらが本当か
// 分からなくなる（局面の写しを持たないのと同じ方針）。
import type { EvalGraph, EvalPoint } from "../bindings/ikkyoku-app/models";

const NS = "http://www.w3.org/2000/svg";

// 縦軸の頭打ち（センチポーン）。**詰みはこの端に置く。**
//
// 評価値は 1 手で数千動くことがあるので、頭打ちにしないと**普段の 100〜500 の
// 変化が潰れて読めなくなる**。⚠️ **自作 `engine` の評価値の絶対値は当てにならない**
// （PST が手作り・未調整）ので、ここを細かく合わせようとしないこと。
//
// ⚠️ **2000 まで詰めないこと**（2026-08-12 に 2000 → 3000）。**2000〜3000 は
// まだ「どれくらい優勢か」に意味がある帯**で、そこで頭打ちにすると
// 優勢がどこまで広がったのかが読めなくなる。目盛りは CAP の半分ごとなので、
// 3000 だと ±1500 の線が入る。
const CAP = 3000;

// 折れ線の色（登場順）。**エンジンの数だけ回す。**
// 評価値の色（青＝先手 / 橙＝後手）とは別の役割なので、そちらと同じ色を先頭に
// 置かない —— 「線の色 = どのエンジンか」であって、形勢の色ではない。
const COLORS = ["#7ddc8a", "#e0a3ff", "#ffd166", "#8ecae6", "#ff8fa3"];

const PAD = { top: 8, right: 10, bottom: 15, left: 36 };

export interface EvalGraphHandle {
  // render は Go から返ってきたグラフを描く（null なら空にする）。
  render(graph: EvalGraph | null): void;
  // relayout は測り直して描き直す。
  //
  // ⚠️ **解析タブを開いた瞬間に呼ぶこと。** 大きさは `clientWidth` で測るが、
  // **`display: none` の中では 0 になる**（訂正グリッドの `getScreenCTM` と同じ
  // 落とし穴）。呼ばないとグラフが出ないか、前回の大きさのまま残る。
  relayout(): void;
}

export interface EvalGraphOptions {
  // host はグラフを描く箱。**高さは CSS で固定しておくこと**（中身で伸び縮みすると
  // 盤ごと画面が上下に跳ねる）。
  host: HTMLElement;
  // range は横軸の決め方（"all" = 全て / "custom" = 自由入力）。
  //
  // ⚠️ **既定は「全て」**（＝**指した手が全部見えている状態**）。
  // 「1 から」ではない —— 根は初期局面とは限らないので、撮った 40 手目の局面から
  // 始めたなら 40 手目から始まるのが「全て」。
  range: HTMLSelectElement;
  // from / to は自由入力の範囲（手数）。**既定は 1-150。**
  //
  // 少ししか指していなくても「1-150 の中のどこに居るか」で読みたいことがあるので、
  // **全体表示とは別に、手で範囲を決められる口が要る。**
  from: HTMLInputElement;
  to: HTMLInputElement;
  // fields は自由入力の欄を包む要素（"全て" のときは隠す）。
  fields: HTMLElement;
  // legend はどの色がどのエンジンかを出す場所。
  //
  // ⚠️ **グラフの中に描かないこと。** 高さ 116px の絵に文字を重ねると
  // 目盛りの線と重なるうえ、**そのぶん折れ線の描ける範囲が狭くなる**。
  legend: HTMLElement;
  // readout は指した位置の読み上げ（手数・手・各エンジンの評価値）。
  readout: HTMLElement;
  // onSeek はグラフを押したときの行き先（根からの手数）。
  //
  // **手順のリストと同じ「戻って見る」操作**（手順は消さない）。
  // onSeek はその局面へ戻す。**引数は手順ツリーの節点 id**（手数ではない）。
  onSeek(id: number): void;
}

export function mountEvalGraph(opts: EvalGraphOptions): EvalGraphHandle {
  const { host, range, from, to, fields, legend, readout, onSeek } = opts;

  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("class", "eval-graph-svg");
  host.appendChild(svg);

  let graph: EvalGraph | null = null;
  // 今ホバーしている手数（null なら離れている）。**画面だけの状態。**
  let hover: number | null = null;
  // ドラッグで範囲を選んでいる最中の両端（null なら掴んでいない）。
  //
  // ⚠️ **クリック（その局面へ戻る）と同じボタンで始まる**ので、
  // **どちらだったかは離したときに決める**（下の finishDrag）。
  let dragA: number | null = null;
  let dragB: number | null = null;
  // 掴んだ位置の画面座標。**動いていなければクリック**として扱う。
  let dragX = 0;

  const el = (name: string, attrs: Record<string, string | number>): SVGElement => {
    const node = document.createElementNS(NS, name);
    for (const [k, v] of Object.entries(attrs)) {
      node.setAttribute(k, String(v));
    }
    return node;
  };

  // 横軸の範囲。
  //
  // **「全て」は根の手数 〜 最終手**（＝ 指した手が全部見えている状態）。
  // ⚠️ **1 から始まるとは限らない** —— 根は初期局面とは限らないので、撮った
  // 40 手目の局面から始めたなら 40 手目から始まる。
  //
  // **「自由入力」は書いたとおりの手数**。少ししか指していなくても
  // 「1-150 の中のどこに居るか」で読めるようにするための口で、
  // **ドラッグで絞ったときの行き先でもある**（絞った値がそのまま欄に入る）。
  const domain = (): { x0: number; x1: number } => {
    if (range.value === "custom") {
      const a = Math.max(0, Math.floor(Number(from.value) || 0));
      const b = Math.floor(Number(to.value) || 0);
      // ⚠️ **逆さや潰れた範囲でも描けること。** 打っている途中の欄は普通に
      // 壊れた値になる（"15" と打ちたい途中の "1"）ので、**弾かずに丸める。**
      return { x0: a, x1: Math.max(b, a + 1) };
    }
    const first = graph?.first ?? 0;
    const last = graph?.last ?? 0;
    // 手が少ないうちに軸が詰まりすぎないよう、最低 10 手ぶんは取る。
    return { x0: first, x1: Math.max(last, first + 10) };
  };

  // 自由入力の欄は「自由入力」のときだけ出す（"全て" のときは意味が無い）。
  const syncFields = () => {
    fields.hidden = range.value !== "custom";
  };

  // useRange はドラッグで選んだ範囲を横軸にする。
  //
  // ⚠️ **選んだ値を欄に入れて「自由入力」に切り替える**こと。範囲を 2 か所
  // （欄とドラッグ）に持つと、どちらが今の範囲か分からなくなる。
  // **見えている数字がそのまま今の範囲**なら、続けて手で直せる。
  const useRange = (a: number, b: number) => {
    range.value = "custom";
    from.value = String(Math.min(a, b));
    to.value = String(Math.max(a, b));
    syncFields();
  };

  // 評価値 → 縦位置。**詰みは端に置く**（数として大きすぎるので潰れる）。
  const value = (p: EvalPoint): number => {
    if (p.mate !== 0) {
      return p.mate > 0 ? CAP : -CAP;
    }
    return Math.max(-CAP, Math.min(CAP, p.cp));
  };

  const draw = () => {
    const w = host.clientWidth;
    const h = host.clientHeight;
    svg.replaceChildren();
    // ⚠️ **測れないときは何も描かない**（タブが隠れている・まだ配置前）。
    // 0 幅の viewBox を入れると、次に開いたときに潰れたまま残る。
    if (w <= 0 || h <= 0) {
      return;
    }
    svg.setAttribute("viewBox", `0 0 ${w} ${h}`);

    const { x0, x1 } = domain();
    const span = Math.max(x1 - x0, 1);
    const left = PAD.left;
    const right = w - PAD.right;
    const top = PAD.top;
    const bottom = h - PAD.bottom;
    const mid = (top + bottom) / 2;
    const half = (bottom - top) / 2;
    const px = (n: number) => left + ((n - x0) / span) * (right - left);
    const py = (v: number) => mid - (v / CAP) * half;

    // ---- 目盛り ------------------------------------------------------------
    //
    // 0 の線だけ明るくする（**形勢が入れ替わる線**なので、そこだけは読めること）。
    for (const v of [CAP, CAP / 2, 0, -CAP / 2, -CAP]) {
      svg.appendChild(
        el("line", {
          x1: left, x2: right, y1: py(v), y2: py(v),
          class: v === 0 ? "eval-axis is-zero" : "eval-axis",
        }),
      );
      if (v !== 0) {
        svg.appendChild(
          el("text", { x: left - 4, y: py(v) + 3, class: "eval-tick", "text-anchor": "end" }),
        ).textContent = v > 0 ? `+${v}` : String(v);
      }
    }
    // 縦の目盛り。**間隔は範囲に合わせる**（150 手で 5 手刻みにすると読めない）。
    const step = span <= 30 ? 5 : span <= 80 ? 10 : span <= 200 ? 20 : 50;
    for (let n = Math.ceil(x0 / step) * step; n <= x1; n += step) {
      svg.appendChild(el("line", { x1: px(n), x2: px(n), y1: top, y2: bottom, class: "eval-grid" }));
      svg.appendChild(
        el("text", { x: px(n), y: h - 4, class: "eval-tick", "text-anchor": "middle" }),
      ).textContent = String(n);
    }

    // ---- 分かれなかったほうの線（枝に居るとき）------------------------------
    //
    // ⚠️ **薄く・破線で、折れ線より先に描くこと**（下に敷く）。主役は今辿っている
    // 線で、これは**比べる相手**。**枝を選んだ結果がどう転んだかは、元の線と
    // 並べて初めて読める。**
    //
    // ⚠️ **点は打たない**（ホバーの相手も作らない）。今の経路の点と重なると、
    // どちらの手順の値を読んでいるのか分からなくなる。
    const refs = (graph?.ref ?? []).filter((s) => (s.points ?? []).length > 0);
    const engineIndex = new Map<string, number>();
    (graph?.series ?? []).forEach((s, i) => engineIndex.set(s.engineId, i));
    for (const s of refs) {
      const color = COLORS[(engineIndex.get(s.engineId) ?? 0) % COLORS.length];
      const pts = (s.points ?? []).filter((p) => p.number >= x0 && p.number <= x1);
      if (pts.length < 2) {
        continue;
      }
      svg.appendChild(el("polyline", {
        points: pts.map((p) => `${px(p.number)},${py(value(p))}`).join(" "),
        class: "eval-line is-ref",
        stroke: color,
      }));
    }

    // ---- 枝が分かれた手 ----------------------------------------------------
    //
    // **どこから枝に入ったのか**が分からないと、2 本の線の意味が読めない。
    // ⚠️ **「今見ている手」の線とは別の見た目にすること**（あちらはカーソル）。
    const fork = graph?.fork ?? 0;
    if (fork > 0 && fork >= x0 && fork <= x1) {
      svg.appendChild(el("line", {
        x1: px(fork), x2: px(fork), y1: top, y2: bottom, class: "eval-fork",
      }));
      const tag = el("text", { x: px(fork) + 3, y: top + 9, class: "eval-fork-tag" });
      tag.textContent = "分岐";
      svg.appendChild(tag);
    }

    const series = (graph?.series ?? []).filter((s) => (s.points ?? []).length > 0);
    if (series.length === 0) {
      svg.appendChild(
        el("text", { x: (left + right) / 2, y: mid - 6, class: "eval-empty", "text-anchor": "middle" }),
      ).textContent = "解析すると、ここに評価値が並びます";
    }

    // ---- 折れ線 ------------------------------------------------------------
    series.forEach((s, i) => {
      const color = COLORS[i % COLORS.length];
      const pts = (s.points ?? []).filter((p) => p.number >= x0 && p.number <= x1);
      if (pts.length === 0) {
        return;
      }
      if (pts.length > 1) {
        svg.appendChild(
          el("polyline", {
            points: pts.map((p) => `${px(p.number)},${py(value(p))}`).join(" "),
            class: "eval-line",
            stroke: color,
          }),
        );
      }
      // 点は詰まりすぎない範囲でだけ描く（150 手ぶん打つと線が潰れる）。
      if (span <= 80 || pts.length <= 40) {
        for (const p of pts) {
          const dot = el("circle", { cx: px(p.number), cy: py(value(p)), r: 2.2, fill: color });
          const title = document.createElementNS(NS, "title");
          title.textContent = `${p.number}手目 ${p.move}　${s.label}: ${p.label}（深さ ${p.depth}）`;
          dot.appendChild(title);
          svg.appendChild(dot);
        }
      }
    });

    // ---- 今見ている手 ------------------------------------------------------
    //
    // **どこを見ているかが分からないと、折れ線と盤が結びつかない。**
    const cur = graph?.number ?? 0;
    if (graph && cur >= x0 && cur <= x1) {
      svg.appendChild(el("line", { x1: px(cur), x2: px(cur), y1: top, y2: bottom, class: "eval-cursor" }));
    }
    if (hover !== null && dragA === null && hover >= x0 && hover <= x1) {
      svg.appendChild(el("line", { x1: px(hover), x2: px(hover), y1: top, y2: bottom, class: "eval-hover" }));
    }

    // ---- ドラッグで選んでいる範囲 ------------------------------------------
    //
    // **どこからどこまでを掴んでいるか**が見えないと、離すまで結果が分からない。
    if (dragA !== null && dragB !== null && dragA !== dragB) {
      const a = px(Math.min(dragA, dragB));
      const b = px(Math.max(dragA, dragB));
      svg.appendChild(el("rect", {
        x: a, y: top, width: Math.max(b - a, 1), height: bottom - top, class: "eval-select",
      }));
    }

    // ---- 凡例 --------------------------------------------------------------
    //
    // **どの線がどのエンジンか**が分からないと、食い違いを読む意味が無い。
    // ⚠️ **見出しの行に出す**（グラフの中に重ねない。上の EvalGraphOptions）。
    legend.replaceChildren();
    series.forEach((s, i) => {
      const chip = document.createElement("span");
      chip.className = "eval-legend";
      chip.style.color = COLORS[i % COLORS.length];
      chip.textContent = s.label || s.engineId;
      legend.appendChild(chip);
    });
    // 枝に居るあいだは、薄い線が何なのかを 1 語で出す（凡例の末尾）。
    if (refs.length > 0) {
      const note = document.createElement("span");
      note.className = "eval-legend is-ref";
      note.textContent = "分岐前の手順";
      legend.appendChild(note);
    }
  };

  // 押した/触った位置の手数。
  //
  // ⚠️ **範囲の外は捨てずに丸めること。** ドラッグは箱の端まで引いてから離す
  // 操作が普通にあるので、外へ出た瞬間に「掴んでいる位置が無い」状態にすると
  // **端まで含めた範囲を選べない。**
  const numberAt = (ev: MouseEvent): number | null => {
    const w = host.clientWidth;
    if (w <= 0 || !graph) {
      return null;
    }
    const { x0, x1 } = domain();
    const left = PAD.left;
    const rightEdge = w - PAD.right;
    const x = ev.clientX - host.getBoundingClientRect().left;
    const n = Math.round(x0 + ((x - left) / Math.max(rightEdge - left, 1)) * (x1 - x0));
    return Math.max(x0, Math.min(x1, n));
  };

  // 触っている手数の中身を 1 行で出す。**ツールチップだけにしない**
  // （点の上にぴったり乗せないと出ないので、線を目で追いながらは読めない）。
  const showReadout = (n: number | null) => {
    if (n === null || !graph) {
      readout.textContent = "";
      return;
    }
    const parts: string[] = [`${n}手目`];
    let move = "";
    for (const s of graph.series ?? []) {
      const p = (s.points ?? []).find((q) => q.number === n);
      if (!p) {
        continue;
      }
      move = move || p.move;
      parts.push(`${s.label || s.engineId} ${p.label}`);
    }
    readout.textContent = move ? `${parts[0]} ${move}　${parts.slice(1).join(" / ")}`
      : parts.join(" / ");
  };

  // seek はその手数の局面へ戻す（**手順は消さない**。手順のチップと同じ操作）。
  // **手順の外を押しても何もしない。**
  //
  // ⚠️ **`onSeek` に渡すのは節点の id**（手数ではない）。枝が入ってからは
  // 「手数 → 局面」が一意に決まらないので、**今の経路の id を Go 側から
  // 受け取って引く**（`EvalGraph.ids`）。**手数から作らないこと。**
  const seek = (n: number) => {
    if (!graph) {
      return;
    }
    const ply = n - (graph.first ?? 0);
    const id = (graph.ids ?? [])[ply];
    if (ply < 0 || id === undefined) {
      return;
    }
    onSeek(id);
  };

  // ⚠️ **クリックとドラッグは同じボタンで始まる**ので、**どちらだったかは
  // 離したときに決める**。動いていなければ「その局面へ戻る」、横に動いていれば
  // 「その範囲に絞る」。
  const finishDrag = (ev: MouseEvent) => {
    if (dragA === null) {
      return;
    }
    const a = dragA;
    const b = dragB ?? a;
    dragA = null;
    dragB = null;
    // 動いた量で分ける。**手数の差だけで見ないこと** —— 軸が広いと、
    // 数十 px 動かしても手数が変わらないことがある（＝絞れない）。
    const moved = Math.abs(ev.clientX - dragX) > 4;
    if (moved && Math.abs(b - a) >= 2) {
      useRange(a, b);
    } else if (!moved) {
      seek(a);
    }
    hover = null;
    showReadout(null);
    draw();
  };

  host.addEventListener("mousedown", (ev) => {
    const n = numberAt(ev);
    if (n === null) {
      return;
    }
    // ⚠️ **既定の選択（テキストのドラッグ）を止める。** 止めないと、
    // 横に引いたときに見出しごと青く反転して掴んでいる範囲が見えなくなる。
    ev.preventDefault();
    dragA = n;
    dragB = n;
    dragX = ev.clientX;
    draw();
  });
  host.addEventListener("mousemove", (ev) => {
    const n = numberAt(ev);
    if (dragA !== null) {
      if (n === dragB) {
        return;
      }
      dragB = n;
      showReadout(null);
      // 掴んでいるあいだは「これから絞る範囲」を出す（離す前に確かめられる）。
      if (dragA !== null && dragB !== null) {
        readout.textContent = `${Math.min(dragA, dragB)}〜${Math.max(dragA, dragB)}手目に絞る`;
      }
      draw();
      return;
    }
    // ⚠️ **手数が変わったときだけ描き直すこと。** mousemove のたびに描くと、
    // 同じ絵を毎フレーム作り直すことになる（1 手ぶん動かないと絵は変わらない）。
    if (n === hover) {
      return;
    }
    hover = n;
    showReadout(hover);
    draw();
  });
  host.addEventListener("mouseleave", () => {
    // ⚠️ **掴んでいる最中は離脱で消さないこと**（箱の外まで引いてから離す操作が
    // 普通にある）。終わらせるのは window の mouseup。
    if (dragA !== null) {
      return;
    }
    hover = null;
    showReadout(null);
    draw();
  });
  // ⚠️ **離すのは箱の外のこともある**ので window で受ける（host だけだと、
  // 端まで引いて離したときに掴んだままになる）。
  window.addEventListener("mouseup", finishDrag);

  range.addEventListener("change", () => {
    syncFields();
    draw();
  });
  // 打っている途中でも追随させる（確定を待たない。壊れた値は domain が丸める）。
  for (const input of [from, to]) {
    input.addEventListener("input", draw);
  }
  syncFields();
  // 箱の大きさが変わったら測り直す（ウィンドウのリサイズもここで拾える）。
  new ResizeObserver(() => draw()).observe(host);

  return {
    render(next: EvalGraph | null) {
      graph = next;
      hover = null;
      // ⚠️ **掴んでいる状態を持ち越さないこと**（局面が変わったのに、前の絵の
      // 上で選び始めた範囲が残る）。**範囲そのもの（欄の値）は残す** ——
      // 絞って見ているところに 1 手指したら全体に戻る、では使えない。
      dragA = null;
      dragB = null;
      showReadout(null);
      draw();
    },
    relayout: draw,
  };
}
