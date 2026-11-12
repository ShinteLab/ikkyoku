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
//            (画像は認識を通るので訂正タブへ、SFEN/KIF は確定済みなので解析タブへ)
//   訂正   … 認識の誤りを**直す**面。**ここに居ること自体が訂正モード**
//            (自由編集・合法性を問わない・手番も駒台の先後も未決でよい)
//   解析   … **確定した局面**の面。評価値を出し、今後ここに手順と分岐ツリーが乗る
//            (合法手だけを辿る)
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
  FiExternalLink,
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
  SettingsService,
  StudyService,
} from "../bindings/github.com/ShinteLab/ikkyoku/app";
import { iconMarkup } from "./icon";
import { mountEditor } from "./editor";
import { mountLibrary } from "./library";
import { mountInputTab } from "./inputtab";
import { mountIssues } from "./issues";
import { mountEvalPane } from "./evalgraphpane";
import { enableVeilDrag } from "./veildrag";
import type { EvalMode } from "./evalgraph";
import { mountSidePane, type EngineScore } from "./sidepane";
import { openPopup } from "./popup";
import { hideHint } from "./hint";
import { mountStudyBoard } from "./study";
import { mountSettingsTab } from "./settingstab";
import { mountTrainSend, type ShotRegion } from "./trainsend";
import { mountStudyLayout } from "./studylayout";
import { mountFollowControl } from "./followcontrol";
import type { RecognizerStatus } from "../bindings/ikkyoku/models";
import type {
  AppSettings,
  StudyState,
} from "../bindings/github.com/ShinteLab/ikkyoku/app/models";
import type { Stock } from "../bindings/github.com/ShinteLab/ikkyoku/position/models";
// 認識の観測情報。**型を手で書き写さない**(Go 側は suteme の型をそのまま通しており、
// ここで別に定義すると矩形の意味がずれても気づけない)。
import type { Debug } from "../bindings/github.com/ShinteLab/suteme";
import {
  confirmStopFollow, isFollowing, onFollowingChange,
} from "./followguard";

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
        <div class="board-area">
          <!-- 盤と駒台の配置。**後手の駒台は盤の左上、先手の駒台は右下**
               (訂正タブと同じ並び＝実際の将棋盤と同じ)。
               ⚠️ **こちらは読み取り専用**（駒を掴めない）。訂正タブの
               「足りない駒」はここには無い —— 未決が残った局面はそもそも
               このタブに来ない（StudyService.Adopt が断る）。 -->
          <div id="study-board-with-hands" class="board-with-hands">
            <!-- 勝率バー（2026-08-12）。**盤の真上・盤と同じ幅。**
                 評価値を読むより先に「どちらがどれくらい良いか」が目に入るほうが速い
                 （数字は下の候補手の行にある）。**左が後手（青）・右が先手（赤）**で、
                 盤の向き（後手が上）とは別に、横棒として一般的な並びに合わせてある。

                 ⚠️ **盤と同じグリッドの列に入れてあるのが要点**（board-area の
                 直下に置くと、右の手順リストのぶん盤が左に寄っているので**盤と
                 左右がずれる**）。列の幅＝盤の幅なので、width: 100% で揃う。

                 勝率 = 1 / (1 + exp(-評価値 / ポナンザ定数))。定数は設定タブ（既定 1500）。
                 ⚠️ **計算はフロントでしない**（Go 側の analyze.WinRate。式を 2 か所に
                 持つと、定数を変えたときに片方だけ古い値で描く）。

                 ⚠️ **出すのは 1 つのエンジンだけ**（押すと切り替わる）。
                 **合成ではない** —— 複数走っていても、今見ているのはどれか 1 つ。
                 ⚠️ **数字とエンジン名は出さない**（盤の上に文字を積むと、そのぶん
                 盤が小さくなる）。値はカーソルを当てたときに出す。 -->
            <!-- 対局者（2026-08-13）。**グリッドの左右の列**（駒台の上の空きセル）に
                 出す。勝率バーは盤と同じ幅のまま **1 行目の真ん中の列**なので、
                 その左右がちょうど空いている。

                 ⚠️ **左が後手・右が先手**（バーの左右と同じ並び）。
                 ⚠️ **視点を反転しても入れ替えないこと** —— 勝率バーそのものが
                 反転しないので、名前だけ動くと帯との対応が壊れる。
                 **入れ替わるのは帯の左右を反転したときだけ**（2026-10-02。黒地の
                 右クリック。applyWinRateFlip が帯と名前を一緒に動かす）。

                 ⚠️ **名前が無いときの「後手」「先手」は表示側の既定。**
                 Go 側は空を返す（名前が分かっているのか、既定を出しているだけ
                 なのかを区別できるようにするため）。 -->
            <span id="player-name-white" class="player-name is-white" hidden></span>
            <div id="winrate-row" class="winrate-row" hidden>
              <button id="winrate-bar" class="winrate-bar is-empty" type="button">
                <span class="winrate-track">
                  <span id="winrate-white" class="winrate-white"></span>
                  <!-- 真ん中（互角）の目印。**これが無いと、どちらへ傾いているのかが
                       バーの端との比較でしか読めない。** -->
                  <span class="winrate-mid" aria-hidden="true"></span>
                </span>
              </button>
            </div>
            <span id="player-name-black" class="player-name is-black" hidden></span>
            <!-- 視点（2026-08-13 に右の列から移した）。**表示だけの反転で、
                 局面には効かない。**

                 ⚠️ **置き場所はグリッドの右の列の真ん中のセル**（先手の対局者名の下・
                 先手の駒台の上）。ここは元から空いているので、**盤の上下にも
                 右の列にも行が増えない**（＝盤が小さくならない）。

                 ⚠️ **視点を反転しても列を移さないこと。** 駒台は左右が入れ替わるが、
                 **押すたびにボタンが飛ぶと、もう一度押すのに探すことになる**。
                 どちら側が手前かはボタンの文字が言っている。

                 中継の札（2026-10-08）。**視点のボタンの上に積む**（同じ空きのセル）。
                 画面を撮って追っているあいだは赤、URL の棋譜を自動更新しているあいだは緑
                 （ユーザー指定）。**両方入っていれば 2 枚とも出す**（別物なので 1 枚にまとめない）。
                 ⚠️ **押せない**（入り切りは「中継を追う」と自動更新のアイコンが持つ）。 -->
            <div class="view-side">
              <span id="study-live-follow" class="live-badge is-follow" hidden
                    title="中継の画面を撮って、進んだ手を本譜に足しています">中継を追跡中</span>
              <span id="study-live-kifu" class="live-badge is-kifu" hidden
                    title="棋譜の URL を取り直して、新しい手に付いていきます">棋譜を自動更新中</span>
              <button id="study-flip" class="ghost-btn view-flip" type="button" hidden></button>
            </div>
            <div id="study-hand-white-slot" class="hand-slot" hidden></div>
            <div id="study-stage" class="board-stage">
              <!-- ⚠️ fluid: 置き場所の幅いっぱいに広げる（core/web の属性。2026-08-12）。
                   これが無いと**盤は固有サイズの 560px 止まり**で、盤を主役に
                   大きく見せられない。訂正タブの盤には付けていない（あちらは
                   撮った画像を並べる面なので、盤だけ大きくしても仕方がない）。 -->
              <shogi-board id="study-board" fluid hidden></shogi-board>
            </div>
            <div id="study-hand-black-slot" class="hand-slot" hidden></div>
            <!-- 今見ている局面の SFEN（2026-08-14 に手順の列から**盤の下**へ戻した）。
                 ⚠️ **盤の下に行を積んでも盤は小さくしない** —— 評価値グラフの箱を
                 38px 詰めたぶんの空きに収める（.board-area は align-items: center
                 なので、そこは元から遊んでいる）。**そのため 1 行に収めること**
                 （4 行目 + gap 6px で 23px ほど。⚠️ **折り返させると盤が押し出される**）。

                 ⚠️ **盤と同じグリッドの列に入れる**（勝率バーと同じ理由）。
                 .board-area の直下に置くと、**右の列のぶん盤が左に寄っている**ので
                 盤と左右がずれる。

                 溢れたぶんは省略して title に出す（**選んでコピーはできる**）。 -->
            <div id="study-sfen-row" class="study-sfen-row" hidden>
              <!-- 中継を追う（2026-09-15）。**押しているあいだ撮り続けて、繋がるなら
                   人に聞かずに手を足す。**

                   ⚠️ **訂正タブへは飛ばさない。** 「訂正を挟むかどうか」を
                   こちらが決めることになる（それは人が決めること）。
                   **おかしくてもとにかく進み、違うと思ったら人が戻る。**

                   ⚠️ **置き場所が盤の下なのは、行を増やさないため**（SFEN の行に
                   相乗り）。盤の大きさは式で決まるので、行を足すと盤が縮む。 -->
              <button id="study-follow" class="ghost-btn is-tiny" type="button" hidden
                      aria-pressed="false"
                      title="中継の画面を撮り続けて、進んだ手を本譜に足します（枠を出しておいてください）">中継を追う</button>
              <span class="field-label">SFEN</span>
              <code id="study-sfen" class="sfen" title="今見ている局面">-</code>
            </div>
          </div>
          <!-- 縦のスプリットバー（2026-08-13）。**盤と、右の解析の列の境目。**
               ドラッグで盤の大きさを変える。⚠️ **変えているのは
               --study-side-min ただ 1 つ**で、盤の大きさはその式から付いてくる。

               ⚠️ **掴んだ時点で列の幅が固定される**（2026-09-12。--study-side-max）。
               そこから先は右へ引けば列が下限（300px）まで狭まり、**盤が伸びない
               ぶんは余白になって盤が中央へ寄る**（列は右端のまま）。
               判定は**実際の盤の幅を測って**行う（式の定数を JS に写さない）。 -->
          <div id="study-split" class="split-bar is-vertical" role="separator"
               aria-orientation="vertical" aria-label="盤と解析の列の幅" tabindex="0"
               title="ドラッグで盤と解析の列の幅を変えます。左右キーでも動きます">
            <!-- 解析の列の折り畳み（2026-08-13）。⚠️ **バーの上に置くのが要点** ——
                 畳むと列ごと消えるので、列の中に置いたら戻す手段が無くなる
                 （評価値グラフで見出しの行を残しているのと同じ話）。
                 ⚠️ **押してもドラッグが始まらないようにすること**
                 （pointerdown を止める）。 -->
            <button id="study-split-toggle" class="split-toggle" type="button"
                    aria-expanded="true"></button>
          </div>
          <!-- 盤の右の列（2026-08-12 に作り替えた）。**解析のものは全部ここに入る**
               —— 解析の行・エンジンごとの結果・手順。⚠️ **SFEN は盤の下**
               （2026-08-14 に戻した）、**評価値グラフは盤の下・横いっぱい**。

               ⚠️ **盤の下に行を積まないこと。** 盤の大きさは
               「ウィンドウの高さ − 上下に積んだ行」で決まるので、**下に積むほど
               盤が小さくなる**（実際、エンジンの結果と評価値グラフを下に置いていた
               ときは 560px の盤すら出せていなかった）。横に置けば、盤の高さを
               削るのはウィンドウの高さだけになる。

               手順は「駒をクリック → 動かせる位置が光る → そこをクリックで指す」の
               結果が縦に積まれる列（棋譜ソフトと同じ並び）。⚠️ **チップを押すと
               戻れるが、手順は消えない**（進め直せる）。消えるのは戻った先で別の手を
               指したとき。「1手戻す」は指し間違えの取り消しなので**手順からも消す**。 -->
          <!-- ⚠️ 中身は sidepane.ts が組む（2026-09-08）。別ウィンドウへ
               切り離せるようにするため、器だけをここに置いて中身は向こうが持つ
               （切り離した窓には同じ中身が入る。片方だけ直せないように）。
               ⚠️ この中はテンプレートリテラルなので、コメントにも
               バッククォートを書かないこと（文字列がそこで終わる）。 -->
          <div id="study-side" class="study-side" hidden></div>
          <p id="study-placeholder" class="board-placeholder">
            訂正タブで「この局面を解析する」を押すと、ここに局面が出ます。
          </p>
        </div>
        <!-- 評価値グラフ（2026-08-12）。**手順の 1 手ごとの最善手の評価値**を
             折れ線にする。**「次善手を選んだらどう転ぶか」を辿った結果が
             どう転んだか**を見せる面で、この画面の目的そのもの。

             ⚠️ **エンジンごとに別の折れ線**（合成しない。平均も多数決も取らない）。
             ⚠️ **高さは CSS で固定すること**（--eval-graph-h）。連続モードでは
             1 手ごとに点が増えるので、中身で伸び縮みすると盤ごと画面が跳ねる。 -->
        <div id="eval-graph-row" class="eval-graph-row" hidden>
          <!-- スプリットバー（2026-08-13）。**盤とグラフの境目。**
               ドラッグでグラフの高さを変える。⚠️ **変えているのは
               --eval-graph-h ただ 1 つ**で、盤の大きさはその式から自動で
               付いてくる（--board-size がこれを引いている）。
               ⚠️ **一番下まで下げると畳む**（＝高さ 0）ので、
               「畳む」と「高さを変える」は**同じ 1 つの値**。 -->
          <div id="eval-graph-split" class="split-bar" role="separator"
               aria-orientation="horizontal" aria-label="評価値グラフの高さ" tabindex="0"
               title="ドラッグで高さを変えます（一番下まで下げると畳みます）。上下キーでも動きます">
            <!-- 折り畳み。**畳むと高さ 0** になり、そのぶん盤が大きくなる。
                 ⚠️ **縦のバーと同じくバーの上に載せる**（2026-08-13 に見出しの行から
                 移した）。畳んでも残るのはバーと見出しの行の両方だが、**戻す入口は
                 バーの上に統一する** —— 2 本のバーで操作の形が違うと、どちらが
                 どうだったかを覚えることになる。
                 ⚠️ **押してもドラッグが始まらないようにすること**（pointerdown を止める）。 -->
            <button id="eval-graph-toggle" class="split-toggle" type="button"
                    aria-expanded="true"></button>
          </div>
          <div class="eval-graph-head">
            <!-- 縦軸（2026-09-13）。**評価値と勝率は同じ点の別の読み方。**
                 評価値は「どれくらい差が付いたか」、勝率は「その差がどれくらい
                 勝ちに効くか」で、**同じ +500 でも序盤と終盤では意味が違う。**

                 ⚠️ **見出しの札の代わりに置いてある** —— どれで見ているかが
                 グラフの題そのもの。⚠️ **今どれかが読めること**（aria-pressed）:
                 軸が変わると折れ線の形も変わるので、分からないと数字を読み違える。
                 ⚠️ **記録は 1 つも変わらない**（点が評価値も勝率も持っている）。

                 ⚠️ **「圧縮」は「評価値」の代わりではない。** 等間隔の目盛りには
                 「傾きがそのまま点差の動き」という読み方があるので、**素の
                 評価値グラフは残すこと。** ⚠️ **「勝率」もその代わりではない**
                 （0 の近くを広げるために足したものではなく、勝率で見たい人の軸）。 -->
            <span id="eval-graph-mode" class="eval-graph-mode" role="group"
                  aria-label="縦軸">
              <button type="button" data-mode="eval" class="is-on" aria-pressed="true"
                      title="評価値（センチポーン）そのまま。目盛りは等間隔で、±3000 で頭打ちです">評価値</button>
              <button type="button" data-mode="scaled" aria-pressed="false"
                      title="圧縮: ±500 までで上下半分、±3000（まぁ勝ち）までで 8 割、±30000（詰み・必至）までを端に畳みます。目盛りの数字は評価値のままです">圧縮</button>
              <button type="button" data-mode="winrate" aria-pressed="false"
                      title="先手の勝率（0〜100%）で見ます。評価値からの変換で、定数は設定タブ">勝率</button>
            </span>
            <!-- 横軸の範囲。**既定は「全て」＝ 指した手が全部見えている状態。**
                 ⚠️ **1 から始まるとは限らない** —— 根は初期局面とは限らないので、
                 撮った 40 手目の局面から始めたなら 40 手目から始まる。

                 **絞るのはグラフの上をドラッグする**のが本筋で、選んだ範囲は
                 そのまま下の欄に入る（＝見えている数字が今の範囲）。
                 「1-150 の中のどこに居るか」で読みたいときは欄に直接書く。 -->
            <label class="eval-graph-all"
                   title="全て: 指した手が全部見える範囲にします。外すと右の欄の手数がそのまま横軸になります">
              <input id="eval-graph-all" type="checkbox" checked />
              <span>全て</span>
            </label>
            <!-- 横軸の範囲の欄。⚠️ **「全て」のときも隠さないこと**（2026-09-10）——
                 今どの範囲を見ているのかが画面から読めなくなる。「全て」のあいだは
                 触れなくして、実際の範囲（根の手数〜最終手）を書き込む。
                 ⚠️ **「自由入力」という札は要らない** —— チェックを外せば
                 触れるようになる欄そのものが、そう言っている。 -->
            <span id="eval-graph-fields" class="eval-graph-fields">
              <input id="eval-graph-from" class="eval-graph-num" type="number"
                     min="0" max="999" step="1" value="1" title="左端の手数" />
              <span class="eval-graph-dash">-</span>
              <input id="eval-graph-to" class="eval-graph-num" type="number"
                     min="1" max="999" step="1" value="150" title="右端の手数" />
            </span>
            <!-- どの色がどのエンジンか。**グラフの中に重ねない**（目盛りと重なるうえ、
                 折れ線の描ける範囲がそのぶん狭くなる）。 -->
            <span id="eval-graph-legend" class="eval-graph-legend"></span>
            <!-- 触った位置の中身。**ツールチップだけにしない**（点の上にぴったり
                 乗せないと出ないので、線を目で追いながらは読めない）。 -->
            <span id="eval-graph-readout" class="note eval-graph-readout"></span>
            <!-- 切り離し（2026-09-09 にバーの上からここへ移した）。
                 ⚠️ **切り離した窓の「ドックに戻す」と同じ場所**にすること ——
                 出す/戻すが同じ位置にあると、どちらの状態でも探す場所が変わらない。
                 ⚠️ 畳む（高さ 0）とは別物。畳むのは「今は見ない」で、
                 こちらは別の窓で見る（盤の大きさに効かなくなる）。 -->
            <button id="eval-graph-detach" class="icon-btn" type="button"
                    title="切り離す: 評価値グラフを別ウィンドウに出します（盤の大きさに効かなくなります）"
                    aria-label="評価値グラフを切り離す"></button>
          </div>
          <div id="eval-graph" class="eval-graph"
               title="押すとその局面に戻ります（手順は消えません）。横にドラッグするとその範囲に絞ります。押したあとは ← / → で 1 手ずつ動かせます"></div>
        </div>
        <!-- 連続解析のあいだ被せる幕（2026-08-14）。**触れなくするのが目的。**
             連続解析は 1 手ずつ局面を動かしながら走るので、その最中に盤や
             手順を触ると**自分の操作と連続解析が同じ局面を取り合う**
             （押した手からまた解析が進んでいくように見える）。

             ⚠️ **止める口をこの中に置くこと。** 幕は下を全部塞ぐので、
             側の列の「停止」も押せなくなる。**出口が無い幕にしない。**
             ⚠️ **薄くすること** —— 下で盤と評価値グラフが 1 手ずつ進むのが
             見えていないと、待っているあいだ何が起きているのか分からない。 -->
        <div id="batch-veil" class="veil" hidden>
          <div class="veil-box" title="ドラッグで動かせます（盤や評価値グラフが隠れているとき）">
            <p id="batch-veil-note" class="veil-note">連続解析中…</p>
            <button id="batch-veil-cancel" class="veil-btn" type="button">解析をキャンセル</button>
          </div>
        </div>
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
      showStudy(state);
    },
    setStudyViewpoint: (black) => setStudyViewpoint(black),
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
      // ⚠️ **連続解析は持ち主に止めさせること**（2026-09-12）。切り離していると
      // 回しているのは**別の窓**なので、こちらで `Stop()` しても
      // **今の 1 手が止まるだけで次の手が始まる**（`cancelBatch` が持ち主でなければ
      // `study:cancel` を飛ばす）。
      // ⚠️ **走っているときだけ頼むこと** —— 走っていないのに頼むと、
      // 向こうの面に「連続解析を止めました」とだけ出る。
      if (sidePane.stepping()) {
        sidePane.cancelBatch();
      }
      sidePane.release();
      void AnalyzeService.Release();
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
    sidePane.setTabShown(target === studyTab);
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
      studyBoardUI.relayout();
      // ⚠️ **グラフも測り直す。** 大きさは `clientWidth` で測っており、
      // **`display: none` の中では 0 になる**（測らないと出ないか、前回の
      // 大きさのまま残る）。盤のグリッドと同じ落とし穴。
      evalGraphUI.relayout();
      // ⚠️ **縦のスプリットバーの遊びもここで消す。** 幅の判定は**実際の盤を
      // 測って**行うので、**隠れているあいだは決められない**（測ると 0）。
      // これが無いと、開いた直後の 1 回目のドラッグが空振りする。
      layout.settle();
      // **解析タブに来たら（連続モードなら）そのまま解析を始める。**
      // まだ解析していない局面のときだけ動く（止めた解析を勝手に起こし直さない）。
      // ⚠️ **切り離しているあいだは向こうが持っている**（こちらは眠っている）。
      if (!layout.sideDetached()) {
        sidePane.reveal();
      }
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

  // ---- エンジン解析（Phase 4）。**解析タブの中身。** --------------------------
  //
  // **確定した局面にだけかかる。** 確定していない局面はそもそも解析タブに来ない
  // （Go 側の StudyService.Adopt が断る）ので、以前あった「訂正中は押せない」という
  // 判定はフロントから消えた —— **タブを分けたことで構造上そこに手が届かない。**
  //
  // 反復深化なので**深さが 1 つ終わるたびに答えが更新される**。終わるまで黙って
  // いると数秒固まって見えるので、途中経過をそのまま出して育つ様子を見せる。
  //
  // ⚠️ **局面を採り直したら結果を消す。** 評価値は「その局面の」値で、盤が変わった
  // あとも残っていると別の局面の値を今の盤の評価として読ませることになる。
  // ---- 解析タブの盤 --------------------------------------------------------
  //
  // **訂正タブとは別の <shogi-board>。** 同じ盤を出し入れして使い回さないこと
  // （タブごとに別の局面が出ているのが正しい状態で、片方を動かしたらもう片方も
  // 動く、という作りにすると「今どちらの局面を見ているか」が分からなくなる）。
  //
  // ⚠️ **重ねるグリッドは訂正タブのものを流用しない**（別のタブの別の盤）。
  // ここのグリッド（`study.ts`）は**合法手だけ**を扱い、クリック 2 回で指す。
  const studyStage = root.querySelector<HTMLElement>("#study-stage")!;
  const studyBoard = root.querySelector<HTMLElement>("#study-board")!;
  const studyPlaceholder = root.querySelector<HTMLParagraphElement>("#study-placeholder")!;
  const studySfenOut = root.querySelector<HTMLElement>("#study-sfen")!;
  const studySfenRow = root.querySelector<HTMLElement>("#study-sfen-row")!;
  const studyFollowBtn = root.querySelector<HTMLButtonElement>("#study-follow")!;
  // 盤の脇の駒台（読み取り専用）。**訂正タブの駒台とは別物**で、
  // ドラッグの入口も「足りない駒」も持たない。
  const studyHandSlots = {
    black: root.querySelector<HTMLElement>("#study-hand-black-slot")!,
    white: root.querySelector<HTMLElement>("#study-hand-white-slot")!,
  };
  // 盤の右の列（解析の行・エンジンの結果・手順）。SFEN と評価値グラフは盤の下。
  // ⚠️ **中身は局面があるときだけ出す**（無いときは盤の代わりに案内を出す）。
  const studySide = root.querySelector<HTMLDivElement>("#study-side")!;
  // 中身は訂正タブと同じ .hand-zone だが、**見出しは出さない**（`is-readonly`）。
  // 盤との位置関係そのものが「どちらの駒台か」の説明になっているので、
  // 掴む相手でもない箱に文字を足すと、そのぶん駒台が縦に伸びるだけになる。
  // 読み上げ用に aria-label だけ持たせる。
  const studyHandZones = [true, false].map((black) => {
    const zone = document.createElement("div");
    zone.className = "hand-zone is-readonly";
    zone.dataset.black = String(black);
    zone.setAttribute("aria-label", `${black ? "先手" : "後手"}の駒台`);
    // 手番のマーク（2026-08-13）。**駒台の外側の角に絶対配置**なので高さを食わない
    // （盤の大きさは `--board-size` の式で決まるので、行を積むと盤が小さくなる）。
    // ⚠️ **常に両側に置いて手番側だけ光らせる**（出たり消えたりさせない）。
    // ⚠️ **解析タブでは手番がここにしか出ていない** —— SFEN 行の値は
    // ツールチップなので、ホバーしないと読めない。
    zone.innerHTML = `<span class="turn-mark">${black ? "▲" : "△"}</span>` +
      `<div class="hand-chips"></div>`;
    (black ? studyHandSlots.black : studyHandSlots.white).appendChild(zone);
    return zone;
  });
  // 投了（2026-10-09）。**駒台の外側の縁、手番のマークの横の角**（奥の駒台なら上、
  // 手前の駒台なら下。置き場所は style.css の `.resign-btn` と `_docs/ui/layout.md` の「投了のボタン」）。
  //
  // **投了すると終局になり、投了の行の右クリックから棋譜タブに登録できる。** 対局の
  // 途中で誤って登録する事故を防ぐため、登録の口は投了の行にしか無い（`movelist.ts`）。
  // ⚠️ **駒台の中・盤の真ん中寄りに戻さないこと**（2026-10-09 に 2 度置いて外した）。
  // 駒台の中は駒を取ろうとして押し、盤の真ん中寄りは視点のボタンと間違えて押す。
  // **外側の縁は誰も押さない**（手番のマークしか無い）。
  // ⚠️ **駒台の中の要素として置き、絶対配置で外側へ出す**（手番のマークと同じ）——
  // 視点を反転すると駒台ごと動き、高さも食わない（盤が小さくならない）。
  // ⚠️ **押せるのは手番の側だけ**（KIF の「91 投了」も手番の側の投了と読まれる）。
  // **両側に常に置いて押せない側は `disabled`**（手番のマークと同じく出たり消えたりさせない）。
  // ⚠️ **投了できるかは Go 側**（`StudyState.canResign`。本譜の先端で、終局していないとき）。
  // ⚠️ **中継を追っているあいだは押させない**（盤の駒と同じ。追従の状態は Go 側に無い）。
  // ⚠️ **押したら確認を出す**（初めは「やめる」。Enter の連打で投了しない）。
  const studyResignBtns = studyHandZones.map((zone) => {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "resign-btn";
    b.textContent = "投了";
    b.hidden = true;
    b.dataset.black = zone.dataset.black;
    // ⚠️ **駒台のクリック（打つ駒を選ぶ）に流さない**（study.ts は `.stock-chip` しか見ないが念のため）。
    b.addEventListener("click", (e) => {
      e.stopPropagation();
      askResign(b, e);
    });
    zone.appendChild(b);
    return b;
  });
  // 投了のボタンの出し入れに要る値（**局面の写しではない**。showStudy が入れる）。
  let studyCanResign = false;
  let studyTurnNow = 0;
  let studyFromURL = false;
  let studyLoadedForResign = false;
  let studyDropped = false;
  const resignSide = (b: HTMLButtonElement): boolean => b.dataset.black === "true";
  const sideName = (black: boolean) =>
    black ? (studyDropped ? "▲下手" : "▲先手") : (studyDropped ? "△上手" : "△後手");
  const syncResign = () => {
    for (const b of studyResignBtns) {
      const black = resignSide(b);
      const side = sideName(black);
      const mine = studyTurnNow === (black ? 1 : 2);
      // URL から読んだ棋譜は終局を取得元が決めるので、ボタンごと出さない。
      b.hidden = !studyLoadedForResign || studyFromURL;
      b.disabled = !(studyCanResign && mine && !isFollowing());
      b.title = isFollowing()
        ? "中継を追っているあいだは投了できません"
        : !mine
          ? `${side}の手番ではありません`
          : studyCanResign
            ? `${side}が投了します`
            : "本譜の最後の手で、終局していないときだけ投了できます";
    }
  };
  onFollowingChange(() => syncResign());
  const askResign = (b: HTMLButtonElement, e: MouseEvent) => {
    if (b.disabled || isFollowing()) {
      return;
    }
    openPopup(e.clientX, e.clientY, {
      label: "投了",
      // ⚠️ **初期フォーカスは「やめる」**（手順を消す確認と同じ）。
      focus: 1,
      items: [
        { label: `${sideName(resignSide(b))}が投了する`, onPick: () => void resignStudy() },
        { label: "やめる", onPick: () => {} },
      ],
    });
  };
  const resignStudy = async () => {
    if (isFollowing()) {
      return;
    }
    try {
      showStudy(await StudyService.Resign());
      sidePane.setStatus("");
    } catch (err) {
      sidePane.setStatus(String(err instanceof Error ? err.message : err));
    }
  };
  // 訂正タブ側の一行。訂正の操作が通らなかった理由と、確定できない理由を出す。
  const editStatus = root.querySelector<HTMLParagraphElement>("#edit-status")!;

  // 視点のボタン（解析タブ）。**盤のグリッドの右の列**に置いてある
  // （先手の対局者名の下・先手の駒台の上）。
  const studyFlip = root.querySelector<HTMLButtonElement>("#study-flip")!;
  // 中継の札（2026-10-08。視点のボタンの上）。画面を撮って追っている / 棋譜の自動更新。
  // ⚠️ **映すだけ**（入り切りの持ち主は `setFollowing` と `StudyState.kifuFollow`）。
  const studyLiveFollow = root.querySelector<HTMLElement>("#study-live-follow")!;
  const studyLiveKifu = root.querySelector<HTMLElement>("#study-live-kifu")!;
  // 棋譜の自動更新が入っているか（`StudyState.kifuFollow` の写し。**書き換えるのは `showStudy` だけ**）。
  let kifuFollowOn = false;

  // 視点（手前が先手 / 手前が後手）。**表示だけの反転で、局面には効かない。**
  // 切り替えの中身は下の「視点」の節にまとめてある（ここは値の置き場所だけ）。
  //
  // ⚠️ **持っているのは解析タブのぶんだけ**（2026-08-18）。訂正タブは
  // **撮ったとおりを描く面**なので反転しない（ボタンごと外した）。解析タブは
  // 撮った画像が後手目線なら**180 度回した局面**を持つので、撮った見え方に
  // 揃えるにはこちらを反転する（採るときに `adoptToStudy` が合わせる）。
  // ⚠️ **訂正タブにも反転を戻さないこと** —— 戻すと、後手目線で採った瞬間に
  // どちらを裏返すのかが 2 通りになり、撮った画像と見比べられなくなる。
  let studyFlipped = false;
  // 解析タブの駒台に最後に描いた中身。**視点を切り替えたときに並べ直すため**に持つ
  // （局面の写しではない —— 駒台の並び順だけがここに依存している）。
  let studyHands: Stock[] = [];
  // 最後に描いた状態の版（`StudyState.rev`。2026-09-08）。
  //
  // ⚠️ **局面の写しではない。** `study:changed` は**メソッドの戻り値とは別の経路**で
  // 届くので順番が入れ替わりうる（十字キーで手を続けて辿ると、古い局面のイベントが
  // 後から届く）。**既に描いた版より新しいときだけ描く**ための番号。
  let studyRev = 0;
  // 解析タブに局面があるか。
  //
  // ⚠️ **他の要素の `hidden` から読まないこと**（2026-09-09 に踏んだ）。
  // 切り離すと `.study-side` も `#eval-graph-row` も**別の理由で hidden になる**ので、
  // 片方から読むと**もう片方を戻したときに「局面が無い」と誤読する**
  // （実際、解析の列を切り離しているとグラフがドックへ戻らなかった）。
  let studyLoaded = false;

  // followWaitFrom は本譜の先端から「次に来るはずの手数」を割り出す。
  //
  // ⚠️ **`moveNumber` を使わないこと** —— あちらは**今見ている局面**の手数で、
  // 枝を読んでいると本譜の先端とずれる（追っているのは本譜の先端のほう）。
  // ⚠️ **`first` を足すこと** —— `Node.Number` は**根からの**手数で、棋譜の手数ではない。
  const followWaitFrom = (st: StudyState) => {
    if (!st.loaded) {
      return 0;
    }
    const tip = (st.nodes ?? []).find((n) => n.id === st.mainTip);
    return (st.first ?? 0) + (tip?.number ?? 0) + 1;
  };

  // ---- 盤の周り（勝率バー・対局者・連続解析の幕）----------------------------
  //
  // ⚠️ **ここは盤の側**（2026-09-08 に側の列と分けた）。**候補手も手順も持たない** ——
  // 切り離すと側の列は別の窓にあるので、**そちらの要素をここから触らないこと。**
  const evalGraphRow = root.querySelector<HTMLDivElement>("#eval-graph-row")!;
  // 連続解析のあいだ被せる幕（2026-08-14）。**触れなくするのが目的。**
  // ⚠️ **出口（止める口）を中に置くこと。** 幕は下を全部塞ぐので、
  // 側の列の「停止」も押せなくなる。
  const batchVeil = root.querySelector<HTMLElement>("#batch-veil")!;
  const batchVeilNote = root.querySelector<HTMLElement>("#batch-veil-note")!;
  const batchVeilCancel = root.querySelector<HTMLButtonElement>("#batch-veil-cancel")!;
  // ⚠️ **箱は真ん中に出して、邪魔ならドラッグで避けてもらう**（2026-09-14。
  // 以前は左の真ん中へ決め打ちで寄せていた）。**どこが邪魔かは窓の形と
  // 「今何を見ているか」で変わる**ので、寄せ先を決め打ちにしない。
  enableVeilDrag(batchVeil, root.querySelector<HTMLElement>("#batch-veil .veil-box")!);

  const winrateRow = root.querySelector<HTMLDivElement>("#winrate-row")!;
  const playerNames = {
    black: root.querySelector<HTMLElement>("#player-name-black")!,
    white: root.querySelector<HTMLElement>("#player-name-white")!,
  };

  // showPlayers は勝率バーの左右に対局者を出す。
  //
  // ⚠️ **Go 側は空を「先手」「後手」で埋めない**（名前が分かっているのか、
  // 既定を出しているだけなのかが区別できなくなる）。**既定の文言はここが持つ。**
  // ⚠️ **▲△ は名前があっても付ける**（名前だけではどちらか分からない）。
  //
  // ⚠️ **駒落ちでは既定を「下手／上手」にする**（2026-09-12。`StudyState.handicap`
  // が空でなければ駒落ち）。**替わるのは名前が無いときの既定だけ**で、
  // ▲△ も左右も側も動かさない（下手＝先手＝▲のまま）。
  const showPlayers = (black: string, white: string, handicap: string) => {
    const dropped = handicap !== "";
    const blackName = black || (dropped ? "下手" : "先手");
    const whiteName = white || (dropped ? "上手" : "後手");
    playerNames.black.textContent = `▲${blackName}`;
    playerNames.white.textContent = `△${whiteName}`;
    playerNames.black.title = blackName;
    playerNames.white.title = whiteName;
  };

  const winrateBar = root.querySelector<HTMLButtonElement>("#winrate-bar")!;
  const winrateWhite = root.querySelector<HTMLElement>("#winrate-white")!;
  // 評価値グラフの折れ線の色（**エンジンごと**）。⚠️ **側の列とは別に持つ** ——
  // 切り離すとあちらは別の窓に居るので、引きに行けない。出所は同じ設定なので、
  // **どちらも `showSettings` から入れ直す**。
  const graphColors = new Map<string, string>();

  // 今バーに出しているエンジン（**押すと次へ回る**）。
  //
  // ⚠️ **解析をまたいで残すこと。** 連続モードでは 1 手ごとに作り直されるので、
  // 毎回 1 つ目へ戻ると切り替えた意味が無い。
  let winrateEngineId = "";
  // 側の列から届いた**エンジンごとの最善手**。⚠️ **ここで cp から勝率を
  // 計算し直さないこと**（式も定数も Go 側。設定で定数を変えたときに片方だけ古くなる）。
  let winrateScores: EngineScore[] = [];

  // 勝率バーの左右を入れ替えているか（2026-10-02。黒地の右クリック）。
  // 立っていれば**左が先手（赤）・右が後手（青）**。
  //
  // ⚠️ **盤の視点（`studyFlipped`）とは別物。連動させないこと** —— 帯は盤ではなく
  // 横棒なので上下の視点と対応が無い。こちらは「横棒をどちら向きで読みたいか」の好み。
  let winrateFlipped = false;

  // applyWinRateFlip は帯と対局者名の左右を入れ替える（**画面だけ**。保存は呼び出し側）。
  //
  // ⚠️ **対局者名も一緒に動かすこと** —— 名前は帯の左右に並んでいて ▲△ で帯の色と
  // 対応しているので、帯だけ動くと名前が反対側の色の隣に居ることになる。
  // ⚠️ **駒台は動かさない**（盤の一部。遠い側が左上・近い側が右下のまま）。
  // ⚠️ **値の向きは触らない** —— 入れ替えるのは**どちらの端から描くか**（CSS）だけで、
  // `winrateWhite` の幅は後手の勝率のまま。
  const applyWinRateFlip = () => {
    winrateRow.classList.toggle("is-flipped", winrateFlipped);
    playerNames.black.classList.toggle("is-swapped", winrateFlipped);
    playerNames.white.classList.toggle("is-swapped", winrateFlipped);
    // title の並びも見た目の左右に合わせる。
    renderWinRate();
  };

  // 勝率バーに出しているエンジンを**側の列のカードに薄い枠で示す**（2026-10-02）。
  //
  // ⚠️ **選んでいるのはここ**（`winrateEngineId`）で、側の列は言われたカードに
  // 印を付けるだけ。**ドックしている列には直に、切り離した窓には
  // `study:winrate-engine` で**渡す（切り離した窓は切り離す前から生きているので、
  // 変わるたびに送っておけば出たときに揃っている）。
  // ⚠️ **印を付けるのはエンジンが 2 つ以上のときだけ**（1 つなら見れば分かる）。
  // **帯を隠しているあいだも付けない**（見えていない帯の持ち主を示しても意味が無い）。
  // `markWinRateCard` は側の列ができてから差し替える（ここより下で作るため）。
  let markWinRateCard = (_id: string) => {};
  let winrateMarkSent: string | null = null;
  const syncWinRateMark = () => {
    const one =
      winrateScores.find((e) => e.id === winrateEngineId) ?? winrateScores[0];
    const id = one && winrateScores.length > 1 && !winrateHidden ? one.id : "";
    markWinRateCard(id);
    if (id !== winrateMarkSent) {
      winrateMarkSent = id;
      void Events.Emit("study:winrate-engine", id);
    }
  };

  // renderWinRate は選ばれているエンジンの勝率をバーに描く。
  //
  // ⚠️ **既定は左が後手（青）・右が先手（赤）。符号はここでいじらないこと**
  // （逆に伸びると「エンジンが間違えている」としか読めない壊れ方になる）。
  // **左右の入れ替え**（`winrateFlipped`）は CSS が描く端を変えるだけで、
  // ここが入れる値（後手の幅）は同じ。
  // ⚠️ **出すのは 1 つだけ**（押すと切り替わる）。並べると盤の上に段が積まれて
  // そのぶん盤が小さくなるうえ、**形勢を一目で見るための帯**なので読む対象が増える。
  const renderWinRate = () => {
    const one =
      winrateScores.find((e) => e.id === winrateEngineId) ?? winrateScores[0];
    // **押して切り替えられるのは 2 つ以上あるときだけ。**
    const many = winrateScores.length > 1;
    winrateBar.classList.toggle("is-switchable", many);
    winrateBar.disabled = !many;
    syncWinRateMark();

    if (!one || one.winRate === null) {
      // **まだ値が無いあいだは中立の見た目にして、50% と書かない**
      // ——「互角」と「まだ分からない」は別物。
      winrateBar.classList.add("is-empty");
      winrateWhite.style.width = "50%";
      winrateBar.title = one
        ? `${one.label}: 解析するとここに形勢が出ます`
        : "解析するとここに形勢が出ます";
      return;
    }
    const black = Math.min(Math.max(one.winRate, 0), 1);
    const white = 1 - black;
    winrateBar.classList.remove("is-empty");
    winrateWhite.style.width = `${(white * 100).toFixed(1)}%`;
    // ⚠️ **数字は画面に出さず、カーソルを当てたときだけ出す**（盤の上に文字を
    // 積むと、そのぶん盤が小さくなる）。
    // 並びは見た目の左右に合わせる（入れ替えているなら先手が先）。
    const whiteText = `後手 ${Math.round(white * 100)}%`;
    const blackText = `先手 ${Math.round(black * 100)}%`;
    const head = winrateFlipped
      ? `${blackText} ／ ${whiteText}`
      : `${whiteText} ／ ${blackText}`;
    winrateBar.title =
      `${head}（評価値 ${one.scoreLabel}）／ ${one.label}` +
      (many ? "　押すと別のエンジンに切り替わります" : "");
  };

  // 押すと次のエンジンに切り替える（登録順で回る）。
  winrateBar.addEventListener("click", () => {
    if (winrateScores.length < 2) {
      return;
    }
    const at = winrateScores.findIndex((e) => e.id === winrateEngineId);
    winrateEngineId = winrateScores[(at + 1) % winrateScores.length].id;
    renderWinRate();
  });

  // ---- 勝率バーを隠す（2026-09-10）------------------------------------------
  //
  // **中継を観ながら使うので「評価値を見たくない」場面がある。** 盤の真上の帯は
  // 目に入るのを避けようが無いので、消せるようにした。切り替えるのは
  // **盤の右クリック**（設定タブには置いていない —— 見えているものを消す操作は、
  // その場で切り替えるほうが素直）。
  //
  // ⚠️ **解析は止めない。** 消えるのは帯だけで、候補手も評価値も右の列には
  // 今までどおり出る（**見たくないものだけを消す**）。
  //
  // ⚠️ **帯と対局者名は別々に消せる**（2026-09-10。1 行目に並んでいる 2 つ）。
  // **帯だけ消して名前は残す**（誰の対局かは見ていたい）も、**両方消す**
  // （盤を大きくしたい）も、どちらも普通の使い方なので 1 つにまとめない。
  let winrateHidden = false;
  let playersHidden = false;

  // ⚠️ **`is-toprow-empty` だけはここが持つ**（ほかの `#panel-study` の class と CSS 変数は studylayout.ts）。
  const panelStudy = root.querySelector<HTMLElement>("#panel-study")!;
  // applyStudyTopRow は**盤の上の 1 行**（帯・対局者名）の出し入れ。
  // **画面だけに効かせる**（保存は呼び出し側）。
  //
  // ⚠️ **出す条件をここ 1 か所にまとめてあること。** 「局面がある」と
  // 「隠していない」の掛け算なので、`syncStudyChrome` からもここを呼ぶ ——
  // 2 か所に書くと、**隠したまま別の局面を読み込んだ瞬間に戻る**。
  //
  // ⚠️ **盤の取り分（`--winrate-h`）を返すのは、行が空になったときだけ。**
  // 片方でも出ているなら 1 行目は残るので、引く量だけ減らすと**そのぶん盤が
  // 縦にはみ出す**（対局者名は 24px あり、帯の 34px とほとんど変わらない）。
  const applyStudyTopRow = () => {
    winrateRow.hidden = winrateHidden || !studyLoaded;
    // 帯を隠したらカードの印も外す（出したら戻す）。
    syncWinRateMark();
    playerNames.black.hidden = playersHidden || !studyLoaded;
    playerNames.white.hidden = playersHidden || !studyLoaded;
    // ⚠️ **`studyLoaded` は見ないこと。** 局面が無いあいだも行の高さは
    // 予約したままにする（出たり消えたりすると盤ごと上下に動く）。
    const empty = winrateHidden && playersHidden;
    if (panelStudy.classList.contains("is-toprow-empty") === empty) {
      return;
    }
    panelStudy.classList.toggle("is-toprow-empty", empty);
    // 盤の大きさが変わったので、重ねたグリッドと横の遊びを取り直す
    // （評価値グラフの切り離しと同じ後始末）。
    studyBoardUI.relayout();
    evalGraphUI.relayout();
    layout.settle();
  };

  // 解析タブの**空いているところ**（盤・駒台・帯・右の列のどれでもない黒地）の
  // 右クリックで出すメニュー（2026-09-10）。
  //
  // ⚠️ **盤と駒台の上では出さない。** あちらの右クリックは**掴んだ駒を離す**
  // 操作（2026-08-29）で、指す先を探している最中にメニューが出ると
  // **やめる操作がメニューを閉じる操作に化ける。**
  //
  // ⚠️ **`window.confirm` と同じで、押した場所に出すこと**（`popup.ts`）。
  const openBoardMenu = (x: number, y: number) => {
    openPopup(x, y, {
      label: "盤の表示",
      // ⚠️ **1 つにまとめないこと**（2026-09-10）。**帯だけ消して名前は残す**
      // 使い方があるので、独立したチェック 2 つにしてある。
      items: [
        {
          label: "評価値バー",
          checked: !winrateHidden,
          onPick: () => {
            winrateHidden = !winrateHidden;
            // **先に画面へ効かせる**（押した手応えを保存の往復まで待たせない）。
            applyStudyTopRow();
            // ⚠️ **設定に残すこと** —— 画面の組み方の好みなので、次の起動でも
            // 同じ形で始まってほしい（切り離しと同じ扱い）。
            void SettingsService.SetHideWinRateBar(winrateHidden);
          },
        },
        {
          label: "対局者名",
          checked: !playersHidden,
          onPick: () => {
            playersHidden = !playersHidden;
            applyStudyTopRow();
            void SettingsService.SetHidePlayerNames(playersHidden);
          },
        },
        {
          // 2026-10-02。⚠️ **隠す 2 つとは別のチェック**（帯を消したまま向きだけ
          // 変えても、次に出したときにその向きで出ればよい）。
          label: "評価値バーの左右を反転",
          checked: winrateFlipped,
          onPick: () => {
            winrateFlipped = !winrateFlipped;
            applyWinRateFlip();
            void SettingsService.SetFlipWinRateBar(winrateFlipped);
          },
        },
      ],
    });
  };

  // isStudyBackdrop は「解析タブの、何も置いていないところを押したか」。
  //
  // ⚠️ **判定は「押した先が入れ物そのものか」で行う。除外リストを持たないこと** ——
  // 盤・駒台・帯・ボタン・右の列・幕はどれも**中身の要素が受ける**ので、入れ物まで
  // 抜けてくるのは何も置いていないところを押したときだけ。除外リストにすると、
  // **行を足すたびに書き足すことになり、書き忘れた場所でだけメニューが出る。**
  //
  // ⚠️ **`#panel-study` の中に限ること。** `.board-area` は訂正タブにもある。
  const isStudyBackdrop = (t: EventTarget | null): boolean => {
    const el = t as HTMLElement | null;
    if (!el?.closest?.("#panel-study")) {
      return false;
    }
    return (
      el.id === "panel-study" ||
      el.id === "study-board-with-hands" ||
      el.classList.contains("board-area")
    );
  };

  root.addEventListener("contextmenu", (e) => {
    if (!isStudyBackdrop(e.target)) {
      return;
    }
    // ⚠️ **webview の既定メニューは止める**（盤まわりでは意味が無いうえ、
    // こちらのメニューと二重に出る）。
    e.preventDefault();
    openBoardMenu(e.clientX, e.clientY);
  });

  // syncStudyChrome は**盤の周りの出し入れ**（局面があるかどうかだけで決まる）。
  //
  // ⚠️ **まだ結果が無くても枠は出す。** 出たり消えたりすると盤が上下に動くうえ、
  // 勝率バーは盤の**上**なので、動くと盤ごと押し下げる。
  const syncStudyChrome = (loaded: boolean) => {
    // ⚠️ **盤の上の 1 行（帯・対局者名）は `applyStudyTopRow` に任せること。**
    // 「隠してあるなら局面があっても出さない」の判断を 2 か所に書くと、
    // **隠したまま別の局面を読み込んだ瞬間に戻る**（`studyLoaded` は
    // 呼び出し元が先に入れてある）。
    applyStudyTopRow();
    // 視点のボタンも盤と一緒（盤が出ていないのに向きだけ変えても意味が無い）。
    studyFlip.hidden = !loaded;
    // 側の列・縦のバー・評価値グラフの行（⚠️ **切り離しているあいだは出さない**。式は studylayout.ts の 1 か所）。
    layout.showPanes(loaded);
  };

  // showStudy は解析タブの表示一式を描く。
  //
  // ⚠️ **盤の側だけ**（2026-09-08）。**候補手・手順・解析の操作は `sidepane.ts`** で、
  // ここは `render` を呼ぶだけ —— **別ウィンドウへ切り離しても同じものが動く**
  // ようにするため、側の列の中身をここから触らないこと。
  //
  // ⚠️ **盤の操作から呼ばれたとき（`fromBoard`）だけは盤を描き直さない**
  // （あちらが既に描いている。同じ状態を 2 か所から描くと、どちらが今か分からなくなる）。
  const showStudy = (st: StudyState, o?: { fromBoard?: boolean }) => {
    // ⚠️ **描いた版を控えること。** これが遅れて届いた `study:changed` を弾く鍵。
    studyRev = Math.max(studyRev, st.rev ?? 0);
    const loaded = !!st.loaded;
    studyLoaded = loaded;
    studyStage.hidden = !loaded;
    studyBoard.hidden = !loaded;
    studyPlaceholder.hidden = loaded;
    // 駒台は局面の一部なので、局面があるあいだは**空でも出す**
    // （持ち駒が 0 枚であることも局面の情報）。
    studyHandSlots.black.hidden = !loaded;
    studyHandSlots.white.hidden = !loaded;
    // SFEN も盤と一緒（盤の下の行なので、局面が無いのに枠だけ残さない）。
    studySfenRow.hidden = !loaded;
    if (loaded) {
      studyBoard.setAttribute("sfen", st.boardSfen);
      // 手番と手数は SFEN に入っているが、読むのに要るのは文字のほう。
      const n = st.moveNumber > 0 ? ` / ${st.moveNumber}手目` : "";
      studySfenOut.textContent = `${st.sfen}`;
      // ⚠️ **SFEN そのものも title に入れること**（2026-08-14）。盤の下は 1 行
      // しか無いので、長い局面は**画面では末尾が切れる**。
      studySfenOut.title = `${st.sfen}
${st.turnLabel}${n}`;
      showStudyHand(st.hands ?? []);
    } else {
      studySfenOut.textContent = "-";
      showStudyHand([]);
    }
    // ⚠️ **手番のマークは局面と一緒に更新する。** 1 手ごとに入れ替わるので、
    // 落とすと**前の手番のまま光り続ける**（連続モードでは毎手ずれる）。
    showStudyTurn(loaded ? st.turn : 0);
    // 投了のボタンも手番と一緒（押せるのは手番の側だけ）。
    studyCanResign = loaded && !!st.canResign;
    studyTurnNow = loaded ? st.turn : 0;
    studyFromURL = loaded && (st.sourceUrl ?? "") !== "";
    studyLoadedForResign = loaded;
    studyDropped = loaded && (st.handicap ?? "") !== "";
    syncResign();
    // ⚠️ **局面と一緒に更新する。** 根を入れ替えると対局者も変わる。
    showPlayers(st.black ?? "", st.white ?? "", st.handicap ?? "");
    if (!o?.fromBoard) {
      studyBoardUI.render(loaded ? st : null);
    }
    // ⚠️ **側の列は必ずここで描くこと。** `fromBoard` は「盤が自分で描いた」の
    // 意味で、**手順も候補手も描かれていない。**
    sidePane.render(loaded ? st : null);
    // 盤の周りの出し入れ（勝率バー・対局者・視点・評価値グラフ・側の列）。
    syncStudyChrome(loaded);
    // 中継を追うボタンも局面があるときだけ（繋ぐ先が無いと足せない）。
    studyFollowBtn.hidden = !loaded;
    // 棋譜の自動更新の札。⚠️ **条件は自動更新のアイコンと同じ**（URL から読んだときだけ。sidepane.ts）。
    kifuFollowOn = loaded && (st.sourceUrl ?? "") !== "" && !!st.kifuFollow;
    studyLiveKifu.hidden = !kifuFollowOn;
    // ⚠️ **待っている手数はここで取ること。** 追跡のループの中だけで数えると、
    // **人が手順を消したり分岐にしたりしたときに古いまま**になる
    // （`study:changed` もここへ来る）。
    followUI.setWaiting(followWaitFrom(st));
    // 局面が変わったら点を取り直す（**戻った位置の縦線も動く**）。
    refreshEvalGraph();
  };

  // ⚠️ **別ウィンドウとの連動の土台**（2026-09-08）。`StudyService` は局面を
  // 変えるたびに `study:changed` を流す（`app.Event.Emit` は**アプリ全体**に届く）ので、
  // **自分が呼んでいない変更**にも気づける。
  //
  // ⚠️ **戻り値で描く経路は残してある。** 呼んだ窓はその場で描けるほうが速いし、
  // **描くのは同じ `showStudy` に同じ `StudyState` を渡すだけ**なので、
  // 2 つの描き方が生まれるわけではない（下の版の判定で二度描きにならない）。
  //
  // ⚠️ **版が古いイベントは捨てること。** イベントと戻り値は別の経路なので
  // **順番が入れ替わりうる** —— 捨てないと、十字キーで手を続けて辿ったときに
  // **古い局面が後から届いて盤が戻る**。
  Events.On("study:changed", (event: { data: StudyState }) => {
    const st = event.data;
    if (!st || (st.rev ?? 0) <= studyRev) {
      return;
    }
    showStudy(st);
  });

  // 解析タブの駒台の角のマークを手番に合わせる（1=先手番 / 2=後手番 / 0=局面なし）。
  //
  // ⚠️ **`showStudyHand` と分けてあるのは、視点の切り替えで駒台だけを並べ直す
  // 経路があるから**（あちらは手番を知らない）。手番はここ 1 本で更新する。
  const showStudyTurn = (turn: number) => {
    for (const zone of studyHandZones) {
      const black = zone.dataset.black === "true";
      const mark = zone.querySelector<HTMLElement>(".turn-mark")!;
      const mine = turn === (black ? 1 : 2);
      const name = black ? "先手" : "後手";
      mark.classList.toggle("is-active", mine);
      mark.title = mine ? `${name}番です` : `${name}の駒台`;
    }
  };

  // 解析タブの駒台。**未決は残っていない**（確定した局面なので）。
  //
  // ⚠️ **駒種 1 つにつき駒 1 枚を出し、枚数は数字で添える**（2026-08-12 に変えた）。
  // 以前は訂正タブと同じく**枚数ぶん並べていた**が、歩が溜まると駒台から溢れ、
  // **スクロールバーが出て幅を食い、1 行に入る駒が 3 枚から 2 枚に減って更に溢れる**
  // という悪循環になっていた。駒種は最大 8 つなので、この形なら**溢れが原理的に
  // 起きない**（多くの将棋ソフトと同じ見せ方でもある）。
  // ⚠️ **訂正タブの駒台は今までどおり枚数ぶん並べる** —— あちらは 1 枚ずつ掴んで
  // 動かす面なので、駒の数と操作の対象が一致しているほうがよい。
  const showStudyHand = (inv: Stock[]) => {
    // ⚠️ **視点を切り替えたときに並べ直すため、最後に描いた中身を覚えておく。**
    // 覚えないと、反転しても駒台だけが前の向きの並びのまま残る。
    studyHands = inv;
    for (const zone of studyHandZones) {
      const black = zone.dataset.black === "true";
      const chips = zone.querySelector<HTMLDivElement>(".hand-chips")!;
      chips.replaceChildren();
      let total = 0;
      // 並びは Go 側の Inventory の順（歩香桂銀金角飛王）。
      // **駒が 180 度回っている側は逆順**（そちら側から読んで同じ並びになる）。
      // ⚠️ **回っているのは「後手」ではなく「奥の側」**なので、視点を反転すると
      // 逆順にする相手も入れ替わる（`black === flipped`）。
      for (const s of black === studyFlipped ? [...inv].reverse() : inv) {
        const n = black ? s.handBlack : s.handWhite;
        if (n <= 0) {
          continue;
        }
        total += n;
        const chip = document.createElement("div");
        chip.className = black ? "stock-chip" : "stock-chip is-white";
        // ⚠️ **piece を持たせること。** 駒台の駒はマスを持たないので、
        // 「打てる位置」を合法手（`legal.Move.drop`）と突き合わせる鍵がこれしかない。
        chip.dataset.piece = String(s.piece);
        chip.append(black ? s.letter : s.letter.toLowerCase());
        // ⚠️ **見出しは dataset にも持たせる。** `study.ts` が「押せない理由」を
        // 足した title を組み立てるので、素の見出しが要る（title へ直に足すと、
        // 描き直すたびに理由が積み重なる）。
        chip.dataset.label = `${black ? "先手" : "後手"}の${s.name}（${n}枚）`;
        chip.title = chip.dataset.label;
        // 1 枚のときは数字を出さない（実際の駒台と同じで、見れば分かる）。
        if (n > 1) {
          const count = document.createElement("span");
          count.className = "stock-count";
          count.textContent = String(n);
          chip.appendChild(count);
        }
        chips.appendChild(chip);
      }
      zone.classList.toggle("is-empty", total === 0);
    }
    // ⚠️ **文字の要約（「先手 歩2 / 後手 角1」）は出さない**（2026-08-12 に外した）。
    // 盤の脇に駒そのものが並んでいるので同じことを 2 度言っており、
    // **盤の下に行を積むぶんだけ盤が小さくなっていた。**
  };

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
    sidePane.setStatus("");
    try {
      showStudy(await StudyService.Adopt());
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
    setStudyViewpoint(!editNearWhite);
    selectTab(studyTab);
    // ⚠️ **追っていたなら上の確認で止まっている**ので、ここでは必ず「入れる」側になる。
    // ⚠️ **ボタンを押す形にしないこと** —— 画面の写し（`followOn`）は止めた知らせが届くまで古いので、押すと止める側に倒れる。
    if (follow) {
      followUI.start();
    }
  };

  // ---- 中継を追う（followcontrol.ts）------------------------------------------
  //
  // ⚠️ **押して描くだけ**（1 周の段取りと状態は Go の中継セッション。`follow:update` を受けて札と帯へ流す）。
  const followUI = mountFollowControl({
    studyFollowBtn,
    studyLiveFollow,
    setSideStatus: (text) => sidePane.setStatus(text),
    frameVisible: () => frameVisible,
    kifuFollowOn: () => kifuFollowOn,
  });

  // 解析タブの盤の操作（手を進める UI）。**合法手だけ。**
  //
  // ⚠️ **訂正タブの mountEditor とは別物。** あちらはドラッグで自由に置く面で、
  // こちらはクリック 2 回で合法手だけを辿る面。**同じグリッドを流用しないこと。**
  const studyBoardUI = mountStudyBoard({
    stage: studyStage,
    handSlots: studyHandSlots,
    // 指したあとの局面は showStudy がそのまま描く（盤・駒台・SFEN・警告・手順）。
    onState: (st) => showStudy(st, { fromBoard: true }),
    // 空文字は「理由を消す」（駒を掴み直したときなど）。**出しっぱなしにしないこと** ——
    // 前の操作の理由が残っていると、今の操作が失敗したように見える。
    onError: (message) => sidePane.setStatus(message),
  });

  // ---- 十字キーの上下で手順を辿る（2026-08-18）----------------------------
  //
  // **↑ で 1 手戻り、↓ で 1 手進む。** 手順リストの上下の並びと同じ向きなので、
  // 押した方向とカーソルの動く向きが一致する。**盤の右のリストを押すのと同じ操作**
  // （`GoTo`）で、**手順は 1 手も消えない。**
  //
  // ⚠️ **どこを押していても効く**（リストにフォーカスを当てさせない）。手を辿る
  // ときに見ているのは**盤**なので、先にリストを掴ませるのは 1 手多い。
  // そのぶん**横取りしてはいけない相手**を並べて外してある（下記）。
  //
  // ⚠️ **Delete キー（2026-09-26）もここで受ける** —— 今見ている手から下を消す
  // （手順リストの右クリック →「以降の手を削除」と同じ。**1 手だけなら聞かずに
  // 消し、2 手以上なら確認が出る**。判断は `movelist.ts` の `dropAt`）。
  // ⚠️ **手順リストにフォーカスがあるときだけ、にはしない。** 手のボタンは押すと
  // 描き直されてフォーカスが外れるので、それでは**押した直後に効かない**。
  // 止める相手は ↑ / ↓ と同じ（欄の中の Delete は文字を消す操作）。
  // ⚠️ **手順を切り離しているときはあちらの窓が受ける**（`sidePane.dropCurrent` が
  // 「この窓に手順が出ているか」を見て何もしない）。
  const studyStep = (e: KeyboardEvent) => {
    const drop = e.key === "Delete";
    const delta = e.key === "ArrowUp" ? -1 : e.key === "ArrowDown" ? 1 : 0;
    if (!delta && !drop) {
      return;
    }
    // ⚠️ **解析タブに居るときだけ。** 訂正タブにも盤があるので、
    // タブを見ずに動かすと**見えていない盤の局面が変わる**。
    if (!studyTab.classList.contains("is-active")) {
      return;
    }
    // ⚠️ **修飾キー付きは触らない**（ブラウザや OS 側の操作なので横取りしない）。
    if (e.ctrlKey || e.altKey || e.metaKey || e.shiftKey) {
      return;
    }
    // ⚠️ **連続解析が走っているあいだは動かさない**（2026-08-14 の幕と同じ理由）。
    // あちらが 1 手ずつ局面を動かしているので、横から動かすと**自分の操作と
    // 連続解析が同じ局面を取り合う**。幕はポインタしか塞げない。
    if (sidePane.stepping()) {
      return;
    }
    // ⚠️ **小さなダイアログが開いているあいだも動かさない**（成る/成らず・
    // 手順を消す・手順を追加）。局面が変われば `study.ts` が黙って閉じるので、
    // **聞かれている最中に盤が進んで、答えが別の手に効く**ように見える。
    if (document.querySelector(".popup-menu")) {
      return;
    }
    // ⚠️ **上下キーを本来の意味で使う相手から奪わないこと。** 数値欄・選択・
    // テキスト欄と、**3 本のスプリットバー**（`role="separator"`。上下キーで
    // 大きさを変える）がそれで、奪うと**掴んでいるつもりの操作が盤を動かす**。
    const el = e.target as HTMLElement | null;
    const tag = el?.tagName ?? "";
    if (
      tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" ||
      el?.isContentEditable || el?.closest('[role="separator"]')
    ) {
      return;
    }
    // ⚠️ **ここまで来たら既定の動作は止める**（パネルが縦にスクロールする）。
    e.preventDefault();
    if (drop) {
      sidePane.dropCurrent();
      return;
    }
    studyBoardUI.step(delta);
  };
  document.addEventListener("keydown", studyStep);

  // 再読み込み（2026-08-13）。**URL の側を正**にして手順を最新にする。
  //

  // ⚠️ **置き場所は解析の行の右端**（2026-09-09 にバーの上から移した）——
  // **切り離した窓の「ドックに戻す」と同じ場所**。出す/戻すが同じ位置にあると、
  // どちらの状態でも探す場所が変わらない。
  // ⚠️ **アイコンだけなので、意味は `aria-label` と `title` が持つ。**
  const studySplitDetach = document.createElement("button");
  studySplitDetach.id = "study-side-detach";
  studySplitDetach.className = "icon-btn";
  studySplitDetach.type = "button";
  studySplitDetach.innerHTML = iconMarkup(FiExternalLink);
  studySplitDetach.setAttribute("aria-label", "候補手を切り離す");
  studySplitDetach.title =
    "切り離す: 候補手を別ウィンドウに出します（盤の大きさに効かなくなります）";

  // 手順の側の「切り離す」（2026-09-12）。⚠️ **置き場所は手順の見出しの行の右端**
  // —— 切り離した窓の「ドックに戻す」と同じ場所。**候補手の側と作法を揃えること。**
  // ⚠️ **候補手のボタンと 1 つにまとめないこと** —— どちらを外に出すかは
  // そのときの読み方で変わるので、**別々に押せることが要る。**
  const studyMovesDetach = document.createElement("button");
  studyMovesDetach.id = "study-moves-detach";
  studyMovesDetach.className = "icon-btn";
  studyMovesDetach.type = "button";
  studyMovesDetach.innerHTML = iconMarkup(FiExternalLink);
  studyMovesDetach.setAttribute("aria-label", "手順を切り離す");
  studyMovesDetach.title =
    "切り離す: 手順を別ウィンドウに出します（盤の大きさに効かなくなります）";

  // 盤の右の列（2026-09-08 に `sidepane.ts` へ切り出した）。
  //
  // ⚠️ **中身をここから触らないこと。** 別ウィンドウへ切り離せるようにするための
  // 切り出しなので、**ここが知っているのは「渡す 5 つ」だけ**にする。
  const sidePane = mountSidePane({
    host: studySide,
    // 手順の操作で局面が変わったとき（**盤も一緒に描き直す**）。
    onState: (st) => showStudy(st),
    // 候補手を選んだら盤に矢印を出す。⚠️ **切り離すと盤は別の窓**なので、
    // 側の列から直に触らせない（ここが唯一の受け口）。
    onHint: (usi) => studyBoardUI.showHint(usi),
    // 勝率バーの材料（**エンジンごとの最善手**）。
    onScores: (scores) => {
      winrateScores = scores;
      renderWinRate();
    },
    // 連続解析の幕。⚠️ **盤も塞ぐこと**（1 手ずつ動かしている最中に触られると、
    // 自分の操作と連続解析が同じ局面を取り合う）。
    // ⚠️ **評価値グラフの窓にも知らせること**（2026-09-09 に踏んだ）。あちらは
    // **点を押すと `GoTo` が飛ぶ**ので、塞がないと連続解析と局面を取り合う。
    // ⚠️ **配るのは「この列が使われているとき」だけ**（切り離していれば向こうが
    // 配る）。両方が配ると、**1 手ごとに幕が瞬く。**
    onBusy: (on, note) => {
      batchVeil.hidden = !on;
      batchVeilNote.textContent = note;
      if (!layout.sideDetached()) {
        void Events.Emit("study:busy", { on, note });
      }
    },
    // 設定を書き換えたら設定タブも描き直す（色・候補手の本数）。
    onSettings: (st) => settingsUI.showSettings(st),
    // ⚠️ **その面の中の同じ場所に置くもの。** ドックしているときは「切り離す」、
    // 切り離した窓では「ドックに戻す」——**同じ場所**にすること。
    analyzeAction: studySplitDetach,
    movesAction: studyMovesDetach,
  });
  // 勝率バーのエンジンの印（`syncWinRateMark`）。側の列ができたのでここで繋ぐ。
  markWinRateCard = (id) => sidePane.setWinRateEngine(id);
  // ⚠️ **幕の出口はここ**（幕は下を全部塞ぐので、側の列の「停止」も押せない）。
  batchVeilCancel.addEventListener("click", () => sidePane.cancelBatch());
  // メイン画面は入力タブで始まる（解析タブを開くまで棋譜の自動更新を刻まない）。
  sidePane.setTabShown(false);

  // ⚠️ **別の窓の幕からも止められること**（`study:cancel`）。評価値グラフの窓には
  // 「停止」も手順も無いので、**出口がここに繋がっていないと窓を閉じるしかない。**
  // ⚠️ **持ち主だけが応じること** —— 切り離しているあいだ連続解析を持っているのは
  // 向こうの窓で、こちらが応じても止める相手が居ない。
  Events.On("study:cancel", () => {
    if (layout.sideDetached()) {
      return;
    }
    sidePane.cancelBatch();
  });

  // 評価値グラフ（2026-08-12）。**手順の 1 手ごとの最善手の評価値**を折れ線にする。
  //
  // ⚠️ **点はここに溜めない。** 持っているのは Go 側（`StudyService.Evals`）で、
  // **手順を切ったときにどこまで捨てるかを知っているのはあちらだけ**。
  // フロントにも溜めると、戻って別の手を指したときに片方だけ古い値が残る。
  const evalGraphUI = mountEvalPane({
    host: root.querySelector<HTMLElement>("#eval-graph")!,
    all: root.querySelector<HTMLInputElement>("#eval-graph-all")!,
    from: root.querySelector<HTMLInputElement>("#eval-graph-from")!,
    to: root.querySelector<HTMLInputElement>("#eval-graph-to")!,
    mode: root.querySelector<HTMLElement>("#eval-graph-mode")!,
    legend: root.querySelector<HTMLElement>("#eval-graph-legend")!,
    readout: root.querySelector<HTMLElement>("#eval-graph-readout")!,
    // 折れ線の色は**エンジンごと**（登場順ではない）。⚠️ **設定を直に読ませない** ——
    // 既定色の解決は Go 側で済んでおり、ここは受け取った写しを引くだけ。
    colorOf: (id) => graphColors.get(id) ?? "#b8c0d0",
    // **押したらその局面へ戻る**（手順のチップと同じ「戻って見る」操作。手順は消さない）。
    // ⚠️ **渡ってくるのは節点の id**（手数ではない。枝があると同じ手数が何個もある）。
    onSeek: (id) => {
      void (async () => {
        try {
          showStudy(await StudyService.GoTo(id));
        } catch (err) {
          sidePane.setStatus(String(err instanceof Error ? err.message : err));
        }
      })();
    },
    // 縦軸を切り替えたら**保存する**（次の起動も、切り離した窓も同じ軸で始まる）。
    // ⚠️ **保存できなくても画面はそのまま**（設計原則3）。次の起動で戻るだけで、
    // 今見えている折れ線は正しい。
    onMode: (m) => {
      void SettingsService.SetEvalGraphAxis(m).catch(() => {});
    },
    // ← / → で手を動かしてよいか（2026-09-26）。**止める条件は ↑ / ↓ と同じ**
    // （連続解析の最中・小さなダイアログが出ているあいだ。`studyStep`）。
    canStep: () => !sidePane.stepping() && !document.querySelector(".popup-menu"),
  });

  // ⚠️ **取り直しの実装は `evalgraphpane.ts` に移した**（2026-09-08）。
  // **切り離した窓と同じものを使う**ので、片方だけ直して挙動が食い違うことがない。
  const refreshEvalGraph = () => evalGraphUI.refresh();

  // ⚠️ **解析のイベントはここでも拾うこと**（2026-09-08）。折れ線は解析が
  // 進むたびに伸びるが、**側の列は別ウィンドウに居ることがある**ので、
  // あちら経由にすると切り離した瞬間にグラフが止まる。
  // ⚠️ **途中経過は間引く**（深さが 1 つ進むたびに、しかもエンジンの数だけ届く）。
  Events.On("analyze:info", () => evalGraphUI.refreshSoon());
  Events.On("analyze:done", () => evalGraphUI.refresh());
  Events.On("analyze:failed", () => evalGraphUI.refresh());

  // ---- 解析タブの寸法と切り離し（studylayout.ts）----------------------------
  //
  // 評価値グラフの高さ・盤と解析の列の幅・列の折り畳み・別ウィンドウへの切り離し。
  // ⚠️ **ここ（盤・側の列・グラフを組んだ直後）で組むこと** —— 組んだ時点で
  // アイコンと `aria-*` を入れ、測り直しも呼ぶ。
  const layout = mountStudyLayout({
    root,
    studyStage,
    studySide,
    evalGraphRow,
    studyLoaded: () => studyLoaded,
    studyBoardUI,
    evalGraphUI,
    sidePane,
    studySplitDetach,
    studyMovesDetach,
  });

  // ---- 切り離した窓からの知らせ（**盤に効くもの**）--------------------------
  //
  // ⚠️ **フロントの `Events.Emit` は Go を経由して全部の窓へ配られる**
  // （`EmitEvent` → `dispatchEventToWindows`）。ドックしているあいだは
  // **コールバックで直に渡している**ので、こちらは飛んでこない。
  //
  // ⚠️ **ドックしているあいだは聞かないこと**（2026-09-09 に踏んだ）。切り離しの窓は
  // **切り離していなくても作られていて中身が動いている**ので、こちらが自分の
  // コールバックで描いているものを**向こうの描き直しが上書きする** ——
  // 実際、連続解析の最中に**幕が 1 手ごとに消えてはまた出た**（チカチカする）。
  // **同じ値を 2 経路で描かない**、が元からの約束（向こうも出さないようにしてある）。
  const fromSideWindow = () => layout.sideDetached();
  Events.On("study:hint", (event: { data: string | null }) => {
    if (!fromSideWindow()) {
      return;
    }
    studyBoardUI.showHint(event.data ?? null);
  });
  Events.On("study:scores", (event: { data: EngineScore[] }) => {
    if (!fromSideWindow()) {
      return;
    }
    winrateScores = event.data ?? [];
    renderWinRate();
  });
  // ⚠️ **幕は盤にも被せること** —— 連続解析は 1 手ずつ局面を動かすので、
  // その最中に盤を触ると自分の操作と取り合う。
  Events.On("study:busy", (event: { data: { on: boolean; note: string } }) => {
    if (!fromSideWindow()) {
      return;
    }
    batchVeil.hidden = !event.data?.on;
    batchVeilNote.textContent = event.data?.note ?? "";
  });

  // ⚠️ **「訂正に戻る」ボタンは無くした**（2026-08-12）。**上のタブで戻れる**うえ、
  // タブを離れたときの後始末（走っている解析を止めてエンジンを手放す）は
  // `selectTab` が既にやっているので、ボタンは同じことを 2 つ目の入口でしていた。
  // **盤の上の段ごと外して、そのぶん盤を大きくしてある。**

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
      showStudy(got.state);
    },
  });

  // ---- 視点（手前が先手 / 手前が後手）--------------------------------------
  //
  // **表示だけの反転で、局面には一切効かない。** 反転するのは
  // `<shogi-board>` の絵（`flip` 属性）・駒台の置き場所と向き・盤に重ねる
  // グリッドの読み替えだけで、**Go 側は視点を知らない**。
  //
  // ⚠️ **モデルは反転しない。** SFEN も先後も手番も指す手（USI）もそのままで、
  // エンジンに渡すものも変わらない。`_docs/design-position.md` の「視点」（＝撮った画像と盤面が一致していること。学習データのラベルは
  // 画素と一致していなければならない）は**そのまま生きている**。
  //
  // ⚠️ **そのぶん見え方とモデルが反対になる。** 読み替えは
  // `editor.ts` / `study.ts` の `applyFlip`（見た目の位置 → 局面のマス）と、
  // ここの CSS クラス（駒台の置き場所と駒の向き）の 2 か所だけに閉じてある。
  // **他の場所で「反転しているなら…」と分岐を足さないこと。**
  //
  // ⚠️ **反転すると座標の表示も変わる**（筋が左から 1・2・…、段が下から
  // 一・二・… になるので **左下が 1一**）。盤の絵の座標は core/web が、
  // マスのツールチップは局面座標から作る側が、それぞれ勝手に付いてくる。
  //
  // **その場かぎりの値**（config.json には持たない）。起動のたびに
  // 「手前が先手」で始まる —— 中継の原則がそちらで、切り替えは
  // 連続モードのチェックと同じくその場の操作だから。
  //
  // ⚠️ **訂正タブと解析タブで 1 つの値。** 片方だけ反転していると、
  // 採った局面が上下逆に出てきて何が起きたのか分からなくなる
  // （値そのものは `showStudyHand` より前で宣言してある）。
  const studyBoardWithHands = root.querySelector<HTMLElement>("#study-board-with-hands")!;

  // paintFlipButton はボタン 1 つぶんの見た目（文字・ツールチップ・押下状態）。
  const paintFlipButton = (b: HTMLButtonElement, flip: boolean) => {
    b.textContent = flip ? "手前: 後手" : "手前: 先手";
    b.title = flip
      ? "手前が後手（先手が奥）。押すと手前が先手に戻ります。盤の向きが変わるだけで、局面は変わりません"
      : "手前が先手（後手が奥）。押すと手前が後手になります。盤の向きが変わるだけで、局面は変わりません";
    b.setAttribute("aria-pressed", String(flip));
  };

  // ⚠️ **訂正タブは反転しない**（2026-08-18 にボタンごと外した）。ここに
  // `board.toggleAttribute("flip", ...)` を戻さないこと —— 盤は撮ったとおりで固定する。
  const applyViewpoint = () => {
    studyBoard.toggleAttribute("flip", studyFlipped);
    studyBoardWithHands.classList.toggle("is-flipped", studyFlipped);
    studyBoardUI.setFlip(studyFlipped);
    // 駒台は「逆順に並べる側」が入れ替わるので並べ直す。
    showStudyHand(studyHands);
    paintFlipButton(studyFlip, studyFlipped);
  };

  // setStudyViewpoint は**解析タブの**視点を決める。**「自分がどちら側か」を渡す**
  // （新規対局の「あなたの手番」と、採ったときの「撮った盤の目線」がここに入る）。
  const setStudyViewpoint = (black: boolean) => {
    if (studyFlipped === !black) {
      return;
    }
    studyFlipped = !black;
    applyViewpoint();
  };

  // 反転中なら「手前が先手」に戻し、そうでなければ「手前が後手」にする。
  studyFlip.addEventListener("click", () => setStudyViewpoint(studyFlipped));
  // ⚠️ **一度は通すこと。** ボタンの文字（「手前: 先手」）はここで入れているので、
  // 通さないとラベルが空のボタンが出る。
  applyViewpoint();

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
      followUI.publish();
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
    // エンジンの色（評価値グラフ・見出しの色見本）。**設定が唯一の出所**で、
    // 既定色の解決も Go 側が済ませてある（`EngineSettings.Color` は常に入っている）。
    // ⚠️ **評価値グラフの置き場所も設定から受け取る**（2026-09-08）。
    // Go 側は起動時にも `graph:detached` を出すが、**その時点でフロントはまだ
    // 購読していない**ので、起動直後の形はここで決まる。
    layout.applyGraphDetached(!!s.evalGraphDetached);
    layout.applySideDetached(!!s.studyPaneDetached);
    layout.applyMovesDetached(!!s.movePaneDetached);
    // ⚠️ **評価値グラフの縦軸も設定から受け取る**（2026-09-13）。切り替えるのは
    // グラフの見出しだが、**起動直後にどちらで始まるかはここで決まる**
    // （切り離した窓で切り替えたときも `settings:changed` でここを通る）。
    evalGraphUI.setMode(s.evalGraphAxis as EvalMode);
    // ⚠️ **盤の上の 1 行の出し入れも設定から受け取る**（2026-09-10）。切り替えは
    // 黒地の右クリックだが、**起動直後にどちらで始まるかはここで決まる。**
    winrateHidden = !!s.hideWinRateBar;
    playersHidden = !!s.hidePlayerNames;
    applyStudyTopRow();
    winrateFlipped = !!s.flipWinRateBar;
    applyWinRateFlip();
    // ⚠️ **評価値グラフの折れ線の色はここが持つ**（側の列とは別）。
    // 切り離すと側の列は別の窓に居るので、**あちらから引けない**。
    graphColors.clear();
    for (const e of s.engines ?? []) {
      graphColors.set(e.id, e.color);
    }
    // ⚠️ **側の列へは押し込むこと**（色・名前・候補手の本数・考える秒数）。
    // **設定が唯一の出所**で、既定色の解決も Go 側が済ませてある。
    sidePane.setEngines(s.engines ?? [], s.engineColors ?? [], s.analyzeSeconds, s.batchSeconds);
    // 棋譜の自動更新の間隔（設定ファイルだけで決まる。既定の解決は Go 側）。
    sidePane.setKifuFollowMinutes(s.kifuFollowMinutes);
    // 手順の下のコメント欄（2026-10-04）。**起動直後にどちらで始まるかはここで決まる。**
    sidePane.setShowComment(!!s.showMoveComment);
    // 送信ボタンを出すかどうかはこの設定で決まる。
    trainUI.setEnabled(s.training.enabled);
    trainUI.sync();
  };
  const settingsUI = mountSettingsTab(tabs[4].panel, {
    pieceBoards: [board, studyBoard],
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
