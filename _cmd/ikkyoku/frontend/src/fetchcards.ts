// 中継から取得（入力タブ）。**対局中の棋譜を追う口。**
//
// 取得元は読売（竜王戦）のスクレイピングと日本将棋連盟の棋譜中継（.kif の直取得）。
// **取り方は違うが、カードに積む・更新する・保存する、の扱いは同じ**にしてある
// （取得元の判別も取得も Go 側の `KifuService` が持つ）。
//
// ⚠️ **取得は 1 件だけ持つ設計にしない。** 同じ日に 2 局以上進むこと（並行対局、
// 複数局を交互に追う）があるので、取得のたびにカードを 1 枚積む。
//
// ⚠️ **同じ棋譜を取り直したときはカードを増やさない。** 対局中は同じ棋譜を
// 繰り返し取りに行くので、key（取得元:棋譜 ID）が一致するカードは位置と
// 「保存済み」を保ったまま中身だけ差し替える。
//
// ⚠️ **「取得 URL をコピー」は置かない**（kicho の UI にはある）。あれは
// ShogiHome 等の外部ツールへ渡すためのもので、ikkyoku では渡す先が自分自身。
// **代わりに置くのが「解析する」。**
import { KifuService } from "../bindings/github.com/ShinteLab/ikkyoku/app";
import { openPopup } from "./popup";
import type { GameDetail, GameSummary, KifuLoad } from "../bindings/github.com/ShinteLab/ikkyoku/app/models";

export type FetchCardsHandle = {
  // clear は入力とカードを全部捨てる。
  clear: () => void;
};

// SOURCE_LABELS はライブ取得できる取得元の表示名。
//
// ⚠️ **ここに載っているものが「ライブ取得できる取得元」**（`Refresh` で
// 取り直せる相手）。URL 取り込み・貼り付けは取得元での一意な ID が無いので入らない。
const SOURCE_LABELS: Record<string, string> = {
  yomiuri: "読売（竜王戦）",
  shogilive: "将棋連盟 中継",
};

// cardKey はカードの識別子。
//
// ⚠️ **取得元も含めること。** 棋譜 ID の形が取得元ごとに違う（読売は 24 桁の ID、
// 連盟は中継のパス）。保存側が `(source, source_id)` で同一性を見るのと同じ粒度。
const cardKey = (g: GameDetail): string => `${g.source}:${g.sourceId}`;

const formatDate = (rfc3339: string): string => {
  if (!rfc3339) return "-";
  const d = new Date(rfc3339);
  if (Number.isNaN(d.getTime())) return "-";
  return d.toLocaleString("ja-JP");
};

// movesText は手数の表示。**0 手かつ未終局は「（対局前）」。**
// 中継は対局開始前から棋譜を置いているので、**取得の失敗ではない。**
const movesText = (g: GameDetail): string => {
  if (g.finished) return `${g.moves}手（終局）`;
  if (g.moves === 0) return "（対局前）";
  return `${g.moves}手`;
};

// FetchCard は取得結果 1 枚。
type FetchCard = {
  key: string;
  game: GameDetail;
  // saved は棚に入れたときの記録（「保存し直す」の出し分けに使う）。
  saved: GameSummary | null;
  notice: string;
  error: string;
};

// refreshNotice は取り直したときの案内。
//
// **手数の変化を出す**（対局が進んだかどうかが知りたい）。
// ⚠️ **保存済みで手数が変わったら「保存し直す」よう促すこと。** 保存は画面の内容を
// そのまま書き込むので、更新しただけでは棚の中身は古いまま —— 黙っていると
// 「更新＝保存」だと誤解される。
const refreshNotice = (prev: FetchCard, g: GameDetail): string => {
  if (g.moves === prev.game.moves) return "最新化しました（手数は変わっていません）";
  const head = `最新化しました（${prev.game.moves} → ${g.moves} 手）`;
  return prev.saved ? `${head}。保存し直すと棚の棋譜も最新になります` : head;
};

export function mountFetchCards(
  root: ParentNode,
  opts: {
    // onAnalyze は「解析する」を押したとき（解析タブへ移って描くのは呼び出し側）。
    onAnalyze: (load: KifuLoad) => void;
    // onSaved は棚に入れたとき（棋譜タブの一覧を取り直す）。
    onSaved: () => void;
  },
): FetchCardsHandle {
  const input = root.querySelector<HTMLInputElement>("#fetch-input")!;
  const run = root.querySelector<HTMLButtonElement>("#fetch-run")!;
  const clearBtn = root.querySelector<HTMLButtonElement>("#fetch-clear")!;
  const status = root.querySelector<HTMLParagraphElement>("#fetch-status")!;
  const host = root.querySelector<HTMLElement>("#fetch-cards")!;

  // ⚠️ **状態はここが持つ**（タブを跨いでも消えない）。
  // **ディスクへは永続化しない**（アプリを閉じればクリアされる）。
  let cards: FetchCard[] = [];

  const setStatus = (msg: string, kind: "" | "error" | "warn" = "") => {
    status.textContent = msg;
    status.hidden = msg === "";
    status.classList.toggle("is-error", kind === "error");
    status.classList.toggle("is-warn", kind === "warn");
  };

  // mergeCard は取得結果をカードへ反映する。
  //
  // ⚠️ **key が一致するカードがあれば増やさず、位置と saved を保って中身だけ
  // 差し替える。** 入力欄からの「取得」もカードの「更新」もここを通す。
  const mergeCard = (g: GameDetail) => {
    const key = cardKey(g);
    const i = cards.findIndex((c) => c.key === key);
    if (i < 0) {
      cards = [{ key, game: g, saved: null, notice: "", error: "" }, ...cards];
      return;
    }
    cards[i] = { ...cards[i], game: g, notice: refreshNotice(cards[i], g), error: "" };
  };

  const patch = (key: string, next: Partial<FetchCard>) => {
    const i = cards.findIndex((c) => c.key === key);
    if (i < 0) return;
    cards[i] = { ...cards[i], ...next };
    render();
  };

  const addMeta = (dl: HTMLElement, label: string, value: string) => {
    const dt = document.createElement("dt");
    dt.textContent = label;
    const dd = document.createElement("dd");
    dd.textContent = value || "-";
    dl.append(dt, dd);
  };

  const busyKeys = new Set<string>();

  const refresh = async (card: FetchCard) => {
    busyKeys.add(card.key);
    render();
    try {
      // ⚠️ **入力欄からの `Fetch` ではなく `Refresh`。** 取得元が既に分かっているので、
      // 判別も中継ページ → 棋譜 ID の往復も挟まらない。
      mergeCard(await KifuService.Refresh(card.game.source, card.game.sourceId));
    } catch (err) {
      patch(card.key, { error: `取り直せませんでした: ${String(err)}`, notice: "" });
      return;
    } finally {
      busyKeys.delete(card.key);
    }
    render();
  };

  const save = async (card: FetchCard) => {
    busyKeys.add(card.key);
    render();
    try {
      // ⚠️ **画面に出している内容をそのまま保存する**（サイトへ取り直しには行かない）。
      // 最新にしたいなら先に「更新」を押す、という役割分担。
      const rec = await KifuService.Save(card.game);
      patch(card.key, { saved: rec, notice: "棚に保存しました", error: "" });
      opts.onSaved();
    } catch (err) {
      patch(card.key, { error: `保存できませんでした: ${String(err)}`, notice: "" });
    } finally {
      busyKeys.delete(card.key);
      render();
    }
  };

  const analyze = async (card: FetchCard) => {
    busyKeys.add(card.key);
    render();
    try {
      // ⚠️ **棚を通さない。** 取得しただけの棋譜も解析できる（二系統を残す方針）。
      opts.onAnalyze(await KifuService.SendToStudyGame(card.game));
    } catch (err) {
      patch(card.key, { error: `解析タブへ送れませんでした: ${String(err)}`, notice: "" });
    } finally {
      busyKeys.delete(card.key);
      render();
    }
  };

  // 削除の確認。**カードを画面から外すだけで、棚の棋譜は消えない。**
  //
  // ⚠️ **消える範囲が違うので、そう書くこと** —— 棚の削除（棋譜タブ）と
  // 見た目が同じ赤いボタンなので、何が消えるのかを文言で分ける。
  const askRemove = (e: MouseEvent, card: FetchCard) => {
    openPopup(e.clientX, e.clientY, {
      label: "このカードを閉じる",
      focus: 1,
      items: [
        {
          label: "カードを閉じる（棚の棋譜は消えません）",
          kind: "danger",
          onPick: () => {
            cards = cards.filter((c) => c.key !== card.key);
            render();
          },
        },
        { label: "やめる", onPick: () => {} },
      ],
    });
  };

  const renderCard = (card: FetchCard): HTMLElement => {
    const { game, saved, notice, error } = card;
    const busy = busyKeys.has(card.key);

    const box = document.createElement("div");
    box.className = "fetch-card";

    const head = document.createElement("div");
    head.className = "fetch-card-head";
    const title = document.createElement("span");
    title.className = "fetch-card-title";
    title.textContent = game.event || "(棋戦名なし)";
    const tag = document.createElement("span");
    tag.className = "fetch-card-source";
    tag.textContent = SOURCE_LABELS[game.source] ?? game.source;
    head.append(title, tag);

    const refreshBtn = document.createElement("button");
    refreshBtn.type = "button";
    refreshBtn.className = "ghost-btn";
    refreshBtn.textContent = busy ? "通信中…" : "更新";
    refreshBtn.title = "サイトから取り直してこのカードを最新にします（対局中は棋譜が伸びます）";
    refreshBtn.disabled = busy || !game.sourceId;
    refreshBtn.addEventListener("click", () => void refresh(card));

    const closeBtn = document.createElement("button");
    closeBtn.type = "button";
    closeBtn.className = "danger-btn";
    closeBtn.textContent = "閉じる";
    closeBtn.title = "このカードを画面から外します（棚の棋譜は消えません）";
    closeBtn.disabled = busy;
    closeBtn.addEventListener("click", (e) => askRemove(e, card));
    head.append(refreshBtn, closeBtn);
    box.append(head);

    const meta = document.createElement("dl");
    meta.className = "library-meta";
    addMeta(meta, "先手", game.black);
    addMeta(meta, "後手", game.white);
    addMeta(meta, "開始", formatDate(game.startedAt));
    addMeta(meta, "場所", game.place);
    addMeta(meta, "手数", movesText(game));
    box.append(meta);

    // ⚠️ **未終局の注意は出すこと。** 保存は画面の内容をそのまま書き込むので、
    // 「更新してから保存」の順序を知らないと古い棋譜が棚に入る。
    if (!game.finished) {
      const hint = document.createElement("p");
      hint.className = "setting-note";
      hint.textContent =
        "まだ終局していません。保存はいま表示している内容をそのまま書き込むので、" +
        "最新を棚に入れたいときは先に「更新」を押してください（同じ棋譜なら増えません）。";
      box.append(hint);
    }

    if (error) {
      const p = document.createElement("p");
      p.className = "status is-error";
      p.textContent = error;
      box.append(p);
    } else if (notice) {
      const p = document.createElement("p");
      p.className = "status";
      p.textContent = notice;
      box.append(p);
    }

    const actions = document.createElement("div");
    actions.className = "fetch-card-actions";

    const analyzeBtn = document.createElement("button");
    analyzeBtn.type = "button";
    analyzeBtn.className = "ghost-btn is-primary";
    analyzeBtn.textContent = "解析する";
    analyzeBtn.title = "この内容を解析タブで開きます（棚には入りません）";
    analyzeBtn.disabled = busy;
    analyzeBtn.addEventListener("click", () => void analyze(card));

    const saveBtn = document.createElement("button");
    saveBtn.type = "button";
    saveBtn.className = "ghost-btn";
    saveBtn.textContent = saved ? "保存し直す" : "この内容を保存";
    saveBtn.title = "いま表示している内容を棚（棋譜タブ）に書き込みます";
    saveBtn.disabled = busy;
    saveBtn.addEventListener("click", () => void save(card));

    actions.append(analyzeBtn, saveBtn);
    box.append(actions);

    const details = document.createElement("details");
    const summary = document.createElement("summary");
    summary.textContent = "KIF を表示";
    const pre = document.createElement("pre");
    pre.className = "library-kif";
    // ⚠️ **原本をそのまま出す**（整形し直さない）。棚に入るのもこれ。
    pre.textContent = game.kif;
    details.append(summary, pre);
    box.append(details);

    return box;
  };

  const render = () => {
    host.replaceChildren(...cards.map(renderCard));
  };

  const fetchNow = async () => {
    const value = input.value.trim();
    if (!value) {
      setStatus("URL または棋譜 ID を入れてください。", "error");
      return;
    }
    run.disabled = true;
    setStatus("取得しています…");
    try {
      mergeCard(await KifuService.Fetch(value));
      setStatus("");
      render();
    } catch (err) {
      // ⚠️ **取得そのものの失敗はカードにならない**ので、ここに出す。
      setStatus(`取得できませんでした: ${String(err)}`, "error");
    } finally {
      run.disabled = false;
    }
  };

  run.addEventListener("click", () => void fetchNow());
  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      void fetchNow();
    }
  });

  const clearAll = () => {
    input.value = "";
    cards = [];
    setStatus("");
    render();
  };
  clearBtn.addEventListener("click", clearAll);

  return { clear: clearAll };
}
