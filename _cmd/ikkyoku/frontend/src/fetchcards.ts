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

// SOURCE_LABELS は取得元の表示名。
//
// ⚠️ **載っていない取得元も来る**（2026-09-12）。URL 欄はどのサイトでも受けるので、
// 連盟・読売以外の .kif は `url` で返ってくる（`Refresh` で取り直せる ——
// あちらの `source_id` は URL そのもの）。**出せないのは名前だけ**なので、
// 引き当たらなければ取得元の値をそのまま出す（`?? game.source`）。
// ⚠️ **ここに無い取得元をエラー扱いにしないこと。**
const SOURCE_LABELS: Record<string, string> = {
  yomiuri: "読売（竜王戦）",
  shogilive: "将棋連盟 中継",
  url: "URL",
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
// 中身が要るときは「更新」を押す（保存はそれまでできない）。
// ⚠️ **解析だけは直行できる** —— 「取り直して解析」が先に取り直す（analyze を参照）。
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
  return prev.saved ? `${head}。保存し直すと棋譜タブの棋譜も最新になります` : head;
};

export function mountFetchCards(
  root: ParentNode,
  opts: {
    // onAnalyze は「解析する」を押したとき（解析タブへ移って描くのは呼び出し側）。
    onAnalyze: (load: KifuLoad) => void;
    // onSaved は棚に入れたとき（棋譜タブの一覧を取り直す）。
    onSaved: () => void;
  },
) {
  // ⚠️ **入力欄は「棋譜の URL から」の 1 つだけで、持ち主はここ**（2026-09-12）。
  // **`#fetch-input` に戻さないこと** —— 同じ URL を入れる場所が 2 か所あると、
  // どちらに入れたかで通る道が変わる（それを畳んだのがこの変更）。
  // ⚠️ **URL からできることは「取得」だけ。** 解析も登録も**カード**が持つので、
  // `mainscreen.ts` はこの欄を読まない（読む側が 2 つあると、どちらの値で
  // 動いたのかが追えなくなる）。
  const input = root.querySelector<HTMLInputElement>("#kifu-url")!;
  const run = root.querySelector<HTMLButtonElement>("#fetch-run")!;
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

  // busyKeys は今サイトへ取りに行っているカード。
  //
  // ⚠️ **1 枚ずつ塞ぐ**（2026-09-12 に「すべて更新」を外したので、全部を
  // まとめて塞ぐ状態が無くなった）。**カードは 2〜3 枚が普通**で、まとめて
  // 回す口は要らない —— 1 枚ずつ「更新」を押す。
  const busyKeys = new Set<string>();

  // fetchInto はサイトから取り直してカードへ反映する（取れたら新しい中身を返す）。
  //
  // ⚠️ **busy の出し入れをここでしないこと。** 「更新」からも「解析する」からも
  // 通るので、塞ぐのは押した側の都合（解析は取り直したあとにまだ続きがある）。
  const fetchInto = async (card: FetchCard): Promise<GameDetail | null> => {
    let got: GameDetail;
    try {
      // ⚠️ **入力欄からの `Fetch` ではなく `Refresh`。** 取得元が既に分かっているので、
      // 判別も中継ページ → 棋譜 ID の往復も挟まらない。
      got = await KifuService.Refresh(card.game.source, card.game.sourceId);
    } catch (err) {
      patch(card.key, { error: `取り直せませんでした: ${String(err)}`, notice: "" });
      return null;
    }
    mergeCard(got);
    render();
    // 取り直した内容で仮の一覧も最新化する（手数・終局が一覧に出る）。
    await remember(got);
    return got;
  };

  const refresh = async (card: FetchCard) => {
    busyKeys.add(card.key);
    render();
    try {
      await fetchInto(card);
    } finally {
      busyKeys.delete(card.key);
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
          ? "棋譜タブに保存しました（終局しているので仮の一覧から外しました）"
          : "棋譜タブに保存しました",
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

  // analyze はカードの棋譜を解析タブへ送る。
  //
  // ⚠️ **本文が無ければ、先にサイトから取り直す**（2026-09-09。
  // 後述の「復元したカード」がそれ）。以前は「更新」を押すまで
  // 押せなくしていたが、**押せなかった理由は「送っても必ず失敗する」だけ**で、
  // 取り直してから送ればその理由は消える。人に「更新 → 解析」と
  // 2 回押させる意味が無い。
  //
  // ⚠️ **本文があるカードでは取り直しに行かないこと。** 送るのは
  // 「いま画面に出ているこの内容」という約束（保存と同じ）で、
  // 最新手を追うのは「更新」か解析タブの「再読み込み」の役目。
  const analyze = async (card: FetchCard) => {
    busyKeys.add(card.key);
    render();
    try {
      let game = card.game;
      if (isRestored(card)) {
        const got = await fetchInto(card);
        // ⚠️ **取れなかったら解析へ進まない。** 理由は fetchInto がカードに
        // 出しているので、ここで書き直すと「送れなかった」で上書きして
        // **取得に失敗したのか送れなかったのかが分からなくなる**。
        if (!got) return;
        game = got;
      }
      // ⚠️ **棚を通さない。** 取得しただけの棋譜も解析できる（二系統を残す方針）。
      opts.onAnalyze(await KifuService.SendToStudyGame(game));
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
          label: "カードを閉じる（棋譜タブの棋譜は消えません）",
          kind: "danger",
          onPick: () => void removeCard(card),
        },
        { label: "やめる", onPick: () => {} },
      ],
    });
  };

  const renderCard = (card: FetchCard): HTMLElement => {
    const { game, saved, notice, error } = card;
    // 「通信中…」を出すのは今取りに行っているカードだけ。
    const fetching = busyKeys.has(card.key);
    const busy = fetching;
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
    closeBtn.title = "このカードを画面と仮の一覧から外します（棋譜タブの棋譜は消えません）";
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
        "「取り直して解析」ならそのまま進めます。" +
        "棋譜タブに入れるときは先に「更新」を押してください。";
      box.append(hint);
    } else if (!game.finished) {
      // ⚠️ **未終局の注意は出すこと。** 保存は画面の内容をそのまま書き込むので、
      // 「更新してから保存」の順序を知らないと古い棋譜が棚に入る。
      const hint = document.createElement("p");
      hint.className = "setting-note";
      hint.textContent =
        "まだ終局していません。保存はいま表示している内容をそのまま書き込むので、" +
        "最新を棋譜タブに入れたいときは先に「更新」を押してください（同じ棋譜なら増えません）。";
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
    // ⚠️ **復元しただけのカードでも押せる**（2026-09-09。押したときに
    // サイトから取り直してから送る）。⚠️ **そのときはラベルを変えること** ——
    // 外へ取りに行くので数秒かかる。**同じ「解析する」のままにしないこと。**
    analyzeBtn.textContent = fetching && restored
      ? "取り直しています…"
      : restored
        ? "取り直して解析"
        : "解析する";
    analyzeBtn.title = restored
      ? "サイトから取り直してから解析タブで開きます（棋譜タブには入りません）"
      : "この内容を解析タブで開きます（棋譜タブには入りません。最新手は「更新」してから）";
    // ⚠️ **`sourceId` を見るのは取り直すときだけ。** 本文があるカードは
    // 送るだけなので、取得元の ID は要らない。
    analyzeBtn.disabled = busy || (restored && !game.sourceId);
    analyzeBtn.addEventListener("click", () => void analyze(card));

    const saveBtn = document.createElement("button");
    saveBtn.type = "button";
    saveBtn.className = "ghost-btn";
    saveBtn.textContent = saved ? "保存し直す" : "この内容を保存";
    // ⚠️ **保存は「解析する」と違って、取り直しに行かない。** 保存は
    // **画面の内容をそのまま書き込む**操作だから（上の「未終局の注意」と同根）で、
    // 本文の無いカードは押せないままにしてある。
    saveBtn.title = restored
      ? "先に「更新」でサイトから取り直してください（棋譜本文がまだありません）"
      : "いま表示している内容を棋譜タブに書き込みます";
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
  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      void fetchNow();
    }
  });

  // 起動時に仮の一覧からカードを復元する。
  //
  // ⚠️ **ここではサイトへ取りに行かない。** 起動のたびに追跡ぶんの通信が走ると
  // 待たされるうえ、中継を追っていない日でも毎回外へ出ることになる。
  // 中身が要るときはカードの「更新」を押す（または「取り直して解析」）。
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
}
