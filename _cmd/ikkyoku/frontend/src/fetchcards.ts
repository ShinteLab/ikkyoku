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
// 繰り返し取りに行くので、key（取得元:棋譜 ID）が一致するカードは位置を
// 保ったまま中身だけ差し替える。
//
// ⚠️ **カードは棚の DB にも残る**（仮の一覧 / `watches`。2026-09-08）。
// 2 日制の対局では翌日また中継の URL を貼り直すことになるので、
// **「どのサイトのどの棋譜か」だけ**を覚えておいて再起動後に並べ直す。
// **KIF 本文は持たない** —— 中身は「更新」で取り直す。棋譜そのものを残すのは
// 「棋譜に保存」（棚）の役目で、**役割を混ぜないこと。**
//
// ⚠️ **「棋譜に保存」したらカードを削除する**（2026-11-18）。棋譜タブに入った棋譜はそちらで
// 扱う（解析も棋譜タブから）ので、カードに残す理由が無い。仮の一覧からも外す。
//
// ⚠️ **「取得 URL をコピー」は置かない**（kicho の UI にはある）。あれは
// ShogiHome 等の外部ツールへ渡すためのもので、ikkyoku では渡す先が自分自身。
// **代わりに置くのが「解析する」。**
//
// **貼り付けた棋譜もカードになる**（2026-11-18。「貼り付け」・「ファイルを開く…」・ドラッグ＆ドロップ）。
// 取得したカードとの違いは**取りに行く先が無いこと**だけ —— ⚠️ **「更新」を出さない**・
// ⚠️ **仮の一覧に載せない**（再起動すると消える。kicho も弾く）。行き先は同じ 2 つ
// （「解析する」→ 解析タブ / 「棋譜に保存」→ 棋譜タブ）。
import { Events } from "@wailsio/runtime";
import { KifuService, StudyService } from "../bindings/github.com/ShinteLab/ikkyoku/app";
import { openPopup } from "./popup";
import type {
  GameDetail,
  GameSummary,
  KifuLoad,
  PastedKifu,
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
  shogidb2: "将棋DB2",
  fuwalete: "ふわりと将棋",
  url: "URL",
  paste: "貼り付け",
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

// FetchCard は取得結果 1 枚（貼り付けた棋譜も同じ形。`pasted`）。
type FetchCard = {
  key: string;
  game: GameDetail;
  // pasted は貼り付けた（開いた）棋譜のカードか（2026-11-18）。**取りに行く先が無い**ので
  // 「更新」を出さず、仮の一覧にも載せない。⚠️ **鍵は Go が本文から作る**（`PastedKifu.key`）。
  pasted: boolean;
  // origin は貼り付けたカードの出どころ（"貼り付け" かファイル名）。取得したカードは空。
  origin: string;
  // watched は仮の一覧（棚の DB）に載っているか。
  //
  // 載っていれば再起動しても復元される。**「棋譜に保存」するとカードごと外す。**
  watched: boolean;
  notice: string;
  error: string;
};

// cardFromWatch は仮の一覧の 1 件をカードに戻す（再起動後の復元）。
//
// ⚠️ **KIF 本文は入っていない。** 復元したあと裏で取り直す（`restore`）が、取れるまで・
// 取れなかったときは本文が無い（保存はそれまでできない。「更新」で取り直せる）。
// ⚠️ **解析だけは直行できる** —— 「解析する」が先に取り直す（analyze を参照）。
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
    // ⚠️ **中継のカードは人が書く欄を持たない**（2026-10-02）。直せるのは
    // 棋譜タブに入った棋譜だけ（`KifuService.Annotate`）で、仮の一覧は対象外。
    eventEdited: "",
    note: "",
    kif: "",
    encoding: "",
  };
  return {
    key: cardKey(game),
    game,
    pasted: false,
    origin: "",
    watched: true,
    notice: "",
    error: "",
  };
};

// isRestored は「復元しただけで、まだサイトから取り直していない」カードかどうか。
//
// ⚠️ **判定は本文の有無。** 別のフラグを持たせると、更新して中身が入ったのに
// 復元扱いのまま、という食い違いが起きる。
const isRestored = (card: FetchCard): boolean => !card.pasted && card.game.kif === "";

// refreshNotice は取り直したときの案内。
//
// **手数の変化を出す**（対局が進んだかどうかが知りたい）。
// ⚠️ **保存したカードはもう無い**（2026-11-18。保存すると削除する）ので、「保存し直す」の案内も無い。
const refreshNotice = (prev: FetchCard, g: GameDetail): string => {
  // 復元しただけのカードは「前と比べて何手増えた」の比較対象にならない
  // （手数のメタは持っているが、中身が無いので初めて取れたのと同じ）。
  if (isRestored(prev)) return "サイトから取り直しました";
  if (g.moves === prev.game.moves) return "最新化しました（手数は変わっていません）";
  return `最新化しました（${prev.game.moves} → ${g.moves} 手）`;
};

// FetchCardsHandle は入力タブから呼ぶ口（2026-11-18）。
//
// ⚠️ **どれも失敗は投げる**（理由の文つきの Error）。どこへ出すかは呼び出し側が決める ——
// 「貼り付け」・ファイルは群の状態の行、「手入力」はダイアログの中（直して出し直せるように）。
export interface FetchCardsHandle {
  // addText は貼り付けた・手入力した文字をカードにする（2026-11-18）。**URL・棋譜 ID なら取得したカード、
  // KIF / CSA の本文なら貼り付けたカード。** ⚠️ **見分けは Go**（`KifuService.IsKifuRef`。画面に書かない）。
  // origin は本文だったときのカードの札（"貼り付け" / "手入力"）。
  addText(text: string, origin: string): Promise<void>;
  // addPasted は本文をそのまま貼り付けたカードにする（ファイル・ドロップ。origin はファイル名）。
  // ⚠️ **棋譜かどうかの判定も対局者などの読み取りも Go 側**（`KifuService.PreviewPaste`）。
  addPasted(text: string, origin: string): Promise<void>;
  // setStatus は群の状態の行（#fetch-status）。⚠️ **群が 1 つなので行も 1 つ** —— 貼り付けの
  // 失敗もここへ出す（入力タブに別の行を作らない）。
  setStatus(msg: string, kind?: "" | "error" | "warn"): void;
}

export function mountFetchCards(
  root: ParentNode,
  opts: {
    // onAnalyze は「解析する」を押したとき（解析タブへ移って描くのは呼び出し側）。
    onAnalyze: (load: KifuLoad) => void;
    // confirmAnalyze は**送る前に聞く**（中継を追っているなら止めてよいか。偽ならやめる）。
    confirmAnalyze?: () => Promise<boolean>;
    // onSaved は棚に入れたとき（棋譜タブの一覧を取り直す）。
    onSaved: () => void;
  },
): FetchCardsHandle {
  // ⚠️ **URL の入力欄は無い**（2026-11-18）。URL も本文も「貼り付け」か「手入力」で入ってきて、
  // ここは `addText` で受け取るだけ。**URL からできることは「取得」だけ** —— 解析も登録も**カード**が持つ。
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
  // ⚠️ **key が一致するカードがあれば増やさず、位置を保って中身だけ
  // 差し替える。** 入力欄からの「取得」もカードの「更新」もここを通す。
  const mergeCard = (g: GameDetail) => {
    const key = cardKey(g);
    const i = cards.findIndex((c) => c.key === key);
    if (i < 0) {
      // watched は仮の一覧へ載せてから立てる（remember を参照）。
      cards = [
        { key, game: g, pasted: false, origin: "", watched: false, notice: "", error: "" },
        ...cards,
      ];
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

  // save はカードの棋譜を棋譜タブへ入れて、**カードを削除する**（2026-11-18。「棋譜に保存」）。
  //
  // ⚠️ **画面に出している内容をそのまま保存する**（サイトへ取り直しには行かない）。
  // 最新にしたいなら先に「更新」を押す、という役割分担。
  // 取得したカードは `Save`（取得元で upsert。同じ棋譜を保存し直しても増えない）、
  // 貼り付けたカードは `ImportKIF`（毎回新しい棋譜。開いたファイルのままなら元の文字コードも残る）。
  // ⚠️ **削除するときは仮の一覧からも外す**（外さないと再起動で戻ってくる。`removeCard`）。
  // 終局していれば kicho の `Library.Save` が先に外しているが、もう一度外しても失敗にはならない。
  // ⚠️ **削除したあとの返事は群の状態の行**（カードはもう無い）。
  const save = async (card: FetchCard) => {
    busyKeys.add(card.key);
    render();
    let rec: GameSummary;
    try {
      rec = card.pasted
        ? await KifuService.ImportKIF(card.game.kif)
        : await KifuService.Save(card.game);
    } catch (err) {
      busyKeys.delete(card.key);
      patch(card.key, { error: `棋譜に保存できませんでした: ${String(err)}`, notice: "" });
      return;
    }
    opts.onSaved();
    busyKeys.delete(card.key);
    const failed = await removeCard(card);
    if (failed) {
      // ⚠️ **保存はできている**ので、そう書く（「保存できなかった」に見せない）。
      patch(card.key, {
        error: `棋譜タブに保存しましたが、仮の一覧から外せませんでした（再起動すると戻ります）: ${failed}`,
        notice: "",
      });
      return;
    }
    const who = [rec.black, rec.white].filter(Boolean).join(" - ");
    setStatus(`棋譜タブに保存しました: ${[rec.event, who].filter(Boolean).join(" / ") || "(棋戦名なし)"}`);
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
    if (opts.confirmAnalyze && !(await opts.confirmAnalyze())) {
      return;
    }
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
      // ⚠️ **貼り付けたカードは本文の口**（`LoadKifu`。前に同じ本文を解析していれば続きから開く）。
      opts.onAnalyze(
        card.pasted
          ? await StudyService.LoadKifu(game.kif)
          : await KifuService.SendToStudyGame(game),
      );
    } catch (err) {
      patch(card.key, { error: `解析タブへ送れませんでした: ${String(err)}`, notice: "" });
    } finally {
      busyKeys.delete(card.key);
      render();
    }
  };

  // removeCard はカードを削除する。**棚の棋譜は消えないが、仮の一覧からは外す。**
  // 外せなければカードを残して理由を返す（削除できたら null）。
  //
  // ⚠️ **外さずに消すと再起動で戻ってくる**（画面から消えただけになる）。
  const removeCard = async (card: FetchCard): Promise<string | null> => {
    if (card.watched) {
      try {
        await KifuService.Unwatch(card.game.source, card.game.sourceId);
      } catch (err) {
        return String(err);
      }
    }
    cards = cards.filter((c) => c.key !== card.key);
    render();
    return null;
  };

  // 削除の確認。**カードと仮の一覧から外すだけで、棚の棋譜は消えない。**
  //
  // ⚠️ **文言は「削除する」だけ**（2026-11-18）。以前は「棋譜タブの棋譜は消えません」と添えていたが、
  // 保存したカードは消えるようになったので、残っているカードは棋譜タブに入れていないもの ——
  // 添える理由が無くなった。⚠️ **初期フォーカスは「やめる」**（消す操作なので、Enter の連打で消えない）。
  const askRemove = (e: MouseEvent, card: FetchCard) => {
    openPopup(e.clientX, e.clientY, {
      label: "このカードを削除",
      focus: 1,
      items: [
        {
          label: "削除する",
          kind: "danger",
          onPick: () =>
            void removeCard(card).then((failed) => {
              if (failed) {
                patch(card.key, { error: `仮の一覧から外せませんでした: ${failed}`, notice: "" });
              }
            }),
        },
        { label: "やめる", onPick: () => {} },
      ],
    });
  };

  const renderCard = (card: FetchCard): HTMLElement => {
    const { game, notice, error } = card;
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
    tag.textContent = card.origin || (SOURCE_LABELS[game.source] ?? game.source);
    head.append(title, tag);

    // ⚠️ **仮の一覧に載っていないことは画面に出すこと。** 載っていないカードは
    // 再起動で消えるので、黙っていると「昨日のカードが無い」の理由が分からない。
    // ⚠️ **貼り付けたカードには出さない**（載せられないのが当たり前で、いつも出ていると見慣れる）。
    if (!card.watched && !card.pasted) {
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

    const deleteBtn = document.createElement("button");
    deleteBtn.type = "button";
    deleteBtn.className = "danger-btn";
    deleteBtn.textContent = "削除";
    deleteBtn.disabled = busy;
    deleteBtn.addEventListener("click", (e) => askRemove(e, card));
    // ⚠️ **貼り付けたカードに「更新」を出さない**（取りに行く先が無い）。
    if (card.pasted) {
      head.append(deleteBtn);
    } else {
      head.append(refreshBtn, deleteBtn);
    }
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
        "「解析する」なら取り直してからそのまま進めます。" +
        "棋譜タブに入れるときは先に「更新」を押してください。";
      box.append(hint);
    } else if (!game.finished && !card.pasted) {
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
    // サイトから取り直してから送る）。
    // ⚠️ **ラベルは「解析する」のまま**（2026-11-18。以前は「取り直して解析」だったが、
    // 取り直さずに解析する道が無いので言い分ける意味が無い）。
    // ⚠️ **取り直しているあいだだけはラベルを変えること** —— 外へ取りに行くので数秒かかる。
    analyzeBtn.textContent = fetching && restored ? "取り直しています…" : "解析する";
    analyzeBtn.title = restored
      ? "サイトから取り直してから解析タブで開きます（棋譜タブには入りません）"
      : card.pasted
        ? "この棋譜を解析タブで開きます（棋譜タブには入りません）"
        : "この内容を解析タブで開きます（棋譜タブには入りません。最新手は「更新」してから）";
    // ⚠️ **`sourceId` を見るのは取り直すときだけ。** 本文があるカードは
    // 送るだけなので、取得元の ID は要らない。
    analyzeBtn.disabled = busy || (restored && !game.sourceId);
    analyzeBtn.addEventListener("click", () => void analyze(card));

    const saveBtn = document.createElement("button");
    saveBtn.type = "button";
    saveBtn.className = "ghost-btn";
    // ⚠️ **取得したカードも貼り付けたカードも「棋譜に保存」**（2026-11-18。押すとカードを削除する。`save`）。
    saveBtn.textContent = "棋譜に保存";
    // ⚠️ **保存は「解析する」と違って、取り直しに行かない。** 保存は
    // **画面の内容をそのまま書き込む**操作だから（上の「未終局の注意」と同根）で、
    // 本文の無いカードは押せないままにしてある。
    saveBtn.title = restored
      ? "先に「更新」でサイトから取り直してください（棋譜本文がまだありません）"
      : card.pasted
        ? "この棋譜を棋譜タブに保存して、カードを削除します"
        : "いま表示している内容を棋譜タブに保存して、カードを削除します";
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

  // fetchRef は URL・棋譜 ID から取得してカードに積み、仮の一覧へ載せる。
  // ⚠️ **取得そのものの失敗はカードにならない**ので投げる（呼び出し側が出す）。
  // ⚠️ **仮の一覧へ載せ損ねても失敗にしない**（`remember` がカードに出す）。
  const fetchRef = async (value: string) => {
    let got: GameDetail;
    try {
      got = await KifuService.Fetch(value);
    } catch (err) {
      throw new Error(`取得できませんでした: ${String(err)}`);
    }
    mergeCard(got);
    render();
    await remember(got);
  };

  // addPasted は本文を貼り付けたカードにする。
  // ⚠️ **読めなかったものはカードにしない**（投げる）。
  const addPasted = async (text: string, origin: string) => {
    let got: PastedKifu;
    try {
      got = await KifuService.PreviewPaste(text);
    } catch (err) {
      throw new Error(`棋譜として読めませんでした: ${String(err)}`);
    }
    const i = cards.findIndex((c) => c.key === got.key);
    if (i < 0) {
      cards = [
        {
          key: got.key,
          game: got.game,
          pasted: true,
          origin,
          watched: false,
          notice: "",
          error: "",
        },
        ...cards,
      ];
    } else {
      // ⚠️ **同じ本文ならカードを増やさない**（位置を保つ。取得したカードと同じ約束）。
      cards[i] = { ...cards[i], notice: "同じ棋譜のカードがあります（増やしていません）", error: "" };
    }
    render();
  };

  // 起動時に仮の一覧からカードを復元し、**続けてサイトから取り直す**（2026-11-18）。
  //
  // 以前は「起動のたびに通信が走ると待たされる・中継を追っていない日も外へ出る」ので
  // 取りに行かず、「更新」を押させていた。⚠️ **待たせないこと** —— 取り直しは復元して
  // 描いたあとに裏で回し、画面はその間も使える（カードは 1 枚ずつ「通信中…」になる）。
  // ⚠️ **1 枚ずつ順に取りに行く**（同じサイトへまとめて投げない。カードは 2〜3 枚が普通）。
  // ⚠️ **取れなかったカードは復元したまま残す**（理由はカードに出る。「更新」で取り直せる）。
  //
  // ⚠️ **棚を開き直したとき（設定タブ）に復元し直していない。** 復元は
  // メタだけで作り直すので、**今日取ったカードの本文（KIF）を捨てることになる。**
  // 削除すれば新しい棚からも外れる（無ければ何も起きない）ので実害は無い。
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
    const restored = list.map(cardFromWatch).filter((c) => !known.has(c.key));
    cards = [...cards, ...restored];
    render();
    for (const r of restored) {
      // ⚠️ **その間に人が触ったカードは飛ばす**（削除した・保存した・自分で更新や解析を押した）。
      // 削除したカードを取り直すと `mergeCard` がカードを積み直してしまう。
      const card = cards.find((c) => c.key === r.key);
      if (!card || busyKeys.has(card.key) || !isRestored(card)) continue;
      await refresh(card);
    }
  };
  void restore();

  // 解析タブが URL から取り直して本譜が変わった（2026-11-18。Go の `kifu:reloaded`）。
  // **同じ棋譜のカードも取り直す** —— 40 手で解析を始めて自動更新で 60 手まで進んでも、
  // カードが 40 手のまま残らないように。
  // ⚠️ **解析タブの本文を写さず、カードの口（`Refresh`）で取り直す**（カードの中身は取得元ごとに
  // kicho が組み立てるので、解析タブが読んだ本文とは作りが違いうる）。そのぶん通信は 1 回増えるが、
  // Go は**本譜が変わったときだけ**知らせてくる（手が来ていない自動更新では来ない）。
  // ⚠️ **取り直している最中のカードは飛ばす**（同じ取り直しを重ねない）。
  // `kifu:reloaded` の中身（Go の `app.KifuReloaded`）。⚠️ **bindings には出ない**ので形を手書きする。
  type KifuReloaded = { source: string; sourceId: string; moves: number };
  Events.On("kifu:reloaded", (event: { data: KifuReloaded }) => {
    const key = `${event.data.source}:${event.data.sourceId}`;
    const card = cards.find((c) => c.key === key);
    if (!card || card.pasted || busyKeys.has(key)) return;
    void refresh(card);
  });

  render();

  return {
    addText: async (text, origin) => {
      const value = text.trim();
      if (!value) {
        throw new Error("棋譜が空です。URL・棋譜 ID か、KIF / KI2 / CSA の棋譜を入れてください。");
      }
      if (await KifuService.IsKifuRef(value)) {
        await fetchRef(value);
        return;
      }
      await addPasted(value, origin);
    },
    addPasted,
    setStatus,
  };
}
