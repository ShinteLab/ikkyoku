// 入力タブ（2026-11-13 に `mainscreen.ts` から切り出した）。
//
// **局面を取り込む面。** 口は 5 つで、**何を通って、どこへ行くか**で行き先が決まる:
//
//   画面から撮る / 画像ファイル … 認識を通るので**訂正タブ**（撮る操作そのものは枠のツールバー）
//   新規対局                   … **解析タブ**（「訂正タブで調整」だけは訂正タブ。独自ハンデの入口）
//   詰将棋                     … 空の盤を**訂正タブ**へ
//   棋譜を読み込む              … URL の取得・貼り付け・ファイルの**どれもカードに積む**（`fetchcards.ts`）。
//                                 カードの「解析する」で**解析タブ**、「棋譜に登録」で棋譜タブ
//
// ⚠️ **受け渡しは `InputTabOptions`（もらうもの）と `InputTabHandle`（返すもの）だけ**
// （`sidepane.ts` と同じ型）。**訂正タブにも解析タブにも直には触れない** —— 局面を出すのも、
// 訂正タブを置き換えるのも呼び出し側（撮った画像に紐づくものの片付けは `edittab.ts` の `clearShotContext`）。
// ⚠️ **局面の写しを持たない**（frontend/src/AGENTS.md の 5）。
import { Clipboard, Events } from "@wailsio/runtime";
import { CaptureService } from "../bindings/ikkyoku";
import { KifuService, StudyService } from "../bindings/github.com/ShinteLab/ikkyoku/app";
import type { StudyState } from "../bindings/github.com/ShinteLab/ikkyoku/app/models";
import { mountFetchCards } from "./fetchcards";
import { confirmStopFollow } from "./followguard";
import { attachHints } from "./hint";
import { openPopup } from "./popup";

export interface InputTabOptions {
  // openStudy は局面を解析タブへ出す（新規対局・棋譜の読み込み・カードの「解析する」）。
  // ⚠️ **タブを開いてから描くのは呼び出し側**（`display: none` の中では手順の scrollIntoView も盤のグリッドも効かない）。
  openStudy(state: StudyState): void;
  // setStudyViewpoint は解析タブの視点（新規対局の「あなたの手番」。⚠️ **局面を描く前に呼ぶ**）。
  setStudyViewpoint(black: boolean): void;
  // editLoaded は訂正タブに局面が載っているか（置き換える前に聞くかどうか）。
  editLoaded(): boolean;
  // editHandicap / editEmpty は訂正タブを手合割の初期局面 / 空の盤で置き換えて開く。
  // ⚠️ **撮った画像に紐づくもの（画像・認識の情報・学習への送信）を片付けるのは呼び出し側。**
  editHandicap(handicap: string): Promise<void>;
  editEmpty(): Promise<void>;
  // onKifuSaved は棚に入れたとき（棋譜タブの一覧を取り直す）。
  onKifuSaved(): void;
}

export interface InputTabHandle {
  // captureStatus は撮った／読み込んだ結果の 1 行（`#status`）。
  // ⚠️ **撮る経路と共用する**（撮ったのか読み込んだのかで別の面を作らない）。
  // 書くのは `edittab.ts` の showShot / showResult / showError と、ここの画像ファイルの読み込みの失敗だけ。
  captureStatus: HTMLParagraphElement;
}

export function mountInputTab(root: HTMLElement, opts: InputTabOptions): InputTabHandle {
  // ⚠️ **この中の HTML コメントにバッククォートを書かないこと**（`mainscreen.ts` の同じ注記。`check-css.mjs` が見ている）。
  root.innerHTML = `
    <!-- ⚠️ **ボタンはここに戻さないこと**（2026-08-18）。枠を出すのは
         **タイトルバーのトグル**、盤に合わせるのは**枠のツールバーの □**。
         どちらも「枠を出してから枠に対してやること」なので、
         入力タブに置くとタブを行き来することになる。ここに残すのは案内だけ。 -->
    <div class="setting-group">
      <span class="setting-title" data-hint="hint-capture">画面から撮る</span>
      <span id="hint-capture" class="setting-note is-hint">
        タイトルバーの<strong>「枠を表示」</strong>でガイド枠を出し、
        中継の盤面に合わせてから、枠のツールバーのカメラを押します
        （枠の □ で盤に合わせられます）。
        撮ると<strong>訂正タブ</strong>が開きます。
        <strong>枠が出ていないあいだは撮れません</strong>
        （どこを撮るのかが画面に見えていない状態で撮らないため）。
        撮った画像のファイル名は<strong>訂正タブの「認識詳細情報」</strong>に出ます
        （押すとフルパスをコピーできます）。
      </span>
    </div>
    <!-- 画像ファイルを読み込む（2026-09-12）。**撮るのと同じ経路**で、
         行き先も同じ**訂正タブ**（どちらも認識を通るため）。

         ⚠️ **枠は要らない。** 「どこを撮るか」の話が無いので、
         **枠が出ていなくても押せる**（撮るほうの門番と混ぜないこと）。

         ⚠️ **結果の行は下の #status を共用する。** 撮ったのか読み込んだのかで
         **別の面を作らない** —— 出てくるものは同じ「画像から起こした 1 局面」で、
         この先の訂正も学習への還元も 1 つの経路しかない。 -->
    <!-- ⚠️ **画像ファイルを落とせるのはこの群だけ**（2026-11-15。data-file-drop-target）。
         id は Go 側の imageDropTarget と揃える。落とした画像は「画像を選ぶ…」と同じ経路で読む。 -->
    <div class="setting-group file-drop" id="image-drop" data-file-drop-target>
      <span class="setting-title" data-hint="hint-image">画像ファイルを読み込む</span>
      <span id="hint-image" class="setting-note is-hint">
        手元の画像（PNG / JPEG / GIF）から盤面を認識します。
        撮るのと同じで、読み込むと<strong>訂正タブ</strong>が開きます。
        <strong>枠は要りません</strong>（どこを撮るかの指定が無いので）。
        撮り溜めた PNG を<strong>認識し直す</strong>のにも使えます。
        ここへのドラッグ＆ドロップでも読み込めます。
      </span>
      <div class="setting-fields">
        <button id="image-open" class="ghost-btn is-primary" type="button">画像を選ぶ…</button>
      </div>
    </div>

    <p id="status" class="status" role="status" aria-live="polite">
      ガイド枠を盤面に合わせて撮影するか、画像ファイルを読み込んでください。
    </p>

    <!-- 何もないところから始める（2026-08-13）。**3 つめの入力の口。**
         行き先は棋譜と同じ**解析タブ**で、訂正タブは通らない
         （初期局面は手合割で一意に決まるので、直すものが無い）。

         ⚠️ **初期局面をここに書かない。** 手合割 → 盤面は将棋の**仕様**なので
         core/kifu.StartSFEN の 1 か所だけが持つ（Go 側の position.NewGame
         がそれを引く）。フロントに SFEN を書き写すと、棋譜から読んだ平手と
         新規で作った平手が食い違いうる。

         ⚠️ **手合割の値は core/kifu が読める表記そのまま。** ここは表を
         持っているのではなく、向こうの表の名前を **value に書き写している
         だけ**（StartSFEN が知らない名前はエラーになるので、打ち間違えれば
         押した瞬間に分かる）。**盤面は 1 文字もここに書かない。**
         ⚠️ **出しているのは代表的な 6 つだけ**（2026-09-12）。core/kifu は
         右香・飛香・三/五/八/十枚落ちも持っているが、**まず使うものだけ並べる**。
         詰将棋は初期局面が無い（人が並べる）ので別の口になる —— そちらは
         訂正タブ側の話で、ここには並ばない。

         ⚠️ **「あなたの手番」が今決めているのは視点（画面の向き）だけ。**
         初期局面は手合割で決まっていて、どちらを持っても同じものが出る。
         対局モード（片側を人、もう片側をエンジンが指す）を入れる段になったら、
         この選択がそのまま「自分の側」になる。

         ⚠️ **駒落ちではラベルを「下手／上手」に替える**（2026-09-12）。
         側そのものは平手と同じ（下手＝先手＝b、上手＝後手＝w）で、
         **呼び名だけが替わる**。⚠️ **上手が「先手」になるのではない** ——
         駒落ちで上手が指すのは**初手**であって、SFEN 上は後手のまま
         （初期局面が w から始まる）。ここを入れ替えると盤ごとひっくり返る。 -->
    <div class="setting-group">
      <span class="setting-title" data-hint="hint-newgame">新しく対局を始める</span>
      <span id="hint-newgame" class="setting-note is-hint">
        初期局面から始めます。<strong>解析タブ</strong>が開いて、
        盤の駒を押せばそのまま手を進められます。
        <strong>あなたの手番に選んだ側が手前に来ます</strong>
        （盤の向きが変わるだけで、局面は変わりません）。
        表に無いハンデは<strong>「訂正タブで調整」</strong>から
        （初期局面を訂正タブに出すので、駒を足し引きできます）。
      </span>
      <div class="setting-fields">
        <span class="field-label">手合割</span>
        <select id="newgame-handicap"
                title="駒を落とすのは上手（後手）側です。詰将棋はこれから">
          <option value="平手" selected>平手</option>
          <option value="香落ち">香落ち</option>
          <option value="角落ち">角落ち</option>
          <option value="飛車落ち">飛車落ち</option>
          <option value="二枚落ち">二枚落ち</option>
          <option value="四枚落ち">四枚落ち</option>
          <option value="六枚落ち">六枚落ち</option>
        </select>
        <span class="field-label">あなたの手番</span>
        <div class="turn-group" role="group" aria-label="あなたの手番">
          <button id="newgame-black" class="turn-btn is-active" type="button"
                  data-side="black">先手</button>
          <button id="newgame-white" class="turn-btn" type="button"
                  data-side="white">後手</button>
        </div>
        <button id="newgame-start" class="ghost-btn is-primary" type="button">対局を始める</button>
        <button id="newgame-edit" class="ghost-btn" type="button"
                title="選んだ手合割の初期局面を訂正タブに出します。駒を足し引きすれば、表に無い独自のハンデも作れます（そのあと「この局面を解析する」で解析タブへ）">訂正タブで調整</button>
      </div>
      <p id="newgame-status" class="status" role="status" aria-live="polite" hidden></p>
    </div>

    <!-- 詰将棋。**手合割とは別の口**（2026-09-12）。初期局面が無い（人が並べる）
         ので手合割の表は引けず、行き先も訂正タブ（自由に置ける盤が要る）。

         ⚠️ **「新しく対局を始める」の option に「詰将棋」を足さないこと。**
         あちらは**確定した局面を解析タブへ渡す**経路で、行き先が違う。

         ⚠️ **詰将棋は「属性」**（訂正タブのチェック 1 つ）。入れた時点で
         余った駒は**全部 玉方（奥）の持駒**になり、盤を直すたびに追随する。
         手番は攻方（先手）固定・目線も先手固定。**手合割とは排他。** -->
    <div class="setting-group">
      <span class="setting-title" data-hint="hint-mate">詰将棋を並べる</span>
      <span id="hint-mate" class="setting-note is-hint">
        空の盤を<strong>訂正タブ</strong>に出します。駒を置けば、
        <strong>余った駒は玉方（奥）の持駒になります</strong>（並べるだけで確定します）。
        手番は<strong>攻方（先手）</strong>固定です。
      </span>
      <div class="setting-fields">
        <button id="mate-start" class="ghost-btn is-primary" type="button">空の盤を並べる</button>
      </div>
      <p id="mate-status" class="status" role="status" aria-live="polite" hidden></p>
    </div>

    <!-- 棋譜を読み込む（2026-11-18 に「棋譜を貼り付ける」と「棋譜の URL から」を 1 つの群にした）。
         **画像を通らない入口**なので、行き先も違う
         （撮影は認識を通るので訂正タブ、棋譜は既に確定しているので解析タブ）。

         **入り方は 2 つ、出口はカード 1 つ**:
           URL / 棋譜 ID → 取得        … カード（更新で取り直す・再起動後も残る）
           貼り付け / ファイル / ドロップ … カード（取りに行く先が無いので更新は無い）
         どちらのカードからも「解析する」で解析タブ、「棋譜に登録する」（取得したものは
         「この内容を保存」）で棋譜タブ。⚠️ **二系統を残してある**（2026-09-04。棚は解析の前提条件ではない）。

         ⚠️ **本文を打つ欄（textarea）を戻さないこと**（2026-11-18 に外した）。棋譜は打つものではなく
         **貼るか開くもの**で、貼った中身はカードの「KIF を表示」で見える。
         ⚠️ **URL の入力欄を 2 つにしないこと**（2026-09-12）。読売なのか連盟なのか他サイトの .kif
         なのかは**こちらの都合**で、判別は kicho の Fetch（⚠️ **判別を画面に書かないこと**）。 -->
    <!-- ⚠️ **ファイルを落とせるのはこの群だけ**（2026-11-15。data-file-drop-target）。
         id は Go 側の KifuDropTarget と揃える（Go は落ちた要素の id で確かめる）。 -->
    <div class="setting-group file-drop" id="kifu-drop" data-file-drop-target>
      <span class="setting-title" data-hint="hint-kifu">棋譜を読み込む</span>
      <span id="hint-kifu" class="setting-note is-hint">
        <span class="hint-para">
        <strong>棋譜の URL を入れて「取得」</strong>するか、コピーした KIF / CSA の棋譜を
        <strong>「貼り付け」</strong>で取り込むと、<strong>下にカードが出ます。</strong>
        <strong>「ファイルを開く…」</strong>かここへのドラッグ＆ドロップで .kif / .csa も読み込めます。
        カードの<strong>「解析する」</strong>で、<strong>指し手を全て反映した局面</strong>で
        <strong>解析タブ</strong>が開きます（<strong>訂正タブは通りません</strong>）。
        </span>
        <span class="hint-para">
        <strong>URL はどのサイトのものでもかまいません。</strong>
        <strong>読売（竜王戦）の対局ページ</strong>・<strong>日本将棋連盟の棋譜中継</strong>・
        ほかのサイトの <code>.kif</code>・読売の棋譜 ID を、この 1 つの欄で扱います
        （<strong>どこのものかは自動で見分けます</strong>）。
        対局中は棋譜が伸びるので、カードの<strong>「更新」</strong>で取り直してから
        保存し直してください（<strong>同じ棋譜を保存し直しても増えません</strong>）。
        </span>
        <span class="hint-para">
        カードの<strong>「棋譜に登録する」</strong>（取得したものは<strong>「この内容を保存」</strong>）で
        <strong>棋譜タブに残ります</strong>（解析するだけでは残りません）。解析したあとに登録しても、
        <strong>その解析は棋譜タブの「解析する」から続きを開けます</strong>。
        </span>
        <span class="hint-para">
        <strong>取得したカードは再起動しても残ります。</strong>2 日制の対局で翌日また URL を
        貼り直さずに済むよう、「どのサイトのどの棋譜か」を覚えておきます
        （<strong>棋譜そのものではありません</strong>）。復元したカードは中身が空なので
        「更新」でサイトから取り直してください（「取り直して解析」なら 1 回で済みます）。
        <strong>貼り付けたカードは再起動すると消えます</strong>（残すなら棋譜タブに登録してください）。
        追うのをやめるときはカードの「閉じる」で外します
        （<strong>棋譜タブの棋譜は消えません</strong>）。終局した棋譜を保存したときは自動で外れます。
        </span>
      </span>
      <!-- ⚠️ **URL の入力欄はこの 1 つだけ**（#kifu-url）。⚠️ **#fetch-input に
           戻さないこと** —— 取得（カード）も解析も登録も同じ欄を読む。

           ⚠️ **取得も文字コードの判別も Go 側**（kicho）。webview の fetch では
           CORS で弾かれるうえ、**中継の .kif は Shift_JIS** なので、
           いずれにせよこちらでは扱えない。 -->
      <div class="setting-fields kifu-url-row">
        <span class="field-label">URL / 棋譜 ID</span>
        <input id="kifu-url" type="text" spellcheck="false"
               placeholder="http://live.shogi.or.jp/oui/kifu/67/oui202607290101.html" />
        <button id="fetch-run" class="ghost-btn is-primary" type="button"
                title="この URL の棋譜を取ってカードに出します（解析・登録はカードから）">取得</button>
      </div>
      <!-- 貼り付け・ファイル（2026-11-18）。**どれもカードを 1 枚出すだけ**で、行き先はカードが持つ。
           ⚠️ **ここに「解析する」「登録する」を置かないこと**（行き先の口が 2 か所になる）。
           ⚠️ **クリップボードは Wails ランタイムで読む**（navigator.clipboard はカスタムスキーム配信の
           webview では当てにできない）。 -->
      <div class="setting-fields">
        <button id="kifu-paste" class="ghost-btn" type="button"
                title="クリップボードの棋譜（KIF / CSA）をカードにします">貼り付け</button>
        <button id="kifu-file" class="ghost-btn" type="button"
                title=".kif / .csa を開いてカードにします">ファイルを開く…</button>
      </div>
      <!-- ⚠️ **この群の状態表示は 1 つ**（#fetch-status）。取得・貼り付け・ファイルの
           どれもここへ出す（持ち主は fetchcards.ts。入力タブは setStatus を借りる）。
           **群が 1 つなら状態の行も 1 つ** —— 2 つ置くと、どのボタンの返事なのかが
           読めなくなる。カード自身の返事はカードの中。 -->
      <p id="fetch-status" class="status" role="status" aria-live="polite" hidden></p>
      <!-- カード。**新しいものが先頭。** 同じ棋譜を取り直した・貼り直したときは
           カードを増やさない（取得は key = 取得元:棋譜 ID、貼り付けは本文のハッシュ）。
           ⚠️ **起動時は仮の一覧（DB）から取得したカードだけを復元する**（サイトへは取りに行かない）。 -->
      <div id="fetch-cards" class="fetch-cards"></div>
    </div>
  `;

  // 項目の説明をホバーで出す（`hint.ts`）。⚠️ **innerHTML を組んだ直後に 1 回だけ**
  // （中身を描き直さないので付け直しは要らない）。
  attachHints(root);

  // 撮った／読み込んだ結果の 1 行。⚠️ **撮る経路（edittab.ts）と共用する**（下の画像ファイルの注記）。
  const status = root.querySelector<HTMLParagraphElement>("#status")!;

  // 画像ファイルを読み込む（2026-09-12）。**撮るのと同じ経路に載る**ので、
  // ここでやるのは Go 側を呼ぶことだけ —— 盤も訂正タブも
  // `capture:shot` / `capture:done` が運ぶ（**受け側を 2 系統にしない**）。
  //
  // ⚠️ **取り消しは失敗ではない**（ダイアログを閉じただけ）。Go 側はパスが空の
  // 結果を返し、イベントも出さないので、**画面を 1 つも変えずに黙って戻る**。
  const imageOpen = root.querySelector<HTMLButtonElement>("#image-open")!;
  // 落とした画像が読めなかった（2026-11-15。Go の `registerFileDrop`）。**出し方は「画像を選ぶ…」と同じ**。
  // ⚠️ **読めたときはここでは何もしない** —— 撮った 1 枚と同じ `capture:shot` / `capture:done` が運ぶ。
  Events.On("image:drop-failed", (event: { data: string }) => {
    status.textContent = `画像を読み込めませんでした: ${event.data}`;
    status.classList.add("is-error");
    status.classList.remove("is-warn");
  });
  imageOpen.addEventListener("click", () => {
    void (async () => {
      imageOpen.disabled = true;
      try {
        const picked = await CaptureService.OpenImage();
        if (!picked?.path) {
          return; // 取り消し
        }
      } catch (err) {
        status.textContent =
          `画像を読み込めませんでした: ${String(err instanceof Error ? err.message : err)}`;
        status.classList.add("is-error");
        status.classList.remove("is-warn");
      } finally {
        imageOpen.disabled = false;
      }
    })();
  });

  // 新しく対局を始める（2026-08-13）。**3 つめの入力の口で、行き先は解析タブ。**
  //
  // ⚠️ **初期局面をフロントで作らない**（Go 側の `position.NewGame` →
  // `core/kifu.StartSFEN`）。手合割 → 盤面は将棋の**仕様**なので、
  // ここに SFEN を書き写すと棋譜から読んだ平手と食い違いうる。
  //
  // ⚠️ **「あなたの手番」が決めているのは視点だけ。** Go 側には渡さない
  // （平手の初期局面はどちらを持っても同じで、局面には効かない）。
  // 対局モードを入れる段になったら、この選択がそのまま「自分の側」になる。
  const newgameHandicap = root.querySelector<HTMLSelectElement>("#newgame-handicap")!;
  const newgameStart = root.querySelector<HTMLButtonElement>("#newgame-start")!;
  const newgameEdit = root.querySelector<HTMLButtonElement>("#newgame-edit")!;
  const newgameStatus = root.querySelector<HTMLParagraphElement>("#newgame-status")!;
  const newgameSides = Array.from(
    root.querySelectorAll<HTMLButtonElement>(".turn-group .turn-btn"),
  );
  // 既定は先手（中継の原則と同じ「手前が先手」）。**駒落ちでは下手**で、
  // これも既定のままでよい（下手＝先手＝`b` で、側は平手と同じ）。
  let newgameBlack = true;
  for (const b of newgameSides) {
    b.addEventListener("click", () => {
      newgameBlack = b.dataset.side === "black";
      for (const x of newgameSides) {
        x.classList.toggle("is-active", x === b);
      }
    });
  }
  // 駒落ちのときだけ「先手／後手」を「下手／上手」に言い替える（2026-09-12）。
  //
  // ⚠️ **替えるのは呼び名だけ。** `data-side` も `newgameBlack` も触らない
  // （下手＝先手＝`b`、上手＝後手＝`w` は駒落ちでも同じ）。
  // ⚠️ **「上手が先手」にしないこと** —— 上手が指すのは**初手**で、
  // SFEN 上は後手のまま（駒落ちの初期局面が `w` から始まる。Go 側が持っている）。
  const applyNewgameLabels = () => {
    const dropped = newgameHandicap.value !== "平手";
    for (const b of newgameSides) {
      const black = b.dataset.side === "black";
      b.textContent = dropped ? (black ? "下手" : "上手") : black ? "先手" : "後手";
    }
  };
  newgameHandicap.addEventListener("change", applyNewgameLabels);
  applyNewgameLabels();

  // 「訂正タブで調整」（2026-09-12）。**独自ハンデの入口。**
  //
  // ⚠️ **4 つめの入力の口だが、行き先だけが違う**（手合割 → **訂正タブ**）。
  // 表に無いハンデ（「飛車と左香を落として歩を 2 枚抜く」など）は
  // `core/kifu` の表には無いので、**近い手合割から始めて駒を足し引きする**。
  //
  // ⚠️ **撮った画像に紐づくものを全部片付けること。** 訂正タブは元々
  // 「撮った 1 枚を直す面」で、**画像・認識の情報・学習への送信**がぶら下がって
  // いる。残したまま別の局面を入れると、**別の画像のラベルとして訂正結果を
  // 送れてしまう**（学習データが壊れる）。
  //
  // ⚠️ **確認を挟むこと** —— 訂正中の局面を捨てる操作なので、
  // 撮ったものが載っているときは黙って上書きしない。
  // ⚠️ **`window.confirm` は使わない**（`popup.ts` の先頭の理由。押した場所で聞く）。
  // ⚠️ **初期フォーカスは「やめる」**（捨てる操作なので、Enter の連打で消えない）。
  const startHandicapEdit = () => {
    void (async () => {
      newgameEdit.disabled = true;
      newgameStatus.hidden = false;
      newgameStatus.classList.remove("is-error");
      newgameStatus.textContent = "並べています…";
      try {
        await opts.editHandicap(newgameHandicap.value);
        newgameStatus.textContent =
          `${newgameHandicap.value}の初期局面を訂正タブに出しました。駒を足し引きしてから「この局面を解析する」を押してください`;
      } catch (err) {
        newgameStatus.textContent =
          `並べられませんでした: ${String(err instanceof Error ? err.message : err)}`;
        newgameStatus.classList.add("is-error");
      } finally {
        newgameEdit.disabled = false;
      }
    })();
  };

  newgameEdit.addEventListener("click", (e) => {
    if (!opts.editLoaded()) {
      startHandicapEdit();
      return;
    }
    openPopup(e.clientX, e.clientY, {
      label: "訂正タブの局面を置き換え",
      focus: 1,
      items: [
        {
          label: `訂正タブを「${newgameHandicap.value}」で置き換える`,
          kind: "danger",
          onPick: startHandicapEdit,
        },
        { label: "やめる", onPick: () => {} },
      ],
    });
  });

  // 詰将棋（2026-09-12）。**空の盤を訂正タブへ。** 手合割とは別の口で、
  // ⚠️ **こちらは駒台を逆算したまま始める**（「残り全部は玉方の持駒」を
  // 「まとめる」で寄せて確定させる）。**手合割のチェックは入れない。**
  const mateStart = root.querySelector<HTMLButtonElement>("#mate-start")!;
  const mateStatus = root.querySelector<HTMLParagraphElement>("#mate-status")!;
  const startMateEdit = () => {
    void (async () => {
      mateStart.disabled = true;
      mateStatus.hidden = false;
      mateStatus.classList.remove("is-error");
      mateStatus.textContent = "空の盤を出しています…";
      try {
        await opts.editEmpty();
        mateStatus.textContent =
          "空の盤を訂正タブに出しました。駒を置けば、余った駒は玉方の持駒になります";
      } catch (err) {
        mateStatus.textContent =
          `並べられませんでした: ${String(err instanceof Error ? err.message : err)}`;
        mateStatus.classList.add("is-error");
      } finally {
        mateStart.disabled = false;
      }
    })();
  };

  // ⚠️ **置き換える前に聞く**（「訂正タブで調整」と同じ。初期フォーカスは「やめる」）。
  mateStart.addEventListener("click", (e) => {
    if (!opts.editLoaded()) {
      startMateEdit();
      return;
    }
    openPopup(e.clientX, e.clientY, {
      label: "訂正タブの局面を置き換え",
      focus: 1,
      items: [
        { label: "訂正タブを空の盤で置き換える", kind: "danger", onPick: startMateEdit },
        { label: "やめる", onPick: () => {} },
      ],
    });
  });

  newgameStart.addEventListener("click", () => {
    void (async () => {
      if (!(await confirmStopFollow("新しい対局を始める"))) {
        return;
      }
      newgameStart.disabled = true;
      newgameStatus.hidden = false;
      newgameStatus.classList.remove("is-error");
      newgameStatus.textContent = "対局を作っています…";
      try {
        const got = await StudyService.NewGame(newgameHandicap.value);
        // ⚠️ **視点は局面を描く前に決める。** 後から反転すると、盤とグリッドを
        // 二度組み直すことになる（そのぶん 1 マスずれる隙ができる）。
        opts.setStudyViewpoint(newgameBlack);
        // ⚠️ **タブを先に開いてから描く**（棋譜の読み込みと同じ理由。
        // `display: none` の中ではグリッドを測れないし scrollIntoView も効かない）。
        opts.openStudy(got.state);
        newgameStatus.textContent = got.summary;
      } catch (err) {
        newgameStatus.textContent =
          `対局を始められませんでした: ${String(err instanceof Error ? err.message : err)}`;
        newgameStatus.classList.add("is-error");
      } finally {
        newgameStart.disabled = false;
      }
    })();
  });

  // ---- 棋譜を読み込む（カード）--------------------------------------------
  //
  // ⚠️ **取得元の判別も取得も、棋譜かどうかの判定も Go 側（KifuService）。** ここは
  // 入り方（貼り付け・ファイル・ドロップ）をカードへ繋ぐだけ。
  // ⚠️ **URL の欄・状態の行・カードは全部あちらが持つ**（2026-09-12）。
  // 解析も登録も**カード**の操作なので、こちらが触るのは行き先（解析タブ・棋譜タブの一覧）だけ。
  // ⚠️ **フロントで KIF を解釈しないこと**（将棋の仕様は core に一本化する）。
  const cards = mountFetchCards(root, {
    confirmAnalyze: () => confirmStopFollow("棋譜を解析タブへ送る"),
    // 棚を通らない経路だが、**描き方は棚から送ったときと同じ**（KifuLoad）。
    onAnalyze: (got) => opts.openStudy(got.state),
    onSaved: () => opts.onKifuSaved(),
  });

  // 貼り付け（2026-11-18）。**クリップボードの中身をカードにするだけ**（本文を打つ欄は無い）。
  // ⚠️ **クリップボードは Wails ランタイム**（`navigator.clipboard` は当てにできない。`library.ts` と同じ）。
  const kifuPaste = root.querySelector<HTMLButtonElement>("#kifu-paste")!;
  kifuPaste.addEventListener("click", () => {
    void (async () => {
      kifuPaste.disabled = true;
      try {
        const text = (await Clipboard.Text()).trim();
        if (!text) {
          cards.setStatus("クリップボードに文字がありません。棋譜（KIF / CSA）をコピーしてから押してください。", "error");
          return;
        }
        await cards.addPasted(text, "貼り付け");
      } catch (err) {
        cards.setStatus(
          `クリップボードを読めませんでした: ${String(err instanceof Error ? err.message : err)}`,
          "error",
        );
      } finally {
        kifuPaste.disabled = false;
      }
    })();
  });

  // ファイルを開く（2026-11-15。2026-11-18 から**カードにする**）。⚠️ **文字コードをここで持たないこと** ——
  // 開いた本文のまま登録されたら元の文字コードを残すのは Go 側（`KifuService.ImportKIF`）。
  // ⚠️ **取り消しは失敗ではない**（空の本文が返る。カードも状態も変えない）。
  const kifuFile = root.querySelector<HTMLButtonElement>("#kifu-file")!;
  kifuFile.addEventListener("click", () => {
    void (async () => {
      kifuFile.disabled = true;
      try {
        const got = await KifuService.OpenKifuFile();
        if (!got?.text) {
          return; // 取り消し
        }
        await cards.addPasted(got.text, got.name);
      } catch (err) {
        cards.setStatus(
          `ファイルを読み込めませんでした: ${String(err instanceof Error ? err.message : err)}`,
          "error",
        );
      } finally {
        kifuFile.disabled = false;
      }
    })();
  });
  // `kifu:dropped` の中身（Go の `app.KifuDrop`）。⚠️ **bindings には出ない**（`DropKifuFiles` は
  // `//wails:ignore` なので型が生成されない）ので、ここで形を書く。**Go の json タグと揃えること。**
  type KifuDrop = { name: string; text: string; encoding: string; error: string; skipped: number };
  // ドラッグ＆ドロップ（2026-11-15）。**読むのは Go 側**（`KifuService.DropKifuFiles`。落ちた先が
  // この群のときだけ）で、ここは届いた中身をカードにするだけ。⚠️ **2 つ目以降は読まない**（Go 側）。
  Events.On("kifu:dropped", (event: { data: KifuDrop }) => {
    const got = event.data;
    if (got.error) {
      cards.setStatus(`ファイルを読み込めませんでした: ${got.error}`, "error");
      return;
    }
    void cards.addPasted(got.text, got.name).then((ok) => {
      if (ok && got.skipped > 0) {
        cards.setStatus(`${got.name} を読み込みました。ほかの ${got.skipped} 個は読んでいません`, "warn");
      }
    });
  });

  return { captureStatus: status };
}
