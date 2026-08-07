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
import { CaptureService, SettingsService } from "../bindings/ikkyoku-app";
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
        <div class="board-area">
          <!-- .board-stage は盤と同じ大きさの箱。訂正 UI の 9x9 グリッドを
               ここに重ねる(editor.ts)。**幅の上限は盤の固有サイズと揃えること**
               (--board-max。ずれるとマスの当たり判定が 1 マスずれる)。 -->
          <div id="board-stage" class="board-stage">
            <shogi-board id="board" hidden></shogi-board>
          </div>
          <p id="board-placeholder" class="board-placeholder">まだ撮っていません。</p>
        </div>
        <div id="editor" class="editor"></div>
        <div class="sfen-row">
          <span class="field-label">SFEN</span>
          <code id="sfen" class="sfen">-</code>
        </div>
        <!-- 駒台と警告は**デバッグにも盤面にも出す**。デバッグ側は「認識がどれくらい
             外したか」の指標として、こちら側は**局面を直すときに要る情報**として置く
             (駒台の枚数は駒数保存則の逆算そのもので、警告は「どこが怪しいか」の提示)。
             訂正 UI が乗るのはこの面なので、盤の近くに置く。 -->
        <!-- 駒台。**訂正中の局面の値**なので先後の割り振りが出る
             (デバッグタブ側は認識した時点の推定枚数で「先後不明」のまま)。 -->
        <div id="board-hand-row" class="hand-row" hidden>
          <span class="field-label">駒台</span>
          <span id="board-hand" class="hand"></span>
        </div>
        <ul id="board-warnings" class="warnings is-compact" hidden></ul>
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
  const editor = mountEditor({
    stage: boardStage,
    panel: root.querySelector<HTMLElement>("#editor")!,
    onState: (st) => {
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

  // 設定タブ。今は「起動時に盤面を探す」だけ。
  //
  // **切り替えたその場で保存する**(適用ボタンを置かない)。項目が 1 つで、
  // 効くのは次の起動なので、押し忘れて反映されないほうが分かりにくい。
  // 保存に失敗したらチェックを元に戻す(画面の状態と設定ファイルを食い違わせない)。
  const fitOnStartup = root.querySelector<HTMLInputElement>("#fit-on-startup")!;
  const settingsStatus = root.querySelector<HTMLParagraphElement>("#settings-status")!;
  const settingsPath = root.querySelector<HTMLElement>("#settings-path")!;

  const showSettings = (s: { fitOnStartup: boolean; path: string }) => {
    fitOnStartup.checked = s.fitOnStartup;
    settingsPath.textContent = s.path || "(保存先を決められませんでした)";
  };

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
