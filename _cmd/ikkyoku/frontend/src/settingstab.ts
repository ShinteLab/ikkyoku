// 設定タブ（2026-11-13 に `mainscreen.ts` から切り出した）。
//
// **関わりでまとめてある**(2026-09-12。撮る / 解析 / 見た目 / 棋譜データベース /
// 盤面認識(suteme))。⚠️ **足した順に積まないこと。**
// ⚠️ **畳むのは区切りの単位で、中でもう一度畳まない**(解析エンジンは
// 「解析」の中の 1 項目)。⚠️ **見出し(.setting-section)は 2 つ以上を
// 畳まずに並べるときだけ** —— 今は「撮る」だけ。
// ⚠️ **並びの基準は「どれだけの人が触るか」** —— よく触るものが上、
// **既定のままで動くものは下**。⚠️ **盤面認識をベース機能だからと上へ
// 戻さないこと**(認識器の置き場所や学習データへの登録は相当な上級者の操作)。
// ⚠️ **認識器の読み込み元と「訂正盤面を suteme に登録する」を離さないこと**
// (相手が同じ suteme)。項目を足すときはこの 5 つのどれかに入れる。
// 詳しくは `_docs/ui/screens.md`
//
// ⚠️ **受け渡しは `SettingsTabOptions`（もらうもの）と `SettingsTabHandle`（返すもの）だけ**
// （`sidepane.ts` と同じ型）。**盤にも手順にも触れない** —— 設定が効く先（盤の周り・側の列・
// 評価値グラフ・訂正データの送信）へは `onSettings` で配り、配る中身は `mainscreen.ts` が持つ。
// ⚠️ **設定の写しを持たない**（局面と同じ理由）。書き換えはどれも `AppSettings` を返すので、
// 返ってきたもので描き直す。
import { Clipboard, Events } from "@wailsio/runtime";
import { FiChevronDown, FiChevronUp } from "react-icons/fi";
// ⚠️ **CaptureService だけ出所が違う**（`mainscreen.ts` の import の注記）。
import { CaptureService } from "../bindings/ikkyoku";
import {
  AnalyzeService,
  FontService,
  KifuService,
  SettingsService,
  TrainingService,
} from "../bindings/github.com/ShinteLab/ikkyoku/app";
import type { RecognizerStatus } from "../bindings/ikkyoku/models";
import type {
  AppSettings,
  EngineSettings,
  FontChoice,
  FontState,
  KifuDBStatus,
} from "../bindings/github.com/ShinteLab/ikkyoku/app/models";
import { iconMarkup } from "./icon";
import { attachHints } from "./hint";
import { applyTheme } from "./theme";

export interface SettingsTabOptions {
  // pieceBoards は王/玉・左馬を当てる盤。**2 つの盤の両方**（訂正タブと解析タブ）。
  // ⚠️ **片方だけだと、採った瞬間に字が変わって見える。**
  pieceBoards: Element[];
  // onSettings は設定を描いたあとに呼ぶ（設定タブの外へ配る）。
  // ⚠️ **起動直後の形もここで決まる**（評価値グラフの置き場所・縦軸・盤の上の 1 行など）。
  onSettings(settings: AppSettings): void;
  // onRecognizer は認識器の読み込み元を変えて読み直したとき（訂正タブの「認識器: …」へ）。
  // ⚠️ **「今どこから読んでいるか」もそこから `showSutemeState` で戻ってくる**（更新の経路を 1 本にする）。
  onRecognizer(status: RecognizerStatus): void;
  // onKifuDBChanged は棚を開き直したとき（棋譜タブの一覧を取り直す）。
  // ⚠️ **設定タブから棋譜タブの DOM を触らないこと** —— 一覧を持っているのは
  // あちらなので、繋ぐのはこの 1 本にする。
  onKifuDBChanged(): void;
}

export interface SettingsTabHandle {
  // showSettings は設定を描き直す（側の列で書き換えたとき。`onSettings` まで通る）。
  showSettings(settings: AppSettings): void;
  // showSutemeState は認識器を今どこから読んでいるかを映す（**Go の RecognizerStatus を写すだけ**）。
  showSutemeState(status: RecognizerStatus): void;
}

// registerFontFace は data URL のフォントをドキュメントに登録する（`<style>` 1 枚）。
//
// **同じ id を渡せば中身ごと差し替わる**（前の `@font-face` は消える）ので、
// 家族名を使い回さない限り取り違えは起きない。
function registerFontFace(id: string, family: string, dataUrl: string): void {
  let style = document.getElementById(id) as HTMLStyleElement | null;
  if (!style) {
    style = document.createElement("style");
    style.id = id;
    document.head.appendChild(style);
  }
  style.textContent =
    `@font-face { font-family: "${family}"; src: url("${dataUrl}") format("truetype"); }`;
}

// applyPieceFont は設定の「駒の字」で選んだフォントを画面に当てる
// （**端末に入っているフォントから焼いた駒の字**。Go 側の `FontService`）。
//
// ⚠️ **当て方は `--shogi-font` ただ 1 つ。** `<shogi-board>`（core/web）も、
// 駒台の駒も、掴んだ駒の絵も、候補手の重ね表示も、この変数を見ている
// （style.css）。**要素ごとに font-family を書かないこと** —— 1 か所でも
// 直し忘れると、そこだけ書体が食い違う。
//
// ⚠️ **同梱の ShogiSFEN は上書きしない**（`registerBoardFont` が登録したまま）。
// 同名の family を二重に登録すると**どちらが当たるかがブラウザ任せ**になり、
// 同梱に戻せなくなる。だから family は登録ごとに違う名前になっている
// （Go 側の `ikkyoku.PieceFontFamily`）。
function applyPieceFont(face: { family: string; dataUrl: string } | null): void {
  const id = "shinte-piece-font";
  if (!face) {
    // 同梱に戻す。**変数を消すだけ**で、CSS の既定値（"ShogiSFEN"）に落ちる。
    document.getElementById(id)?.remove();
    document.documentElement.style.removeProperty("--shogi-font");
    return;
  }
  registerFontFace(id, face.family, face.dataUrl);
  // ⚠️ **引用符ごと入れること**（値は CSS の font-family。core/web の README）。
  document.documentElement.style.setProperty("--shogi-font", `"${face.family}"`);
}

// applyPieceStyle は「王/玉」「馬/左馬」を画面に当てる。
//
// 中身は**駒フォントの stylistic set**（`ss01` = 王→玉 / `ss02` = 馬→左馬）。
// ⚠️ **`K` と `k` はフォント上で同じグリフ**なので、盤全体にまとめて当てると
// **先後を分けられない。** そのため当て方が 2 通りある:
//
//   盤（`<shogi-board>`）    … `gyoku` / `hidari-uma` 属性。core/web が駒 1 つずつに当てる
//   ikkyoku が自分で描く駒  … 先手用・後手用の 2 つを CSS 変数で配る（style.css）
//
// 後者に当たるのは**「足りない駒」の王**と**掴んだ駒の絵**（駒台に王も馬も
// 出てこないので、そこは実質効かない）。⚠️ **どちらか片方だけ直すと、
// 「盤は玉なのに掴むと王」という見ないと分からない食い違いが出る。**
//
// ⚠️ **組み立ては Go 側**（`pieceStyle`）。`font-feature-settings` は
// **個別の値が積み上がらない**（後から当てた宣言が丸ごと勝つ）ので玉と左馬を
// 1 つの値にまとめる必要があり、**その判断を 2 か所に持たない。**
function applyPieceStyle(
  style: {
    boardGyoku: string;
    boardGyokuOn: boolean;
    boardHidariUma: boolean;
    black: string;
    white: string;
    ink: string;
    board: string;
    line: string;
    stand: string;
  },
  boards: Element[],
): void {
  const root = document.documentElement.style;
  root.setProperty("--piece-features-black", style.black);
  root.setProperty("--piece-features-white", style.white);
  // 駒の字の色。⚠️ **濃さ込みの 1 つの値**（Go 側の `PieceInk` が合成する）。
  // 盤（core/web の `--shogi-piece-color`）と、ikkyoku が HTML で描く駒
  // （駒台のチップ・掴んだ駒の絵）の**両方が同じ値を見る** ——
  // 濃さを別に当てると、HTML 側は element の `opacity` になり
  // **駒の背景（木地）ごと透ける。**
  root.setProperty("--shogi-piece-color", style.ink);
  // 盤面・線・駒台の色（2026-10-02）。盤は core/web の差し替え口、駒台は style.css。
  // ⚠️ **既定の解決は Go 側**（`ikkyoku.ColorOr`）。いつも解決済みの値が来る。
  root.setProperty("--shogi-board-color", style.board);
  root.setProperty("--shogi-line-color", style.line);
  root.setProperty("--stand-color", style.stand);
  for (const b of boards) {
    // ⚠️ **属性そのものを外すこと。** 値を空にして残すと「値なし」＝
    // **先後とも玉**になる（`core/web/README.md` の表）。
    if (style.boardGyokuOn) {
      b.setAttribute("gyoku", style.boardGyoku);
    } else {
      b.removeAttribute("gyoku");
    }
    // ⚠️ **左馬は有無だけ**（値を書いても見られない。盤全体に効く）。
    if (style.boardHidariUma) {
      b.setAttribute("hidari-uma", "");
    } else {
      b.removeAttribute("hidari-uma");
    }
  }
}

export function mountSettingsTab(root: HTMLElement, opts: SettingsTabOptions): SettingsTabHandle {
  // ⚠️ **この中の HTML コメントにバッククォートを書かないこと**（`mainscreen.ts` の同じ注記。`check-css.mjs` が見ている）。
  root.innerHTML = `
    <h3 class="setting-section">撮る</h3>
    <label class="setting">
      <input id="fit-on-startup" type="checkbox" />
      <span class="setting-body">
        <span class="setting-title" data-hint="hint-fit">起動時に盤面を探す</span>
        <span id="hint-fit" class="setting-note is-hint">
          起動のたびに一度だけ画面から盤を探して、ガイド枠を合わせます。
          見つからなければ枠はそのままです。
        </span>
      </span>
    </label>

    <!-- 枠の内側で後ろの画面を操作する（2026-08-19）。

         枠は中継の「上」に重ねる最前面のウィンドウなので、合わせたあとは
         **その下にある中継のシークバーや再生ボタンが押せない**（押すには
         枠をどかすしかなく、どかすと位置合わせがやり直しになる）。

         ⚠️ **素通しになるのは「撮る範囲の内側」だけ。** ツールバーと縁は
         押せるまま残す —— 枠ごと素通しにすると、この設定を切るまで
         枠を動かすことも撮ることもできなくなる。 -->
    <label class="setting">
      <input id="click-through" type="checkbox" />
      <span class="setting-body">
        <span class="setting-title" data-hint="hint-clickthrough">枠の内側で後ろの画面を操作する</span>
        <span id="hint-clickthrough" class="setting-note is-hint">
          ガイド枠の内側（撮る範囲）のクリックとホイールを、後ろにある中継の画面へ
          そのまま通します。枠をどかさずにシークや再生ができます。
          ツールバーと枠の縁は今までどおり押せます（移動・リサイズ・撮影）。
          Windows のみ。
        </span>
      </span>
    </label>
    <label class="setting">
      <input id="follow-first" type="checkbox" />
      <span class="setting-body">
        <span class="setting-title" data-hint="hint-followfirst">中継を追うあいだは盤の読みを優先する</span>
        <span id="hint-followfirst" class="setting-note is-hint">
          追っているあいだだけ、外部エンジンの優先度を下げて、盤の読み取りが先に CPU を使えるようにします。
          エンジンは空いた CPU では今までどおり回りますが、解析が遅くなることがあります。
          追うのを止めると元に戻ります。同梱エンジンには効きません。Windows のみ。
        </span>
      </span>
    </label>
    <label class="setting">
      <input id="follow-debug" type="checkbox" />
      <span class="setting-body">
        <span class="setting-title" data-hint="hint-followdebug">中継の録画とログを残す（デバッグ）</span>
        <span id="hint-followdebug" class="setting-note is-hint">
          中継を追うとき、手を決めた画像と追従の細かいログを一時フォルダの ikkyoku に残します。
          追従がおかしいときに調べるためのものです。1 週間より古いものは消えます。
        </span>
      </span>
    </label>
    <div class="setting is-block">
      <span class="setting-body">
        <span class="setting-title" data-hint="hint-loglevel">ログの細かさ</span>
        <span id="hint-loglevel" class="setting-note is-hint">
          一時フォルダの ikkyoku に残すログの細かさです。既定は、開発版が INFO、配布版が NOTICE です。
          何か起きたときに細かくして調べ、済んだら既定に戻してください。
        </span>
      </span>
      <div class="setting-fields">
        <label class="field">
          <span class="field-label">レベル</span>
          <select id="log-level"></select>
        </label>
      </div>
    </div>

    <details id="fold-analyze" class="setting-group setting-fold">
      <summary class="setting-fold-head">
        <span class="setting-title">解析</span>
        <span id="fold-analyze-sum" class="setting-fold-sum"></span>
      </summary>
      <!-- 勝率バーの変換に使う定数（解析タブ。2026-08-12）。
           **評価値の尺度はエンジンによって違う**ので、ここで合わせられるように
           してある（⚠️ 自作エンジンの評価値の絶対値は当てにならない）。
           ⚠️ **既定値（1500）は Go 側が解決して返す。フロントに書かないこと。** -->
      <div class="setting-group">
        <span class="setting-title" data-hint="hint-winrate">勝率の表示</span>
        <span id="hint-winrate" class="setting-note is-hint">
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
        <div class="setting is-block">
          <span class="setting-body">
            <span class="setting-title" data-hint="hint-engine">解析エンジン</span>
            <span id="hint-engine" class="setting-note is-hint">
              USI を話すエンジンの実行ファイルを登録します（やねうら王・水匠など）。
              <strong>「解析に使う」を付けたエンジンが同時に走り、解析タブに結果が並びます。</strong>
              実行ファイルを<strong>空にするとその登録は同梱のエンジン</strong>になります。
              同じエンジンを <code>setoption</code> 違いで 2 つ登録して比べることもできます。
              <strong>「接続を確認」を押すと、そのエンジンの設定項目（option）を読み込んで
              各行の「エンジンの設定」から変えられるようになります。</strong>
              変えた値は次の解析から送られます。
            </span>
          </span>
        </div>
        <ul id="engine-list" class="engine-list"></ul>
        <div class="setting-fields">
          <button id="engine-add" class="ghost-btn" type="button"
                  title="実行ファイルを選んで登録します">エンジンを追加…</button>
          <button id="engine-add-builtin" class="ghost-btn" type="button"
                  title="同梱のエンジンを登録します">同梱エンジンを追加</button>
        </div>
        <p id="engine-status" class="status" role="status" aria-live="polite"></p>
      </div>
    </details>

    <!-- 見た目（2026-10-02）。**画面の配色・盤と駒台の色・駒の字**をまとめた区切り。
         以前は「駒の字」と「配色」が別の区切りだったが、どれも**好みで選ぶ見え方**
         なので 1 つにした。

         ⚠️ **中でもう一度畳まないこと**（区切りの約束）。中の 3 つは素の項目で、
         短いものが上（配色 → 盤と駒台）、**伸びる一覧を持つ駒の字が下**。
         ⚠️ **盤と駒台の色はテーマで切り替えない**（駒の字の色と同じ扱い）。 -->
    <details id="fold-look" class="setting-group setting-fold">
      <summary class="setting-fold-head">
        <span class="setting-title">見た目</span>
        <span class="setting-fold-sum"><span id="fold-theme-sum"></span> / <span
              id="fold-piecefont-sum"></span></span>
      </summary>

      <!-- 配色（2026-10-02）。**盤の外の色だけ**を切り替える。
           ⚠️ **選択肢は Go 側が返す**（themes）。フロントに表を書かないこと。 -->
      <div class="setting-group">
        <span class="setting-title">配色</span>
        <div class="setting-fields">
          <label class="field">
            <span class="field-label">テーマ</span>
            <select id="theme-select"
                    title="画面の配色。盤・駒台・駒の色は変わりません"></select>
          </label>
        </div>
      </div>

      <!-- 盤と駒台の色（2026-10-02）。**駒の字の色と同じ扱い**（好みで選ぶ）。
           ⚠️ **既定は Go 側が解決して返す**（ColorOr）。「既定に戻す」は空を送る。
           線の色は盤の枠・罫線・星と、解析タブの駒台の縁に効く。 -->
      <div class="setting-group">
        <span class="setting-title" data-hint="hint-boardcolor">盤と駒台</span>
        <span id="hint-boardcolor" class="setting-note is-hint">
          盤面・線・駒台の色です。線は盤の枠と罫線・星、解析タブの駒台の縁に使います。
          画面の配色（テーマ）を変えてもこの色は変わりません。
        </span>
        <div class="setting-fields">
          <label class="field">
            <span class="field-label">盤</span>
            <input id="board-color" type="color" title="盤面の色" />
          </label>
          <label class="field">
            <span class="field-label">線</span>
            <input id="line-color" type="color" title="盤の枠・罫線・星と、駒台の縁の色" />
          </label>
          <label class="field">
            <span class="field-label">駒台</span>
            <input id="stand-color" type="color" title="解析タブの駒台の色" />
          </label>
          <button id="board-color-reset" class="ghost-btn" type="button"
                  title="盤・線・駒台の色を既定に戻します">既定に戻す</button>
        </div>
      </div>

        <!-- 駒の字（2026-08-16）。**端末に入っているフォントから駒の字を焼いて使う。**

             同梱できる駒フォントは「派生物の作成と再配布を認める」ライセンスの
             ものに限られる（core/web/README.md。游明朝・どへた・桜鯰は実際に外している）。
             一方**自分の端末に入っているフォントを、自分の端末で表示に使うのは
             再配布ではない**、というのがこの機能の拠り所。

             ⚠️ **焼いた字を書き出す口を作らないこと**（Go 側にも無い）。
             書き出せると「その端末で表示する」を越えてしまい、元フォントの
             条項が効く側の話になる。 -->
      <div class="setting-group">
        <span class="setting-title" data-hint="hint-piecefont">駒の字</span>
        <span id="hint-piecefont" class="setting-note is-hint">
          盤に並ぶ駒の書体です。端末に入っているフォントから、駒に要る
          <code id="font-required"></code> の字だけを抜き出して使います。
          <strong>抜き出した字はこのアプリの表示に使うだけで、ファイルとしては
          保存も配布もされません。</strong>
          <strong>元フォントの利用条件はそのまま効きます</strong>ので、
          作った盤面を配ったり素材として使ったりするときは、そちらを確認してください。
        </span>
        <ul id="font-list" class="engine-list"></ul>

        <!-- 王/玉と馬/左馬（2026-08-16）。**どのフォントでも効く**
             （駒フォントは同じ生成器で焼いているので ss01/ss02 が必ず入っている）。

             ⚠️ **先後を分けられるのは玉だけ。** 玉は王将/玉将という**駒そのものの
             呼び分け**（上位者が王）なので片側だけがありうるが、左馬は**盤の
             見た目の選択**なので、使うと決めたら盤全体がそうなる。
             **左馬に先後の欄を足さないこと**（core/web/README.md）。 -->
        <div class="setting-fields">
          <label class="field">
            <span class="field-label">王 / 玉</span>
            <select id="font-gyoku"
                    title="王を玉で書くか。先手だけ・後手だけも選べます"></select>
          </label>
          <label class="setting is-inline">
            <input id="font-hidari-uma" type="checkbox" />
            <span class="setting-body">
              <span class="setting-title">馬を左馬にする</span>
            </span>
          </label>
        </div>

        <!-- 駒の字の色と濃さ（2026-08-16）。**字の強いフォント（太い明朝・毛筆）は
             少し薄いほうが盤に映える。** 端末のフォントを選べるようにした以上、
             書体ごとに濃さを合わせたくなる。

             ⚠️ **画面では別々の欄だが、当てるのは 1 つの値**（Go 側が rgba に
             合成する）。濃さを別に当てると、HTML で描いている駒台のチップは
             element の opacity になり**木地ごと透ける**。 -->
        <div class="setting-fields">
          <label class="field">
            <span class="field-label">字の色</span>
            <input id="font-ink-color" type="color"
                   title="駒の字の色（盤・駒台・掴んだ駒に効きます）" />
          </label>
          <label class="field">
            <span class="field-label">濃さ</span>
            <input id="font-ink-opacity" class="ink-range" type="range"
                   min="20" max="100" step="5"
                   title="駒の字の濃さ。字の強いフォントは少し薄いほうが盤に映えます" />
            <output id="font-ink-opacity-value" class="ink-value"></output>
          </label>
          <button id="font-ink-reset" class="ghost-btn" type="button"
                  title="字の色と濃さを既定に戻します">既定に戻す</button>
        </div>

        <div class="setting-fields">
          <button id="font-scan" class="ghost-btn" type="button"
                  title="端末に入っているフォントを探します（数秒かかります）">フォントを追加…</button>
        </div>
        <p id="font-status" class="status" role="status" aria-live="polite"></p>

        <!-- 端末のフォントの一覧。**押したときだけ探す**（実測で 190 ファイル・
             577MB を読んで 1 秒弱）。起動のたびに走らせる類の処理ではない。 -->
        <div id="font-picker" class="font-picker" hidden>
          <div class="setting-fields">
            <label class="field">
              <span class="field-label">絞り込み</span>
              <input id="font-filter" type="search" placeholder="名前・ファイル名"
                     spellcheck="false" autocomplete="off" />
            </label>
            <!-- ⚠️ **既定は切**（＝全部出す）。駒の字が無いフォントを消してしまうと、
                 **探しているのか対象外なのかが画面から分からない。** -->
            <label class="setting is-inline">
              <input id="font-only-usable" type="checkbox" />
              <span class="setting-body">
                <span class="setting-title">駒の字が揃うものだけ</span>
              </span>
            </label>
          </div>
          <!-- 選んだ行の見本。**実際に焼いてから当てる**ので、盤に出る字そのもの。 -->
          <p id="font-sample" class="font-sample" hidden></p>
          <ul id="font-choices" class="font-choices"></ul>
          <!-- 探した場所。**目当てのフォントが出てこないときに、どこを見たのかが
               分からないと打つ手が無い。** -->
          <p id="font-dirs" class="setting-path"></p>
        </div>
      </div>
    </details>

    <!-- 棋譜データベース（棚）。**実装は kicho のままで、ikkyoku は利用する側。**

         ⚠️ **棚は解析の前提条件ではない**（設計原則3）。開けなくても
         撮った 1 局面と貼った棋譜の解析は今までどおり動き、棋譜タブだけが
         理由を出して機能しない。**ここでエラーを赤く出しても、他の機能は
         壊れていないことが分かるように書くこと。** -->
    <details id="fold-kifudb" class="setting-group setting-fold">
      <summary class="setting-fold-head">
        <span class="setting-title" data-hint="hint-kifudb">棋譜データベース</span>
        <span id="fold-kifudb-sum" class="setting-fold-sum"></span>
      </summary>
      <span id="hint-kifudb" class="setting-note is-hint">
        棋譜タブに溜める棋譜を置くファイルです。変えるとその場で開き直します。
        <strong>既定のままで動きます</strong>ので、置き場所を変えたいときだけ
        触ってください。<strong>開けなくても撮影・訂正・解析はそのまま使えます</strong>
        （棋譜タブだけが使えなくなります）。
      </span>
      <div class="setting-fields">
        <label class="field is-wide">
          <span class="field-label">場所</span>
          <input id="kifudb-path" type="text" spellcheck="false"
                 placeholder="(空なら既定の場所)" />
        </label>
        <button id="kifudb-browse" type="button">参照…</button>
      </div>
      <p id="kifudb-note" class="setting-note"></p>
    </details>

    <details id="fold-recognize" class="setting-group setting-fold">
      <summary class="setting-fold-head">
        <span class="setting-title" data-hint="hint-recognize">盤面認識（suteme）</span>
        <span class="setting-fold-sum">認識の精度を自分で育てるとき</span>
      </summary>
      <p id="hint-recognize" class="setting-section-note is-hint">
        認識に使う学習データと、訂正した局面の戻し先。<strong>どちらも既定のままで
        動きます</strong>ので、普通に使うぶんには触らなくてかまいません。
        相手はどちらも suteme なので、片方だけ設定しても噛み合いません。
      </p>
      <!-- 認識器の読み込み元（2026-08-27。2026-10-04 に 3 段にした）。

           **認識器は 3 段**: 学習データ（自分で育てたもの）→ 配布モデル（ダウンロードして
           置いたもの）→ 焼き込み（exe に最初から入っているもの）。**基本は焼き込みで動き、
           置いたもの・育てたものがあればそちらを使う。** 選ぶのは「どの段から見始めるか」で、
           読めない段は飛ばして下の段で動く（そのときはツールバーの ⚠ に出る）。

           ⚠️ **「自動」は学習データから。** 焼き込みは固定したデータなので、
           ここが焼き込みへ倒れると**学習データを更新しても反映されない**という
           最も気づきにくい事故になる（Go 側 recognizerOrder）。
           ⚠️ **今どこから読んでいるかは Go が返す**（RecognizerStatus）。設定の値から
           ここで推し量らないこと（読めない段は飛ばされるので食い違う）。 -->
      <div class="setting-group">
        <div class="setting is-block">
          <span class="setting-body">
            <span class="setting-title" data-hint="hint-sutemesource">認識器の読み込み元</span>
            <span id="hint-sutemesource" class="setting-note is-hint">
              盤面認識に使うデータを、学習データ・配布モデル・焼き込みのどこから読むかです。
              読めないものは飛ばして次を使います。切り替えるとその場で読み直します。
            </span>
          </span>
        </div>
        <div class="setting-fields">
          <label class="field">
            <span class="field-label">読み込み元</span>
            <select id="suteme-source">
              <option value="auto">自動（学習データ → 配布モデル → 焼き込み）</option>
              <option value="dir">学習データから</option>
              <option value="model">配布モデルから</option>
              <option value="embed">焼き込みから</option>
            </select>
          </label>
          <label class="field is-wide">
            <span class="field-label">学習データ</span>
            <input id="suteme-data-dir" type="text" spellcheck="false"
                   placeholder="(空なら使わない)" />
          </label>
          <label class="field is-wide">
            <span class="field-label">配布モデル</span>
            <input id="suteme-model-dir" type="text" spellcheck="false"
                   placeholder="(空なら既定の場所)" />
          </label>
        </div>
        <p id="suteme-source-note" class="setting-note"></p>
      </div>

      <!-- 訂正結果を suteme の学習データに戻す設定。**自動送信のスイッチではない**
           (2026-08-07 の決定: 自動で送ると、人が直した 1 マス以外は推論結果のまま
           なので自分の出力を正解として食う)。ここで有効にすると、確定した局面ごとに
           「訂正データを送信」が出るだけ。 -->
      <div class="setting-group">
        <label class="setting">
          <input id="train-enabled" type="checkbox" />
          <span class="setting-body">
            <span class="setting-title" data-hint="hint-training">訂正盤面を suteme に登録する</span>
            <span id="hint-training" class="setting-note is-hint">
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
          <!-- 接続先は URL 1 つ(2026-11-08)。suteme を -base で載せたならパスまで書く。
               suteme の APIタブの「登録先」(…/api/register)をそのまま貼ってもよい
               (読み方は Go の training.ParseURL)。 -->
          <label class="field is-wide">
            <span class="field-label">URL</span>
            <input id="train-url" type="text" spellcheck="false" autocomplete="off" />
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
    </details>

    <p id="settings-status" class="status" role="status" aria-live="polite"></p>
    <!-- 設定ファイルの場所。⚠️ **設定の項目ではない**（触るものが何も無い）ので、
         区切りの外・一番下の右端に小さく置く。以前は他の項目と同じ左端に
         並んでいたため、**何かの設定に見えていた。**
         押すとフルパスがクリップボードに入る（撮った PNG のパスと同じ作法）。 -->
    <p class="setting-footer">
      <span id="app-version" class="app-version" hidden></span>
      <button id="settings-path" class="path-btn" type="button"
              title="押すとフルパスをクリップボードにコピーします">-</button>
    </p>
  `;

  // 項目の説明をホバーで出す（`hint.ts`）。⚠️ **innerHTML を組んだ直後に 1 回だけ**
  // （中身を描き直さないので付け直しは要らない）。
  attachHints(root);

  // 設定タブ。「起動時に盤面を探す」と「訂正盤面を suteme に登録する」。
  //
  // **変えたその場で保存する**(適用ボタンを置かない)。押し忘れて反映されないほうが
  // 分かりにくいため。保存に失敗したら画面の値を元に戻す(画面の状態と設定ファイルを
  // 食い違わせない)。テキスト欄は change(確定時)で拾うので、1 文字ごとには書かない。
  const fitOnStartup = root.querySelector<HTMLInputElement>("#fit-on-startup")!;
  const clickThrough = root.querySelector<HTMLInputElement>("#click-through")!;
  // 中継を追うあいだは盤の読みを優先する（2026-10-07）。**切り替えたその場で効く**（追っている最中なら）。
  const followFirst = root.querySelector<HTMLInputElement>("#follow-first")!;
  // 中継の録画とログを残す（デバッグ。2026-10-11）。追従のログは切り替えたその場から、録画は次に追い始めたときから。
  const followDebug = root.querySelector<HTMLInputElement>("#follow-debug")!;
  // ログの細かさ（2026-10-11）。選択肢は Go 側が返したものをそのまま並べる（**フロントに表を書かない**）。
  const logLevel = root.querySelector<HTMLSelectElement>("#log-level")!;
  // 認識器の読み込み元。**「焼き込みがあるビルドか」は Go が返す**
  // （`sutemeEmbedAvailable`）。⚠️ **フロントで判定できない**（バイナリの中身の話）。
  const sutemeSource = root.querySelector<HTMLSelectElement>("#suteme-source")!;
  const sutemeDataDir = root.querySelector<HTMLInputElement>("#suteme-data-dir")!;
  const sutemeModelDir = root.querySelector<HTMLInputElement>("#suteme-model-dir")!;
  const sutemeSourceNote = root.querySelector<HTMLParagraphElement>("#suteme-source-note")!;

  // 認識器を今どこから読んでいるか（2026-10-04）。**Go が返した RecognizerStatus を写すだけ**
  // —— 設定の値から推し量らないこと（読めない段は飛ばされるので食い違う）。
  // ⚠️ **状態の行なので is-hint にしない**（frontend/src/AGENTS.md の 14）。
  const recognizerModeLabel: Record<string, string> = {
    dir: "学習データ",
    model: "配布モデル",
    embed: "焼き込み",
  };
  const showSutemeState = (st: RecognizerStatus) => {
    // 配布モデルの欄が空なら、実際に探した既定の場所を薄く見せる（**組み立てるのは Go**）。
    sutemeModelDir.placeholder = st.modelDir ? `(空なら ${st.modelDir})` : "(空なら既定の場所)";
    const lines = st.ready
      ? [`今は${recognizerModeLabel[st.mode] ?? st.mode}から読んでいます（${st.source}）。`]
      : ["認識器がありません。撮った画像から盤面を読めません。"];
    lines.push(...(st.skipped ?? []));
    sutemeSourceNote.textContent = lines.join("\n");
    sutemeSourceNote.classList.toggle("is-warn", !st.ready || (st.skipped ?? []).length > 0);
  };
  const kifuDBPath = root.querySelector<HTMLInputElement>("#kifudb-path")!;
  const kifuDBBrowse = root.querySelector<HTMLButtonElement>("#kifudb-browse")!;
  const kifuDBNote = root.querySelector<HTMLParagraphElement>("#kifudb-note")!;
  const kifuDBFold = root.querySelector<HTMLDetailsElement>("#fold-kifudb")!;
  const kifuDBFoldSum = root.querySelector<HTMLElement>("#fold-kifudb-sum")!;
  const settingsStatus = root.querySelector<HTMLParagraphElement>("#settings-status")!;
  const themeSelect = root.querySelector<HTMLSelectElement>("#theme-select")!;
  const themeFoldSum = root.querySelector<HTMLElement>("#fold-theme-sum")!;
  const settingsPath = root.querySelector<HTMLButtonElement>("#settings-path")!;
  // アプリの版（2026-10-05）。設定ファイルの場所と同じく**設定の項目ではない**ので、隅に小さく出す。
  const appVersion = root.querySelector<HTMLSpanElement>("#app-version")!;
  // ⚠️ **クリップボードは Wails ランタイム**（`navigator.clipboard` は secure context
  // 前提で、カスタムスキーム配信のこの webview では当てにできない）。
  settingsPath.addEventListener("click", () => {
    const path = settingsPath.dataset.path ?? "";
    if (!path) {
      return;
    }
    void Clipboard.SetText(path).then(() => {
      settingsPath.classList.add("is-copied");
      window.setTimeout(() => settingsPath.classList.remove("is-copied"), 900);
    });
  });
  const trainEnabledInput = root.querySelector<HTMLInputElement>("#train-enabled")!;
  const trainURL = root.querySelector<HTMLInputElement>("#train-url")!;
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
  // 畳んでいるときの 1 行（見出しの右）。⚠️ **中身の代わりにはしない** ——
  // 開かなくても「今どうなっているか」が分かるだけの添え物。
  // ⚠️ **畳んでいるのは「解析」の区切りごと**（2026-09-12）。エンジンの一覧は
  // その中の 1 項目で、自分では畳まない（親が畳むので二重にしない）。
  const analyzeFold = root.querySelector<HTMLDetailsElement>("#fold-analyze")!;
  const analyzeFoldSum = root.querySelector<HTMLElement>("#fold-analyze-sum")!;

  // ---- 駒の字（2026-08-16）----
  //
  // **端末に入っているフォントから駒の 19 グリフだけを焼いて使う**（Go 側の
  // `FontService`）。同梱できるフォントがライセンスの都合で限られる一方、
  // 自分の端末のフォントを自分の端末で表示に使うのは再配布ではない、というのが
  // この機能の拠り所。⚠️ **書き出す口は作らないこと。**
  const fontRequired = root.querySelector<HTMLElement>("#font-required")!;
  const fontList = root.querySelector<HTMLUListElement>("#font-list")!;
  const fontScanBtn = root.querySelector<HTMLButtonElement>("#font-scan")!;
  const fontStatus = root.querySelector<HTMLParagraphElement>("#font-status")!;
  const fontPicker = root.querySelector<HTMLDivElement>("#font-picker")!;
  const fontFilter = root.querySelector<HTMLInputElement>("#font-filter")!;
  const fontOnlyUsable = root.querySelector<HTMLInputElement>("#font-only-usable")!;
  const fontSample = root.querySelector<HTMLParagraphElement>("#font-sample")!;
  const fontChoices = root.querySelector<HTMLUListElement>("#font-choices")!;
  const fontDirs = root.querySelector<HTMLParagraphElement>("#font-dirs")!;
  const fontGyoku = root.querySelector<HTMLSelectElement>("#font-gyoku")!;
  const fontHidariUma = root.querySelector<HTMLInputElement>("#font-hidari-uma")!;
  const fontInkColor = root.querySelector<HTMLInputElement>("#font-ink-color")!;
  const fontInkOpacity = root.querySelector<HTMLInputElement>("#font-ink-opacity")!;
  const fontInkOpacityValue = root.querySelector<HTMLOutputElement>("#font-ink-opacity-value")!;
  const fontInkReset = root.querySelector<HTMLButtonElement>("#font-ink-reset")!;
  const boardColor = root.querySelector<HTMLInputElement>("#board-color")!;
  const lineColor = root.querySelector<HTMLInputElement>("#line-color")!;
  const standColor = root.querySelector<HTMLInputElement>("#stand-color")!;
  const boardColorReset = root.querySelector<HTMLButtonElement>("#board-color-reset")!;
  const fontFold = root.querySelector<HTMLDetailsElement>("#fold-look")!;
  const fontFoldSum = root.querySelector<HTMLElement>("#fold-piecefont-sum")!;

  // 王/玉・左馬を当てる相手。**2 つの盤の両方**（訂正タブと解析タブ）。
  // ⚠️ **片方だけだと、採った瞬間に字が変わって見える。**
  const pieceBoards = opts.pieceBoards;

  // 探した結果。**探したときだけ埋まる**（1 秒近くかかるので、絞り込みのたびに
  // 探し直さない —— 手元に持っておいて絞るのはこちらの仕事）。
  let fontChoiceRows: FontChoice[] = [];
  // 見本に出す字（＝駒に要る字）。**Go 側が返したものをそのまま使う**
  // （`core/shogifont.Required`。フロントで並べ直さないこと）。
  let fontSampleText = "";

  const setFontStatus = (msg: string, kind: "" | "error" | "warn" = "") => {
    fontStatus.textContent = msg;
    fontStatus.classList.toggle("is-error", kind === "error");
    fontStatus.classList.toggle("is-warn", kind === "warn");
    // ⚠️ **知らせるときは畳んでいても開く。** 畳めるようにした以上、
    // 起動時に焼けなかった等の理由が**閉じた中に隠れてはいけない**。
    if (msg && kind) {
      fontFold.open = true;
    }
  };

  // 一覧の 1 行。**先頭は必ず同梱**（id は空文字）で、ラジオで 1 つだけ選ぶ。
  //
  // ⚠️ **「使う」を行の中のチェックにしないこと。** 盤は 1 つしかなく駒の字も
  // 同時に 1 つしか使えないので、**2 つに印が付いている状態を作れてはいけない**
  // （エンジンの `Enabled` とは性格が違う）。
  const fontRow = (
    opts: {
      id: string;
      name: string;
      custom?: boolean;
      file?: string;
      note?: string;
      ok: boolean;
      copyright?: string;
      license?: string;
      licenseUrl?: string;
    },
    selected: boolean,
  ): HTMLLIElement => {
    const li = document.createElement("li");
    li.className = "engine-row font-row";
    li.classList.toggle("is-selected", selected);

    const head = document.createElement("label");
    head.className = "font-row-head";
    const radio = document.createElement("input");
    radio.type = "radio";
    radio.name = "piece-font";
    radio.checked = selected;
    // ⚠️ **使えない登録でも選べること。** フォントを入れ直せばそのまま戻るので、
    // 選択ごと奪うと「入れ直したのに戻らない」に見える（理由は下の行に出る）。
    radio.addEventListener("change", () => {
      void useFont(opts.id);
    });
    head.appendChild(radio);

    if (opts.id === "") {
      const span = document.createElement("span");
      span.className = "font-row-name";
      span.textContent = opts.name;
      head.appendChild(span);
    } else {
      // ⚠️ **既定の名前を value に入れないこと**（エンジンの行と同じ）。
      // 入れると、元フォントの名前が変わっても追従しなくなる。
      const input = document.createElement("input");
      input.type = "text";
      input.className = "font-row-name-input";
      input.placeholder = opts.name;
      input.value = opts.custom ? opts.name : "";
      input.title = "この登録に付ける名前（空にすると元フォントの名前に戻ります）";
      input.addEventListener("change", () => {
        void renameFont(opts.id, input.value);
      });
      head.appendChild(input);
    }
    li.appendChild(head);

    const meta = document.createElement("div");
    meta.className = "font-row-meta";
    if (opts.file) {
      const file = document.createElement("span");
      file.className = "font-row-file";
      file.textContent = opts.file;
      meta.appendChild(file);
    }
    // 権利表記。**何に由来する字かを利用者が判断できるように出す。**
    // ⚠️ **空でも「制約が無い」ではない**ので、無いときに「自由に使えます」とは書かない。
    const rights = [opts.copyright, opts.license].filter(Boolean).join(" / ");
    if (rights) {
      const span = document.createElement("span");
      span.className = "font-row-rights";
      span.textContent = rights;
      span.title = rights + (opts.licenseUrl ? `\n${opts.licenseUrl}` : "");
      meta.appendChild(span);
    }
    if (meta.childElementCount > 0) {
      li.appendChild(meta);
    }

    if (opts.note) {
      const note = document.createElement("p");
      note.className = "status is-warn";
      note.textContent = opts.note;
      li.appendChild(note);
    }

    if (opts.id !== "") {
      const actions = document.createElement("div");
      actions.className = "font-row-actions";
      const del = document.createElement("button");
      del.type = "button";
      del.className = "ghost-btn";
      del.textContent = "削除";
      del.title = "この登録を消します（フォントそのものは消えません）";
      del.addEventListener("click", () => {
        void removeFont(opts.id);
      });
      actions.appendChild(del);
      li.appendChild(actions);
    }
    return li;
  };

  const showFontState = (st: FontState) => {
    fontRequired.textContent = st.required;
    fontSampleText = st.required;
    // ⚠️ **当てるのはここ 1 か所。** 盤も駒台も候補手の重ね表示も
    // `--shogi-font` を見ているので、要素ごとに書かない。
    applyPieceFont(st.face);
    // 王/玉と馬/左馬。**盤（属性）と自前の駒（CSS 変数）を一緒に当てる**
    // ——別々に当てると「盤は玉なのに掴むと王」になる。
    applyPieceStyle(st.style, pieceBoards);

    // 選択肢は Go 側が返したものをそのまま並べる（**フロントに表を書かない**）。
    if (fontGyoku.options.length !== (st.gyokuOptions ?? []).length) {
      fontGyoku.replaceChildren();
      for (const o of st.gyokuOptions ?? []) {
        const opt = document.createElement("option");
        opt.value = o.value;
        opt.textContent = o.label;
        fontGyoku.appendChild(opt);
      }
    }
    // ⚠️ **Go 側が倒した結果をそのまま入れること**（知らない値は「王のまま」に
    // 倒して返ってくるので、選び直されたことが画面に出る）。
    fontGyoku.value = st.gyoku;
    fontHidariUma.checked = st.hidariUma;
    // 字の色と濃さ。⚠️ **既定も下限も Go 側が解決して返す**ので、
    // フロントに数値を書かない（丸めた結果もそのまま返るので、画面に出る）。
    fontInkColor.value = st.pieceColor;
    fontInkOpacity.min = String(Math.round(st.minPieceOpacity * 100));
    fontInkOpacity.value = String(Math.round(st.pieceOpacity * 100));
    fontInkOpacityValue.textContent = `${Math.round(st.pieceOpacity * 100)}%`;
    // 盤・線・駒台の色。⚠️ **既定は Go 側が解決して返す**（フロントに書かない）。
    boardColor.value = st.boardColor;
    lineColor.value = st.lineColor;
    standColor.value = st.standColor;
    // ⚠️ **見本の色をここで当てないこと。** `.font-sample` も CSS で
    // `--shogi-piece-color` を見ているので、当てると同じ値を 2 経路で書くことになる
    // （実際に盤へ出る濃さのまま見える、という狙いは CSS 側で満たされている）。

    // 畳んでいるときは「今どの書体か」だけ見出しの右に出す。
    fontFoldSum.textContent =
      st.current === ""
        ? st.builtinName
        : (st.fonts ?? []).find((f) => f.id === st.current)?.name ?? st.builtinName;

    fontList.replaceChildren();
    fontList.appendChild(
      fontRow({ id: "", name: st.builtinName, ok: true }, st.current === ""),
    );
    for (const f of st.fonts ?? []) {
      fontList.appendChild(
        fontRow(
          {
            id: f.id, name: f.name, custom: f.custom, file: f.file,
            note: f.note, ok: f.ok,
            copyright: f.copyright, license: f.license, licenseUrl: f.licenseUrl,
          },
          st.current === f.id,
        ),
      );
    }
    // ⚠️ **焼けなくてもエラーにしない**（同梱の字で描けている。設計原則3）。
    if (st.note) {
      setFontStatus(st.note, "warn");
    } else {
      setFontStatus("");
    }
    // 一覧が開きっぱなしなら、登録済みの印を取り直す。
    if (!fontPicker.hidden) {
      const registered = new Set((st.fonts ?? []).map((f) => `${f.path.toLowerCase()}|${f.index}`));
      for (const c of fontChoiceRows) {
        c.registered = registered.has(`${c.path.toLowerCase()}|${c.index}`);
      }
      renderFontChoices();
    }
  };

  // 端末のフォントの一覧を描く。**足りないものも出して、選べない見た目にするだけ**
  // （消すと、探しているのか対象外なのかが画面から分からない）。
  const renderFontChoices = () => {
    const q = fontFilter.value.trim().toLowerCase();
    const onlyUsable = fontOnlyUsable.checked;
    const rows = fontChoiceRows.filter((c) => {
      if (onlyUsable && c.missing) {
        return false;
      }
      if (!q) {
        return true;
      }
      return (
        c.name.toLowerCase().includes(q) ||
        c.file.toLowerCase().includes(q) ||
        (c.family ?? "").toLowerCase().includes(q)
      );
    });

    fontChoices.replaceChildren();
    if (rows.length === 0) {
      const li = document.createElement("li");
      li.className = "font-choice is-empty";
      li.textContent = fontChoiceRows.length === 0
        ? "フォントが見つかりませんでした。"
        : "絞り込みに合うフォントがありません。";
      fontChoices.appendChild(li);
      return;
    }

    for (const c of rows) {
      const li = document.createElement("li");
      li.className = "font-choice";
      li.classList.toggle("is-unusable", !!c.missing);

      const name = document.createElement("span");
      name.className = "font-choice-name";
      name.textContent = c.name;
      li.appendChild(name);

      const meta = document.createElement("span");
      meta.className = "font-choice-meta";
      // ⚠️ **ファイル名も出すこと。** 同じ名前の書体が別のファイルに入っていることが
      // あるうえ、日本語名を持たないフォントはファイル名が唯一の手掛かりになる。
      meta.textContent = c.family ? `${c.family} — ${c.file}` : c.file;
      meta.title = c.path;
      li.appendChild(meta);

      if (c.missing) {
        const why = document.createElement("span");
        why.className = "font-choice-why";
        why.textContent = `字が足りません: ${c.missing}`;
        why.title = `駒に要る字のうち ${c.missing} がこのフォントにありません`;
        li.appendChild(why);
      } else {
        const actions = document.createElement("span");
        actions.className = "font-choice-actions";

        const preview = document.createElement("button");
        preview.type = "button";
        preview.className = "ghost-btn";
        preview.textContent = "見本";
        preview.title = "このフォントで駒の字を焼いて、下に見本を出します（登録はしません）";
        preview.addEventListener("click", () => {
          void previewFont(c);
        });
        actions.appendChild(preview);

        const use = document.createElement("button");
        use.type = "button";
        use.className = "ghost-btn";
        use.textContent = c.registered ? "これにする" : "追加して使う";
        use.title = c.registered
          ? "この書体は登録済みです。押すと駒の字をこれに切り替えます"
          : "この書体を登録して、駒の字をこれに切り替えます";
        use.addEventListener("click", () => {
          void addFont(c);
        });
        actions.appendChild(use);

        li.appendChild(actions);
      }
      fontChoices.appendChild(li);
    }
  };

  // 見本。**実際に焼いてから当てる**ので、盤に出る字そのものになる。
  // ⚠️ **family は Go 側が毎回変えて返す** —— 同じ名前で焼き直すと、
  // どちらが当たるかがブラウザ任せになって**前のフォントのまま**に見えることがある。
  const previewFont = async (c: FontChoice) => {
    setFontStatus(`「${c.name}」の駒の字を作っています…`);
    try {
      const face = await FontService.Preview(c.path, c.index);
      registerFontFace("shinte-piece-font-preview", face.family, face.dataUrl);
      fontSample.hidden = false;
      fontSample.style.fontFamily = `"${face.family}", serif`;
      fontSample.textContent = fontSampleText;
      fontSample.title = `${c.name}（${c.file}）`;
      setFontStatus(`「${c.name}」の見本です。使うには「追加して使う」を押してください。`);
    } catch (err) {
      setFontStatus(`見本を作れませんでした: ${String(err)}`, "error");
    }
  };

  const addFont = async (c: FontChoice) => {
    setFontStatus(`「${c.name}」を登録しています…`);
    try {
      showFontState(await FontService.Add(c.path, c.index));
      setFontStatus(`駒の字を「${c.name}」にしました。`);
    } catch (err) {
      setFontStatus(`登録できませんでした: ${String(err)}`, "error");
    }
  };

  const useFont = async (id: string) => {
    try {
      showFontState(await FontService.Use(id));
    } catch (err) {
      setFontStatus(`切り替えられませんでした: ${String(err)}`, "error");
      // 画面を設定ファイルの内容に戻す（食い違ったまま使わせない）。
      try {
        showFontState(await FontService.State());
      } catch {
        /* 読み直せないなら画面はそのまま。理由は上に出ている。 */
      }
    }
  };

  const removeFont = async (id: string) => {
    try {
      showFontState(await FontService.Remove(id));
    } catch (err) {
      setFontStatus(`消せませんでした: ${String(err)}`, "error");
    }
  };

  const renameFont = async (id: string, name: string) => {
    try {
      showFontState(await FontService.Rename(id, name));
    } catch (err) {
      setFontStatus(`名前を変えられませんでした: ${String(err)}`, "error");
    }
  };

  // ⚠️ **押したときだけ探す**（実測 1 秒弱）。**待ちを出すこと** ——
  // 押しても何も起きない時間があると、壊れているように見える。
  fontScanBtn.addEventListener("click", () => {
    void (async () => {
      fontScanBtn.disabled = true;
      const label = fontScanBtn.textContent;
      fontScanBtn.textContent = "探しています…";
      setFontStatus("端末に入っているフォントを探しています…");
      try {
        const scan = await FontService.Scan();
        fontChoiceRows = scan.fonts ?? [];
        fontPicker.hidden = false;
        fontDirs.textContent = `探した場所: ${(scan.dirs ?? []).join(" / ")}`;
        renderFontChoices();
        setFontStatus(
          `${fontChoiceRows.length} 書体のうち、${scan.usable} 書体で駒の字を作れます。`,
        );
      } catch (err) {
        setFontStatus(`フォントを探せませんでした: ${String(err)}`, "error");
      } finally {
        fontScanBtn.disabled = false;
        fontScanBtn.textContent = label;
      }
    })();
  });

  fontFilter.addEventListener("input", renderFontChoices);
  fontOnlyUsable.addEventListener("change", renderFontChoices);

  // 王/玉・左馬。**変えたその場で保存して、その場で盤に出る**
  // （設定タブの他の項目と同じで、適用ボタンは置かない）。
  fontGyoku.addEventListener("change", () => {
    void (async () => {
      try {
        showFontState(await FontService.SetGyoku(fontGyoku.value));
      } catch (err) {
        setFontStatus(`保存できませんでした: ${String(err)}`, "error");
      }
    })();
  });
  fontHidariUma.addEventListener("change", () => {
    void (async () => {
      try {
        showFontState(await FontService.SetHidariUma(fontHidariUma.checked));
      } catch (err) {
        setFontStatus(`保存できませんでした: ${String(err)}`, "error");
      }
    })();
  });

  // 字の色と濃さ。**色と濃さを 1 回で送る**（設定を書く経路を 2 つに分けない）。
  //
  // ⚠️ **`rgba` の合成をここに書かないこと。** 当てる値を作るのは Go 側
  // （`ikkyoku.PieceInk`）だけで、**引いている最中の見た目のためだけに
  // フロントで作り直すと、丸め方が食い違ったときに「離した瞬間に色が飛ぶ」**
  // という追いにくい壊れ方をする（`Score.Label` / 折れ線の色と同じ理由）。
  //
  // 代わりに**送るのを間引く**。引いている最中も反映されるが、
  // 設定ファイルへの書き込みは止まったときの 1 回で済む。
  let inkTimer = 0;
  const saveInk = async (color: string, opacity: number) => {
    window.clearTimeout(inkTimer);
    try {
      showFontState(await FontService.SetPieceInk(color, opacity));
    } catch (err) {
      setFontStatus(`保存できませんでした: ${String(err)}`, "error");
    }
  };
  const saveInkSoon = () => {
    window.clearTimeout(inkTimer);
    inkTimer = window.setTimeout(() => {
      void saveInk(fontInkColor.value, Number(fontInkOpacity.value) / 100);
    }, 150);
  };

  fontInkOpacity.addEventListener("input", () => {
    // 数字だけは即座に動かす（**これは合成ではないので Go を待たなくてよい**）。
    fontInkOpacityValue.textContent = `${fontInkOpacity.value}%`;
    saveInkSoon();
  });
  fontInkColor.addEventListener("input", saveInkSoon);
  // 離したときは待たずに送る（**間引きの取りこぼしを残さない**）。
  for (const el of [fontInkOpacity, fontInkColor]) {
    el.addEventListener("change", () => {
      void saveInk(fontInkColor.value, Number(fontInkOpacity.value) / 100);
    });
  }
  fontInkReset.addEventListener("click", () => {
    // ⚠️ **既定の値をここに書かないこと。** 空を送れば Go 側が既定に倒す。
    void saveInk("", 0);
  });

  // 盤・線・駒台の色（2026-10-02）。**3 つを 1 回で送る**（字の色と同じく、
  // 引いている最中は間引いて送り、離したときは待たずに送る）。
  let boardTimer = 0;
  const saveBoardColors = async (board: string, line: string, stand: string) => {
    window.clearTimeout(boardTimer);
    try {
      showFontState(await FontService.SetBoardColors(board, line, stand));
    } catch (err) {
      setFontStatus(`保存できませんでした: ${String(err)}`, "error");
    }
  };
  for (const el of [boardColor, lineColor, standColor]) {
    el.addEventListener("input", () => {
      window.clearTimeout(boardTimer);
      boardTimer = window.setTimeout(() => {
        void saveBoardColors(boardColor.value, lineColor.value, standColor.value);
      }, 150);
    });
    el.addEventListener("change", () => {
      void saveBoardColors(boardColor.value, lineColor.value, standColor.value);
    });
  }
  boardColorReset.addEventListener("click", () => {
    // ⚠️ **既定の値をここに書かないこと。** 空を送れば Go 側が既定に倒す。
    void saveBoardColors("", "", "");
  });

  // 認識器の読み込み元を画面に映す。
  //
  // ⚠️ **既定の解決（空なら auto）は Go 側**（`Config.SutemeSourceOr`）。
  // 返ってきた値をそのまま入れるだけにすること。
  const showSutemeSource = (s: {
    sutemeSource: string;
    sutemeDataDir: string;
    sutemeModelDir: string;
    sutemeEmbedAvailable: boolean;
    sutemeEmbedSource: string;
  }) => {
    sutemeSource.value = s.sutemeSource;
    sutemeDataDir.value = s.sutemeDataDir;
    sutemeModelDir.value = s.sutemeModelDir;
    const embedOption = sutemeSource.querySelector<HTMLOptionElement>('option[value="embed"]')!;
    // 焼き込みの無いビルドでは選ばせない。**選択肢ごと消さない** ——
    // 設定ファイルが "embed" のまま開かれることがあり、消すと選択が勝手に変わる。
    embedOption.disabled = !s.sutemeEmbedAvailable;
    embedOption.textContent = s.sutemeEmbedAvailable
      ? `焼き込みから（${s.sutemeEmbedSource || "出所不明"}）`
      : "焼き込みから（このビルドには入っていません）";
    // 欄は「焼き込みから」のときも残す（戻すときに打ち直させない）。
    sutemeDataDir.disabled = false;
    sutemeModelDir.disabled = false;
    // ⚠️ **今どこから読んでいるか（sutemeSourceNote）はここで書かない** —— 設定の値からは
    // 決まらない。showSutemeState（Go の RecognizerStatus）が書く。
  };

  const showSettings = (s: AppSettings) => {
    fitOnStartup.checked = s.fitOnStartup;
    clickThrough.checked = s.clickThrough;
    followFirst.checked = s.followFirst;
    followDebug.checked = s.followDebug;
    if (logLevel.options.length !== (s.logLevels ?? []).length) {
      logLevel.replaceChildren();
      for (const o of s.logLevels ?? []) {
        const opt = document.createElement("option");
        opt.value = o.value;
        opt.textContent = o.label;
        logLevel.appendChild(opt);
      }
    }
    logLevel.value = s.logLevel;
    // 配色。選択肢は Go 側が返したものをそのまま並べる（**フロントに表を書かない**）。
    if (themeSelect.options.length !== (s.themes ?? []).length) {
      themeSelect.replaceChildren();
      for (const o of s.themes ?? []) {
        const opt = document.createElement("option");
        opt.value = o.value;
        opt.textContent = o.label;
        themeSelect.appendChild(opt);
      }
    }
    themeSelect.value = s.theme;
    themeFoldSum.textContent = themeSelect.selectedOptions[0]?.textContent ?? "";
    applyTheme(s.theme);
    showSutemeSource(s);
    // ⚠️ **既定の解決は Go 側**（`Config.KifuDB`）。返ってきた場所をそのまま入れる。
    kifuDBPath.value = s.kifuDbPath;
    void refreshKifuDBNote();
    // ⚠️ **既定値の解決は Go 側**（`analyze.PonanzaConstantOr`）。返ってきた値を
    // そのまま入れるだけにすること（フロントに既定を書くと 2 か所に散る）。
    ponanzaConstant.value = String(s.ponanzaConstant);
    // ⚠️ **押せるかどうかは場所が分かっているかで決める**（決められなかったときに
    // 空をコピーさせない）。コピーするのは `dataset` のほうで、表示は短くしない。
    settingsPath.textContent = s.path
      ? `設定ファイル: ${s.path}`
      : "設定ファイル: (保存先を決められませんでした)";
    settingsPath.dataset.path = s.path ?? "";
    settingsPath.disabled = !s.path;
    // ⚠️ **版の組み立ては Go 側**（開発ビルドの `+dev` もあちら）。来たものを出すだけ。
    appVersion.textContent = s.appVersion ? `ikkyoku v${s.appVersion}` : "";
    appVersion.hidden = !s.appVersion;
    showEngineList(s.engines ?? []);
    // 既定値の解決は Go 側(training パッケージ)が済ませて返す。**フロントに
    // 既定値を書かないこと**(2 か所に持つと、既定を変えたときに食い違う)。
    const t = s.training;
    trainEnabledInput.checked = t.enabled;
    trainURL.value = t.url;
    trainToken.value = t.token;
    // 設定タブの外（盤の周り・側の列・評価値グラフ・訂正データの送信）へ配る。
    // ⚠️ **配る先はここ 1 本**（設定が唯一の出所。配る中身は mainscreen.ts の applySettings）。
    opts.onSettings(s);
  };

  // 接続設定の保存。**接続の確認はしない**(保存と疎通は別の操作)。
  // 先に設定を入れてから suteme を起動する、という順序が普通にあるため。
  // ⚠️ **URL の読み方をここに書かない**(既定の補い・/api/register を剥がすのは Go の training.ParseURL)。
  const saveTraining = async () => {
    settingsStatus.textContent = "";
    settingsStatus.classList.remove("is-error");
    try {
      showSettings(
        await SettingsService.SetTraining(
          trainEnabledInput.checked,
          trainURL.value,
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

  for (const el of [trainEnabledInput, trainURL, trainToken]) {
    el.addEventListener("change", () => {
      void saveTraining();
    });
  }

  // 配色の保存（2026-10-02）。⚠️ **ほかの窓へは `theme:changed` で知らせる** ——
  // 枠と切り離した窓も同じ配色にする（受け取るのは `theme.ts`）。
  themeSelect.addEventListener("change", () => {
    void (async () => {
      settingsStatus.textContent = "";
      settingsStatus.classList.remove("is-error");
      try {
        const st = await SettingsService.SetTheme(themeSelect.value);
        showSettings(st);
        await Events.Emit("theme:changed", st.theme);
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
    // 畳んでいても開く（理由が閉じた中に隠れてはいけない）。
    analyzeFold.open = true;
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

  // 行を id で引き直す。⚠️ **要素を覚えておかないこと** ——「接続を確認」の途中で
  // 設定を読み直して**一覧ごと描き直す**ので、掴んでいた要素は捨てられている
  // （候補手の選択を「エンジン + 順位」で持っているのと同じ話）。
  const engineRow = (id: string) =>
    engineList.querySelector<HTMLElement>(`.engine-row[data-id="${id}"]`);

  // エンジン 1 つに実際に繋いでみる。**1 行ずつ**（まとめて起こすと、どれが遅くて
  // どれが落ちたのか分からない）。確かめたら閉じるので、プロセスは残らない。
  //
  // ⚠️ **繋がったら設定を読み直す。** Go 側がこのときに option の宣言を控えるので、
  // 読み直さないと**設定項目が画面に出てくるのが次に設定タブを開いたとき**になる
  // （押した結果が見えないと、効いたのかどうか分からない）。
  const checkEngine = async (id: string, btn: HTMLButtonElement) => {
    const note = engineRowNote(engineRow(id)!);
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
      // 宣言を控えたぶんを画面に出す（**この呼び出しで一覧が描き直される**）。
      try {
        openOptions.add(id); // 読めた設定項目をそのまま開いて見せる
        showSettings(await SettingsService.Settings());
      } catch {
        /* 読み直せないなら一覧はそのまま。結果は下に出る。 */
      }
      const after = engineRow(id);
      if (!after) return; // 行ごと消えた（削除された）。出す先が無い
      const note2 = engineRowNote(after);
      // **何を送ったかまで出す。** setoption には応答が返らないので、
      // 効いているかどうかを確かめる手掛かりがこれしかない。
      const applied =
        r.options > 0
          ? `option ${r.options} 件を宣言、${r.applied} 件を送信（既定値を含む）`
          : "option の宣言はありません";
      // ⚠️ **起動の時間は「解析タブで最初に解析するとき」に 1 回払う**
      // （そのあとは接続を使い回す）。繋ぎ先を選ぶ材料になるので出しておく。
      const startup = `起動 ${(r.startupMs / 1000).toFixed(1)} 秒（解析タブで最初に解析するときにかかります）`;
      note2.textContent = `繋がりました: ${r.name} / ${applied} / ${startup}`;
    } catch (err) {
      const now = engineRow(id);
      if (!now) return;
      const n = engineRowNote(now);
      n.textContent = `確認できませんでした: ${String(err instanceof Error ? err.message : err)}`;
      n.classList.add("is-error");
    } finally {
      // ⚠️ **押したボタンではなく、今そこにあるボタンを戻す**（描き直しで別物になっている）。
      const now = engineRow(id)?.querySelector<HTMLButtonElement>(".engine-check");
      if (now) now.disabled = false;
      btn.disabled = false;
    }
  };

  // フォーカスを戻す相手を引くクラス名を選ぶ。
  //
  // ⚠️ **`engine-` で始まるクラスを優先すること。** 素朴に「最初のクラス」で引くと、
  // `ghost-btn engine-check` のような**見た目のクラスが先に来ているボタン**で
  // **同じ行の別のボタン（参照…）に戻ってしまう**。役割を表しているのは後ろのほう。
  const keepId = (cls: string) => {
    const names = cls.split(" ").filter(Boolean);
    return names.find((n) => n.startsWith("engine-")) ?? names[0] ?? "";
  };

  // 開いているエンジンの設定（折りたたみ）。⚠️ **画面だけの状態なので Go に持たせない**
  // （手順の畳み方と同じ扱い）。一覧は保存のたびに描き直されるので、
  // **覚えておかないと値を 1 つ変えるたびに閉じる。**
  const openOptions = new Set<string>();

  // option 1 つぶんの入力欄を作る。**型ごとに形を変える**のがここの仕事:
  //
  //   check              → チェックボックス
  //   spin               → 数値（min/max つき）
  //   combo              → 選択（var の並び）
  //   string / filename  → テキスト
  //   button             → **押すだけ**。値を持たないので設定できない（下記）
  //
  // ⚠️ **既定値も範囲も選択肢も Go 側が渡したものをそのまま使う。** エンジンごとに
  // 違うので、フロントに表を書くと必ず食い違う。
  const engineOptionRow = (
    id: string,
    o: NonNullable<EngineSettings["options"]>[number],
  ): HTMLElement => {
    const box = document.createElement("div");
    box.className = "engine-option";
    box.dataset.name = o.name;
    box.classList.toggle("is-custom", o.custom);

    const label = document.createElement("label");
    label.className = "engine-option-name";
    label.textContent = o.name;
    // 何を送る項目なのかは名前だけでは分からないので、型と既定値を添える。
    label.title =
      `${o.name}（${o.known ? o.type : "宣言に無い項目"}）` +
      (o.known && o.default !== "" ? ` / 既定: ${o.default}` : "") +
      (o.hasMin || o.hasMax
        ? ` / 範囲: ${o.hasMin ? o.min : ""}〜${o.hasMax ? o.max : ""}`
        : "");
    box.appendChild(label);

    // 保存は**その場で**（設定タブの他の項目と同じ。適用ボタンを置かない）。
    const save = (value: string) => {
      void applyEngineChange(() => SettingsService.SetEngineOption(id, o.name, value));
    };

    if (o.type === "button") {
      // ⚠️ **button は値を持たない**（送ること自体が「押した」という動作）。
      // 押せる相手は**繋がっているエンジン**だけで、ここには居ない
      // （接続は解析タブに居るあいだしか生きていない）。**設定として保存しない。**
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = "ghost-btn engine-option-input";
      btn.textContent = o.name;
      btn.disabled = true;
      btn.title = "押すだけの項目です（値を持たないので、ここでは設定できません）";
      box.appendChild(btn);
      return box;
    }

    let input: HTMLInputElement | HTMLSelectElement;
    if (o.type === "check") {
      const el = document.createElement("input");
      el.type = "checkbox";
      el.checked = o.value === "true";
      el.addEventListener("change", () => save(el.checked ? "true" : "false"));
      input = el;
    } else if (o.type === "combo" && (o.vars ?? []).length > 0) {
      const el = document.createElement("select");
      for (const v of o.vars ?? []) {
        const opt = document.createElement("option");
        opt.value = v;
        opt.textContent = v;
        el.appendChild(opt);
      }
      el.value = o.value;
      el.addEventListener("change", () => save(el.value));
      input = el;
    } else if (o.type === "spin") {
      const el = document.createElement("input");
      el.type = "number";
      if (o.hasMin) el.min = String(o.min);
      if (o.hasMax) el.max = String(o.max);
      el.value = o.value;
      // ⚠️ **change（確定時）で拾う**（設定タブのテキスト欄と同じ）。
      // input だと 1 文字ごとに保存して、打っている途中の値で弾かれる。
      el.addEventListener("change", () => save(el.value));
      input = el;
    } else {
      const el = document.createElement("input");
      el.type = "text";
      el.spellcheck = false;
      el.value = o.value;
      el.placeholder = o.default;
      el.addEventListener("change", () => save(el.value));
      input = el;
    }
    input.className = "engine-option-input";
    box.appendChild(input);

    // 既定に戻す口。⚠️ **人が変えた項目にだけ出す**（押せるものが常に並んでいると、
    // どれを変えたのかが分からない）。宣言に無い項目では「消す」ことになる。
    if (o.custom) {
      const reset = document.createElement("button");
      reset.type = "button";
      reset.className = "engine-option-reset";
      reset.textContent = o.known ? "既定" : "消す";
      reset.title = o.known
        ? `既定（${o.default === "" ? "空" : o.default}）に戻します`
        : "この項目を設定から消します";
      reset.addEventListener("click", () => save(""));
      box.appendChild(reset);
    }
    if (!o.known) {
      const note = document.createElement("span");
      note.className = "engine-option-note note";
      note.textContent = "宣言に無い項目";
      note.title =
        "エンジンが宣言していない option です（設定ファイルに書いたものか、" +
        "実行ファイルを差し替えて宣言だけ捨てたもの）。値はそのまま送ります。";
      box.appendChild(note);
    }
    return box;
  };

  // エンジン 1 つぶんの設定（行の中の折りたたみ）。
  //
  // ⚠️ **既定で閉じる。** 数十件あるエンジンが普通なので、開きっぱなしにすると
  // 一覧が読めない。⚠️ **見出しに「何件変えているか」を出すこと** ——
  // 畳んだままでも、既定のままなのかどうかが分かる必要がある。
  const engineOptionsBox = (e: EngineSettings): HTMLElement => {
    const box = document.createElement("details");
    box.className = "engine-options";
    box.open = openOptions.has(e.id);
    box.addEventListener("toggle", () => {
      if (box.open) openOptions.add(e.id);
      else openOptions.delete(e.id);
    });

    const head = document.createElement("summary");
    head.textContent = "エンジンの設定";
    const count = document.createElement("span");
    count.className = "note";
    const options = e.options ?? [];
    count.textContent = e.optionsKnown
      ? `（${options.length} 項目${e.optionCount > 0 ? ` / ${e.optionCount} 件を変更中` : ""}）`
      : options.length > 0
        ? `（${options.length} 件。宣言は未取得）`
        : "（未取得）";
    head.appendChild(count);
    box.appendChild(head);

    const body = document.createElement("div");
    body.className = "engine-options-body";
    if (!e.optionsKnown) {
      // ⚠️ **「宣言が無い」と言い切らないこと。** まだ繋いでいないだけかもしれない。
      const hint = document.createElement("p");
      hint.className = "note";
      hint.textContent =
        "「接続を確認」を押すと、このエンジンが受け付ける設定項目を読み込みます。";
      body.appendChild(hint);
    } else if (options.length === 0) {
      const hint = document.createElement("p");
      hint.className = "note";
      hint.textContent = "このエンジンは設定項目を宣言していません。";
      body.appendChild(hint);
    }
    for (const o of options) body.appendChild(engineOptionRow(e.id, o));

    if (e.optionCount > 0) {
      const reset = document.createElement("button");
      reset.type = "button";
      reset.className = "ghost-btn engine-options-reset";
      reset.textContent = "全部を既定に戻す";
      reset.title = "設定した値を全部捨てて、エンジンの既定値に戻します";
      reset.addEventListener("click", () => {
        void applyEngineChange(() => SettingsService.ResetEngineOptions(e.id));
      });
      body.appendChild(reset);
    }
    box.appendChild(body);
    return box;
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
    // option の欄はクラス名が全部同じなので、**どの項目だったか**も覚えておく
    // （名前で引き直す。⚠️ 落とすと、1 つ変えるたびに一覧の先頭へフォーカスが飛ぶ）。
    const keepOption = active?.closest<HTMLElement>(".engine-option")?.dataset.name;

    // 畳んでいるときは「何個登録していて、何個が走るか」だけ出す。
    const useCount = engines.filter((e) => e.enabled).length;
    analyzeFoldSum.textContent =
      engines.length === 0
        ? "エンジンの登録なし"
        : `エンジン ${engines.length} 個 / 解析に使う ${useCount} 個`;

    engineList.replaceChildren();
    for (const [index, e] of engines.entries()) {
      const row = document.createElement("li");
      row.className = "engine-row";
      row.dataset.id = e.id;
      row.innerHTML = `
        <!-- 並べ替え。**押した位置に答えが出る**ように行の先頭に置く。
             ⚠️ ドラッグにしていないのは、行の中に入力欄が 2 つあって掴む場所が
             残らないため（掴み手を別に作るくらいなら、押せば動くほうが速い）。 -->
        <div class="engine-order">
          <button class="engine-up engine-move" type="button"
                  aria-label="上へ" title="1 つ上へ">${iconMarkup(FiChevronUp)}</button>
          <button class="engine-down engine-move" type="button"
                  aria-label="下へ" title="1 つ下へ">${iconMarkup(FiChevronDown)}</button>
        </div>
        <label class="engine-use" title="解析のときにこのエンジンを使います（複数選べます）">
          <input class="engine-enabled" type="checkbox" />
          <span>解析に使う</span>
        </label>
        <!-- 詰み探索に使うか（2026-09-12）。⚠️ **「解析に使う」とは独立した軸**で、
             両方入れれば両方に使う。**片方がもう片方を外さないこと** ——
             チェックを入れたのに使われないのは、画面から理由が読めない。
             （詰将棋エンジンは通常の go に答えないことがあるが、それを承知で
             使うかどうかを決めるのは人。こちらは title で説明するだけ。） -->
        <label class="engine-use" title="詰みを go mate で解かせる相手にします。「解析に使う」とは別なので、両方入れれば両方に使います（詰将棋エンジンは通常の解析に答えないことがあります）">
          <input class="engine-mate" type="checkbox" />
          <span>詰み</span>
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
      const mate = row.querySelector<HTMLInputElement>(".engine-mate")!;
      const name = row.querySelector<HTMLInputElement>(".engine-name")!;
      const path = row.querySelector<HTMLInputElement>(".engine-path")!;
      enabled.checked = e.enabled;
      mate.checked = e.mate;
      // ⚠️ **既定の名前は placeholder に出し、value には入れない。**
      // 入れてしまうと、パスを変えても名前が追従しなくなる（Go 側が
      // 「人が付けた名前か」を custom で返しているのはこのため）。
      name.value = e.custom ? e.name : "";
      name.placeholder = e.name;
      path.value = e.path;
      // **同梱かどうかの判定は Go 側の値を使う**（フロントで path === "" を書かない）。
      const opts = e.optionCount > 0 ? ` / setoption ${e.optionCount} 件` : "";
      // ⚠️ **「詰み」は「解析に使う」を打ち消さない**ので、並べて出すだけ。
      const kind = [e.builtin ? "同梱のエンジン" : "", e.mate ? "詰み探索に使う" : ""]
        .filter(Boolean)
        .join(" / ");
      engineRowNote(row).textContent = kind + opts;
      row.classList.toggle("is-off", !e.enabled);

      // 並べ替え。⚠️ **端では押せなくする**（押しても何も起きないボタンは、
      // 壊れているのか端なのかが区別できない）。回り込ませもしない。
      const up = row.querySelector<HTMLButtonElement>(".engine-up")!;
      const down = row.querySelector<HTMLButtonElement>(".engine-down")!;
      up.disabled = index === 0;
      down.disabled = index === engines.length - 1;
      up.addEventListener("click", () => {
        void applyEngineChange(() => SettingsService.MoveEngine(e.id, -1));
      });
      down.addEventListener("click", () => {
        void applyEngineChange(() => SettingsService.MoveEngine(e.id, 1));
      });

      enabled.addEventListener("change", () => {
        void applyEngineChange(() => SettingsService.SetEngineEnabled(e.id, enabled.checked));
      });
      mate.addEventListener("change", () => {
        void applyEngineChange(() => SettingsService.SetEngineMate(e.id, mate.checked));
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
        void checkEngine(e.id, checkBtn);
      });
      row.querySelector<HTMLButtonElement>(".engine-remove")!.addEventListener("click", () => {
        void applyEngineChange(() => SettingsService.RemoveEngine(e.id));
      });

      // エンジンの設定（option）。⚠️ **行の中に置くこと** —— どのエンジンの設定かは
      // 位置で示す（設定タブに別の区画を作ると、行と結び付かない）。
      row.appendChild(engineOptionsBox(e));

      engineList.appendChild(row);
    }

    // 打っている最中だった欄にフォーカスと文字を戻す。
    //
    // ⚠️ **ボタンにも戻すこと**（並べ替えの ▲▼）。行ごと描き直すので、戻さないと
    // **1 つ動かすたびにフォーカスが飛んで、続けて押せない**（3 つ上げたいときに
    // 毎回カーソルで押しにいくことになる）。ボタンは**役割のクラス名で引く**ので
    // （`keepId`）、`.engine-up` / `.engine-down` の順で書いてある
    // （`.engine-move` を先頭にすると下ボタンを押したのに上ボタンへ戻る）。
    if (keep) {
      const row = engineList.querySelector<HTMLElement>(`.engine-row[data-id="${keep}"]`);
      // option の欄は**その項目の中から**引く（クラス名は全部同じなので、
      // 行から引くと必ず先頭の項目に戻ってしまう）。
      const scope = keepOption
        ? [...(row?.querySelectorAll<HTMLElement>(".engine-option") ?? [])].find(
            (b) => b.dataset.name === keepOption,
          )
        : row;
      const el = scope?.querySelector<HTMLElement>(`.${keepId(keepClass)}`);
      if (el instanceof HTMLInputElement && el.type === "text") {
        el.value = keepValue;
      }
      // 端まで動かしたら押せなくなっているので、そのときは行だけ見えていればよい。
      if (el && !(el instanceof HTMLButtonElement && el.disabled)) {
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
      } catch (err) {
        fitOnStartup.checked = !want;
        settingsStatus.textContent = `設定を保存できませんでした: ${String(err)}`;
        settingsStatus.classList.add("is-error");
      } finally {
        fitOnStartup.disabled = false;
      }
    })();
  });

  // 枠の内側の素通し。**切り替えたその場で効く**（起動時に盤面を探す、とはそこが違う）。
  clickThrough.addEventListener("change", () => {
    void (async () => {
      const want = clickThrough.checked;
      clickThrough.disabled = true;
      settingsStatus.textContent = "";
      settingsStatus.classList.remove("is-error");
      try {
        showSettings(await SettingsService.SetClickThrough(want));
      } catch (err) {
        clickThrough.checked = !want;
        settingsStatus.textContent = `設定を保存できませんでした: ${String(err)}`;
        settingsStatus.classList.add("is-error");
      } finally {
        clickThrough.disabled = false;
      }
    })();
  });

  followFirst.addEventListener("change", () => {
    void (async () => {
      const want = followFirst.checked;
      followFirst.disabled = true;
      settingsStatus.textContent = "";
      settingsStatus.classList.remove("is-error");
      try {
        showSettings(await SettingsService.SetFollowFirst(want));
      } catch (err) {
        followFirst.checked = !want;
        settingsStatus.textContent = `設定を保存できませんでした: ${String(err)}`;
        settingsStatus.classList.add("is-error");
      } finally {
        followFirst.disabled = false;
      }
    })();
  });

  logLevel.addEventListener("change", () => {
    void (async () => {
      const want = logLevel.value;
      logLevel.disabled = true;
      settingsStatus.textContent = "";
      settingsStatus.classList.remove("is-error");
      try {
        showSettings(await SettingsService.SetLogLevel(want));
      } catch (err) {
        settingsStatus.textContent = `設定を保存できませんでした: ${String(err)}`;
        settingsStatus.classList.add("is-error");
        try {
          showSettings(await SettingsService.Settings());
        } catch {
          /* 読み直せないなら画面はそのまま。理由は上に出ている。 */
        }
      } finally {
        logLevel.disabled = false;
      }
    })();
  });

  followDebug.addEventListener("change", () => {
    void (async () => {
      const want = followDebug.checked;
      followDebug.disabled = true;
      settingsStatus.textContent = "";
      settingsStatus.classList.remove("is-error");
      try {
        showSettings(await SettingsService.SetFollowDebug(want));
      } catch (err) {
        followDebug.checked = !want;
        settingsStatus.textContent = `設定を保存できませんでした: ${String(err)}`;
        settingsStatus.classList.add("is-error");
      } finally {
        followDebug.disabled = false;
      }
    })();
  });

  // 棋譜データベース（棚）。
  //
  // ⚠️ **「開けているか」は設定ではなく状態**なので、AppSettings ではなく
  // `KifuService.Status` から取る（設定は「どこを開こうとしているか」）。
  // ⚠️ **開けなくてもエラー色にしすぎないこと** —— 棚は解析の前提条件ではない
  // （設計原則3）。「棋譜タブだけが使えない」ことが読めるように書く。
  const refreshKifuDBNote = async () => {
    let st: KifuDBStatus;
    try {
      st = await KifuService.Status();
    } catch (err) {
      kifuDBNote.textContent = `状態を取得できませんでした: ${String(err)}`;
      kifuDBNote.classList.add("is-error");
      kifuDBFoldSum.textContent = "状態が取れません";
      kifuDBFold.open = true;
      return;
    }
    if (st.ready) {
      kifuDBNote.textContent = `開いています（${st.count}件）。`;
      kifuDBNote.classList.remove("is-error");
      kifuDBFoldSum.textContent = `${st.count}件`;
    } else {
      kifuDBNote.textContent = st.error
        ? `開けていません: ${st.error}（棋譜タブだけが使えません）`
        : "開けていません（棋譜タブだけが使えません）。";
      kifuDBNote.classList.add("is-error");
      kifuDBFoldSum.textContent = "開けていません";
      // ⚠️ **開けていないときは畳んでいても開く**（理由が閉じた中に隠れてはいけない）。
      kifuDBFold.open = true;
    }
  };

  // ⚠️ **change（確定時）で拾う。** パスを 1 文字打つたびに DB を開き直さない。
  const applyKifuDBPath = async (want: string) => {
    kifuDBPath.disabled = true;
    kifuDBBrowse.disabled = true;
    settingsStatus.textContent = "";
    settingsStatus.classList.remove("is-error");
    try {
      showSettings(await SettingsService.SetKifuDBPath(want));
      // ⚠️ **開き直した結果はここで取り直す**（showSettings は設定しか映さない）。
      await refreshKifuDBNote();
      opts.onKifuDBChanged();
    } catch (err) {
      settingsStatus.textContent = `設定を保存できませんでした: ${String(err)}`;
      settingsStatus.classList.add("is-error");
      try {
        showSettings(await SettingsService.Settings());
      } catch {
        // 読み直せないなら画面はそのまま。
      }
    } finally {
      kifuDBPath.disabled = false;
      kifuDBBrowse.disabled = false;
    }
  };

  kifuDBPath.addEventListener("change", () => {
    void applyKifuDBPath(kifuDBPath.value);
  });

  kifuDBBrowse.addEventListener("click", () => {
    void (async () => {
      kifuDBBrowse.disabled = true;
      try {
        showSettings(await SettingsService.BrowseKifuDB());
        await refreshKifuDBNote();
        opts.onKifuDBChanged();
      } catch (err) {
        settingsStatus.textContent = `選べませんでした: ${String(err)}`;
        settingsStatus.classList.add("is-error");
      } finally {
        kifuDBBrowse.disabled = false;
      }
    })();
  });

  // 認識器の読み込み元。**切り替えたその場で読み直す**（Go 側が ReloadRecognizer を呼ぶ）。
  // 読み直した結果は撮影タブの「認識器: …」に出るので、ここでも取り直して映す。
  const reloadRecognizerView = async () => {
    try {
      opts.onRecognizer(await CaptureService.Recognizer());
    } catch {
      // 表示の更新に失敗しても設定の保存は済んでいる。黙って諦める。
    }
  };

  sutemeSource.addEventListener("change", () => {
    void (async () => {
      const want = sutemeSource.value;
      sutemeSource.disabled = true;
      settingsStatus.textContent = "";
      settingsStatus.classList.remove("is-error");
      try {
        showSettings(await SettingsService.SetSutemeSource(want));
        await reloadRecognizerView();
      } catch (err) {
        settingsStatus.textContent = `設定を保存できませんでした: ${String(err)}`;
        settingsStatus.classList.add("is-error");
        try {
          showSettings(await SettingsService.Settings());
        } catch {
          // 読み直せないなら画面はそのまま。
        }
      } finally {
        sutemeSource.disabled = false;
      }
    })();
  });

  // 配布モデルの置き場所（2026-10-04）。学習データの欄と同じく change で拾う。
  sutemeModelDir.addEventListener("change", () => {
    void (async () => {
      const want = sutemeModelDir.value;
      sutemeModelDir.disabled = true;
      settingsStatus.textContent = "";
      settingsStatus.classList.remove("is-error");
      try {
        showSettings(await SettingsService.SetSutemeModelDir(want));
        await reloadRecognizerView();
      } catch (err) {
        settingsStatus.textContent = `設定を保存できませんでした: ${String(err)}`;
        settingsStatus.classList.add("is-error");
      } finally {
        sutemeModelDir.disabled = false;
      }
    })();
  });

  // ⚠️ **change（確定時）で拾う。** パスを 1 文字打つたびに読み込み直すと、
  // 20MB 級の学習データを何度も読むことになる。
  sutemeDataDir.addEventListener("change", () => {
    void (async () => {
      const want = sutemeDataDir.value;
      sutemeDataDir.disabled = true;
      settingsStatus.textContent = "";
      settingsStatus.classList.remove("is-error");
      try {
        showSettings(await SettingsService.SetSutemeDataDir(want));
        await reloadRecognizerView();
      } catch (err) {
        settingsStatus.textContent = `設定を保存できませんでした: ${String(err)}`;
        settingsStatus.classList.add("is-error");
      } finally {
        sutemeDataDir.disabled = false;
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

  // 駒の字。**起動した時点で当てる**（設定タブを開くまで同梱の字、では遅い ——
  // 最初に見えるのは盤なので、そこが既に選んだ書体になっている必要がある）。
  //
  // ⚠️ **失敗しても黙って同梱のままにすること**（設計原則3）。フォントを
  // 消していても、盤は描けるし解析もできる。理由は設定タブに出る。
  void (async () => {
    try {
      showFontState(await FontService.State());
    } catch (err) {
      fontStatus.textContent = `駒の字の設定を読み込めませんでした: ${String(err)}`;
      fontStatus.classList.add("is-error");
    }
  })();

  // 切り離した窓で色や本数を変えたら、設定タブと折れ線の色も追随させる。
  Events.On("settings:changed", () => {
    void (async () => {
      try {
        showSettings(await SettingsService.Settings());
      } catch {
        /* 読めなくても今の表示のまま（設計原則3）。 */
      }
    })();
  });

  return { showSettings, showSutemeState };
}
