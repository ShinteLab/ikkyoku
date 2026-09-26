// 棋譜タブ（棚）。**保存済みの棋譜を探して解析へ送る面。**
//
// 実装は `kicho`（棋譜データベース）にあり、ここが呼ぶのは Go 側の
// `KifuService` だけ。**検索の条件も手数の数え方もここには書かない。**
//
// ⚠️ **入力タブの「棋譜を貼り付ける」とは系統が違う。** あちらは
// 「保存せず解析する」で、こちらは「棚に溜めたものから選ぶ」。
// **二系統を残してある** —— 棚は解析の前提条件ではない（設計原則3）。
//
// ⚠️ **「棋譜 URL をコピー」は置かない**（kicho の UI にはある）。あれは
// ShogiHome 等の外部ツールへ渡すためのもので、ikkyoku では渡す先が自分自身。
// **代わりに置くのが「解析」で、その口は棋戦名のリンク**（2026-09-14）。
import { Clipboard } from "@wailsio/runtime";
import { KifuService } from "../bindings/github.com/ShinteLab/ikkyoku/app";
import { openPopup } from "./popup";
import type {
  GameDetail,
  GameSummary,
  KifuLoad,
  SearchResult,
} from "../bindings/github.com/ShinteLab/ikkyoku/app/models";

export type LibraryHandle = {
  // reveal はタブを開いたときに呼ぶ（棚を読み直す）。
  //
  // ⚠️ **開くたびに読み直すこと。** 同じ DB を kicho アプリからも触れるので、
  // 初回だけ読む作りにすると**向こうで足した棋譜が見えない**（共用にした意味が消える）。
  reveal: () => void;
  // refresh は棚そのものが入れ替わったときに呼ぶ（設定で DB を開き直したとき）。
  refresh: () => void;
};

// MIN_SEARCH_LENGTH は索引（FTS5 trigram）が効く最小文字数。
//
// ⚠️ **Go 側の `MinSearchLength`（= `store.MinTrigramLen`）と同じ値。**
// 案内を出すためだけに持っており、**判定そのものは Go 側**（3 文字未満は
// LIKE へ落ちる）。**ここを検索の条件に使わないこと。**
const MIN_SEARCH_LENGTH = 3;

// SOURCE_LABELS は取得元の表示名。
const SOURCE_LABELS: Record<string, string> = {
  yomiuri: "読売（竜王戦）",
  shogilive: "将棋連盟 中継",
  shogidb2: "将棋DB2",
  url: "URL から取り込み",
  paste: "貼り付け",
};

const formatDate = (rfc3339: string): string => {
  if (!rfc3339) return "-";
  const d = new Date(rfc3339);
  if (Number.isNaN(d.getTime())) return "-";
  return d.toLocaleDateString("ja-JP");
};

// movesText は手数の表示。
//
// **0 手かつ未終局は「（対局前）」。** 中継は対局開始前から棋譜を置いており、
// ヘッダだけで指し手がまだ 1 手も無い .kif が返る。**取得の失敗ではない。**
const movesText = (g: GameSummary): string => {
  if (g.finished) return `${g.moves}手（終局）`;
  if (g.moves === 0) return "（対局前）";
  return `${g.moves}手`;
};

export function mountLibrary(
  root: ParentNode,
  opts: {
    // onAnalyze は「解析」を押したとき（解析タブへ移って描くのは呼び出し側）。
    onAnalyze: (load: KifuLoad) => void;
  },
): LibraryHandle {
  const count = root.querySelector<HTMLElement>("#library-count")!;
  const reload = root.querySelector<HTMLButtonElement>("#library-reload")!;
  const text = root.querySelector<HTMLInputElement>("#library-text")!;
  const from = root.querySelector<HTMLInputElement>("#library-from")!;
  const to = root.querySelector<HTMLInputElement>("#library-to")!;
  const finishedOnly = root.querySelector<HTMLInputElement>("#library-finished")!;
  const search = root.querySelector<HTMLButtonElement>("#library-search")!;
  const clear = root.querySelector<HTMLButtonElement>("#library-clear")!;
  const hint = root.querySelector<HTMLElement>("#library-hint")!;
  const status = root.querySelector<HTMLParagraphElement>("#library-status")!;
  const rows = root.querySelector<HTMLTableSectionElement>("#library-rows")!;
  // 詳細はモーダル（2026-09-12）。**開く口は棋戦名のリンクだけ。**
  const modal = root.querySelector<HTMLDialogElement>("#library-modal")!;
  const modalTitle = root.querySelector<HTMLElement>("#library-modal-title")!;
  const modalClose = root.querySelector<HTMLButtonElement>("#library-modal-close")!;
  const modalMeta = root.querySelector<HTMLElement>("#library-modal-meta")!;
  const modalCopy = root.querySelector<HTMLButtonElement>("#library-modal-copy")!;

  // 今開いている詳細の KIF（原本）。**「KIF をコピー」が渡すのはこれ。**
  // ⚠️ **画面には出さない**（本文を出すのをやめたのが 2026-09-12 の変更）。
  let openKif = "";
  // 読み込み中は多重に走らせない（検索ボタン連打・タブの出入り）。
  let busy = false;

  const setStatus = (msg: string, kind: "" | "error" | "warn" = "") => {
    status.textContent = msg;
    status.hidden = msg === "";
    status.classList.toggle("is-error", kind === "error");
    status.classList.toggle("is-warn", kind === "warn");
  };

  const hasConditions = (): boolean =>
    text.value !== "" || from.value !== "" || to.value !== "" || finishedOnly.checked;

  // 3 文字未満の案内。**索引が効かないので全件走査になる**ことを知らせる
  // （件数が増えたときに「急に遅くなった」と見えないように）。
  //
  // ⚠️ **打っている途中には出さない**（2026-09-12）。1 文字ごとに出入りすると
  // **案内そのものがちらつく**うえ、以前は検索欄の下に置いていたので
  // **下の表まで動いていた。** 出すのは**検索を走らせたとき**だけで、
  // 場所は**見出しの行**（高さが再読み込みボタンで決まっているのでずれない）。
  const showHint = () => {
    const n = [...text.value].length;
    const short = n > 0 && n < MIN_SEARCH_LENGTH;
    hint.hidden = !short;
    if (short) {
      hint.textContent = `${MIN_SEARCH_LENGTH} 文字未満のため索引を使わず全件を走査しました（件数が増えると遅くなります）`;
    }
  };

  const closePreview = () => {
    openKif = "";
    modalCopy.classList.remove("is-copied");
    if (modal.open) modal.close();
  };

  const addMeta = (label: string, value: string) => {
    if (!value) return;
    const dt = document.createElement("dt");
    dt.textContent = label;
    const dd = document.createElement("dd");
    dd.textContent = value;
    modalMeta.append(dt, dd);
  };

  const showPreview = (d: GameDetail) => {
    openKif = d.kif;
    modalTitle.textContent = d.event || "(棋戦名なし)";
    modalMeta.replaceChildren();
    addMeta("先手", d.black);
    addMeta("後手", d.white);
    addMeta("手合割", d.handicap);
    addMeta("開始", formatDate(d.startedAt));
    addMeta("場所", d.place);
    addMeta("手数", movesText(d));
    addMeta("取得元", SOURCE_LABELS[d.source] ?? d.source);
    addMeta("取得元 URL", d.sourceUrl);
    // ⚠️ **KIF の本文は画面に出さない**（2026-09-12）。持ち出す口は
    // 「KIF をコピー」だけで、渡すのは**原本のまま**（整形し直さない）——
    // 保存されているのも原本なので、渡すものと棚の中身を食い違わせない。
    modalCopy.classList.remove("is-copied");
    modalCopy.disabled = openKif === "";
    if (!modal.open) modal.showModal();
  };

  const show = async (id: string) => {
    setStatus("");
    try {
      showPreview(await KifuService.Get(id));
    } catch (err) {
      setStatus(`棋譜を読めませんでした: ${String(err)}`, "error");
    }
  };

  const remove = async (id: string) => {
    setStatus("");
    try {
      await KifuService.Delete(id);
      closePreview();
      await load();
    } catch (err) {
      setStatus(`削除できませんでした: ${String(err)}`, "error");
    }
  };

  // 削除の確認。**取り返しがつかない**（取得元が無いもの＝貼り付け登録は
  // 戻せない）ので一度聞く。
  //
  // ⚠️ **`window.confirm` を使わないこと**（popup.ts の先頭の理由。
  // 画面の真ん中に出るうえに「OK / キャンセル」という
  // この選択とは無関係な語でしか聞けない）。
  // ⚠️ **初期フォーカスは「やめる」**（消す操作なので、Enter の連打で
  // 消えてしまわないように。手順を消すときと同じ）。
  const askRemove = (e: MouseEvent, id: string, label: string) => {
    openPopup(e.clientX, e.clientY, {
      label: `${label} を棋譜タブから削除`,
      focus: 1,
      items: [
        { label: `「${label}」を削除`, kind: "danger", onPick: () => void remove(id) },
        { label: "やめる", onPick: () => {} },
      ],
    });
  };

  // analyze は棚の 1 局を解析タブへ送る（**棋戦名のリンク**から呼ばれる）。
  //
  // btn は押した相手（リンクもボタン）。⚠️ **送っているあいだ押せなくすること。**
  const analyze = async (id: string, btn: HTMLButtonElement) => {
    setStatus("");
    btn.disabled = true;
    try {
      // ⚠️ **取得元の URL も一緒に渡るのは Go 側の仕事**（`SendToStudy`）。
      // これがあると解析タブの「再読み込み」で中継の最新手を追える。
      opts.onAnalyze(await KifuService.SendToStudy(id));
    } catch (err) {
      setStatus(`解析タブへ送れませんでした: ${String(err)}`, "error");
    } finally {
      btn.disabled = false;
    }
  };

  const cell = (tr: HTMLTableRowElement, value: string, cls = ""): HTMLTableCellElement => {
    const td = document.createElement("td");
    td.textContent = value;
    if (cls) td.className = cls;
    tr.append(td);
    return td;
  };

  const render = (res: SearchResult) => {
    const games = res.games ?? [];
    rows.replaceChildren();
    for (const g of games) {
      const tr = document.createElement("tr");
      tr.dataset.id = g.id;

      cell(tr, formatDate(g.startedAt));

      // 棋戦名のリンクが**解析の口**（2026-09-14。それまでは詳細を開いていた）。
      //
      // ⚠️ **一覧から拾って解析へ送るのがこの面の主目的**なので、**その棋譜
      // そのものである棋戦名**を押すのが解析に当たる。詳細は補助なので、
      // **押し間違えても何も起きない「表示」ボタン**の側へ回してある。
      // ⚠️ **行そのものを押して解析する作りにしないこと** —— 選ぶつもりの操作で
      // 毎回解析タブへ飛ぶ。**押せる場所は棋戦名と「表示」の 2 つだけ。**
      const event = cell(tr, "");
      const link = document.createElement("button");
      link.type = "button";
      link.className = "library-link";
      link.textContent = g.event || "(棋戦名なし)";
      link.title = `${SOURCE_LABELS[g.source] ?? g.source} ／ クリックで解析タブへ`;
      // ⚠️ **押しているあいだは押せなくすること**（`analyze` が自分で外す）。
      // 続けて押すと**同じ棋譜を 2 回送って根が入れ替わる。**
      link.addEventListener("click", () => void analyze(g.id, link));
      event.append(link);

      // ⚠️ **「検討あり」の印を足さないこと**（2026-09-16 に入れて外した）。
      // **ほとんどの棋譜は一度は開く**ので、印を付けると**全部に付いて
      // 情報量がゼロ**になる。**前の検討があれば黙って続きから開く**だけでよい
      // （押した結果で分かる）。

      // 「表示」は**先手より前**（2026-09-14）。⚠️ **行の左端に戻さないこと** ——
      // 左端は一覧を目で追う起点（開始日）で、**そこに押すものがあると、
      // 拾い読みのたびにボタンを避けることになる**。棋戦名のすぐ後ろなら、
      // **「この棋譜の詳細」だと位置で読める。**
      // ⚠️ **「削除」は右端のまま**（間違って押される場所に置かない）。
      const view = document.createElement("td");
      view.className = "is-actions is-view";
      const viewBtn = document.createElement("button");
      viewBtn.type = "button";
      viewBtn.className = "ghost-btn";
      viewBtn.textContent = "表示";
      viewBtn.title = "この棋譜の詳細を出します（解析タブは触りません）";
      viewBtn.addEventListener("click", () => void show(g.id));
      view.append(viewBtn);
      tr.append(view);

      cell(tr, g.black || "-");
      cell(tr, g.white || "-");
      const moves = cell(tr, movesText(g), "is-num");
      if (g.endMark) moves.title = g.endMark;

      const actions = document.createElement("td");
      actions.className = "is-actions";

      const delBtn = document.createElement("button");
      delBtn.type = "button";
      delBtn.className = "danger-btn";
      delBtn.textContent = "削除";
      delBtn.title = "棋譜タブから削除します";
      delBtn.addEventListener("click", (e) => {
        askRemove(e, g.id, g.event || g.black || g.id);
      });

      actions.append(delBtn);
      tr.append(actions);
      rows.append(tr);
    }

    // 件数。条件を付けているときだけ「N / 全体」にする。
    // ⚠️ **並べた行数ではなく該当件数（matched）を出す。** 上限で切られていると
    // 行数は上限そのものになり、「何件あるのか」が分からなくなる。
    count.textContent =
      hasConditions() && res.total > 0
        ? `棋譜一覧（${res.matched} / ${res.total}）`
        : `棋譜一覧（${res.total}）`;

    if (games.length === 0) {
      setStatus(
        hasConditions()
          ? "条件に合う棋譜がありません。"
          : "まだ棋譜がありません。入力タブの「棋譜に登録する」や「中継から取得」で追加してください。",
        "warn",
      );
    } else if (res.truncated) {
      // ⚠️ **上限は Go 側（kicho）が決めている**ので、数値を書かず res.shown を写す。
      // ⚠️ **`shown` は「実際に並んだ件数」**（上限の定数ではない）——
      // limit を指定して呼べばそちらが効くので、定数を出すと画面が嘘をつく。
      setStatus(
        `該当 ${res.matched} 件のうち新しい ${res.shown} 件だけを表示しています。条件で絞り込んでください。`,
        "warn",
      );
    }
  };

  const load = async () => {
    if (busy) return;
    busy = true;
    reload.disabled = true;
    search.disabled = true;
    setStatus("読み込み中…");
    try {
      // ⚠️ **`limit: 0` は「上限は Go 側に任せる」。** 無制限ではない ——
      // 棚は溜め込んでいく前提なので、kicho が MaxSearchRows で切って
      // 切ったことを truncated で返す。**ここに件数を書かないこと。**
      //
      // ⚠️ **件数は Search が一緒に返す。Count を別に呼ばないこと** ——
      // 条件付きの該当件数は検索と同じ条件で数える必要がある。
      const res = await KifuService.Search({
        text: text.value,
        from: from.value,
        to: to.value,
        finishedOnly: finishedOnly.checked,
        limit: 0,
        offset: 0,
      });
      setStatus("");
      render(res);
    } catch (err) {
      // ⚠️ **棚が開けていないのが一番ありうる**（設定タブで場所を直す）。
      // Go 側がその旨のエラーを返すので、そのまま出せば行き先が分かる。
      rows.replaceChildren();
      count.textContent = "棋譜一覧";
      setStatus(String(err), "error");
    } finally {
      busy = false;
      reload.disabled = false;
      search.disabled = false;
    }
  };

  const runSearch = () => {
    showHint();
    closePreview();
    void load();
  };

  search.addEventListener("click", runSearch);
  reload.addEventListener("click", runSearch);
  // ⚠️ **打つたびには検索しない**（Enter か「検索」で実行）。
  // 3 文字未満は全件走査になるので、1 文字ごとに走らせない。
  text.addEventListener("keydown", (e) => {
    if (e.key === "Enter") runSearch();
  });
  for (const el of [from, to, finishedOnly]) {
    el.addEventListener("change", runSearch);
  }
  clear.addEventListener("click", () => {
    text.value = "";
    from.value = "";
    to.value = "";
    finishedOnly.checked = false;
    runSearch();
  });
  modalClose.addEventListener("click", closePreview);
  // Esc で閉じたときも状態を揃える（`<dialog>` は自前で閉じる）。
  modal.addEventListener("close", () => {
    openKif = "";
    modalCopy.classList.remove("is-copied");
  });
  // ⚠️ **クリップボードは Wails ランタイム**（`navigator.clipboard` は secure context
  // 前提で、カスタムスキーム配信のこの webview では当てにできない）。
  modalCopy.addEventListener("click", () => {
    if (!openKif) return;
    void Clipboard.SetText(openKif).then(() => {
      modalCopy.classList.add("is-copied");
      window.setTimeout(() => modalCopy.classList.remove("is-copied"), 900);
    });
  });

  return {
    reveal: () => void load(),
    refresh: () => {
      closePreview();
      void load();
    },
  };
}
