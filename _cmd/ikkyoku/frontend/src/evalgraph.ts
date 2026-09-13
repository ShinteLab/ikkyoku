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
import type { EvalGraph, EvalPoint } from "../bindings/github.com/ShinteLab/ikkyoku/app/models";

const NS = "http://www.w3.org/2000/svg";

// 縦軸の頭打ち（センチポーン）。**詰みはこの端に置く。**
//
// 評価値は 1 手で数千動くことがあるので、頭打ちにしないと**普段の 100〜500 の
// 変化が潰れて読めなくなる**。⚠️ **自作 `engine` の評価値の絶対値は当てにならない**
// （PST が手作り・未調整）ので、ここを細かく合わせようとしないこと。
//
// ⚠️ **2000 まで詰めないこと**（2026-08-12 に 2000 → 3000）。**2000〜3000 は
// まだ「どれくらい優勢か」に意味がある帯**で、そこで頭打ちにすると
// 優勢がどこまで広がったのかが読めなくなる。
const CAP = 3000;

// 横線の刻み（2026-08-14。以前は `CAP / 2` ＝ 1500 だった）。**評価値は 1000 単位で
// 語られる**ので、目盛りもそこに合わせる。⚠️ **CAP を変えたらここも見直すこと**
// —— CAP を上げたまま刻みが細かいと、線だらけになって折れ線が読めなくなる。
const TICK_STEP = 1000;

// 0 と ±CAP を含む、`step` ごとの目盛りの値（上から下へ）。
// ⚠️ **端（±CAP）は刻みで割り切れなくても必ず入れる** —— そこが頭打ちの線なので、
// 無いと「これ以上は潰れている」ことが読めない。
const ticks = (cap: number, step: number): number[] => {
  const out: number[] = [cap];
  for (let v = Math.floor(cap / step) * step; v > -cap; v -= step) {
    if (v < cap) {
      out.push(v);
    }
  }
  out.push(-cap);
  return out;
};

// ---- 縦軸（評価値 / 勝率。2026-09-13）------------------------------------
//
// **同じ点を 2 通りの縦軸で読む。** 評価値は「どれくらい差が付いたか」、勝率は
// 「その差がどれくらい勝ちに効くか」で、**同じ +500 でも局面によって受け取り方が
// 違う**（序盤の +500 と終盤の +500）。切り替えの口があれば、**辿った枝が
// どう転んだか**を両方の読み方で見られる。
//
// ⚠️ **点は 1 種類**（`EvalPoint` が評価値も勝率も持っている）。**軸を変えても
// 記録は 1 つも変わらない** —— 変わるのは**どちらを縦に取るか**だけ。
//
// ⚠️ **勝率をここで計算しないこと。** 式もポナンザ定数も Go 側（`analyze.WinRate`）に
// あり、点の `winRate` はその結果。**フロントに 2 つ目の式を作らない**（勝率バーと
// 食い違う）。
// ⚠️ **綴りは Go 側の `ikkyoku.EvalAxis*` と揃えること**（設定に入る文字列）。
// 片方だけ変えると、保存された軸が読めずに既定へ戻る。
export type EvalMode = "eval" | "scaled" | "winrate";

// 縦軸 1 本ぶんの決まりごと。
//
// **位置は -1（下端）〜 +1（上端）に正規化して返す。** こうしておくと、
// 目盛りも折れ線もカーソルも**軸の中身を知らずに描ける**（軸を足しても
// 描画側を触らない）。
interface Axis {
  // at は点の縦位置（-1〜+1。**+ が先手**）。
  at(p: EvalPoint): number;
  // ticks は横線（`at` と同じ正規化値）。`zero` は**形勢が入れ替わる線**。
  //
  // `minor` は**細かい刻み**（圧縮の軸の 100 単位）。⚠️ **線は全部引くが、
  // 文字は入るぶんだけ**（下の `placeLabels`）—— 116px の箱では 100 刻みが
  // 5px 間隔になるので、全部に文字を付けると読めなくなる。
  // `pri` は**文字を出す優先順**（小さいほど先。既定 1）。⚠️ **箱が低いときに
  // 何を残すかを決めるのは軸**（`placeLabels` は順に詰めるだけ）—— 圧縮の軸なら
  // **折れ点（±500 / ±3000）が最優先**で、端（±30000）はその次。
  ticks: { at: number; text: string; zero: boolean; minor?: boolean; pri?: number }[];
  // label は読み上げ・ツールチップに出す値（**その軸の読み方で**）。
  label(p: EvalPoint): string;
}

const clamp = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, v));

// 評価値の軸（センチポーン。**±CAP で頭打ち**）。
// ---- 折れた目盛り（`scaled`。2026-09-13）----------------------------------
//
// **プロの対局は 100 手くらい ±500 の中で動く。** そこが一番読みたいのに、
// 等間隔（±3000）では**上下 20px 弱に潰れて**いた。かといって上限を詰めると
// 大差の局面が端に貼り付くだけで、**評価値は詰みに向かって 30000 まで伸びる。**
//
// そこで**折れ点を 2 つ置いて、帯ごとに縮尺を変える**:
//
//	0〜±500      … 上下半分（**競っている帯。ここを一番広く。100 単位で目盛り**）
//	±500〜±3000  … そこから 90% まで（**「まぁ勝ちだな」の帯。1000 単位**）
//	±3000〜±30000 … 残り 10%（**詰み・必至の帯。畳んでよい**）
//
// ⚠️ **これは素の評価値グラフの代わりではない。** 等間隔の目盛りには
// 「傾きがそのまま点差の動き」という読み方があり、畳んだ軸はそれを捨てている。
// **両方残すこと**（トグルで選ぶ）。
//
// ⚠️ **目盛りの数字は評価値のまま**（±500 / ±3000 / ±30000）。読み替えが
// 要らないのがこの軸の取り柄なので、**％や無名の刻みにしないこと。**
//
// ⚠️ **折れ点を動かすなら目盛りも動かすこと。** 折れ点に線が引かれていないと、
// **同じ 1cm が場所によって違う点差を指している**ことが画面から読めない。
const KNEE = 500;
const KNEE_AT = 0.5;
// 「まぁ勝ちだな」の帯の上端。⚠️ **9 割のあたりに置くこと** —— ここから上は
// 「勝ったかどうか」しか読まない帯なので、1 割あれば足りる。
const WIN = 3000;
const WIN_AT = 0.9;
// 端（**詰み・必至**）。⚠️ **ここで頭打ち** —— 自作 `engine` の詰みスコアは
// 1<<20 まで伸びるが、そこは `Mate` として端に乗るので気にしなくてよい。
const MATE = 30000;

// scaledAt は評価値 → 0〜1（絶対値のぶん）。**折れ点で縮尺が変わる。**
const scaledAt = (cp: number): number => {
  const a = Math.abs(cp);
  if (a <= KNEE) {
    return (a / KNEE) * KNEE_AT;
  }
  if (a <= WIN) {
    return KNEE_AT + ((a - KNEE) / (WIN - KNEE)) * (WIN_AT - KNEE_AT);
  }
  return WIN_AT + (Math.min(a, MATE) - WIN) / (MATE - WIN) * (1 - WIN_AT);
};

// scaledTicks は圧縮の軸の横線（上から下へ）。
//
// ⚠️ **`minor`（100 単位）にも線は引く。** 文字が入らないぶんは `placeLabels` が
// 間引くが、**線そのものは残す** —— 競っている帯がどれくらいの幅なのかが、
// 線の間隔として見えていること自体が目盛りの役目。
const scaledTicks = (): Axis["ticks"] => {
  // 大きいほう（上）から: ±30000 → ±3000 → ±2000 → ±1000 → ±500 → 100 単位 → 0。
  //
  // ⚠️ **`pri` は「箱が低いとき何を残すか」。** 折れ点（±500 / ±3000）が
  // 最優先で、端（±30000）はその次 —— ドックした 116px の箱では ±3000 と
  // ±30000 が 5px しか離れておらず、**どちらか 1 つしか文字を置けない。**
  const half: { v: number; minor?: boolean; pri: number }[] = [
    { v: MATE, pri: 2 },
    { v: WIN, pri: 0 },
    { v: 2000, pri: 1 },
    { v: 1000, pri: 1 },
    { v: KNEE, pri: 0 },
    { v: 400, minor: true, pri: 1 },
    { v: 300, minor: true, pri: 1 },
    { v: 200, minor: true, pri: 1 },
    { v: 100, minor: true, pri: 1 },
  ];
  const out: Axis["ticks"] = [];
  for (const t of half) {
    out.push({ at: scaledAt(t.v), text: `+${t.v}`, zero: false, minor: t.minor, pri: t.pri });
  }
  out.push({ at: 0, text: "0", zero: true });
  for (const t of [...half].reverse()) {
    out.push({ at: -scaledAt(t.v), text: `-${t.v}`, zero: false, minor: t.minor, pri: t.pri });
  }
  return out;
};

const scaledAxis: Axis = {
  at: (p) =>
    p.mate !== 0 ? (p.mate > 0 ? 1 : -1) : Math.sign(p.cp) * scaledAt(p.cp),
  // ⚠️ **折れ点には必ず線を引く**（±500 / ±3000）。そこで縮尺が変わることが
  // 見えていないと、**傾きを点差の動きとして読み違える。**
  //
  // ⚠️ **競っている帯は 100 単位**（±100〜±400 が `minor`）。ここを読むための
  // 軸なので、**目盛りが 500 刻みでは折れ線がどれだけ動いたのか読めない。**
  // その上は 1000 単位（±1000 / ±2000）で、端が ±30000。
  ticks: scaledTicks(),
  // ⚠️ **読み上げは評価値のまま**（軸を畳んでも数字は畳まない）。
  label: (p) => p.label,
};

const evalAxis: Axis = {
  at: (p) => (p.mate !== 0 ? (p.mate > 0 ? 1 : -1) : clamp(p.cp, -CAP, CAP) / CAP),
  ticks: ticks(CAP, TICK_STEP).map((v) => ({
    at: v / CAP,
    text: v > 0 ? `+${v}` : String(v),
    zero: v === 0,
  })),
  label: (p) => p.label,
};

// 勝率の軸（**先手の勝率** 0〜100%）。
//
// ⚠️ **真ん中は 50%**（＝評価値の 0 と同じ「形勢が入れ替わる線」）。
// ⚠️ **頭打ちは要らない** —— 0〜1 に収まっている値なので、**詰みも端に
// 自然に乗る**（`analyze.WinRate` が 1.0 / 0.0 に振り切る）。
const rateAxis: Axis = {
  at: (p) => (clamp(p.winRate, 0, 1) - 0.5) * 2,
  ticks: [100, 75, 50, 25, 0].map((v) => ({
    at: (v - 50) / 50,
    text: `${v}%`,
    zero: v === 50,
  })),
  // ⚠️ **評価値も添えること。** 勝率だけだと「何手目で何が起きたか」を
  // 読み筋や候補手（評価値で出ている）と突き合わせられない。
  label: (p) => `${Math.round(clamp(p.winRate, 0, 1) * 100)}%（${p.label}）`,
};

// 目盛りの文字を置ける最小の間隔（px）。**9px の字 + 息継ぎ。**
const LABEL_GAP = 11;

// placeLabels は**文字を出す目盛りを選ぶ**（2026-09-13）。
//
// **線は全部引くが、文字は入るぶんだけ。** 圧縮の軸の 100 単位は、ドックした
// 116px の箱では 5px 間隔になるので、全部に文字を付けると重なって読めない
// （切り離して窓を高くすれば、そのぶん多く出る）。
//
// ⚠️ **太い目盛り（`minor` でないもの）を先に置くこと。** ±500 / ±3000 /
// ±30000 は**折れ点そのもの**なので、細かい刻みに押し出されてはいけない。
// ⚠️ **0 には文字を出さない**（手数の目盛りが乗っている）。
const placeLabels = (
  ticks: Axis["ticks"],
  y: (at: number) => number,
): Set<Axis["ticks"][number]> => {
  const out = new Set<Axis["ticks"][number]>();
  const used: number[] = [];
  // 0 の行は文字を出さないが、**場所は取る**（手数の目盛りが乗っているので、
  // そこに評価値の文字が寄ると重なる）。
  for (const t of ticks) {
    if (t.zero) {
      used.push(y(t.at));
    }
  }
  // ⚠️ **大事な目盛りから順に置くこと。** 上から順に詰めると、**箱が低いときに
  // 端（±30000）が ±3000 を押し出す**（5px しか離れていない）。
  const order = ticks
    .filter((t) => !t.zero)
    .sort((a, b) => Number(!!a.minor) - Number(!!b.minor) || (a.pri ?? 1) - (b.pri ?? 1));
  for (const t of order) {
    if (used.every((u) => Math.abs(u - y(t.at)) >= LABEL_GAP)) {
      out.add(t);
      used.push(y(t.at));
    }
  }
  return out;
};

// 描画の余白。⚠️ **下は狭い**（2026-08-14）—— 手数の目盛りを**真ん中の 0 の線の上**へ
// 移したので、下に文字を置く場所を取らなくてよくなった（そのぶん折れ線が縦に広がる）。
// 目盛りを下に戻すなら、ここも 15px 前後に戻すこと（戻さないと文字が切れる）。
const PAD = { top: 8, right: 10, bottom: 6, left: 36 };

export interface EvalGraphHandle {
  // render は Go から返ってきたグラフを描く（null なら空にする）。
  render(graph: EvalGraph | null): void;
  // setMode は縦軸を入れ替える（**設定から入れ直す口**。2026-09-13）。
  //
  // ⚠️ **`onMode` は呼ばない**（これは「設定がこうなっている」を反映するだけで、
  // 人が押したわけではない）。呼ぶと、書き戻しがもう一周する。
  setMode(mode: EvalMode): void;
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
  // all は「全て」のチェック（2026-09-10 に select からこちらへ変えた）。
  //
  // ⚠️ **既定は入っている**（＝**指した手が全部見えている状態**）。
  // **外すと下の from / to がそのまま横軸になる。**
  // 「1 から」ではない —— 根は初期局面とは限らないので、撮った 40 手目の局面から
  // 始めたなら 40 手目から始まるのが「全て」。
  all: HTMLInputElement;
  // from / to は横軸の範囲（手数）。**既定は 1-150。**
  //
  // 少ししか指していなくても「1-150 の中のどこに居るか」で読みたいことがあるので、
  // **全体表示とは別に、手で範囲を決められる口が要る。**
  //
  // ⚠️ **「全て」のあいだも隠さないこと**（2026-09-10）。以前は欄ごと消していたが、
  // **今どの範囲を見ているのかが画面から読めなくなる**。「全て」のあいだは
  // **触れなくして（disabled）、実際の範囲を書き込む**（＝見えている数字が今の範囲）。
  from: HTMLInputElement;
  to: HTMLInputElement;
  // legend はどの色がどのエンジンかを出す場所。
  //
  // ⚠️ **グラフの中に描かないこと。** 高さ 116px の絵に文字を重ねると
  // 目盛りの線と重なるうえ、**そのぶん折れ線の描ける範囲が狭くなる**。
  // mode は縦軸の切り替え（**評価値 / 勝率**。2026-09-13）。
  //
  // 中の `button[data-mode]` を押すと切り替わる（`data-mode` は "eval" / "winrate"）。
  // ⚠️ **今どちらを見ているかが読めること**（`aria-pressed` と `.is-on`）——
  // 縦軸が変わると**折れ線の形そのものが変わる**ので、どちらの軸で見ているのかが
  // 分からないと数字を読み違える。
  mode: HTMLElement;
  legend: HTMLElement;
  // readout は指した位置の読み上げ（手数・手・各エンジンの評価値）。
  readout: HTMLElement;
  // colorOf は折れ線の色を引く（**エンジンごと**）。
  //
  // ⚠️ **色をここで決めないこと**（2026-08-14 に「登場順の色」をやめた）。
  // 色は**エンジンの登録に紐づく設定**で、解決するのは Go 側。ここで
  // 並び順から決めると、**エンジンを 1 つ外しただけで残りの線の色が入れ替わり**、
  // 前に見ていた線と同じ色が別のエンジンを指す。
  colorOf(engineId: string): string;
  // onSeek はグラフを押したときの行き先（根からの手数）。
  //
  // **手順のリストと同じ「戻って見る」操作**（手順は消さない）。
  // onSeek はその局面へ戻す。**引数は手順ツリーの節点 id**（手数ではない）。
  onSeek(id: number): void;
  // onMode は人が縦軸を切り替えたときに呼ぶ（**保存する側**）。
  //
  // ⚠️ **保存を待たずに描き替えること**（押した手応えが要る）。保存に失敗しても
  // 画面はその軸のままでよい —— 次の起動で戻るだけで、今見えているものは正しい。
  onMode?(mode: EvalMode): void;
}

export function mountEvalGraph(opts: EvalGraphOptions): EvalGraphHandle {
  const { host, all, from, to, mode, legend, readout, colorOf, onSeek } = opts;

  // 今の縦軸（**画面だけの状態ではない** —— 設定に保存する。既定は評価値）。
  let axisMode: EvalMode = "eval";
  const axis = (): Axis =>
    axisMode === "winrate" ? rateAxis : axisMode === "scaled" ? scaledAxis : evalAxis;

  // 見出しのボタンに今の軸を映す。⚠️ **色だけで示さないこと**（`aria-pressed`）。
  const syncMode = () => {
    for (const b of mode.querySelectorAll<HTMLButtonElement>("button[data-mode]")) {
      const on = b.dataset.mode === axisMode;
      b.classList.toggle("is-on", on);
      b.setAttribute("aria-pressed", String(on));
    }
  };

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
    if (!all.checked) {
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

  // 欄は**常に出したまま**、「全て」のあいだだけ触れなくする（2026-09-10）。
  //
  // ⚠️ **隠さないこと。** 以前は「全て」のときに欄ごと消していたが、
  // **今どの範囲を見ているのかが画面から読めなくなる。** 実際の範囲は下の
  // `reflect` が書き込むので、「全て」を外した瞬間から**見えていた数字が
  // そのまま自由入力の初期値**になり、続けて手で直せる。
  const syncFields = () => {
    from.disabled = all.checked;
    to.disabled = all.checked;
  };

  // reflect は「全て」のあいだ、実際の横軸の範囲を欄に書き戻す。
  // ⚠️ **自由入力のときは書き換えないこと**（打っている途中の値を潰す）。
  const reflect = (x0: number, x1: number) => {
    if (!all.checked) {
      return;
    }
    from.value = String(x0);
    to.value = String(x1);
  };

  // useRange はドラッグで選んだ範囲を横軸にする。
  //
  // ⚠️ **選んだ値を欄に入れて「自由入力」に切り替える**こと。範囲を 2 か所
  // （欄とドラッグ）に持つと、どちらが今の範囲か分からなくなる。
  // **見えている数字がそのまま今の範囲**なら、続けて手で直せる。
  const useRange = (a: number, b: number) => {
    all.checked = false;
    from.value = String(Math.min(a, b));
    to.value = String(Math.max(a, b));
    syncFields();
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
    // 「全て」のあいだも、今の範囲をそのまま欄に出す（欄は隠さない）。
    reflect(x0, x1);
    const span = Math.max(x1 - x0, 1);
    const left = PAD.left;
    const right = w - PAD.right;
    const top = PAD.top;
    const bottom = h - PAD.bottom;
    const mid = (top + bottom) / 2;
    const half = (bottom - top) / 2;
    const px = (n: number) => left + ((n - x0) / span) * (right - left);
    // ⚠️ **縦は正規化値（-1〜+1）で受ける。** 評価値か勝率かを知っているのは
    // 軸（`Axis`）だけで、**描く側は軸の中身を知らない。**
    const py = (v: number) => mid - v * half;
    const ax = axis();
    // 点 → 縦位置。**詰みは端**（評価値なら頭打ち、勝率なら 100% / 0%）。
    const at = (p: EvalPoint) => py(ax.at(p));

    // ---- 目盛り ------------------------------------------------------------
    //
    // 0 の線だけ明るくする（**形勢が入れ替わる線**なので、そこだけは読めること）。
    //
    // ⚠️ **刻みは CAP に依らず 1000 ごと**（2026-08-14。以前は CAP の半分＝1500）。
    // 評価値は 1000 単位で語られるので、**線の数を増やすより読める数字にする**
    // ほうが目盛りとして役に立つ。⚠️ **CAP を変えたら刻みも見直すこと**
    // （細かすぎると線だらけになって折れ線が読めない）。
    // ⚠️ **文字は入るぶんだけ**（`placeLabels`）。線は全部引く —— 細かい刻みの
    // 間隔そのものが「この帯はここまで広い」という目盛りになっている。
    const labelled = placeLabels(ax.ticks, py);
    for (const t of ax.ticks) {
      svg.appendChild(
        el("line", {
          x1: left, x2: right, y1: py(t.at), y2: py(t.at),
          class: t.zero ? "eval-axis is-zero" : t.minor ? "eval-axis is-minor" : "eval-axis",
        }),
      );
      // ⚠️ **真ん中の線には値を書かない** —— そこには手数の目盛りが乗っている。
      if (labelled.has(t)) {
        svg.appendChild(
          el("text", { x: left - 4, y: py(t.at) + 3, class: "eval-tick", "text-anchor": "end" }),
        ).textContent = t.text;
      }
    }
    // 縦の目盛り。**間隔は範囲に合わせる**（150 手で 5 手刻みにすると読めない）。
    //
    // ⚠️ **手数の文字はここで描かない**（2026-08-14）。**真ん中の 0 の線の上**に
    // 出すので、**折れ線より後**に描かないと線に隠れる（下の「手数の目盛り」）。
    const step = span <= 30 ? 5 : span <= 80 ? 10 : span <= 200 ? 20 : 50;
    const xticks: number[] = [];
    for (let n = Math.ceil(x0 / step) * step; n <= x1; n += step) {
      svg.appendChild(el("line", { x1: px(n), x2: px(n), y1: top, y2: bottom, class: "eval-grid" }));
      xticks.push(n);
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
    for (const s of refs) {
      const color = colorOf(s.engineId);
      const pts = (s.points ?? []).filter((p) => p.number >= x0 && p.number <= x1);
      if (pts.length < 2) {
        continue;
      }
      svg.appendChild(el("polyline", {
        points: pts.map((p) => `${px(p.number)},${at(p)}`).join(" "),
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
      ).textContent = axisMode === "winrate"
        ? "解析すると、ここに勝率が並びます"
        : "解析すると、ここに評価値が並びます";
    }

    // ---- 折れ線 ------------------------------------------------------------
    series.forEach((s) => {
      const color = colorOf(s.engineId);
      const pts = (s.points ?? []).filter((p) => p.number >= x0 && p.number <= x1);
      if (pts.length === 0) {
        return;
      }
      if (pts.length > 1) {
        svg.appendChild(
          el("polyline", {
            points: pts.map((p) => `${px(p.number)},${at(p)}`).join(" "),
            class: "eval-line",
            stroke: color,
          }),
        );
      }
      // 点は詰まりすぎない範囲でだけ描く（150 手ぶん打つと線が潰れる）。
      if (span <= 80 || pts.length <= 40) {
        for (const p of pts) {
          const dot = el("circle", { cx: px(p.number), cy: at(p), r: 2.2, fill: color });
          const title = document.createElementNS(NS, "title");
          title.textContent =
            `${p.number}手目 ${p.move}　${s.label}: ${ax.label(p)}（深さ ${p.depth}）`;
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

    // ---- 手数の目盛り ------------------------------------------------------
    //
    // ⚠️ **真ん中（0 の線）に出す**（2026-08-14。以前は箱の下端）。評価値の
    // 折れ線は互角のあたりを行き来することが多いので、**手数が一番読みたいのは
    // その近く**。下端に置くと、線を目で追いながら手数を読むのに視線が往復する。
    //
    // ⚠️ **折れ線より後に描くこと**（線に隠れる）。⚠️ **文字の後ろは地の色で
    // 縁取る**（`paint-order: stroke`）—— 0 の線と折れ線がそのまま文字を横切るので、
    // 縁取りが無いと読めない。
    for (const n of xticks) {
      svg.appendChild(
        el("text", {
          x: px(n), y: mid + 3, class: "eval-tick is-axis", "text-anchor": "middle",
        }),
      ).textContent = String(n);
    }

    // ---- 凡例 --------------------------------------------------------------
    //
    // **どの線がどのエンジンか**が分からないと、食い違いを読む意味が無い。
    // ⚠️ **見出しの行に出す**（グラフの中に重ねない。上の EvalGraphOptions）。
    legend.replaceChildren();
    series.forEach((s) => {
      const chip = document.createElement("span");
      chip.className = "eval-legend";
      chip.style.color = colorOf(s.engineId);
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
    const ax = axis();
    for (const s of graph.series ?? []) {
      const p = (s.points ?? []).find((q) => q.number === n);
      if (!p) {
        continue;
      }
      move = move || p.move;
      // ⚠️ **今の軸の読み方で出すこと**（勝率で見ているのに評価値が出ると、
      // 折れ線の高さと読み上げが噛み合わない）。
      parts.push(`${s.label || s.engineId} ${ax.label(p)}`);
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

  all.addEventListener("change", () => {
    syncFields();
    draw();
  });

  // 縦軸の切り替え（見出しの「評価値 / 勝率」）。
  //
  // ⚠️ **保存を待たずに描き替えること**（押した手応えが要る）。保存は
  // `onMode` に任せ、**失敗しても今の画面はそのまま**（次の起動で戻るだけ）。
  mode.addEventListener("click", (ev) => {
    const btn = (ev.target as HTMLElement | null)?.closest<HTMLButtonElement>("button[data-mode]");
    const next = btn?.dataset.mode;
    if (next !== "eval" && next !== "scaled" && next !== "winrate") {
      return;
    }
    if (next === axisMode) {
      return;
    }
    axisMode = next;
    syncMode();
    draw();
    opts.onMode?.(axisMode);
  });
  // 打っている途中でも追随させる（確定を待たない。壊れた値は domain が丸める）。
  for (const input of [from, to]) {
    input.addEventListener("input", draw);
  }
  syncFields();
  syncMode();
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
    setMode(next: EvalMode) {
      if (next === axisMode) {
        return;
      }
      axisMode = next;
      syncMode();
      draw();
    },
    relayout: draw,
  };
}
