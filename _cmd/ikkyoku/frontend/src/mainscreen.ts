// メイン画面。アプリ本体。
//
// 撮った画像から割り出した盤面・SFEN・警告を出す画面。起動時は非表示で、最初の
// キャプチャで現れる。**この画面を閉じるとアプリが終了する**(枠を閉じても終了しない)。
//
// 画面は 3 タブ(盤面 / デバッグ / 設定)。
//
// **盤面タブはこのアプリの作業場所。** 今は撮った局面を見るだけだが、ここが
// **訂正 → 決定 → 局面をいじる → その評価値を見る**を行う面になる
// (Phase 3 の局面矯正、Phase 5 の検討 UI)。
//
// デバッグタブに寄せてあるのは**認識精度を追うための情報**(認識器の状態・検出の
// 信頼度・盤面領域・推論器・保存先・撮った画像)。**盤面タブから外したのはこれらで
// あって、「項目を足すな」ではない。** 置き場所は「局面を読む・直す・動かすのに要るか
// (盤面)」「認識がどれくらい外したかを見るものか(デバッグ)」で決める。
//
// **駒台と警告は両方に出す。** デバッグ側では「認識がどれくらい外したか」だが、
// 盤面側では**局面を直すために要る情報**(駒台の枚数は駒数保存則の逆算そのもの、
// 警告は「どこが怪しいか」の提示)。同じ値でも読む目的が違う。
//
// **撮る操作はここには置かない。** 撮るのは盤に枠を合わせている最中の操作なので、
// 枠のツールバーとホットキーで完結する。ここは撮れたものを見る側。
//
// 枠(frame.ts)とは別ウィンドウなので、ここに置いた要素はキャプチャに写り込まない
// ——ただし**枠に重なる位置に動かすと写り込む**(画面の合成結果を撮るため)。初回だけ
// Go 側が枠の外へ逃がす(captureservice.go の placeMainBesideFrame)。
import { Clipboard, Events } from "@wailsio/runtime";
import { FiCopy, FiImage } from "react-icons/fi";
import { AnalyzeService, CaptureService, SettingsService, TrainingService } from "../bindings/ikkyoku-app";
import { iconMarkup } from "./icon";
import { mountEditor } from "./editor";
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
  score: { cp: number; mate: number; label: string };
  moves: string[];
}

interface AnalyzeProgress {
  seq: number;
  done: boolean;
  progress: {
    depth: number;
    nodes: number;
    elapsedMs: number;
    lines: AnalyzeLine[];
  };
}

interface AnalyzeFailure {
  seq: number;
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
  root.innerHTML = `
    <div class="main-screen">
      <div class="main-toolbar">
        <div class="tabs" role="tablist">
          <button id="tab-board" class="tab is-active" type="button"
                  role="tab" aria-selected="true" aria-controls="panel-board">盤面</button>
          <button id="tab-debug" class="tab" type="button"
                  role="tab" aria-selected="false" aria-controls="panel-debug">デバッグ</button>
          <button id="tab-settings" class="tab" type="button"
                  role="tab" aria-selected="false" aria-controls="panel-settings">設定</button>
        </div>
        <span class="spacer"></span>
        <span class="hint">撮るのは枠のツールバー、または <span class="hotkey-hint">Alt+S</span></span>
        <button id="show-frame-btn" class="ghost-btn" type="button">枠を表示</button>
      </div>

      <div id="panel-board" class="panel is-active" role="tabpanel" aria-labelledby="tab-board">
        <!-- 上段。左に警告、右に「認識結果に戻す」。
             局面として成立していない点は**盤より上に出す**(訂正しながら見るものなので、
             盤の下だと見落とすし、件数で下の行が動く)。中身は**今の局面**(EditState)の
             値で、デバッグタブ側の同じ見出しとは別物(あちらは認識した時点の記録)。
             やり直しのボタンは**訂正した内容を捨てる操作**なので、押し間違えないよう
             盤から遠い右上に離し、赤くしてある。 -->
        <div class="board-head">
          <ul id="board-warnings" class="warnings is-compact" hidden></ul>
          <button id="edit-reset" class="danger-btn" type="button" hidden
                  title="訂正を捨てて、認識したときの盤面に戻します">認識結果に戻す</button>
        </div>
        <div class="board-area">
          <!-- 撮った画像。**訂正中だけ盤の左に出す**(確定したら畳む)。
               訂正は「原本を見ながら 1 マスずつ直す」作業なので、原本がデバッグタブの
               向こう側にあると成立しない。**盤は 560px より大きくならない**ので、
               左の余白はもともと遊んでおり、ここに置くのは盤を狭めない。

               ⚠️ **出すのは画像そのものだけ。** 重ね表示・信頼度・保存先は
               「認識がどれくらい外したか」の情報なのでデバッグタブに残す
               (盤面タブの基準は「局面を読む・直す・動かすのに要るか」)。
               画像の src はデバッグ側と**同じ CaptureResult.thumbnail**。 -->
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
        <!-- エンジン解析（Phase 4）。**確定した局面にだけかかる。**
             手番と駒台の先後が決まらないと SFEN が組み上がらないので、それまでは
             ボタンを押せなくして理由を出す（決めていないことを勝手に決めない。設計原則5）。

             ⚠️ **局面を直したら結果を消す。** 評価値は「その局面の」値なので、
             盤が変わったあとも残っていると、別の局面の値を今の盤の評価だと読ませる。 -->
        <div id="analyze-row" class="analyze-row" hidden>
          <button id="analyze-run" class="ghost-btn" type="button">解析</button>
          <label class="analyze-time">
            <select id="analyze-seconds" title="考える時間。途中で切っても、そこまでの評価値は出ます">
              <option value="1">1秒</option>
              <option value="3" selected>3秒</option>
              <option value="10">10秒</option>
              <option value="30">30秒</option>
            </select>
          </label>
          <span id="analyze-meta" class="note"></span>
        </div>
        <!-- 候補手（MultiPV）。**1 本しか来なくても一覧の形で出す** ——
             次善手を辿るのが構想の中心なので、ここが複数本になるのが前提の作り。 -->
        <ol id="analyze-lines" class="analyze-lines" hidden></ol>
        <p id="analyze-status" class="note is-caution" hidden></p>
        <!-- 訂正した局面を suteme の学習データとして送る。**確定してから出す**
             (訂正の途中の盤面を送る意味が無い)。設定で有効にしていないときは
             行ごと出さない。**押したときだけ送る**(自動送信はしない)。 -->
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
             (デバッグタブ側は認識した時点の推定枚数で「先後不明」のまま)。
             訂正中は盤の脇に駒そのものが並ぶので、こちらは文字の要約。 -->
        <div id="board-hand-row" class="hand-row" hidden>
          <span class="field-label">駒台</span>
          <span id="board-hand" class="hand"></span>
        </div>
      </div>

      <div id="panel-debug" class="panel" role="tabpanel" aria-labelledby="tab-debug" hidden>
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
        <p id="status" class="status" role="status" aria-live="polite">
          ガイド枠を盤面に合わせて撮影してください。
        </p>

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

             ⚠️ **「外部を使う」のチェックボックスは置かない。** パスが空なら同梱、
             入っていれば外部。2 つ持つと「パスが入っているのに無効」という
             食い違いが起きる。 -->
        <div class="setting-group">
          <span class="setting-title">解析エンジン</span>
          <span class="setting-note">
            USI を話すエンジンの実行ファイルを指定します（やねうら王・水匠など）。
            <strong>空にすると同梱のエンジン</strong>に戻ります。
            <code>setoption</code> で送る値は設定ファイルの <code>engine.options</code> に書けます。
          </span>
          <div class="setting-fields">
            <label class="field is-wide">
              <span class="field-label">実行ファイル</span>
              <input id="engine-path" type="text" spellcheck="false"
                     placeholder="空なら同梱のエンジンを使います" />
            </label>
            <button id="engine-browse" class="ghost-btn" type="button"
                    title="実行ファイルを選びます">参照…</button>
            <button id="engine-clear" class="ghost-btn" type="button"
                    title="同梱のエンジンに戻します">同梱に戻す</button>
            <button id="engine-check" class="ghost-btn" type="button"
                    title="実際に起動して、USI で応答するか確かめます">接続を確認</button>
          </div>
          <p id="engine-status" class="status" role="status" aria-live="polite"></p>
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

  const showFrame = root.querySelector<HTMLButtonElement>("#show-frame-btn")!;
  const reloadBtn = root.querySelector<HTMLButtonElement>("#reload-btn")!;
  const recognizer = root.querySelector<HTMLParagraphElement>("#recognizer")!;
  const board = root.querySelector<HTMLElement>("#board")!;
  const placeholder = root.querySelector<HTMLParagraphElement>("#board-placeholder")!;
  const sfenOut = root.querySelector<HTMLElement>("#sfen")!;
  const confidenceRow = root.querySelector<HTMLDivElement>("#confidence-row")!;
  const confidenceOut = root.querySelector<HTMLElement>("#confidence")!;
  // 駒台と警告は両方のタブに出るが、**出所が違う**。
  //
  //   デバッグタブ … 撮って認識した時点の値。**訂正しても変わらない**
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

  // タブ。盤面タブは局面を扱う面(盤・SFEN・駒台・警告。今後ここに訂正と検討が乗る)、
  // デバッグタブは認識精度を追うための面(認識器の状態・信頼度・撮った画像)。
  // 足す項目がどちらに載るかはこの基準で決める。盤はできるだけ大きく見せたいので、
  // 盤の周りに積む行は短く保つこと。
  //
  // 隠すのは表示だけで、両方のパネルの中身は常に更新する(タブを切り替えた瞬間に
  // 古い内容が出ることが無いように)。
  const tabs: { tab: HTMLButtonElement; panel: HTMLElement }[] = [
    { tab: root.querySelector<HTMLButtonElement>("#tab-board")!, panel: root.querySelector<HTMLElement>("#panel-board")! },
    { tab: root.querySelector<HTMLButtonElement>("#tab-debug")!, panel: root.querySelector<HTMLElement>("#panel-debug")! },
    { tab: root.querySelector<HTMLButtonElement>("#tab-settings")!, panel: root.querySelector<HTMLElement>("#panel-settings")! },
  ];
  const debugTab = tabs[1].tab;

  const selectTab = (target: HTMLButtonElement) => {
    for (const { tab, panel } of tabs) {
      const active = tab === target;
      tab.classList.toggle("is-active", active);
      tab.setAttribute("aria-selected", String(active));
      panel.classList.toggle("is-active", active);
      panel.hidden = !active;
    }
    if (target === debugTab) {
      debugTab.classList.remove("has-warn", "has-error");
    }
  };

  for (const { tab } of tabs) {
    tab.addEventListener("click", () => selectTab(tab));
  }

  // 警告やエラーはデバッグタブの中にあるので、盤面タブを見ているあいだは気づけない。
  // タブ側に印を出して「見に行くべきものがある」ことだけ伝える。
  const markDebug = (level: "" | "warn" | "error") => {
    debugTab.classList.remove("has-warn", "has-error");
    if (debugTab.classList.contains("is-active") || level === "") {
      return;
    }
    debugTab.classList.add(level === "error" ? "has-error" : "has-warn");
  };

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

  // 訂正中に盤の左へ出す「撮った画像」。**デバッグタブのサムネイルと同じ値**を描くだけで、
  // 別の経路で取り直さない(片方だけ更新されると、どちらが今の 1 枚か分からなくなる)。
  //
  // 出す条件は「訂正中」かつ「画像がある」の両方。撮る前と、確定したあとは畳む。
  const captureRef = root.querySelector<HTMLDivElement>("#capture-ref")!;
  const captureRefImg = root.querySelector<HTMLImageElement>("#capture-ref-img")!;
  let editingNow = false;
  // 画像の有無は自前で覚える。**`img.src` は空文字を入れてもページの URL に解決される**
  // ので、要素から「画像が入っているか」は読めない。
  let hasShot = false;
  const syncCaptureRef = () => {
    captureRef.hidden = !editingNow || !hasShot;
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
  let lastRegion: { x1: number; y1: number; x2: number; y2: number } | null = null;

  const syncTrain = () => {
    const ready = !editingNow && editLoaded && !!shotFullPath && !!lastRegion;
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

  // ---- エンジン解析（Phase 4） --------------------------------------------
  //
  // **確定した局面にだけかかる。** 手番か駒台の先後が未決だと SFEN が組み上がらず、
  // Go 側が始める前に断る（決めていないことを勝手に決めない。設計原則5）。
  //
  // 反復深化なので**深さが 1 つ終わるたびに答えが更新される**。終わるまで黙って
  // いると数秒固まって見えるので、途中経過をそのまま出して育つ様子を見せる。
  //
  // ⚠️ **局面を直したら結果を消す。** 評価値は「その局面の」値で、盤が変わったあとも
  // 残っていると別の局面の値を今の盤の評価として読ませることになる。
  const analyzeRow = root.querySelector<HTMLDivElement>("#analyze-row")!;
  const analyzeRun = root.querySelector<HTMLButtonElement>("#analyze-run")!;
  const analyzeSeconds = root.querySelector<HTMLSelectElement>("#analyze-seconds")!;
  const analyzeLines = root.querySelector<HTMLOListElement>("#analyze-lines")!;
  const analyzeMeta = root.querySelector<HTMLElement>("#analyze-meta")!;
  const analyzeStatus = root.querySelector<HTMLParagraphElement>("#analyze-status")!;

  // 今の解析の世代。**打ち切った解析の途中経過は後から届く**ので、これで捨てる。
  let analyzeSeq = -1;
  let analyzeRunning = false;
  // 何を解析した値なのか。今の局面と食い違ったら表示を消す。
  let analyzedSfen = "";
  // 解析できる局面か（EditState.sfen が埋まっているか）。
  let analyzeReady = false;
  // 答えたエンジンの名前。**何が出した評価値なのかは見せる**（繋ぎ先を差し替えられる以上、
  // 出所を伏せると比べようがない）。
  let analyzeEngine = "";

  const syncAnalyzeButton = () => {
    analyzeRun.textContent = analyzeRunning ? "停止" : "解析";
    analyzeRun.classList.toggle("is-active", analyzeRunning);
    analyzeRun.disabled = !analyzeRunning && !analyzeReady;
    analyzeRun.title = analyzeRunning
      ? "ここまでの結果で打ち切ります"
      : analyzeReady
        ? "この局面をエンジンに解析させます"
        : "手番と駒台の先後を決めると解析できます";
  };

  const clearAnalyzeResult = () => {
    analyzedSfen = "";
    analyzeLines.replaceChildren();
    analyzeLines.hidden = true;
    analyzeMeta.textContent = "";
    analyzeStatus.hidden = true;
    analyzeStatus.textContent = "";
  };

  const showAnalyzeProgress = (p: AnalyzeProgress["progress"]) => {
    const lines = p.lines ?? [];
    analyzeLines.replaceChildren();
    for (const l of lines) {
      const li = document.createElement("li");
      li.className = "analyze-line";

      const score = document.createElement("span");
      score.textContent = l.score.label;
      // 先手が良ければ青、後手が良ければ橙。**符号は Go 側が先手視点に揃えてある。**
      const side = l.score.mate !== 0 ? l.score.mate : l.score.cp;
      score.className =
        "analyze-score" + (side > 0 ? " is-black" : side < 0 ? " is-white" : "");

      const moves = document.createElement("span");
      moves.className = "analyze-moves";
      // ⚠️ **読み筋の長さはエンジン次第。** 自作 engine は 1 手しか返さないので、
      // 深い読み筋があるかのように見せないこと（無ければ何も出さない）。
      moves.textContent = l.moves?.join(" ") ?? "";

      li.append(score, moves);
      analyzeLines.appendChild(li);
    }
    analyzeLines.hidden = lines.length === 0;

    const parts = [`深さ ${p.depth}`];
    if (p.nodes > 0) {
      parts.push(`${p.nodes.toLocaleString()} ノード`);
    }
    parts.push(`${(p.elapsedMs / 1000).toFixed(1)} 秒`);
    if (analyzeEngine) {
      parts.push(analyzeEngine);
    }
    analyzeMeta.textContent = parts.join(" / ");
  };

  const startAnalyze = async () => {
    analyzeStatus.hidden = true;
    analyzeStatus.textContent = "";
    analyzeLines.replaceChildren();
    analyzeLines.hidden = true;
    analyzeMeta.textContent = "考えています…";
    try {
      const st = await AnalyzeService.Start(Number(analyzeSeconds.value) || 0);
      analyzeSeq = st.seq;
      analyzedSfen = st.sfen;
      analyzeEngine = st.engine;
      analyzeRunning = true;
    } catch (err) {
      clearAnalyzeResult();
      analyzeStatus.textContent = `解析できません: ${String(err instanceof Error ? err.message : err)}`;
      analyzeStatus.hidden = false;
    } finally {
      syncAnalyzeButton();
    }
  };

  analyzeRun.addEventListener("click", () => {
    if (analyzeRunning) {
      // **打ち切っても、そこまでの評価値は残る**（設計原則3）。捨てる操作ではない。
      void AnalyzeService.Stop();
      return;
    }
    void startAnalyze();
  });

  // 局面が変わったら解析の可否と表示を追随させる。**結果は局面と紐づける。**
  const syncAnalyze = (sfen: string, loaded: boolean) => {
    analyzeRow.hidden = !loaded;
    analyzeReady = !!sfen;
    if (analyzedSfen && sfen !== analyzedSfen) {
      // 直したので、前の評価値は今の盤の値ではなくなった。
      clearAnalyzeResult();
      analyzeSeq = -1;
      analyzeRunning = false;
    }
    syncAnalyzeButton();
  };

  Events.On("analyze:info", (event: { data: AnalyzeProgress }) => {
    if (event.data.seq !== analyzeSeq) {
      return; // 打ち切った解析の遅れてきた途中経過
    }
    showAnalyzeProgress(event.data.progress);
  });
  Events.On("analyze:done", (event: { data: AnalyzeProgress }) => {
    if (event.data.seq !== analyzeSeq) {
      return;
    }
    analyzeRunning = false;
    showAnalyzeProgress(event.data.progress);
    syncAnalyzeButton();
  });
  Events.On("analyze:failed", (event: { data: AnalyzeFailure }) => {
    if (event.data.seq !== analyzeSeq) {
      return;
    }
    analyzeRunning = false;
    clearAnalyzeResult();
    analyzeStatus.textContent = `解析できません: ${event.data.error}`;
    analyzeStatus.hidden = false;
    syncAnalyzeButton();
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
      syncAnalyze(st?.sfen ?? "", !!st?.loaded);
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
    onEditing: (on) => {
      editingNow = on;
      syncCaptureRef();
      syncTrain();
    },
    onError: (message) => {
      status.textContent = `訂正できませんでした: ${message}`;
      status.classList.add("is-warn");
    },
  });

  // 盤面タブの駒台。**訂正中の局面の値**で、先後の割り振りと**未決のぶん**まで出す
  // (デバッグタブ側は認識した時点の推定枚数のまま)。
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

  // デバッグタブ側。**認識した時点の駒台の推定枚数**(先後不明)。
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

  // デバッグタブ側の警告。**認識した時点のもの**で、訂正しても書き換えない。
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
    // 盤・SFEN・駒台・警告は訂正 UI 側(EditState)が描く。**認識結果をここで直接
    // 描かない**(訂正した内容が撮り直すまで残る、という食い違いを作らないため)。
    // 認識できていれば読み込んで訂正を始められる状態にし、駄目なら空に戻す。
    if (result.sfen) {
      void editor.load(result.sfen);
    } else {
      editor.clear();
      showBoard("", true);
      sfenOut.textContent = "-";
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
    // 訂正中に盤の左へ出す参照画像。**同じ 1 枚を 2 か所に描くだけ**にすること
    // (別々に更新すると、どちらが今の画像か分からなくなる)。
    hasShot = !!result.thumbnail;
    if (hasShot) {
      captureRefImg.src = result.thumbnail;
      captureRefImg.title = result.path;
    }
    syncCaptureRef();
    // 前の 1 枚の送信結果を残さない（別の画像の話になるため）。
    trainSendStatus.textContent = "";
    trainSendStatus.classList.remove("is-error");
    // suteme へ送るときの盤面矩形。**保存した PNG の座標系に直して覚える**
    // （認識はメモリ上の画像の座標系で答えるが、PNG は原点 (0,0) に正規化される。
    //  今は常に一致するはずだが、ずれると学習サンプルの切り出しが黙って 1 マスずれる）。
    const dbg = result.debug;
    lastRegion = dbg
      ? {
          x1: dbg.region.Min.X - dbg.image_bounds.Min.X,
          y1: dbg.region.Min.Y - dbg.image_bounds.Min.Y,
          x2: dbg.region.Max.X - dbg.image_bounds.Min.X,
          y2: dbg.region.Max.Y - dbg.image_bounds.Min.Y,
        }
      : null;
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

  // 枠は閉じても隠れるだけなので、ここから出し直せる。
  showFrame.addEventListener("click", () => {
    void CaptureService.ShowFrame();
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
  const trainCheck = root.querySelector<HTMLButtonElement>("#train-check")!;
  const trainCheckStatus = root.querySelector<HTMLParagraphElement>("#train-check-status")!;

  // 解析エンジン。**パスが空なら同梱**（「外部を使う」のトグルは持たない）。
  const enginePath = root.querySelector<HTMLInputElement>("#engine-path")!;
  const engineBrowse = root.querySelector<HTMLButtonElement>("#engine-browse")!;
  const engineClear = root.querySelector<HTMLButtonElement>("#engine-clear")!;
  const engineCheck = root.querySelector<HTMLButtonElement>("#engine-check")!;
  const engineStatus = root.querySelector<HTMLParagraphElement>("#engine-status")!;

  const showSettings = (s: {
    fitOnStartup: boolean;
    path: string;
    training: { enabled: boolean; host: string; port: number; token: string; target: string };
    engine: { path: string; builtin: boolean; optionCount: number };
  }) => {
    fitOnStartup.checked = s.fitOnStartup;
    settingsPath.textContent = s.path || "(保存先を決められませんでした)";
    // 入力中は上書きしない（保存のたびに読み直すので、打っている途中で飛ぶ）。
    if (document.activeElement !== enginePath) {
      enginePath.value = s.engine.path;
    }
    engineClear.disabled = s.engine.builtin;
    // **同梱かどうかの判定は Go 側の値を使う**（フロントで path === "" を書かない）。
    const opts = s.engine.optionCount > 0 ? `（setoption ${s.engine.optionCount} 件）` : "";
    engineStatus.classList.remove("is-error");
    engineStatus.textContent = s.engine.builtin
      ? "同梱のエンジンを使います。"
      : `外部のエンジンを使います${opts}。`;
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

  // ---- 解析エンジンの指定 --------------------------------------------------
  //
  // **保存と接続の確認は別の操作。** まだ置いていないパスを先に書いておく、という
  // 順序が普通にあるので、保存時に起動はしない（存在の確認だけ Go 側でする）。
  const saveEnginePath = async (path: string) => {
    engineStatus.classList.remove("is-error");
    try {
      showSettings(await SettingsService.SetEnginePath(path));
    } catch (err) {
      engineStatus.textContent = String(err instanceof Error ? err.message : err);
      engineStatus.classList.add("is-error");
    }
  };

  enginePath.addEventListener("change", () => {
    void saveEnginePath(enginePath.value);
  });

  engineBrowse.addEventListener("click", () => {
    void (async () => {
      engineBrowse.disabled = true;
      engineStatus.classList.remove("is-error");
      try {
        // 取り消したときは Go 側が何も変えずに今の設定を返す。
        showSettings(await SettingsService.BrowseEngine());
      } catch (err) {
        engineStatus.textContent = String(err instanceof Error ? err.message : err);
        engineStatus.classList.add("is-error");
      } finally {
        engineBrowse.disabled = false;
      }
    })();
  });

  engineClear.addEventListener("click", () => {
    void saveEnginePath("");
  });

  // 実際に起動して USI で応答するか確かめる。**繋いだ接続はそのまま解析に使う**
  // ので、確認したあとの 1 回目が速い。
  engineCheck.addEventListener("click", () => {
    void (async () => {
      engineCheck.disabled = true;
      engineStatus.classList.remove("is-error");
      engineStatus.textContent = "起動して確かめています…";
      try {
        const r = await AnalyzeService.CheckEngine();
        if (!r.ok) {
          engineStatus.textContent = `繋がりません: ${r.error}`;
          engineStatus.classList.add("is-error");
          return;
        }
        engineStatus.textContent = r.builtin
          ? `同梱のエンジンに繋がりました（${r.name}）。`
          : `繋がりました: ${r.name}`;
      } catch (err) {
        engineStatus.textContent = `確認できませんでした: ${String(err instanceof Error ? err.message : err)}`;
        engineStatus.classList.add("is-error");
      } finally {
        engineCheck.disabled = false;
      }
    })();
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
  Events.On("hotkey:register-failed", (event: { data: { hotkey: string; error: string } }) => {
    status.textContent = `グローバルホットキー(${event.data.hotkey})の登録に失敗しました。枠のツールバーの「撮る」は使えます。`;
    status.classList.add("is-error");
    markDebug("error");
  });
}
