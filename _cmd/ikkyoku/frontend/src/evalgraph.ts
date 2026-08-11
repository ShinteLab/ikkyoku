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
const CAP = 2000;

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
  // range は横軸の範囲の指定（"auto" か手数）。
  range: HTMLSelectElement;
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
  onSeek(ply: number): void;
}

export function mountEvalGraph(opts: EvalGraphOptions): EvalGraphHandle {
  const { host, range, legend, readout, onSeek } = opts;

  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("class", "eval-graph-svg");
  host.appendChild(svg);

  let graph: EvalGraph | null = null;
  // 今ホバーしている手数（null なら離れている）。**画面だけの状態。**
  let hover: number | null = null;

  const el = (name: string, attrs: Record<string, string | number>): SVGElement => {
    const node = document.createElementNS(NS, name);
    for (const [k, v] of Object.entries(attrs)) {
      node.setAttribute(k, String(v));
    }
    return node;
  };

  // 横軸の範囲。
  //
  // **自動は「根の手数 〜 最終手」**（＝ 1〜手数）。固定はいつも 0 から数えた
  // 絶対の手数で、**根が中盤の局面でも軸は動かない**（中継の棋譜と突き合わせる
  // ときに、軸が毎回変わると読み比べられない）。
  const domain = (): { x0: number; x1: number } => {
    const fixed = Number(range.value) || 0;
    if (fixed > 0) {
      return { x0: 0, x1: fixed };
    }
    const first = graph?.first ?? 0;
    const last = graph?.last ?? 0;
    // 手が少ないうちに軸が詰まりすぎないよう、最低 20 手ぶんは取る。
    return { x0: first, x1: Math.max(last, first + 20) };
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
    if (hover !== null && hover >= x0 && hover <= x1) {
      svg.appendChild(el("line", { x1: px(hover), x2: px(hover), y1: top, y2: bottom, class: "eval-hover" }));
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
  };

  // 押した/触った位置の手数。範囲の外なら null。
  const numberAt = (ev: MouseEvent): number | null => {
    const w = host.clientWidth;
    if (w <= 0 || !graph) {
      return null;
    }
    const { x0, x1 } = domain();
    const left = PAD.left;
    const rightEdge = w - PAD.right;
    const x = ev.clientX - host.getBoundingClientRect().left;
    if (x < left - 4 || x > rightEdge + 4) {
      return null;
    }
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

  host.addEventListener("mousemove", (ev) => {
    const n = numberAt(ev);
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
    hover = null;
    showReadout(null);
    draw();
  });
  // ⚠️ **押すとその局面へ戻る**（手順のリストのチップと同じ「戻って見る」操作）。
  // 手順は消さないので、進め直せる。**範囲の外や手順の外を押しても何もしない。**
  host.addEventListener("click", (ev) => {
    const n = numberAt(ev);
    if (n === null || !graph) {
      return;
    }
    const ply = n - (graph.first ?? 0);
    if (ply < 0 || ply > (graph.last ?? 0) - (graph.first ?? 0)) {
      return;
    }
    onSeek(ply);
  });

  range.addEventListener("change", draw);
  // 箱の大きさが変わったら測り直す（ウィンドウのリサイズもここで拾える）。
  new ResizeObserver(() => draw()).observe(host);

  return {
    render(next: EvalGraph | null) {
      graph = next;
      hover = null;
      showReadout(null);
      draw();
    },
    relayout: draw,
  };
}
