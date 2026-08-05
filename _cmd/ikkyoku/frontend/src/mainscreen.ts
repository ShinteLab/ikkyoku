// メイン画面。アプリ本体。
//
// 撮った画像から割り出した盤面・SFEN・警告を出す画面。起動時は非表示で、最初の
// キャプチャで現れる。**この画面を閉じるとアプリが終了する**(枠を閉じても終了しない)。
//
// 画面は 2 タブ。**盤面タブは盤と SFEN だけ**で、盤をできるだけ大きく見せる。
// 認識器の状態・駒台の推定・警告・保存先・撮った画像はデバッグタブに寄せてある
// (認識精度を追うための情報であって、盤を読むのに要るものではないため)。
//
// **撮る操作はここには置かない。** 撮るのは盤に枠を合わせている最中の操作なので、
// 枠のツールバーとホットキーで完結する。ここは撮れたものを見る側。
//
// 枠(frame.ts)とは別ウィンドウなので、ここに置いた要素はキャプチャに写り込まない
// ——ただし**枠に重なる位置に動かすと写り込む**(画面の合成結果を撮るため)。初回だけ
// Go 側が枠の外へ逃がす(captureservice.go の placeMainBesideFrame)。
import { Events } from "@wailsio/runtime";
import { CaptureService } from "../bindings/ikkyoku-app";

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
}

// これを下回ったら検出を疑う。盤が映っていない画面を撮ったときにここが落ちる。
const LOW_CONFIDENCE = 0.75;

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
        </div>
        <span class="spacer"></span>
        <span class="hint">撮るのは枠のツールバー、または <span class="hotkey-hint">Alt+S</span></span>
        <button id="show-frame-btn" class="ghost-btn" type="button">枠を表示</button>
      </div>

      <div id="panel-board" class="panel is-active" role="tabpanel" aria-labelledby="tab-board">
        <div class="board-area">
          <shogi-board id="board" hidden></shogi-board>
          <p id="board-placeholder" class="board-placeholder">まだ撮っていません。</p>
        </div>
        <div class="sfen-row">
          <span class="field-label">SFEN</span>
          <code id="sfen" class="sfen">-</code>
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
          <span class="field-label">撮った画像</span>
          <img id="thumbnail" class="thumbnail" alt="直近のキャプチャ" hidden />
        </div>
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
  const handRow = root.querySelector<HTMLDivElement>("#hand-row")!;
  const handOut = root.querySelector<HTMLElement>("#hand")!;
  const warnings = root.querySelector<HTMLUListElement>("#warnings")!;
  const status = root.querySelector<HTMLParagraphElement>("#status")!;
  const thumbnail = root.querySelector<HTMLImageElement>("#thumbnail")!;

  // タブ。盤面タブは「撮れた盤と SFEN」だけに絞り、認識器の状態・警告・撮った画像
  // といった突き合わせ用の情報はデバッグタブへ寄せてある。**盤を大きく見せるのが目的**
  // なので、盤面タブに項目を足すときは本当にそこに要るのかを毎回考えること。
  //
  // 隠すのは表示だけで、両方のパネルの中身は常に更新する(タブを切り替えた瞬間に
  // 古い内容が出ることが無いように)。
  const tabs: { tab: HTMLButtonElement; panel: HTMLElement }[] = [
    { tab: root.querySelector<HTMLButtonElement>("#tab-board")!, panel: root.querySelector<HTMLElement>("#panel-board")! },
    { tab: root.querySelector<HTMLButtonElement>("#tab-debug")!, panel: root.querySelector<HTMLElement>("#panel-debug")! },
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

  // sfen が空なら盤を隠して理由を出す。撮る前と「撮ったが認識できなかった」は別物なので
  // 文言を分ける(認識失敗はキャプチャの失敗ではない。設計原則3)。
  const showBoard = (sfen: string, captured: boolean) => {
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

  const showHand = (hand: Record<string, number>) => {
    const parts = HAND_ORDER.filter((k) => (hand?.[k] ?? 0) > 0).map(
      (k) => `${HAND_LABEL[k]}${hand[k]}`,
    );
    if (parts.length === 0) {
      handRow.hidden = true;
      return;
    }
    handOut.textContent = parts.join(" ");
    handRow.hidden = false;
  };

  const showWarnings = (list: string[]) => {
    warnings.innerHTML = "";
    if (!list || list.length === 0) {
      warnings.hidden = true;
      return;
    }
    for (const w of list) {
      const li = document.createElement("li");
      li.textContent = w;
      warnings.appendChild(li);
    }
    warnings.hidden = false;
  };

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

    sfenOut.textContent = result.sfen || "-";
    showBoard(result.sfen, true);
    showConfidence(result.confidence, result.sfen);
    showHand(result.handTotal);
    showWarnings(result.warnings);

    if (result.thumbnail) {
      thumbnail.src = result.thumbnail;
      thumbnail.hidden = false;
    }

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
  Events.On("hotkey:register-failed", (event: { data: { hotkey: string; error: string } }) => {
    status.textContent = `グローバルホットキー(${event.data.hotkey})の登録に失敗しました。枠のツールバーの「撮る」は使えます。`;
    status.classList.add("is-error");
    markDebug("error");
  });
}
