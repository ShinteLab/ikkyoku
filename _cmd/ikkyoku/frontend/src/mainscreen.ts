// メイン画面。アプリ本体。
//
// 撮った画像から割り出した盤面・SFEN・警告を出す画面。起動時は非表示で、最初の
// キャプチャで現れる。**この画面を閉じるとアプリが終了する**(枠を閉じても終了しない)。
//
// 画面は 4 タブ(入力 / 訂正 / 解析 / 設定)。**2026-08-10 に 3 タブから割り**
// (以前は「盤面」1 枚の中で訂正モードをトグルしていた)、**2026-08-11 に
// デバッグタブを訂正タブの中の折りたたみ「認識詳細情報」へ畳んだ。**
//
// ⚠️ **訂正タブと解析タブは別の局面を持っている。** Go 側も 2 つに分かれており
// (PositionService / StudyService)、繋がるのは「この局面を解析する」を押した
// ときの 1 回だけ(写しを渡す)。**片方の値をもう片方に流用しないこと。**
//
//   入力   … 局面を**取り込む**面。**新規対局**・キャプチャ・**棋譜(KIF)の貼り付け**。今後 SFEN / 画像ファイル
//            (画像は認識を通るので訂正タブへ、SFEN/KIF は確定済みなので解析タブへ)。**中身は `inputtab.ts`**
//   訂正   … 認識の誤りを**直す**面。**ここに居ること自体が訂正モード**
//            (自由編集・合法性を問わない・手番も駒台の先後も未決でよい)。**中身は `edittab.ts`**
//   解析   … **確定した局面**の面。評価値を出し、今後ここに手順と分岐ツリーが乗る
//            (合法手だけを辿る)。**中身は `studytab.ts`**(2026-11-13 に切り出した)
//   設定   … 設定。**中身は `settingstab.ts`**(2026-11-13 に切り出した。並べ方の約束もあちら)。
//            ここが持つのは、設定が効く先へ配る `applySettings` だけ
//
// 「認識詳細情報」(訂正タブの中の折りたたみ。旧デバッグタブ)は
// **認識精度を追う**ところ(認識器の状態・検出の信頼度・盤面領域・推論器・
// 保存先・撮った画像)。局面そのものの話ではないものはここ。
// ⚠️ **既定で閉じる。** 訂正のあいだ盤の上に積む行は短く保つ。
//
// 足す項目がどこに載るかはこの区分で決める。**「項目を足すな」ではない。**
//
// **駒台と警告は訂正タブの盤の周りと「認識詳細情報」の両方に出す。** デバッグ側では「認識が
// どれくらい外したか」の記録(訂正しても変わらない)だが、訂正側では**局面を直すために
// 要る情報**(駒台の枚数は駒数保存則の逆算そのもの、警告は「どこが怪しいか」の提示)。
// 同じ見た目でも**出所が違う**ので、片方の更新をもう片方に流用しないこと。
//
// **撮る操作そのものはここには置かない。** 撮るのは盤に枠を合わせている最中の操作
// なので、枠のツールバーで完結する。入力タブに置いてあるのは
// その入口(枠を出す・盤に合わせる)と、撮れた結果の表示。
//
// ⚠️ **ホットキー(Alt+S)は画面のどこにも書かない**(2026-08-10)。登録はしてあるが、
// 枠が出ていないと撮れないようにしたので「フォーカスがどこにあっても撮れる」という
// 売りが枠の出ているあいだに限られる。入力の口もこれから増えるので、撮ることを
// 前提にした案内をツールバーに常設しない。
//
// 枠(frame.ts)とは別ウィンドウなので、ここに置いた要素はキャプチャに写り込まない
// ——ただし**枠に重なる位置に動かすと写り込む**(画面の合成結果を撮るため)。初回だけ
// Go 側が枠の外へ逃がす(capture_windows.go の placeMainBesideFrame)。
import { Events, Window } from "@wailsio/runtime";
import {
  FiAlertTriangle,
  FiMaximize,
  FiMinus,
  FiSquare,
  FiX,
} from "react-icons/fi";
// ⚠️ **CaptureService だけ出所が違う。** あれはウィンドウ（枠）と HWND を触るので
// `_cmd/ikkyoku` に残っており、他の Service は `ikkyoku/app` にある。
import { CaptureService } from "../bindings/ikkyoku";
import { StudyService } from "../bindings/github.com/ShinteLab/ikkyoku/app";
import { iconMarkup } from "./icon";
import { mountLibrary } from "./library";
import { mountInputTab } from "./inputtab";
import { mountIssues } from "./issues";
import { hideHint } from "./hint";
import { mountSettingsTab } from "./settingstab";
import { mountEditTab } from "./edittab";
import { mountStudyTab } from "./studytab";
import type { AppSettings } from "../bindings/github.com/ShinteLab/ikkyoku/app/models";
import { confirmStopFollow } from "./followguard";

export function mountMainScreen(root: HTMLElement): void {
  // ⚠️ **この中の HTML コメントにバッククォートを書かないこと。** テンプレート
  // リテラルなので、`.foo` のような引用がそこで文字列を終わらせる。**エラーは
  // ずっと下の行に出る**（実際に踏んだ）ので、原因に辿り着きにくい。
  root.innerHTML = `
    <div class="main-screen">
      <!-- ⚠️ **これがタイトルバーそのもの**（2026-08-12 にメイン画面を Frameless にした）。
           OS のタイトルバーを外して、その場所にタブの行を上げてある。狙いは**縦の領域**で、
           「タイトルバー + タブの行」の 2 段が 1 段になったぶん、解析タブの盤が大きくなる
           （盤の大きさは 100vh から引いて決まる。style.css の --board-size）。

           ⚠️ **ここが移動ハンドル**（.main-toolbar に --wails-draggable: drag）。
           押せるものは全部 no-drag に戻すこと（.tabs / .win-controls）。**外すと
           ボタンがドラッグ扱いになってクリックが効かなくなる**（枠の .frame-actions と同じ話）。

           ⚠️ **アプリ名は出さない。** 出す場所はタブの行しか無く、そのぶん横が狭くなる。
           窓の識別はタスクバーの表示（Title オプション）が持っている。 -->
      <div class="main-toolbar">
        <div class="tabs" role="tablist">
          <button id="tab-input" class="tab is-active" type="button"
                  role="tab" aria-selected="true" aria-controls="panel-input">入力</button>
          <button id="tab-library" class="tab" type="button"
                  role="tab" aria-selected="false" aria-controls="panel-library">棋譜</button>
          <button id="tab-edit" class="tab" type="button"
                  role="tab" aria-selected="false" aria-controls="panel-edit">訂正</button>
          <button id="tab-study" class="tab" type="button"
                  role="tab" aria-selected="false" aria-controls="panel-study">解析</button>
          <button id="tab-settings" class="tab" type="button"
                  role="tab" aria-selected="false" aria-controls="panel-settings">設定</button>
        </div>
        <!-- 枠を出すトグル（2026-08-18 に入力タブから上げた）。
             ⚠️ **タイトルバーに置いてあるのが要点。** 枠は**どのタブに居ても
             出したくなる**（撮ってから訂正・解析と進んだあと、もう一度撮る）ので、
             入力タブの中にあると**そのたびにタブを行き来する**ことになる。

             ⚠️ **押すたびに出す/隠すが入れ替わる**（2026-08-18）。枠の ✕ でも
             隠せるが、**出す口と同じ場所で戻せるのが素直** —— 枠が中継の裏に
             回っているときや別モニタにあるときは、✕ を押しに行くほうが遠い。

             ⚠️ **今どちらなのかが見えていること**（aria-pressed + 色）。
             アイコンだけなので、**状態が読めないと押すまで分からない**。
             意味は aria-label / title が持つ（⚠️ **どちらも状態で書き換えること**）。

             ⚠️ **状態は Go 側が知らせる**（frame:visible イベント）。枠の ✕ で隠したことは
             メイン画面からは分からないので、自前で覚えると**隠れているのに
             「出ています」のまま**になる。

             ⚠️ **no-drag に戻すこと**（ツールバー全体が移動ハンドル）。
             ⚠️ **「盤に合わせる」は置かない**（2026-08-18 に入力タブのものも消した）。
             枠の側のツールバーに □ があり、**合わせる相手は枠**なので、
             枠が出ていない状態から押す操作ではない。 -->
        <div class="toolbar-actions">
          <!-- 起動はできたが足りないもの・できないこと（2026-10-04。issues.ts）。
               ⚠️ **問題があるときだけ出る**（hidden で始める）。押すと一覧の吹き出し。
               ⚠️ **枠のトグルより左に置く** —— 出たり消えたりするので、右に置くと
               いつも押すトグルの位置が動く。 -->
          <button id="issues-btn" class="toolbar-btn is-icon is-issues" type="button"
                  hidden>${iconMarkup(FiAlertTriangle)}</button>
          <button id="frame-toggle" class="toolbar-btn is-icon" type="button"
                  aria-pressed="false">${iconMarkup(FiMaximize)}</button>
        </div>

        <!-- ウィンドウ操作。**OS のタイトルバーを外した代わり**なので、右端に置いて
             最小化 → 最大化 → 閉じる の順（Windows のタイトルバーと同じ並び）。
             ⚠️ **✕ は Go 側の Quit を呼ぶ** —— 自前のボタンは WindowClosing を
             通らないので、ランタイムの Window.Close() では**位置・サイズが保存されない**。 -->
        <div class="win-controls">
          <button id="win-minimise" class="win-btn" type="button"
                  aria-label="最小化" title="最小化">${iconMarkup(FiMinus)}</button>
          <button id="win-maximise" class="win-btn" type="button"
                  aria-label="最大化" title="最大化 / 元に戻す">${iconMarkup(FiSquare)}</button>
          <button id="win-close" class="win-btn is-danger" type="button"
                  aria-label="閉じる" title="ikkyoku を終了します">${iconMarkup(FiX)}</button>
        </div>
      </div>

      <!-- 入力タブ。**局面を取り込む面。** 今はキャプチャだけだが、ここに
           SFEN・KIF・画像ファイルの入口が並ぶ（そのとき行き先が分かれる:
           画像は認識を通るので訂正タブへ、SFEN/KIF は確定済みなので解析タブへ）。 -->
      <div id="panel-input" class="panel is-active" role="tabpanel" aria-labelledby="tab-input">
        <!-- 中身は inputtab.ts が組む（mountInputTab）。 -->
      </div>

      <!-- 棋譜タブ（棚）。**保存済みの棋譜を探して解析へ送る面。**

           ⚠️ **入力タブの「棋譜を貼り付ける」とは系統が違う。** あちらは
           「保存せず解析する」で、こちらは「棚に溜めたものから選ぶ」。
           **二系統を残してある**（棚は解析の前提条件ではない。設計原則3）。

           ⚠️ **「棋譜 URL をコピー」は置かない**（kicho の UI にはある）。
           あれは ShogiHome 等の外部ツールへ渡すためのもので、ikkyoku では
           渡す先が自分自身。**代わりに置くのが「解析する」。** -->
      <div id="panel-library" class="panel" role="tabpanel" aria-labelledby="tab-library">
        <div class="library-head">
          <span id="library-count" class="field-label">棋譜一覧</span>
          <!-- 索引が効かないときの案内。⚠️ **見出しの行に出すこと**（2026-09-12）——
               検索欄の下に置いていた頃は、**打つたびに 1 行が出入りして
               下の表がポコポコ動いていた。** ここなら行の高さは再読み込み
               ボタンで決まっているので、出ても表はずれない。 -->
          <span id="library-hint" class="library-hint" hidden></span>
          <button id="library-reload" class="ghost-btn" type="button"
                  title="棋譜を読み直します">再読み込み</button>
        </div>

        <!-- 検索。⚠️ **打つたびには検索しない**（Enter か「検索」で実行）。
             件数が増えると 3 文字未満は全件走査になるため。 -->
        <div class="library-search">
          <input id="library-text" class="library-text" type="search" spellcheck="false"
                 placeholder="棋戦名・対局者・場所・備考で検索" />
          <label class="field">
            <span class="field-label">期間</span>
            <input id="library-from" type="date" />
          </label>
          <label class="field">
            <span class="field-label">〜</span>
            <input id="library-to" type="date" />
          </label>
          <label class="library-check">
            <input id="library-finished" type="checkbox" />
            <span>終局済みのみ</span>
          </label>
          <button id="library-search" class="ghost-btn is-primary" type="button">検索</button>
          <button id="library-clear" class="ghost-btn" type="button">条件をクリア</button>
        </div>
        <p id="library-status" class="status" role="status" aria-live="polite" hidden></p>

        <div class="library-table-wrap">
          <table class="library-table">
            <thead>
              <tr>
                <!-- ⚠️ **棋戦名の後ろの空欄は「表示」の列**（2026-09-14。
                     それまでは行の左端に「解析」があった）。**解析は棋戦名の
                     リンクそのもの**なので列を持たない。末尾の空欄は「削除」。
                     ⚠️ **左端に押すものを戻さないこと** —— 左端は一覧を目で追う
                     起点（開始日）で、そこにボタンがあると拾い読みのたびに避ける。 -->
                <th>開始日</th>
                <th>棋戦</th>
                <th></th>
                <th>先手</th>
                <th>後手</th>
                <th class="is-num">手数</th>
                <th></th>
              </tr>
            </thead>
            <tbody id="library-rows"></tbody>
          </table>
        </div>

        <!-- 詳細。**モーダル**（2026-09-12。それまでは一覧の下に開いていた）。
             ⚠️ **KIF の本文は出さない** —— 150 手を流しても読む場面が無く、
             詳細だけで画面が埋まっていた。**代わりに「KIF をコピー」**を置いて、
             原本が要るときは他のツールへ持っていってもらう。
             ⚠️ **開く口は「表示」ボタン**（2026-09-14 に棋戦名のリンクが解析の口になった）。
             棋戦名・備考を直すのもここ（2026-10-02）。 -->
        <dialog id="library-modal" class="library-modal">
          <div class="library-head">
            <span id="library-modal-title" class="field-label library-modal-title"></span>
            <button id="library-modal-close" class="ghost-btn" type="button">閉じる</button>
          </div>
          <dl id="library-modal-meta" class="library-meta"></dl>
          <!-- 人が書く欄（2026-10-02）。**棋戦名を直す・備考を残す**だけで、
               ⚠️ **KIF の本文は編集しない**（棚の KIF は原本のまま）。
               ⚠️ **取得した棋戦名は書き換えない** —— 直した名前は別の欄に入り、
               取り直して保存しても残る（KifuService.Annotate）。 -->
          <div class="library-annotate">
            <label class="library-annotate-field">
              <span class="field-label">棋戦名</span>
              <input id="library-modal-event" class="library-annotate-input" type="text"
                     spellcheck="false" autocomplete="off" />
            </label>
            <label class="library-annotate-field">
              <span class="field-label">備考</span>
              <textarea id="library-modal-note" class="library-annotate-input is-note"
                        rows="3" spellcheck="false"></textarea>
            </label>
            <p id="library-modal-status" class="status" role="status" aria-live="polite" hidden></p>
            <div class="library-annotate-bar">
              <button id="library-modal-revert" class="ghost-btn" type="button" hidden>元に戻す</button>
              <button id="library-modal-save" class="ghost-btn is-primary" type="button">保存</button>
            </div>
          </div>
          <div class="library-modal-foot">
            <button id="library-modal-copy" class="ghost-btn" type="button">KIF をコピー</button>
          </div>
        </dialog>
      </div>

      <!-- 訂正タブ。**認識の誤りを直す面。ここに居ること自体が訂正モード**
           （2026-08-10。以前は盤面タブ 1 枚の中でトグルしていた）。
           出口は「この局面を解析する」だけで、押すと解析タブへ写しが渡る。 -->
      <div id="panel-edit" class="panel" role="tabpanel" aria-labelledby="tab-edit">
        <!-- 中身は edittab.ts が組む（mountEditTab）。 -->
      </div>

      <!-- 解析タブ。**確定した局面の面。** 訂正タブとは別の局面を持つ
           （Go 側も PositionService / StudyService の 2 つに分かれている）。

           ⚠️ **ここに訂正の道具を置かないこと。** 駒を自由に置ける盤は
           「まだ決めていない局面」で、その評価値には意味が無い。直したくなったら
           訂正タブへ戻る（戻ると解析結果は捨てる。別の局面の話になるため）。

           **これから**: 手を進める UI（合法手だけ・1 手ごとに手番が入れ替わる）と
           分岐ツリーがここに乗る。 -->
      <div id="panel-study" class="panel" role="tabpanel" aria-labelledby="tab-study" hidden>
        <!-- 中身は studytab.ts が組む（mountStudyTab）。 -->
      </div>

      <div id="panel-settings" class="panel" role="tabpanel" aria-labelledby="tab-settings" hidden>
        <!-- 中身は settingstab.ts が組む（mountSettingsTab）。 -->
      </div>
    </div>
  `;

  // ---- 入力タブ（inputtab.ts）----
  //
  // ⚠️ **ここ（innerHTML を組んだ直後）で組むこと** —— 撮った結果の 1 行（`#status`）は
  // 入力タブの中にあり、下の撮る経路がそれを使う。項目の説明の吹き出しは入力タブ・設定タブが
  // それぞれ自分の中身に付ける（このファイルの markup には `data-hint` が無い）。
  const inputUI = mountInputTab(root.querySelector<HTMLElement>("#panel-input")!, {
    openStudy: (state) => {
      // ⚠️ **タブを先に開いてから描くこと。** 手順のリストは「今見ている手」を
      // scrollIntoView で見せるが、`display: none` の中では効かない。
      selectTab(studyTab);
      studyUI.showStudy(state);
    },
    setStudyViewpoint: (black) => studyUI.setViewpoint(black),
    editLoaded: () => editUI.editLoaded(),
    // ⚠️ **画像の無い局面を訂正タブに入れる口なので、撮った画像に紐づくものを片付ける**（`clearShotContext`）。
    editHandicap: async (handicap) => {
      await editUI.loadHandicap(handicap);
      selectTab(editTab);
    },
    editEmpty: async () => {
      await editUI.loadEmpty();
      selectTab(editTab);
    },
    onKifuSaved: () => libraryUI.refresh(),
  });

  // ---- 訂正タブ（edittab.ts）-----------------------------------------------
  //
  // ⚠️ **ここ（入力タブの直後）で組むこと** —— 撮った結果の 1 行（`#status`）は入力タブにあり、
  // 撮る経路はこちらにある。
  const editUI = mountEditTab(root.querySelector<HTMLElement>("#panel-edit")!, {
    captureStatus: inputUI.captureStatus,
    openTab: (tab) => selectTab(tab === "edit" ? editTab : inputTab),
    onConfirm: (follow) => {
      void adoptToStudy(follow);
    },
    onSutemeState: (st) => settingsUI.showSutemeState(st),
  });

  // タブは 5 枚。**局面を扱う面が「訂正」と「解析」の 2 つに分かれている**のが要点で、
  // 持っている局面も別物（Go 側の PositionService / StudyService）。
  //
  //   入力     … 局面を取り込む（新規対局・キャプチャ・棋譜の貼り付け。今後 SFEN / 画像ファイル）
  //   訂正     … 認識の誤りを直す。**自由編集**（合法性を問わない・未決でよい）
  //   解析     … 確定した局面。**手を選んで進める**面（合法手だけ。手順 UI はこれから）
  //   デバッグ … 認識精度を追う（認識器の状態・信頼度・撮った画像）
  //   設定     … 設定
  //
  // 足す項目がどこに載るかはこの区分で決める。盤はできるだけ大きく見せたいので、
  // 盤の周りに積む行は短く保つこと。
  //
  // 隠すのは表示だけで、パネルの中身は常に更新する(タブを切り替えた瞬間に
  // 古い内容が出ることが無いように)。
  const mainToolbar = root.querySelector<HTMLDivElement>(".main-toolbar")!;
  const tabOf = (name: string) => ({
    tab: root.querySelector<HTMLButtonElement>(`#tab-${name}`)!,
    panel: root.querySelector<HTMLElement>(`#panel-${name}`)!,
  });
  const tabs = ["input", "library", "edit", "study", "settings"].map(tabOf);
  const inputTab = tabs[0].tab;
  // 棋譜タブ（棚）。**入力の隣に置いてある** —— 局面を持ってくる口という点で
  // 入力タブと同じ側で、訂正・解析はその先の面。
  const libraryTab = tabs[1].tab;
  const editTab = tabs[2].tab;
  const studyTab = tabs[3].tab;
  const selectTab = (target: HTMLButtonElement) => {
    // 設定の説明の吹き出しは、タブを移ったら消す（関係の無い面に残る）。
    hideHint();
    // ⚠️ **解析タブを離れたらエンジンを手放す**（2026-08-12）。接続は解析を
    // またいで使い回すようになったので、**放っておくとタブを移ったあとも
    // `USI_Hash` ぶん（GB 級になりうる）のメモリを掴んだまま**になる。
    // タブの境界を寿命にしてあるのは、アイドルタイマーを持たずに済ませるため。
    //
    // **走っている解析（連続解析を含む）も止まる。** タブを移ると止まるのは
    // そういう約束で、**そこまでの評価値は残る**（設計原則3）。
    //
    // ⚠️ **2026-09-12 に「切り離しているあいだは手放さない」を外した。**
    // あの例外の理由は「別ウィンドウに出しているならタブを移っても見えている」
    // だったが、**切り離した窓も解析タブと一緒に隠れる**ようになったので
    // （`CaptureService.SetStudyTabActive`）、見えている相手が居ない。
    // **見えないところで局面が進み続けるほうが困る。**
    if (target !== studyTab && studyTab.classList.contains("is-active")) {
      // 連続解析を持ち主に止めさせ、エンジンを手放す（中身は studytab.ts の leave）。
      studyUI.leave();
    }
    // ⚠️ **訂正タブを離れるときは掴んでいるものを離す。** 「足りない駒」は
    // クリックで掴んだままになるので、そのままタブを移ると**見えない盤に対して
    // 掴んだ状態**が残り、カーソルには駒の絵が付いてくる。
    if (target !== editTab && editTab.classList.contains("is-active")) {
      editUI.release();
    }
    // ⚠️ **切り離した窓は解析タブと一緒に出し入れする**（2026-09-12）。
    // 評価値グラフも候補手も手順も**解析タブの中身**なので、他のタブに居るあいだ
    // 出しておいても読む相手が居ない（起動直後の入力タブも同じ）。
    // ⚠️ **設定は変わらない** —— Go 側がやるのは `Show`/`Hide` だけで、
    // 「切り離しているか」はそのまま（**タブに戻れば同じ形で出し直す**）。
    void CaptureService.SetStudyTabActive(target === studyTab);
    // 棋譜の自動更新は解析タブに居るあいだだけ刻む（**切り離していても呼ぶ**）。
    studyUI.setTabShown(target === studyTab);
    for (const { tab, panel } of tabs) {
      const active = tab === target;
      tab.classList.toggle("is-active", active);
      tab.setAttribute("aria-selected", String(active));
      panel.classList.toggle("is-active", active);
      panel.hidden = !active;
    }
    // ⚠️ **隠れているパネルの中では盤に重ねるグリッドの位置が測れない**
    // （`getScreenCTM()` が null を返す）。開いた瞬間に測り直さないと、
    // グリッドが出ないか前回の大きさのまま残り、1 マスずれたところを編集する。
    if (target === editTab) {
      editUI.relayout();
    }
    // ⚠️ **解析タブの盤にもグリッドが乗っている**（手を進める UI。2026-08-11）。
    // **こちらを落とすと、光った位置と実際に指す位置が 1 マスずれる。**
    // ⚠️ **棋譜タブは開くたびに棚を読み直す。** 同じ DB を kicho アプリからも
    // 触れるので、初回だけ読む作りにすると**向こうで足した棋譜が見えない**
    // （共用にした意味が消える）。入力タブから登録したときも同じ。
    if (target === libraryTab) {
      libraryUI.reveal();
    }
    if (target === studyTab) {
      // 盤のグリッド・グラフ・縦のバーの遊びを測り直し、（連続モードなら）解析を始める
      // （中身と順番は studytab.ts の shown）。
      studyUI.shown();
    }
  };

  for (const { tab } of tabs) {
    tab.addEventListener("click", () => selectTab(tab));
  }

  // ---- ウィンドウ操作（Frameless の代償） ----------------------------------
  //
  // OS のタイトルバーを外したので、最小化・最大化・閉じるを自前で持つ。
  //
  // ⚠️ **✕ だけランタイムではなく Go 側を呼ぶ**（`CaptureService.Quit`）。
  // 自前のボタンは `WindowClosing` を通らないので、`Window.Close()` では
  // **位置・サイズが保存されない**（wails3 skill pitfalls.md）。終了時の後始末は
  // Go 側の 1 本（`quit`）に寄せてある。
  const winMinimise = root.querySelector<HTMLButtonElement>("#win-minimise")!;
  const winMaximise = root.querySelector<HTMLButtonElement>("#win-maximise")!;
  const winClose = root.querySelector<HTMLButtonElement>("#win-close")!;
  winMinimise.addEventListener("click", () => void Window.Minimise());
  winMaximise.addEventListener("click", () => void Window.ToggleMaximise());
  winClose.addEventListener("click", () => void CaptureService.Quit());

  // タイトルバーのダブルクリックで最大化 / 元に戻す（OS のタイトルバーと同じ）。
  //
  // ⚠️ **ボタンの上では無視する**（押した直後にもう一度押すと最大化する、では驚く）。
  // macOS ではランタイムが capture 段階で dblclick を横取りして自分で
  // ToggleMaximise を呼ぶので、ここは呼ばれない（**二重に切り替わらない**）。
  mainToolbar.addEventListener("dblclick", (e) => {
    if ((e.target as HTMLElement).closest("button")) {
      return;
    }
    void Window.ToggleMaximise();
  });

  // ---- 解析タブ（studytab.ts）-----------------------------------------------
  //
  // 盤・盤の周り・十字キー・側の列・評価値グラフ・中継を追う・寸法と切り離し・視点。
  // ⚠️ **ここで組むこと**（今までと同じ位置。イベントを聞く順番と起動時の呼び出しの順番を変えない）。
  const studyUI = mountStudyTab(tabs[3].panel, {
    screen: root,
    isShown: () => studyTab.classList.contains("is-active"),
    frameVisible: () => frameVisible,
    onSettings: (st) => settingsUI.showSettings(st),
  });
  // 訂正タブ → 解析タブ。**受け渡しはこの 1 か所だけ。**
  //
  // ⚠️ **確定しているかの判定は Go 側（StudyService.Adopt）に任せる。** 手番か
  // 駒台の先後が未決ならエラーが返るので、そのまま訂正タブに出して留まる
  // （フロントで同じ判定を書くと 2 か所に散る）。
  //
  // follow が真なら「そのまま追跡」（2026-10-08）—— 渡して解析タブへ移ったあと、
  // **盤の下の「中継を追う」を押すのと同じ**ことをする（入口を 2 本にしない）。
  // ⚠️ **渡せなかったら追い始めない**（訂正タブに留まって理由を出す）。
  // ⚠️ **本譜へ繋ぐ口ではない** —— この局面を**新しい根**にして、そこから追う
  // （「本譜に繋ぐ」を外した理由は `_docs/design-follow.md`）。
  const adoptToStudy = async (follow = false) => {
    // ⚠️ **根が入れ替わるので、中継を追っていたら聞いて止める**（`followguard.ts`）。
    if (!(await confirmStopFollow("この局面を解析する"))) {
      return;
    }
    editUI.setStatus("");
    studyUI.setStatus("");
    try {
      studyUI.showStudy(await StudyService.Adopt());
    } catch (err) {
      editUI.setStatus(String(err instanceof Error ? err.message : err));
      return;
    }
    // ⚠️ **訂正した 1 枚を、中継を追う盤（マスタ）にする**（2026-10-09。Go 側の `SetFollowShot`）。
    // 押した瞬間の 1 枚をマスタにすると、手が盤に被っていたときに初手から「別の盤」になる。
    // ⚠️ **追い始める前に待つこと**（下の `studyFollowBtn.click()` より先に覚えさせる）。
    // 画像の無い局面・画像ファイルなら Go 側が忘れる（押した瞬間の 1 枚で決める）。
    try {
      await CaptureService.SetFollowShot(editUI.shotPath());
    } catch {
      // 覚えられなくても解析は始まる（押した瞬間の 1 枚で決めるだけ。設計原則3）。
    }
    // ⚠️ **見え方を撮った画像に合わせる。** 後手目線で撮った局面は、解析タブへ
    // 渡るときに 180 度回っている（＝先後が実際どおりになっている）ので、
    // **そのまま描くと盤が上下逆に見える**。表示視点を後手にすれば、
    // 局面は正しいまま、見た目は撮った画像と同じ向きになる。
    // **あとから解析タブのボタンで自由に戻せる。**
    studyUI.setViewpoint(!editUI.nearWhite());
    selectTab(studyTab);
    // ⚠️ **追っていたなら上の確認で止まっている**ので、ここでは必ず「入れる」側になる。
    // ⚠️ **ボタンを押す形にしないこと** —— 画面の写し（`followOn`）は止めた知らせが届くまで古いので、押すと止める側に倒れる。
    if (follow) {
      studyUI.startFollow();
    }
  };

  // ---- 棋譜タブ（棚）------------------------------------------------------
  //
  // ⚠️ **一覧も検索も Go 側（KifuService）が持つ。** ここは行き先を繋ぐだけ。
  const libraryUI = mountLibrary(root, {
    confirmAnalyze: () => confirmStopFollow("棋譜を解析タブへ送る"),
    onAnalyze: (got) => {
      // ⚠️ **入力タブの棋譜読み込みと同じ描き方に合流させる**（`KifuLoad` を
      // 共有しているのはそのため）。**別の経路を作らないこと。**
      //
      // ⚠️ **タブを先に開いてから描くこと。** 手順のリストは「今見ている手」を
      // scrollIntoView で見せるが、`display: none` の中では効かない。
      selectTab(studyTab);
      studyUI.showStudy(got.state);
    },
  });

  // 枠を出す唯一の入口（**起動時は出ていない**。閉じても隠れるだけなので出し直せる）。
  //
  // ⚠️ **タイトルバーに置いてある**（2026-08-18 に入力タブから上げた）。枠は
  // どのタブに居ても出したくなるもので、入力タブの中にあると**そのたびに
  // タブを行き来する**ことになる。
  //
  // ⚠️ **押すたびに出す/隠すが入れ替わる**（2026-08-18）。枠の ✕ でも隠せるが、
  // **出した口と同じ場所で戻せるのが素直** —— 枠が中継の裏や別モニタにあるとき、
  // ✕ を押しに行くほうが遠い。⚠️ **どちらの経路で隠しても状態は 1 つ**
  // （`frame:visible`）なので、2 つあっても食い違わない。
  //
  // ⚠️ **アイコンなので、意味は aria-label / title が持つ。** どちらも**状態で
  // 書き換えること** —— 絵が同じままなので、文字が変わらないと今どちらなのかが
  // 読めない（見た目の区別は `aria-pressed` の色が持つ）。
  //
  // ⚠️ **状態は Go 側が知らせる**（`frame:visible`）—— 枠の ✕ で隠したことは
  // メイン画面からは分からないので、自前で覚えると**隠れているのに
  // 「出ています」のまま**になる。
  mountIssues(root.querySelector<HTMLButtonElement>("#issues-btn")!);

  const frameToggle = root.querySelector<HTMLButtonElement>("#frame-toggle")!;
  let frameVisible = false;
  const showFrameState = (visible: boolean) => {
    frameVisible = visible;
    frameToggle.setAttribute("aria-pressed", String(visible));
    const label = visible ? "ガイド枠を隠す" : "ガイド枠を表示";
    frameToggle.setAttribute("aria-label", label);
    frameToggle.title = visible
      ? "ガイド枠を隠します（位置は覚えているので、出し直せば同じ領域に戻ります）"
      : "ガイド枠を表示します（撮りたい盤面に合わせてから、枠のカメラで撮ります）";
  };
  showFrameState(false);
  frameToggle.addEventListener("click", () => {
    // ⚠️ **ここで状態を書き換えない。** 反映するのは `frame:visible` を受けたときだけ
    // （2 か所に持つと食い違う）。隠す経路が枠の ✕ にもあるので、なおさら。
    if (frameVisible) {
      void CaptureService.HideFrame();
    } else {
      void CaptureService.ShowFrame();
    }
  });
  // 枠の出入り。**押した結果もここで受ける**（ShowFrame / HideFrame が Go 側から
  // 知らせる）ので、押したときに自分で状態を書き換えない。
  Events.On("frame:visible", (event: { data: boolean }) => {
    showFrameState(event.data);
    // ⚠️ **枠が出たら追跡の状態を送り直すこと。** 枠は出たばかりで何も知らないので、
    // **追っている最中に枠を出し直すと「追跡中」が出ないまま**になる
    // （状態を持っているのはこちらだけ）。
    if (event.data) {
      studyUI.publishFollow();
    }
  });
  // 起動した時点で出ていることがある（設定「起動時に盤面を探す」）。
  void (async () => {
    try {
      showFrameState(await CaptureService.FrameVisible());
    } catch {
      /* 取れなくても「隠れている」で始めれば押せる（設計原則3）。 */
    }
  })();

  // ---- 設定タブ（settingstab.ts）----
  //
  // ⚠️ **設定が効く先へ配るのはここ 1 本**（設定タブは `onSettings` で呼ぶだけ）。
  // 側の列で書き換えたときも、切り離した窓から `settings:changed` が来たときも、
  // 設定タブの `showSettings` を通ってここに来る。
  const applySettings = (s: AppSettings) => {
    // 解析タブに効くもの（切り離し・縦軸・盤の上の 1 行・勝率バー・折れ線の色・側の列）。
    studyUI.applySettings(s);
    // 送信ボタンを出すかどうかはこの設定で決まる。
    editUI.setTrainEnabled(s.training.enabled);
  };
  const settingsUI = mountSettingsTab(tabs[4].panel, {
    pieceBoards: [editUI.board, studyUI.board],
    onSettings: applySettings,
    onRecognizer: (st) => editUI.showRecognizer(st),
    // 棚を開き直したら一覧を取り直す。
    onKifuDBChanged: () => libraryUI.refresh(),
  });

  // 起動時は読み込み直さず、Go 側が起動時に読んだ結果をそのまま出す
  // (表示のためだけに 3.5MB を読み直さない)。
  void (async () => {
    try {
      editUI.showRecognizer(await CaptureService.Recognizer());
    } catch {
      /* 状態が取れないだけなので黙って諦める。実害は最初のキャプチャで分かる。 */
    }
  })();

  // 枠のメニュー(▼ → 設定)からこの画面を呼び出したときに、開いてほしいタブが来る
  // (CaptureService.ShowMain)。**この画面は隠れていてもフロントは動いている**ので、
  // 前面に出る前に届く。知らないタブ名は無視して今のタブのままにする。
  Events.On("main:tab", (event: { data: string }) => {
    const target = root.querySelector<HTMLButtonElement>(`#tab-${event.data}`);
    if (target && tabs.some(({ tab }) => tab === target)) {
      selectTab(target);
    }
  });
  // ⚠️ **ホットキーの登録失敗は画面に出さない**（2026-08-10）。案内していない操作なので、
  // 失敗を伝えても何をすればよいか分からない。撮る手段は枠のツールバーのカメラで、
  // そちらは無関係に効く。理由が要るときは Go 側の warn ログを読むこと
  // （`hotkey:register-failed` イベント自体は残してある。出す先が要るときのために）。
}
