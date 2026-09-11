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
//   設定   … 設定。**5 つの区切りに分けてある**(2026-09-12。撮る / 解析 / 盤の表示 /
//            ファイルの場所 / 盤面認識(suteme))。⚠️ **足した順ではなく関わりでまとめる。**
//            ⚠️ **並びの基準は「どれだけの人が触るか」** —— よく触るものが上、
//            **既定のままで動くものは下**。⚠️ **盤面認識をベース機能だからと上へ
//            戻さないこと**(認識器の置き場所や学習データへの登録は相当な上級者の操作)。
//            ⚠️ **認識器の読み込み元と「訂正盤面を suteme に登録する」を離さないこと**
//            (相手が同じ suteme)。項目を足すときはこの 5 つのどれかに入れる。
//            詳しくは `_docs/ui/screens.md`
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
// Go 側が枠の外へ逃がす(captureservice.go の placeMainBesideFrame)。
import { Clipboard, Events, Window } from "@wailsio/runtime";
import {
  FiChevronDown,
  FiChevronLeft,
  FiChevronRight,
  FiChevronUp,
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
  FontService,
  KifuService,
  SettingsService,
  StudyService,
  TrainingService,
} from "../bindings/github.com/ShinteLab/ikkyoku/app";
import { iconMarkup } from "./icon";
import { mountEditor } from "./editor";
import { mountLibrary } from "./library";
import { mountFetchCards } from "./fetchcards";
import { mountEvalPane } from "./evalgraphpane";
import { mountSidePane, type EngineScore } from "./sidepane";
import { openPopup } from "./popup";
import { mountStudyBoard } from "./study";
import type { RecognizerStatus } from "../bindings/ikkyoku/models";
import type {
  AppSettings,
  EngineSettings,
  FontChoice,
  FontState,
  GameSummary,
  KifuDBStatus,
  KifuLoad,
  StudyState,
} from "../bindings/github.com/ShinteLab/ikkyoku/app/models";
import type { EngineColorOption } from "../bindings/github.com/ShinteLab/ikkyoku/models";
import type { Stock } from "../bindings/github.com/ShinteLab/ikkyoku/position/models";
// 認識の観測情報。**型を手で書き写さない**(Go 側は suteme の型をそのまま通しており、
// ここで別に定義すると矩形の意味がずれても気づけない)。
import type { Debug } from "../bindings/github.com/ShinteLab/suteme";

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
}

interface CaptureResult {
  path: string;
  width: number;
  height: number;
  thumbnail: string;
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

// 撮った PNG の座標系での盤面矩形（`Debug.Region` から `ImageBounds.Min` を引いたもの）。
// **使い道は 2 つ**——suteme へ送る学習サンプルの切り出し位置と、訂正タブの参照画像の
// 切り取り。**同じ 1 つの値から両方を出すこと**（別々に持つと片方だけずれる）。
type ShotRegion = { x1: number; y1: number; x2: number; y2: number };

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

// registerFontFace は data URL のフォントをドキュメントに登録する（`<style>` 1 枚）。
//
// **同じ id を渡せば中身ごと差し替わる**（前の `@font-face` は消える）ので、
// 家族名を使い回さない限り取り違えは起きない。
function registerFontFace(id: string, family: string, dataUrl: string): void {
  let style = document.getElementById(id) as HTMLStyleElement | null;
  if (!style) {
    style = document.createElement("style");
    style.id = id;
    document.head.appendChild(style);
  }
  style.textContent =
    `@font-face { font-family: "${family}"; src: url("${dataUrl}") format("truetype"); }`;
}

// applyPieceFont は設定の「駒の字」で選んだフォントを画面に当てる
// （**端末に入っているフォントから焼いた駒の字**。Go 側の `FontService`）。
//
// ⚠️ **当て方は `--shogi-font` ただ 1 つ。** `<shogi-board>`（core/web）も、
// 駒台の駒も、掴んだ駒の絵も、候補手の重ね表示も、この変数を見ている
// （style.css）。**要素ごとに font-family を書かないこと** —— 1 か所でも
// 直し忘れると、そこだけ書体が食い違う。
//
// ⚠️ **同梱の ShogiSFEN は上書きしない**（`registerBoardFont` が登録したまま）。
// 同名の family を二重に登録すると**どちらが当たるかがブラウザ任せ**になり、
// 同梱に戻せなくなる。だから family は登録ごとに違う名前になっている
// （Go 側の `ikkyoku.PieceFontFamily`）。
function applyPieceFont(face: { family: string; dataUrl: string } | null): void {
  const id = "shinte-piece-font";
  if (!face) {
    // 同梱に戻す。**変数を消すだけ**で、CSS の既定値（"ShogiSFEN"）に落ちる。
    document.getElementById(id)?.remove();
    document.documentElement.style.removeProperty("--shogi-font");
    return;
  }
  registerFontFace(id, face.family, face.dataUrl);
  // ⚠️ **引用符ごと入れること**（値は CSS の font-family。core/web の README）。
  document.documentElement.style.setProperty("--shogi-font", `"${face.family}"`);
}

// applyPieceStyle は「王/玉」「馬/左馬」を画面に当てる。
//
// 中身は**駒フォントの stylistic set**（`ss01` = 王→玉 / `ss02` = 馬→左馬）。
// ⚠️ **`K` と `k` はフォント上で同じグリフ**なので、盤全体にまとめて当てると
// **先後を分けられない。** そのため当て方が 2 通りある:
//
//   盤（`<shogi-board>`）    … `gyoku` / `hidari-uma` 属性。core/web が駒 1 つずつに当てる
//   ikkyoku が自分で描く駒  … 先手用・後手用の 2 つを CSS 変数で配る（style.css）
//
// 後者に当たるのは**「足りない駒」の王**と**掴んだ駒の絵**（駒台に王も馬も
// 出てこないので、そこは実質効かない）。⚠️ **どちらか片方だけ直すと、
// 「盤は玉なのに掴むと王」という見ないと分からない食い違いが出る。**
//
// ⚠️ **組み立ては Go 側**（`pieceStyle`）。`font-feature-settings` は
// **個別の値が積み上がらない**（後から当てた宣言が丸ごと勝つ）ので玉と左馬を
// 1 つの値にまとめる必要があり、**その判断を 2 か所に持たない。**
function applyPieceStyle(
  style: {
    boardGyoku: string;
    boardGyokuOn: boolean;
    boardHidariUma: boolean;
    black: string;
    white: string;
    ink: string;
  },
  boards: Element[],
): void {
  const root = document.documentElement.style;
  root.setProperty("--piece-features-black", style.black);
  root.setProperty("--piece-features-white", style.white);
  // 駒の字の色。⚠️ **濃さ込みの 1 つの値**（Go 側の `PieceInk` が合成する）。
  // 盤（core/web の `--shogi-piece-color`）と、ikkyoku が HTML で描く駒
  // （駒台のチップ・掴んだ駒の絵）の**両方が同じ値を見る** ——
  // 濃さを別に当てると、HTML 側は element の `opacity` になり
  // **駒の背景（木地）ごと透ける。**
  root.setProperty("--shogi-piece-color", style.ink);
  for (const b of boards) {
    // ⚠️ **属性そのものを外すこと。** 値を空にして残すと「値なし」＝
    // **先後とも玉**になる（`core/web/README.md` の表）。
    if (style.boardGyokuOn) {
      b.setAttribute("gyoku", style.boardGyoku);
    } else {
      b.removeAttribute("gyoku");
    }
    // ⚠️ **左馬は有無だけ**（値を書いても見られない。盤全体に効く）。
    if (style.boardHidariUma) {
      b.setAttribute("hidari-uma", "");
    } else {
      b.removeAttribute("hidari-uma");
    }
  }
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
        <!-- 何もないところから始める（2026-08-13）。**3 つめの入力の口。**
             行き先は棋譜と同じ**解析タブ**で、訂正タブは通らない
             （初期局面は手合割で一意に決まるので、直すものが無い）。

             ⚠️ **初期局面をここに書かない。** 手合割 → 盤面は将棋の**仕様**なので
             core/kifu.StartSFEN の 1 か所だけが持つ（Go 側の position.NewGame
             がそれを引く）。フロントに SFEN を書き写すと、棋譜から読んだ平手と
             新規で作った平手が食い違いうる。

             ⚠️ **手合割は select にしてある。** 今出しているのは平手だけだが、
             駒落ちは Go 側（core/kifu）が既に全部持っているので、**option を
             足すだけで通る**。詰将棋は初期局面が無い（人が並べる）ので別の口になる
             —— そちらは訂正タブ側の話で、ここには並ばない。

             ⚠️ **「あなたの手番」が今決めているのは視点（画面の向き）だけ。**
             平手の初期局面はどちらを持っても同じなので、局面には効かない。
             対局モード（片側を人、もう片側をエンジンが指す）を入れる段になったら、
             この選択がそのまま「自分の側」になる。 -->
        <div class="setting-group">
          <span class="setting-title">新しく対局を始める</span>
          <span class="setting-note">
            初期局面から始めます。<strong>解析タブ</strong>が開いて、
            盤の駒を押せばそのまま手を進められます。
            <strong>あなたの手番に選んだ側が手前に来ます</strong>
            （盤の向きが変わるだけで、局面は変わりません）。
          </span>
          <div class="setting-fields">
            <span class="field-label">手合割</span>
            <select id="newgame-handicap" title="今は平手だけです（駒落ち・詰将棋はこれから）">
              <option value="平手" selected>平手</option>
            </select>
            <span class="field-label">あなたの手番</span>
            <div class="turn-group" role="group" aria-label="あなたの手番">
              <button id="newgame-black" class="turn-btn is-active" type="button"
                      data-side="black">先手</button>
              <button id="newgame-white" class="turn-btn" type="button"
                      data-side="white">後手</button>
            </div>
            <button id="newgame-start" class="ghost-btn is-primary" type="button">対局を始める</button>
          </div>
          <p id="newgame-status" class="status" role="status" aria-live="polite" hidden></p>
        </div>

        <!-- ⚠️ **ボタンはここに戻さないこと**（2026-08-18）。枠を出すのは
             **タイトルバーのトグル**、盤に合わせるのは**枠のツールバーの □**。
             どちらも「枠を出してから枠に対してやること」なので、
             入力タブに置くとタブを行き来することになる。ここに残すのは案内だけ。 -->
        <div class="setting-group">
          <span class="setting-title">画面から撮る</span>
          <span class="setting-note">
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
        <p id="status" class="status" role="status" aria-live="polite">
          ガイド枠を盤面に合わせて撮影してください。
        </p>

        <!-- 棋譜を貼り付ける。**画像を通らない 2 つめの入口**なので、行き先も違う
             （撮影は認識を通るので訂正タブ、棋譜は既に確定しているので解析タブ）。 -->
        <div class="setting-group">
          <span class="setting-title">棋譜を貼り付ける</span>
          <span class="setting-note">
            KIF 形式の棋譜を貼るか、<strong>.kif の URL</strong> を入れて読み込むと、
            <strong>指し手を全て反映した局面</strong>で<strong>解析タブ</strong>が開きます。
            手順は盤の右に並ぶので、押せばその局面まで戻れます。
            <strong>訂正タブは通りません</strong>（棋譜の局面は初期局面と手順で決まるため）。
            <strong>「棚に登録する」を押すと棋譜タブに残ります</strong>
            （読み込むだけでは残りません）。
          </span>
          <!-- URL から取る（2026-08-12）。日本将棋連盟の棋譜中継のように
               .kif を直に配っているところなら、貼り付けと同じ扱いで読める。
               ⚠️ **取得も文字コードの判別も Go 側**（ikkyoku/kifuweb）。
               webview の fetch では CORS で弾かれるうえ、
               **中継の .kif は Shift_JIS** なので、いずれにせよこちらでは扱えない。 -->
          <div class="setting-fields kifu-url-row">
            <span class="field-label">URL</span>
            <input id="kifu-url" type="url" spellcheck="false"
                   placeholder="http://live.shogi.or.jp/.../oui202607290101.kif" />
            <button id="kifu-load-url" class="ghost-btn" type="button">URL から読み込む</button>
            <button id="kifu-import-url" class="ghost-btn" type="button"
                    title="この URL の棋譜を棚（棋譜タブ）に登録します">棚に登録する</button>
          </div>
          <textarea id="kifu-text" class="kifu-text" spellcheck="false"
                    placeholder="手数----指手---------消費時間--&#10;   1 ７六歩(77)   ( 0:16/00:00:16)&#10;   2 ３四歩(33)   ( 0:04/00:00:04)"></textarea>
          <!-- ⚠️ **入力欄を 2 つに増やさない。** 同じ入力の**行き先が 2 つある**
               だけなので、欄を分けると「どちらに貼ったか」で挙動が変わる面になる。

               ⚠️ **二系統を残してある**（2026-09-04）——「読み込む」は棚に入らず
               解析タブへ直行し、「棚に登録する」は棚へ入れるだけで解析タブを触らない。
               **棚は解析の前提条件ではない**（設計原則3）。 -->
          <div class="setting-fields">
            <button id="kifu-load" class="ghost-btn" type="button">読み込む</button>
            <button id="kifu-import" class="ghost-btn" type="button"
                    title="貼り付けた棋譜を棚（棋譜タブ）に登録します">棚に登録する</button>
            <button id="kifu-clear" class="ghost-btn" type="button">消す</button>
          </div>
          <p id="kifu-status" class="status" role="status" aria-live="polite" hidden></p>
        </div>

        <!-- 中継から取得する（kicho の「取得」タブ）。**対局中の棋譜を追う口。**

             ⚠️ **上の「棋譜を貼り付ける」とは扱いが違う。** あちらは終局後の
             .kif を単発で取り込む口で、こちらは**取得元での一意な ID がある**ので
             取り直しても同じ棋譜として更新される（棚でも増えない）。
             だから**カードとして積んで「更新」で取り直す**形にしてある。

             ⚠️ **日本将棋連盟の中継はこちらで扱うこと。** 上の URL 欄から入れると
             source_id が毎回 UUID になり、取り込むたびに別の棋譜として増える。 -->
        <div class="setting-group">
          <span class="setting-title">中継から取得する</span>
          <span class="setting-note">
            <strong>読売（竜王戦）</strong>の対局ページと
            <strong>日本将棋連盟の棋譜中継</strong>から取れます。URL か棋譜 ID を入れてください。
            <strong>取得しただけでは棚に入りません</strong>（カードの「この内容を保存」で入ります）。
            対局中は棋譜が伸びるので、「更新」で取り直してから保存し直してください。
          </span>
          <!-- ⚠️ **カードは再起動しても残る**（仮の一覧 / kicho の watches）。
               2 日制の対局で翌日また URL を貼り直さずに済ませるためのもので、
               **覚えているのは「どのサイトのどの棋譜か」だけ**（棋譜そのものではない）。 -->
          <span class="setting-note">
            <strong>カードは再起動しても残ります。</strong>2 日制の対局で翌日また URL を
            貼り直さずに済むよう、「どのサイトのどの棋譜か」を覚えておきます
            （<strong>棋譜そのものではありません</strong>）。復元したカードは中身が空なので
            「更新」でサイトから取り直してください。追うのをやめるときはカードの「閉じる」で外します
            （<strong>保存済みの棋譜は消えません</strong>）。終局した棋譜を保存したときは自動で外れます。
          </span>
          <div class="setting-fields">
            <span class="field-label">URL / 棋譜 ID</span>
            <input id="fetch-input" type="text" spellcheck="false"
                   placeholder="http://live.shogi.or.jp/oui/kifu/67/oui202607290101.html" />
            <button id="fetch-run" class="ghost-btn is-primary" type="button">取得</button>
            <button id="fetch-refresh-all" class="ghost-btn" type="button"
                    title="並んでいるカードを順にサイトから取り直します（復元した直後に使います）">すべて更新</button>
            <button id="fetch-clear" class="ghost-btn" type="button"
                    title="入力とカードをすべて捨てます（仮の一覧も空にします。棚の棋譜は消えません）">クリア</button>
          </div>
          <p id="fetch-status" class="status" role="status" aria-live="polite" hidden></p>
          <!-- 取得結果。**新しいものが先頭。** 同じ棋譜を取り直したときは
               カードを増やさず中身だけ差し替える（key = 取得元:棋譜 ID）。
               ⚠️ **起動時は仮の一覧（DB）から復元する**（サイトへは取りに行かない）。 -->
          <div id="fetch-cards" class="fetch-cards"></div>
        </div>
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
          <button id="library-reload" class="ghost-btn" type="button"
                  title="棚を読み直します">再読み込み</button>
        </div>

        <!-- 検索。⚠️ **打つたびには検索しない**（Enter か「検索」で実行）。
             件数が増えると 3 文字未満は全件走査になるため。 -->
        <div class="library-search">
          <input id="library-text" class="library-text" type="search" spellcheck="false"
                 placeholder="棋戦名・対局者・場所で検索" />
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
        <p id="library-hint" class="setting-note" hidden></p>
        <p id="library-status" class="status" role="status" aria-live="polite" hidden></p>

        <div class="library-table-wrap">
          <table class="library-table">
            <thead>
              <tr>
                <th>開始日</th>
                <th>棋戦</th>
                <th>先手</th>
                <th>後手</th>
                <th class="is-num">手数</th>
                <th></th>
              </tr>
            </thead>
            <tbody id="library-rows"></tbody>
          </table>
        </div>

        <!-- 詳細。**KIF の原本をそのまま出す**（整形し直さない）。 -->
        <div id="library-preview" class="library-preview" hidden>
          <div class="library-head">
            <span id="library-preview-title" class="field-label"></span>
            <button id="library-preview-close" class="ghost-btn" type="button">閉じる</button>
          </div>
          <dl id="library-preview-meta" class="library-meta"></dl>
          <pre id="library-preview-kif" class="library-kif"></pre>
        </div>
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

               ⚠️ **認識が使った盤面領域(Debug.Region)で切り取って出す**(2026-08-11)。
               画面いっぱいに広げたときに盤が小さすぎて 1 マスずつ見比べられないのと、
               **盤面領域を誤認識していても気づけない**ため。切り取ると、ずれていれば
               「盤が欠けた画像」として一目で分かる。**領域が無いときは全体を出す**
               (撮った 1 枚を見せないより良い。設計原則3)。

               ⚠️ **出すのは画像そのものだけ。** 重ね表示・信頼度・保存先は
               「認識がどれくらい外したか」の情報なので「認識詳細情報」に残す
               (盤面タブの基準は「局面を読む・直す・動かすのに要るか」)。
               もとの画像は「認識詳細情報」と**同じ CaptureResult.thumbnail**。 -->
          <div id="capture-ref" class="capture-ref" hidden>
            <img id="capture-ref-img" class="capture-ref-img" alt="訂正のもとになった画像" />
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
             盤から離すと何に対する設定なのか分からなくなる。 -->
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
                 どちら側が手前かはボタンの文字が言っている。 -->
            <button id="study-flip" class="ghost-btn view-flip" type="button" hidden></button>
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
            <span class="field-label">評価値</span>
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
               title="押すとその局面に戻ります（手順は消えません）。横にドラッグするとその範囲に絞ります"></div>
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
          <div class="veil-box">
            <p id="batch-veil-note" class="veil-note">連続解析中…</p>
            <button id="batch-veil-cancel" class="veil-btn" type="button">解析をキャンセル</button>
          </div>
        </div>
      </div>

      <div id="panel-settings" class="panel" role="tabpanel" aria-labelledby="tab-settings" hidden>
        <h3 class="setting-section">撮る</h3>
        <label class="setting">
          <input id="fit-on-startup" type="checkbox" />
          <span class="setting-body">
            <span class="setting-title">起動時に盤面を探す</span>
            <span class="setting-note">
              起動のたびに一度だけ画面から盤を探して、ガイド枠を合わせます。
              見つからなければ枠はそのままです。
            </span>
          </span>
        </label>

        <!-- 枠の内側で後ろの画面を操作する（2026-08-19）。

             枠は中継の「上」に重ねる最前面のウィンドウなので、合わせたあとは
             **その下にある中継のシークバーや再生ボタンが押せない**（押すには
             枠をどかすしかなく、どかすと位置合わせがやり直しになる）。

             ⚠️ **素通しになるのは「撮る範囲の内側」だけ。** ツールバーと縁は
             押せるまま残す —— 枠ごと素通しにすると、この設定を切るまで
             枠を動かすことも撮ることもできなくなる。 -->
        <label class="setting">
          <input id="click-through" type="checkbox" />
          <span class="setting-body">
            <span class="setting-title">枠の内側で後ろの画面を操作する</span>
            <span class="setting-note">
              ガイド枠の内側（撮る範囲）のクリックとホイールを、後ろにある中継の画面へ
              そのまま通します。枠をどかさずにシークや再生ができます。
              ツールバーと枠の縁は今までどおり押せます（移動・リサイズ・撮影）。
              Windows のみ。
            </span>
          </span>
        </label>

        <h3 class="setting-section">解析</h3>
        <!-- 解析エンジン。**繋ぎ先は「USI を話すプロセス」なら何でもよい**
             （やねうら王・水匠・prokishi.exe・同梱のエンジン）。検討ツールとして
             実用になるかは繋ぐエンジンの棋力で決まるので、ここで差し替えられる。

             ⚠️ **1 つに絞らない**（2026-08-11）。**複数登録でき、「解析に使う」を
             付けたものが同時に走る。** どのエンジンが正しいかは局面によって違うので、
             評価が食い違うところを並べて読めることに意味がある。

             ⚠️ **「外部を使う」のチェックボックスは置かない。** パスが空なら同梱、
             入っていれば外部。2 つ持つと「パスが入っているのに無効」という
             食い違いが起きる。 -->
        <details id="fold-engine" class="setting-group setting-fold">
          <summary class="setting-fold-head">
            <span class="setting-title">解析エンジン</span>
            <span id="fold-engine-sum" class="setting-fold-sum"></span>
          </summary>
          <span class="setting-note">
            USI を話すエンジンの実行ファイルを登録します（やねうら王・水匠など）。
            <strong>「解析に使う」を付けたエンジンが同時に走り、解析タブに結果が並びます。</strong>
            実行ファイルを<strong>空にするとその登録は同梱のエンジン</strong>になります。
            同じエンジンを <code>setoption</code> 違いで 2 つ登録して比べることもできます。
            <strong>「接続を確認」を押すと、そのエンジンの設定項目（option）を読み込んで
            各行の「エンジンの設定」から変えられるようになります。</strong>
            変えた値は次の解析から送られます。
          </span>
          <ul id="engine-list" class="engine-list"></ul>
          <div class="setting-fields">
            <button id="engine-add" class="ghost-btn" type="button"
                    title="実行ファイルを選んで登録します">エンジンを追加…</button>
            <button id="engine-add-builtin" class="ghost-btn" type="button"
                    title="同梱のエンジンを登録します">同梱エンジンを追加</button>
          </div>
          <p id="engine-status" class="status" role="status" aria-live="polite"></p>
        </details>

        <!-- 勝率バーの変換に使う定数（解析タブ。2026-08-12）。
             **評価値の尺度はエンジンによって違う**ので、ここで合わせられるように
             してある（⚠️ 自作エンジンの評価値の絶対値は当てにならない）。
             ⚠️ **既定値（1500）は Go 側が解決して返す。フロントに書かないこと。** -->
        <div class="setting-group">
          <span class="setting-title">勝率の表示</span>
          <span class="setting-note">
            解析タブの盤の上に出る勝率バーの計算に使います。
            <code>勝率(先手) = 1 / (1 + exp(-評価値 / ポナンザ定数))</code>。
            <strong>小さくするほど、同じ評価値でも勝率が振り切れます。</strong>
            空欄にすると既定に戻ります。
          </span>
          <div class="setting-fields">
            <label class="field">
              <span class="field-label">ポナンザ定数</span>
              <input id="ponanza-constant" class="port" type="number" min="1" max="100000"
                     step="10" title="評価値を勝率に直すときの定数（既定 1500）" />
            </label>
          </div>
        </div>

        <h3 class="setting-section">盤の表示</h3>
        <!-- 駒の字（2026-08-16）。**端末に入っているフォントから駒の字を焼いて使う。**

             同梱できる駒フォントは「派生物の作成と再配布を認める」ライセンスの
             ものに限られる（core/web/README.md。游明朝・どへた・桜鯰は実際に外している）。
             一方**自分の端末に入っているフォントを、自分の端末で表示に使うのは
             再配布ではない**、というのがこの機能の拠り所。

             ⚠️ **焼いた字を書き出す口を作らないこと**（Go 側にも無い）。
             書き出せると「その端末で表示する」を越えてしまい、元フォントの
             条項が効く側の話になる。 -->
        <details id="fold-piecefont" class="setting-group setting-fold">
          <summary class="setting-fold-head">
            <span class="setting-title">駒の字</span>
            <span id="fold-piecefont-sum" class="setting-fold-sum"></span>
          </summary>
          <span class="setting-note">
            盤に並ぶ駒の書体です。端末に入っているフォントから、駒に要る
            <code id="font-required"></code> の字だけを抜き出して使います。
            <strong>抜き出した字はこのアプリの表示に使うだけで、ファイルとしては
            保存も配布もされません。</strong>
            <strong>元フォントの利用条件はそのまま効きます</strong>ので、
            作った盤面を配ったり素材として使ったりするときは、そちらを確認してください。
          </span>
          <ul id="font-list" class="engine-list"></ul>

          <!-- 王/玉と馬/左馬（2026-08-16）。**どのフォントでも効く**
               （駒フォントは同じ生成器で焼いているので ss01/ss02 が必ず入っている）。

               ⚠️ **先後を分けられるのは玉だけ。** 玉は王将/玉将という**駒そのものの
               呼び分け**（上位者が王）なので片側だけがありうるが、左馬は**盤の
               見た目の選択**なので、使うと決めたら盤全体がそうなる。
               **左馬に先後の欄を足さないこと**（core/web/README.md）。 -->
          <div class="setting-fields">
            <label class="field">
              <span class="field-label">王 / 玉</span>
              <select id="font-gyoku"
                      title="王を玉で書くか。先手だけ・後手だけも選べます"></select>
            </label>
            <label class="setting is-inline">
              <input id="font-hidari-uma" type="checkbox" />
              <span class="setting-body">
                <span class="setting-title">馬を左馬にする</span>
              </span>
            </label>
          </div>

          <!-- 駒の字の色と濃さ（2026-08-16）。**字の強いフォント（太い明朝・毛筆）は
               少し薄いほうが盤に映える。** 端末のフォントを選べるようにした以上、
               書体ごとに濃さを合わせたくなる。

               ⚠️ **画面では別々の欄だが、当てるのは 1 つの値**（Go 側が rgba に
               合成する）。濃さを別に当てると、HTML で描いている駒台のチップは
               element の opacity になり**木地ごと透ける**。 -->
          <div class="setting-fields">
            <label class="field">
              <span class="field-label">字の色</span>
              <input id="font-ink-color" type="color"
                     title="駒の字の色（盤・駒台・掴んだ駒に効きます）" />
            </label>
            <label class="field">
              <span class="field-label">濃さ</span>
              <input id="font-ink-opacity" class="ink-range" type="range"
                     min="20" max="100" step="5"
                     title="駒の字の濃さ。字の強いフォントは少し薄いほうが盤に映えます" />
              <output id="font-ink-opacity-value" class="ink-value"></output>
            </label>
            <button id="font-ink-reset" class="ghost-btn" type="button"
                    title="字の色と濃さを既定に戻します">既定に戻す</button>
          </div>

          <div class="setting-fields">
            <button id="font-scan" class="ghost-btn" type="button"
                    title="端末に入っているフォントを探します（数秒かかります）">フォントを追加…</button>
          </div>
          <p id="font-status" class="status" role="status" aria-live="polite"></p>

          <!-- 端末のフォントの一覧。**押したときだけ探す**（実測で 190 ファイル・
               577MB を読んで 1 秒弱）。起動のたびに走らせる類の処理ではない。 -->
          <div id="font-picker" class="font-picker" hidden>
            <div class="setting-fields">
              <label class="field">
                <span class="field-label">絞り込み</span>
                <input id="font-filter" type="search" placeholder="名前・ファイル名"
                       spellcheck="false" autocomplete="off" />
              </label>
              <!-- ⚠️ **既定は切**（＝全部出す）。駒の字が無いフォントを消してしまうと、
                   **探しているのか対象外なのかが画面から分からない。** -->
              <label class="setting is-inline">
                <input id="font-only-usable" type="checkbox" />
                <span class="setting-body">
                  <span class="setting-title">駒の字が揃うものだけ</span>
                </span>
              </label>
            </div>
            <!-- 選んだ行の見本。**実際に焼いてから当てる**ので、盤に出る字そのもの。 -->
            <p id="font-sample" class="font-sample" hidden></p>
            <ul id="font-choices" class="font-choices"></ul>
            <!-- 探した場所。**目当てのフォントが出てこないときに、どこを見たのかが
                 分からないと打つ手が無い。** -->
            <p id="font-dirs" class="setting-path"></p>
          </div>
        </details>

        <h3 class="setting-section">ファイルの場所</h3>
        <p class="setting-section-note">
          <strong>既定のままで動きます。</strong>置き場所を変えたいときだけ触ってください。
        </p>
        <!-- 棋譜データベース（棚）。**実装は kicho のままで、ikkyoku は利用する側。**

             ⚠️ **棚は解析の前提条件ではない**（設計原則3）。開けなくても
             撮った 1 局面と貼った棋譜の解析は今までどおり動き、棋譜タブだけが
             理由を出して機能しない。**ここでエラーを赤く出しても、他の機能は
             壊れていないことが分かるように書くこと。** -->
        <div class="setting-group">
          <div class="setting is-block">
            <span class="setting-body">
              <span class="setting-title">棋譜データベース</span>
              <span class="setting-note">
                棋譜タブの「棚」を置くファイルです。変えるとその場で開き直します。
                <strong>開けなくても撮影・訂正・解析はそのまま使えます</strong>
                （棋譜タブだけが使えなくなります）。
              </span>
            </span>
          </div>
          <div class="setting-fields">
            <label class="field is-wide">
              <span class="field-label">場所</span>
              <input id="kifudb-path" type="text" spellcheck="false"
                     placeholder="(空なら既定の場所)" />
            </label>
            <button id="kifudb-browse" type="button">参照…</button>
          </div>
          <p id="kifudb-note" class="setting-note"></p>
        </div>

        <h3 class="setting-section">盤面認識（suteme）</h3>
        <p class="setting-section-note">
          認識に使う学習データと、訂正した局面の戻し先。<strong>どちらも既定のままで
          動きます</strong>ので、<strong>認識の精度を自分で育てるとき</strong>だけ
          触ってください。相手はどちらも suteme なので、片方だけ設定しても噛み合いません。
        </p>
        <!-- 認識器の読み込み元（2026-08-27）。

             **exe 1 つで配れる形と、学習データを育てながら使う形の両方が要る。**
             配布ビルド（-tags embedmodel）は認識器を焼き込んであるので、
             suteme のリポジトリが無い環境でもそのまま動く。開発中は
             ディレクトリを指しておけば、データを更新した結果がすぐ反映される。

             ⚠️ **「自動」はディレクトリ優先。** 焼き込みは固定したデータなので、
             ここが焼き込みへ倒れると**学習データを更新しても反映されない**という
             最も気づきにくい事故になる（Go 側 resolveRecognizerSource）。 -->
        <div class="setting-group">
          <div class="setting is-block">
            <span class="setting-body">
              <span class="setting-title">認識器の読み込み元</span>
              <span class="setting-note">
                盤面認識に使う suteme の学習データをどこから読むかです。
                切り替えるとその場で読み直します。
              </span>
            </span>
          </div>
          <div class="setting-fields">
            <label class="field">
              <span class="field-label">読み込み元</span>
              <select id="suteme-source">
                <option value="auto">自動（ディレクトリ優先）</option>
                <option value="dir">学習データのディレクトリ</option>
                <option value="embed">このアプリに焼き込んだデータ</option>
              </select>
            </label>
            <label class="field is-wide">
              <span class="field-label">ディレクトリ</span>
              <input id="suteme-data-dir" type="text" spellcheck="false"
                     placeholder="(空なら suteme 既定の探索)" />
            </label>
          </div>
          <p id="suteme-source-note" class="setting-note"></p>
        </div>

        <!-- 訂正結果を suteme の学習データに戻す設定。**自動送信のスイッチではない**
             (2026-08-07 の決定: 自動で送ると、人が直した 1 マス以外は推論結果のまま
             なので自分の出力を正解として食う)。ここで有効にすると、確定した局面ごとに
             「訂正データを送信」が出るだけ。 -->
        <div class="setting-group">
          <label class="setting">
            <input id="train-enabled" type="checkbox" />
            <span class="setting-body">
              <span class="setting-title">訂正盤面を suteme に登録する</span>
              <span class="setting-note">
                確定した盤面を suteme の学習データとして送れるようにします。
                <strong>送るのはボタンを押したときだけ</strong>で、自動では送りません。
                向こうには「未確認」として入り、suteme の解析タブで人が確認するまで
                学習には使われません。
                <strong>画面に見えていない駒を知識で補った局面は送らないでください</strong>
                （テロップで盤が隠れているときなど。ラベルが画素と一致しなくなります）。
              </span>
            </span>
          </label>
          <div class="setting-fields">
            <label class="field">
              <span class="field-label">サーバ</span>
              <input id="train-host" type="text" placeholder="127.0.0.1" spellcheck="false" />
            </label>
            <label class="field">
              <span class="field-label">ポート</span>
              <input id="train-port" class="port" type="number" min="1" max="65535" />
            </label>
            <!-- トークンは**同じマシンなら要らない**(suteme はループバックを
                 認証免除にしている)。別のマシンへ送るときだけ入れる。 -->
            <label class="field">
              <span class="field-label">トークン</span>
              <input id="train-token" type="password" placeholder="同じマシンなら不要"
                     spellcheck="false" autocomplete="off" />
            </label>
            <button id="train-check" class="ghost-btn" type="button"
                    title="suteme が登録を受け付けられる状態か確かめます">接続を確認</button>
          </div>
          <p id="train-check-status" class="status" role="status" aria-live="polite"></p>
        </div>

        <p id="settings-status" class="status" role="status" aria-live="polite"></p>
        <p class="setting-path">設定ファイル: <code id="settings-path">-</code></p>
      </div>
    </div>
  `;

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
  const status = root.querySelector<HTMLParagraphElement>("#status")!;
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
      settleStudySide();
      // **解析タブに来たら（連続モードなら）そのまま解析を始める。**
      // まだ解析していない局面のときだけ動く（止めた解析を勝手に起こし直さない）。
      // ⚠️ **切り離しているあいだは向こうが持っている**（こちらは眠っている）。
      if (sideDetached !== true) {
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
  // 出す条件は「訂正中」かつ「画像がある」の両方。撮る前と、確定したあとは畳む。
  const captureRef = root.querySelector<HTMLDivElement>("#capture-ref")!;
  const captureRefImg = root.querySelector<HTMLImageElement>("#capture-ref-img")!;
  // 画像の有無は自前で覚える。**`img.src` は空文字を入れてもページの URL に解決される**
  // ので、要素から「画像が入っているか」は読めない。
  //
  // **訂正タブに居るあいだは常に出す**（タブそのものが訂正モードなので、
  // 以前の「訂正中だけ」という条件は画像の有無だけになった）。
  let hasShot = false;
  const syncCaptureRef = () => {
    captureRef.hidden = !hasShot;
  };

  // 参照画像は**認識が使った盤面領域で切り取って**出す（2026-08-11）。
  //
  // 全体のままだと、ウィンドウを広げたときに盤が小さいままで 1 マスずつ見比べられず、
  // **盤面領域そのものを誤認識していても気づけない**（重ね表示は「認識詳細情報」の
  // 中で、畳んでいると見えない）。切り取れば、ずれていれば盤の欠けた画像として出る。
  //
  // ⚠️ **切り取りはここでやる（Go 側に 2 枚目を作らせない）。** サムネイルは既に
  // 等倍 PNG の base64 なので、切り取った画像も返すとイベントのペイロードが倍になる。
  // 重ね表示の SVG と同じ考え方で、**Go が返すのは座標だけ**。
  //
  // ⚠️ **領域が無い（認識に失敗した）ときは全体を出す。** 撮った 1 枚を見せないより
  // 良い（設計原則3）。**画像が壊れていて読めないときも同じ。**
  let captureRefSeq = 0;
  const showCaptureRefImage = (
    thumbnail: string,
    region: ShotRegion | null,
    path: string,
  ) => {
    // 撮り直しの競合よけ。切り取りは画像の読み込みを挟む非同期なので、
    // 遅れて終わった前の 1 枚が新しい画像を上書きしないようにする。
    const seq = ++captureRefSeq;
    captureRefImg.title = path;
    captureRefImg.src = thumbnail;
    if (!region) {
      return;
    }
    const img = new Image();
    img.onload = () => {
      if (seq !== captureRefSeq) {
        return;
      }
      // 画像からはみ出す座標は詰める（はみ出したまま drawImage すると空白が入る）。
      const x = Math.max(0, Math.min(region.x1, img.naturalWidth));
      const y = Math.max(0, Math.min(region.y1, img.naturalHeight));
      const w = Math.max(0, Math.min(region.x2, img.naturalWidth) - x);
      const h = Math.max(0, Math.min(region.y2, img.naturalHeight) - y);
      if (w <= 0 || h <= 0) {
        return; // 切り取れないので全体のまま
      }
      const canvas = document.createElement("canvas");
      canvas.width = w;
      canvas.height = h;
      const ctx = canvas.getContext("2d");
      if (!ctx) {
        return;
      }
      ctx.drawImage(img, x, y, w, h, 0, 0, w, h);
      captureRefImg.src = canvas.toDataURL("image/png");
    };
    img.src = thumbnail;
  };

  // ---- 訂正データの送信（suteme への還元） --------------------------------
  //
  // **確定してから出す。** 訂正の途中の盤面を送る意味が無いので、訂正モードを
  // 抜けたときだけボタンが現れる。設定で有効にしていなければ行ごと出さない。
  //
  // 送るのに要るのは 3 点組（撮った PNG のパス・正解 SFEN・**画像の中での盤面の矩形**）。
  // 矩形は認識結果の `Debug.Region` そのもの。**認識に失敗した画像は送れない**
  // （盤の位置が分からないと suteme が学習サンプルを切り出せない）。
  const trainRow = root.querySelector<HTMLDivElement>("#train-row")!;
  const trainSend = root.querySelector<HTMLButtonElement>("#train-send")!;
  const trainNote = root.querySelector<HTMLElement>("#train-note")!;
  const trainSendStatus = root.querySelector<HTMLElement>("#train-send-status")!;
  let trainEnabled = false;
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

  const syncTrain = () => {
    const ready = editLoaded && !!shotFullPath && !!lastRegion;
    trainRow.hidden = !trainEnabled || !ready;
    if (trainRow.hidden) {
      return;
    }
    // 送れる状態でも、手番や駒台の先後が未決のことはある。**止めない**
    // （学習に使うのは盤面部分なので、それでも価値がある。設計原則3）。
    // ただし**何が落ちるかは先に出す**（持ち駒が黙って落ちるのを一度やっている）。
    trainSend.disabled = false;
    trainNote.textContent = editNotes.join(" / ");
  };

  const sendTraining = async () => {
    if (!lastRegion) {
      return;
    }
    trainSend.disabled = true;
    trainSendStatus.classList.remove("is-error");
    trainSendStatus.textContent = "送信しています…";
    try {
      const r = await TrainingService.Send(
        shotFullPath,
        editSfen,
        lastRegion.x1,
        lastRegion.y1,
        lastRegion.x2,
        lastRegion.y2,
      );
      // **重複は失敗ではない**（suteme は画像のハッシュで再送を弾き、既存の ID を返す）。
      trainSendStatus.textContent = r.duplicate
        ? `この画像は登録済みです (${r.id})`
        : `登録しました (${r.id})。suteme の解析タブで確認すると学習に使われます`;
    } catch (err) {
      trainSendStatus.textContent = `登録できませんでした: ${String(err instanceof Error ? err.message : err)}`;
      trainSendStatus.classList.add("is-error");
    } finally {
      trainSend.disabled = false;
    }
  };

  trainSend.addEventListener("click", () => {
    void sendTraining();
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
  // 訂正タブ側の一行。訂正の操作が通らなかった理由と、確定できない理由を出す。
  const editStatus = root.querySelector<HTMLParagraphElement>("#edit-status")!;

  // 視点のボタン（解析タブ）。**盤のグリッドの右の列**に置いてある
  // （先手の対局者名の下・先手の駒台の上）。
  const studyFlip = root.querySelector<HTMLButtonElement>("#study-flip")!;

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
  const showPlayers = (black: string, white: string) => {
    playerNames.black.textContent = `▲${black || "先手"}`;
    playerNames.white.textContent = `△${white || "後手"}`;
    playerNames.black.title = black || "先手";
    playerNames.white.title = white || "後手";
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

  // renderWinRate は選ばれているエンジンの勝率をバーに描く。
  //
  // ⚠️ **左が後手（青）・右が先手（赤）。符号も向きもここでいじらないこと**
  // （逆に伸びると「エンジンが間違えている」としか読めない壊れ方になる）。
  // ⚠️ **出すのは 1 つだけ**（押すと切り替わる）。並べると盤の上に段が積まれて
  // そのぶん盤が小さくなるうえ、**形勢を一目で見るための帯**なので読む対象が増える。
  const renderWinRate = () => {
    const one =
      winrateScores.find((e) => e.id === winrateEngineId) ?? winrateScores[0];
    // **押して切り替えられるのは 2 つ以上あるときだけ。**
    const many = winrateScores.length > 1;
    winrateBar.classList.toggle("is-switchable", many);
    winrateBar.disabled = !many;

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
    const head = `後手 ${Math.round(white * 100)}% ／ 先手 ${Math.round(black * 100)}%`;
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
    settleStudySide();
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
    // 側の列と、その境目のバー。
    // ⚠️ **切り離しているあいだは出さないこと**（2026-09-08）。別ウィンドウに
    // 出ているので、**同じ値を 2 か所に描くことになる**。
    // ⚠️ **消えるのは「両方とも切り離したとき」だけ**（2026-09-12）。片方だけなら
    // 列は残り、**残った面がその幅を全部もらう**（`sidePane.setParts`）。
    studySide.hidden = bothDetached() || !loaded;
    // ⚠️ **縦のスプリットバーも局面があるときだけ出す。** 局面が無いときは
    // 分ける相手（解析の列）が出ていないので、バーだけが宙に浮く。
    studySplit.hidden = bothDetached() || !loaded;
    // ⚠️ **評価値グラフは切り離しているあいだ出さないこと**（2026-09-08）。
    // 別ウィンドウに出ているので、**同じ値を 2 か所に描くことになる**。
    evalGraphRow.hidden = graphDetached === true || !loaded;
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
    // ⚠️ **局面と一緒に更新する。** 根を入れ替えると対局者も変わる。
    showPlayers(st.black ?? "", st.white ?? "");
    if (!o?.fromBoard) {
      studyBoardUI.render(loaded ? st : null);
    }
    // ⚠️ **側の列は必ずここで描くこと。** `fromBoard` は「盤が自分で描いた」の
    // 意味で、**手順も候補手も描かれていない。**
    sidePane.render(loaded ? st : null);
    // 盤の周りの出し入れ（勝率バー・対局者・視点・評価値グラフ・側の列）。
    syncStudyChrome(loaded);
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
  const adoptToStudy = async () => {
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
    // ⚠️ **見え方を撮った画像に合わせる。** 後手目線で撮った局面は、解析タブへ
    // 渡るときに 180 度回っている（＝先後が実際どおりになっている）ので、
    // **そのまま描くと盤が上下逆に見える**。表示視点を後手にすれば、
    // 局面は正しいまま、見た目は撮った画像と同じ向きになる。
    // **あとから解析タブのボタンで自由に戻せる。**
    setStudyViewpoint(!editNearWhite);
    selectTab(studyTab);
  };

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
  const studyStep = (e: KeyboardEvent) => {
    const delta = e.key === "ArrowUp" ? -1 : e.key === "ArrowDown" ? 1 : 0;
    if (!delta) {
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
      if (sideDetached !== true) {
        void Events.Emit("study:busy", { on, note });
      }
    },
    // 設定を書き換えたら設定タブも描き直す（色・候補手の本数）。
    onSettings: (st) => showSettings(st),
    // ⚠️ **その面の中の同じ場所に置くもの。** ドックしているときは「切り離す」、
    // 切り離した窓では「ドックに戻す」——**同じ場所**にすること。
    analyzeAction: studySplitDetach,
    movesAction: studyMovesDetach,
  });
  // ⚠️ **幕の出口はここ**（幕は下を全部塞ぐので、側の列の「停止」も押せない）。
  batchVeilCancel.addEventListener("click", () => sidePane.cancelBatch());
  // ⚠️ **別の窓の幕からも止められること**（`study:cancel`）。評価値グラフの窓には
  // 「停止」も手順も無いので、**出口がここに繋がっていないと窓を閉じるしかない。**
  // ⚠️ **持ち主だけが応じること** —— 切り離しているあいだ連続解析を持っているのは
  // 向こうの窓で、こちらが応じても止める相手が居ない。
  Events.On("study:cancel", () => {
    if (sideDetached === true) {
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

  // ---- 評価値グラフの高さ（折り畳み + スプリットバー。2026-08-13）-----------
  //
  // ⚠️ **持っている値は「グラフの高さ」1 つだけ。** 折り畳みは**高さ 0** で表す。
  // 「畳んでいるか」の真偽値を別に持つと、**畳んでいるのに高さがある**という
  // 食い違いが起きうる（「外部エンジンを使う」の真偽値を持たないのと同じ話）。
  //
  // ⚠️ **書き込む先は `:root` の `--eval-graph-h` 1 か所。** 盤の大きさ
  // （`#panel-study` の `--board-size`）がこれを引いているので、**グラフを縮めた
  // ぶんだけ盤が自動で大きくなる**。⚠️ **カスタムプロパティは下へしか継承しない**
  // ので、グラフの箱に直接高さを書かないこと（盤の式から読めなくなる）。
  //
  // **その場かぎりの値**（config.json には持たない。視点と同じ扱い）。
  const evalGraphSplit = root.querySelector<HTMLDivElement>("#eval-graph-split")!;
  const evalGraphToggle = root.querySelector<HTMLButtonElement>("#eval-graph-toggle")!;
  // 既定値は style.css の `--eval-graph-h` と同じにすること（起動直後に
  // JS が書き込むまでは CSS 側の値が出ているので、食い違うと初回だけ跳ねる）。
  // ⚠️ **この値はグラフの箱の高さそのものではない**（2026-08-14）。
  // `.eval-graph` は**ここから 38px 詰めた高さ**で描くので、見えるグラフは
  // 116px（＝今までと同じ）になる。
  const EVAL_GRAPH_DEFAULT = 154;
  // 詰めるぶん（38px）。**style.css の `.eval-graph` の calc と同じ値にすること。**
  const EVAL_GRAPH_CHROME = 38;
  // これより低いと折れ線が読めないので、ここが「畳んでいない」ときの下限。
  // ⚠️ **見えるグラフのほうで 48px を確保する**（詰めるぶんを足しておく）。
  const EVAL_GRAPH_MIN = 48 + EVAL_GRAPH_CHROME;
  // ⚠️ **下限より下へドラッグしたら畳む**（0 にする）。下限で止めると、
  // ドラッグだけでは畳めないのに「一番下まで下げた」ようには見える。
  // ⚠️ **こちらも詰めるぶんを足す**（掴んでいる位置＝この値なので、見えている
  // グラフが 32px を切ったところで畳む、という手応えに揃える）。
  const EVAL_GRAPH_SNAP = 32 + EVAL_GRAPH_CHROME;
  let evalGraphH = EVAL_GRAPH_DEFAULT;
  // 畳む前の高さ。**畳んで開き直したときに元の高さへ戻すため**に覚えておく
  // （既定に戻すと、せっかく広げたのが畳むたびに失われる）。
  let evalGraphOpenH = EVAL_GRAPH_DEFAULT;

  // 上限は窓の高さから決める。**盤が潰れるところまで伸ばさせない**
  // （`#panel-study` はスクロールしないので、伸ばしすぎると盤がはみ出す）。
  // 360px は「タブの行 + 余白 + グラフの見出し + 勝率バー + 最低限の盤」の見積もり。
  const evalGraphMax = () => Math.max(EVAL_GRAPH_MIN, window.innerHeight - 360);

  const setEvalGraphH = (px: number) => {
    const next =
      px < EVAL_GRAPH_SNAP ? 0 : Math.min(Math.max(px, EVAL_GRAPH_MIN), evalGraphMax());
    if (next === evalGraphH) {
      return;
    }
    evalGraphH = next;
    if (next > 0) {
      evalGraphOpenH = next;
    }
    // ⚠️ **`documentElement` に入れること**（`:root`）。盤の式が読む先はここ。
    document.documentElement.style.setProperty("--eval-graph-h", `${next}px`);
    const open = next > 0;
    evalGraphRow.classList.toggle("is-collapsed", !open);
    evalGraphToggle.innerHTML = iconMarkup(open ? FiChevronDown : FiChevronUp);
    evalGraphToggle.setAttribute("aria-expanded", String(open));
    evalGraphToggle.title = open
      ? "評価値グラフを畳みます（そのぶん盤が大きくなります）"
      : "評価値グラフを開きます";
    evalGraphSplit.setAttribute("aria-valuenow", String(next));
    // ⚠️ **測り直しを 2 つとも落とさないこと。** グラフは `clientHeight` で
    // 描いており、**盤は式で大きさが変わる**ので重ねたグリッドがずれる。
    evalGraphUI.relayout();
    studyBoardUI.relayout();
    // ⚠️ **盤の高さの上限が動いたので、横の遊びも取り直す。** グラフを畳むと
    // 盤は縦に大きくなれるようになり、**そのぶん横の下限も下がる**。
    settleStudySide();
  };

  evalGraphToggle.addEventListener("click", () => {
    // ⚠️ **ドラッグ側（`setEvalGraphH` を毎フレーム呼ぶ経路）では付けないこと。**
    // 高さは変数 1 つなので、こちらは起点を作る小細工が要らない。
    beginAnimation();
    setEvalGraphH(evalGraphH > 0 ? 0 : evalGraphOpenH);
  });
  // ⚠️ **押してもドラッグが始まらないようにする**（縦のバーのトグルと同じ）。
  // 止めないと「掴んだ」と解釈されて、離すまで高さが動き続ける。
  evalGraphToggle.addEventListener("pointerdown", (e) => e.stopPropagation());

  // ドラッグ。**上へ引くと高くなる**（境目そのものを掴んでいる感覚に合わせる）。
  // ⚠️ **pointer capture を取ること** —— 掴んだまま盤の上やウィンドウの外へ
  // 出ることが普通にあるので、取らないと途中で追従が切れる。
  evalGraphSplit.addEventListener("pointerdown", (e) => {
    e.preventDefault();
    cancelAnimation();
    evalGraphSplit.setPointerCapture(e.pointerId);
    evalGraphSplit.classList.add("is-dragging");
    const startY = e.clientY;
    const startH = evalGraphH;
    const onMove = (ev: PointerEvent) => setEvalGraphH(startH - (ev.clientY - startY));
    const onUp = () => {
      evalGraphSplit.classList.remove("is-dragging");
      evalGraphSplit.removeEventListener("pointermove", onMove);
      evalGraphSplit.removeEventListener("pointerup", onUp);
      evalGraphSplit.removeEventListener("pointercancel", onUp);
    };
    evalGraphSplit.addEventListener("pointermove", onMove);
    evalGraphSplit.addEventListener("pointerup", onUp);
    evalGraphSplit.addEventListener("pointercancel", onUp);
  });

  // キーボードでも動かせるようにする（`role="separator"` の作法）。
  // ⚠️ **畳んだ状態からの ↑ は「開く」にすること** —— 8px 足しても
  // スナップの下限に届かず、押しても何も起きないように見える。
  evalGraphSplit.addEventListener("keydown", (e) => {
    const step = e.shiftKey ? 32 : 8;
    if (e.key === "ArrowUp") {
      e.preventDefault();
      setEvalGraphH(evalGraphH === 0 ? evalGraphOpenH : evalGraphH + step);
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      setEvalGraphH(evalGraphH - step);
    }
  });

  // 窓が低くなると上限も下がる。**はみ出したままにしないこと。**
  // ⚠️ **横の遊びも取り直す**（窓の幅が変われば盤の上限に張り付く一点も動く）。
  // ⚠️ **窓が狭くて盤が潰れたら、幅の指定を捨てて取り直す** —— 盤が読めない
  // 大きさのまま残るより、まず盤を成立させる。
  window.addEventListener("resize", () => {
    if (evalGraphH > 0) {
      setEvalGraphH(evalGraphH);
    }
    // ⚠️ **畳んでいるときは触らない**（0 を上書きすると、窓を動かしただけで
    // 勝手に開く）。畳んでいれば列は幅を取っていないので、そもそも起きない。
    if (studySideW > 0 && boardW() > 0 && boardW() < STUDY_BOARD_MIN) {
      studySideW = STUDY_SIDE_MIN;
      studySideOpenW = STUDY_SIDE_MIN;
      rawSide(STUDY_SIDE_MIN);
    }
    settleStudySide();
  });

  // ⚠️ **一度は通すこと**（ボタンのアイコンと `aria-*` はここで入れている）。
  // ⚠️ **`setEvalGraphH` は値が同じなら何もしない**ので、既定値そのものは通らない。
  evalGraphToggle.innerHTML = iconMarkup(FiChevronDown);
  evalGraphToggle.title = "評価値グラフを畳みます（そのぶん盤が大きくなります）";
  evalGraphSplit.setAttribute("aria-valuenow", String(evalGraphH));

  // ---- 別ウィンドウへの切り離し（2026-09-08）--------------------------------
  //
  // **ペインのままだと盤の大きさに効く**（`--board-size` がグラフの高さを引いている）
  // ので、盤を好きな大きさにしたい人のために窓へ出せるようにした。
  //
  // ⚠️ **畳む（高さ 0）とは別の状態。** 畳むのは「今は見ない」、切り離しは
  // 「**別の窓で見る**」。**畳んだ高さは覚えたまま**なので、戻せば元の高さで出る。
  //
  // ⚠️ **状態を持っているのは Go 側**（設定 `evalGraphDetached`）。ここが持つのは
  // 写しだけで、**切り替えは必ず `SettingsService.SetEvalGraphDetached` を通す** ——
  // グラフ窓を閉じる操作も同じ口を通るので、**どちらから切り替えても食い違わない。**
  const evalGraphDetach = root.querySelector<HTMLButtonElement>("#eval-graph-detach")!;
  evalGraphDetach.innerHTML = iconMarkup(FiExternalLink);

  // 切り離しているとき、盤から**余分に引く**量。**実測で決めた値**
  // （窓 4 通り × 畳み方で測り、**盤の下に 5px 残る**ところ。`--eval-graph-pad`
  // の表も読むこと）。行ごと消えるので、畳んだとき（20px）より小さい。
  // ⚠️ **0 にしないこと** —— 実測で盤の下端が 3px はみ出す。
  // ⚠️ **畳んだ行の高さ（バー 8 + gap 2 + 逃げ 6 = 16px）を変えたら測り直すこと。**
  const EVAL_GRAPH_PAD_DETACHED = "8px";

  // ⚠️ **null は「まだ決まっていない」。** 起動直後の 1 回だけは、値が同じでも
  // 通してレイアウトを整える必要がある。
  let graphDetached: boolean | null = null;

  const applyGraphDetached = (on: boolean) => {
    if (graphDetached === on) {
      return;
    }
    graphDetached = on;
    // ⚠️ **行ごと消すこと**（バーも見出しも）。同じ値を 2 か所に描かない。
    evalGraphRow.hidden = on || !studyLoaded;
    // ⚠️ **盤の式が読む値も切り替えること。** 切り離したのに高さが残っていると、
    // **そのぶん盤が小さいまま**になる（何も無い余白ができる）。
    document.documentElement.style.setProperty(
      "--eval-graph-h",
      on ? "0px" : `${evalGraphH}px`,
    );
    if (on) {
      document.documentElement.style.setProperty(
        "--eval-graph-pad",
        EVAL_GRAPH_PAD_DETACHED,
      );
    } else {
      // ⚠️ **消すこと**（値を書き戻さない）。CSS 側は畳む/開くに追随する式なので、
      // inline が残ると**畳んでも引き算が変わらない**。
      document.documentElement.style.removeProperty("--eval-graph-pad");
    }
    // ⚠️ **見えていない側は取り直さない**（イベント 1 回で往復が 2 回になる）。
    evalGraphUI.setActive(!on);
    // 盤の大きさが変わったので、重ねたグリッドと横の遊びを取り直す。
    studyBoardUI.relayout();
    evalGraphUI.relayout();
    settleStudySide();
  };

  evalGraphDetach.addEventListener("click", () => {
    void SettingsService.SetEvalGraphDetached(true);
  });

  // ⚠️ **戻すのは向こうから**（グラフ窓の「ドックに戻す」と、窓を閉じる操作）。
  // Go 側が `graph:detached` で知らせてくるので、**こちらから状態を作らない。**
  Events.On("graph:detached", (event: { data: boolean }) => {
    applyGraphDetached(!!event.data);
  });

  // ---- 盤の右の列の切り離し（2026-09-08）------------------------------------
  //
  // **作りは評価値グラフと同じ**（設定が状態を持ち、切り替えは必ず
  // `SettingsService.SetStudyPaneDetached` を通る）。**揃えておくこと。**

  // ⚠️ **2 つある**（2026-09-12。候補手 / 手順）。**1 つにまとめないこと** ——
  // 片方だけ外に出す使い方が普通で、**どちらを出すかはそのときの読み方で変わる。**
  //
  // ⚠️ **`sideDetached` が指すのは候補手の面だけ**（2026-09-12 に意味が狭まった）。
  // 名前は設定・イベント（`studyPaneDetached` / `side:detached`）と揃えてある。
  let sideDetached: boolean | null = null;
  let movesDetached: boolean | null = null;

  // 列そのものが要らなくなるのは**両方とも外に出したとき**だけ。
  const bothDetached = () => sideDetached === true && movesDetached === true;

  // applyPanes は列の見た目を今の 2 つの状態に合わせる（**書き換える場所はここ 1 つ**）。
  //
  // ⚠️ **「局面があるか」は `studyLoaded` から読むこと。** 他の要素の `hidden` から
  // 読まないこと —— 切り離すと**別の理由で `hidden` になる**ので、戻したときに
  // 「局面が無い」と誤読する（2026-09-09 に踏んだ）。
  const applyPanes = () => {
    const both = bothDetached();
    // ⚠️ **列ごと消すのは両方切り離したときだけ**（バーも）。
    panelStudy.classList.toggle("is-side-detached", both);
    studySide.hidden = both || !studyLoaded;
    studySplit.hidden = both || !studyLoaded;
    // ⚠️ **盤の式が読む幅も 0 にすること。** 列が出ていないのに幅を予約したままだと、
    // **そのぶん盤が小さいまま**になる（何も無い余白ができる）。
    rawSide(both ? 0 : studySideW);
    // ⚠️ **ドック側が出す面は「切り離していないほう」**（同じ値を 2 か所に描かない）。
    sidePane.setParts({ analyze: sideDetached !== true, moves: movesDetached !== true });
    // ⚠️ **使われていない側は自動解析をしないこと**（2026-09-08）。切り離しても
    // ドック側のペインは隠れたまま生きているので、**両方が起こし合う**。
    // ⚠️ **持ち主を決めるのは候補手の面だけ**（エンジンを掴んでいるのがあちら）——
    // 手順を切り離しても、解析はこちらが持ったまま。
    sidePane.setActive(sideDetached !== true);
    studyBoardUI.relayout();
    evalGraphUI.relayout();
    if (!both) {
      settleStudySide();
    }
  };

  const applySideDetached = (on: boolean) => {
    if (sideDetached === on) {
      return;
    }
    sideDetached = on;
    applyPanes();
  };

  const applyMovesDetached = (on: boolean) => {
    if (movesDetached === on) {
      return;
    }
    movesDetached = on;
    applyPanes();
  };

  studySplitDetach.addEventListener("click", () => {
    void SettingsService.SetStudyPaneDetached(true);
  });
  studyMovesDetach.addEventListener("click", () => {
    void SettingsService.SetMovePaneDetached(true);
  });

  Events.On("side:detached", (event: { data: boolean }) => {
    applySideDetached(!!event.data);
  });
  Events.On("moves:detached", (event: { data: boolean }) => {
    applyMovesDetached(!!event.data);
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
  const fromSideWindow = () => sideDetached === true;
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
  // 切り離した窓で色や本数を変えたら、設定タブと折れ線の色も追随させる。
  Events.On("settings:changed", () => {
    void (async () => {
      try {
        showSettings(await SettingsService.Settings());
      } catch {
        /* 読めなくても今の表示のまま（設計原則3）。 */
      }
    })();
  });

  // ---- 盤と解析の列の幅（縦のスプリットバー。2026-08-13）--------------------
  //
  // ⚠️ **書き換えるのは `--study-side-min`**（＋掴んだあとは `--study-side-max`）。
  // 盤の大きさ（`--board-size`）が前者を引いているので、詰めれば盤が大きくなる。
  // ⚠️ **`#panel-study` の inline style に入れること** —— あの変数は
  // `#panel-study` 自身が定義しているので、`:root` へ書いても負ける
  // （`--eval-graph-h` などとは事情が違う）。
  //
  // ⚠️ **掴むまでは列が余りを全部もらう**（`.study-side` は `flex: 1 1 auto`）。
  // つまり**列の実幅は「窓幅 − 盤」で決まっていて、`--study-side-min` は
  // 盤の式の引き算としてしか効かない。** 盤は縦（＝評価値グラフの高さ）でも
  // 決まるので、**盤が高さで頭打ちになったところから先は列が 1px も動かない** ——
  // 広い窓では列が 600px を超えたまま狭められなかった（2026-09-12 に外した制限）。
  //
  // ⚠️ **掴んだら列の幅を固定する**（`lockSide`。`--study-side-max`）。そこから先は
  // **1px 引けば 1px 動く**。⚠️ **余ったぶんは盤の側に出る**（盤は中央へ寄る。
  // 盤が高さで頭打ちのあいだは、引いたぶんがそのまま余白になる）。
  //
  // ⚠️ **掴むまでは固定しないこと。** 既定を「余りを全部もらう」にしておかないと、
  // **起動のたびに列が下限（300px）で始まる**（幅は設定に持っていない）。
  //
  // ⚠️ **判定は「実際の盤の幅を測って」行う。式の定数を JS に写さないこと。**
  // `--board-size` の式（`110px` や `1.4`）を写すと、CSS を直したときに
  // **黙って食い違う**（画面では気づけない）。測れば式が変わっても付いてくる。
  const panelStudy = root.querySelector<HTMLElement>("#panel-study")!;
  const studySplit = root.querySelector<HTMLDivElement>("#study-split")!;
  // 盤をこれより小さくしてまで解析の列を広げない。
  const STUDY_BOARD_MIN = 240;
  // ⚠️ **解析の列をこれより詰めない。** 盤を優先して詰め切ると、窓が狭いときに
  // **解析の行が入らない幅まで潰れて読めなくなる**（`.study-side` の下限の話）。
  const STUDY_SIDE_MIN = 300;
  // ⚠️ **0 は「畳んでいる」。** 評価値グラフの高さと同じで、**真偽値を別に
  // 持たない**（畳んでいるのに幅がある、という食い違いを作らないため）。
  // ドラッグの下限は STUDY_SIDE_MIN なので、**0 になるのは畳んだときだけ**。
  let studySideW = STUDY_SIDE_MIN; // style.css の --study-side-min と同じ既定
  // 開き直すときの幅。**畳む前の幅に戻す**（既定に戻すと、広げたのが失われる）。
  let studySideOpenW = STUDY_SIDE_MIN;

  // 人が幅を決めたか（2026-09-12）。**決めたら列は余りをもらわなくなる。**
  // ⚠️ **一度決めたら戻さないこと** —— 窓を広げるたびに列が太るのは、
  // 幅を決めたあとの振る舞いとしては裏切りになる（広がるのは盤の側）。
  let sideLocked = false;

  // 盤の実寸（`.board-stage` の幅 = `--board-size`）。タブが隠れていれば 0。
  const boardW = () => studyStage.getBoundingClientRect().width;
  // ⚠️ **決めたあとは min と max の両方に入れること。** `min-width` だけでは
  // 列は余りをもらったままなので、**下げても狭くならない**（この節の冒頭）。
  const rawSide = (px: number) => {
    const v = `${Math.round(px)}px`;
    panelStudy.style.setProperty("--study-side-min", v);
    if (sideLocked) {
      panelStudy.style.setProperty("--study-side-max", v);
    }
  };

  // lockSide は列の幅を人が決めたことにする（**バーを掴んだ / キーで動かした**）。
  //
  // ⚠️ **固定する値は「画面に出ている幅」を測って決めること**（2026-09-12 に踏んだ）。
  // `.study-side` は **`min-width: 0`** なので、`--study-side-min` は
  // **盤の式の引き算としてしか効かず、列の実幅は「余り」で決まっている** ——
  // つまり `studySideW`（`settleStudySide` が式から逆算した値）と
  // **実幅は一致するとは限らない。** その差のぶん、**掴んだ瞬間に列が飛ぶ**
  // （固定したあとは両者が一致するので、**2 回目以降だけ綺麗**という形で出る）。
  //
  // ⚠️ **`settleStudySide` のあとに呼ぶこと。** あちらが `--study-side-min` を
  // 詰めていないと、**盤の式が実幅と食い違ったまま固定される。**
  const lockSide = () => {
    if (sideLocked) {
      return;
    }
    const shown = Math.round(studySide.getBoundingClientRect().width);
    sideLocked = true;
    // ⚠️ **測れないときは今の値のまま**（タブが隠れている等。0 で固定すると
    // 列が消える）。
    if (shown > 0) {
      studySideW = Math.max(shown, STUDY_SIDE_MIN);
      studySideOpenW = studySideW;
      studySplit.setAttribute("aria-valuenow", String(studySideW));
    }
    rawSide(studySideW);
  };

  // settleStudySide は「盤が上限に張り付いたまま取れる最大の幅」まで詰める。
  //
  // **見た目は 1px も動かない**（盤は上限のまま、右の列は余りをもらうので）。
  // これをやっておかないと、**ドラッグし始めても最初のうち何も動かない**
  // （遊びのぶんだけ空振りする）。
  //
  // ⚠️ **既に盤が縮んでいるときは触らないこと** —— それはユーザーが自分で
  // 列を広げた状態なので、勝手に戻すと設定を奪う。
  const settleStudySide = () => {
    // ⚠️ **切り離しているあいだは触らない**（2026-09-08）。列が出ていないので
    // 詰める相手が居ない（畳んでいるときと同じ扱い）。
    // ⚠️ **幅を決めたあとは触らないこと**（2026-09-12）。ここは「盤が上限に
    // 張り付いたまま取れる最大の幅まで詰める」処理なので、**人が決めた幅を
    // その最大値へ押し戻してしまう**（狭めた直後に元へ戻る）。
    if (sideLocked) {
      return;
    }
    // ⚠️ **見るのは `bothDetached()`**（2026-09-12）。`sideDetached` は
    // **候補手の面だけ**を指すようになったので、ここで見ると
    // **手順だけ残っている列でバーが死ぬ**（掴んでも 1px も動かない。実際に踏んだ）。
    if (bothDetached() || studySideW === 0 || boardW() <= 0) {
      return; // 両方とも切り離し / 畳んでいる / タブが隠れている（測れない）
    }
    // ⚠️ **折り畳みのアニメーション中は測らない。** 盤の幅が動いている最中なので、
    // 二分探索が途中の値を掴んで**でたらめな幅で確定する**。終わったら呼び直す。
    if (panelStudy.classList.contains("is-animating")) {
      return;
    }
    // **詰め切ったとき（＝列を下限まで狭めたとき）の盤**が、この窓で取れる上限。
    // ⚠️ **0 で測らないこと** —— 窓が狭いと盤は横で決まるので、0 まで詰めた
    // 大きさを上限にすると**解析の列を潰し切るまで詰めてしまう**。
    rawSide(STUDY_SIDE_MIN);
    const cap = boardW();
    rawSide(studySideW);
    if (studySideW > STUDY_SIDE_MIN && boardW() < cap - 0.5) {
      return; // 盤は既に横で決まっている＝ユーザーが自分で列を広げた側
    }
    // 上限に張り付いている最大の幅を二分探索で求める。
    let lo = STUDY_SIDE_MIN;
    let hi = Math.max(studySideW, window.innerWidth);
    for (let i = 0; i < 20; i++) {
      const mid = (lo + hi) / 2;
      rawSide(mid);
      if (boardW() >= cap - 0.5) {
        lo = mid;
      } else {
        hi = mid;
      }
    }
    studySideW = Math.round(lo);
    studySideOpenW = studySideW;
    rawSide(studySideW);
  };

  // setStudySideW は幅を変える。**効果が無い方向へは動かさない。**
  const setStudySideW = (px: number) => {
    // ⚠️ **`bothDetached()` で見ること**（`settleStudySide` と同じ ⚠️）。
    // 列が 1 つでも残っているなら、幅は今までどおり変えられなければならない。
    if (bothDetached() || studySideW === 0 || boardW() <= 0) {
      return; // 両方とも切り離し / 畳んでいるあいだは幅を変えない（戻すのはトグルの仕事）
    }
    const next = Math.max(Math.round(px), STUDY_SIDE_MIN);
    if (next === studySideW) {
      return;
    }
    const before = studySideW;
    rawSide(next);
    const afterBoard = boardW();
    // ⚠️ **広げすぎて盤が潰れるのは止める**（下限は `STUDY_BOARD_MIN`）。
    //
    // ⚠️ **「詰めても盤が大きくならないなら詰めない」は外した**（2026-09-12）。
    // あれは列が余りをもらう作りが前提で、**盤が高さで頭打ちになった時点で
    // 列がそれ以上狭められない**という壁になっていた（広い窓で 600px 超）。
    // 今は `lockSide` で列の幅そのものを持つので、**盤が伸びない範囲でも
    // 狭められる**（余ったぶんは盤の側の余白になり、盤は中央へ寄る）。
    if (next > before && afterBoard < STUDY_BOARD_MIN) {
      rawSide(before);
      return;
    }
    studySideW = next;
    studySideOpenW = next;
    studySplit.setAttribute("aria-valuenow", String(next));
    // 盤の大きさが変わったので、重ねたグリッドとグラフを測り直す。
    studyBoardUI.relayout();
    evalGraphUI.relayout();
  };

  // ⚠️ **掴む前に遊びを消しておく**（見た目は動かない）。これが無いと、
  // 右へ引き始めても盤の右端が動かない区間ができる。
  studySplit.addEventListener("pointerdown", (e) => {
    e.preventDefault();
    cancelAnimation();
    // ⚠️ **順番を変えないこと。** 遊びを消してから固定する（`lockSide` の ⚠️）。
    settleStudySide();
    lockSide();
    studySplit.setPointerCapture(e.pointerId);
    studySplit.classList.add("is-dragging");
    const startX = e.clientX;
    // ⚠️ **起点は `lockSide` のあとに読むこと** —— あちらが実測で
    // `studySideW` を入れ直すので、先に読むと**1 回目のドラッグだけ
    // 見た目とずれた位置を掴む。**
    const startW = studySideW;
    // **右へ引く = 盤を広げる = 右の列を詰める。**
    const onMove = (ev: PointerEvent) => setStudySideW(startW - (ev.clientX - startX));
    const onUp = () => {
      studySplit.classList.remove("is-dragging");
      studySplit.removeEventListener("pointermove", onMove);
      studySplit.removeEventListener("pointerup", onUp);
      studySplit.removeEventListener("pointercancel", onUp);
    };
    studySplit.addEventListener("pointermove", onMove);
    studySplit.addEventListener("pointerup", onUp);
    studySplit.addEventListener("pointercancel", onUp);
  });

  studySplit.addEventListener("keydown", (e) => {
    const step = e.shiftKey ? 32 : 8;
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") {
      return;
    }
    e.preventDefault();
    // ⚠️ **ドラッグと同じ順で**（遊びを消してから固定する）。キーでも
    // 固定しないと、**右キーで狭めたつもりが 1px も動かない。**
    settleStudySide();
    lockSide();
    setStudySideW(studySideW + (e.key === "ArrowLeft" ? step : -step));
  });

  studySplit.setAttribute("aria-valuenow", String(studySideW));

  // ---- 解析の列の折り畳み（2026-08-13）------------------------------------
  //
  // ⚠️ **畳むと列ごと消える**ので、トグルは**縦のスプリットバーの上**に置く
  // （列の中に置いたら戻す手段が無くなる。評価値グラフで見出しの行を残して
  // あるのと同じ話）。
  //
  // ⚠️ **畳んだら盤を中央に寄せる。** 左上を起点にしているのは右に列があるから
  // で、列が無くなれば寄せる理由も無くなる（盤だけがある画面で左に寄っていると、
  // 右の余白が「何かあるはず」に見える）。
  //
  // ⚠️ **畳んでいるあいだはドラッグしない**（`studySideW === 0` で弾く）。
  // 幅は 0 と STUDY_SIDE_MIN のあいだが飛んでいるので、ドラッグの続きにならない。
  const studySplitToggle = root.querySelector<HTMLButtonElement>("#study-split-toggle")!;

  const applyStudySideCollapsed = () => {
    const open = studySideW > 0;
    panelStudy.classList.toggle("is-side-collapsed", !open);
    // ⚠️ **`.study-side` の `hidden` は触らないこと** —— あちらは
    // 「局面があるか」を表しており（`showStudy`）、持ち主が 2 つになると
    // どちらが今の話か分からなくなる。畳むほうは class で消す。
    studySplitToggle.innerHTML = iconMarkup(open ? FiChevronRight : FiChevronLeft);
    studySplitToggle.setAttribute("aria-expanded", String(open));
    studySplitToggle.title = open
      ? "解析の列を畳みます（盤が中央に来ます）"
      : "解析の列を開きます";
    studySplit.setAttribute("aria-valuenow", String(studySideW));
    // 畳むあいだは幅の予約も外す（窓が狭いときに盤が取れる幅が増える）。
    rawSide(open ? studySideW : 0);
    studyBoardUI.relayout();
    evalGraphUI.relayout();
  };

  // ---- 折り畳みのアニメーション（2026-08-13）------------------------------
  //
  // ⚠️ **畳む/開くときだけ付ける。** 常時付けると、スプリットバーの**ドラッグが
  // 0.3s 遅れて追ってくる**（掴んでいる位置と盤がずれる）。
  //
  // ⚠️ **後始末（遊びの取り直しと測り直し）は終わってから。** 途中で測ると
  // 動いている最中の値を掴む。
  const ANIM_MS = 300;
  let animTimer = 0;
  const endAnimation = () => {
    window.clearTimeout(animTimer);
    animTimer = 0;
    panelStudy.classList.remove("is-animating");
    // 起点として入れた inline の max-width を外す（畳んだ側は CSS が 0 を持つ）。
    studySide.style.maxWidth = "";
    settleStudySide();
    studyBoardUI.relayout();
    evalGraphUI.relayout();
  };
  const beginAnimation = () => {
    panelStudy.classList.add("is-animating");
    window.clearTimeout(animTimer);
    animTimer = window.setTimeout(endAnimation, ANIM_MS + 40);
  };
  // ⚠️ **掴んだら即座に終わらせる。** 畳んだ直後にバーを掴むと、残りの 0.3s は
  // トランジションが効いたままで**ドラッグが遅れて追ってくる**。
  const cancelAnimation = () => {
    if (animTimer !== 0) {
      endAnimation();
    }
  };

  studySplitToggle.addEventListener("click", () => {
    const opening = studySideW === 0;
    studySideW = opening ? studySideOpenW : 0;
    if (opening) {
      // ⚠️ **開く幅は「開いてみないと分からない」**（列は余りをもらうので）。
      // ① いったん最終状態にして測り、② 見た目だけ畳んだ状態へ戻して起点にし、
      // ③ トランジションを入れてから最終状態へ、という順で動かす。
      // **①〜②は同じタスクの中なので、途中の状態は描かれない。**
      applyStudySideCollapsed();
      const target = studySide.getBoundingClientRect().width;
      panelStudy.classList.add("is-side-collapsed");
      studySide.style.maxWidth = "0px";
      void studySide.offsetWidth; // ここまでをレイアウトに反映させる
      beginAnimation();
      panelStudy.classList.remove("is-side-collapsed");
      studySide.style.maxWidth = `${target}px`;
    } else {
      // 畳むほうは起点が今の幅そのもの。
      studySide.style.maxWidth = `${studySide.getBoundingClientRect().width}px`;
      void studySide.offsetWidth;
      beginAnimation();
      applyStudySideCollapsed();
      studySide.style.maxWidth = "0px";
    }
  });
  // ⚠️ **押してもドラッグが始まらないようにする。** バーの上に載っているので、
  // 止めないと「掴んだ」と解釈されて、離すまで幅が動き続ける。
  studySplitToggle.addEventListener("pointerdown", (e) => e.stopPropagation());

  applyStudySideCollapsed();

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
      syncTrain();
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
    onConfirm: () => {
      void adoptToStudy();
    },
    onError: (message) => {
      // **訂正タブの中に出す。** 撮影の結果（入力タブの #status）とは別の話で、
      // タブを跨いだ先に理由が出ても読めない。
      editStatus.textContent = `訂正できませんでした: ${message}`;
      editStatus.classList.add("is-error");
    },
  });

  // onKifuDBChanged は棚を開き直したときに呼ぶ（棋譜タブの一覧を取り直す）。
  // ⚠️ **設定タブから棋譜タブの DOM を触らないこと** —— 一覧を持っているのは
  // あちらなので、繋ぐのはこの 1 本にする。
  let onKifuDBChanged: (() => void) | undefined;

  // ---- 棋譜タブ（棚）------------------------------------------------------
  //
  // ⚠️ **一覧も検索も Go 側（KifuService）が持つ。** ここは行き先を繋ぐだけ。
  const libraryUI = mountLibrary(root, {
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

  // 棚を開き直したら一覧を取り直す（設定タブから繋いである 1 本）。
  onKifuDBChanged = () => libraryUI.refresh();

  // ---- 中継から取得（入力タブ）--------------------------------------------
  //
  // ⚠️ **取得元の判別も取得も Go 側（KifuService）。** ここは行き先を繋ぐだけ。
  mountFetchCards(root, {
    onAnalyze: (got) => {
      // 棚を通らない経路だが、**描き方は棚から送ったときと同じ**（KifuLoad）。
      selectTab(studyTab);
      showStudy(got.state);
    },
    onSaved: () => libraryUI.refresh(),
  });

  // ---- 視点（手前が先手 / 手前が後手）--------------------------------------
  //
  // **表示だけの反転で、局面には一切効かない。** 反転するのは
  // `<shogi-board>` の絵（`flip` 属性）・駒台の置き場所と向き・盤に重ねる
  // グリッドの読み替えだけで、**Go 側は視点を知らない**。
  //
  // ⚠️ **モデルは反転しない。** SFEN も先後も手番も指す手（USI）もそのままで、
  // エンジンに渡すものも変わらない。CLAUDE.md の「取り込みでも訂正でも盤を
  // 反転しない」（＝撮った画像と盤面が一致していること。学習データのラベルは
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

  // 設定で指定した学習データの置き場所(ikkyoku.Config の SutemeDataDir)。
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
      overlay.appendChild(el);
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
    status.textContent = `撮りました（${taken.width}x${taken.height}）。盤面を認識しています…`;
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
    trainSendStatus.textContent = "";
    trainSendStatus.classList.remove("is-error");

    // 撮った画像。**まだ盤面領域が分からないので切り取らずに全体を出す**
    // （領域は認識が出すもの。届いたら showResult が同じ画像を切り取って出し直す）。
    if (taken.thumbnail) {
      thumbnail.src = taken.thumbnail;
      shot.hidden = false;
    }
    lastRegion = null;
    hasShot = !!taken.thumbnail;
    if (hasShot) {
      showCaptureRefImage(taken.thumbnail, null, taken.path);
    }
    syncCaptureRef();
    syncTrain();

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
      status.textContent = `撮りました（${result.width}x${result.height}）。盤面は認識できませんでした: ${result.recognizeError}`;
      status.classList.add("is-warn");
      status.classList.remove("is-error");
    } else {
      status.textContent = `撮りました（${result.width}x${result.height}）。盤面を認識しました。`;
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
      void editor.load(result.sfen).then(() => selectTab(editTab));
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
    // ここではそれを盤面領域で切り取って出すだけ（別の経路で取り直さない）。
    hasShot = !!result.thumbnail;
    if (hasShot) {
      showCaptureRefImage(result.thumbnail, lastRegion, result.path);
    }
    syncCaptureRef();
    syncTrain();
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
  });
  // 起動した時点で出ていることがある（設定「起動時に盤面を探す」）。
  void (async () => {
    try {
      showFrameState(await CaptureService.FrameVisible());
    } catch {
      /* 取れなくても「隠れている」で始めれば押せる（設計原則3）。 */
    }
  })();

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
  const newgameStatus = root.querySelector<HTMLParagraphElement>("#newgame-status")!;
  const newgameSides = Array.from(
    root.querySelectorAll<HTMLButtonElement>("#panel-input .turn-group .turn-btn"),
  );
  // 既定は先手（中継の原則と同じ「手前が先手」）。
  let newgameBlack = true;
  for (const b of newgameSides) {
    b.addEventListener("click", () => {
      newgameBlack = b.dataset.side === "black";
      for (const x of newgameSides) {
        x.classList.toggle("is-active", x === b);
      }
    });
  }
  newgameStart.addEventListener("click", () => {
    void (async () => {
      newgameStart.disabled = true;
      newgameStatus.hidden = false;
      newgameStatus.classList.remove("is-error");
      newgameStatus.textContent = "対局を作っています…";
      try {
        const got = await StudyService.NewGame(newgameHandicap.value);
        // ⚠️ **視点は局面を描く前に決める。** 後から反転すると、盤とグリッドを
        // 二度組み直すことになる（そのぶん 1 マスずれる隙ができる）。
        setStudyViewpoint(newgameBlack);
        // ⚠️ **タブを先に開いてから描く**（棋譜の読み込みと同じ理由。
        // `display: none` の中ではグリッドを測れないし scrollIntoView も効かない）。
        selectTab(studyTab);
        showStudy(got.state);
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

  // 棋譜（KIF）を貼り付ける。**キャプチャと並ぶ入力の口で、行き先は解析タブ。**
  //
  // ⚠️ **読み取りも指し手の変換も Go 側**（core/kifu → position.FromKIF）。
  // フロントで KIF を解釈しないこと（将棋の仕様は core に一本化する）。
  const kifuText = root.querySelector<HTMLTextAreaElement>("#kifu-text")!;
  const kifuURL = root.querySelector<HTMLInputElement>("#kifu-url")!;
  const kifuLoad = root.querySelector<HTMLButtonElement>("#kifu-load")!;
  const kifuLoadURL = root.querySelector<HTMLButtonElement>("#kifu-load-url")!;
  const kifuImport = root.querySelector<HTMLButtonElement>("#kifu-import")!;
  const kifuImportURL = root.querySelector<HTMLButtonElement>("#kifu-import-url")!;
  const kifuStatus = root.querySelector<HTMLParagraphElement>("#kifu-status")!;
  const showKifuStatus = (message: string, kind?: "warn" | "error") => {
    kifuStatus.textContent = message;
    kifuStatus.classList.toggle("is-warn", kind === "warn");
    kifuStatus.classList.toggle("is-error", kind === "error");
    kifuStatus.hidden = message === "";
  };
  root.querySelector<HTMLButtonElement>("#kifu-clear")!.addEventListener("click", () => {
    kifuText.value = "";
    showKifuStatus("");
    kifuText.focus();
  });
  // 貼り付けでも URL でも、読み込んだあとにやることは同じ。
  // **1 か所にまとめてある**（2 つに分けると、片方だけ直したときに挙動が食い違う）。
  const runKifuLoad = async (button: HTMLButtonElement, load: () => Promise<KifuLoad>) => {
    button.disabled = true;
    kifuLoad.disabled = true;
    kifuLoadURL.disabled = true;
    showKifuStatus("読み込んでいます…");
    try {
      const got = await load();
      // ⚠️ **タブを先に開いてから描くこと。** 手順のリストは「今見ている手」を
      // scrollIntoView で見せるが、`display: none` の中では効かない。
      // 逆順にすると、見ている手がどこにあるか分からないまま出る。
      // ⚠️ **読み込んだ直後に見ているのは開始局面**（2026-08-18。
      // `StudyService.LoadKifu`）。**先へ進めないこと** —— 連続解析は
      // 今見ている手から走るので、初手から解析できる位置に置いてある。
      selectTab(studyTab);
      showStudy(got.state);
      // ⚠️ **note が空でないことをエラー扱いしないこと。** 途中で止まっても
      // そこまでの手順は正しく、その局面は解析できる（設計原則3）。
      showKifuStatus(
        got.note ? `${got.summary}（${got.note}）` : got.summary,
        got.note ? "warn" : undefined,
      );
    } catch (err) {
      showKifuStatus(
        `棋譜を読み込めませんでした: ${String(err instanceof Error ? err.message : err)}`,
        "error",
      );
    } finally {
      kifuLoad.disabled = false;
      kifuLoadURL.disabled = false;
    }
  };
  kifuLoad.addEventListener("click", () => {
    const text = kifuText.value.trim();
    if (!text) {
      showKifuStatus("棋譜が空です。KIF 形式のテキストを貼り付けてください。", "error");
      return;
    }
    void runKifuLoad(kifuLoad, () => StudyService.LoadKifu(text));
  });
  // URL から取る。**取得は Go 側**（webview の fetch は CORS で弾かれるうえ、
  // 中継の .kif は Shift_JIS なのでどのみちこちらでは読めない）。
  const loadFromURL = () => {
    const url = kifuURL.value.trim();
    if (!url) {
      showKifuStatus("URL が空です。.kif ファイルの URL を入れてください。", "error");
      return;
    }
    void runKifuLoad(kifuLoadURL, () => StudyService.LoadKifuURL(url));
  };
  kifuLoadURL.addEventListener("click", loadFromURL);

  // 棚に登録する（棋譜タブ）。**「読み込む」とは行き先が違うだけ**で、
  // 入力欄は同じ。
  //
  // ⚠️ **二系統を残してある**（2026-09-04）——「読み込む」は棚に入らず解析タブへ
  // 直行し、こちらは棚へ入れるだけで**解析タブを触らない**。棚は解析の前提条件では
  // ないので（設計原則3）、DB が開けていなくても「読み込む」は今までどおり動く。
  //
  // ⚠️ **タブは移らない。** 登録は「あとで探せるようにする」操作で、今すぐ見る
  // わけではない（今すぐ見たいなら「読み込む」）。**代わりに一覧は取り直す**
  // （棋譜タブを開いたときに反映されていないと、登録できたのか分からない）。
  const runKifuImport = async (button: HTMLButtonElement, save: () => Promise<GameSummary>) => {
    button.disabled = true;
    kifuImport.disabled = true;
    kifuImportURL.disabled = true;
    showKifuStatus("棚に登録しています…");
    try {
      const rec = await save();
      libraryUI.refresh();
      const who = [rec.black, rec.white].filter(Boolean).join(" - ");
      showKifuStatus(
        `棚に登録しました: ${[rec.event, who].filter(Boolean).join(" / ") || "(棋戦名なし)"}`,
      );
    } catch (err) {
      showKifuStatus(
        `棚に登録できませんでした: ${String(err instanceof Error ? err.message : err)}`,
        "error",
      );
    } finally {
      kifuImport.disabled = false;
      kifuImportURL.disabled = false;
    }
  };
  kifuImport.addEventListener("click", () => {
    const text = kifuText.value.trim();
    if (!text) {
      showKifuStatus("棋譜が空です。KIF 形式のテキストを貼り付けてください。", "error");
      return;
    }
    void runKifuImport(kifuImport, () => KifuService.ImportKIF(text));
  });
  kifuImportURL.addEventListener("click", () => {
    const url = kifuURL.value.trim();
    if (!url) {
      showKifuStatus("URL が空です。.kif ファイルの URL を入れてください。", "error");
      return;
    }
    void runKifuImport(kifuImportURL, () => KifuService.ImportURL(url));
  });
  // URL 欄で Enter を押したら読み込む（打ってからボタンへ手を戻さずに済む）。
  kifuURL.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      loadFromURL();
    }
  });

  // 学習データを育てながら使うための入口。suteme は一度読んだ推論器をキャッシュするので、
  // データを更新してもこれを押すまで(あるいは再起動するまで)反映されない。
  //
  // **読むものは 2 つ**(駒種推論器と盤の縁の帯の判定器)。帯の判定器が無くても認識は
  // 動くのでエラーにはしないが、**黙って落とすと盤の位置が 1マス滑ったまま
  // 信頼度 100% で返る**ので警告として出す。
  const showRecognizer = (st: RecognizerStatus) => {
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
      recognizer.textContent =
        st.mode === "embed"
          ? "認識器: 焼き込んだデータを読み込めませんでした"
          : "認識器: suteme の既定の場所を探します";
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

  // 設定タブ。「起動時に盤面を探す」と「訂正盤面を suteme に登録する」。
  //
  // **変えたその場で保存する**(適用ボタンを置かない)。押し忘れて反映されないほうが
  // 分かりにくいため。保存に失敗したら画面の値を元に戻す(画面の状態と設定ファイルを
  // 食い違わせない)。テキスト欄は change(確定時)で拾うので、1 文字ごとには書かない。
  const fitOnStartup = root.querySelector<HTMLInputElement>("#fit-on-startup")!;
  const clickThrough = root.querySelector<HTMLInputElement>("#click-through")!;
  // 認識器の読み込み元。**「焼き込みがあるビルドか」は Go が返す**
  // （`sutemeEmbedAvailable`）。⚠️ **フロントで判定できない**（バイナリの中身の話）。
  const sutemeSource = root.querySelector<HTMLSelectElement>("#suteme-source")!;
  const sutemeDataDir = root.querySelector<HTMLInputElement>("#suteme-data-dir")!;
  const sutemeSourceNote = root.querySelector<HTMLParagraphElement>("#suteme-source-note")!;
  const kifuDBPath = root.querySelector<HTMLInputElement>("#kifudb-path")!;
  const kifuDBBrowse = root.querySelector<HTMLButtonElement>("#kifudb-browse")!;
  const kifuDBNote = root.querySelector<HTMLParagraphElement>("#kifudb-note")!;
  const settingsStatus = root.querySelector<HTMLParagraphElement>("#settings-status")!;
  const settingsPath = root.querySelector<HTMLElement>("#settings-path")!;
  const trainEnabledInput = root.querySelector<HTMLInputElement>("#train-enabled")!;
  const trainHost = root.querySelector<HTMLInputElement>("#train-host")!;
  const trainPort = root.querySelector<HTMLInputElement>("#train-port")!;
  const trainToken = root.querySelector<HTMLInputElement>("#train-token")!;
  // 勝率バーの変換に使う定数（解析タブ）。**空欄なら既定に戻す。**
  const ponanzaConstant = root.querySelector<HTMLInputElement>("#ponanza-constant")!;
  const trainCheck = root.querySelector<HTMLButtonElement>("#train-check")!;
  const trainCheckStatus = root.querySelector<HTMLParagraphElement>("#train-check-status")!;

  // 解析エンジン。**パスが空なら同梱**（「外部を使う」のトグルは持たない）。
  // ⚠️ **一覧**（2026-08-11）。行ごとに「解析に使う」があり、付けたものが同時に走る。
  const engineList = root.querySelector<HTMLUListElement>("#engine-list")!;
  const engineAdd = root.querySelector<HTMLButtonElement>("#engine-add")!;
  const engineAddBuiltin = root.querySelector<HTMLButtonElement>("#engine-add-builtin")!;
  const engineStatus = root.querySelector<HTMLParagraphElement>("#engine-status")!;
  // 畳んでいるときの 1 行（見出しの右）。⚠️ **中身の代わりにはしない** ——
  // 開かなくても「今どうなっているか」が分かるだけの添え物。
  const engineFold = root.querySelector<HTMLDetailsElement>("#fold-engine")!;
  const engineFoldSum = root.querySelector<HTMLElement>("#fold-engine-sum")!;

  // ---- 駒の字（2026-08-16）----
  //
  // **端末に入っているフォントから駒の 19 グリフだけを焼いて使う**（Go 側の
  // `FontService`）。同梱できるフォントがライセンスの都合で限られる一方、
  // 自分の端末のフォントを自分の端末で表示に使うのは再配布ではない、というのが
  // この機能の拠り所。⚠️ **書き出す口は作らないこと。**
  const fontRequired = root.querySelector<HTMLElement>("#font-required")!;
  const fontList = root.querySelector<HTMLUListElement>("#font-list")!;
  const fontScanBtn = root.querySelector<HTMLButtonElement>("#font-scan")!;
  const fontStatus = root.querySelector<HTMLParagraphElement>("#font-status")!;
  const fontPicker = root.querySelector<HTMLDivElement>("#font-picker")!;
  const fontFilter = root.querySelector<HTMLInputElement>("#font-filter")!;
  const fontOnlyUsable = root.querySelector<HTMLInputElement>("#font-only-usable")!;
  const fontSample = root.querySelector<HTMLParagraphElement>("#font-sample")!;
  const fontChoices = root.querySelector<HTMLUListElement>("#font-choices")!;
  const fontDirs = root.querySelector<HTMLParagraphElement>("#font-dirs")!;
  const fontGyoku = root.querySelector<HTMLSelectElement>("#font-gyoku")!;
  const fontHidariUma = root.querySelector<HTMLInputElement>("#font-hidari-uma")!;
  const fontInkColor = root.querySelector<HTMLInputElement>("#font-ink-color")!;
  const fontInkOpacity = root.querySelector<HTMLInputElement>("#font-ink-opacity")!;
  const fontInkOpacityValue = root.querySelector<HTMLOutputElement>("#font-ink-opacity-value")!;
  const fontInkReset = root.querySelector<HTMLButtonElement>("#font-ink-reset")!;
  const fontFold = root.querySelector<HTMLDetailsElement>("#fold-piecefont")!;
  const fontFoldSum = root.querySelector<HTMLElement>("#fold-piecefont-sum")!;

  // 王/玉・左馬を当てる相手。**2 つの盤の両方**（訂正タブと解析タブ）。
  // ⚠️ **片方だけだと、採った瞬間に字が変わって見える。**
  const pieceBoards = [
    root.querySelector<HTMLElement>("#board")!,
    root.querySelector<HTMLElement>("#study-board")!,
  ];

  // 探した結果。**探したときだけ埋まる**（1 秒近くかかるので、絞り込みのたびに
  // 探し直さない —— 手元に持っておいて絞るのはこちらの仕事）。
  let fontChoiceRows: FontChoice[] = [];
  // 見本に出す字（＝駒に要る字）。**Go 側が返したものをそのまま使う**
  // （`core/shogifont.Required`。フロントで並べ直さないこと）。
  let fontSampleText = "";

  const setFontStatus = (msg: string, kind: "" | "error" | "warn" = "") => {
    fontStatus.textContent = msg;
    fontStatus.classList.toggle("is-error", kind === "error");
    fontStatus.classList.toggle("is-warn", kind === "warn");
    // ⚠️ **知らせるときは畳んでいても開く。** 畳めるようにした以上、
    // 起動時に焼けなかった等の理由が**閉じた中に隠れてはいけない**。
    if (msg && kind) {
      fontFold.open = true;
    }
  };

  // 一覧の 1 行。**先頭は必ず同梱**（id は空文字）で、ラジオで 1 つだけ選ぶ。
  //
  // ⚠️ **「使う」を行の中のチェックにしないこと。** 盤は 1 つしかなく駒の字も
  // 同時に 1 つしか使えないので、**2 つに印が付いている状態を作れてはいけない**
  // （エンジンの `Enabled` とは性格が違う）。
  const fontRow = (
    opts: {
      id: string;
      name: string;
      custom?: boolean;
      file?: string;
      note?: string;
      ok: boolean;
      copyright?: string;
      license?: string;
      licenseUrl?: string;
    },
    selected: boolean,
  ): HTMLLIElement => {
    const li = document.createElement("li");
    li.className = "engine-row font-row";
    li.classList.toggle("is-selected", selected);

    const head = document.createElement("label");
    head.className = "font-row-head";
    const radio = document.createElement("input");
    radio.type = "radio";
    radio.name = "piece-font";
    radio.checked = selected;
    // ⚠️ **使えない登録でも選べること。** フォントを入れ直せばそのまま戻るので、
    // 選択ごと奪うと「入れ直したのに戻らない」に見える（理由は下の行に出る）。
    radio.addEventListener("change", () => {
      void useFont(opts.id);
    });
    head.appendChild(radio);

    if (opts.id === "") {
      const span = document.createElement("span");
      span.className = "font-row-name";
      span.textContent = opts.name;
      head.appendChild(span);
    } else {
      // ⚠️ **既定の名前を value に入れないこと**（エンジンの行と同じ）。
      // 入れると、元フォントの名前が変わっても追従しなくなる。
      const input = document.createElement("input");
      input.type = "text";
      input.className = "font-row-name-input";
      input.placeholder = opts.name;
      input.value = opts.custom ? opts.name : "";
      input.title = "この登録に付ける名前（空にすると元フォントの名前に戻ります）";
      input.addEventListener("change", () => {
        void renameFont(opts.id, input.value);
      });
      head.appendChild(input);
    }
    li.appendChild(head);

    const meta = document.createElement("div");
    meta.className = "font-row-meta";
    if (opts.file) {
      const file = document.createElement("span");
      file.className = "font-row-file";
      file.textContent = opts.file;
      meta.appendChild(file);
    }
    // 権利表記。**何に由来する字かを利用者が判断できるように出す。**
    // ⚠️ **空でも「制約が無い」ではない**ので、無いときに「自由に使えます」とは書かない。
    const rights = [opts.copyright, opts.license].filter(Boolean).join(" / ");
    if (rights) {
      const span = document.createElement("span");
      span.className = "font-row-rights";
      span.textContent = rights;
      span.title = rights + (opts.licenseUrl ? `\n${opts.licenseUrl}` : "");
      meta.appendChild(span);
    }
    if (meta.childElementCount > 0) {
      li.appendChild(meta);
    }

    if (opts.note) {
      const note = document.createElement("p");
      note.className = "status is-warn";
      note.textContent = opts.note;
      li.appendChild(note);
    }

    if (opts.id !== "") {
      const actions = document.createElement("div");
      actions.className = "font-row-actions";
      const del = document.createElement("button");
      del.type = "button";
      del.className = "ghost-btn";
      del.textContent = "削除";
      del.title = "この登録を消します（フォントそのものは消えません）";
      del.addEventListener("click", () => {
        void removeFont(opts.id);
      });
      actions.appendChild(del);
      li.appendChild(actions);
    }
    return li;
  };

  const showFontState = (st: FontState) => {
    fontRequired.textContent = st.required;
    fontSampleText = st.required;
    // ⚠️ **当てるのはここ 1 か所。** 盤も駒台も候補手の重ね表示も
    // `--shogi-font` を見ているので、要素ごとに書かない。
    applyPieceFont(st.face);
    // 王/玉と馬/左馬。**盤（属性）と自前の駒（CSS 変数）を一緒に当てる**
    // ——別々に当てると「盤は玉なのに掴むと王」になる。
    applyPieceStyle(st.style, pieceBoards);

    // 選択肢は Go 側が返したものをそのまま並べる（**フロントに表を書かない**）。
    if (fontGyoku.options.length !== (st.gyokuOptions ?? []).length) {
      fontGyoku.replaceChildren();
      for (const o of st.gyokuOptions ?? []) {
        const opt = document.createElement("option");
        opt.value = o.value;
        opt.textContent = o.label;
        fontGyoku.appendChild(opt);
      }
    }
    // ⚠️ **Go 側が倒した結果をそのまま入れること**（知らない値は「王のまま」に
    // 倒して返ってくるので、選び直されたことが画面に出る）。
    fontGyoku.value = st.gyoku;
    fontHidariUma.checked = st.hidariUma;
    // 字の色と濃さ。⚠️ **既定も下限も Go 側が解決して返す**ので、
    // フロントに数値を書かない（丸めた結果もそのまま返るので、画面に出る）。
    fontInkColor.value = st.pieceColor;
    fontInkOpacity.min = String(Math.round(st.minPieceOpacity * 100));
    fontInkOpacity.value = String(Math.round(st.pieceOpacity * 100));
    fontInkOpacityValue.textContent = `${Math.round(st.pieceOpacity * 100)}%`;
    // ⚠️ **見本の色をここで当てないこと。** `.font-sample` も CSS で
    // `--shogi-piece-color` を見ているので、当てると同じ値を 2 経路で書くことになる
    // （実際に盤へ出る濃さのまま見える、という狙いは CSS 側で満たされている）。

    // 畳んでいるときは「今どの書体か」だけ見出しの右に出す。
    fontFoldSum.textContent =
      st.current === ""
        ? st.builtinName
        : (st.fonts ?? []).find((f) => f.id === st.current)?.name ?? st.builtinName;

    fontList.replaceChildren();
    fontList.appendChild(
      fontRow({ id: "", name: st.builtinName, ok: true }, st.current === ""),
    );
    for (const f of st.fonts ?? []) {
      fontList.appendChild(
        fontRow(
          {
            id: f.id, name: f.name, custom: f.custom, file: f.file,
            note: f.note, ok: f.ok,
            copyright: f.copyright, license: f.license, licenseUrl: f.licenseUrl,
          },
          st.current === f.id,
        ),
      );
    }
    // ⚠️ **焼けなくてもエラーにしない**（同梱の字で描けている。設計原則3）。
    if (st.note) {
      setFontStatus(st.note, "warn");
    } else {
      setFontStatus("");
    }
    // 一覧が開きっぱなしなら、登録済みの印を取り直す。
    if (!fontPicker.hidden) {
      const registered = new Set((st.fonts ?? []).map((f) => `${f.path.toLowerCase()}|${f.index}`));
      for (const c of fontChoiceRows) {
        c.registered = registered.has(`${c.path.toLowerCase()}|${c.index}`);
      }
      renderFontChoices();
    }
  };

  // 端末のフォントの一覧を描く。**足りないものも出して、選べない見た目にするだけ**
  // （消すと、探しているのか対象外なのかが画面から分からない）。
  const renderFontChoices = () => {
    const q = fontFilter.value.trim().toLowerCase();
    const onlyUsable = fontOnlyUsable.checked;
    const rows = fontChoiceRows.filter((c) => {
      if (onlyUsable && c.missing) {
        return false;
      }
      if (!q) {
        return true;
      }
      return (
        c.name.toLowerCase().includes(q) ||
        c.file.toLowerCase().includes(q) ||
        (c.family ?? "").toLowerCase().includes(q)
      );
    });

    fontChoices.replaceChildren();
    if (rows.length === 0) {
      const li = document.createElement("li");
      li.className = "font-choice is-empty";
      li.textContent = fontChoiceRows.length === 0
        ? "フォントが見つかりませんでした。"
        : "絞り込みに合うフォントがありません。";
      fontChoices.appendChild(li);
      return;
    }

    for (const c of rows) {
      const li = document.createElement("li");
      li.className = "font-choice";
      li.classList.toggle("is-unusable", !!c.missing);

      const name = document.createElement("span");
      name.className = "font-choice-name";
      name.textContent = c.name;
      li.appendChild(name);

      const meta = document.createElement("span");
      meta.className = "font-choice-meta";
      // ⚠️ **ファイル名も出すこと。** 同じ名前の書体が別のファイルに入っていることが
      // あるうえ、日本語名を持たないフォントはファイル名が唯一の手掛かりになる。
      meta.textContent = c.family ? `${c.family} — ${c.file}` : c.file;
      meta.title = c.path;
      li.appendChild(meta);

      if (c.missing) {
        const why = document.createElement("span");
        why.className = "font-choice-why";
        why.textContent = `字が足りません: ${c.missing}`;
        why.title = `駒に要る字のうち ${c.missing} がこのフォントにありません`;
        li.appendChild(why);
      } else {
        const actions = document.createElement("span");
        actions.className = "font-choice-actions";

        const preview = document.createElement("button");
        preview.type = "button";
        preview.className = "ghost-btn";
        preview.textContent = "見本";
        preview.title = "このフォントで駒の字を焼いて、下に見本を出します（登録はしません）";
        preview.addEventListener("click", () => {
          void previewFont(c);
        });
        actions.appendChild(preview);

        const use = document.createElement("button");
        use.type = "button";
        use.className = "ghost-btn";
        use.textContent = c.registered ? "これにする" : "追加して使う";
        use.title = c.registered
          ? "この書体は登録済みです。押すと駒の字をこれに切り替えます"
          : "この書体を登録して、駒の字をこれに切り替えます";
        use.addEventListener("click", () => {
          void addFont(c);
        });
        actions.appendChild(use);

        li.appendChild(actions);
      }
      fontChoices.appendChild(li);
    }
  };

  // 見本。**実際に焼いてから当てる**ので、盤に出る字そのものになる。
  // ⚠️ **family は Go 側が毎回変えて返す** —— 同じ名前で焼き直すと、
  // どちらが当たるかがブラウザ任せになって**前のフォントのまま**に見えることがある。
  const previewFont = async (c: FontChoice) => {
    setFontStatus(`「${c.name}」の駒の字を作っています…`);
    try {
      const face = await FontService.Preview(c.path, c.index);
      registerFontFace("shinte-piece-font-preview", face.family, face.dataUrl);
      fontSample.hidden = false;
      fontSample.style.fontFamily = `"${face.family}", serif`;
      fontSample.textContent = fontSampleText;
      fontSample.title = `${c.name}（${c.file}）`;
      setFontStatus(`「${c.name}」の見本です。使うには「追加して使う」を押してください。`);
    } catch (err) {
      setFontStatus(`見本を作れませんでした: ${String(err)}`, "error");
    }
  };

  const addFont = async (c: FontChoice) => {
    setFontStatus(`「${c.name}」を登録しています…`);
    try {
      showFontState(await FontService.Add(c.path, c.index));
      setFontStatus(`駒の字を「${c.name}」にしました。`);
    } catch (err) {
      setFontStatus(`登録できませんでした: ${String(err)}`, "error");
    }
  };

  const useFont = async (id: string) => {
    try {
      showFontState(await FontService.Use(id));
    } catch (err) {
      setFontStatus(`切り替えられませんでした: ${String(err)}`, "error");
      // 画面を設定ファイルの内容に戻す（食い違ったまま使わせない）。
      try {
        showFontState(await FontService.State());
      } catch {
        /* 読み直せないなら画面はそのまま。理由は上に出ている。 */
      }
    }
  };

  const removeFont = async (id: string) => {
    try {
      showFontState(await FontService.Remove(id));
    } catch (err) {
      setFontStatus(`消せませんでした: ${String(err)}`, "error");
    }
  };

  const renameFont = async (id: string, name: string) => {
    try {
      showFontState(await FontService.Rename(id, name));
    } catch (err) {
      setFontStatus(`名前を変えられませんでした: ${String(err)}`, "error");
    }
  };

  // ⚠️ **押したときだけ探す**（実測 1 秒弱）。**待ちを出すこと** ——
  // 押しても何も起きない時間があると、壊れているように見える。
  fontScanBtn.addEventListener("click", () => {
    void (async () => {
      fontScanBtn.disabled = true;
      const label = fontScanBtn.textContent;
      fontScanBtn.textContent = "探しています…";
      setFontStatus("端末に入っているフォントを探しています…");
      try {
        const scan = await FontService.Scan();
        fontChoiceRows = scan.fonts ?? [];
        fontPicker.hidden = false;
        fontDirs.textContent = `探した場所: ${(scan.dirs ?? []).join(" / ")}`;
        renderFontChoices();
        setFontStatus(
          `${fontChoiceRows.length} 書体のうち、${scan.usable} 書体で駒の字を作れます。`,
        );
      } catch (err) {
        setFontStatus(`フォントを探せませんでした: ${String(err)}`, "error");
      } finally {
        fontScanBtn.disabled = false;
        fontScanBtn.textContent = label;
      }
    })();
  });

  fontFilter.addEventListener("input", renderFontChoices);
  fontOnlyUsable.addEventListener("change", renderFontChoices);

  // 王/玉・左馬。**変えたその場で保存して、その場で盤に出る**
  // （設定タブの他の項目と同じで、適用ボタンは置かない）。
  fontGyoku.addEventListener("change", () => {
    void (async () => {
      try {
        showFontState(await FontService.SetGyoku(fontGyoku.value));
      } catch (err) {
        setFontStatus(`保存できませんでした: ${String(err)}`, "error");
      }
    })();
  });
  fontHidariUma.addEventListener("change", () => {
    void (async () => {
      try {
        showFontState(await FontService.SetHidariUma(fontHidariUma.checked));
      } catch (err) {
        setFontStatus(`保存できませんでした: ${String(err)}`, "error");
      }
    })();
  });

  // 字の色と濃さ。**色と濃さを 1 回で送る**（設定を書く経路を 2 つに分けない）。
  //
  // ⚠️ **`rgba` の合成をここに書かないこと。** 当てる値を作るのは Go 側
  // （`ikkyoku.PieceInk`）だけで、**引いている最中の見た目のためだけに
  // フロントで作り直すと、丸め方が食い違ったときに「離した瞬間に色が飛ぶ」**
  // という追いにくい壊れ方をする（`Score.Label` / 折れ線の色と同じ理由）。
  //
  // 代わりに**送るのを間引く**。引いている最中も反映されるが、
  // 設定ファイルへの書き込みは止まったときの 1 回で済む。
  let inkTimer = 0;
  const saveInk = async (color: string, opacity: number) => {
    window.clearTimeout(inkTimer);
    try {
      showFontState(await FontService.SetPieceInk(color, opacity));
    } catch (err) {
      setFontStatus(`保存できませんでした: ${String(err)}`, "error");
    }
  };
  const saveInkSoon = () => {
    window.clearTimeout(inkTimer);
    inkTimer = window.setTimeout(() => {
      void saveInk(fontInkColor.value, Number(fontInkOpacity.value) / 100);
    }, 150);
  };

  fontInkOpacity.addEventListener("input", () => {
    // 数字だけは即座に動かす（**これは合成ではないので Go を待たなくてよい**）。
    fontInkOpacityValue.textContent = `${fontInkOpacity.value}%`;
    saveInkSoon();
  });
  fontInkColor.addEventListener("input", saveInkSoon);
  // 離したときは待たずに送る（**間引きの取りこぼしを残さない**）。
  for (const el of [fontInkOpacity, fontInkColor]) {
    el.addEventListener("change", () => {
      void saveInk(fontInkColor.value, Number(fontInkOpacity.value) / 100);
    });
  }
  fontInkReset.addEventListener("click", () => {
    // ⚠️ **既定の値をここに書かないこと。** 空を送れば Go 側が既定に倒す。
    void saveInk("", 0);
  });

  // 認識器の読み込み元を画面に映す。
  //
  // ⚠️ **既定の解決（空なら auto）は Go 側**（`Config.SutemeSourceOr`）。
  // 返ってきた値をそのまま入れるだけにすること。
  const showSutemeSource = (s: {
    sutemeSource: string;
    sutemeDataDir: string;
    sutemeEmbedAvailable: boolean;
    sutemeEmbedSource: string;
  }) => {
    sutemeSource.value = s.sutemeSource;
    sutemeDataDir.value = s.sutemeDataDir;
    const embedOption = sutemeSource.querySelector<HTMLOptionElement>('option[value="embed"]')!;
    // 焼き込みの無いビルドでは選ばせない。**選択肢ごと消さない** ——
    // 設定ファイルが "embed" のまま開かれることがあり、消すと選択が勝手に変わる。
    embedOption.disabled = !s.sutemeEmbedAvailable;
    embedOption.textContent = s.sutemeEmbedAvailable
      ? `このアプリに焼き込んだデータ（${s.sutemeEmbedSource || "出所不明"}）`
      : "このアプリに焼き込んだデータ（このビルドには入っていません）";
    // ディレクトリ欄は「焼き込みだけを使う」ときも残す（戻すときに打ち直させない）。
    sutemeDataDir.disabled = false;
    if (!s.sutemeEmbedAvailable && s.sutemeSource === "embed") {
      sutemeSourceNote.textContent =
        "このビルドには認識器が焼き込まれていないため、ディレクトリから読みます。";
    } else if (s.sutemeSource === "embed") {
      sutemeSourceNote.textContent =
        "アプリに焼き込んだデータを使います。suteme のリポジトリが無くても動きます。";
    } else if (s.sutemeSource === "dir") {
      sutemeSourceNote.textContent = s.sutemeDataDir
        ? "指定したディレクトリから読みます。データを更新したら「認識器を読み込み直す」で反映されます。"
        : "ディレクトリが空なので、suteme 既定の探索（カレント → 実行ファイルの隣）に任せます。";
    } else {
      sutemeSourceNote.textContent = s.sutemeDataDir
        ? "ディレクトリを指定してあるので、そちらから読みます。"
        : s.sutemeEmbedAvailable
          ? "ディレクトリが空なので、アプリに焼き込んだデータを使います。"
          : "ディレクトリが空で焼き込みも無いため、suteme 既定の探索に任せます。";
    }
  };

  const showSettings = (s: {
    fitOnStartup: boolean;
    clickThrough: boolean;
    // 評価値グラフを別ウィンドウに切り離しているか（2026-09-08）。
    evalGraphDetached: boolean;
    // **候補手の面**を別ウィンドウに切り離しているか（2026-09-08）。
    studyPaneDetached: boolean;
    // **手順の面**を別ウィンドウに切り離しているか（2026-09-12）。
    // ⚠️ **候補手とは別の設定**（両方外に出すと右の列そのものが消える）。
    movePaneDetached: boolean;
    // 勝率バー（評価値バー）を隠しているか（2026-09-10。黒地の右クリック）。
    hideWinRateBar: boolean;
    // 対局者名を隠しているか（2026-09-10。⚠️ **帯とは別の設定**）。
    hidePlayerNames: boolean;
    path: string;
    training: { enabled: boolean; host: string; port: number; token: string; target: string };
    engines: EngineSettings[] | null;
    engineColors: EngineColorOption[] | null;
    analyzeSeconds: number;
    ponanzaConstant: number;
    sutemeSource: string;
    sutemeDataDir: string;
    sutemeEmbedAvailable: boolean;
    sutemeEmbedSource: string;
    kifuDbPath: string;
  }) => {
    fitOnStartup.checked = s.fitOnStartup;
    clickThrough.checked = s.clickThrough;
    showSutemeSource(s);
    // ⚠️ **既定の解決は Go 側**（`Config.KifuDB`）。返ってきた場所をそのまま入れる。
    kifuDBPath.value = s.kifuDbPath;
    void refreshKifuDBNote();
    // ⚠️ **既定値の解決は Go 側**（`analyze.PonanzaConstantOr`）。返ってきた値を
    // そのまま入れるだけにすること（フロントに既定を書くと 2 か所に散る）。
    ponanzaConstant.value = String(s.ponanzaConstant);
    settingsPath.textContent = s.path || "(保存先を決められませんでした)";
    // エンジンの色（評価値グラフ・見出しの色見本）。**設定が唯一の出所**で、
    // 既定色の解決も Go 側が済ませてある（`EngineSettings.Color` は常に入っている）。
    // ⚠️ **評価値グラフの置き場所も設定から受け取る**（2026-09-08）。
    // Go 側は起動時にも `graph:detached` を出すが、**その時点でフロントはまだ
    // 購読していない**ので、起動直後の形はここで決まる。
    applyGraphDetached(!!s.evalGraphDetached);
    applySideDetached(!!s.studyPaneDetached);
    applyMovesDetached(!!s.movePaneDetached);
    // ⚠️ **盤の上の 1 行の出し入れも設定から受け取る**（2026-09-10）。切り替えは
    // 黒地の右クリックだが、**起動直後にどちらで始まるかはここで決まる。**
    winrateHidden = !!s.hideWinRateBar;
    playersHidden = !!s.hidePlayerNames;
    applyStudyTopRow();
    // ⚠️ **評価値グラフの折れ線の色はここが持つ**（側の列とは別）。
    // 切り離すと側の列は別の窓に居るので、**あちらから引けない**。
    graphColors.clear();
    for (const e of s.engines ?? []) {
      graphColors.set(e.id, e.color);
    }
    // ⚠️ **側の列へは押し込むこと**（色・名前・候補手の本数・考える秒数）。
    // **設定が唯一の出所**で、既定色の解決も Go 側が済ませてある。
    sidePane.setEngines(s.engines ?? [], s.engineColors ?? [], s.analyzeSeconds);
    showEngineList(s.engines ?? []);
    // 既定値の解決は Go 側(training パッケージ)が済ませて返す。**フロントに
    // 既定値を書かないこと**(2 か所に持つと、既定を変えたときに食い違う)。
    const t = s.training;
    trainEnabledInput.checked = t.enabled;
    trainHost.value = t.host;
    trainPort.value = String(t.port);
    trainToken.value = t.token;
    // 送信ボタンを出すかどうかはこの設定で決まる。
    trainEnabled = t.enabled;
    syncTrain();
  };

  // 接続設定の保存。**接続の確認はしない**(保存と疎通は別の操作)。
  // 先に設定を入れてから suteme を起動する、という順序が普通にあるため。
  const saveTraining = async () => {
    const port = Number(trainPort.value);
    if (!Number.isInteger(port) || port < 1 || port > 65535) {
      settingsStatus.textContent = "ポート番号は 1〜65535 で指定してください。";
      settingsStatus.classList.add("is-error");
      return;
    }
    settingsStatus.textContent = "";
    settingsStatus.classList.remove("is-error");
    try {
      showSettings(
        await SettingsService.SetTraining(
          trainEnabledInput.checked,
          trainHost.value,
          port,
          trainToken.value,
        ),
      );
    } catch (err) {
      settingsStatus.textContent = `設定を保存できませんでした: ${String(err)}`;
      settingsStatus.classList.add("is-error");
      // 画面を設定ファイルの内容に戻す(食い違ったまま使わせない)。
      try {
        showSettings(await SettingsService.Settings());
      } catch {
        /* 読み直せないなら画面はそのまま。理由は上に出ている。 */
      }
    }
  };

  for (const el of [trainEnabledInput, trainHost, trainPort, trainToken]) {
    el.addEventListener("change", () => {
      void saveTraining();
    });
  }

  // 勝率の定数の保存。**空欄なら既定に戻す**（0 を渡すと Go 側が既定に倒す）。
  //
  // ⚠️ **次に解析したときから効く。** 走っている解析の途中経過は、そのときの
  // 定数で計算された値なので変わらない（`AnalyzeService.Start` が設定を読む）。
  ponanzaConstant.addEventListener("change", () => {
    void (async () => {
      const raw = ponanzaConstant.value.trim();
      const v = raw === "" ? 0 : Number(raw);
      if (!Number.isFinite(v) || v < 0) {
        settingsStatus.textContent = "ポナンザ定数は正の数で指定してください。";
        settingsStatus.classList.add("is-error");
        return;
      }
      settingsStatus.textContent = "";
      settingsStatus.classList.remove("is-error");
      try {
        showSettings(await SettingsService.SetPonanzaConstant(v));
      } catch (err) {
        settingsStatus.textContent = `設定を保存できませんでした: ${String(err)}`;
        settingsStatus.classList.add("is-error");
        try {
          showSettings(await SettingsService.Settings());
        } catch {
          /* 読み直せないなら画面はそのまま。理由は上に出ている。 */
        }
      }
    })();
  });

  // ---- 解析エンジンの一覧 --------------------------------------------------
  //
  // **保存と接続の確認は別の操作。** まだ置いていないパスを先に書いておく、という
  // 順序が普通にあるので、保存時に起動はしない（存在の確認だけ Go 側でする）。
  //
  // **変えたその場で保存する**（他の設定と同じ。適用ボタンは置かない）。
  const engineFailed = (err: unknown) => {
    engineStatus.textContent = String(err instanceof Error ? err.message : err);
    engineStatus.classList.add("is-error");
    // 畳んでいても開く（理由が閉じた中に隠れてはいけない）。
    engineFold.open = true;
  };

  // 設定の書き換えはどれも AppSettings を返すので、返ってきたものでそのまま描き直す。
  // ⚠️ **フロントに一覧の写しを持たないこと**（局面と同じ理由。ずれたときに
  // どちらが本当か分からなくなる）。
  const applyEngineChange = async (op: () => Promise<AppSettings>) => {
    engineStatus.classList.remove("is-error");
    try {
      showSettings(await op());
    } catch (err) {
      engineFailed(err);
      // 画面を設定ファイルの内容に戻す（食い違ったまま使わせない）。
      try {
        showSettings(await SettingsService.Settings());
      } catch {
        /* 読み直せないなら画面はそのまま。理由は上に出ている。 */
      }
    }
  };

  // 1 行ぶんの結果表示（「接続を確認」の答えと、保存できなかった理由）。
  const engineRowNote = (row: HTMLElement) =>
    row.querySelector<HTMLElement>(".engine-note")!;

  // 行を id で引き直す。⚠️ **要素を覚えておかないこと** ——「接続を確認」の途中で
  // 設定を読み直して**一覧ごと描き直す**ので、掴んでいた要素は捨てられている
  // （候補手の選択を「エンジン + 順位」で持っているのと同じ話）。
  const engineRow = (id: string) =>
    engineList.querySelector<HTMLElement>(`.engine-row[data-id="${id}"]`);

  // エンジン 1 つに実際に繋いでみる。**1 行ずつ**（まとめて起こすと、どれが遅くて
  // どれが落ちたのか分からない）。確かめたら閉じるので、プロセスは残らない。
  //
  // ⚠️ **繋がったら設定を読み直す。** Go 側がこのときに option の宣言を控えるので、
  // 読み直さないと**設定項目が画面に出てくるのが次に設定タブを開いたとき**になる
  // （押した結果が見えないと、効いたのかどうか分からない）。
  const checkEngine = async (id: string, btn: HTMLButtonElement) => {
    const note = engineRowNote(engineRow(id)!);
    btn.disabled = true;
    note.classList.remove("is-error");
    note.textContent = "起動して確かめています…";
    try {
      const r = await AnalyzeService.CheckEngine(id);
      if (!r.ok) {
        note.textContent = `繋がりません: ${r.error}`;
        note.classList.add("is-error");
        return;
      }
      // 宣言を控えたぶんを画面に出す（**この呼び出しで一覧が描き直される**）。
      try {
        openOptions.add(id); // 読めた設定項目をそのまま開いて見せる
        showSettings(await SettingsService.Settings());
      } catch {
        /* 読み直せないなら一覧はそのまま。結果は下に出る。 */
      }
      const after = engineRow(id);
      if (!after) return; // 行ごと消えた（削除された）。出す先が無い
      const note2 = engineRowNote(after);
      // **何を送ったかまで出す。** setoption には応答が返らないので、
      // 効いているかどうかを確かめる手掛かりがこれしかない。
      const applied =
        r.options > 0
          ? `option ${r.options} 件を宣言、${r.applied} 件を送信（既定値を含む）`
          : "option の宣言はありません";
      // ⚠️ **起動の時間は「解析タブで最初に解析するとき」に 1 回払う**
      // （そのあとは接続を使い回す）。繋ぎ先を選ぶ材料になるので出しておく。
      const startup = `起動 ${(r.startupMs / 1000).toFixed(1)} 秒（解析タブで最初に解析するときにかかります）`;
      note2.textContent = `繋がりました: ${r.name} / ${applied} / ${startup}`;
    } catch (err) {
      const now = engineRow(id);
      if (!now) return;
      const n = engineRowNote(now);
      n.textContent = `確認できませんでした: ${String(err instanceof Error ? err.message : err)}`;
      n.classList.add("is-error");
    } finally {
      // ⚠️ **押したボタンではなく、今そこにあるボタンを戻す**（描き直しで別物になっている）。
      const now = engineRow(id)?.querySelector<HTMLButtonElement>(".engine-check");
      if (now) now.disabled = false;
      btn.disabled = false;
    }
  };

  // フォーカスを戻す相手を引くクラス名を選ぶ。
  //
  // ⚠️ **`engine-` で始まるクラスを優先すること。** 素朴に「最初のクラス」で引くと、
  // `ghost-btn engine-check` のような**見た目のクラスが先に来ているボタン**で
  // **同じ行の別のボタン（参照…）に戻ってしまう**。役割を表しているのは後ろのほう。
  const keepId = (cls: string) => {
    const names = cls.split(" ").filter(Boolean);
    return names.find((n) => n.startsWith("engine-")) ?? names[0] ?? "";
  };

  // 開いているエンジンの設定（折りたたみ）。⚠️ **画面だけの状態なので Go に持たせない**
  // （手順の畳み方と同じ扱い）。一覧は保存のたびに描き直されるので、
  // **覚えておかないと値を 1 つ変えるたびに閉じる。**
  const openOptions = new Set<string>();

  // option 1 つぶんの入力欄を作る。**型ごとに形を変える**のがここの仕事:
  //
  //   check              → チェックボックス
  //   spin               → 数値（min/max つき）
  //   combo              → 選択（var の並び）
  //   string / filename  → テキスト
  //   button             → **押すだけ**。値を持たないので設定できない（下記）
  //
  // ⚠️ **既定値も範囲も選択肢も Go 側が渡したものをそのまま使う。** エンジンごとに
  // 違うので、フロントに表を書くと必ず食い違う。
  const engineOptionRow = (
    id: string,
    o: NonNullable<EngineSettings["options"]>[number],
  ): HTMLElement => {
    const box = document.createElement("div");
    box.className = "engine-option";
    box.dataset.name = o.name;
    box.classList.toggle("is-custom", o.custom);

    const label = document.createElement("label");
    label.className = "engine-option-name";
    label.textContent = o.name;
    // 何を送る項目なのかは名前だけでは分からないので、型と既定値を添える。
    label.title =
      `${o.name}（${o.known ? o.type : "宣言に無い項目"}）` +
      (o.known && o.default !== "" ? ` / 既定: ${o.default}` : "") +
      (o.hasMin || o.hasMax
        ? ` / 範囲: ${o.hasMin ? o.min : ""}〜${o.hasMax ? o.max : ""}`
        : "");
    box.appendChild(label);

    // 保存は**その場で**（設定タブの他の項目と同じ。適用ボタンを置かない）。
    const save = (value: string) => {
      void applyEngineChange(() => SettingsService.SetEngineOption(id, o.name, value));
    };

    if (o.type === "button") {
      // ⚠️ **button は値を持たない**（送ること自体が「押した」という動作）。
      // 押せる相手は**繋がっているエンジン**だけで、ここには居ない
      // （接続は解析タブに居るあいだしか生きていない）。**設定として保存しない。**
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = "ghost-btn engine-option-input";
      btn.textContent = o.name;
      btn.disabled = true;
      btn.title = "押すだけの項目です（値を持たないので、ここでは設定できません）";
      box.appendChild(btn);
      return box;
    }

    let input: HTMLInputElement | HTMLSelectElement;
    if (o.type === "check") {
      const el = document.createElement("input");
      el.type = "checkbox";
      el.checked = o.value === "true";
      el.addEventListener("change", () => save(el.checked ? "true" : "false"));
      input = el;
    } else if (o.type === "combo" && (o.vars ?? []).length > 0) {
      const el = document.createElement("select");
      for (const v of o.vars ?? []) {
        const opt = document.createElement("option");
        opt.value = v;
        opt.textContent = v;
        el.appendChild(opt);
      }
      el.value = o.value;
      el.addEventListener("change", () => save(el.value));
      input = el;
    } else if (o.type === "spin") {
      const el = document.createElement("input");
      el.type = "number";
      if (o.hasMin) el.min = String(o.min);
      if (o.hasMax) el.max = String(o.max);
      el.value = o.value;
      // ⚠️ **change（確定時）で拾う**（設定タブのテキスト欄と同じ）。
      // input だと 1 文字ごとに保存して、打っている途中の値で弾かれる。
      el.addEventListener("change", () => save(el.value));
      input = el;
    } else {
      const el = document.createElement("input");
      el.type = "text";
      el.spellcheck = false;
      el.value = o.value;
      el.placeholder = o.default;
      el.addEventListener("change", () => save(el.value));
      input = el;
    }
    input.className = "engine-option-input";
    box.appendChild(input);

    // 既定に戻す口。⚠️ **人が変えた項目にだけ出す**（押せるものが常に並んでいると、
    // どれを変えたのかが分からない）。宣言に無い項目では「消す」ことになる。
    if (o.custom) {
      const reset = document.createElement("button");
      reset.type = "button";
      reset.className = "engine-option-reset";
      reset.textContent = o.known ? "既定" : "消す";
      reset.title = o.known
        ? `既定（${o.default === "" ? "空" : o.default}）に戻します`
        : "この項目を設定から消します";
      reset.addEventListener("click", () => save(""));
      box.appendChild(reset);
    }
    if (!o.known) {
      const note = document.createElement("span");
      note.className = "engine-option-note note";
      note.textContent = "宣言に無い項目";
      note.title =
        "エンジンが宣言していない option です（設定ファイルに書いたものか、" +
        "実行ファイルを差し替えて宣言だけ捨てたもの）。値はそのまま送ります。";
      box.appendChild(note);
    }
    return box;
  };

  // エンジン 1 つぶんの設定（行の中の折りたたみ）。
  //
  // ⚠️ **既定で閉じる。** 数十件あるエンジンが普通なので、開きっぱなしにすると
  // 一覧が読めない。⚠️ **見出しに「何件変えているか」を出すこと** ——
  // 畳んだままでも、既定のままなのかどうかが分かる必要がある。
  const engineOptionsBox = (e: EngineSettings): HTMLElement => {
    const box = document.createElement("details");
    box.className = "engine-options";
    box.open = openOptions.has(e.id);
    box.addEventListener("toggle", () => {
      if (box.open) openOptions.add(e.id);
      else openOptions.delete(e.id);
    });

    const head = document.createElement("summary");
    head.textContent = "エンジンの設定";
    const count = document.createElement("span");
    count.className = "note";
    const options = e.options ?? [];
    count.textContent = e.optionsKnown
      ? `（${options.length} 項目${e.optionCount > 0 ? ` / ${e.optionCount} 件を変更中` : ""}）`
      : options.length > 0
        ? `（${options.length} 件。宣言は未取得）`
        : "（未取得）";
    head.appendChild(count);
    box.appendChild(head);

    const body = document.createElement("div");
    body.className = "engine-options-body";
    if (!e.optionsKnown) {
      // ⚠️ **「宣言が無い」と言い切らないこと。** まだ繋いでいないだけかもしれない。
      const hint = document.createElement("p");
      hint.className = "note";
      hint.textContent =
        "「接続を確認」を押すと、このエンジンが受け付ける設定項目を読み込みます。";
      body.appendChild(hint);
    } else if (options.length === 0) {
      const hint = document.createElement("p");
      hint.className = "note";
      hint.textContent = "このエンジンは設定項目を宣言していません。";
      body.appendChild(hint);
    }
    for (const o of options) body.appendChild(engineOptionRow(e.id, o));

    if (e.optionCount > 0) {
      const reset = document.createElement("button");
      reset.type = "button";
      reset.className = "ghost-btn engine-options-reset";
      reset.textContent = "全部を既定に戻す";
      reset.title = "設定した値を全部捨てて、エンジンの既定値に戻します";
      reset.addEventListener("click", () => {
        void applyEngineChange(() => SettingsService.ResetEngineOptions(e.id));
      });
      body.appendChild(reset);
    }
    box.appendChild(body);
    return box;
  };

  // 一覧を描き直す。
  //
  // ⚠️ **入力中の欄は上書きしない。** 保存のたびに描き直すので、打っている途中の
  // 名前やパスが飛ぶ（フォーカスのある行だけ残す）。
  const showEngineList = (engines: EngineSettings[]) => {
    const active = document.activeElement as HTMLElement | null;
    const keep = active?.closest<HTMLElement>(".engine-row")?.dataset.id;
    const keepValue = active instanceof HTMLInputElement ? active.value : "";
    const keepClass = active?.className ?? "";
    // option の欄はクラス名が全部同じなので、**どの項目だったか**も覚えておく
    // （名前で引き直す。⚠️ 落とすと、1 つ変えるたびに一覧の先頭へフォーカスが飛ぶ）。
    const keepOption = active?.closest<HTMLElement>(".engine-option")?.dataset.name;

    // 畳んでいるときは「何個登録していて、何個が走るか」だけ出す。
    const useCount = engines.filter((e) => e.enabled).length;
    engineFoldSum.textContent =
      engines.length === 0
        ? "登録なし"
        : `${engines.length} 個の登録 / 解析に使う ${useCount} 個`;

    engineList.replaceChildren();
    for (const [index, e] of engines.entries()) {
      const row = document.createElement("li");
      row.className = "engine-row";
      row.dataset.id = e.id;
      row.innerHTML = `
        <!-- 並べ替え。**押した位置に答えが出る**ように行の先頭に置く。
             ⚠️ ドラッグにしていないのは、行の中に入力欄が 2 つあって掴む場所が
             残らないため（掴み手を別に作るくらいなら、押せば動くほうが速い）。 -->
        <div class="engine-order">
          <button class="engine-up engine-move" type="button"
                  aria-label="上へ" title="1 つ上へ">${iconMarkup(FiChevronUp)}</button>
          <button class="engine-down engine-move" type="button"
                  aria-label="下へ" title="1 つ下へ">${iconMarkup(FiChevronDown)}</button>
        </div>
        <label class="engine-use" title="解析のときにこのエンジンを使います（複数選べます）">
          <input class="engine-enabled" type="checkbox" />
          <span>解析に使う</span>
        </label>
        <input class="engine-name" type="text" spellcheck="false" />
        <input class="engine-path" type="text" spellcheck="false"
               placeholder="空なら同梱のエンジン" />
        <button class="ghost-btn engine-browse" type="button"
                title="実行ファイルを選び直します">参照…</button>
        <button class="ghost-btn engine-check" type="button"
                title="実際に起動して、USI で応答するか確かめます">接続を確認</button>
        <button class="ghost-btn engine-remove" type="button"
                title="この登録を消します">削除</button>
        <span class="engine-note note"></span>
      `;
      const enabled = row.querySelector<HTMLInputElement>(".engine-enabled")!;
      const name = row.querySelector<HTMLInputElement>(".engine-name")!;
      const path = row.querySelector<HTMLInputElement>(".engine-path")!;
      enabled.checked = e.enabled;
      // ⚠️ **既定の名前は placeholder に出し、value には入れない。**
      // 入れてしまうと、パスを変えても名前が追従しなくなる（Go 側が
      // 「人が付けた名前か」を custom で返しているのはこのため）。
      name.value = e.custom ? e.name : "";
      name.placeholder = e.name;
      path.value = e.path;
      // **同梱かどうかの判定は Go 側の値を使う**（フロントで path === "" を書かない）。
      const opts = e.optionCount > 0 ? ` / setoption ${e.optionCount} 件` : "";
      engineRowNote(row).textContent = (e.builtin ? "同梱のエンジン" : "") + opts;
      row.classList.toggle("is-off", !e.enabled);

      // 並べ替え。⚠️ **端では押せなくする**（押しても何も起きないボタンは、
      // 壊れているのか端なのかが区別できない）。回り込ませもしない。
      const up = row.querySelector<HTMLButtonElement>(".engine-up")!;
      const down = row.querySelector<HTMLButtonElement>(".engine-down")!;
      up.disabled = index === 0;
      down.disabled = index === engines.length - 1;
      up.addEventListener("click", () => {
        void applyEngineChange(() => SettingsService.MoveEngine(e.id, -1));
      });
      down.addEventListener("click", () => {
        void applyEngineChange(() => SettingsService.MoveEngine(e.id, 1));
      });

      enabled.addEventListener("change", () => {
        void applyEngineChange(() => SettingsService.SetEngineEnabled(e.id, enabled.checked));
      });
      name.addEventListener("change", () => {
        void applyEngineChange(() => SettingsService.SetEngineName(e.id, name.value));
      });
      path.addEventListener("change", () => {
        void applyEngineChange(() => SettingsService.SetEnginePath(e.id, path.value));
      });
      row.querySelector<HTMLButtonElement>(".engine-browse")!.addEventListener("click", () => {
        // 参照は「追加」ではなく**この行の差し替え**（取り消したら何も変わらない）。
        void applyEngineChange(() => SettingsService.BrowseEngineFor(e.id));
      });
      const checkBtn = row.querySelector<HTMLButtonElement>(".engine-check")!;
      checkBtn.addEventListener("click", () => {
        void checkEngine(e.id, checkBtn);
      });
      row.querySelector<HTMLButtonElement>(".engine-remove")!.addEventListener("click", () => {
        void applyEngineChange(() => SettingsService.RemoveEngine(e.id));
      });

      // エンジンの設定（option）。⚠️ **行の中に置くこと** —— どのエンジンの設定かは
      // 位置で示す（設定タブに別の区画を作ると、行と結び付かない）。
      row.appendChild(engineOptionsBox(e));

      engineList.appendChild(row);
    }

    // 打っている最中だった欄にフォーカスと文字を戻す。
    //
    // ⚠️ **ボタンにも戻すこと**（並べ替えの ▲▼）。行ごと描き直すので、戻さないと
    // **1 つ動かすたびにフォーカスが飛んで、続けて押せない**（3 つ上げたいときに
    // 毎回カーソルで押しにいくことになる）。ボタンは**役割のクラス名で引く**ので
    // （`keepId`）、`.engine-up` / `.engine-down` の順で書いてある
    // （`.engine-move` を先頭にすると下ボタンを押したのに上ボタンへ戻る）。
    if (keep) {
      const row = engineList.querySelector<HTMLElement>(`.engine-row[data-id="${keep}"]`);
      // option の欄は**その項目の中から**引く（クラス名は全部同じなので、
      // 行から引くと必ず先頭の項目に戻ってしまう）。
      const scope = keepOption
        ? [...(row?.querySelectorAll<HTMLElement>(".engine-option") ?? [])].find(
            (b) => b.dataset.name === keepOption,
          )
        : row;
      const el = scope?.querySelector<HTMLElement>(`.${keepId(keepClass)}`);
      if (el instanceof HTMLInputElement && el.type === "text") {
        el.value = keepValue;
      }
      // 端まで動かしたら押せなくなっているので、そのときは行だけ見えていればよい。
      if (el && !(el instanceof HTMLButtonElement && el.disabled)) {
        el.focus();
      }
    }
  };

  // エンジンを足す。**足したものはそのまま使える状態**にする（Go 側で enabled）。
  engineAdd.addEventListener("click", () => {
    void (async () => {
      engineAdd.disabled = true;
      engineStatus.classList.remove("is-error");
      try {
        // 取り消したときは Go 側が何も変えずに今の設定を返す。
        showSettings(await SettingsService.BrowseEngine());
      } catch (err) {
        engineFailed(err);
      } finally {
        engineAdd.disabled = false;
      }
    })();
  });

  // 同梱エンジンを足す（パスが空の登録）。**外部エンジンと並べて比べるため。**
  engineAddBuiltin.addEventListener("click", () => {
    void applyEngineChange(() => SettingsService.AddEngine(""));
  });

  // 「今このサーバに送ってよいか」の問い合わせ(`GET /api/status`)。
  //
  // ⚠️ **結果で送信を止めない。** suteme はループバックからのアクセスを受付判定の
  // 手前で素通しにするので、「登録受付は無効」でも同じマシンからなら送れる。
  // ここに出すのは状態であって、可否の判定ではない。
  trainCheck.addEventListener("click", () => {
    void (async () => {
      trainCheck.disabled = true;
      trainCheckStatus.classList.remove("is-error", "is-warn");
      trainCheckStatus.textContent = "確認しています…";
      try {
        const st = await TrainingService.Status();
        if (!st.reachable) {
          trainCheckStatus.textContent = `つながりません: ${st.error}`;
          trainCheckStatus.classList.add("is-error");
          return;
        }
        const parts = [`${st.target} に接続できました`];
        parts.push(st.accepting ? "登録受付: 有効" : "登録受付: 無効");
        if (st.detailed) {
          parts.push(`履歴 ${st.entries}/${st.capacity} 件`);
          if (st.dataVersion) {
            parts.push(st.dataVersion);
          }
        }
        trainCheckStatus.textContent = parts.join(" / ") + (st.note ? `。${st.note}` : "");
        if (st.note) {
          trainCheckStatus.classList.add("is-warn");
        }
      } catch (err) {
        trainCheckStatus.textContent = `確認できませんでした: ${String(err)}`;
        trainCheckStatus.classList.add("is-error");
      } finally {
        trainCheck.disabled = false;
      }
    })();
  });

  fitOnStartup.addEventListener("change", () => {
    void (async () => {
      const want = fitOnStartup.checked;
      fitOnStartup.disabled = true;
      settingsStatus.textContent = "";
      settingsStatus.classList.remove("is-error");
      try {
        showSettings(await SettingsService.SetFitOnStartup(want));
        settingsStatus.textContent = want
          ? "次の起動から、盤面を探してガイド枠を合わせます。"
          : "起動時には探しません。枠のツールバーの □ からはいつでも実行できます。";
      } catch (err) {
        fitOnStartup.checked = !want;
        settingsStatus.textContent = `設定を保存できませんでした: ${String(err)}`;
        settingsStatus.classList.add("is-error");
      } finally {
        fitOnStartup.disabled = false;
      }
    })();
  });

  // 枠の内側の素通し。**切り替えたその場で効く**（起動時に盤面を探す、とはそこが違う）。
  clickThrough.addEventListener("change", () => {
    void (async () => {
      const want = clickThrough.checked;
      clickThrough.disabled = true;
      settingsStatus.textContent = "";
      settingsStatus.classList.remove("is-error");
      try {
        showSettings(await SettingsService.SetClickThrough(want));
        settingsStatus.textContent = want
          ? "ガイド枠の内側をクリックすると、後ろの画面に届きます。ツールバーと枠の縁はそのまま押せます。"
          : "ガイド枠の内側のクリックは後ろへ通しません。";
      } catch (err) {
        clickThrough.checked = !want;
        settingsStatus.textContent = `設定を保存できませんでした: ${String(err)}`;
        settingsStatus.classList.add("is-error");
      } finally {
        clickThrough.disabled = false;
      }
    })();
  });

  // 棋譜データベース（棚）。
  //
  // ⚠️ **「開けているか」は設定ではなく状態**なので、AppSettings ではなく
  // `KifuService.Status` から取る（設定は「どこを開こうとしているか」）。
  // ⚠️ **開けなくてもエラー色にしすぎないこと** —— 棚は解析の前提条件ではない
  // （設計原則3）。「棋譜タブだけが使えない」ことが読めるように書く。
  const refreshKifuDBNote = async () => {
    let st: KifuDBStatus;
    try {
      st = await KifuService.Status();
    } catch (err) {
      kifuDBNote.textContent = `状態を取得できませんでした: ${String(err)}`;
      kifuDBNote.classList.add("is-error");
      return;
    }
    if (st.ready) {
      kifuDBNote.textContent = `開いています（${st.count}件）。`;
      kifuDBNote.classList.remove("is-error");
    } else {
      kifuDBNote.textContent = st.error
        ? `開けていません: ${st.error}（棋譜タブだけが使えません）`
        : "開けていません（棋譜タブだけが使えません）。";
      kifuDBNote.classList.add("is-error");
    }
  };

  // ⚠️ **change（確定時）で拾う。** パスを 1 文字打つたびに DB を開き直さない。
  const applyKifuDBPath = async (want: string) => {
    kifuDBPath.disabled = true;
    kifuDBBrowse.disabled = true;
    settingsStatus.textContent = "";
    settingsStatus.classList.remove("is-error");
    try {
      showSettings(await SettingsService.SetKifuDBPath(want));
      // ⚠️ **開き直した結果はここで取り直す**（showSettings は設定しか映さない）。
      await refreshKifuDBNote();
      onKifuDBChanged?.();
      settingsStatus.textContent = "棋譜データベースを開き直しました。";
    } catch (err) {
      settingsStatus.textContent = `設定を保存できませんでした: ${String(err)}`;
      settingsStatus.classList.add("is-error");
      try {
        showSettings(await SettingsService.Settings());
      } catch {
        // 読み直せないなら画面はそのまま。
      }
    } finally {
      kifuDBPath.disabled = false;
      kifuDBBrowse.disabled = false;
    }
  };

  kifuDBPath.addEventListener("change", () => {
    void applyKifuDBPath(kifuDBPath.value);
  });

  kifuDBBrowse.addEventListener("click", () => {
    void (async () => {
      kifuDBBrowse.disabled = true;
      try {
        showSettings(await SettingsService.BrowseKifuDB());
        await refreshKifuDBNote();
        onKifuDBChanged?.();
      } catch (err) {
        settingsStatus.textContent = `選べませんでした: ${String(err)}`;
        settingsStatus.classList.add("is-error");
      } finally {
        kifuDBBrowse.disabled = false;
      }
    })();
  });

  // 認識器の読み込み元。**切り替えたその場で読み直す**（Go 側が ReloadRecognizer を呼ぶ）。
  // 読み直した結果は撮影タブの「認識器: …」に出るので、ここでも取り直して映す。
  const reloadRecognizerView = async () => {
    try {
      showRecognizer(await CaptureService.Recognizer());
    } catch {
      // 表示の更新に失敗しても設定の保存は済んでいる。黙って諦める。
    }
  };

  sutemeSource.addEventListener("change", () => {
    void (async () => {
      const want = sutemeSource.value;
      sutemeSource.disabled = true;
      settingsStatus.textContent = "";
      settingsStatus.classList.remove("is-error");
      try {
        showSettings(await SettingsService.SetSutemeSource(want));
        await reloadRecognizerView();
        settingsStatus.textContent = "認識器を読み込み直しました。";
      } catch (err) {
        settingsStatus.textContent = `設定を保存できませんでした: ${String(err)}`;
        settingsStatus.classList.add("is-error");
        try {
          showSettings(await SettingsService.Settings());
        } catch {
          // 読み直せないなら画面はそのまま。
        }
      } finally {
        sutemeSource.disabled = false;
      }
    })();
  });

  // ⚠️ **change（確定時）で拾う。** パスを 1 文字打つたびに読み込み直すと、
  // 20MB 級の学習データを何度も読むことになる。
  sutemeDataDir.addEventListener("change", () => {
    void (async () => {
      const want = sutemeDataDir.value;
      sutemeDataDir.disabled = true;
      settingsStatus.textContent = "";
      settingsStatus.classList.remove("is-error");
      try {
        showSettings(await SettingsService.SetSutemeDataDir(want));
        await reloadRecognizerView();
        settingsStatus.textContent = "認識器を読み込み直しました。";
      } catch (err) {
        settingsStatus.textContent = `設定を保存できませんでした: ${String(err)}`;
        settingsStatus.classList.add("is-error");
      } finally {
        sutemeDataDir.disabled = false;
      }
    })();
  });

  void (async () => {
    try {
      showSettings(await SettingsService.Settings());
    } catch (err) {
      settingsStatus.textContent = `設定を読み込めませんでした: ${String(err)}`;
      settingsStatus.classList.add("is-error");
    }
  })();

  // 駒の字。**起動した時点で当てる**（設定タブを開くまで同梱の字、では遅い ——
  // 最初に見えるのは盤なので、そこが既に選んだ書体になっている必要がある）。
  //
  // ⚠️ **失敗しても黙って同梱のままにすること**（設計原則3）。フォントを
  // 消していても、盤は描けるし解析もできる。理由は設定タブに出る。
  void (async () => {
    try {
      showFontState(await FontService.State());
    } catch (err) {
      fontStatus.textContent = `駒の字の設定を読み込めませんでした: ${String(err)}`;
      fontStatus.classList.add("is-error");
    }
  })();

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
