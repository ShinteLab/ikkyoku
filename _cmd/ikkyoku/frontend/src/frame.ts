// 盤面に重ねる透過ガイド枠。
//
// Frameless ウィンドウなので OS のタイトルバーが無い。代わりに上部へ自前のツールバーを
// 置き、そこにドラッグ移動と「撮る」を集約している。ツールバーはキャプチャ領域の外(上)に
// あるので写り込まない(領域の算出は Go 側 captureservice.go の captureRegion)。
//
// ⚠️ ドラッグ移動もリサイズも、frontend/src/main.ts の素の `import "@wailsio/runtime"` が
// 前提。あれが無いと、ここで --wails-draggable を指定しても一切効かない。
//
// 寸法(枠の太さ・ツールバーの高さ)はここでは決めない。Go 側が唯一のソースで、
// CaptureService.Layout() から受け取って CSS 変数に流し込む。以前はフロントにも同じ
// 定数を置いていたが、ずれると枠が写り込むという直接的な不具合になるため一本化した。
import { CaptureService } from "../bindings/ikkyoku-app";

export function mountFrame(root: HTMLElement): void {
  root.innerHTML = `
    <div class="frame-toolbar">
      <span class="frame-title">ikkyoku</span>
      <div class="frame-actions">
        <button id="frame-capture" class="frame-btn is-primary" type="button">撮る</button>
        <button id="frame-hide" class="frame-btn" type="button"
                title="枠を隠す(領域は保持され、Alt+S でそのまま撮れます)">✕</button>
      </div>
    </div>
    <div class="capture-guide"></div>
  `;

  const title = root.querySelector<HTMLSpanElement>(".frame-title")!;
  const captureBtn = root.querySelector<HTMLButtonElement>("#frame-capture")!;
  const hideBtn = root.querySelector<HTMLButtonElement>("#frame-hide")!;

  // 自前の✕は WindowClosing を通らない(wails3 skill tray-hotkey.md 3)ので、
  // ランタイムの Window.Hide() ではなく Go 側の HideFrame() を呼ぶ。閉じるのではなく
  // 隠すだけで、ウィンドウが生きている限りキャプチャ領域の定義も生きる(隠したままでも
  // Alt+S で同じ領域が撮れる)。「隠した結果ウィンドウが 1 枚も見えなくなるならメイン画面を
  // 出す」という判断を Go 側に一本化したいので、Alt+F4 の経路と同じ入口を通す。
  hideBtn.addEventListener("click", () => {
    void CaptureService.HideFrame();
  });

  captureBtn.addEventListener("click", () => {
    void (async () => {
      captureBtn.disabled = true;
      try {
        await CaptureService.Capture();
      } catch (err) {
        // 結果の表示はメイン画面の役目。ここではツールバーが狭いので簡潔に出す。
        title.textContent = `失敗: ${String(err)}`;
        title.classList.add("is-error");
      } finally {
        captureBtn.disabled = false;
      }
    })();
  });

  // 寸法を Go から受け取ってから枠を描く。ここが失敗したまま既定値で描くと、
  // 「見えている枠」と「実際に撮れる領域」がずれたまま気づけないため、
  // 黙って続けずツールバーにエラーを出す。
  void (async () => {
    try {
      const layout = await CaptureService.Layout();
      const style = document.documentElement.style;
      style.setProperty("--guide-border", `${layout.borderPx}px`);
      style.setProperty("--toolbar-height", `${layout.toolbarPx}px`);
    } catch (err) {
      // 寸法が無いとツールバーの高さも 0 のままでエラーが読めないため、
      // CSS 側で高さを与えるクラスを付ける(style.css の is-layout-error)。
      document.documentElement.classList.add("is-layout-error");
      title.textContent = `枠の寸法を取得できません: ${String(err)}`;
      title.classList.add("is-error");
    }
  })();
}
