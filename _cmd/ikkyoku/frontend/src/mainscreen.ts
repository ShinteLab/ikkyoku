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
//            (自由編集・合法性を問わない・手番も駒台の先後も未決でよい)
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
import { Clipboard, Events, Window } from "@wailsio/runtime";
import {
  FiAlertTriangle,
  FiCopy,
  FiImage,
  FiMaximize,
  FiMinus,
  FiSquare,
  FiX,
} from "react-icons/fi";
// ⚠️ **CaptureService だけ出所が違う。** あれはウィンドウ（枠）と HWND を触るので
// `_cmd/ikkyoku` に残っており、他の Service は `ikkyoku/app` にある。
import { CaptureService } from "../bindings/ikkyoku";
import {
  AnalyzeService,
  StudyService,
} from "../bindings/github.com/ShinteLab/ikkyoku/app";
import { iconMarkup } from "./icon";
import { mountEditor } from "./editor";
import { mountLibrary } from "./library";
import { mountInputTab } from "./inputtab";
import { mountIssues } from "./issues";
import { hideHint } from "./hint";
import { mountSettingsTab } from "./settingstab";
import { mountTrainSend, type ShotRegion } from "./trainsend";
import { mountStudyTab } from "./studytab";
import type { RecognizerStatus } from "../bindings/ikkyoku/models";
import type { AppSettings } from "../bindings/github.com/ShinteLab/ikkyoku/app/models";
// 認識の観測情報。**型を手で書き写さない**(Go 側は suteme の型をそのまま通しており、
// ここで別に定義すると矩形の意味がずれても気づけない)。
import type { Debug } from "../bindings/github.com/ShinteLab/suteme";
import { confirmStopFollow } from "./followguard";

// 「撮れた」ことだけを伝えるイベントのペイロード（Go 側 CaptureShot / `capture:shot`）。
//
// ⚠️ **認識結果は載っていない**（まだ走っている）。撮ってから認識が終わるまでは
// 数秒あるので、そこを 1 つのイベントにまとめると**待っているあいだ画面が
// 前の 1 枚のまま**になる。**続きは capture:done が丸ごと持ってくる。**
//
// **bindings から取れない**（メソッドの戻り値ではないため）。CaptureResult と同じで、
// **Go 側を変えたらここも直すこと。**
interface CaptureShot {
  path: string;
  width: number;
  height: number;
  thumbnail: string;
  // 出どころ（"screen" = 撮った / "file" = 画像ファイルを読み込んだ）。
  // ⚠️ **これで振る舞いを変えないこと**（2026-09-12）——変わるのは言い回しだけで、
  // 認識も訂正も行き先（訂正タブ）も同じ。
  source?: string;
}

interface CaptureResult {
  path: string;
  width: number;
  height: number;
  thumbnail: string;
  source?: string;
  sfen: string;
  confidence: number;
  warnings: string[];
  handTotal: Record<string, number>;
  recognizeError: string;
  debug?: Debug | null;
}

// これを下回ったら検出を疑う。盤が映っていない画面を撮ったときにここが落ちる。
const LOW_CONFIDENCE = 0.75;

// マスの確信度がこれを下回ったら重ね表示で目立たせる。
//
// **盤面検出の信頼度(LOW_CONFIDENCE)とは別物。** k-NN の確信度は「上位k件の投票で
// 勝ったクラスの重み比率」なので、迷いが無ければ 1.0 に張り付く。下がっているマスは
// 「似た候補が競っている」＝訂正の候補、という読み方になる。
const LOW_CELL_CONFIDENCE = 0.8;


// 盤面領域の決め方の表示名。**どれも異常ではない。**
// ikkyoku はガイド枠を盤に合わせて撮るので、盤の縁が画像端に来る whole は
// むしろ想定どおりの経路(枠が合っているほど whole になる)。
const REGION_SOURCE_LABEL: Record<string, string> = {
  detect: "画像内から検出",
  whole: "画像全体を盤とみなした",
  option: "座標を明示指定",
};

const SVG_NS = "http://www.w3.org/2000/svg";

// 段(row)・筋(col)から人が読めるマス名を作る。row 0 = 一段、col 0 = 9筋。
const RANK_KANJI = ["一", "二", "三", "四", "五", "六", "七", "八", "九"];
const cellName = (row: number, col: number) => `${9 - col}${RANK_KANJI[row] ?? "?"}`;

// 駒台の表示順(飛角金銀桂香歩)。SFEN の駒文字をそのまま並べる。
const HAND_ORDER = ["R", "B", "G", "S", "N", "L", "P"];
const HAND_LABEL: Record<string, string> = {
  R: "飛", B: "角", G: "金", S: "銀", N: "桂", L: "香", P: "歩",
};

// registerBoardFont は駒文字用フォント(ShogiSFEN)をドキュメント側に登録する。
//
// core/web の `<shogi-board>` は `@font-face` を Shadow DOM 内の style に持っているが、
// **Chromium は shadow root 内の `@font-face` を無視する**(フォントはドキュメント単位で
// 解決される)。そのため登録しないと駒がラテン文字(l/n/s/g/k…)のまま描画される。
//
// ここでやっているのは同じ data URL をドキュメントにも登録し直すことだけで、
// フォントの実体も定義も core/web の font.js のまま。**フォントをコピーはしない。**
// 本来は core/web 側で登録するのが筋なので、直ったらこの関数は消せる。
async function registerBoardFont(): Promise<void> {
  const id = "shinte-shogi-font";
  if (document.getElementById(id)) {
    return;
  }
  const fontModule = "/shinte-web/font.js";
  const { FONT_DATA_URL } = await import(/* @vite-ignore */ fontModule);
  const style = document.createElement("style");
  style.id = id;
  style.textContent =
    `@font-face { font-family: "ShogiSFEN"; src: url("${FONT_DATA_URL}") format("truetype"); }`;
  document.head.appendChild(style);
}

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
            <span id="library-modal-title" class="field-label"></span>
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
        <!-- 上段。左に警告、右に「認識結果に戻す」。
             局面として成立していない点は**盤より上に出す**(訂正しながら見るものなので、
             盤の下だと見落とすし、件数で下の行が動く)。中身は**今の局面**(EditState)の
             値で、「認識詳細情報」側の同じ見出しとは別物(あちらは認識した時点の記録)。
             やり直しのボタンは**訂正した内容を捨てる操作**なので、押し間違えないよう
             盤から遠い右上に離し、赤くしてある。 -->
        <div class="board-head">
          <ul id="board-warnings" class="warnings is-compact" hidden></ul>
          <!-- ⚠️ **表示視点（反転）のボタンはここに戻さないこと**（2026-08-18 に外した）。
               訂正は**撮った画像と見比べて直す**作業なので、盤は撮ったとおりで固定する。
               裏から眺める選択肢があると、盤の下の「目線」と混同する
               （あちらは**局面そのものの向き**の話で、押すと解析へ渡す局面が回る）。
               反転が要るのは解析タブのほうで、そちらには残してある。 -->
          <button id="edit-reset" class="danger-btn" type="button" hidden
                  title="訂正を捨てて、認識したときの盤面に戻します">認識結果に戻す</button>
        </div>

        <div class="board-area">
          <!-- 撮った画像。**訂正中だけ盤の左に出す**(確定したら畳む)。
               訂正は「原本を見ながら 1 マスずつ直す」作業なので、原本が折りたたみの
               中にあると成立しない（**畳んでいても必ず見えていること**）。
               **盤は 560px より大きくならない**ので、
               左の余白はもともと遊んでおり、ここに置くのは盤を狭めない。

               ⚠️ **盤だと判定した矩形とマス割りをここに重ねる**(2026-09-13)。
               以前は矩形で黙って切り取るだけで、**矩形そのものは「認識詳細情報」の
               折りたたみの中にしか無かった**。そのため認識がおかしいときに
               「矩形がずれているのか、駒種を外しているのか」が訂正の手を止めずには
               分からず、**撮った画像のほうを疑うことになった**(2026-09-12 の
               撮った 1 枚。実際には矩形はピクセル単位で合っていた)。
               **判断に要るものを、判断する場所に出す。**

               ⚠️ **切り取っても余白を残すこと。** 矩形が画像の縁と重なると線が
               見えず、「判定した範囲」を確かめられない(切り取ったことすら分からない)。

               ⚠️ **信頼度・保存先・推論器は「認識詳細情報」のまま。** あちらは
               「認識がどれくらい外したか」の記録で、ここは「今直している 1 枚」。
               もとの画像は「認識詳細情報」と**同じ CaptureResult.thumbnail**。 -->
          <div id="capture-ref" class="capture-ref" hidden>
            <div class="capture-ref-head">
              <span class="field-label">撮った画像</span>
              <button id="capture-ref-scope" class="ghost-btn is-tiny" type="button"
                      title="盤だと判定した範囲を拡大するか、撮った画像の全体を出すかを切り替えます">画像全体</button>
            </div>
            <svg id="capture-ref-svg" class="capture-ref-svg" preserveAspectRatio="xMidYMid meet"
                 role="img" aria-label="訂正のもとになった画像">
              <image id="capture-ref-image" />
              <g id="capture-ref-marks" class="capture-ref-marks"></g>
            </svg>
          </div>
          <!-- 盤と駒台の配置。**後手の駒台は盤の左上、先手の駒台は右下**
               (実際の将棋盤と同じ並び)。訂正モードのときだけ出る。 -->
          <div id="board-with-hands" class="board-with-hands">
            <div id="hand-white-slot" class="hand-slot"></div>
            <!-- .board-stage は盤と同じ大きさの箱。訂正 UI の 9x9 グリッドを
                 ここに重ねる(editor.ts)。**幅の上限は盤の固有サイズと揃えること**
                 (--board-max。ずれるとマスの当たり判定が 1 マスずれる)。 -->
            <div id="board-stage" class="board-stage">
              <shogi-board id="board" hidden></shogi-board>
            </div>
            <!-- 「足りない駒」は先手の駒台の**上**（後手の駒台と左右対称の位置）。
                 縦 1 列に 8 種を並べるので、盤の半分の高さが要る。 -->
            <div id="missing-slot" class="hand-slot"></div>
            <div id="hand-black-slot" class="hand-slot"></div>
          </div>
          <p id="board-placeholder" class="board-placeholder">まだ撮っていません。</p>
        </div>
        <!-- 目線（2026-08-18 に手番の行から盤の下へ出した）。中身は editor.ts が入れる。
             **撮った画像がどちら側から写したものか**で、⚠️ **盤は 1 マスも動かない**
             （効くのは解析へ渡すときに 180 度回すかどうかだけ）。

             ⚠️ **手番の行に戻さないこと。** 「目線」と「手番」は別の事実なのに、
             同じ見た目のボタンが隣り合っていると同じものの選択肢に見える。
             ⚠️ **盤のすぐ下に置くこと** —— 言っているのは「この盤がどちら向きか」なので、
             盤から離すと何に対する設定なのか分からなくなる。


             ⚠️ **「手合割か」をここに並べないこと**（2026-09-12 に一度置いて外した）。
             あれは「足りない駒」の枠の意味そのものを変えるので、**枠の中**に置く
             （missing-zone のチェック）。ここに出すと、何が変わるのか分からない。 -->
        <div id="edit-view-row" class="edit-view-row"></div>

        <!-- 認識詳細情報（旧デバッグタブ）。**盤の下・「この局面を解析する」の上**
             （2026-08-18。盤の上＝警告のすぐ下から移した）。

             ⚠️ **盤より上へ戻さないこと。** 開くと**盤が下へ押される**ので、
             中を見たいときに**盤が視界から消える**（そのために訂正タブを
             スクロールさせていた）。下なら**盤は動かず、下に伸びるだけ**。

             ⚠️ **幅は「撮った画像 + 盤」の全体**（盤の左の列に押し込まない）。
             中身は横に長い行（推論器のパス・警告の文）と等倍のサムネイルなので、
             半分の幅では折り返しと縦スクロールだらけになる。

             ⚠️ **中身は認識した時点の記録**（CaptureResult）。盤の上の警告や
             盤の脇の駒台（EditState）とは**別の値**で、訂正しても変わらない。
             同じ見出しが 1 つの画面に 2 度出るが、**読む目的が違う**ので
             片方を消さないこと（上＝今の局面を直すための情報、
             ここ＝認識がどれくらい外したかの記録）。

             ⚠️ **中は「撮った画像 → 認識の情報」の順**（2026-08-18 に入れ替えた）。
             最初に見るのは**撮れているかどうか**で、確信度や推論器はそれを見た
             あとの話。畳んだ状態から開いて、まず画像が目に入るのが正しい。

             ⚠️ **撮った画像（.capture-ref）はこの折りたたみの外**（盤の左）。
             訂正は「原本を見ながら直す」作業なので、**畳んだら消えるところに
             原本を置かない**（中にも画像はあるが、あちらは重ね表示つきの記録）。 -->
        <details id="debug-details" class="debug-details">
          <summary id="debug-summary">認識詳細情報</summary>
          <div class="debug-body">
            <div class="debug-shot">
              <div class="debug-shot-head">
                <span class="field-label">撮った画像</span>
                <button id="shot-path" class="path-btn" type="button" hidden>
                  <span class="path-name"></span>${iconMarkup(FiCopy)}
                </button>
                <button id="shot-copy-image" class="path-btn is-icon-only" type="button" hidden
                        aria-label="画像をコピー"
                        title="画像そのものをクリップボードにコピー">${iconMarkup(FiImage)}</button>
                <label class="overlay-toggle">
                  <input id="overlay-toggle" type="checkbox" checked />
                  認識の重ね表示
                </label>
              </div>
              <div id="shot" class="shot" hidden>
                <img id="thumbnail" class="thumbnail" alt="直近のキャプチャ" />
                <svg id="overlay" class="overlay" preserveAspectRatio="none" aria-hidden="true"></svg>
              </div>
            </div>

            <div class="debug-row">
              <button id="reload-btn" class="ghost-btn" type="button"
                      title="学習データを更新したあとに押すと、認識器を読み込み直します">認識器を再読み込み</button>
              <p id="recognizer" class="recognizer"></p>
            </div>

            <div id="confidence-row" class="hand-row" hidden>
              <span class="field-label">検出</span>
              <span id="confidence" class="note"></span>
            </div>
            <div id="region-row" class="hand-row" hidden>
              <span class="field-label">盤面</span>
              <span id="region" class="note"></span>
            </div>
            <div id="predictor-row" class="hand-row" hidden>
              <span class="field-label">推論器</span>
              <span id="predictor" class="note"></span>
            </div>
            <div id="hand-row" class="hand-row" hidden>
              <span class="field-label">駒台</span>
              <span id="hand" class="hand"></span>
              <span class="note">先後不明</span>
            </div>
            <ul id="warnings" class="warnings" hidden></ul>
          </div>
        </details>

        <!-- ⚠️ **この並びを崩さないこと**（2026-08-18 に並べ替えた）:
             **手番・手数 → SFEN → 訂正データを送信 → この局面を解析する。**

             上から順に効く。**SFEN は手番が決まって初めて組み上がり**、
             送信はその SFEN を送り、解析はそこまでが済んで初めて通る。
             以前は「この局面を解析する」が一番上（手番より上）にあり、
             **手番を入れないと押せないボタンが、その手番より上にある**という
             順序になっていた。 -->
        <div id="editor" class="editor"></div>
        <p id="edit-status" class="status" role="status" aria-live="polite"></p>
        <!-- ⚠️ **「解析へ渡す SFEN」の行と「駒台」の行はここに戻さないこと**
             （2026-08-18 に外した）。

             駒台は**盤の脇に駒そのものが並んでいる**ので、文字の要約は同じことを
             2 度言っているだけだった。解析へ渡す SFEN は、**後手目線のときだけ
             行が増えて下がずれる**うえ、丸ごと読みたい場面がほとんど無い。

             ⚠️ **黙って回さない、という約束は残す** —— 後手目線のときは
             SFEN の後ろに「（解析へは反転して送信）」と添える（下の #sfen-note）。 -->
        <div class="sfen-row">
          <span class="field-label">SFEN</span>
          <code id="sfen" class="sfen">-</code>
          <!-- ⚠️ **後手目線のときだけ出す。** 画面に出ている局面と、エンジンが読む
               局面が上下逆になるので、**何も言わずに回さない**。全文は title に出す。 -->
          <span id="sfen-note" class="note is-caution" hidden>（解析へは反転して送信）</span>
        </div>
        <!-- 訂正した局面を suteme の学習データとして送る。設定で有効にしていない
             ときは行ごと出さない。**押したときだけ送る**(自動送信はしない)。

             ⚠️ **確定を待たない。** 送るのは labelSfen（未確定でも持ち駒を
             落とさない画像ラベル用の SFEN）なので、手番や駒台の先後が決まって
             いなくても学習の役には立つ（設計原則3）。何が落ちるかは note に出る。
             ⚠️ **だから「この局面を解析する」より上**（手番に依らない操作を、
             手番が決まって初めて押せるボタンの下に置かない）。 -->
        <div id="train-row" class="train-row" hidden>
          <button id="train-send" class="ghost-btn" type="button"
                  title="この画像と訂正した盤面を、suteme の学習データとして登録します">訂正データを送信</button>
          <!-- 送る SFEN のために妥協した点(手番が未決・先後未決の持ち駒)。
               **送る前に出す**(何が落ちるか分からないまま送らせない)。 -->
          <span id="train-note" class="note is-caution"></span>
          <span id="train-send-status" class="note"></span>
        </div>
        <!-- 訂正タブの唯一の出口。中身は editor.ts が入れる（ボタンと操作の説明）。 -->
        <div id="edit-confirm-row" class="edit-bar"></div>
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
    editLoaded: () => editLoaded,
    // ⚠️ **画像の無い局面を訂正タブに入れる口なので、撮った画像に紐づくものを片付ける**（`clearShotContext`）。
    editHandicap: async (handicap) => {
      await editor.loadHandicap(handicap);
      clearShotContext();
      selectTab(editTab);
    },
    editEmpty: async () => {
      await editor.loadEmpty();
      clearShotContext();
      selectTab(editTab);
    },
    onKifuSaved: () => libraryUI.refresh(),
  });

  const reloadBtn = root.querySelector<HTMLButtonElement>("#reload-btn")!;
  const recognizer = root.querySelector<HTMLParagraphElement>("#recognizer")!;
  const board = root.querySelector<HTMLElement>("#board")!;
  const placeholder = root.querySelector<HTMLParagraphElement>("#board-placeholder")!;
  const sfenOut = root.querySelector<HTMLElement>("#sfen")!;
  // 「解析へは反転して送信」。**後手目線のときだけ出す**（SFEN の後ろに添える）。
  const sfenNote = root.querySelector<HTMLElement>("#sfen-note")!;
  const confidenceRow = root.querySelector<HTMLDivElement>("#confidence-row")!;
  const confidenceOut = root.querySelector<HTMLElement>("#confidence")!;
  // 駒台と警告は両方のタブに出るが、**出所が違う**。
  //
  //   「認識詳細情報」 … 撮って認識した時点の値。**訂正しても変わらない**
  //                  (どれくらい外したかの記録なので、直した後の値では意味が無い)
  //   盤面タブ     … **今の局面**の値(EditState)。訂正するたびに変わる
  //
  // 同じ見た目で別の値なので、**片方の更新をもう片方に流用しないこと。**
  const handRow = root.querySelector<HTMLDivElement>("#hand-row")!;
  const handOut = root.querySelector<HTMLElement>("#hand")!;
  const warnings = root.querySelector<HTMLUListElement>("#warnings")!;
  const boardWarnings = root.querySelector<HTMLUListElement>("#board-warnings")!;
  const status = inputUI.captureStatus;
  const shot = root.querySelector<HTMLDivElement>("#shot")!;
  const shotPath = root.querySelector<HTMLButtonElement>("#shot-path")!;
  const shotPathName = shotPath.querySelector<HTMLSpanElement>(".path-name")!;
  const shotCopyImage = root.querySelector<HTMLButtonElement>("#shot-copy-image")!;
  const thumbnail = root.querySelector<HTMLImageElement>("#thumbnail")!;
  const overlay = root.querySelector<SVGSVGElement>("#overlay")!;
  const overlayToggle = root.querySelector<HTMLInputElement>("#overlay-toggle")!;
  const regionRow = root.querySelector<HTMLDivElement>("#region-row")!;
  const regionOut = root.querySelector<HTMLElement>("#region")!;
  const predictorRow = root.querySelector<HTMLDivElement>("#predictor-row")!;
  const predictorOut = root.querySelector<HTMLElement>("#predictor")!;

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
  // 認識詳細情報（旧デバッグタブ）。**盤の下・「この局面を解析する」の上**
  // （2026-08-18 に盤の上から移した。開いても盤が動かない位置）。
  const debugDetails = root.querySelector<HTMLDetailsElement>("#debug-details")!;
  const debugSummary = root.querySelector<HTMLElement>("#debug-summary")!;

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
      editor.release();
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
      editor.relayout();
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

  // 警告やエラーは折りたたみの中にあるので、畳んでいるあいだは気づけない。
  // 見出しに点を出して「開くべきものがある」ことだけ伝える（開けば消える）。
  //
  // ⚠️ **勝手に開かない。** 訂正のあいだ盤の上に積む行は短く保ちたいので、
  // 開くかどうかはユーザーが決める（警告そのものは上の board-head にも出ている）。
  // 警告・エラー・信頼度の低下は、畳んでいるあいだ見えない。**見出しに点を出すだけ**で
  // 「開くべきものがある」ことを伝える（開けば消える）。⚠️ **勝手に開かないこと。**
  const markDebug = (level: "" | "warn" | "error") => {
    debugSummary.classList.remove("has-warn", "has-error");
    if (debugDetails.open || level === "") {
      return;
    }
    debugSummary.classList.add(level === "error" ? "has-error" : "has-warn");
  };
  debugDetails.addEventListener("toggle", () => {
    if (debugDetails.open) {
      debugSummary.classList.remove("has-warn", "has-error");
    }
    // ⚠️ **盤より下にあるので開いても盤は動かない**が、訂正タブはスクロールするので
    // 測り直しは残す（**大きさが変わりうる場面を 1 つでも落とすと、駒の見た目と
    // クリック領域がずれる**）。
    editor.relayout();
  });

  // <shogi-board> は core/web の Web Component。Go 側が core の embed から
  // /shinte-web/ 配下に配信している(shinteweb.go)。**フロントにファイルをコピーしない**
  // ので、盤の描画と SFEN の解釈は core の 1 実装のままになる。
  //
  // 実行時に取りに行く URL であって、バンドルに含める依存ではない。
  // パスを変数に逃がしてあるのは、リテラルのままだと tsc が実在しないモジュールとして
  // 解決に失敗するため(@vite-ignore は Vite にしか効かない)。
  void (async () => {
    try {
      const boardModule = "/shinte-web/shogi-board.js";
      await import(/* @vite-ignore */ boardModule);
      await registerBoardFont();
    } catch (err) {
      placeholder.textContent = `盤面コンポーネントを読み込めませんでした: ${String(err)}`;
      placeholder.classList.add("is-error");
    }
  })();

  // 訂正 UI。**盤に描くのは常にここが持つ局面**(EditState)で、認識結果を直接は描かない。
  // 撮った直後は認識結果そのものが入っているので見た目は同じだが、訂正すると
  // 盤・SFEN・駒台・警告がその場で追従する。**2 つの出所を混ぜないこと。**
  const boardStage = root.querySelector<HTMLElement>("#board-stage")!;

  // 訂正中に盤の左へ出す「撮った画像」。**「認識詳細情報」のサムネイルと同じ値**を描くだけで、
  // 別の経路で取り直さない(片方だけ更新されると、どちらが今の 1 枚か分からなくなる)。
  //
  // 出す条件は「画像がある」だけ(訂正タブそのものが訂正モードなので、以前あった
  // 「訂正中かどうか」の条件は消えた)。入れ替えは setCaptureRef のみ。
  const captureRef = root.querySelector<HTMLDivElement>("#capture-ref")!;
  const captureRefSvg = root.querySelector<SVGSVGElement>("#capture-ref-svg")!;
  const captureRefImage = root.querySelector<SVGImageElement>("#capture-ref-image")!;
  const captureRefMarks = root.querySelector<SVGGElement>("#capture-ref-marks")!;
  const captureRefScope = root.querySelector<HTMLButtonElement>("#capture-ref-scope")!;
  // 認識の重ね表示（盤だと判定した矩形とマス割り）を SVG へ描く。
  //
  // **描き先は 2 つある** ——「認識詳細情報」の記録（`#overlay`）と、訂正中に盤の左へ
  // 出す参照画像（`#capture-ref`）。⚠️ **2 つに分けて書かないこと。** 同じ debug から
  // 違う絵が出ると、どちらが本当なのか分からなくなる。見た目の違いは CSS 側
  // （`.overlay` / `.capture-ref-marks`）が持つ。
  //
  // ⚠️ **viewBox は呼び出し側が決める。** ここは画像の座標系にそのまま置くだけで、
  // 全体を出すのか矩形を拡大するのかを知らない（知ると 2 か所で同じ判断をすることになる）。
  const paintRecognition = (target: SVGElement, debug: Debug) => {
    const rect = (
      r: { Min: { X: number; Y: number }; Max: { X: number; Y: number } },
      cls: string,
      tip?: string,
    ) => {
      const el = document.createElementNS(SVG_NS, "rect");
      el.setAttribute("x", String(r.Min.X));
      el.setAttribute("y", String(r.Min.Y));
      el.setAttribute("width", String(r.Max.X - r.Min.X));
      el.setAttribute("height", String(r.Max.Y - r.Min.Y));
      el.setAttribute("class", cls);
      // 線の太さは画像の拡縮に引きずられると見えなくなるので、画面上の px で固定する。
      el.setAttribute("vector-effect", "non-scaling-stroke");
      if (tip) {
        const title = document.createElementNS(SVG_NS, "title");
        title.textContent = tip;
        el.appendChild(title);
      }
      target.appendChild(el);
    };

    for (const c of debug.cells ?? []) {
      // category 0 = 空。空マスは枠だけにして、駒のあるマスの確信度を目立たせる。
      const empty = c.piece === "";
      const cls =
        empty ? "cell is-empty"
        : c.confidence < LOW_CELL_CONFIDENCE ? "cell is-low"
        : "cell";
      rect(
        c.rect,
        cls,
        `${cellName(c.row, c.col)} ${c.piece || "空"} ${Math.round(c.confidence * 100)}%`,
      );
    }
    // 外枠は最後に描いて、マスの線の上に来るようにする。
    rect(debug.region, "region");
  };

  // 参照画像は **SVG 1 枚**に「撮った画像・盤だと判定した矩形・マス割り」をまとめて描く
  // （2026-09-13 に `<img>` + canvas の切り取りから作り直した）。
  //
  // ⚠️ **画像と格子を別々に更新しないこと。** 描く材料は `refShot` ただ 1 つで、
  // `renderCaptureRef` がそこから**画像も矩形もマス割りも一度に**描き直す。
  // **「新しい画像に前の認識の格子が乗る」という状態が作れない**のがこの形の要点。
  // 以前は canvas で切り取っており `img.onload` を挟む非同期だったため、
  // 撮り直しの前後で一瞬ぶれる余地があった（**実際に「前の格子が出ている」と
  // 疑う元になった**。2026-09-12 に撮った 1 枚）。
  //
  // ⚠️ **切り取りは viewBox でやる（画素を作り直さない）。** `<image>` は常に
  // 撮った画像の座標系そのままに置き、viewBox だけを狭める。だから**矩形も
  // マス割りも画像と同じ座標系**にあり、拡大・縮小でずれようがない。
  // 学習へ送る矩形（`lastRegion`）と同じ値をそのまま描いている。
  //
  // ⚠️ **認識できていないときは全体に倒す**（設計原則3）。矩形が無いだけで、
  // 撮った 1 枚は必ず見える。
  const REF_MARGIN_CELLS = 0.6; // 切り取るときに矩形の外へ残す余白（1 マスの何倍か）
  type RefShot = {
    thumbnail: string;
    path: string;
    width: number;
    height: number;
    debug?: Debug | null;
  };
  let refShot: RefShot | null = null;
  // 画像の全体を出すか。**押されたら覚える**（撮り直しても保つ。矩形を疑って
  // 全体にした人は、次の 1 枚も全体で見たいはず）。
  let refWhole = false;

  const renderCaptureRef = () => {
    captureRefMarks.replaceChildren();
    const cur = refShot;
    if (!cur || !cur.thumbnail) {
      captureRefImage.removeAttribute("href");
      captureRefSvg.removeAttribute("viewBox");
      captureRefScope.hidden = true;
      return;
    }
    const dbg = cur.debug;
    // 撮った画像の座標系。**debug があればそちらが正**（原点が 0,0 とは限らない）。
    const b = dbg?.image_bounds;
    const ix = b ? b.Min.X : 0;
    const iy = b ? b.Min.Y : 0;
    const iw = b ? b.Max.X - b.Min.X : cur.width;
    const ih = b ? b.Max.Y - b.Min.Y : cur.height;

    captureRefImage.setAttribute("href", cur.thumbnail);
    captureRefImage.setAttribute("x", String(ix));
    captureRefImage.setAttribute("y", String(iy));
    captureRefImage.setAttribute("width", String(iw));
    captureRefImage.setAttribute("height", String(ih));
    captureRefSvg.setAttribute("aria-label", `訂正のもとになった画像: ${cur.path}`);

    // 矩形が無ければ切り取れない。**ボタンごと畳む**（押しても何も変わらない
    // ボタンを出さない）。
    const region = dbg?.region;
    captureRefScope.hidden = !region;
    const whole = refWhole || !region;
    captureRefScope.textContent = whole ? "盤だけ" : "画像全体";
    captureRefScope.title = whole
      ? "盤だと判定した範囲だけを拡大します"
      : "撮った画像の全体を出します（判定した範囲がどこかを確かめられます）";

    if (whole || !region) {
      captureRefSvg.setAttribute("viewBox", `${ix} ${iy} ${iw} ${ih}`);
    } else {
      // 余白は 1 マスぶんから決める。矩形の線が画像の縁と重なると見えないため。
      const cell = Math.min(
        (region.Max.X - region.Min.X) / 9,
        (region.Max.Y - region.Min.Y) / 9,
      );
      const m = Math.max(1, cell * REF_MARGIN_CELLS);
      const x = Math.max(ix, region.Min.X - m);
      const y = Math.max(iy, region.Min.Y - m);
      const w = Math.min(ix + iw, region.Max.X + m) - x;
      const h = Math.min(iy + ih, region.Max.Y + m) - y;
      captureRefSvg.setAttribute("viewBox", `${x} ${y} ${w} ${h}`);
    }
    if (dbg) {
      paintRecognition(captureRefMarks, dbg);
    }
  };

  captureRefScope.addEventListener("click", () => {
    refWhole = !refWhole;
    renderCaptureRef();
  });

  // showShot / showResult / clearShotContext から呼ぶ**唯一の口**。
  //
  // ⚠️ **`refShot` を直に書き換えないこと。** 描き直しを忘れると画像だけ入れ替わり、
  // **格子が前の 1 枚のまま残る** —— この形にしたのはまさにそれを起こせなくするため。
  //
  // **訂正タブに居るあいだは画像があれば常に出す**（タブそのものが訂正モードなので、
  // 以前の「訂正中だけ」という条件は画像の有無だけになった）。
  const setCaptureRef = (next: RefShot | null) => {
    refShot = next && next.thumbnail ? next : null;
    captureRef.hidden = !refShot;
    renderCaptureRef();
  };

  // ---- 訂正データの送信（suteme への還元。trainsend.ts）--------------------
  //
  // ⚠️ **送るもの（訂正タブの局面と撮った 1 枚）の持ち主はここ。** 送信の行は
  // `current()` で読むだけ（写しを持たせない）。
  // 今の局面（EditState 由来）と、撮ったときの盤面矩形（CaptureResult 由来）。
  // **出所が違うので別々に持つ**（訂正しても矩形は変わらない。画像は同じ 1 枚）。
  let editSfen = "";
  // editNotes は「送る SFEN のために妥協した点」（手番を先手にした・先後未決の
  // 持ち駒を落とした）。**送る前に出す**（何が落ちるか分からないまま送らせない）。
  let editNotes: string[] = [];
  let editLoaded = false;
  // editNearWhite は**撮った画像が後手目線か**（`EditState.nearWhite` の控え）。
  // ⚠️ **解析タブの表示視点（`studyFlipped`）とは別物。** あちらは盤の絵をどちらから
  // 眺めるかで、こちらは**局面そのものの向き**（採るときに 180 度回る）。
  let editNearWhite = false;
  let lastRegion: ShotRegion | null = null;

  const trainUI = mountTrainSend({
    root: root.querySelector<HTMLElement>("#train-row")!,
    current: () => ({
      loaded: editLoaded,
      path: shotFullPath,
      sfen: editSfen,
      notes: editNotes,
      region: lastRegion,
    }),
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
  // 訂正タブ側の一行。訂正の操作が通らなかった理由と、確定できない理由を出す。
  const editStatus = root.querySelector<HTMLParagraphElement>("#edit-status")!;

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
    editStatus.textContent = "";
    editStatus.classList.remove("is-error");
    studyUI.setStatus("");
    try {
      studyUI.showStudy(await StudyService.Adopt());
    } catch (err) {
      editStatus.textContent = String(err instanceof Error ? err.message : err);
      editStatus.classList.add("is-error");
      return;
    }
    // ⚠️ **訂正した 1 枚を、中継を追う盤（マスタ）にする**（2026-10-09。Go 側の `SetFollowShot`）。
    // 押した瞬間の 1 枚をマスタにすると、手が盤に被っていたときに初手から「別の盤」になる。
    // ⚠️ **追い始める前に待つこと**（下の `studyFollowBtn.click()` より先に覚えさせる）。
    // 画像の無い局面・画像ファイルなら Go 側が忘れる（押した瞬間の 1 枚で決める）。
    try {
      await CaptureService.SetFollowShot(refShot?.path ?? "");
    } catch {
      // 覚えられなくても解析は始まる（押した瞬間の 1 枚で決めるだけ。設計原則3）。
    }
    // ⚠️ **見え方を撮った画像に合わせる。** 後手目線で撮った局面は、解析タブへ
    // 渡るときに 180 度回っている（＝先後が実際どおりになっている）ので、
    // **そのまま描くと盤が上下逆に見える**。表示視点を後手にすれば、
    // 局面は正しいまま、見た目は撮った画像と同じ向きになる。
    // **あとから解析タブのボタンで自由に戻せる。**
    studyUI.setViewpoint(!editNearWhite);
    selectTab(studyTab);
    // ⚠️ **追っていたなら上の確認で止まっている**ので、ここでは必ず「入れる」側になる。
    // ⚠️ **ボタンを押す形にしないこと** —— 画面の写し（`followOn`）は止めた知らせが届くまで古いので、押すと止める側に倒れる。
    if (follow) {
      studyUI.startFollow();
    }
  };

  const editor = mountEditor({
    stage: boardStage,
    handSlots: {
      black: root.querySelector<HTMLElement>("#hand-black-slot")!,
      white: root.querySelector<HTMLElement>("#hand-white-slot")!,
      missing: root.querySelector<HTMLElement>("#missing-slot")!,
    },
    resetButton: root.querySelector<HTMLButtonElement>("#edit-reset")!,
    panel: root.querySelector<HTMLElement>("#editor")!,
    viewHost: root.querySelector<HTMLElement>("#edit-view-row")!,
    confirmHost: root.querySelector<HTMLElement>("#edit-confirm-row")!,
    onState: (st) => {
      // suteme に送るのは **LabelSFEN**（画像のラベルとしての SFEN）。
      //
      // ⚠️ **`sfen` を送らないこと。** あちらは局面が確定したときだけ埋まるので、
      // 手番や駒台の先後が未決だと空になり、**盤面部分だけ送って持ち駒が落ちる**
      // （実際にそうなっていた）。`labelSfen` は決まっているぶんの持ち駒を必ず載せ、
      // 妥協した点を `labelNotes` で返す。
      editLoaded = !!st?.loaded;
      editSfen = st?.labelSfen ?? "";
      editNotes = st?.labelNotes ?? [];
      // 撮った盤の目線。**採るときに解析タブの見え方を決めるのに使う**
      // （局面そのものを回すのは Go 側の `adoptPosition`）。
      editNearWhite = !!st?.nearWhite;
      trainUI.sync();
      // ⚠️ **ここから解析の状態を触らないこと。** 訂正タブの局面と解析タブの局面は
      // 別物で、繋ぐのは「この局面を解析する」を押したときの 1 回だけ。
      if (!st?.loaded) {
        showSfenNote("");
        fillWarnings(boardWarnings, []);
        return;
      }
      showBoard(st.boardSfen, "shot");
      sfenOut.textContent = st.sfen || st.boardSfen || "-";
      // 後手目線のときだけ「解析へは反転して送信」と添える（先手目線なら同じ文字列）。
      // ⚠️ **行を増やさない** —— 増やすと出たり消えたりするたびに下のボタンがずれて、
      // 押しづらくなる（実際にそうなっていた）。全文は title で読める。
      showSfenNote(st.analyzeSfen && st.analyzeSfen !== st.sfen ? st.analyzeSfen : "");
      fillWarnings(boardWarnings, st.warnings ?? []);
    },
    onConfirm: (follow) => {
      void adoptToStudy(follow);
    },
    onError: (message) => {
      // **訂正タブの中に出す。** 撮影の結果（入力タブの #status）とは別の話で、
      // タブを跨いだ先に理由が出ても読めない。
      editStatus.textContent = `訂正できませんでした: ${message}`;
      editStatus.classList.add("is-error");
    },
  });

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

  // SFEN の後ろの注記。**後手目線のときだけ出す**（引数は解析へ渡す SFEN。空なら消す）。
  //
  // ⚠️ **行を増やさないこと**（2026-08-18 に「解析へ」の行から変えた）。後手目線の
  // ときだけ行が増えると、**出たり消えたりするたびに下のボタンが上下にずれて押しづらい**。
  // ⚠️ **黙って回さない、という約束は残す** —— 画面の局面とエンジンが読む局面が
  // 上下逆になるので、**何が渡るのかは見えていること**（全文は title）。
  const showSfenNote = (analyzeSfen: string) => {
    sfenNote.hidden = analyzeSfen === "";
    sfenNote.title = analyzeSfen ? `解析へ渡す SFEN: ${analyzeSfen}` : "";
  };


  // sfen が空なら盤を隠して理由を出す。撮る前と「撮ったが認識できなかった」は別物なので
  // 文言を分ける(認識失敗はキャプチャの失敗ではない。設計原則3)。
  // 盤が無いときに何と書くかは 3 通りある。⚠️ **「まだ撮っていません」と
  // 「認識しています」を混ぜないこと** —— 撮った直後の数秒は盤が無いのが正常で、
  // そこに「まだ撮っていません」と出ると**撮れていないように見える**
  // （撮り直しを誘発する。今どの 1 枚の話なのかが読めなくなる）。
  const showBoard = (sfen: string, phase: "none" | "shot" | "busy") => {
    // 盤ごと箱(.board-stage)を隠す。箱は正方形の場所取りをしているので、
    // 中身が無いまま残すと空白が居座る。
    boardStage.hidden = !sfen;
    if (!sfen) {
      board.hidden = true;
      placeholder.hidden = false;
      placeholder.textContent =
        phase === "busy"
          ? "盤面を認識しています…"
          : phase === "shot"
            ? "盤面を認識できませんでした。画像は保存されています。"
            : "まだ撮っていません。";
      return;
    }
    board.setAttribute("sfen", sfen);
    board.hidden = false;
    placeholder.hidden = true;
  };

  // 盤面検出の信頼度。低いときは「盤が映っていない画面を撮った」可能性のほうが高いので、
  // 認識結果を疑う入口として出しておく(警告と違い、局面の中身の話ではない)。
  const showConfidence = (confidence: number, sfen: string) => {
    if (!sfen) {
      confidenceRow.hidden = true;
      return;
    }
    confidenceOut.textContent = `${Math.round(confidence * 100)}%`;
    confidenceOut.classList.toggle("is-low", confidence < LOW_CONFIDENCE);
    confidenceRow.hidden = false;
  };

  // 「認識詳細情報」側。**認識した時点の駒台の推定枚数**(先後不明)。
  const showHand = (hand: Record<string, number>) => {
    const parts = HAND_ORDER.filter((k) => (hand?.[k] ?? 0) > 0).map(
      (k) => `${HAND_LABEL[k]}${hand[k]}`,
    );
    handOut.textContent = parts.join(" ");
    handRow.hidden = parts.length === 0;
  };

  const fillWarnings = (ul: HTMLUListElement, list: string[]) => {
    const items = list ?? [];
    ul.replaceChildren();
    for (const w of items) {
      const li = document.createElement("li");
      li.textContent = w;
      ul.appendChild(li);
    }
    ul.hidden = items.length === 0;
  };

  // 「認識詳細情報」側の警告。**認識した時点のもの**で、訂正しても書き換えない。
  const showWarnings = (list: string[]) => fillWarnings(warnings, list);

  // 設定で指定した学習データの置き場所(config.Config の SutemeDataDir)。
  // 実際に読まれたファイル(debug.predictor.source)と突き合わせるために覚えておく。
  let configuredDir = "";
  const normalizePath = (s: string) => s.toLowerCase().replace(/\\/g, "/");

  // 盤面と判定した矩形。**認識が外れたときに、座標の問題か駒種の問題かを切り分ける材料。**
  // Confidence だけでは「怪しい」までしか言えず、どちらが原因かは分からない。
  const showRegion = (debug: Debug | null | undefined) => {
    if (!debug) {
      regionRow.hidden = true;
      return;
    }
    const r = debug.region;
    const w = r.Max.X - r.Min.X;
    const h = r.Max.Y - r.Min.Y;
    const src = REGION_SOURCE_LABEL[debug.region_source] ?? debug.region_source;
    regionOut.textContent =
      `(${r.Min.X},${r.Min.Y})-(${r.Max.X},${r.Max.Y}) ${w}x${h}` +
      ` / 1マス ${Math.round(w / 9)}x${Math.round(h / 9)} / ${src}`;
    regionRow.hidden = false;
  };

  // 実際に使われた駒種推論器。
  //
  // **「認識器」の行(設定した置き場所)とは別物。** あちらは config に書いたディレクトリで、
  // suteme が本当にどのファイルを読んだかまでは言えない。学習データは育て続けるものなので、
  // 「古いデータで認識していた」に後から気づく事故を防ぐにはこちらが要る。
  const showPredictor = (debug: Debug | null | undefined) => {
    if (!debug) {
      predictorRow.hidden = true;
      return;
    }
    const p = debug.predictor;
    let text = p.kind + (p.detail ? `(${p.detail})` : "");
    if (p.source) {
      text += ` ← ${p.source}`;
    }
    // 設定した場所と違うところから読んでいたら、そこだけ色を変えて知らせる。
    // (SetPredictor が効いておらず suteme 既定の探索に落ちている、など)
    const mismatched =
      configuredDir !== "" &&
      p.source !== undefined &&
      p.source !== "" &&
      !normalizePath(p.source).startsWith(normalizePath(configuredDir));
    if (mismatched) {
      text += " ※設定と別の場所";
    }
    predictorOut.textContent = text;
    predictorOut.classList.toggle("is-low", mismatched);
    predictorRow.hidden = false;
  };

  // 撮った画像に、盤面と判定した矩形とマス割りを重ねる。
  //
  // **画像は Go 側で描かずに座標だけを受け取ってここで重ねる。** サムネイルは既に
  // 等倍 PNG の base64 なので、描き込んだ 2 枚目を送るとペイロードが倍になる。
  // 重ねるだけならマスごとの確信度をホバーで出せるし、オン/オフも切り替えられる。
  //
  // viewBox を入力画像の座標系そのものにしてあるので、矩形は suteme が返した値を
  // そのまま置ける(表示サイズへの換算は preserveAspectRatio="none" が引き受ける。
  // .thumbnail は max-width/max-height だけの指定なので、要素の箱＝画像の描画領域)。
  const drawOverlay = (debug: Debug | null | undefined) => {
    // 中身を捨てれば何も描かれないので、表示/非表示の切り替えは要らない
    // (SVG 要素には hidden 属性の型が無い)。
    overlay.replaceChildren();
    if (!debug) {
      return;
    }
    const b = debug.image_bounds;
    overlay.setAttribute(
      "viewBox",
      `${b.Min.X} ${b.Min.Y} ${b.Max.X - b.Min.X} ${b.Max.Y - b.Min.Y}`,
    );
    paintRecognition(overlay, debug);
  };

  // 撮った画像のファイル名。押すと**フルパス**をクリップボードへ入れる。
  //
  // 撮った PNG は suteme の学習データにも、他のツールで開く対象にもなるので、パスを
  // 手で写す場面がそれなりにある。表示はファイル名だけにして(保存先は毎回同じで、
  // 見て区別が付くのは末尾の時刻の部分だけ)、コピーするのは開くのに使えるフルパス。
  //
  // コピーは 2 通りある。**用途が違うので両方残す。**
  //
  //   - パス … 他のツールで開く・suteme の学習データに混ぜる、といったファイル操作向け。
  //     Wails ランタイムの `Clipboard.SetText` で完結する
  //     (`navigator.clipboard` は secure context 前提で、カスタムスキームで配信している
  //     この webview では使えるとは限らない)
  //   - 画像そのもの … チャットや棋譜ソフトへ直接貼る用。**ランタイムに口が無い**ので
  //     Go 側(`CaptureService.CopyImage`)で Win32 のクリップボードへ CF_DIB を載せる。
  //     渡すのはパスで、Go 側が保存済みの PNG を読み直す(clipboard_windows.go)
  let shotFullPath = "";
  let copiedTimer = 0;
  const showPath = (path: string) => {
    shotFullPath = path;
    if (!path) {
      shotPath.hidden = true;
      shotCopyImage.hidden = true;
      return;
    }
    // Windows の `\` 区切りだが、将来 `/` になっても困らないよう両方で切る。
    shotPathName.textContent = path.split(/[\\/]/).pop() ?? path;
    shotPath.title = `クリックでパスをコピー: ${path}`;
    shotPath.classList.remove("is-copied");
    shotCopyImage.classList.remove("is-copied");
    shotPath.hidden = false;
    shotCopyImage.hidden = false;
  };

  // 押しても何も起きないように見えるのを避ける。表示は変えず、色だけ変えて戻す
  // (ファイル名が「コピーしました」に化けると、何を撮ったのか分からなくなる)。
  const flashCopied = (btn: HTMLButtonElement) => {
    btn.classList.add("is-copied");
    window.clearTimeout(copiedTimer);
    copiedTimer = window.setTimeout(() => {
      shotPath.classList.remove("is-copied");
      shotCopyImage.classList.remove("is-copied");
    }, 1200);
  };

  shotPath.addEventListener("click", () => {
    void (async () => {
      try {
        await Clipboard.SetText(shotFullPath);
      } catch (err) {
        shotPath.title = `コピーできません: ${String(err)}`;
        return;
      }
      flashCopied(shotPath);
    })();
  });

  shotCopyImage.addEventListener("click", () => {
    void (async () => {
      // 数 MB の PNG を読み直して DIB に変換するぶん一瞬かかる。連打を止めておく。
      shotCopyImage.disabled = true;
      try {
        await CaptureService.CopyImage(shotFullPath);
        shotCopyImage.title = "画像そのものをクリップボードにコピー";
        flashCopied(shotCopyImage);
      } catch (err) {
        // 失敗しても画像は保存されたまま(設計原則3: 段階的に劣化する)。
        // ここで status を潰すと保存先が読めなくなるので、ボタンの title にだけ出す。
        shotCopyImage.title = `画像をコピーできません: ${String(err)}`;
      } finally {
        shotCopyImage.disabled = false;
      }
    })();
  });

  // 出どころの言い回し。**ここでしか分けない**（2026-09-12。画像ファイルの読み込み）。
  // ⚠️ **「撮りました」と出さないこと** —— 押していない操作を報告されると、
  // **どの 1 枚の話なのかが分からなくなる**（撮ったものと読み込んだものが混ざる）。
  const tookVerb = (source?: string) =>
    source === "file" ? "読み込みました" : "撮りました";

  // 撮れた時点で呼ぶ（`capture:shot`）。**認識はまだ走っている。**
  //
  // ⚠️ **撮ることと認識することを 1 つの表示に混ぜないこと**（2026-08-18 に分けた）。
  // 認識は数秒かかるので、`capture:done` まで何も変えないでいると
  // **前の 1 枚が盤に出たまま**になり、枠には「撮影中…」が出続ける ——
  // **今どの画像の話なのかが画面から読めない**（撮れているのかどうかも分からない）。
  //
  // ここでやるのは「新しい 1 枚に入れ替えて、認識の結果だけを待つ状態にする」こと:
  // 撮った画像を出し、前の認識結果と前の解析を捨て、訂正タブを開いて「認識中」と出す。
  // **結果が来たら showResult がそのまま上書きする。**
  const showShot = (taken: CaptureShot) => {
    // ⚠️ **ここにフルパスを出さない**（2026-08-18 に外した）。パスは長くて 1 行を
    // 埋めるうえ、**この行はもう見えていない**（撮ると訂正タブへ移る）。
    // 撮った画像の置き場所は**訂正タブの「認識詳細情報」**が持っている
    // （ファイル名 + 押すとフルパスをコピー）。**2 か所に出さないこと。**
    status.textContent = `${tookVerb(taken.source)}（${taken.width}x${taken.height}）。盤面を認識しています…`;
    status.classList.remove("is-error", "is-warn");
    showPath(taken.path);

    // ⚠️ **撮っても解析タブは消さない**（2026-08-22 に `StudyService.Clear` を
    // やめた）。**撮ることと、解析している局面を捨てることは別の操作** ——
    // 学習データを集めるために撮る（suteme へ送る）使い方では、**解析している
    // 局面はそのまま**でないと、撮るたびに検討が消える。棋譜を読んで解析して
    // いる最中に 1 枚撮ったときも同じ。
    //
    // ⚠️ **捨てるのは「この局面を解析する」を押したとき**（`StudyService.Adopt`
    // が根ごと入れ替える）。**そこが訂正タブと解析タブの唯一の継ぎ目**なので、
    // 撮った時点で先回りして消す必要が無い（消していたのは、撮る＝新しい局面を
    // 採るための操作、という前提を置いていたから）。
    //
    // **解析そのものは止める。** 認識に数秒かかるうえ、このあと訂正タブへ移るので
    // `selectTab` がどのみちエンジンを手放す（`AnalyzeService.Release`）。
    // **そこまでの評価値は残る**（設計原則3。タブを移ったときと同じ約束）。
    // ⚠️ **走っているかどうかは側の列が知っている**（2026-09-08）。
    void AnalyzeService.Stop();

    // 前の 1 枚の認識結果を消す。**残すと、新しい画像の隣に古い駒台と古い警告が並ぶ。**
    editor.clear();
    showBoard("", "busy");
    sfenOut.textContent = "-";
    showConfidence(0, "");
    showRegion(null);
    showPredictor(null);
    showHand({});
    showWarnings([]);
    drawOverlay(null);
    markDebug("");
    trainUI.clearStatus();

    // 撮った画像。**まだ盤面領域が分からないので切り取らずに全体を出す**
    // （領域は認識が出すもの。届いたら showResult が同じ画像を切り取って出し直す）。
    if (taken.thumbnail) {
      thumbnail.src = taken.thumbnail;
      shot.hidden = false;
    }
    lastRegion = null;
    // **まだ盤面領域が分からないので矩形は付かない**（認識はこれから）。
    // 届いたら showResult が同じ 1 枚を矩形つきで出し直す。
    setCaptureRef({
      thumbnail: taken.thumbnail,
      path: taken.path,
      width: taken.width,
      height: taken.height,
      debug: null,
    });
    trainUI.sync();

    // **撮ったら訂正タブへ移る。** 行き先は認識の成否に依らないので、
    // 待たせるならその面で待たせる（結果が出てから移ると、待っているあいだ
    // どこを見ていればよいのか分からない）。盤が取れなかったときだけ、
    // showResult が理由の出ている入力タブへ戻す。
    selectTab(editTab);
  };

  const showResult = (result: CaptureResult) => {
    // 認識できなくてもキャプチャは成功している(設計原則3: 段階的に劣化する)。
    // 保存できたことと、認識できたかどうかを分けて出す。
    // ⚠️ **フルパスは出さない**（showShot の ⚠️ と同じ。置き場所は「認識詳細情報」）。
    if (result.recognizeError) {
      status.textContent = `${tookVerb(result.source)}（${result.width}x${result.height}）。盤面は認識できませんでした: ${result.recognizeError}`;
      status.classList.add("is-warn");
      status.classList.remove("is-error");
    } else {
      status.textContent = `${tookVerb(result.source)}（${result.width}x${result.height}）。盤面を認識しました。`;
      status.classList.remove("is-error", "is-warn");
    }

    showPath(result.path);
    // ⚠️ **ここで解析タブを触らないこと。** 撮っても解析している局面は消さない
    // （2026-08-22。理由は showShot の ⚠️）。入れ替わるのは「この局面を解析する」
    // を押したときだけ。
    // 盤・SFEN・駒台・警告は訂正 UI 側(EditState)が描く。**認識結果をここで直接
    // 描かない**(訂正した内容が撮り直すまで残る、という食い違いを作らないため)。
    // 認識できていれば読み込んで訂正を始められる状態にし、駄目なら空に戻す。
    //
    // **撮ったら訂正タブへ移る。** 認識結果はまず直すものなので、そこが行き先。
    // 盤が取れなかったときは直すものが無いので、理由の出ている入力タブに留まる。
    if (result.sfen) {
      void editor
        .load(result.sfen)
        .then(() => selectTab(editTab));
    } else {
      editor.clear();
      showBoard("", "shot");
      sfenOut.textContent = "-";
      selectTab(inputTab);
    }
    showConfidence(result.confidence, result.sfen);
    showRegion(result.debug);
    showPredictor(result.debug);
    showHand(result.handTotal);
    showWarnings(result.warnings);

    if (result.thumbnail) {
      thumbnail.src = result.thumbnail;
      shot.hidden = false;
    }
    // 盤面矩形。**保存した PNG の座標系に直して覚える**
    // （認識はメモリ上の画像の座標系で答えるが、PNG は原点 (0,0) に正規化される。
    //  今は常に一致するはずだが、ずれると学習サンプルの切り出しが黙って 1 マスずれる）。
    // suteme へ送るときと、下の参照画像の切り取りに使う。**同じ値から両方を出す。**
    const dbg = result.debug;
    lastRegion = dbg
      ? {
          x1: dbg.region.Min.X - dbg.image_bounds.Min.X,
          y1: dbg.region.Min.Y - dbg.image_bounds.Min.Y,
          x2: dbg.region.Max.X - dbg.image_bounds.Min.X,
          y2: dbg.region.Max.Y - dbg.image_bounds.Min.Y,
        }
      : null;
    // 訂正中に盤の左へ出す参照画像。**もとの 1 枚は「認識詳細情報」と同じ**で、
    // ここではそれに矩形とマス割りを重ねて出すだけ（別の経路で取り直さない）。
    // ⚠️ **画像と矩形を 1 回で渡すこと** —— 別々に入れると、片方だけ入れ替わった
    // 「新しい画像に前の格子」という状態を作れてしまう。
    setCaptureRef({
      thumbnail: result.thumbnail,
      path: result.path,
      width: result.width,
      height: result.height,
      debug: result.debug,
    });
    trainUI.sync();
    drawOverlay(result.debug);

    // 信頼度が低いのも「見に行くべきもの」に含める。盤が映っていない画面を撮ったときは
    // 警告が 1 件も出ないことがあり、それだと盤面タブ側では何も起きていないように見える。
    const lowConfidence = !!result.sfen && result.confidence < LOW_CONFIDENCE;
    markDebug(
      result.recognizeError
        ? "error"
        : (result.warnings?.length ?? 0) > 0 || lowConfidence
          ? "warn"
          : "",
    );
  };

  const showError = (message: string) => {
    status.textContent = `キャプチャに失敗しました: ${message}`;
    status.classList.add("is-error");
    status.classList.remove("is-warn");
    markDebug("error");
  };

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

  // clearShotContext は**撮った画像に紐づくものを全部片付ける**（2026-09-12）。
  //
  // ⚠️ **画像の無い局面を訂正タブに入れる口では必ず呼ぶこと**（手合割の調整・
  // 詰将棋）。訂正タブは元々「撮った 1 枚を直す面」で、**画像・認識の情報・
  // 学習への送信**がぶら下がっている。残したまま別の局面を入れると、
  // **別の画像のラベルとして訂正結果を送れてしまう**（学習データが壊れる）。
  const clearShotContext = () => {
    showPath("");
    shot.hidden = true;
    lastRegion = null;
    setCaptureRef(null);
    showConfidence(0, "");
    showRegion(null);
    showPredictor(null);
    showHand({});
    showWarnings([]);
    drawOverlay(null);
    markDebug("");
    trainUI.clearStatus();
    // ⚠️ **最後に呼ぶこと** —— `shotFullPath` と盤面領域を見て
    // 「訂正データを送信」の行を畳むので、先に呼ぶと出たままになる。
    trainUI.sync();
  };

  // 学習データを育てながら使うための入口。suteme は一度読んだ推論器をキャッシュするので、
  // データを更新してもこれを押すまで(あるいは再起動するまで)反映されない。
  //
  // **読むものは 2 つ**(駒種推論器と盤の縁の帯の判定器)。帯の判定器が無くても認識は
  // 動くのでエラーにはしないが、**黙って落とすと盤の位置が 1マス滑ったまま
  // 信頼度 100% で返る**ので警告として出す。
  const showRecognizer = (st: RecognizerStatus) => {
    // 設定タブの「今どこから読んでいるか」も同じ結果から書く（更新の経路を 1 本にする）。
    // ⚠️ settingsUI は下で作っている。**呼ばれるのはいつも await のあと**なので届く。
    settingsUI.showSutemeState(st);
    // ⚠️ **ディレクトリから読んだときだけ突き合わせの相手にする。**
    // 焼き込み（mode === "embed"）の source は出所のラベルであってパスではないので、
    // これを入れると `showPredictor` が毎回「※設定と別の場所」を出す。
    configuredDir = st.mode === "dir" ? (st.source ?? "") : "";
    if (st.error) {
      recognizer.textContent = `認識器を読み込めません: ${st.error}`;
      recognizer.className = "recognizer is-error";
      markDebug("error");
      return;
    }
    if (st.ready) {
      recognizer.textContent = `認識器: ${st.source}`;
    } else {
      recognizer.textContent = "認識器: ありません";
    }
    recognizer.className = "recognizer";
    if (st.stripError) {
      // 盤の外枠線が画像の外に出ているキャプチャで効く判定器。これが無いと、
      // 1マス滑った枠が信頼度 100% のまま通る(見分ける手立てが他に無い)。
      recognizer.textContent +=
        "（⚠ 盤の縁の判定データ strip_data_v1.bin が読めません: 盤の位置が 1マス滑ることがあります）";
      recognizer.className = "recognizer is-warn";
    } else if (st.stripSamples) {
      recognizer.textContent += `（盤の縁 ${st.stripSamples} 本）`;
    }
  };

  const reload = async () => {
    reloadBtn.disabled = true;
    try {
      showRecognizer(await CaptureService.ReloadRecognizer());
    } catch (err) {
      recognizer.textContent = `認識器の再読み込みに失敗しました: ${String(err)}`;
      recognizer.className = "recognizer is-error";
      markDebug("error");
    } finally {
      reloadBtn.disabled = false;
    }
  };

  reloadBtn.addEventListener("click", () => {
    void reload();
  });

  // 重ね表示は切れるようにしておく。撮った画像そのものを見たい場面(盤が映っていない
  // 画面を撮ったかどうかの確認)では、線が邪魔になる。
  overlayToggle.addEventListener("change", () => {
    shot.classList.toggle("no-overlay", !overlayToggle.checked);
  });

  // ---- 設定タブ（settingstab.ts）----
  //
  // ⚠️ **設定が効く先へ配るのはここ 1 本**（設定タブは `onSettings` で呼ぶだけ）。
  // 側の列で書き換えたときも、切り離した窓から `settings:changed` が来たときも、
  // 設定タブの `showSettings` を通ってここに来る。
  const applySettings = (s: AppSettings) => {
    // 解析タブに効くもの（切り離し・縦軸・盤の上の 1 行・勝率バー・折れ線の色・側の列）。
    studyUI.applySettings(s);
    // 送信ボタンを出すかどうかはこの設定で決まる。
    trainUI.setEnabled(s.training.enabled);
    trainUI.sync();
  };
  const settingsUI = mountSettingsTab(tabs[4].panel, {
    pieceBoards: [board, studyUI.board],
    onSettings: applySettings,
    onRecognizer: showRecognizer,
    // 棚を開き直したら一覧を取り直す。
    onKifuDBChanged: () => libraryUI.refresh(),
  });

  // 起動時は読み込み直さず、Go 側が起動時に読んだ結果をそのまま出す
  // (表示のためだけに 3.5MB を読み直さない)。
  void (async () => {
    try {
      showRecognizer(await CaptureService.Recognizer());
    } catch {
      /* 状態が取れないだけなので黙って諦める。実害は最初のキャプチャで分かる。 */
    }
  })();

  // ホットキー(Go側の GlobalShortcut)や枠のツールバーからのキャプチャは、この画面が
  // フォーカスされていなくても発生する。結果は Wails イベントで受け取って UI に反映する。
  // ⚠️ **キャプチャのイベントは 2 段**（2026-08-18 に分けた）。撮れた時点で
  // `capture:shot`、認識まで終わってから `capture:done`。**片方だけを見ないこと** ——
  // shot だけでは結果が出ず、done だけでは認識のあいだ前の 1 枚が残る。
  Events.On("capture:shot", (event: { data: CaptureShot }) => {
    showShot(event.data);
  });
  Events.On("capture:done", (event: { data: CaptureResult }) => {
    showResult(event.data);
  });
  Events.On("capture:failed", (event: { data: string }) => {
    showError(event.data);
  });
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
