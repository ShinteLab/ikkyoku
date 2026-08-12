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
//   入力   … 局面を**取り込む**面。キャプチャと**棋譜(KIF)の貼り付け**。今後 SFEN / 画像ファイル
//            (画像は認識を通るので訂正タブへ、SFEN/KIF は確定済みなので解析タブへ)
//   訂正   … 認識の誤りを**直す**面。**ここに居ること自体が訂正モード**
//            (自由編集・合法性を問わない・手番も駒台の先後も未決でよい)
//   解析   … **確定した局面**の面。評価値を出し、今後ここに手順と分岐ツリーが乗る
//            (合法手だけを辿る)
//   設定   … 設定
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
import { Clipboard, Events } from "@wailsio/runtime";
import { FiCopy, FiImage } from "react-icons/fi";
import {
  AnalyzeService,
  CaptureService,
  SettingsService,
  StudyService,
  TrainingService,
} from "../bindings/ikkyoku-app";
import { iconMarkup } from "./icon";
import { mountEditor } from "./editor";
import { mountEvalGraph } from "./evalgraph";
import { mountStudyBoard } from "./study";
import type { AppSettings, EngineSettings, KifuLoad, StudyState } from "../bindings/ikkyoku-app/models";
import type { Stock } from "../bindings/github.com/ShinteLab/ikkyoku/position/models";
// 認識の観測情報。**型を手で書き写さない**(Go 側は suteme の型をそのまま通しており、
// ここで別に定義すると矩形の意味がずれても気づけない)。
import type { Debug } from "../bindings/github.com/ShinteLab/suteme";

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

// 解析の途中経過（Go 側 AnalyzeProgress / analyze.Progress）。
//
// **bindings から取れない。** これはメソッドの戻り値ではなくイベントのペイロードなので、
// 生成の対象外（CaptureResult と同じ事情）。**Go 側を変えたらここも直すこと。**
//
// ⚠️ **score.cp / score.mate は先手視点で、label は Go 側が組み立てた文字列。**
// フロントで符号をいじったり書式を作り直したりしないこと（2 か所に散る）。
//
// ⚠️ **lines は候補手の配列**（MultiPV）。**「最善手 1 個」に畳まないこと** ——
// 次善手を辿るのが構想の中心で、ここが複数本になれることが Phase 5 の前提。
// 今の自作 engine は MultiPV を持たないので 1 本しか来ないが、**それを異常扱いしない**
// （engine/TODO.md の 1 が入れば増える）。
interface AnalyzeLine {
  rank: number;
  // depth はこの候補が届いたときの深さ。
  //
  // ⚠️ **候補ごとに違うことがある。** MultiPV では順位ごとに別々の info が来て、
  // 進み方も揃わない（見出しの「深さ」は一番深いところ）。
  depth: number;
  // winRate は**先手の勝率**（0.0〜1.0）。勝率バーが読む値。
  //
  // ⚠️ **cp から計算し直さないこと。** 式（1/(1+exp(-cp/定数))）も定数も
  // Go 側が持っている（`analyze.WinRate` / 設定タブのポナンザ定数）ので、
  // ここで計算すると**定数を変えたときに片方だけ古い値で描く**。
  score: { cp: number; mate: number; label: string; winRate: number };
  // moves は USI 表記（"8h2b+"）。**手を辿るのに使うのはこちら。**
  moves: string[];
  // text は日本語表記（"▲２二角成"）。**画面に出すのはこちら。**
  //
  // ⚠️ **フロントで組み立て直さないこと。** USI の手には駒種が書いていない
  // （"8h2b+" のどこにも「角」が無い）ので、盤と突き合わせないと作れない。
  // 変換は Go 側（core/kifu）が解析した局面から 1 手ずつ盤を進めて行っている。
  // **moves と同じ長さ**で、変換できなかった手はその USI がそのまま入る。
  text: string[];
}

interface AnalyzeProgress {
  seq: number;
  // ⚠️ **どのエンジンが喋ったか。** 複数のエンジンが同時に走るので、
  // **seq だけでは行き先を決められない**（seq は解析の世代であって、エンジンの区別ではない）。
  engineId: string;
  // engineName はそのエンジンが名乗った名前（`id name`）。
  engineName: string;
  done: boolean;
  progress: {
    depth: number;
    nodes: number;
    elapsedMs: number;
    lines: AnalyzeLine[];
  };
}

// 解析の結末（analyze:done のみ）。**起動にかかった時間は done でしか分からない。**
interface AnalyzeDone extends AnalyzeProgress {
  startupMs: number;
  // ⚠️ **接続を使い回したか。** `startupMs` が 0 のとき、「起動が速かった」のか
  // 「払っていない」のかはこれでしか区別できない。
  reused: boolean;
}

// ⚠️ **1 つのエンジンが落ちても、他のエンジンの解析は続く**（設計原則3）。
// **解析全体の失敗として扱わないこと。**
interface AnalyzeFailure {
  seq: number;
  engineId: string;
  error: string;
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

export function mountMainScreen(root: HTMLElement): void {
  // ⚠️ **この中の HTML コメントにバッククォートを書かないこと。** テンプレート
  // リテラルなので、`.foo` のような引用がそこで文字列を終わらせる。**エラーは
  // ずっと下の行に出る**（実際に踏んだ）ので、原因に辿り着きにくい。
  root.innerHTML = `
    <div class="main-screen">
      <div class="main-toolbar">
        <div class="tabs" role="tablist">
          <button id="tab-input" class="tab is-active" type="button"
                  role="tab" aria-selected="true" aria-controls="panel-input">入力</button>
          <button id="tab-edit" class="tab" type="button"
                  role="tab" aria-selected="false" aria-controls="panel-edit">訂正</button>
          <button id="tab-study" class="tab" type="button"
                  role="tab" aria-selected="false" aria-controls="panel-study">解析</button>
          <button id="tab-settings" class="tab" type="button"
                  role="tab" aria-selected="false" aria-controls="panel-settings">設定</button>
        </div>
        <!-- ⚠️ **ここに「枠を表示」も撮り方の案内も戻さないこと**（2026-08-10 に外した）。
             枠は「撮るときだけ使う道具」で、入力の口はこれから増える
             （SFEN / KIF / 画像ファイル）。取り込みの話は入力タブに寄せる。 -->
      </div>

      <!-- 入力タブ。**局面を取り込む面。** 今はキャプチャだけだが、ここに
           SFEN・KIF・画像ファイルの入口が並ぶ（そのとき行き先が分かれる:
           画像は認識を通るので訂正タブへ、SFEN/KIF は確定済みなので解析タブへ）。 -->
      <div id="panel-input" class="panel is-active" role="tabpanel" aria-labelledby="tab-input">
        <div class="setting-group">
          <span class="setting-title">画面から撮る</span>
          <span class="setting-note">
            「枠を表示」でガイド枠を出し、中継の盤面に合わせてから、
            枠のツールバーのカメラを押します。
            撮ると<strong>訂正タブ</strong>が開きます。
            <strong>枠が出ていないあいだは撮れません</strong>
            （どこを撮るのかが画面に見えていない状態で撮らないため）。
          </span>
          <div class="setting-fields">
            <button id="input-show-frame" class="ghost-btn" type="button">枠を表示</button>
            <button id="input-fit" class="ghost-btn" type="button"
                    title="画面から盤を探して、ガイド枠を合わせます">盤に合わせる</button>
          </div>
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
          </div>
          <textarea id="kifu-text" class="kifu-text" spellcheck="false"
                    placeholder="手数----指手---------消費時間--&#10;   1 ７六歩(77)   ( 0:16/00:00:16)&#10;   2 ３四歩(33)   ( 0:04/00:00:04)"></textarea>
          <div class="setting-fields">
            <button id="kifu-load" class="ghost-btn" type="button">読み込む</button>
            <button id="kifu-clear" class="ghost-btn" type="button">消す</button>
          </div>
          <p id="kifu-status" class="status" role="status" aria-live="polite" hidden></p>
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
          <button id="edit-reset" class="danger-btn" type="button" hidden
                  title="訂正を捨てて、認識したときの盤面に戻します">認識結果に戻す</button>
        </div>

        <!-- 認識詳細情報（旧デバッグタブ。2026-08-11 にタブから畳んでここへ移した）。
             **「どれくらい外したか」を見る面**で、訂正しながら開く。訂正の作業と
             同じ画面にあるほうが行き来が要らないので、独立したタブではなく
             **既定で閉じた折りたたみ**にしてある。

             ⚠️ **中身は認識した時点の記録**（CaptureResult）。すぐ上の警告や盤の脇の
             駒台（EditState）とは**別の値**で、訂正しても変わらない。同じ見出しが
             1 つの画面に 2 度出ることになるが、**読む目的が違う**ので片方を消さないこと
             （上＝今の局面を直すための情報、ここ＝認識がどれくらい外したかの記録）。 -->
        <details id="debug-details" class="debug-details">
          <summary id="debug-summary">認識詳細情報</summary>
          <div class="debug-body">
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
          </div>
        </details>

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
        <div id="editor" class="editor"></div>
        <p id="edit-status" class="status" role="status" aria-live="polite"></p>
        <!-- 訂正した局面を suteme の学習データとして送る。設定で有効にしていない
             ときは行ごと出さない。**押したときだけ送る**(自動送信はしない)。

             ⚠️ **確定を待たない。** 送るのは labelSfen（未確定でも持ち駒を
             落とさない画像ラベル用の SFEN）なので、手番や駒台の先後が決まって
             いなくても学習の役には立つ（設計原則3）。何が落ちるかは note に出る。 -->
        <div id="train-row" class="train-row" hidden>
          <button id="train-send" class="ghost-btn" type="button"
                  title="この画像と訂正した盤面を、suteme の学習データとして登録します">訂正データを送信</button>
          <!-- 送る SFEN のために妥協した点(手番が未決・先後未決の持ち駒)。
               **送る前に出す**(何が落ちるか分からないまま送らせない)。 -->
          <span id="train-note" class="note is-caution"></span>
          <span id="train-send-status" class="note"></span>
        </div>
        <div class="sfen-row">
          <span class="field-label">SFEN</span>
          <code id="sfen" class="sfen">-</code>
        </div>
        <!-- 駒台。**訂正中の局面の値**なので先後の割り振りが出る
             (「認識詳細情報」側は認識した時点の推定枚数で「先後不明」のまま)。
             訂正中は盤の脇に駒そのものが並ぶので、こちらは文字の要約。 -->
        <div id="board-hand-row" class="hand-row" hidden>
          <span class="field-label">駒台</span>
          <span id="board-hand" class="hand"></span>
        </div>
      </div>

      <!-- 解析タブ。**確定した局面の面。** 訂正タブとは別の局面を持つ
           （Go 側も PositionService / StudyService の 2 つに分かれている）。

           ⚠️ **ここに訂正の道具を置かないこと。** 駒を自由に置ける盤は
           「まだ決めていない局面」で、その評価値には意味が無い。直したくなったら
           訂正タブへ戻る（戻ると解析結果は捨てる。別の局面の話になるため）。

           **これから**: 手を進める UI（合法手だけ・1 手ごとに手番が入れ替わる）と
           分岐ツリーがここに乗る。 -->
      <div id="panel-study" class="panel" role="tabpanel" aria-labelledby="tab-study" hidden>
        <div class="board-head">
          <ul id="study-warnings" class="warnings is-compact" hidden></ul>
          <button id="study-back" class="ghost-btn" type="button" hidden
                  title="訂正タブへ戻ります。解析の結果は捨てられます">訂正に戻る</button>
        </div>
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
            <div id="study-hand-white-slot" class="hand-slot" hidden></div>
            <div id="study-stage" class="board-stage">
              <!-- ⚠️ fluid: 置き場所の幅いっぱいに広げる（core/web の属性。2026-08-12）。
                   これが無いと**盤は固有サイズの 560px 止まり**で、盤を主役に
                   大きく見せられない。訂正タブの盤には付けていない（あちらは
                   撮った画像を並べる面なので、盤だけ大きくしても仕方がない）。 -->
              <shogi-board id="study-board" fluid hidden></shogi-board>
            </div>
            <div id="study-hand-black-slot" class="hand-slot" hidden></div>
          </div>
          <!-- 盤の右の列（2026-08-12 に作り替えた）。**解析のものは全部ここに入る**
               —— 解析の行・エンジンごとの結果・評価値グラフ・手順・SFEN。

               ⚠️ **盤の下に行を積まないこと。** 盤の大きさは
               「ウィンドウの高さ − 上下に積んだ行」で決まるので、**下に積むほど
               盤が小さくなる**（実際、エンジンの結果と評価値グラフを下に置いていた
               ときは 560px の盤すら出せていなかった）。横に置けば、盤の高さを
               削るのはウィンドウの高さだけになる。

               手順は「駒をクリック → 動かせる位置が光る → そこをクリックで指す」の
               結果が縦に積まれる列（棋譜ソフトと同じ並び）。⚠️ **チップを押すと
               戻れるが、手順は消えない**（進め直せる）。消えるのは戻った先で別の手を
               指したとき。「1手戻す」は指し間違えの取り消しなので**手順からも消す**。 -->
          <div id="study-side" class="study-side" hidden>
            <!-- エンジン解析（Phase 4）。**確定した局面にだけかかる。**
                 確定していない局面はそもそもこのタブに来ない（Go 側の
                 StudyService.Adopt が断る）ので、ここでの「押せない理由」は
                 「まだ何も採っていない」だけになった。

                 ⚠️ **局面を採り直したら結果を消す。** 評価値は「その局面の」値なので、
                 盤が変わったあとも残っていると、別の局面の値を今の盤の評価だと読ませる。 -->
            <div id="analyze-row" class="analyze-row" hidden>
              <button id="analyze-run" class="ghost-btn" type="button">解析</button>
              <!-- 連続モード（2026-08-11）。**既定で入**。
                   手を進めるたびに勝手に解析し直すので、押す操作が要らなくなる。
                   手順を辿りながら評価値の変化を追うのがこのタブの目的なので、
                   **1 手ごとにボタンを押すほうが例外的**。

                   ⚠️ **エンジンの寿命は変わらない**（解析 1 回ぶん）。前の解析を止めて
                   から起こし直すだけで、常駐にはしない。 -->
              <label class="analyze-continuous"
                     title="手を進めるたびに解析し直します。前の解析は止めてから起こし直すので、エンジンが常駐するわけではありません">
                <input id="analyze-continuous" type="checkbox" checked />
                <span>連続</span>
              </label>
              <label class="analyze-time">
                <select id="analyze-seconds"
                        title="考える時間。途中で切っても、そこまでの評価値は出ます。「無制限」は停止するまで考え続けます（そのあいだエンジンは起動したままです）">
                  <option value="1">1秒</option>
                  <option value="3" selected>3秒</option>
                  <option value="10">10秒</option>
                  <option value="30">30秒</option>
                  <option value="0">無制限</option>
                </select>
              </label>
              <!-- 候補手の本数（MultiPV。2026-08-11）。**次善手を辿るのが構想の中心**
                   なので、最善手だけに絞らずここで増やせるようにする。

                   ⚠️ **対応していないエンジンでは無視される**（自作エンジンが今それ）。
                   1 本しか返らないことを異常扱いしないこと。 -->
              <label class="analyze-time">
                <select id="analyze-multipv"
                        title="候補手を何本出させるか（MultiPV）。対応していないエンジンでは 1 本のままです">
                  <!-- 既定は**候補 3**（2026-08-12）。「次善手を選んだらどう転ぶか」を
                       辿るのが構想の中心なので、最善手だけが出ている状態を既定にしない。
                       ⚠️ 対応していないエンジン（同梱のもの）では 1 本のままになる。 -->
                  <option value="1">候補 1</option>
                  <option value="3" selected>候補 3</option>
                  <option value="5">候補 5</option>
                  <option value="10">候補 10</option>
                </select>
              </label>
              <!-- 全て解析（2026-08-12）。**手順の範囲をまとめて解析する。**
                   中身は「手順リストを 1 つずつ押しては、考える秒数だけ待つ」の
                   繰り返しで、押す操作を人がやらなくてよくなるだけ。
                   棋譜を読み込んだ直後に一度かけると、評価値グラフが全部埋まる。

                   ⚠️ **考える秒数が「無制限」だと使えない**（1 手目で止まったまま
                   次へ進めない）。理由は押せない側に出す。 -->
              <span class="analyze-batch">
                <button id="analyze-batch-run" class="ghost-btn" type="button">全て解析</button>
                <input id="analyze-batch-from" class="analyze-batch-num" type="number"
                       min="0" max="999" step="1" title="解析を始める手数" />
                <span class="analyze-batch-dash">-</span>
                <input id="analyze-batch-to" class="analyze-batch-num" type="number"
                       min="0" max="999" step="1" title="解析を終える手数" />
              </span>
              <span id="analyze-meta" class="note"></span>
              <!-- 押せない理由。**ツールチップだけにしない**（ホバーしないと読めない）。 -->
              <span id="analyze-hint" class="note is-caution" hidden></span>
            </div>
            <!-- エンジンごとの結果（2026-08-11）。設定で「解析に使う」を付けたエンジンが
                 **同時に走り、ここに縦に並ぶ**。エンジンが違えば同じ局面の評価が食い違う
                 のが普通で、**その食い違いこそ見たいもの**（どれが正しいかは局面による）。

                 ⚠️ **エンジンをまたいで結果を合成しないこと**（平均も多数決も取らない）。
                 並べて人が読む。中の候補手（MultiPV）は **1 本しか来なくても一覧の形で
                 出す** —— 次善手を辿るのが構想の中心なので、複数本になるのが前提の作り。 -->
            <div id="analyze-engines" class="analyze-engines" hidden></div>
            <p id="analyze-status" class="note is-caution" hidden></p>
            <!-- 手順（Phase 5「手を進める UI」）。**盤の右の列の下半分。**
                 ⚠️ **高さを中身に依存させないこと**（中でスクロールさせる）。 -->
            <div class="study-move-head">
              <span class="field-label">手順</span>
              <button id="study-undo" class="ghost-btn" type="button"
                      title="最後の 1 手を取り消します（手順からも消えます）">1手戻す</button>
            </div>
            <div id="study-moves" class="study-moves"></div>
            <p id="study-move-status" class="note is-caution" hidden></p>
            <!-- 今見ている局面の SFEN。**盤の下ではなくここに置く**（2026-08-12）。
                 盤の下に積む行はそのぶん盤を小さくするうえ、SFEN は
                 「今どの局面を見ているか」の値なので手順の列にあるほうが素直。
                 ⚠️ **高さを中身に依存させないこと** —— 長さで列が伸びると、
                 上の手順リストの取り分が動く。折り返して 3 行で頭打ちにする。 -->
            <div class="study-sfen-row">
              <span class="field-label">SFEN</span>
              <code id="study-sfen" class="sfen" title="今見ている局面">-</code>
            </div>
          </div>
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
          <div class="eval-graph-head">
            <span class="field-label">評価値</span>
            <!-- 横軸の範囲。**既定は「全て」＝ 指した手が全部見えている状態。**
                 ⚠️ **1 から始まるとは限らない** —— 根は初期局面とは限らないので、
                 撮った 40 手目の局面から始めたなら 40 手目から始まる。

                 **絞るのはグラフの上をドラッグする**のが本筋で、選んだ範囲は
                 そのまま下の欄に入る（＝見えている数字が今の範囲）。
                 「1-150 の中のどこに居るか」で読みたいときは欄に直接書く。 -->
            <select id="eval-graph-range" class="eval-graph-range"
                    title="横軸の範囲。「全て」は指した手が全部見える範囲、「自由入力」は書いたとおりの手数です">
              <option value="all" selected>全て</option>
              <option value="custom">自由入力</option>
            </select>
            <span id="eval-graph-fields" class="eval-graph-fields" hidden>
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
          </div>
          <div id="eval-graph" class="eval-graph"
               title="押すとその局面に戻ります（手順は消えません）。横にドラッグするとその範囲に絞ります"></div>
        </div>
      </div>

      <div id="panel-settings" class="panel" role="tabpanel" aria-labelledby="tab-settings" hidden>
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
        <p id="settings-status" class="status" role="status" aria-live="polite"></p>

        <!-- 解析エンジン。**繋ぎ先は「USI を話すプロセス」なら何でもよい**
             （やねうら王・水匠・prokishi.exe・同梱のエンジン）。検討ツールとして
             実用になるかは繋ぐエンジンの棋力で決まるので、ここで差し替えられる。

             ⚠️ **1 つに絞らない**（2026-08-11）。**複数登録でき、「解析に使う」を
             付けたものが同時に走る。** どのエンジンが正しいかは局面によって違うので、
             評価が食い違うところを並べて読めることに意味がある。

             ⚠️ **「外部を使う」のチェックボックスは置かない。** パスが空なら同梱、
             入っていれば外部。2 つ持つと「パスが入っているのに無効」という
             食い違いが起きる。 -->
        <div class="setting-group">
          <span class="setting-title">解析エンジン</span>
          <span class="setting-note">
            USI を話すエンジンの実行ファイルを登録します（やねうら王・水匠など）。
            <strong>「解析に使う」を付けたエンジンが同時に走り、解析タブに結果が並びます。</strong>
            実行ファイルを<strong>空にするとその登録は同梱のエンジン</strong>になります。
            同じエンジンを <code>setoption</code> 違いで 2 つ登録して比べることもできます
            （送る値は設定ファイルの <code>engines[].options</code>）。
          </span>
          <ul id="engine-list" class="engine-list"></ul>
          <div class="setting-fields">
            <button id="engine-add" class="ghost-btn" type="button"
                    title="実行ファイルを選んで登録します">エンジンを追加…</button>
            <button id="engine-add-builtin" class="ghost-btn" type="button"
                    title="同梱のエンジンを登録します">同梱エンジンを追加</button>
          </div>
          <p id="engine-status" class="status" role="status" aria-live="polite"></p>
        </div>

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

        <p class="setting-path">設定ファイル: <code id="settings-path">-</code></p>
      </div>
    </div>
  `;

  const reloadBtn = root.querySelector<HTMLButtonElement>("#reload-btn")!;
  const recognizer = root.querySelector<HTMLParagraphElement>("#recognizer")!;
  const board = root.querySelector<HTMLElement>("#board")!;
  const placeholder = root.querySelector<HTMLParagraphElement>("#board-placeholder")!;
  const sfenOut = root.querySelector<HTMLElement>("#sfen")!;
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
  const boardHandRow = root.querySelector<HTMLDivElement>("#board-hand-row")!;
  const boardHandOut = root.querySelector<HTMLElement>("#board-hand")!;
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
  //   入力     … 局面を取り込む（キャプチャ・棋譜の貼り付け。今後 SFEN / 画像ファイル）
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
  const tabOf = (name: string) => ({
    tab: root.querySelector<HTMLButtonElement>(`#tab-${name}`)!,
    panel: root.querySelector<HTMLElement>(`#panel-${name}`)!,
  });
  const tabs = ["input", "edit", "study", "settings"].map(tabOf);
  const inputTab = tabs[0].tab;
  const editTab = tabs[1].tab;
  const studyTab = tabs[2].tab;
  // 認識詳細情報（旧デバッグタブ）。**訂正タブの中の折りたたみ**（2026-08-11）。
  const debugDetails = root.querySelector<HTMLDetailsElement>("#debug-details")!;
  const debugSummary = root.querySelector<HTMLElement>("#debug-summary")!;

  const selectTab = (target: HTMLButtonElement) => {
    // ⚠️ **解析タブを離れたらエンジンを手放す**（2026-08-12）。接続は解析を
    // またいで使い回すようになったので、**放っておくとタブを移ったあとも
    // `USI_Hash` ぶん（GB 級になりうる）のメモリを掴んだまま**になる。
    // タブの境界を寿命にしてあるのは、アイドルタイマーを持たずに済ませるため。
    //
    // **走っている解析（全て解析を含む）も止まる。** タブを移ると止まるのは
    // そういう約束で、**そこまでの評価値は残る**（設計原則3）。
    if (target !== studyTab && studyTab.classList.contains("is-active")) {
      stopBatch("");
      void AnalyzeService.Release();
    }
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
    if (target === studyTab) {
      studyBoardUI.relayout();
      // ⚠️ **グラフも測り直す。** 大きさは `clientWidth` で測っており、
      // **`display: none` の中では 0 になる**（測らないと出ないか、前回の
      // 大きさのまま残る）。盤のグリッドと同じ落とし穴。
      evalGraphUI.relayout();
      // **解析タブに来たら（連続モードなら）そのまま解析を始める。**
      // まだ解析していない局面のときだけ動く（止めた解析を勝手に起こし直さない）。
      autoAnalyze();
    }
  };

  for (const { tab } of tabs) {
    tab.addEventListener("click", () => selectTab(tab));
  }

  // 警告やエラーは折りたたみの中にあるので、畳んでいるあいだは気づけない。
  // 見出しに点を出して「開くべきものがある」ことだけ伝える（開けば消える）。
  //
  // ⚠️ **勝手に開かない。** 訂正のあいだ盤の上に積む行は短く保ちたいので、
  // 開くかどうかはユーザーが決める（警告そのものは上の board-head にも出ている）。
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
    // 開閉で盤が上下に動く。**大きさが変わりうる場面を 1 つでも落とすと、
    // 駒の見た目とクリック領域がずれる**ので、ここでも測り直す。
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
  const analyzeRow = root.querySelector<HTMLDivElement>("#analyze-row")!;
  const analyzeRun = root.querySelector<HTMLButtonElement>("#analyze-run")!;
  const analyzeSeconds = root.querySelector<HTMLSelectElement>("#analyze-seconds")!;
  const analyzeContinuous = root.querySelector<HTMLInputElement>("#analyze-continuous")!;
  const analyzeMultiPV = root.querySelector<HTMLSelectElement>("#analyze-multipv")!;
  const analyzeEnginesBox = root.querySelector<HTMLDivElement>("#analyze-engines")!;
  const analyzeMeta = root.querySelector<HTMLElement>("#analyze-meta")!;
  const analyzeHint = root.querySelector<HTMLElement>("#analyze-hint")!;
  const analyzeStatus = root.querySelector<HTMLParagraphElement>("#analyze-status")!;
  const evalGraphRow = root.querySelector<HTMLDivElement>("#eval-graph-row")!;
  const batchRun = root.querySelector<HTMLButtonElement>("#analyze-batch-run")!;
  const batchFrom = root.querySelector<HTMLInputElement>("#analyze-batch-from")!;
  const batchTo = root.querySelector<HTMLInputElement>("#analyze-batch-to")!;

  // 今の解析の世代。**打ち切った解析の途中経過は後から届く**ので、これで捨てる。
  let analyzeSeq = -1;
  let analyzeRunning = false;
  // 何を解析した値なのか。今の局面と食い違ったら表示を消す。
  let analyzedSfen = "";
  // 解析できる局面か。
  let analyzeReady = false;
  // 今**解析タブが持っている**局面（StudyState 由来）。⚠️ **訂正タブの局面
  // （EditState）ではない。** 混ぜると、直している最中の盤の評価値を出すことになる。
  let studySfen = "";
  let studyLoaded = false;

  // エンジンごとの表示（2026-08-11）。**複数のエンジンが同時に走る**ので、
  // 届いたイベントは engineId で振り分ける。**seq だけでは行き先を決められない。**
  //
  // ⚠️ **エンジンをまたいで結果を合成しないこと**（平均も多数決も取らない）。
  // 食い違うところを並べて人が読むのが目的。
  interface EngineCard {
    // label は設定タブで付けた名前。**エンジンが名乗る名前とは別に持つ**
    // （同じ exe を option 違いで 2 つ登録していると、名乗る名前では区別が付かない）。
    label: string;
    // まだ結果が届いていないエンジンか（走っているエンジンの数を数えるのに使う）。
    pending: boolean;
    // 起動〜readyok にかかった時間（done で届く）。**エンジンごとに違う。**
    // ⚠️ **接続を使い回したときは 0**（下の reused）。
    startupMs: number;
    // 繋ぎっぱなしの接続を使い回したか。**startupMs が 0 の理由がこれ。**
    reused: boolean;
    name: HTMLElement;
    meta: HTMLElement;
    lines: HTMLOListElement;
    error: HTMLElement;
    // best は最後に届いた**順位 1 の候補**（勝率バーが読む）。まだ無ければ null。
    //
    // ⚠️ **バーは 1 本しか出ないが、値はエンジンごとに覚えておくこと。**
    // 押して切り替えた瞬間に、そのエンジンの今の値が出るのが期待どおり
    // （切り替えたら次の更新まで空になる、では比べられない）。
    best: AnalyzeLine | null;
  }
  const engineCards = new Map<string, EngineCard>();
  const winrateRow = root.querySelector<HTMLDivElement>("#winrate-row")!;
  const winrateBar = root.querySelector<HTMLButtonElement>("#winrate-bar")!;
  const winrateWhite = root.querySelector<HTMLElement>("#winrate-white")!;
  // 今バーに出しているエンジン（登録 ID）。**押すと次のエンジンに変わる。**
  //
  // ⚠️ **選んだエンジンは解析をまたいで覚えること** —— 1 手ごとに解析し直す
  // （連続モード）ので、そのたびに 1 つ目へ戻ると切り替えた意味が無い。
  let winrateEngineId = "";

  // 解析できない理由。**空なら解析できる。**
  //
  // ⚠️ **理由をツールチップだけにしないこと。** 押せないボタンの `title` は
  // ホバーしないと読めず、「なぜ押せないのか」が分からない（実際に詰まった）。
  const analyzeBlockedReason = (): string => {
    // 局面が無いときは解析の行ごと出ないので、理由を書く相手が居ない。
    if (!studyLoaded) {
      return "";
    }
    // Adopt を通っている以上 SFEN は必ず埋まっているが、念のため。
    if (!studySfen) {
      return "手番と駒台の先後を決めると解析できます";
    }
    return "";
  };

  const syncAnalyzeButton = () => {
    const blocked = analyzeBlockedReason();
    analyzeReady = studyLoaded && blocked === "";
    analyzeRun.textContent = analyzeRunning ? "停止" : "解析";
    analyzeRun.classList.toggle("is-active", analyzeRunning);
    analyzeRun.disabled = !analyzeRunning && !analyzeReady;
    analyzeRun.title = analyzeRunning
      ? "ここまでの結果で打ち切ります"
      : analyzeReady
        ? "この局面をエンジンに解析させます"
        : blocked;
    // 押せない理由は**文字でも出す**（上の ⚠️）。解析中と、押せるときは何も出さない。
    analyzeHint.textContent = analyzeRunning ? "" : blocked;
    analyzeHint.hidden = analyzeHint.textContent === "";
  };

  // ⚠️ **枠は畳まない**（2026-08-11）。連続モードでは 1 手指すたびに
  // 「消す → 起こす → 結果が届く」を繰り返すので、そのたびに枠が伸び縮みすると
  // **盤ごと画面が上下に跳ねる**（駒を掴んでいる最中に動くのが一番困る）。
  // 高さは CSS(.analyze-engines の min-height / .analyze-lines の height)で
  // 確保してあるので、ここでは中身を空にするだけにする。
  const clearAnalyzeResult = () => {
    analyzedSfen = "";
    engineCards.clear();
    analyzeEnginesBox.replaceChildren();
    // ⚠️ **勝率バーも一緒に空に戻すこと。** 別の局面の勝率が盤の上に残っていると、
    // **今の盤の形勢として読まれる**（評価値の一覧を消すのと同じ理由で、
    // むしろこちらのほうが目に入る位置にある）。
    // ⚠️ **枠は消さない**（`renderWinRate` が中立の見た目に戻すだけ）——
    // 盤の真上なので、出たり消えたりすると盤ごと動く。
    renderWinRate();
    analyzeMeta.textContent = "";
    analyzeStatus.hidden = true;
    analyzeStatus.textContent = "";
  };

  // reserveLines は候補手の一覧に確保する高さを、本数の指定に合わせて決める。
  //
  // ⚠️ **高さは固定しておく**（`--analyze-lines-h`）。連続モードでは 1 手ごとに
  // 「消す → 起こす → 結果が届く」を繰り返すので、届いた本数で伸び縮みすると
  // **盤ごと画面が上下に跳ねる**。本数を変えたときだけ高さが変わればよい。
  //
  // ⚠️ **本数ぶん全部は確保しない**（4 本ぶんで頭打ち）。MultiPV を上げると
  // 候補は何本にもなるので、そのぶん盤の下が伸びる。溢れたら中でスクロールさせる。
  const reserveLines = () => {
    const want = Math.min(Math.max(Number(analyzeMultiPV.value) || 1, 1), 4);
    // 1 行 28px + 行間 2px（CSS の --analyze-line-h と揃えること）。
    //
    // ⚠️ **:root（documentElement）に入れること**（2026-08-12）。枠の高さ
    // （--analyze-engines-h）と `#panel-study` の --board-size がこれを引いており、
    // **カスタムプロパティは下へしか継承しない**ので、枠の要素に入れると
    // 盤の大きさの式から読めない（--winrate-rows と同じ）。
    document.documentElement.style.setProperty(
      "--analyze-lines-h", `${want * 28 + (want - 1) * 2}px`);
  };
  analyzeMultiPV.addEventListener("change", reserveLines);
  reserveLines();

  // ---- 勝率バー（盤の上）---------------------------------------------------
  //
  // **左が後手（青）・右が先手（赤）。** 評価値は Go 側が先手視点に揃えてあるので、
  // ⚠️ **ここで符号も向きもいじらないこと**（エンジンをまたいでも同じ向きで読める、
  // という前提がここで効いている）。
  //
  // ⚠️ **勝率そのものも Go 側の値**（`score.winRate`）。式と定数（ポナンザ定数）を
  // フロントに持つと、設定で定数を変えたときに**片方だけ古い値で描く**。

  // ⚠️ **出すのは 1 つのエンジンだけ**（押すと切り替わる）。全部を並べると盤の上に
  // 段が積まれてそのぶん盤が小さくなるうえ、**形勢を一目で見るための帯**なので
  // 複数あると読む対象が増える。食い違いを読むのは下の一覧（そちらは全部出る）。
  // **合成ではない** —— どれか 1 つの値をそのまま出している。

  // winrateSource は今バーに出すエンジンを返す（選ばれていなければ最初の 1 つ）。
  const winrateSource = (): EngineCard | undefined =>
    engineCards.get(winrateEngineId) ?? [...engineCards.values()][0];

  // renderWinRate は選ばれているエンジンの勝率をバーに描く。
  //
  // ⚠️ **候補手の一覧を更新するのと同じ 1 か所から呼ぶこと**（`showAnalyzeProgress`）。
  // 別々に更新すると、バーと評価値の数字が食い違う。
  const renderWinRate = () => {
    const card = winrateSource();
    const line = card?.best ?? null;
    // **押して切り替えられるのは 2 つ以上あるときだけ**（1 つのときに押せる
    // 見た目にすると、押しても何も起きない操作を作ることになる）。
    const many = engineCards.size > 1;
    winrateBar.classList.toggle("is-switchable", many);
    winrateBar.disabled = !many;

    if (!line) {
      // **まだ値が無いあいだは中立の見た目にして、50% と書かない**
      // ——「互角」と「まだ分からない」は別物。
      winrateBar.classList.add("is-empty");
      winrateWhite.style.width = "50%";
      winrateBar.title = card
        ? `${card.label}: 解析するとここに形勢が出ます`
        : "解析するとここに形勢が出ます";
      return;
    }
    // ⚠️ **勝率は Go 側の値。cp から計算し直さないこと**（上の ⚠️）。
    const black = Math.min(Math.max(line.score.winRate, 0), 1);
    const white = 1 - black;
    winrateBar.classList.remove("is-empty");
    winrateWhite.style.width = `${(white * 100).toFixed(1)}%`;
    // ⚠️ **数字は画面に出さず、カーソルを当てたときだけ出す**（盤の上に文字を
    // 積むと、そのぶん盤が小さくなる）。**整数の % で十分** —— 小数まで出しても
    // 読み分けられないうえ、1 手ごとに細かく揺れる。
    // 評価値も添える（label は Go 側が組み立てた文字列）。
    const head = `後手 ${Math.round(white * 100)}% ／ 先手 ${Math.round(black * 100)}%`;
    winrateBar.title =
      `${head}（評価値 ${line.score.label}）／ ${card?.label ?? ""}` +
      (many ? "　押すと別のエンジンに切り替わります" : "");
  };

  // 押すと次のエンジンに切り替える（登録順で回る）。
  //
  // ⚠️ **並べるのではなく切り替えるのが要点。** 盤の上に置ける段は 1 つで、
  // それでも「別のエンジンならどう見えるか」は確かめたい。
  winrateBar.addEventListener("click", () => {
    const ids = [...engineCards.keys()];
    if (ids.length < 2) {
      return;
    }
    const at = ids.indexOf(winrateEngineId);
    winrateEngineId = ids[(at + 1) % ids.length];
    renderWinRate();
  });

  // 解析に参加するエンジンぶんの枠を先に作る。
  //
  // **起動を待っているあいだも見出しを出す**（エンジンによっては評価関数の読み込みで
  // 数秒かかる）。何も出ないと、走っていないのか遅いのかが分からない。
  const buildEngineCards = (engines: { id: string; label: string; name: string }[]) => {
    engineCards.clear();
    analyzeEnginesBox.replaceChildren();
    // ⚠️ **選んでいたエンジンが今回も走っているなら、その選択を残すこと。**
    // 連続モードでは 1 手ごとにここを通るので、毎回 1 つ目に戻ると
    // 切り替えた意味が無い。居なくなっていたら先頭に戻す。
    if (!engines.some((e) => e.id === winrateEngineId)) {
      winrateEngineId = engines[0]?.id ?? "";
    }
    for (const e of engines) {
      const card = document.createElement("section");
      card.className = "analyze-engine";
      card.dataset.id = e.id;
      card.innerHTML = `
        <div class="analyze-engine-head">
          <span class="analyze-engine-name"></span>
          <span class="analyze-engine-meta note"></span>
        </div>
        <ol class="analyze-lines"></ol>
        <p class="analyze-engine-error note is-caution" hidden></p>
      `;
      const entry: EngineCard = {
        label: e.label,
        pending: true,
        startupMs: 0,
        reused: false,
        name: card.querySelector<HTMLElement>(".analyze-engine-name")!,
        meta: card.querySelector<HTMLElement>(".analyze-engine-meta")!,
        lines: card.querySelector<HTMLOListElement>(".analyze-lines")!,
        error: card.querySelector<HTMLElement>(".analyze-engine-error")!,
        best: null,
      };
      // 見出しは**設定タブで付けた名前**。エンジンが名乗る名前（`id name`）は
      // 繋いで初めて分かるので、届いたら括弧で足す（下の showEngineName）。
      showEngineName(entry, e.name ?? "");
      entry.meta.textContent = "エンジンを起動しています…";
      engineCards.set(e.id, entry);
      analyzeEnginesBox.appendChild(card);
    }
    // 起動を待つあいだの見た目（中立）に戻す。**押せるかどうかもここで決まる。**
    renderWinRate();
  };

  // エンジンが名乗った名前を見出しに足す。**設定の名前は消さない**
  // （同じ exe を option 違いで 2 つ登録していると、名乗る名前だけでは区別が付かない）。
  const showEngineName = (card: EngineCard, name: string) => {
    card.name.textContent =
      name && name !== card.label ? `${card.label}（${name}）` : card.label;
  };

  const showAnalyzeProgress = (card: EngineCard, p: AnalyzeProgress["progress"]) => {
    const lines = p.lines ?? [];
    // 盤の上の勝率バーは**最善手（順位 1）の評価値**で描く（評価値グラフに
    // 残すのと同じ値）。⚠️ **候補手の一覧と同じ 1 か所で更新すること。**
    // **値はエンジンごとに覚え、描くのは今選ばれている 1 つだけ**（押すと変わる）。
    card.best = lines[0] ?? null;
    renderWinRate();
    card.lines.replaceChildren();
    for (const [i, l] of lines.entries()) {
      const li = document.createElement("li");
      // ⚠️ **2 番手以降は一段小さく出す**（`is-sub`）。最善手と同じ大きさで
      // 並べると、どれが 1 番手なのかが順番でしか分からない。
      // 小さくするのは**評価値と次の 1 手だけ** —— その先の読み筋はもともと
      // 小さいので、そちらまで変えると 1 番手の行と揃わなくなる。
      li.className = i === 0 ? "analyze-line" : "analyze-line is-sub";

      const score = document.createElement("span");
      score.textContent = l.score.label;
      // 先手が良ければ青、後手が良ければ橙。**符号は Go 側が先手視点に揃えてある。**
      const side = l.score.mate !== 0 ? l.score.mate : l.score.cp;
      score.className =
        "analyze-score" + (side > 0 ? " is-black" : side < 0 ? " is-white" : "");

      // ⚠️ **読み筋の長さはエンジン次第。** 自作 engine は 1 手しか返さないので、
      // 深い読み筋があるかのように見せないこと（無ければ何も出さない）。
      //
      // 出すのは日本語表記（text）。⚠️ **text が無いときに moves へ落とさないこと** ——
      // Go 側は変換に失敗した手も USI のまま text に入れて返すので、text が空なのは
      // 「読み筋そのものが無い」ときだけ。落とすと、古い Go と繋いだときに
      // 静かに USI 表記へ戻る（気づけない）。
      const text = l.text ?? [];

      // **次の 1 手だけ評価値と同じ大きさで出す。** 読むのはほぼ「今この評価値が
      // 付いているのはどの手か」なので、そこだけ拾えれば足りる。以降の手は
      // **手順の裏付け**として小さいまま並べる（読み筋が長くても場所を取らない）。
      const first = document.createElement("span");
      first.className = "analyze-first";
      first.textContent = text[0] ?? "";

      const moves = document.createElement("span");
      moves.className = "analyze-moves";
      moves.textContent = text.slice(1).join(" ");
      // USI 表記はツールチップに残す（エンジンの出力をそのまま確かめたいとき用）。
      // ⚠️ **深さは候補ごとに違うことがある**（MultiPV は順位ごとに別々の info が
      // 来て、進み方も揃わない）。見出しの「深さ」は一番深いところなので、
      // その候補がどこまで読まれた答えなのかはここで確かめられるようにする。
      const usi = l.moves?.join(" ") ?? "";
      // ⚠️ **日本語の読み筋もツールチップに入れること**（2026-08-12）。解析の結果は
      // 盤の右の細い列に入ったので、**長い読み筋は行から溢れて省略される**。
      // 溢れたぶんを読む手段がここしか無い。
      const hint =
        (l.depth > 0 ? `深さ ${l.depth}　` : "") + (text.join(" ") || usi) +
        (text.length > 0 && usi ? `\n${usi}` : "");
      first.title = hint;
      moves.title = hint;

      // ⚠️ **押すとその手を指す。** MultiPV を増やす目的がまさにこれ ——
      // 「次善手を選んだらどう転ぶか」を辿るのに、候補を読んでから盤の上で
      // 同じ手を探し直させるのでは遠回りになる。
      //
      // **合法かどうかは Go 側が言う**（局面が変わると結果は消えるので、
      // ここに並んでいる手は今の局面のものだが、判定はフロントに書かない）。
      const play = l.moves?.[0] ?? "";
      if (play) {
        li.classList.add("is-playable");
        li.tabIndex = 0;
        li.title = `${text[0] ?? play} を指します（${hint}）`;
        const go = () => studyBoardUI.play(play);
        li.addEventListener("click", go);
        li.addEventListener("keydown", (e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            go();
          }
        });
      }

      li.append(score, first, moves);
      card.lines.appendChild(li);
    }

    const parts = [`深さ ${p.depth}`];
    if (p.nodes > 0) {
      parts.push(`${p.nodes.toLocaleString()} ノード`);
    }
    parts.push(`${(p.elapsedMs / 1000).toFixed(1)} 秒`);
    // 起動にかかった時間。**エンジンごとに違う**（NNUE を読むものは数秒かかる）ので
    // その行に出す。これが見えないと「遅い理由」が分からない。
    //
    // ⚠️ **接続を使い回したときは 0**（＝払っていない。「速かった」ではない）ので、
    // **0 を「起動 0.0 秒」と書かないこと** —— 意味が 2 通りになって読めなくなる。
    if (card.startupMs > 0) {
      parts.push(`起動 ${(card.startupMs / 1000).toFixed(1)} 秒`);
    } else if (card.reused) {
      parts.push("接続を使い回し");
    }
    card.meta.textContent = parts.join(" / ");
  };

  // ---- 全て解析（2026-08-12）---------------------------------------------
  //
  // **手順の範囲をまとめて解析する。** 中身は「手順リストを 1 つずつ押しては、
  // 考える秒数だけ待つ」の繰り返しで、**押す操作を人がやらなくてよくなるだけ**。
  // 棋譜を読み込んだ直後に一度かけると、評価値グラフが全部埋まる。
  //
  // ⚠️ **並べて走らせない。** 1 局面ずつ順に解析する —— エンジンのプロセスは
  // 1 回の解析のあいだだけ生きる作りなので、まとめて起こすと**手数ぶんの
  // プロセスが同時に立つ**（`USI_Hash` は GB 級になりうる）。
  //
  // ⚠️ **進めるのは「全部のエンジンが終わったら」**（`syncAnalyzeRunning`）。
  // 1 つ終わっただけで次へ行くと、残りのエンジンの結果が次の局面の裏で届く。
  //
  // **ここが順番を決めているだけで、評価値の記録は普段と同じ経路**（Go 側の
  // `recordEval`）。⚠️ **全て解析だけの特別な記録の道を作らないこと。**

  // 解析する最後の手数。**-1 なら走っていない。**
  let batchLast = -1;
  // 次に解析する手数。
  let batchNext = -1;
  // 次へ進んでいる最中か（done はエンジンの数だけ届くので、二重に進めない）。
  let batchStepping = false;
  // 今見ている局面の棋譜の手数の範囲（`StudyState.first` 由来）。
  let studyFirst = 0;
  let studyMoveCount = 0;

  const batchActive = () => batchLast >= 0;

  // 全て解析を始められない理由。**空なら押せる。**
  const batchBlockedReason = (): string => {
    if (!analyzeReady) {
      return "";
    }
    // ⚠️ **「無制限」では次へ進めない**（1 手目で考え続けて終わらない）。
    // 押せないことより、**なぜ押せないか**が出ているほうが大事。
    if ((Number(analyzeSeconds.value) || 0) <= 0) {
      return "全て解析は、考える秒数を決めてから（「無制限」では次の手へ進めません）";
    }
    // ⚠️ **手が 1 つも無くても押せてよい**（根の局面だけを解析する範囲は正当）。
    return "";
  };

  const syncBatchButton = () => {
    const blocked = batchBlockedReason();
    batchRun.textContent = batchActive() ? "停止" : "全て解析";
    batchRun.classList.toggle("is-active", batchActive());
    // 走っている最中は止められる。走っていないときは、解析できる局面かつ
    // 秒数が決まっているときだけ押せる。
    batchRun.disabled = !batchActive() && (!analyzeReady || blocked !== "");
    batchRun.title = batchActive()
      ? "全て解析を止めます（そこまでの評価値は残ります）"
      : blocked || "入力した範囲の手を、1 つずつ順に解析します";
    batchFrom.disabled = batchActive();
    batchTo.disabled = batchActive();
  };

  // 走っているエンジンが残っているか。**1 つ終わっただけでは解析は終わらない。**
  const syncAnalyzeRunning = () => {
    analyzeRunning = [...engineCards.values()].some((c) => c.pending);
    if (!analyzeRunning) {
      analyzeMeta.textContent = "";
    }
    syncAnalyzeButton();
    // ⚠️ **全部終わってから次の手へ**（上の ⚠️）。
    if (!analyzeRunning && batchActive()) {
      void batchStep();
    }
  };

  // stopBatch は全て解析をやめる。**そこまでの評価値は残る**（設計原則3）。
  const stopBatch = (message: string) => {
    batchLast = -1;
    batchNext = -1;
    batchStepping = false;
    syncBatchButton();
    if (message) {
      analyzeMeta.textContent = message;
    }
  };

  // batchStep は次の手へ進めて解析を仕掛ける。
  const batchStep = async () => {
    if (batchStepping || !batchActive()) {
      return;
    }
    batchStepping = true;
    try {
      if (batchNext > batchLast) {
        stopBatch(`全て解析: ${batchLast}手目まで終わりました`);
        return;
      }
      const n = batchNext;
      batchNext++;
      // ⚠️ **手順リストを押すのと同じ経路**（`GoTo`。手順は消さない）。
      // 「解析のために局面を動かす」専用の道を作らないこと。
      const ply = n - studyFirst;
      showStudy(await StudyService.GoTo(ply));
      // ⚠️ **`showStudy` の中の自動解析（連続モード）には任せない。**
      // あちらは「まだ解析していない局面なら」という条件で動くので、
      // **連続モードが切ってあると 1 手目で止まる。**
      await startAnalyze();
      analyzeMeta.textContent = `全て解析: ${n}〜${batchLast}手目のうち ${n}手目`;
      if (!analyzeRunning) {
        // 起動そのものに失敗した（エンジンが選ばれていない等）。
        // **ここで止めないと、残りの手でも同じ失敗を繰り返す。**
        stopBatch("");
      }
    } catch (err) {
      stopBatch(`全て解析を止めました: ${String(err instanceof Error ? err.message : err)}`);
    } finally {
      batchStepping = false;
    }
  };

  // 手順が変わったら範囲の欄を入れ直す（棋譜を読み込んだら 1〜151、など）。
  //
  // ⚠️ **走っている最中と、打っている最中の欄は触らないこと**（打ち込んだ値が
  // 消える）。手順の長さが変わったときだけ入れ直すので、**絞って指定した範囲は
  // 手を進めるまで残る。**
  let batchFilledFor = "";
  const fillBatchRange = () => {
    const key = `${studyFirst}:${studyMoveCount}`;
    if (batchActive() || key === batchFilledFor) {
      return;
    }
    batchFilledFor = key;
    if (document.activeElement !== batchFrom) {
      batchFrom.value = String(studyFirst);
    }
    if (document.activeElement !== batchTo) {
      batchTo.value = String(studyFirst + studyMoveCount);
    }
  };

  batchRun.addEventListener("click", () => {
    if (batchActive()) {
      stopBatch("全て解析を止めました");
      if (analyzeRunning) {
        void AnalyzeService.Stop();
      }
      return;
    }
    // 範囲は棋譜の手数で受け取る（**評価値グラフの横軸と同じ数え方**）。
    const lo = Math.max(studyFirst, Math.floor(Number(batchFrom.value) || 0));
    const hi = Math.min(studyFirst + studyMoveCount, Math.floor(Number(batchTo.value) || 0));
    if (hi < lo) {
      analyzeMeta.textContent = "全て解析: その範囲に手がありません";
      return;
    }
    batchLast = hi;
    batchNext = lo;
    syncBatchButton();
    void batchStep();
  });
  analyzeSeconds.addEventListener("change", syncBatchButton);

  const startAnalyze = async () => {
    analyzeStatus.hidden = true;
    analyzeStatus.textContent = "";
    engineCards.clear();
    analyzeEnginesBox.replaceChildren();
    renderWinRate();
    analyzeMeta.textContent = "エンジンを起動しています…";
    reserveLines();
    try {
      const st = await AnalyzeService.Start(
        Number(analyzeSeconds.value) || 0,
        Number(analyzeMultiPV.value) || 1,
      );
      analyzeSeq = st.seq;
      analyzedSfen = st.sfen;
      // 参加するエンジンは Go 側が決める（設定で「解析に使う」を付けたもの）。
      // **フロントで設定を読み直さないこと** —— 走っているのと違う顔ぶれが並ぶ。
      buildEngineCards(st.engines ?? []);
      analyzeRunning = engineCards.size > 0;
      analyzeMeta.textContent =
        engineCards.size > 1 ? `${engineCards.size} つのエンジンで解析しています` : "";
    } catch (err) {
      clearAnalyzeResult();
      analyzeStatus.textContent = `解析できません: ${String(err instanceof Error ? err.message : err)}`;
      analyzeStatus.hidden = false;
    } finally {
      syncAnalyzeButton();
    }
  };

  analyzeRun.addEventListener("click", () => {
    // ⚠️ **「停止」は全て解析も止めること。** 止めたのに次の手が始まったら、
    // 止める手段が無い（連続モードで止めたら止まったままにするのと同じ話）。
    if (batchActive()) {
      stopBatch("全て解析を止めました");
    }
    if (analyzeRunning) {
      // **打ち切っても、そこまでの評価値は残る**（設計原則3）。捨てる操作ではない。
      //
      // ⚠️ **連続モードでもここで止まったままにすること。** 止めた局面は
      // 「解析済み」として記録されるので、勝手に起こし直さない
      // （止めた直後にまた起動したら、止める手段が無くなる）。
      void AnalyzeService.Stop();
      return;
    }
    void startAnalyze();
  });

  // 連続モードで自動解析を仕掛けた局面。
  //
  // ⚠️ **失敗しても覚えること。** 覚えないと、解析できない状態（エンジンが 1 つも
  // 選ばれていない等）で**局面が変わるたびに起動を試み続ける**。
  let autoAnalyzed = "";

  // autoAnalyze は連続モードのときに解析を仕掛ける。
  //
  // **手を進めるたびに評価値を出し直すのがこのタブの目的**なので、既定で入。
  // ⚠️ **エンジンの寿命は変わらない**（前の解析を止めてから起こし直すだけで、
  // 常駐にはしない。`AnalyzeService.Start` が前の解析を打ち切る）。
  const autoAnalyze = () => {
    // ⚠️ **全て解析の最中は手を出さない。** あちらが局面と解析の順番を握って
    // いるので、連続モードが横から起こすと**同じ局面を 2 回起こして片方が
    // 打ち切られる**（打ち切られたほうの done で次の手へ進んでしまう）。
    if (batchActive()) {
      return;
    }
    if (!analyzeContinuous.checked || !analyzeReady || analyzeRunning) {
      return;
    }
    // 既に解析した局面と、仕掛けたばかりの局面は放っておく。
    if (!studySfen || studySfen === analyzedSfen || studySfen === autoAnalyzed) {
      return;
    }
    autoAnalyzed = studySfen;
    void startAnalyze();
  };

  analyzeContinuous.addEventListener("change", () => {
    // 入れた瞬間から効かせる。**仕掛け済みの記録は捨てる** ——
    // 前に失敗した局面でも、入れ直したなら試すのが期待どおり。
    autoAnalyzed = "";
    autoAnalyze();
  });

  // 解析タブの局面が変わったら、解析の可否と表示を追随させる。
  // **結果は局面と紐づける。**
  const syncAnalyze = () => {
    analyzeRow.hidden = !studyLoaded;
    // ⚠️ **局面があるあいだは枠を出しっぱなしにする**（中身が空でも）。
    // 解析のたびに畳むと、連続モードでは**1 手ごとに盤が上下に跳ねる**。
    analyzeEnginesBox.hidden = !studyLoaded;
    // 勝率バーも同じ（**まだ結果が無くても枠だけ出す**）。出たり消えたりすると
    // 盤が上下に動くうえ、盤の**上**の行なので動くと盤ごと押し下げる。
    winrateRow.hidden = !studyLoaded;
    // 評価値グラフも同じ（**まだ 1 点も無くても軸だけ出す**）。出たり消えたりすると
    // 盤が上下に動くうえ、「解析すると点が並ぶ場所」が見えているほうが分かりやすい。
    evalGraphRow.hidden = !studyLoaded;
    if (!studyLoaded) {
      // 空に戻った（撮り直した）。**仕掛けた記録も捨てる** —— 同じ局面をもう一度
      // 採ったときに、連続モードなのに解析が始まらない、ということが起きる。
      autoAnalyzed = "";
    }
    if (analyzedSfen && studySfen !== analyzedSfen) {
      // 採り直した・手を進めたので、前の評価値は今の盤の値ではなくなった。
      // ⚠️ **走っているなら止めること**（2026-08-11）。表示を消すだけだと、
      // **もう誰も読まない局面のためにエンジンのプロセスが生き続ける**
      // （手を 1 手進めるたびに増えるので、放っておくと重くなる）。
      if (analyzeRunning) {
        void AnalyzeService.Stop();
      }
      clearAnalyzeResult();
      analyzeSeq = -1;
      analyzeRunning = false;
    }
    syncAnalyzeButton();
    // ⚠️ **analyzeReady を見るので、押せるかどうかを決めたあとに呼ぶこと。**
    syncBatchButton();
    autoAnalyze();
  };

  // 届いたイベントの行き先は **seq（解析の世代）と engineId（どのエンジンか）の両方**で
  // 決まる。⚠️ **engineId を落とすと、複数エンジンの結果が 1 か所で上書きし合う。**
  const cardFor = (d: { seq: number; engineId: string }): EngineCard | undefined => {
    if (d.seq !== analyzeSeq) {
      return undefined; // 打ち切った解析の遅れてきた途中経過
    }
    return engineCards.get(d.engineId);
  };

  Events.On("analyze:info", (event: { data: AnalyzeProgress }) => {
    const card = cardFor(event.data);
    if (!card) {
      return;
    }
    showEngineName(card, event.data.engineName ?? "");
    showAnalyzeProgress(card, event.data.progress);
    // 評価値グラフは**間引いて**取り直す（点そのものは Go 側が既に持っている）。
    refreshEvalGraphSoon();
  });
  Events.On("analyze:done", (event: { data: AnalyzeDone }) => {
    const card = cardFor(event.data);
    if (!card) {
      return;
    }
    card.pending = false;
    card.startupMs = event.data.startupMs ?? 0;
    card.reused = !!event.data.reused;
    showEngineName(card, event.data.engineName ?? "");
    showAnalyzeProgress(card, event.data.progress);
    // ⚠️ **1 つ終わっただけでは解析は終わらない**（他のエンジンはまだ読んでいる）。
    syncAnalyzeRunning();
    // **その手の点はこれで確定する**（打ち切ったときも done は来る。設計原則3）。
    refreshEvalGraph();
  });
  Events.On("analyze:failed", (event: { data: AnalyzeFailure }) => {
    const card = cardFor(event.data);
    if (!card) {
      return;
    }
    // ⚠️ **そのエンジンの失敗であって、解析全体の失敗ではない**（設計原則3）。
    // 他のエンジンの評価値は出るので、**表示を消さずにその行にだけ理由を出す。**
    card.pending = false;
    card.meta.textContent = "";
    card.error.textContent = `解析できません: ${event.data.error}`;
    card.error.hidden = false;
    syncAnalyzeRunning();
  });

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
  const studyWarnings = root.querySelector<HTMLUListElement>("#study-warnings")!;
  const studyBack = root.querySelector<HTMLButtonElement>("#study-back")!;
  // 盤の脇の駒台（読み取り専用）。**訂正タブの駒台とは別物**で、
  // ドラッグの入口も「足りない駒」も持たない。
  const studyHandSlots = {
    black: root.querySelector<HTMLElement>("#study-hand-black-slot")!,
    white: root.querySelector<HTMLElement>("#study-hand-white-slot")!,
  };
  // 盤の右の列（解析の行・エンジンの結果・評価値グラフ・手順・SFEN）。
  // ⚠️ **中身は局面があるときだけ出す**（無いときは盤の代わりに案内を出す）。
  const studySide = root.querySelector<HTMLDivElement>("#study-side")!;
  const studyMoves = root.querySelector<HTMLDivElement>("#study-moves")!;
  const studyUndo = root.querySelector<HTMLButtonElement>("#study-undo")!;
  const studyMoveStatus = root.querySelector<HTMLParagraphElement>("#study-move-status")!;
  // 中身は訂正タブと同じ .hand-zone だが、**見出しは出さない**（`is-readonly`）。
  // 盤との位置関係そのものが「どちらの駒台か」の説明になっているので、
  // 掴む相手でもない箱に文字を足すと、そのぶん駒台が縦に伸びるだけになる。
  // 読み上げ用に aria-label だけ持たせる。
  const studyHandZones = [true, false].map((black) => {
    const zone = document.createElement("div");
    zone.className = "hand-zone is-readonly";
    zone.dataset.black = String(black);
    zone.setAttribute("aria-label", `${black ? "先手" : "後手"}の駒台`);
    zone.innerHTML = `<div class="hand-chips"></div>`;
    (black ? studyHandSlots.black : studyHandSlots.white).appendChild(zone);
    return zone;
  });
  // 訂正タブ側の一行。訂正の操作が通らなかった理由と、確定できない理由を出す。
  const editStatus = root.querySelector<HTMLParagraphElement>("#edit-status")!;

  // showStudy は解析タブの表示一式（盤・駒台・SFEN・警告・手順）を描く。
  //
  // ⚠️ **手順とグリッドの描き直しを落とさないこと。** 盤だけ更新すると、
  // **前の局面の合法手が光ったまま**になり、指せない手を指そうとする。
  // 盤の操作から呼ばれたとき（`fromBoard`）だけは、あちらが既に描いているので
  // 二度描かない（**同じ状態を 2 か所から描くと、どちらが今かが分からなくなる**）。
  const showStudy = (st: StudyState, o?: { fromBoard?: boolean }) => {
    studyLoaded = !!st.loaded;
    studySfen = st.sfen ?? "";
    // 全て解析の範囲は**棋譜の手数**で受け取る（評価値グラフの横軸と同じ数え方）。
    // ⚠️ **`Move.Number` は根からの手数**（`GoTo` の引数）なので、起点を足す。
    studyFirst = st.first ?? 0;
    studyMoveCount = (st.moves ?? []).length;
    fillBatchRange();
    studyStage.hidden = !studyLoaded;
    studyBoard.hidden = !studyLoaded;
    studyPlaceholder.hidden = studyLoaded;
    studyBack.hidden = !studyLoaded;
    // 駒台は局面の一部なので、局面があるあいだは**空でも出す**
    // （持ち駒が 0 枚であることも局面の情報）。
    studyHandSlots.black.hidden = !studyLoaded;
    studyHandSlots.white.hidden = !studyLoaded;
    studySide.hidden = !studyLoaded;
    studyUndo.disabled = (st.ply ?? 0) === 0;
    if (studyLoaded) {
      studyBoard.setAttribute("sfen", st.boardSfen);
      // 手番と手数は SFEN に入っているが、読むのに要るのは文字のほう。
      const n = st.moveNumber > 0 ? ` / ${st.moveNumber}手目` : "";
      studySfenOut.textContent = `${st.sfen}`;
      studySfenOut.title = `${st.turnLabel}${n}`;
      showStudyHand(st.hands ?? []);
    } else {
      studySfenOut.textContent = "-";
      showStudyHand([]);
    }
    // ⚠️ **合法手が出せなくても局面は生きている**（設計原則3）。玉の欠けた局面などでは
    // 手を進められないだけで、盤も解析もそのまま使える。**理由は出すこと** ——
    // 何も出さないと「駒を押しても光らない」の理由が分からない。
    studyMoveStatus.textContent = st.legalError ?? "";
    studyMoveStatus.hidden = !st.legalError;
    // 確定した局面でも警告は出うる（詰将棋のように「論理的におかしくても正しい」
    // 局面があるため。設計原則3）。変な評価値が出たときの手掛かりになる。
    fillWarnings(studyWarnings, st.warnings ?? []);
    if (!o?.fromBoard) {
      studyBoardUI.render(st.loaded ? st : null);
    }
    syncAnalyze();
    // 局面が変わったら点を取り直す（**戻った位置の縦線も動く**）。
    refreshEvalGraph();
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
    for (const zone of studyHandZones) {
      const black = zone.dataset.black === "true";
      const chips = zone.querySelector<HTMLDivElement>(".hand-chips")!;
      chips.replaceChildren();
      let total = 0;
      // 並びは Go 側の Inventory の順（歩香桂銀金角飛王）。
      // **後手は逆順**（駒が 180 度回っているので、そちら側から読んで同じ並びになる）。
      for (const s of black ? inv : [...inv].reverse()) {
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
    studyMoveStatus.hidden = true;
    try {
      showStudy(await StudyService.Adopt());
    } catch (err) {
      editStatus.textContent = String(err instanceof Error ? err.message : err);
      editStatus.classList.add("is-error");
      return;
    }
    selectTab(studyTab);
  };

  // 解析タブの盤の操作（手を進める UI）。**合法手だけ。**
  //
  // ⚠️ **訂正タブの mountEditor とは別物。** あちらはドラッグで自由に置く面で、
  // こちらはクリック 2 回で合法手だけを辿る面。**同じグリッドを流用しないこと。**
  const studyBoardUI = mountStudyBoard({
    stage: studyStage,
    handSlots: studyHandSlots,
    movesPanel: studyMoves,
    // 指したあとの局面は showStudy がそのまま描く（盤・駒台・SFEN・警告）。
    onState: (st) => showStudy(st, { fromBoard: true }),
    // 空文字は「理由を消す」（駒を掴み直したときなど）。**出しっぱなしにしないこと** ——
    // 前の操作の理由が残っていると、今の操作が失敗したように見える。
    onError: (message) => {
      studyMoveStatus.textContent = message;
      studyMoveStatus.hidden = message === "";
    },
  });
  studyUndo.addEventListener("click", () => studyBoardUI.undo());

  // 評価値グラフ（2026-08-12）。**手順の 1 手ごとの最善手の評価値**を折れ線にする。
  //
  // ⚠️ **点はここに溜めない。** 持っているのは Go 側（`StudyService.Evals`）で、
  // **手順を切ったときにどこまで捨てるかを知っているのはあちらだけ**。
  // フロントにも溜めると、戻って別の手を指したときに片方だけ古い値が残る。
  const evalGraphUI = mountEvalGraph({
    host: root.querySelector<HTMLElement>("#eval-graph")!,
    range: root.querySelector<HTMLSelectElement>("#eval-graph-range")!,
    from: root.querySelector<HTMLInputElement>("#eval-graph-from")!,
    to: root.querySelector<HTMLInputElement>("#eval-graph-to")!,
    fields: root.querySelector<HTMLElement>("#eval-graph-fields")!,
    legend: root.querySelector<HTMLElement>("#eval-graph-legend")!,
    readout: root.querySelector<HTMLElement>("#eval-graph-readout")!,
    // **押したらその局面へ戻る**（手順のチップと同じ「戻って見る」操作。手順は消さない）。
    onSeek: (ply) => {
      void (async () => {
        try {
          showStudy(await StudyService.GoTo(ply));
        } catch (err) {
          studyMoveStatus.textContent = String(err instanceof Error ? err.message : err);
          studyMoveStatus.hidden = false;
        }
      })();
    },
  });

  // 途中経過からの取り直しを間引くためのタイマー（0 なら待っていない）。
  let evalGraphTimer = 0;

  // refreshEvalGraph は Go から点を取り直して描く。
  //
  // **呼ぶのは「局面が変わったとき」と「解析が 1 つ終わったとき」。**
  const refreshEvalGraph = () => {
    if (evalGraphTimer !== 0) {
      window.clearTimeout(evalGraphTimer);
      evalGraphTimer = 0;
    }
    void (async () => {
      try {
        evalGraphUI.render(await StudyService.Evals());
      } catch {
        // 取れなくてもグラフが古いままになるだけ。**盤も解析も止めない**（設計原則3）。
      }
    })();
  };

  // 途中経過から呼ぶ側。**間引く。**
  //
  // ⚠️ **info のたびに取り直さないこと** —— 深さが 1 つ進むたびに、しかも
  // エンジンの数だけ届くので、そのまま往復させると読み筋の更新より頻繁になる。
  // ⚠️ **かといって done だけにもできない** —— 考える秒数が「無制限」のときは
  // **止めるまで done が来ない**ので、その手の点がいつまでも出ない。
  const refreshEvalGraphSoon = () => {
    if (evalGraphTimer !== 0) {
      return;
    }
    evalGraphTimer = window.setTimeout(() => {
      evalGraphTimer = 0;
      refreshEvalGraph();
    }, 1000);
  };

  // 解析タブ → 訂正タブ。**解析の結果は捨てる。**
  // 直した結果を採り直せば別の局面になるので、前の評価値を残す意味が無い。
  studyBack.addEventListener("click", () => {
    if (analyzeRunning) {
      void AnalyzeService.Stop();
    }
    selectTab(editTab);
  });

  const editor = mountEditor({
    stage: boardStage,
    handSlots: {
      black: root.querySelector<HTMLElement>("#hand-black-slot")!,
      white: root.querySelector<HTMLElement>("#hand-white-slot")!,
      missing: root.querySelector<HTMLElement>("#missing-slot")!,
    },
    resetButton: root.querySelector<HTMLButtonElement>("#edit-reset")!,
    panel: root.querySelector<HTMLElement>("#editor")!,
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
      syncTrain();
      // ⚠️ **ここから解析の状態を触らないこと。** 訂正タブの局面と解析タブの局面は
      // 別物で、繋ぐのは「この局面を解析する」を押したときの 1 回だけ。
      if (!st?.loaded) {
        boardHandRow.hidden = true;
        fillWarnings(boardWarnings, []);
        return;
      }
      showBoard(st.boardSfen, true);
      sfenOut.textContent = st.sfen || st.boardSfen || "-";
      showEditHand(st.inventory ?? []);
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

  // 盤面タブの駒台。**訂正中の局面の値**で、先後の割り振りと**未決のぶん**まで出す
  // (「認識詳細情報」側は認識した時点の推定枚数のまま)。
  //
  // 未決が残っているあいだは局面が確定しない(SFEN が組み上がらない)ので、
  // **訂正モードを開いていなくても見えるようにしておく**。
  const showEditHand = (inv: Stock[]) => {
    const fmt = (pick: (s: Stock) => number) =>
      inv
        .filter((s) => pick(s) > 0)
        .map((s) => `${s.name}${pick(s)}`)
        .join(" ");
    const black = fmt((s) => s.handBlack);
    const white = fmt((s) => s.handWhite);
    const rest = fmt((s) => s.unassigned);

    const parts: string[] = [];
    if (black) {
      parts.push(`先手 ${black}`);
    }
    if (white) {
      parts.push(`後手 ${white}`);
    }
    if (rest) {
      parts.push(`先後未決 ${rest}`);
    }
    boardHandOut.textContent = parts.join(" / ");
    boardHandOut.classList.toggle("is-unassigned", !!rest);
    boardHandRow.hidden = parts.length === 0;
  };

  // sfen が空なら盤を隠して理由を出す。撮る前と「撮ったが認識できなかった」は別物なので
  // 文言を分ける(認識失敗はキャプチャの失敗ではない。設計原則3)。
  const showBoard = (sfen: string, captured: boolean) => {
    // 盤ごと箱(.board-stage)を隠す。箱は正方形の場所取りをしているので、
    // 中身が無いまま残すと空白が居座る。
    boardStage.hidden = !sfen;
    if (!sfen) {
      board.hidden = true;
      placeholder.hidden = false;
      placeholder.textContent = captured
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

  const showResult = (result: CaptureResult) => {
    // 認識できなくてもキャプチャは成功している(設計原則3: 段階的に劣化する)。
    // 保存できたことと、認識できたかどうかを分けて出す。
    if (result.recognizeError) {
      status.textContent = `保存しました: ${result.path} / 盤面は認識できませんでした: ${result.recognizeError}`;
      status.classList.add("is-warn");
      status.classList.remove("is-error");
    } else {
      status.textContent = `保存しました: ${result.path} (${result.width}x${result.height})`;
      status.classList.remove("is-error", "is-warn");
    }

    showPath(result.path);
    // 撮り直したら解析タブは空に戻す。**前の局面の盤と評価値を残さない**
    // （新しい認識結果の裏で生き残っていると、どちらが今の話か分からなくなる）。
    if (analyzeRunning) {
      void AnalyzeService.Stop();
    }
    void (async () => {
      try {
        showStudy(await StudyService.Clear());
      } catch {
        /* 消せなくても撮影は成功している（設計原則3）。次の Adopt で入れ替わる。 */
      }
    })();
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
      showBoard("", true);
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
    // 前の 1 枚の送信結果を残さない（別の画像の話になるため）。
    trainSendStatus.textContent = "";
    trainSendStatus.classList.remove("is-error");
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
  // ⚠️ **入力タブに置いてあるのが要点。** 枠を出すのは「画面から撮る」ための操作で、
  // 入力の口の 1 つでしかない。ツールバーに置くと常設の機能に見える。
  root.querySelector<HTMLButtonElement>("#input-show-frame")!.addEventListener("click", () => {
    void CaptureService.ShowFrame();
  });

  // 枠のツールバーの □ と同じ操作。**枠を出してからでないと合わせる先が無い**ので、
  // 先に出しておく（HideFrame と違い ShowFrame は出ていれば何もしない）。
  const inputFit = root.querySelector<HTMLButtonElement>("#input-fit")!;
  inputFit.addEventListener("click", () => {
    void (async () => {
      inputFit.disabled = true;
      status.textContent = "盤を探しています…";
      status.classList.remove("is-error", "is-warn");
      try {
        await CaptureService.ShowFrame();
        const r = await CaptureService.FitFrame();
        status.textContent = r.message;
        // 見つからないのはエラーではない（設計原則3）。枠は 1px も動いていない。
        status.classList.toggle("is-warn", !r.fitted);
      } catch (err) {
        status.textContent = `盤を探せませんでした: ${String(err instanceof Error ? err.message : err)}`;
        status.classList.add("is-error");
      } finally {
        inputFit.disabled = false;
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
      // 逆順にすると、100 手の棋譜を読んでもリストが先頭のまま出る
      // （最終手まで進んでいるのに、そこが見えない）。
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
  // URL 欄で Enter を押したら読み込む（打ってからボタンへ手を戻さずに済む）。
  kifuURL.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
      e.preventDefault();
      loadFromURL();
    }
  });

  // 学習データを育てながら使うための入口。suteme は一度読んだ推論器をキャッシュするので、
  // データを更新してもこれを押すまで(あるいは再起動するまで)反映されない。
  const showRecognizer = (st: { source: string; ready: boolean; error: string }) => {
    configuredDir = st.source ?? "";
    if (st.error) {
      recognizer.textContent = `認識器を読み込めません: ${st.error}`;
      recognizer.className = "recognizer is-error";
      markDebug("error");
      return;
    }
    if (st.ready) {
      recognizer.textContent = `認識器: ${st.source}`;
    } else {
      recognizer.textContent = "認識器: suteme の既定の場所を探します";
    }
    recognizer.className = "recognizer";
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

  const showSettings = (s: {
    fitOnStartup: boolean;
    path: string;
    training: { enabled: boolean; host: string; port: number; token: string; target: string };
    engines: EngineSettings[] | null;
    ponanzaConstant: number;
  }) => {
    fitOnStartup.checked = s.fitOnStartup;
    // ⚠️ **既定値の解決は Go 側**（`analyze.PonanzaConstantOr`）。返ってきた値を
    // そのまま入れるだけにすること（フロントに既定を書くと 2 か所に散る）。
    ponanzaConstant.value = String(s.ponanzaConstant);
    settingsPath.textContent = s.path || "(保存先を決められませんでした)";
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

  // エンジン 1 つに実際に繋いでみる。**1 行ずつ**（まとめて起こすと、どれが遅くて
  // どれが落ちたのか分からない）。確かめたら閉じるので、プロセスは残らない。
  const checkEngine = async (id: string, row: HTMLElement, btn: HTMLButtonElement) => {
    const note = engineRowNote(row);
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
      // **何を送ったかまで出す。** setoption には応答が返らないので、
      // 効いているかどうかを確かめる手掛かりがこれしかない。
      const applied =
        r.options > 0
          ? `option ${r.options} 件を宣言、${r.applied} 件を送信（既定値を含む）`
          : "option の宣言はありません";
      // ⚠️ **起動の時間は「解析タブで最初に解析するとき」に 1 回払う**
      // （そのあとは接続を使い回す）。繋ぎ先を選ぶ材料になるので出しておく。
      const startup = `起動 ${(r.startupMs / 1000).toFixed(1)} 秒（解析タブで最初に解析するときにかかります）`;
      note.textContent = `繋がりました: ${r.name} / ${applied} / ${startup}`;
    } catch (err) {
      note.textContent = `確認できませんでした: ${String(err instanceof Error ? err.message : err)}`;
      note.classList.add("is-error");
    } finally {
      btn.disabled = false;
    }
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

    engineList.replaceChildren();
    for (const e of engines) {
      const row = document.createElement("li");
      row.className = "engine-row";
      row.dataset.id = e.id;
      row.innerHTML = `
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
        void checkEngine(e.id, row, checkBtn);
      });
      row.querySelector<HTMLButtonElement>(".engine-remove")!.addEventListener("click", () => {
        void applyEngineChange(() => SettingsService.RemoveEngine(e.id));
      });

      engineList.appendChild(row);
    }

    // 打っている最中だった欄にフォーカスと文字を戻す。
    if (keep) {
      const row = engineList.querySelector<HTMLElement>(`.engine-row[data-id="${keep}"]`);
      const el = row?.querySelector<HTMLInputElement>(`.${keepClass.split(" ")[0]}`);
      if (el) {
        if (el.type === "text") {
          el.value = keepValue;
        }
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

  void (async () => {
    try {
      showSettings(await SettingsService.Settings());
    } catch (err) {
      settingsStatus.textContent = `設定を読み込めませんでした: ${String(err)}`;
      settingsStatus.classList.add("is-error");
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
