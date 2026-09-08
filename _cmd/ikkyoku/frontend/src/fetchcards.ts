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
// ⚠️ **カードは棚の DB にも残る**（仮の一覧 / `watches`。2026-09-08）。
// 2 日制の対局では翌日また中継の URL を貼り直すことになるので、
// **「どのサイトのどの棋譜か」だけ**を覚えておいて再起動後に並べ直す。
// **KIF 本文は持たない** —— 中身は「更新」で取り直す。棋譜そのものを残すのは
// 「この内容を保存」（棚）の役目で、**役割を混ぜないこと。**
//
// ⚠️ **「取得 URL をコピー」は置かない**（kicho の UI にはある）。あれは
// ShogiHome 等の外部ツールへ渡すためのもので、ikkyoku では渡す先が自分自身。
// **代わりに置くのが「解析する」。**
import { KifuService } from "../bindings/github.com/ShinteLab/ikkyoku/app";
import { openPopup } from "./popup";
import type {
  GameDetail,
  GameSummary,
  KifuLoad,
  WatchEntry,
} from "../bindings/github.com/ShinteLab/ikkyoku/app/models";

export type FetchCardsHandle = {
  // clear は入力とカードを全部捨てる（仮の一覧も空にする）。
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
// 連盟は中継のパス）。保存側が `(source, source_id)` で同一性を見るのと同じ粒度で、
// **仮の一覧の主キーもこの組み合わせ**。
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
  // watched は仮の一覧（棚の DB）に載っているか。
  //
  // 載っていれば再起動しても復元される。**終局済みを保存すると kicho 側が
  // 外す**ので false に戻る（もう取り直す必要がないため）。
  watched: boolean;
  notice: string;
  error: string;
};

// cardFromWatch は仮の一覧の 1 件をカードに戻す（再起動後の復元）。
//
// ⚠️ **KIF 本文は入っていない。** 復元の時点ではサイトへ取りに行かないので、
// 中身が要るときはユーザが「更新」を押す（それまで保存も解析もできない）。
const cardFromWatch = (w: WatchEntry): FetchCard => {
  const game: GameDetail = {
    id: "",
    source: w.source,
    sourceId: w.sourceId,
    sourceUrl: w.sourceUrl,
    event: w.event,
    handicap: "",
    place: "",
    black: w.black,
    white: w.white,
    startedAt: w.startedAt,
    endMark: w.endMark,
    finished: w.finished,
    moves: w.moves,
    kif: "",
    encoding: "",
  };
  return { key: cardKey(game), game, saved: null, watched: true, notice: "", error: "" };
};

// isRestored は「復元しただけで、まだサイトから取り直していない」カードかどうか。
//
// ⚠️ **判定は本文の有無。** 別のフラグを持たせると、更新して中身が入ったのに
// 復元扱いのまま、という食い違いが起きる。
const isRestored = (card: FetchCard): boolean => card.game.kif === "";

// refreshNotice は取り直したときの案内。
//
// **手数の変化を出す**（対局が進んだかどうかが知りたい）。
// ⚠️ **保存済みで手数が変わったら「保存し直す」よう促すこと。** 保存は画面の内容を
// そのまま書き込むので、更新しただけでは棚の中身は古いまま —— 黙っていると
// 「更新＝保存」だと誤解される。
const refreshNotice = (prev: FetchCard, g: GameDetail): string => {
  // 復元しただけのカードは「前と比べて何手増えた」の比較対象にならない
  // （手数のメタは持っているが、中身が無いので初めて取れたのと同じ）。
  if (isRestored(prev)) return "サイトから取り直しました";
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
  const refreshAllBtn = root.querySelector<HTMLButtonElement>("#fetch-refresh-all")!;
  const clearBtn = root.querySelector<HTMLButtonElement>("#fetch-clear")!;
  const status = root.querySelector<HTMLParagraphElement>("#fetch-status")!;
  const host = root.querySelector<HTMLElement>("#fetch-cards")!;

  // ⚠️ **状態はここが持つ**（タブを跨いでも消えない）。
  // **本文（KIF）はここにしか無い** —— 棚の DB に残るのは
  // 「どのサイトのどの棋譜か」と一覧用のメタだけ。
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
      // watched は仮の一覧へ載せてから立てる（remember を参照）。
      cards = [{ key, game: g, saved: null, watched: false, notice: "", error: "" }, ...cards];
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

  // remember は取得したカードを仮の一覧（棚の DB）へ載せる。
  //
  // ⚠️ **載せ損ねても取得結果は画面に残すこと。** 今日の作業は続けられるので
  // 失敗にはせず、「再起動すると消える」ことだけカードに出す
  // （**貼り付け・URL 取り込みは kicho が弾く** —— 取り直す先が無いため）。
  const remember = async (g: GameDetail) => {
    const key = cardKey(g);
    try {
      await KifuService.Watch(g);
      patch(key, { watched: true });
    } catch (err) {
      patch(key, {
        watched: false,
        error: `仮の一覧に残せませんでした（再起動すると消えます）: ${String(err)}`,
      });
    }
  };

  const addMeta = (dl: HTMLElement, label: string, value: string) => {
    const dt = document.createElement("dt");
    dt.textContent = label;
    const dd = document.createElement("dd");
    dd.textContent = value || "-";
    dl.append(dt, dd);
  };

  const busyKeys = new Set<string>();
  // busyAll は「すべて更新」で回っているあいだ（1 枚ずつ順に取りに行く）。
  let busyAll = false;

  const refresh = async (card: FetchCard) => {
    busyKeys.add(card.key);
    render();
    let got: GameDetail;
    try {
      // ⚠️ **入力欄からの `Fetch` ではなく `Refresh`。** 取得元が既に分かっているので、
      // 判別も中継ページ → 棋譜 ID の往復も挟まらない。
      got = await KifuService.Refresh(card.game.source, card.game.sourceId);
      mergeCard(got);
    } catch (err) {
      patch(card.key, { error: `取り直せませんでした: ${String(err)}`, notice: "" });
      return;
    } finally {
      busyKeys.delete(card.key);
    }
    render();
    // 取り直した内容で仮の一覧も最新化する（手数・終局が一覧に出る）。
    await remember(got);
  };

  // refreshAll は並んでいるカードを順に取り直す（復元した直後に使う）。
  //
  // ⚠️ **1 枚ずつ順に。** サイトへ同時に投げない。
  const refreshAll = async () => {
    if (busyAll || cards.length === 0) return;
    busyAll = true;
    setStatus("");
    render();
    try {
      // ⚠️ **回している最中に配列が差し替わる**（mergeCard が作り直す）ので、
      // 走る前の並びを控えてから 1 枚ずつ引き当てる。
      for (const key of cards.map((c) => c.key)) {
        const card = cards.find((c) => c.key === key);
        if (card) await refresh(card);
      }
    } finally {
      busyAll = false;
      render();
    }
  };

  const save = async (card: FetchCard) => {
    busyKeys.add(card.key);
    render();
    try {
      // ⚠️ **画面に出している内容をそのまま保存する**（サイトへ取り直しには行かない）。
      // 最新にしたいなら先に「更新」を押す、という役割分担。
      const rec = await KifuService.Save(card.game);
      // ⚠️ **終局していれば kicho 側が仮の一覧から外す**（もう取り直す必要が無い）。
      // **対局中はそのまま残る** —— 2 日制なら翌日も同じカードで追うので消えては困る。
      // **この分岐を書き換えないこと**（判断を持っているのは `Library.Save`）。
      patch(card.key, {
        saved: rec,
        watched: rec.finished ? false : card.watched,
        notice: rec.finished
          ? "棚に保存しました（終局しているので仮の一覧から外しました）"
          : "棚に保存しました",
        error: "",
      });
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

  // カードを閉じる。**棚の棋譜は消えないが、仮の一覧からは外す。**
  //
  // ⚠️ **外さずに閉じると再起動で戻ってくる**（画面から消えただけになる）。
  const removeCard = async (card: FetchCard) => {
    if (card.watched) {
      try {
        await KifuService.Unwatch(card.game.source, card.game.sourceId);
      } catch (err) {
        patch(card.key, { error: `仮の一覧から外せませんでした: ${String(err)}`, notice: "" });
        return;
      }
    }
    cards = cards.filter((c) => c.key !== card.key);
    render();
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
          onPick: () => void removeCard(card),
        },
        { label: "やめる", onPick: () => {} },
      ],
    });
  };

  const renderCard = (card: FetchCard): HTMLElement => {
    const { game, saved, notice, error } = card;
    // ⚠️ **「通信中…」を出すのは今取りに行っているカードだけ。** 「すべて更新」で
    // 回っているあいだは全部のボタンを塞ぐが、**そこで全部が「通信中…」になると
    // 何枚目を取っているのか分からなくなる。**
    const fetching = busyKeys.has(card.key);
    const busy = fetching || busyAll;
    // 復元しただけで、まだサイトから取り直していないカード（本文が無い）。
    const restored = isRestored(card);

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

    // ⚠️ **仮の一覧に載っていないことは画面に出すこと。** 載っていないカードは
    // 再起動で消えるので、黙っていると「昨日のカードが無い」の理由が分からない。
    if (!card.watched) {
      const warn = document.createElement("span");
      warn.className = "fetch-card-source";
      warn.textContent = "仮の一覧に無し";
      warn.title = "再起動すると消えます（貼り付け・URL 取り込みは取り直せないので載せられません）";
      head.append(warn);
    }

    const refreshBtn = document.createElement("button");
    refreshBtn.type = "button";
    refreshBtn.className = "ghost-btn";
    refreshBtn.textContent = fetching ? "通信中…" : "更新";
    refreshBtn.title = "サイトから取り直してこのカードを最新にします（対局中は棋譜が伸びます）";
    refreshBtn.disabled = busy || !game.sourceId;
    refreshBtn.addEventListener("click", () => void refresh(card));

    const closeBtn = document.createElement("button");
    closeBtn.type = "button";
    closeBtn.className = "danger-btn";
    closeBtn.textContent = "閉じる";
    closeBtn.title = "このカードを画面と仮の一覧から外します（棚の棋譜は消えません）";
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

    if (restored) {
      // ⚠️ **「取れている」ように見せないこと。** 覚えているのは
      // どのサイトのどの棋譜かだけで、棋譜本文はまだ手元に無い。
      const hint = document.createElement("p");
      hint.className = "setting-note";
      hint.textContent =
        "前回のカードを復元しました。棋譜本文はまだありません" +
        "（覚えているのは「どのサイトのどの棋譜か」だけです）。" +
        "「更新」を押すとサイトから取り直し、解析・保存ができるようになります。";
      box.append(hint);
    } else if (!game.finished) {
      // ⚠️ **未終局の注意は出すこと。** 保存は画面の内容をそのまま書き込むので、
      // 「更新してから保存」の順序を知らないと古い棋譜が棚に入る。
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
    // ⚠️ **復元しただけのカードは押せないこと。** 本文が無いので送っても必ず
    // 失敗する（`SendToStudyGame` が断る）。**理由をボタンに出す。**
    analyzeBtn.title = restored
      ? "先に「更新」でサイトから取り直してください（棋譜本文がまだありません）"
      : "この内容を解析タブで開きます（棚には入りません）";
    analyzeBtn.disabled = busy || restored;
    analyzeBtn.addEventListener("click", () => void analyze(card));

    const saveBtn = document.createElement("button");
    saveBtn.type = "button";
    saveBtn.className = "ghost-btn";
    saveBtn.textContent = saved ? "保存し直す" : "この内容を保存";
    saveBtn.title = restored
      ? "先に「更新」でサイトから取り直してください（棋譜本文がまだありません）"
      : "いま表示している内容を棚（棋譜タブ）に書き込みます";
    saveBtn.disabled = busy || restored;
    saveBtn.addEventListener("click", () => void save(card));

    actions.append(analyzeBtn, saveBtn);
    box.append(actions);

    const details = document.createElement("details");
    const summary = document.createElement("summary");
    summary.textContent = "KIF を表示";
    const pre = document.createElement("pre");
    pre.className = "library-kif";
    // ⚠️ **原本をそのまま出す**（整形し直さない）。棚に入るのもこれ。
    pre.textContent = game.kif || "（未取得）";
    details.append(summary, pre);
    box.append(details);

    return box;
  };

  const render = () => {
    host.replaceChildren(...cards.map(renderCard));
    // 「すべて更新」はカードがあるときだけ。回っているあいだは入口を全部塞ぐ
    // （1 枚ずつ順に取りに行くので、横から取得を足されると順番が崩れる）。
    refreshAllBtn.disabled = busyAll || cards.length === 0;
    clearBtn.disabled = busyAll;
    run.disabled = busyAll;
  };

  const fetchNow = async () => {
    const value = input.value.trim();
    if (!value) {
      setStatus("URL または棋譜 ID を入れてください。", "error");
      return;
    }
    run.disabled = true;
    setStatus("取得しています…");
    let got: GameDetail;
    try {
      got = await KifuService.Fetch(value);
      mergeCard(got);
      setStatus("");
      render();
    } catch (err) {
      // ⚠️ **取得そのものの失敗はカードにならない**ので、ここに出す。
      setStatus(`取得できませんでした: ${String(err)}`, "error");
      return;
    } finally {
      run.disabled = false;
    }
    await remember(got);
  };

  run.addEventListener("click", () => void fetchNow());
  refreshAllBtn.addEventListener("click", () => void refreshAll());
  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      void fetchNow();
    }
  });

  // クリアは仮の一覧ごと捨てる。
  //
  // ⚠️ **画面から消すだけにしないこと** —— 再起動でカードが戻ってくる。
  // **棚の棋譜は消えない**（消えるのは「翌日また並べ直す」という約束だけ）。
  const clearAll = async () => {
    if (cards.length > 0) {
      try {
        await KifuService.UnwatchAll();
      } catch (err) {
        setStatus(`仮の一覧を空にできませんでした: ${String(err)}`, "error");
        return;
      }
    }
    input.value = "";
    cards = [];
    setStatus("");
    render();
  };
  clearBtn.addEventListener("click", () => void clearAll());

  // 起動時に仮の一覧からカードを復元する。
  //
  // ⚠️ **ここではサイトへ取りに行かない。** 起動のたびに追跡ぶんの通信が走ると
  // 待たされるうえ、中継を追っていない日でも毎回外へ出ることになる。
  // 中身が要るときはカードの「更新」または「すべて更新」を押す。
  //
  // ⚠️ **棚を開き直したとき（設定タブ）に復元し直していない。** 復元は
  // メタだけで作り直すので、**今日取ったカードの本文（KIF）を捨てることになる。**
  // 閉じれば新しい棚からも外れる（無ければ何も起きない）ので実害は無い。
  const restore = async () => {
    let list: WatchEntry[] | null;
    try {
      list = await KifuService.Watches();
    } catch (err) {
      // ⚠️ **ここで赤字にしないこと** —— 復元できないだけで何かを壊したわけでは
      // ない。直す先（設定タブ）は Go 側の文言が持っている。
      setStatus(`前回のカードを復元できませんでした: ${String(err)}`, "warn");
      return;
    }
    if (!list || list.length === 0) return;
    // ⚠️ **既に取ったカードを上書きしないこと**（復元は本文の無いカード）。
    const known = new Set(cards.map((c) => c.key));
    cards = [...cards, ...list.map(cardFromWatch).filter((c) => !known.has(c.key))];
    render();
  };
  void restore();

  render();
  return { clear: () => void clearAll() };
}
