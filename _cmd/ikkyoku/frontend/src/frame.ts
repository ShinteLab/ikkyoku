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
import { Events } from "@wailsio/runtime";
import { FiCamera } from "react-icons/fi";
import { CaptureService } from "../bindings/ikkyoku-app";
import { iconMarkup } from "./icon";

// capture:done のうち、枠が使う部分だけ。認識結果はメイン画面の担当なので見ない
// (CaptureResult の全体は mainscreen.ts に定義がある)。
interface CaptureDone {
  thumbnail: string; // data:image/png;base64,... の等倍サムネイル
}

export function mountFrame(root: HTMLElement): void {
  root.innerHTML = `
    <div class="frame-toolbar">
      <span class="frame-title">ikkyoku</span>
      <div class="frame-actions">
        <button id="frame-capture" class="frame-btn is-primary is-icon" type="button"
                aria-label="撮る" title="撮る(Alt+S)">${iconMarkup(FiCamera)}</button>
        <button id="frame-hide" class="frame-btn" type="button"
                title="枠を隠す(領域は保持され、Alt+S でそのまま撮れます)">✕</button>
      </div>
    </div>
    <div class="capture-guide">
      <img id="frame-flyout" class="capture-flyout" alt="">
    </div>
  `;

  const title = root.querySelector<HTMLSpanElement>(".frame-title")!;
  const captureBtn = root.querySelector<HTMLButtonElement>("#frame-capture")!;
  const hideBtn = root.querySelector<HTMLButtonElement>("#frame-hide")!;
  const guide = root.querySelector<HTMLDivElement>(".capture-guide")!;
  const toolbar = root.querySelector<HTMLDivElement>(".frame-toolbar")!;
  const flyout = root.querySelector<HTMLImageElement>("#frame-flyout")!;
  const baseTitle = title.textContent ?? "";

  // 自前の✕は WindowClosing を通らない(wails3 skill tray-hotkey.md 3)ので、
  // ランタイムの Window.Hide() ではなく Go 側の HideFrame() を呼ぶ。閉じるのではなく
  // 隠すだけで、ウィンドウが生きている限りキャプチャ領域の定義も生きる(隠したままでも
  // Alt+S で同じ領域が撮れる)。「隠した結果ウィンドウが 1 枚も見えなくなるならメイン画面を
  // 出す」という判断を Go 側に一本化したいので、Alt+F4 の経路と同じ入口を通す。
  hideBtn.addEventListener("click", () => {
    void CaptureService.HideFrame();
  });

  // 「撮った」ことを枠の側でも分かるようにする。
  //
  // 撮った結果はメイン画面に出るが、枠は中継の上に重ねて使うので、メイン画面が
  // 背面や別モニタにあると「押したのに何も起きていない」ように見える。誤って押した
  // ときに気づけないと、いつの間にか PNG が増えていく。ホットキー(Alt+S)は
  // なおさらで、フォーカスが別アプリにあるまま発火するため気づく手がかりが無い。
  //
  // 合図は 2 つ。flash がツールバーとガイド枠の**線**を光らせるもの(領域の外なので
  // 写り込みようがない)、playFlyout が撮れた画像そのものを領域の内側に重ねるもの。
  // 内側に描く後者だけが写り込みうる。詳細は playFlyout の ⚠️ を読むこと。
  let flashTimer = 0;
  let flyoutTimer = 0;
  let warnTimer = 0;
  const flash = (text: string) => {
    window.clearTimeout(flashTimer);
    window.clearTimeout(warnTimer);
    for (const el of [toolbar, guide]) {
      // 連打しても毎回光るよう、アニメーションを付け直す(リフローで巻き戻す)。
      // is-warn も落とす。同じ border-color を奪い合い、後勝ちで撮影の合図が
      // 出なくなるため(CSS の宣言順)。
      el.classList.remove("is-warn");
      el.classList.remove("is-flash");
      void (el as HTMLElement).offsetWidth;
      el.classList.add("is-flash");
    }
    title.textContent = text;
    title.classList.remove("is-error");
    flashTimer = window.setTimeout(() => {
      title.textContent = baseTitle;
    }, 900);
  };

  // 結果の表示はメイン画面の役目。ここではツールバーが狭いので簡潔に出す。
  const showError = (message: string) => {
    window.clearTimeout(flashTimer);
    title.textContent = `失敗: ${message}`;
    title.classList.add("is-error");
  };

  // 撮れた画像そのものを、撮った位置にそのまま重ねて出し、右下へ縮めながら消す。
  // ガイド枠の内側は撮った領域と 1:1 で一致するので、一瞬だけ盤が静止して見え、
  // 「今この範囲が撮れた」がそのまま伝わる。
  //
  // ⚠️ これは**キャプチャ領域の内側に描く唯一の要素**。画面の合成結果を撮っている以上、
  // アニメ中(flyoutMs)に次のキャプチャが走ると縮小画像が写り込む。撮る側を止められる
  // 経路(ツールバーのボタン)は再生中は無効にしてあるが、**ホットキーは止められない**
  // ので、連打すると 1 枚目のゴーストが写った PNG ができうる。エフェクトを長くしない
  // こと。長さを伸ばすほど写り込みの窓が広がる。
  const flyoutMs = 600;
  const playFlyout = (thumbnail: string) => {
    if (!thumbnail) {
      return; // PNG エンコードに失敗したとき(Go 側で空になる)
    }
    flyout.src = thumbnail;
    // 連続して撮ったときに毎回頭から再生させる(リフローで巻き戻す)。
    flyout.classList.remove("is-playing");
    void flyout.offsetWidth;
    flyout.classList.add("is-playing");
    captureBtn.disabled = true;
    window.clearTimeout(flyoutTimer);
    flyoutTimer = window.setTimeout(() => {
      flyout.classList.remove("is-playing");
      flyout.removeAttribute("src"); // 等倍 PNG の data URL を抱えたままにしない
      captureBtn.disabled = false;
    }, flyoutMs);
  };

  // 透明部分をクリックしたときに「後ろには届いていない」ことを知らせる。
  //
  // 中継の上に重ねて使うので、後ろの画面を触ったつもりでこの枠を叩くことがある。
  // .capture-guide の pointer-events:none は **DOM 内のヒットテストを外すだけ**で、
  // OS から見れば枠ウィンドウが普通にクリックを受け取っており、後ろへは一切渡らない。
  // 無反応だと「クリックが効かない画面」に見えてしまうので、枠が受け取ったことを返す。
  //
  // ⚠️ ここでもキャプチャ領域の内側には何も描かない(写り込むため)。合図はツールバーと
  // ガイド枠の線を警告色で明滅させるだけ。**何も動かさない** — ウィンドウを揺らすと
  // 枠の位置がそのままキャプチャ領域の定義なので撮る場所がずれるし、中の要素を揺らすと
  // はみ出したぶんで水平スクロールバーが出る(実際に試して却下した)。
  //
  // アニメーションは CSS 側で 0.22s×2 = 0.44s。文字だけ少し長く残す。
  const warnHoldMs = 1000;
  const warnClickBlocked = () => {
    window.clearTimeout(warnTimer);
    window.clearTimeout(flashTimer);
    for (const el of [toolbar, guide]) {
      el.classList.remove("is-warn");
      el.classList.remove("is-flash"); // 同上。border-color を奪い合わせない
      void (el as HTMLElement).offsetWidth; // 連打しても毎回頭から再生させる
      el.classList.add("is-warn");
    }
    title.textContent = "クリックは後ろに届きません";
    title.classList.remove("is-error");
    warnTimer = window.setTimeout(() => {
      for (const el of [toolbar, guide]) {
        el.classList.remove("is-warn");
      }
      title.textContent = baseTitle;
    }, warnHoldMs);
  };

  // クリックかどうかの判定。ウィンドウ端(リサイズ)とツールバーは対象外にする。
  // resizeEdgePx は Wails ランタイムがリサイズ判定に使う幅に合わせた値。
  // 厳密に一致していなくてよい(少し広めに見て合図を出さないだけ)。
  const resizeEdgePx = 6;
  const dragSlopPx = 3;
  let downX = 0;
  let downY = 0;
  root.addEventListener("mousedown", (e) => {
    downX = e.clientX;
    downY = e.clientY;
  });
  root.addEventListener("click", (e) => {
    if ((e.target as HTMLElement).closest(".frame-toolbar")) {
      return; // ツールバーの操作。合図は要らない
    }
    if (Math.abs(e.clientX - downX) > dragSlopPx || Math.abs(e.clientY - downY) > dragSlopPx) {
      return; // ドラッグ(リサイズ)の終わり。クリックではない
    }
    if (
      e.clientX < resizeEdgePx ||
      e.clientY < resizeEdgePx ||
      window.innerWidth - e.clientX < resizeEdgePx ||
      window.innerHeight - e.clientY < resizeEdgePx
    ) {
      return; // ウィンドウ端。リサイズを掴もうとした操作
    }
    warnClickBlocked();
  });

  // **エフェクトの起点はイベント 1 本にする。**「撮る」ボタンとホットキー(Alt+S)は
  // どちらも Go 側の CaptureService.Capture() に入り、成功すると capture:done が
  // 全ウィンドウへ飛ぶ(captureservice.go)。ボタン側の await でも光らせると、
  // クリック時だけ二重に光る(イベントの到着は await の解決と前後する)。
  Events.On("capture:done", (event: { data: CaptureDone }) => {
    flash("撮りました");
    playFlyout(event.data.thumbnail);
  });
  // ホットキー経由の失敗はこちらに来る(main.go の GlobalShortcut ハンドラ)。
  // ボタン経由の失敗は呼び出し元で捕まえるので、ここには来ない。
  Events.On("capture:failed", (event: { data: string }) => {
    showError(event.data);
  });

  captureBtn.addEventListener("click", () => {
    void (async () => {
      captureBtn.disabled = true;
      // 押した直後の反応は文字だけにする。**撮り終える前に光らせてはいけない**
      // (ツールバーは領域外なので写らないが、ガイド枠の線の色は撮影中に変わると
      // 境界の見え方が変わる。合図は撮り終えてから capture:done で出す)。
      window.clearTimeout(flashTimer);
      title.textContent = "撮影中…";
      try {
        await CaptureService.Capture();
      } catch (err) {
        showError(String(err));
      } finally {
        // 再生中なら押せるようにしない(解除は playFlyout のタイマーがやる)。
        // capture:done の到着が await の解決と前後するため、無条件に戻すと
        // アニメ中にボタンが生き返り、写り込みの窓が開く。
        captureBtn.disabled = flyout.classList.contains("is-playing");
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
