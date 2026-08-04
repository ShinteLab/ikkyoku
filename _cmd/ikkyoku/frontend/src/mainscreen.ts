// メイン画面。アプリ本体。
//
// 撮った画像・保存先・(将来は)認識結果と設定を置く画面。起動時は非表示で、最初の
// キャプチャで現れる。**この画面を閉じるとアプリが終了する**(枠を閉じても終了しない)。
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
}

export function mountMainScreen(root: HTMLElement): void {
  root.innerHTML = `
    <div class="main-screen">
      <div class="main-toolbar">
        <span class="hint">撮るのは枠のツールバー、または <span class="hotkey-hint">Alt+S</span></span>
        <span class="spacer"></span>
        <button id="show-frame-btn" class="ghost-btn" type="button">枠を表示</button>
      </div>
      <p id="status" class="status" role="status" aria-live="polite">
        ガイド枠を盤面に合わせて撮影してください。
      </p>
      <div class="preview">
        <img id="thumbnail" class="thumbnail" alt="直近のキャプチャ" hidden />
      </div>
    </div>
  `;

  const showFrame = root.querySelector<HTMLButtonElement>("#show-frame-btn")!;
  const status = root.querySelector<HTMLParagraphElement>("#status")!;
  const thumbnail = root.querySelector<HTMLImageElement>("#thumbnail")!;

  const showResult = (result: CaptureResult) => {
    status.textContent = `保存しました: ${result.path} (${result.width}x${result.height})`;
    status.classList.remove("is-error");
    if (result.thumbnail) {
      thumbnail.src = result.thumbnail;
      thumbnail.hidden = false;
    }
  };

  const showError = (message: string) => {
    status.textContent = `キャプチャに失敗しました: ${message}`;
    status.classList.add("is-error");
  };

  // 枠は閉じても隠れるだけなので、ここから出し直せる。
  showFrame.addEventListener("click", () => {
    void CaptureService.ShowFrame();
  });

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
  });
}
