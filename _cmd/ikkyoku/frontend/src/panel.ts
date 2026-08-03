// 操作パネル。「撮る」ボタン・保存先パス・直近のサムネイルを表示する。
// ガイド枠(frame.ts)とは別ウィンドウなので、ここに置いた要素はキャプチャに写り込まない。
import { Events } from "@wailsio/runtime";
import { CaptureService } from "../bindings/ikkyoku-app";

export function mountPanel(root: HTMLElement): void {
  root.innerHTML = `
    <div class="panel">
      <div class="panel-row">
        <button id="capture-btn" class="capture-btn" type="button">撮る</button>
        <span class="hotkey-hint">Alt+S</span>
      </div>
      <p id="status" class="status" role="status" aria-live="polite">
        ガイド枠を盤面に合わせて撮影してください。
      </p>
      <img id="thumbnail" class="thumbnail" alt="直近のキャプチャ" hidden />
    </div>
  `;

  const button = root.querySelector<HTMLButtonElement>("#capture-btn")!;
  const status = root.querySelector<HTMLParagraphElement>("#status")!;
  const thumbnail = root.querySelector<HTMLImageElement>("#thumbnail")!;

  const showResult = (result: { path: string; width: number; height: number; thumbnail: string }) => {
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

  const capture = async () => {
    button.disabled = true;
    try {
      const result = await CaptureService.Capture();
      showResult(result);
    } catch (err) {
      showError(String(err));
    } finally {
      button.disabled = false;
    }
  };

  button.addEventListener("click", () => {
    void capture();
  });

  // ホットキー(Go側の GlobalShortcut)からのキャプチャは、このパネルが
  // フォーカスされていなくても発生する。結果は Wails イベントで受け取って
  // UI に反映する(パネルを開いていれば結果がすぐ分かる)。
  Events.On("capture:done", (event: { data: { path: string; width: number; height: number; thumbnail: string } }) => {
    showResult(event.data);
  });
  Events.On("capture:failed", (event: { data: string }) => {
    showError(event.data);
  });
  Events.On("hotkey:register-failed", (event: { data: { hotkey: string; error: string } }) => {
    status.textContent = `グローバルホットキー(${event.data.hotkey})の登録に失敗しました。「撮る」ボタンは使えます。`;
    status.classList.add("is-error");
  });
}
