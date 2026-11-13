// 訂正タブ（2026-11-13 に `mainscreen.ts` から切り出した）。
//
// **認識の誤りを直す面。ここに居ること自体が訂正モード**（自由編集・合法性を問わない・手番も駒台の先後も未決でよい）。
// 撮った結果を受けて描くのもここ —— 盤・SFEN・警告・**撮った画像の参照**（盤の左）・**認識詳細情報**
// （盤の下の折りたたみ。認識器の状態・信頼度・盤面領域・推論器・保存先・撮った画像）・訂正データの送信の行
// （`trainsend.ts`）。盤の操作そのものは `editor.ts`。中身の HTML もここ。
//
// ⚠️ **受け渡しは `EditTabOptions` / `EditTabHandle` だけ**（`sidepane.ts` と同じ型）。
// ⚠️ **解析タブとは別の局面**（Go 側も PositionService / StudyService に分かれている）。
// 繋ぐのは「この局面を解析する」の 1 回だけで、その口（`adoptToStudy`）は**両方のタブに触るので
// `mainscreen.ts`** —— こちらは押されたことを `onConfirm` で知らせ、`setStatus` / `shotPath` / `nearWhite` を貸すだけ。
// ⚠️ **駒台と警告は「認識詳細情報」と盤の周りの両方に出すが、出所が違う**（下の要素の注記）。片方の更新をもう片方に流用しないこと。
// ⚠️ **撮る操作そのものはここに置かない**（枠のツールバーで完結する）。撮った結果の 1 行（`#status`）は入力タブにあり、
// `captureStatus` で借りる（撮ったのか読み込んだのかで別の面を作らない）。
import { Clipboard, Events } from "@wailsio/runtime";
import { FiCopy, FiImage } from "react-icons/fi";
// ⚠️ **CaptureService だけ出所が違う**（`mainscreen.ts` の import の注記）。
import { CaptureService } from "../bindings/ikkyoku";
import { AnalyzeService } from "../bindings/github.com/ShinteLab/ikkyoku/app";
import type { RecognizerStatus } from "../bindings/ikkyoku/models";
// 認識の観測情報。**型を手で書き写さない**(Go 側は suteme の型をそのまま通しており、
// ここで別に定義すると矩形の意味がずれても気づけない)。
import type { Debug } from "../bindings/github.com/ShinteLab/suteme";
import { iconMarkup } from "./icon";
import { mountEditor } from "./editor";
import { mountTrainSend, type ShotRegion } from "./trainsend";

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

export interface EditTabOptions {
  // captureStatus は撮った／読み込んだ結果の 1 行（入力タブの `#status`。`inputtab.ts` の `captureStatus`）。
  captureStatus: HTMLParagraphElement;
  // openTab は撮った結果に合わせてタブを移る（撮ったら訂正タブ。盤が取れなかったら理由の出ている入力タブ）。
  openTab(tab: "edit" | "input"): void;
  // onConfirm は「この局面を解析する」（follow が真なら「そのまま追跡」）を押したとき（`mainscreen.ts` の `adoptToStudy`）。
  onConfirm(follow: boolean): void;
  // onSutemeState は認識器の状態を設定タブへも流す（`settingstab.ts` の `showSutemeState`。更新の経路を 1 本にする）。
  onSutemeState(status: RecognizerStatus): void;
}

export interface EditTabHandle {
  // board は訂正タブの盤（駒の字の王/玉・左馬を当てる相手。解析タブの盤と両方に当てる）。
  board: HTMLElement;
  // editLoaded は訂正タブに局面が載っているか（置き換える前に聞くかどうか）。
  editLoaded(): boolean;
  // loadHandicap / loadEmpty は手合割の初期局面 / 空の盤で置き換える。
  // ⚠️ **撮った画像に紐づくもの（画像・認識の情報・学習への送信）も片付ける**（`clearShotContext`）。タブを開くのは呼び出し側。
  loadHandicap(handicap: string): Promise<void>;
  loadEmpty(): Promise<void>;
  // release は掴んでいる駒を離す（タブを離れるとき）。relayout はグリッドを測り直す（タブを開いた瞬間）。
  release(): void;
  relayout(): void;
  // setStatus は訂正タブの一行（空で消す。空でなければエラーの色）。
  setStatus(text: string): void;
  // shotPath は今の局面を起こした 1 枚のパス（画像の無い局面なら空）。nearWhite は撮った画像が後手目線か。
  shotPath(): string;
  nearWhite(): boolean;
  // showRecognizer は認識器の状態を描く（「認識詳細情報」の 1 行。設定タブへも流す）。
  showRecognizer(status: RecognizerStatus): void;
  // setTrainEnabled は設定「訂正盤面を suteme に登録する」（送信の行を出し直す）。
  setTrainEnabled(on: boolean): void;
}

export function mountEditTab(root: HTMLElement, opts: EditTabOptions): EditTabHandle {
  // ⚠️ **この中の HTML コメントにバッククォートを書かないこと**（`mainscreen.ts` の同じ注記。`check-css.mjs` が見ている）。
  root.innerHTML = `
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
  const status = opts.captureStatus;
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

  // 認識詳細情報（旧デバッグタブ）。**盤の下・「この局面を解析する」の上**
  // （2026-08-18 に盤の上から移した。開いても盤が動かない位置）。
  const debugDetails = root.querySelector<HTMLDetailsElement>("#debug-details")!;
  const debugSummary = root.querySelector<HTMLElement>("#debug-summary")!;

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

  // 訂正タブ側の一行。訂正の操作が通らなかった理由と、確定できない理由を出す。
  const editStatus = root.querySelector<HTMLParagraphElement>("#edit-status")!;

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
      opts.onConfirm(follow);
    },
    onError: (message) => {
      // **訂正タブの中に出す。** 撮影の結果（入力タブの #status）とは別の話で、
      // タブを跨いだ先に理由が出ても読めない。
      editStatus.textContent = `訂正できませんでした: ${message}`;
      editStatus.classList.add("is-error");
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
    opts.openTab("edit");
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
        .then(() => opts.openTab("edit"));
    } else {
      editor.clear();
      showBoard("", "shot");
      sfenOut.textContent = "-";
      opts.openTab("input");
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
    opts.onSutemeState(st);
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

  return {
    board,
    editLoaded: () => editLoaded,
    loadHandicap: async (handicap) => {
      await editor.loadHandicap(handicap);
      clearShotContext();
    },
    loadEmpty: async () => {
      await editor.loadEmpty();
      clearShotContext();
    },
    release: () => editor.release(),
    relayout: () => editor.relayout(),
    setStatus: (text) => {
      editStatus.textContent = text;
      editStatus.classList.toggle("is-error", text !== "");
    },
    shotPath: () => refShot?.path ?? "",
    nearWhite: () => editNearWhite,
    showRecognizer,
    setTrainEnabled: (on) => {
      trainUI.setEnabled(on);
      trainUI.sync();
    },
  };
}
