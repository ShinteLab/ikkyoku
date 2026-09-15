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
import { FiCamera, FiChevronDown, FiCrop, FiRadio, FiX } from "react-icons/fi";
import { CaptureService } from "../bindings/ikkyoku";
import { iconMarkup } from "./icon";

// capture:shot のうち、枠が使う部分だけ。認識結果はメイン画面の担当なので見ない
// (CaptureShot / CaptureResult の全体は Go 側 captureservice.go にある)。
//
// ⚠️ **枠が見るのは capture:shot であって capture:done ではない**(2026-08-18)。
// 認識には数秒かかるので、done を待つと「撮影中…」のまま止まって見える。
interface CaptureShot {
  thumbnail: string; // data:image/png;base64,... の等倍サムネイル
  // 出どころ("screen" = 撮った / "file" = 画像ファイルを読み込んだ)。
  // ⚠️ **枠が合図を出すのは撮ったときだけ**(2026-09-12)。ファイルの読み込みは
  // **メイン画面の中の操作**で、枠は 1 度も関わっていない ——
  // そこでシャッターが光ると「撮れてしまった」と読める。
  source?: string;
}

export function mountFrame(root: HTMLElement): void {
  // .frame-title はアプリ名を出す場所ではなく、**状態メッセージの置き場**。
  // 枠は中継の上に重ねて使うので、常時「ikkyoku」と出ていても邪魔なだけ。
  // 普段は空で、撮影・失敗・警告のときだけ文字が出る(戻すときも空に戻す)。
  //
  // ボタンは ▼(メニュー)・□(盤に合わせる)・カメラ(撮る)・✕(隠す)の 4 つ。
  // よく押すものは左端に、押し間違えると枠が消えて驚く ✕ だけは右端に離す。
  // 間はメッセージ欄が埋める。
  //
  // メニュー(▼)はツールバーの下へ普通に垂らす。
  //
  // ⚠️ **開いているあいだはキャプチャ領域の内側に重なる**(ガイド枠の中に落ちるため)。
  // 開いたまま Alt+S(グローバルホットキーなので止められない)を押すとメニューごと
  // 写り込む。**開けっぱなしにしない**ための手当てが 3 つ:
  //   - 項目を押したら閉じる / Esc・メニュー外のクリックで閉じる
  //   - 撮れた(capture:done)ら閉じる。写り込んだ 1 枚があっても続けて撮る 2 枚目は綺麗になる
  //   - 中身は「開かなくても困らないもの」に限る。よく押すもの(撮る・盤に合わせる)は
  //     ツールバーに出したままにして、メニューに移さない
  //
  // ⚠️ メニューは `.frame-toolbar` の**外**(#app 直下)に置く。ツールバーは高さが
  // toolbarHeightPx で固定・overflow: hidden なので、中に入れると垂れた部分が切れる。
  //
  // ⚠️ ボタンは必ず `.frame-actions` の中に置くこと。ツールバーは全体が
  // `--wails-draggable: drag`(移動ハンドル)で、それを `no-drag` に戻しているのが
  // `.frame-actions` 側だけ。外に出すとドラッグ扱いになり、クリックが効かなくなる。
  root.innerHTML = `
    <div class="frame-toolbar">
      <div class="frame-actions">
        <button id="frame-menu" class="frame-btn is-icon" type="button"
                aria-label="メニュー" aria-expanded="false" aria-controls="frame-menu-items"
                title="メニュー">${iconMarkup(FiChevronDown)}</button>
        <button id="frame-fit" class="frame-btn is-icon" type="button"
                aria-label="盤に合わせる"
                title="盤に合わせる(画面に出ている盤を探して枠を合わせる)">${iconMarkup(FiCrop)}</button>
        <button id="frame-capture" class="frame-btn is-primary is-icon" type="button"
                aria-label="撮る" title="撮る">${iconMarkup(FiCamera)}</button>
        <!-- 中継を追う（2026-09-15）。⚠️ **枠に置いてあるのが要点** ——
             追跡中に見ているのは**中継**なので、状態も操作も**そこに無いと届かない**
             （実機で「枠側が録画しているか分からない」と出た）。 -->
        <button id="frame-follow" class="frame-btn is-icon" type="button"
                aria-pressed="false" aria-label="中継を追う"
                title="中継を追う（撮り続けて、進んだ手を本譜に足します）">${iconMarkup(FiRadio)}</button>
      </div>
      <!-- 追跡中の札。⚠️ **状態メッセージの欄とは別にしてある** —— あちらは撮影の
           一時的な文で、こちらは**追っているあいだずっと出ている**。混ぜると
           撮った瞬間に「追跡中」が消える。 -->
      <span id="frame-follow-state" class="frame-follow" hidden></span>
      <span class="frame-title"></span>
      <div class="frame-actions">
        <button id="frame-hide" class="frame-btn is-icon" type="button"
                aria-label="枠を隠す"
                title="枠を隠す(位置は覚えているので、出し直せば同じ領域に戻ります)">${iconMarkup(FiX)}</button>
      </div>
    </div>
    <div id="frame-menu-items" class="frame-menu" role="menu" hidden>
      <button id="frame-settings" class="frame-btn is-menu" type="button" role="menuitem"
              title="メイン画面の設定タブを開きます">設定</button>
      <button id="frame-repair" class="frame-btn is-menu" type="button" role="menuitem"
              title="メイン画面が真っ黒になって触れなくなったときに、隠して出し直します">メイン画面を描き直す</button>
      <div class="frame-menu-sep" role="separator"></div>
      <button id="frame-quit" class="frame-btn is-menu is-danger" type="button" role="menuitem"
              title="ikkyoku を終了します(枠とメイン画面の位置は保存されます)">終了</button>
    </div>
    <div class="capture-guide">
      <img id="frame-flyout" class="capture-flyout" alt="">
    </div>
  `;

  const title = root.querySelector<HTMLSpanElement>(".frame-title")!;
  const followBtn = root.querySelector<HTMLButtonElement>("#frame-follow")!;
  const followState = root.querySelector<HTMLSpanElement>("#frame-follow-state")!;

  // 追跡の状態はメイン画面が持っている（ループもあちら）。**枠は映すだけ。**
  //
  // ⚠️ **枠側で状態を覚えないこと** —— メイン画面のボタンからも止められるので、
  // 2 か所が別々に覚えると食い違う（枠の「枠を表示」と同じ話）。
  followBtn.addEventListener("click", () => void Events.Emit("follow:toggle", null));
  Events.On("follow:state", (e: { data: { on: boolean; text: string } }) => {
    const on = !!e.data?.on;
    followBtn.setAttribute("aria-pressed", on ? "true" : "false");
    followBtn.classList.toggle("is-active", on);
    followBtn.title = on ? "中継の追跡を止める" : "中継を追う（撮り続けて、進んだ手を本譜に足します）";
    followState.hidden = !on;
    followState.textContent = e.data?.text ?? "";
    // ⚠️ **1 周ごとにボタンを打ち直すこと**（2026-09-15）。**これが動いている証明。**
    // このイベントは**追跡が 1 周するたびに来る**ので、**止まれば光らない**。
    // ⚠️ **CSS の `infinite` で点滅させないこと** —— 時計で回る点滅は
    // **ループが死んでも光り続ける**ので証明にならない（枠の時刻表示を外したのと
    // 同じ理由）。⚠️ **札に色の丸を足さないこと** —— 凡例が増えるだけで、
    // **見るところはボタン 1 つ**のほうが読める。
    followBtn.classList.remove("is-beat");
    if (on) {
      // アニメーションを頭から流し直すには、一度外して**レイアウトを確定させる**。
      void followBtn.offsetWidth;
      followBtn.classList.add("is-beat");
    }
  });
  const menuBtn = root.querySelector<HTMLButtonElement>("#frame-menu")!;
  const menu = root.querySelector<HTMLDivElement>("#frame-menu-items")!;
  const settingsBtn = root.querySelector<HTMLButtonElement>("#frame-settings")!;
  const repairBtn = root.querySelector<HTMLButtonElement>("#frame-repair")!;
  const quitBtn = root.querySelector<HTMLButtonElement>("#frame-quit")!;
  const fitBtn = root.querySelector<HTMLButtonElement>("#frame-fit")!;
  const captureBtn = root.querySelector<HTMLButtonElement>("#frame-capture")!;
  const hideBtn = root.querySelector<HTMLButtonElement>("#frame-hide")!;
  const guide = root.querySelector<HTMLDivElement>(".capture-guide")!;
  const toolbar = root.querySelector<HTMLDivElement>(".frame-toolbar")!;
  const flyout = root.querySelector<HTMLImageElement>("#frame-flyout")!;
  // メッセージを消したあとの既定の表示 = 空。ここに文字を入れると常時表示に戻る。
  const baseTitle = "";

  // 自前の✕は WindowClosing を通らない(wails3 skill tray-hotkey.md 3)ので、
  // ランタイムの Window.Hide() ではなく Go 側の HideFrame() を呼ぶ。閉じるのではなく
  // 隠すだけで、ウィンドウが生きている限りキャプチャ領域の定義も生きる(出し直せば
  // 同じ領域に戻る)。⚠️ **隠しているあいだは撮れない**(Go 側の requireFrame)。
  // 「隠した結果ウィンドウが 1 枚も見えなくなるならメイン画面を出す」という判断を
  // Go 側に一本化したいので、Alt+F4 の経路と同じ入口を通す。
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

  // ▼ のメニュー。ツールバーの下に垂れる(冒頭の ⚠️ を読むこと)。
  //
  // 中身は「設定」と「終了」。どちらも**枠だけを出して使っているときに手が無かった**
  // ものを入口にしたもの:
  //
  //   - 設定 … メイン画面を出す手段が「撮る」か「枠を✕で隠す」しか無かった
  //     (撮りたくないのに撮る / 位置合わせに使う枠が消える、という副作用つき)
  //   - 終了 … 枠の✕は隠すだけなので、メイン画面を一度出して閉じるしかなかった
  //
  // どちらも毎回押すものではないうえ、終了は押すとアプリが消える。ツールバーに
  // 常時並べず、一段隠したここに置く。**セパレータで終了だけを分けている**のは、
  // 上の項目を押すつもりで下まで滑らせる事故を減らすため。
  const setMenuOpen = (open: boolean) => {
    menu.hidden = !open;
    menuBtn.setAttribute("aria-expanded", String(open));
  };
  menuBtn.addEventListener("click", () => {
    setMenuOpen(menu.hidden);
  });
  // **開きっぱなしにしない。** 開いているあいだはキャプチャ領域の内側に重なるので、
  // その状態で撮ると写り込む。用が済んだら畳む(Esc・メニュー外のクリック)。
  root.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      setMenuOpen(false);
    }
  });
  root.addEventListener("pointerdown", (e) => {
    if (!(e.target as HTMLElement).closest("#frame-menu, #frame-menu-items")) {
      setMenuOpen(false);
    }
  });

  // 設定。メイン画面を出して設定タブを開く(タブの指定は Go 側が main:tab で流す)。
  // 枠は隠さない。位置合わせの基準そのものなので、設定を見るために消す理由が無い。
  settingsBtn.addEventListener("click", () => {
    setMenuOpen(false);
    void CaptureService.ShowMain("settings");
  });

  // メイン画面を描き直す。**メイン画面が真っ黒になって何も触れなくなったときの復帰手段。**
  // Go 側で Hide → Show するだけ(CaptureService.RepairMain)。Wails はこの 2 つで
  // WebView2 の PutIsVisible(false)/(true) を呼ぶので、最小化の復帰で不可視のまま
  // 取り残されたコントローラが表示状態に戻る。
  //
  // ⚠️ **この入口が枠の側にあることに意味がある。** 黒くなるのはメイン画面なので、
  // メイン画面の中にボタンを置いても押せない。枠は別ウィンドウなので生きている。
  repairBtn.addEventListener("click", () => {
    setMenuOpen(false);
    void CaptureService.RepairMain();
  });

  // 終了。**メイン画面を閉じたときと同じ扱い**(Go 側で位置・サイズを保存してから
  // Quit する)。枠は隠すだけの✕と違い、こちらはプロセスごと終わる。
  quitBtn.addEventListener("click", () => {
    setMenuOpen(false);
    void CaptureService.Quit();
  });

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
    // 「盤に合わせる」も止める。あれも領域を 1 枚撮ってから探すので、再生中に
    // 走らせると縮小中のゴーストを盤だと思って枠を寄せてしまう。
    captureBtn.disabled = true;
    fitBtn.disabled = true;
    window.clearTimeout(flyoutTimer);
    flyoutTimer = window.setTimeout(() => {
      flyout.classList.remove("is-playing");
      flyout.removeAttribute("src"); // 等倍 PNG の data URL を抱えたままにしない
      captureBtn.disabled = false;
      fitBtn.disabled = false;
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
  const warn = (text: string) => {
    window.clearTimeout(warnTimer);
    window.clearTimeout(flashTimer);
    for (const el of [toolbar, guide]) {
      el.classList.remove("is-warn");
      el.classList.remove("is-flash"); // 同上。border-color を奪い合わせない
      void (el as HTMLElement).offsetWidth; // 連打しても毎回頭から再生させる
      el.classList.add("is-warn");
    }
    title.textContent = text;
    title.classList.remove("is-error");
    warnTimer = window.setTimeout(() => {
      for (const el of [toolbar, guide]) {
        el.classList.remove("is-warn");
      }
      title.textContent = baseTitle;
    }, warnHoldMs);
  };
  const warnClickBlocked = () => warn("クリックは後ろに届きません");

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
    if ((e.target as HTMLElement).closest(".frame-toolbar, .frame-menu")) {
      return; // ツールバー(と、そこから垂れたメニュー)の操作。合図は要らない
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
  // どちらも Go 側の CaptureService.Capture() に入り、撮れると capture:shot が
  // 全ウィンドウへ飛ぶ(captureservice.go)。ボタン側の await でも光らせると、
  // クリック時だけ二重に光る(イベントの到着は await の解決と前後する)。
  //
  // ⚠️ **capture:done を待たないこと**(2026-08-18 に分けた)。あちらは**認識まで
  // 終わってから**飛ぶので、待つと合図が数秒遅れ、そのあいだ「撮影中…」が出たままになる
  // ——「今この 1 枚が撮れた」を伝えるのが合図の役目なので、遅れると意味が無い。
  // 認識の進み具合はメイン画面の担当(枠は撮る道具であって、結果を出す面ではない)。
  Events.On("capture:shot", (event: { data: CaptureShot }) => {
    // 画像ファイルの読み込みは枠と無関係なので、合図もメニューも触らない。
    if (event.data.source === "file") {
      return;
    }
    // 開いたまま撮られていたら畳む。その 1 枚には写り込んでいるが、続けて撮る
    // 2 枚目には写らない(ホットキーは止められないので、これが唯一できる手当て)。
    setMenuOpen(false);
    flash("撮りました");
    playFlyout(event.data.thumbnail);
  });
  // ホットキー経由の失敗はこちらに来る(main.go の GlobalShortcut ハンドラ)。
  // ボタン経由の失敗は呼び出し元で捕まえるので、ここには来ない。
  Events.On("capture:failed", (event: { data: string }) => {
    showError(event.data);
  });

  // 起動時の自動フィット(設定「起動時に盤面を探す」)の結果。
  // **押していないのに枠が動く**操作なので、動いた/動かなかったを必ず出す。
  // ボタン経由のフィットはここを通らない(呼び出し元で結果を受け取る)。
  Events.On("fit:done", (event: { data: { fitted: boolean; message: string } }) => {
    if (event.data.fitted) {
      flash(event.data.message);
    } else {
      warn(event.data.message);
    }
  });

  // 画面に出ている盤を探して、枠をそこへ合わせる。
  //
  // **Go 側は枠を一瞬隠して画面全体を撮ってから探す**(CaptureService.FitFrame)ので、
  // 押してから結果が出るまでに一呼吸あり、その間に枠が消えて戻る。撮る操作と同じく、
  // 押した直後は文字だけを出してガイド枠の線には触らない(探すための 1 枚に
  // 線の色の変化が乗らないように)。
  //
  // 盤が見つからなかったのは失敗ではない(画面に盤が出ていないだけ)。枠は動かず、
  // 理由だけが出る。エラー表示にせず警告の明滅で返すのはそのため。
  fitBtn.addEventListener("click", () => {
    void (async () => {
      fitBtn.disabled = true;
      window.clearTimeout(flashTimer);
      title.textContent = "盤を探しています…";
      title.classList.remove("is-error");
      try {
        const result = await CaptureService.FitFrame();
        if (result.fitted) {
          flash(result.message);
        } else {
          warn(result.message);
        }
      } catch (err) {
        showError(String(err));
      } finally {
        fitBtn.disabled = flyout.classList.contains("is-playing");
      }
    })();
  });

  captureBtn.addEventListener("click", () => {
    void (async () => {
      captureBtn.disabled = true;
      // 押した直後の反応は文字だけにする。**撮り終える前に光らせてはいけない**
      // (ツールバーは領域外なので写らないが、ガイド枠の線の色は撮影中に変わると
      // 境界の見え方が変わる。合図は撮り終えてから capture:shot で出す)。
      //
      // ⚠️ この文字が出ているのは**撮り終えるまで**(認識のあいだではない)。
      // capture:shot が「撮りました」で上書きするので、実際にはほぼ一瞬しか見えない。
      window.clearTimeout(flashTimer);
      title.textContent = "撮影中…";
      try {
        await CaptureService.Capture();
      } catch (err) {
        showError(String(err));
      } finally {
        // 再生中なら押せるようにしない(解除は playFlyout のタイマーがやる)。
        // capture:shot の到着が await の解決と前後するため、無条件に戻すと
        // アニメ中にボタンが生き返り、写り込みの窓が開く。
        // ⚠️ **await が解けるのは認識まで終わってから**なので、普通はここに来る頃には
        // エフェクトは終わっている(押せる状態に戻すのは playFlyout のタイマーの仕事)。
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
