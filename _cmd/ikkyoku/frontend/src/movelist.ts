// 手順（棋譜）のツリービュー（2026-09-08 に `study.ts` から切り出した）。
//
// **盤の右に縦に積む。クリックでその局面へ戻れる。**
//
// ⚠️ **盤とは別のモジュールにしてある。** 元は `mountStudyBoard` が盤と一緒に
// 面倒を見ていたが、**解析の列を別ウィンドウへ切り離せるようにするには、
// 盤の無い窓でも手順が出せる必要がある**（切り離した窓に盤は無い）。
//
// ⚠️ **局面は持たない。** 描くのは渡された `StudyState` そのもので、
// 操作は `StudyService` を呼ぶだけ。**どちらの窓から押しても、変わるのは
// Go 側の 1 つの手順**で、両方の窓が `study:changed` で追随する。
//
// ⚠️ **畳み方だけはここが持つ**（画面だけの状態。Go には持たせない）。
import { StudyService } from "../bindings/github.com/ShinteLab/ikkyoku/app";
import type { StudyState } from "../bindings/github.com/ShinteLab/ikkyoku/app/models";
import { openPopup, type PopupHandle, type PopupItem } from "./popup";

export interface MoveListHandle {
  // render は手順を描く（null なら空にする）。
  render(state: StudyState | null): void;
  // foldAdded は足したばかりの読み筋を畳む（**その手 1 行 +「＋」の形にする**）。
  //
  // **読み筋を足した直後に呼ぶ**（`AddLine` が返した最初の節点）。⚠️ 読み筋は
  // 15 手ぶら下がることがあるので、**足したぶんがそのまま並ぶと手順が読めない**。
  // ⚠️ **畳むのは足した手だけ** —— 他の候補まで畳み直すと、開いて読んでいる
  //最中に別の候補を足したときに**読んでいたほうが黙って閉じる**。
  foldAdded(id: number): void;
  // dropCurrent は**今見ている手から下を消す**（Delete キー。2026-09-26）。
  // 右クリック →「以降の手を削除」と同じ操作で、**消えるのが 1 手なら聞かずに消し、
  // 2 手以上なら確認を出す**（`dropAt`）。
  //
  // ⚠️ **相手は「今見ている手」**（光っている行）。手のボタンは押すと描き直されて
  // フォーカスが外れるので、**フォーカスのある行**では決められない。
  // 消すと親へ戻るので、**最後の手で押し続ければ 1 手ずつ短くなる。**
  dropCurrent(): void;
}

export interface MoveListOptions {
  // panel は手順を並べる場所。
  panel: HTMLElement;
  // onState は操作の結果の局面。**盤も含めて描き直すのは呼び出し側。**
  //
  // ⚠️ **ここで自分だけ描き直さないこと** —— 手順を押すと局面が変わるので、
  // **盤も一緒に描き直さないと食い違う**。
  onState(state: StudyState): void;
  // onError は操作が通らなかったときの理由。
  onError(message: string): void;
  // engineOf は**その手を挙げたエンジン**の見た目（`Node.Sources` の 1 件）。
  //
  // ⚠️ **色をここで決めないこと**（評価値グラフの折れ線と**同じ色**でなければ
  // 意味が無い）。
  engineOf(id: string): { color: string; label: string };
}

export function mountMoveList(opts: MoveListOptions): MoveListHandle {
  const { panel: movesPanel, onState, onError, engineOf } = opts;

  let state: StudyState | null = null;
  // 畳んである手（**画面だけの状態**。局面も手順も変わらないので Go には持たせない）。
  //
  // ⚠️ **根が入れ替わったら捨てること。** 節点の id は木ごとに 1 から振り直すので、
  // 前の対局の畳み方が**関係の無い手を隠す**。
  const collapsed = new Set<number>();
  let lastRoot = "";
  // 最後に描いたときの「今見ている手」。**道筋を開くのは、ここが変わったときだけ。**
  let lastAt = -1;


  // run は Go を呼んで、返ってきた局面を**呼び出し側に渡す**。
  //
  // ⚠️ **ここで描き直さないこと**（`onState` が盤ごと描き直す）。
  // **失敗しても状態は取り直して渡す**（画面が古いまま残るほうが分かりにくい）。
  const run = async (op: () => Promise<StudyState>) => {
    try {
      onState(await op());
    } catch (err) {
      try {
        onState(await StudyService.State());
      } catch {
        /* 取れないなら描き直さない。理由だけ出す。 */
      }
      onError(String(err instanceof Error ? err.message : err));
    }
  };

  // ask は開いているダイアログ（**手順リストの右クリックのメニューと確認**）。
  let ask: PopupHandle | null = null;

  const closeAsk = () => {
    ask?.close();
    ask = null;
  };

  // 手順（棋譜）。**盤の右に縦のツリービューで積む。クリックでその局面へ戻れる。**
  //
  // ⚠️ **一直線ではない**（2026-08-13）。枝は 1 段字下げして、**分かれた手の
  // すぐ下**に出す（Go 側の Nodes() が既にその順で返す。**フロントで並べ替えない**）。
  //
  // ⚠️ **戻っても手順は消さない**（進め直せる）。消えるのは**右クリックで
  // 消したとき**だけ（別の手を指しても、もう消えない —— 枝が生える）。
  //
  // ⚠️ **左クリック＝戻る / 右クリック＝その手以下を消す**（2026-08-13）。
  // 左を消す操作にしない —— **戻って見る**のは手順リストの主な用途なので、
  // それを潰すと評価値グラフからしか戻れなくなる。
  const renderMoves = () => {
    movesPanel.replaceChildren();
    if (!state?.loaded) {
      return;
    }
    const at = state.currentId ?? 0;
    const rows = state.nodes ?? [];

    // ---- 折り畳み（2026-08-13）-------------------------------------------
    //
    // ⚠️ **畳むのは「その手にぶら下がった枝」だけ。** 続き（同じ深さで伸びる
    // 手順）は畳まない —— あれはその手の枝ではなく**同じ 1 本の続き**なので、
    // 畳むと本譜が丸ごと消える。
    //
    // 判定は**深さ**で行う: 親より深い子＝枝の入口。
    const depthOf = new Map<number, number>([[0, 0]]);
    const parentOf = new Map<number, number>([[0, -1]]);
    for (const n of rows) {
      depthOf.set(n.id, n.depth);
      parentOf.set(n.id, n.parent);
    }
    // ⚠️ **見ている局面が変わったときだけ、その道筋を開く。**
    // 畳んだ中の手へ飛んだら（評価値グラフから、など）見えるようにするための処理。
    //
    // ⚠️ **描き直しのたびに開かないこと**（2026-08-13 に踏んだ）。**畳む操作自体が
    // 描き直し**なので、毎回開くと**今いる枝を含む手を畳めない**（押しても何も
    // 起きないように見え、「別の手を選んでからでないと畳めない」になる）。
    // **畳んだ結果、今見ている手が隠れるのは正しい**（盤には出ているし、開けば戻る）。
    if (at !== lastAt) {
      lastAt = at;
      for (let n = at; n > 0; n = parentOf.get(n) ?? -1) {
        const up = parentOf.get(n) ?? -1;
        if (up >= 0 && (depthOf.get(n) ?? 0) > (depthOf.get(up) ?? 0)) {
          collapsed.delete(up);
        }
      }
    }
    const hidden = (id: number): boolean => {
      for (let n = id; n > 0; ) {
        const up = parentOf.get(n) ?? -1;
        if (up < 0) {
          return false;
        }
        // 畳んである手の「枝の入口」を通ったなら隠れている。
        if (collapsed.has(up) && (depthOf.get(n) ?? 0) > (depthOf.get(up) ?? 0)) {
          return true;
        }
        n = up;
      }
      return false;
    };
    // 枝を持つ手にだけ +/- を出す（続きしか無い手には出さない）。
    const hasBranch = (id: number) =>
      rows.some((n) => n.parent === id && n.depth > (depthOf.get(id) ?? 0));
    // ⚠️ **変化の始まりには区切りを出す。** 同じ深さの変化が続けて並ぶと
    // （９七角の続きが 2 本、など）、**どこで次の変化に変わったのか**が
    // 手数の戻り方でしか分からない。**本譜側には出さない**（線が増えるだけ）。
    const isFork = (n: { parent: number; main: boolean }) =>
      !n.main && rows.filter((x) => x.parent === n.parent).length > 1;
    // 手数と手を別の要素にして、**手数の桁を揃える**（縦に並ぶので、揃っていないと
    // 何手目を見ているのかが読み取りにくい）。
    const chip = (
      num: string, label: string, id: number, title: string,
      o?: {
        depth?: number; main?: boolean; parent?: number; fork?: boolean;
        sources?: string[]; hand?: boolean; guess?: boolean;
      },
    ) => {
      // ⚠️ **行は chip とトグルの 2 つ**（ボタンの中にボタンは置けない）。
      // 字下げは**行のほう**に付ける（chip に付けると、トグルだけ左に残る）。
      const row = document.createElement("div");
      row.className = o?.fork ? "move-row is-fork" : "move-row";
      row.style.setProperty("--move-depth", String(o?.depth ?? 0));

      const toggle = document.createElement("button");
      toggle.type = "button";
      toggle.className = "move-toggle";
      if (hasBranch(id)) {
        const shut = collapsed.has(id);
        toggle.textContent = shut ? "+" : "−";
        toggle.title = shut ? "枝を開く" : "枝を畳む";
        toggle.setAttribute("aria-expanded", String(!shut));
        toggle.addEventListener("click", () => {
          if (collapsed.has(id)) {
            collapsed.delete(id);
          } else {
            collapsed.add(id);
          }
          // ⚠️ **Go は呼ばない**（畳むのは見た目だけ。局面も手順も変わらない）。
          renderMoves();
        });
      } else {
        // ⚠️ **枝が無くても場所は空けておくこと。** 出したり消したりすると、
        // 手の頭が行ごとに左右へずれて読みにくい。
        toggle.classList.add("is-empty");
        toggle.tabIndex = -1;
        toggle.setAttribute("aria-hidden", "true");
      }
      row.appendChild(toggle);

      const b = document.createElement("button");
      b.type = "button";
      b.className = "move-chip";
      b.title = title;
      const i = document.createElement("span");
      i.className = "move-num";
      i.textContent = num;
      const t = document.createElement("span");
      t.className = "move-text";
      t.textContent = label;
      b.append(i, t);
      // 自分で盤に指した手（2026-08-14）。**黒い丸で、目立たなくてよい** ——
      // 読みたいのは「エンジンが挙げた手（色）」のほうで、これは
      // **後から「これは自分で入れた手だ」と分かればよい**という程度の印。
      // ⚠️ **棋譜（KIF / URL）の手には出ない**（Go 側が印を付けない）。
      // あちらは**実際に現れた指し手**で、自分で試しに指した手とは別物。
      if (o?.hand) {
        const dot = document.createElement("span");
        dot.className = "move-source is-hand";
        dot.title = "自分で指した手";
        b.appendChild(dot);
      }
      // 中継の画面から**推測で足した手**（2026-09-15）。
      //
      // ⚠️ **これが「どこまで戻ればよいか」の材料そのもの。** 追従中は
      // **おかしくてもとにかく進む**ので、**どの手が確かでどの手が推測かが
      // 見えていないと、違うと思ったときに戻る先が分からない。**
      // ⚠️ **ぴったり一致した手には出ない**（Go 側が印を付けない）。
      if (o?.guess) {
        const dot = document.createElement("span");
        dot.className = "move-source is-guess";
        dot.title = "中継の画面から推測で足した手（違っていたらここから消せます）";
        b.appendChild(dot);
      }
      // ⚠️ **手の後ろに出す**（前に置くと、手数と手のあいだに割り込んで
      // **縦に並んだ手の頭が揃わなくなる**）。
      // ⚠️ **複数出しうる** —— 同じ手を 2 つのエンジンが挙げるのは普通で、
      // **その一致が一番読みたいもの**。1 つに丸めないこと。
      // ⚠️ **自分で指した手にエンジンの丸が並ぶこともある**（エンジンが挙げた手を
      // 自分でも指したとき）。**どちらも本当**なので、片方に丸めないこと。
      for (const src of o?.sources ?? []) {
        const e = engineOf(src);
        const dot = document.createElement("span");
        dot.className = "move-source";
        dot.style.background = e.color;
        // ⚠️ **色だけにしないこと**（色が読めなくても誰の手かは分かるように）。
        dot.title = `${e.label} が挙げた手`;
        b.appendChild(dot);
      }
      b.classList.toggle("is-current", id === at);
      // ⚠️ **枝は見た目で分かるようにする**（字下げ + 色）。同じ手数の手が
      // 何行も並ぶので、**どれが本譜か**が分からないと読めない。
      b.classList.toggle("is-branch", o?.main === false);
      // ⚠️ **id と親は消す範囲を出すのに要る**（`.is-doomed` を付ける相手を辿る鍵）。
      // ⚠️ **GoTo に渡すのも id**（手数ではない。枝があると同じ手数が何個もある）。
      b.dataset.id = String(id);
      b.dataset.parent = String(o?.parent ?? -1);
      b.addEventListener("click", () => void run(() => StudyService.GoTo(id)));
      row.appendChild(b);
      return row;
    };
    movesPanel.appendChild(chip("", "開始局面", 0, "採ったときの局面に戻ります"));
    for (const m of rows) {
      // 畳んである枝の中は出さない（**畳むのは見た目だけ**なので、手順は生きている）。
      if (hidden(m.id)) {
        continue;
      }
      movesPanel.appendChild(
        // ⚠️ **出す数字と GoTo に渡す値は別物。** m.number は根からの手数で、
        // 画面に出すのは棋譜の手数（＝根の手数を足したもの）。撮った 41 手目の
        // 局面を根にすると、この 2 つは 40 ずれる。**渡すのは m.id。**
        chip(String((state.first ?? 0) + m.number), m.text || m.usi, m.id,
          `${m.usi} までの局面に戻ります` +
            (m.main ? "" : "（枝）") + "（右クリックでメニュー。Delete キーで今見ている手から下を消します）",
          {
            depth: m.depth, main: m.main, parent: m.parent, fork: isFork(m),
            sources: m.sources ?? [], hand: m.hand ?? false, guess: m.guess ?? false,
          }),
      );
    }
    // 今見ている手が画面の外にあると、進めても手順が動いていないように見える。
    movesPanel.querySelector<HTMLElement>(".move-chip.is-current")?.scrollIntoView({
      block: "nearest",
      inline: "nearest",
    });
  };

  // ---- 手順を消す（手順リストの右クリック）--------------------------------
  //
  // ⚠️ **「1手戻す」の代わり**（2026-08-13。分岐を入れる前段）。消えるのは
  // **押した手とその先**で、`Undo` と違って「今どこを見ているか」に依存しない。
  //
  // ⚠️ **右クリックでいきなり消す UI を出さないこと**（2026-08-14）。
  // まず**メニュー**（「以降の手を削除」）を出し、**押してから**「本当に消すか」を
  // 聞く。右クリックは誤爆しやすいのに、いきなり赤いボタンが指の下に出ると
  // **その勢いで押せてしまう**。候補手の右クリック（「手順を追加」）とも形が揃う。
  //
  // ⚠️ **2 手以上消えるときは聞くほうを省かないこと。** 消えるのは**1 手ではなく、
  // そこから下の全部**（解析結果も一緒に消える）。**メニューは「何をするか」、
  // 確認は「何が消えるか」**で役割が違う。
  // ⚠️ **消えるのが 1 手だけなら聞かない**（2026-09-26）。「何が消えるか」は
  // 押した手そのもので、確認が言うことが何も無い。末尾から 1 手ずつ削る
  // （Delete の連打）たびに聞かれると使えない。判断は `dropAt` の 1 か所。
  // ⚠️ **聞いているあいだ、消える範囲を赤く光らせる**（`.is-doomed`）——
  // 「その手以下が消える」は文字で言うより見せたほうが早い。
  // ⚠️ **光らせるのは確認のときだけ**（メニューの段では光らせない）。
  // 赤は「これから消える」の合図なので、まだ選んでいない段で出すと意味が薄れる。

  // markDoomed はその手とその子孫を「消える」見た目にする（0 で全部戻す）。
  //
  // ⚠️ **子孫は親を辿って決めること。** 字下げ（depth）では見分けられない ——
  // **本譜の続きは親と同じ深さのまま**だからで、深さで切ると本譜側だけ
  // 消えないように見える。
  const markDoomed = (from: number) => {
    const chips = [...movesPanel.querySelectorAll<HTMLElement>(".move-chip")];
    const parent = new Map<number, number>();
    for (const el of chips) {
      parent.set(Number(el.dataset.id ?? "0"), Number(el.dataset.parent ?? "-1"));
    }
    const doomed = (id: number): boolean => {
      for (let n = id; n > 0; n = parent.get(n) ?? -1) {
        if (n === from) {
          return true;
        }
      }
      return false;
    };
    for (const el of chips) {
      el.classList.toggle(
        "is-doomed", from > 0 && doomed(Number(el.dataset.id ?? "0")));
    }
  };

  // askDrop は押した場所で「ここから消す」を聞く。
  const askDrop = (x: number, y: number, id: number, label: string, count: number) => {
    closeAsk();
    markDoomed(id);
    ask = openPopup(x, y, {
      label: "手順を消す",
      // ⚠️ **初期フォーカスは「やめる」**（成る/成らずと逆）。こちらは消す操作なので、
      // Enter の連打で消えてしまわないほうを既定にする。
      focus: 1,
      items: [
        {
          // **何が何手消えるかを文言に出す**（押す前に読めること）。
          label: count > 1 ? `${label} から下を消す（${count}手）` : `${label} を消す`,
          kind: "danger",
          onPick: () => void run(() => StudyService.DropFrom(id)),
        },
        { label: "やめる", onPick: () => {} },
      ],
      // **消える範囲の色も一緒に落とす**（残ると、消していないのに消えた顔をする）。
      onClose: () => markDoomed(0),
    });
  };

  // countDrop はその手を消したときに消える手数（**その手 + 子孫**。ぶら下がった
  // 枝も全部）。⚠️ **字下げでは数えられない**ので、親を辿って数える
  // （markDoomed と同じ理由）。
  const countDrop = (id: number): number => {
    const parent = new Map<number, number>();
    for (const n of state?.nodes ?? []) {
      parent.set(n.id, n.parent);
    }
    return (state?.nodes ?? []).filter((n) => {
      for (let x = n.id; x > 0; x = parent.get(x) ?? -1) {
        if (x === id) {
          return true;
        }
      }
      return false;
    }).length;
  };

  // dropAt はその手から下を消す。**1 手だけなら聞かずに消し、2 手以上なら確認を出す。**
  //
  // ⚠️ **右クリックのメニューと Delete キーの両方がここを通る。** 聞くかどうかの
  // 判断を 2 か所に書くと、片方だけ黙って消すようになる。
  const dropAt = (x: number, y: number, id: number, label: string) => {
    const count = countDrop(id);
    if (count <= 1) {
      closeAsk();
      void run(() => StudyService.DropFrom(id));
      return;
    }
    askDrop(x, y, id, label, count);
  };

  // askMoveMenu は手を右クリックしたときのメニュー。
  //
  // ⚠️ **ここでは何も起きない**（消すほうは、2 手以上なら押すと確認が出る）。
  // ⚠️ **`danger` にしないこと** —— 赤くするのは「押したら消える」ボタンだけで、
  // メニューの段で赤いと**確認が出ることに気づかず身構える**。
  const askMoveMenu = (
    x: number, y: number, id: number, label: string,
    o: { main: boolean; chosen: boolean; canPromote: boolean },
  ) => {
    closeAsk();
    const items: PopupItem[] = [];
    // 「本線にする」は**続きがまだ決まっていない分かれ道の子にだけ出す**
    // （2026-08-18）。⚠️ **出す条件は Go 側が付けた印**（`Node.canPromote`）で、
    // **画面の字下げから判断しないこと** —— 続きが決まっているかは木の形で決まる。
    // ⚠️ **確認は挟まない**（「分岐にする」と同じ。何も消えないし、外せば戻る）。
    if (o.canPromote) {
      items.push({
        label: "本線にする",
        onPick: () => void run(() => StudyService.Promote(id)),
      });
    }
    // 「分岐にする」は**本譜の手**と**本線に選んだ手**に出す（枝は既に分岐なので、
    // 押しても何も起きない項目が並ぶだけ）。⚠️ **選んだ手にも出すこと** ——
    // **これが唯一の外し方**で、無いと最初に選んだ 1 本で固まる。
    // ⚠️ **確認は挟まない** —— 何も消えないし、もう一度指すか棋譜を取り直せば戻る。
    if (o.main || o.chosen) {
      items.push({
        label: "分岐にする",
        onPick: () => void run(() => StudyService.Branch(id)),
      });
    }
    items.push({
      label: "以降の手を削除",
      onPick: () => dropAt(x, y, id, label),
    });
    ask = openPopup(x, y, { label: "手順", items });
  };

  // 手順リストの右クリック。**チップの上でだけ受ける**（列の余白では既定のまま）。
  movesPanel.addEventListener("contextmenu", (e) => {
    const chip = (e.target as HTMLElement | null)?.closest<HTMLElement>(".move-chip");
    if (!chip) {
      return;
    }
    // ⚠️ **webview の既定メニューを止める**（訂正タブの盤と同じ）。
    e.preventDefault();
    const id = Number(chip.dataset.id ?? "0");
    // 「開始局面」は手ではないので消せない（消したいなら 1 手目を押す）。
    if (id < 1 || !state?.loaded) {
      return;
    }
    const label = chip.querySelector<HTMLElement>(".move-text")?.textContent ?? "この手";
    // どの項目を出すかは**その手の印**で決まる（本譜か・本線に選んだか・選べるか）。
    // ⚠️ **判定は Go 側**（`Node`）。フロントで木の形を読み直さないこと。
    const me = (state.nodes ?? []).find((n) => n.id === id);
    askMoveMenu(e.clientX, e.clientY, id, label, {
      main: !!me?.main,
      chosen: !!me?.chosen,
      canPromote: !!me?.canPromote,
    });
  });


  return {
    render(next: StudyState | null) {
      closeAsk();
      if (!next?.loaded) {
        state = null;
        movesPanel.replaceChildren();
        return;
      }
      // ⚠️ **根が変わったら畳み方を捨てる**（id が振り直されるため）。
      if ((next.rootSfen ?? "") !== lastRoot) {
        lastRoot = next.rootSfen ?? "";
        collapsed.clear();
        // ⚠️ **道筋を開く判定もやり直す**（別の木なので、前の位置と比べる意味が無い）。
        lastAt = -1;
      }
      state = next;
      renderMoves();
    },
    foldAdded(id: number) {
      if (!state?.loaded || id <= 0) {
        return;
      }
      // ⚠️ **畳むのは見た目だけ**（Go は呼ばない。手順も局面も変わらない）。
      // 足した手は「変化の頭」なので、続きは 1 段深い ——
      // **畳めばその 1 手だけが残って「＋」が付く**（`hasBranch` / `hidden`）。
      collapsed.add(id);
      renderMoves();
    },
    dropCurrent() {
      const id = state?.currentId ?? 0;
      // 「開始局面」は手ではないので消せない（右クリックと同じ）。
      if (!state?.loaded || id < 1) {
        return;
      }
      const chip = movesPanel.querySelector<HTMLElement>(`.move-chip[data-id="${id}"]`);
      const label = chip?.querySelector<HTMLElement>(".move-text")?.textContent ?? "この手";
      // 確認はその手の行の下に出す（右クリックならカーソルの位置だが、キーには無い）。
      // ⚠️ **行が見えないときはリストの左上**（今見ている手は畳んでも道筋が開くので
      // 普通は見えている）。
      const r = (chip ?? movesPanel).getBoundingClientRect();
      dropAt(r.left + 16, r.bottom, id, label);
    },
  };
}
