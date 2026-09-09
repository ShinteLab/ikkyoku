// 盤の右の列（解析の面）。**候補手・手順・解析の操作が全部ここに入る。**
//
// ⚠️ **別ウィンドウへ切り離せるようにするため、`mainscreen.ts` から切り出した**
// （2026-09-08）。**ドックしたときも切り離したときも同じものが動く** ——
// 片方だけ直せる形にすると、置き場所を変えただけで挙動が変わる。
//
// ⚠️ **盤は持たない。** 盤に効くもの（候補手の矢印・勝率バー・連続解析の幕）は
// **呼び出し側へ渡す**（`onHint` / `onScores` / `onBusy`）。切り離すと盤は
// 別の窓にあるので、**ここから直に触れない**のが前提。
//
// ⚠️ **局面も持たない。** 描くのは渡された `StudyState` そのもので、
// 操作は Service を呼ぶだけ。どちらの窓から押しても、変わるのは Go 側の 1 つの手順。
import { Events } from "@wailsio/runtime";
import { FiRefreshCw } from "react-icons/fi";

import {
  AnalyzeService,
  SettingsService,
  StudyService,
} from "../bindings/github.com/ShinteLab/ikkyoku/app";
import type {
  AppSettings,
  EngineSettings,
  StudyState,
} from "../bindings/github.com/ShinteLab/ikkyoku/app/models";
import type { EngineColorOption } from "../bindings/github.com/ShinteLab/ikkyoku/models";
import { iconMarkup } from "./icon";
import { mountMoveList } from "./movelist";
import { openPopup } from "./popup";
import type {
  AnalyzeDone,
  AnalyzeFailure,
  AnalyzeLine,
  AnalyzeProgress,
} from "./analyzeevents";

// 勝率バーに出す材料（**エンジンごとの最善手**）。
//
// ⚠️ **盤の上のバーは呼び出し側が描く**ので、ここは値を渡すだけ。
// 切り離すとバーは別の窓にあるので、**ここから直に触れない**。
export interface EngineScore {
  id: string;
  label: string;
  // winRate は先手の勝率（0〜1）。**Go 側の値**で、cp から計算し直さないこと。
  winRate: number | null;
  scoreLabel: string;
}

export interface SidePaneHandle {
  // render は局面が変わったときに呼ぶ（null なら空にする）。
  render(state: StudyState | null): void;
  // setEngines は設定（色・名前・候補手の本数）を入れ直す。
  setEngines(engines: EngineSettings[], colors: EngineColorOption[], seconds: number): void;
  // setStatus は手順の下の 1 行（**空なら消す**）。
  setStatus(message: string): void;
  // reveal は面に来たとき（連続モードならそのまま解析を始める）。
  reveal(): void;
  // release は解析を止める（面を離れる / 窓を閉じるとき）。
  release(): void;
  // cancelBatch は連続解析をやめる（**幕の中の出口から呼ばれる**）。
  cancelBatch(): void;
  // stepping は連続解析が走っているか（**キー操作を横取りしないため**）。
  stepping(): boolean;
  // setActive は「この面が今使われているか」（2026-09-08）。
  //
  // ⚠️ **切り離すと、ドック側のペインは隠れたまま生き続ける。** そのままだと
  // **両方が連続モードで解析を起こし合う**（互いの解析を打ち切り続ける）ので、
  // 使われていない側は**自動解析をしない**。
  setActive(on: boolean): void;
}

export interface SidePaneOptions {
  // host は中身を組む器（`#study-side`）。
  host: HTMLElement;
  // onState は操作で局面が変わったときに呼ぶ。**盤も一緒に描き直すのは呼び出し側。**
  onState(state: StudyState): void;
  // onHint は候補手を選んだとき（盤に矢印を出す。null で消す）。
  onHint(usi: string | null): void;
  // onScores は勝率バーの材料（**エンジンごとの最善手**）。
  onScores(scores: EngineScore[]): void;
  // onBusy は連続解析の幕（触れなくする）。
  onBusy(on: boolean, note: string): void;
  // onSettings は設定を書き換えたとき（設定タブを描き直すのは呼び出し側）。
  onSettings(settings: AppSettings): void;
  // action は**解析の行の右端**に置くもの（2026-09-09）。
  //
  // 切り離した窓の「ドックに戻す」がこれ。⚠️ **ドックしているときは渡さないこと**
  // —— あちらに戻す相手が居ない（戻す口はバーの上のトグル）。
  //
  // ⚠️ **見出しの行を別に作らないこと。** 窓の一番上に 1 行足すと、そのぶん
  // **候補手と手順の取り分が減る**（切り離すのは大きく見たいからで、逆行する）。
  action?: HTMLElement;
}

export function mountSidePane(opts: SidePaneOptions): SidePaneHandle {
  const { host, onState, onHint, onScores, onBusy, onSettings } = opts;
  host.innerHTML = `
            <!-- 確定した局面でも警告は出うる（詰将棋のように「論理的におかしくても
                 正しい」局面があるため。設計原則3）。変な評価値が出たときの手掛かり。
                 ⚠️ **盤の上に置かないこと**（2026-08-12 に上段ごと外した）——
                 めったに出ないもののために、盤の高さを常に削ることになる。 -->
            <ul id="study-warnings" class="warnings is-compact" hidden></ul>
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
                  <!-- 1〜10 秒を刻んで、そのあと 30 秒と無制限（2026-08-15）。
                       ⚠️ **細かいのは短いほうだけでよい** —— 連続解析は
                       「手数 × 秒数」で待ち時間が決まるので、**1 秒の差が
                       150 手では 2 分半になる**（長いほうは刻んでも使い分けない）。
                       ⚠️ **既定は 3 秒**（selected を付けた option）。変えると
                       連続解析のボタンの文言と見積もりが一緒に動く。
                       ⚠️ template literal の中なので、コメントにバッククォートを
                       使わないこと（文字列がそこで切れる）。 -->
                  <option value="1">1秒</option>
                  <option value="2">2秒</option>
                  <option value="3" selected>3秒</option>
                  <option value="4">4秒</option>
                  <option value="5">5秒</option>
                  <option value="6">6秒</option>
                  <option value="7">7秒</option>
                  <option value="8">8秒</option>
                  <option value="9">9秒</option>
                  <option value="10">10秒</option>
                  <option value="30">30秒</option>
                  <option value="0">無制限</option>
                </select>
              </label>
              <!-- ⚠️ **「全て表示」もここには無い**（2026-08-15）。**エンジンごと**なので、
                   入口は本数（MultiPV）と同じ**エンジンの見出し**。 -->
              <!-- ⚠️ **候補手の本数（MultiPV）の欄はここには無い**（2026-08-15 に外した）。
                   **エンジンごとの設定**になったので、入口は下の**エンジンの見出し**。
                   全エンジン共通の欄を 1 つ置くと、速いエンジンは多めに・重いエンジンは
                   1 本、という使い分けができない（複数を同時に走らせる意味が薄れる）。
                   ⚠️ **ここに戻さないこと**（同じ値の入口が 2 つになる）。 -->
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
            <!-- エンジンの結果と手順の境目（2026-08-15）。**高さは人が決める。**
                 ⚠️ **候補の本数で自動では動かさない** —— 動かすと、手で決めた高さが
                 解析のたびに上書きされる。書き換えるのは --analyze-engines-h
                 ただ 1 つで、余りは手順のリストがもらう。
                 ⚠️ **他の 2 本（評価値グラフ・解析の列）と操作の形を揃えること。** -->
            <div id="analyze-split" class="split-bar" role="separator" hidden
                 aria-orientation="horizontal" aria-label="解析結果の高さ" tabindex="0"
                 title="ドラッグで解析結果の高さを変えます（余りは手順に渡ります）。上下キーでも動きます"></div>
            <p id="analyze-status" class="note is-caution" hidden></p>
            <!-- 手順（Phase 5「手を進める UI」）。**盤の右の列の下半分。**
                 ⚠️ **高さを中身に依存させないこと**（中でスクロールさせる）。 -->
            <!-- 手順の見出しの行。**連続解析もここに置く**（2026-08-12）。
                 「1手戻す」と同じ高さに並べて、**手順に対する操作**であることを
                 位置で示す（下に並んでいるリストが対象）。

                 ⚠️ **解析の行に戻さないこと。** あちらは「今の局面」に対する操作の行。

                 ⚠️ **解析の行の「連続」（連続モード）とは別物。** あちらは
                 「手を進めるたびに今の局面を解析し直す」で、局面を動かすのは人。
                 こちらは**局面を動かすほうも自分でやる**。名前が似ているので、
                 片方を直すときにもう片方と混同しないこと。

                 ⚠️ **範囲の指定は無い**（2026-08-12 に手数の欄を外した）。
                 **今見ている局面から最後の手まで**を順に解析する。始点は
                 手順リストや評価値グラフを押して決める —— **どこから始めるかは
                 「今どこを見ているか」で既に決まっている**ので、同じことを
                 数字でもう一度言わせない。

                 ⚠️ **考える秒数が「無制限」だと使えない**（1 手目で止まったまま
                 次へ進めない）。理由は押せない側に出す。 -->
            <!-- 再読み込み（2026-08-13）。**URL から読み込んだときだけ出す。**
                 中継の .kif は 1 手進むたびに書き換わるので、同じ URL を
                 読み直して**手順を最新にする**のがこのボタン。

                 ⚠️ **入力タブから読み直させないこと** —— あちらを通ると
                 根ごと入れ替わるので**評価値が全部消える**。こちらは
                 食い違ったところから先だけを差し替える（判断は Go 側の
                 StudyService.ReloadKifu。**フロントで手順を突き合わせない**）。

                 ⚠️ **出す条件は StudyState.sourceUrl。** 入力タブの URL 欄の
                 中身で判断しないこと（欄はいつでも書き換えられるので、
                 **今の手順がどこから来たか**とは別物になる）。 -->
            <div class="study-move-head">
              <!-- ⚠️ **十字キーの上下で辿れることは title でしか言っていない**
                   （2026-08-18）。行に文字を足すと、そのぶん手順のリストが
                   短くなる（この列は縦の取り合いが厳しい）。 -->
              <span class="field-label" title="↑ で1手戻る／↓ で1手進む">手順</span>
              <!-- 取り直しは**「手順」の真横**（2026-08-15。以前は右側の
                   ghost-btn だった）。⚠️ **取り直す相手は「その手順」**なので、
                   見出しの隣に置いて位置で対象を示す（連続解析のボタンを
                   手順の見出しの行へ移したのと同じ考え方）。
                   ⚠️ **アイコンだけなので、意味は aria-label / title が持つ。**
                   文言は showStudy が URL つきで入れ替える。 -->
              <button id="study-reload" class="icon-btn" type="button" hidden
                      aria-label="棋譜を再読み込み">${iconMarkup(FiRefreshCw)}</button>
              <!-- ⚠️ **文言は syncBatchButton が入れる**（「x手目から解析」）。
                   ここに書いてあるのは、まだ局面が無いときの見た目だけ。
                   ⚠️ この markup は template literal の中なので、
                   コメントにバッククォートを使わないこと（文字列がそこで切れる）。 -->
              <button id="analyze-batch-run" class="ghost-btn" type="button">連続解析</button>
            </div>
            <!-- ⚠️ **「1手戻す」は無くなった**（2026-08-13。分岐を入れる前段）。
                 手順を短くするのは**リストの手を右クリック**するだけで、
                 消えるのは**その手とその先**。ボタンは「今どこを見ているか」に
                 依存していて、戻って見ている最中に押すと何が消えるのか
                 画面から読めなかった。**ボタンを戻さないこと。** -->
            <div id="study-moves" class="study-moves"></div>
            <p id="study-move-status" class="note is-caution" hidden></p>
`;
  const q = <T extends Element>(sel: string) => host.querySelector<T>(sel)!;

  // この面が今使われているか（**切り離すと、ドック側は隠れたまま生き続ける**）。
  // ⚠️ **false のあいだは自動解析をしないこと** —— 両方が起こし合う。
  let active = true;

  // ⚠️ **右端へ寄せるのは `margin-left: auto`**（`.analyze-row-action`）。
  // 解析の行は `flex-wrap` するので、**幅が足りなければ次の行へ回る**。
  if (opts.action) {
    opts.action.classList.add("analyze-row-action");
    q<HTMLElement>("#analyze-row").appendChild(opts.action);
  }

  const studyWarnings = q<HTMLUListElement>("#study-warnings");
  const studyMoves = q<HTMLDivElement>("#study-moves");
  const studyReload = q<HTMLButtonElement>("#study-reload");
  const studyMoveStatus = q<HTMLParagraphElement>("#study-move-status");

  // fillWarnings は警告のリストを書き換える（`mainscreen.ts` にも同じものがある）。
  // ⚠️ **件数で高さを変えないこと**（出たり消えたりするたびに下が動く）。
  const fillWarnings = (ul: HTMLUListElement, list: string[]) => {
    ul.replaceChildren();
    for (const w of list) {
      const li = document.createElement("li");
      li.textContent = w;
      ul.appendChild(li);
    }
    ul.hidden = list.length === 0;
  };

  // setStatus は手順の下の 1 行（**空なら消す**）。
  // ⚠️ **出しっぱなしにしないこと** —— 前の操作の理由が残っていると、
  // 今の操作が失敗したように見える。
  const setStatus = (message: string, error = false) => {
    studyMoveStatus.textContent = message;
    studyMoveStatus.hidden = message === "";
    studyMoveStatus.classList.toggle("is-error", error && message !== "");
  };

  // pushScores は**エンジンごとの最善手**を呼び出し側へ渡す（勝率バーの材料）。
  // ⚠️ **候補手の一覧を更新するのと同じ 1 か所から呼ぶこと** ——
  // 別々に更新すると、バーと評価値の数字が食い違う。
  const pushScores = () => {
    onScores(
      [...engineCards.values()].map((c) => ({
        id: c.id,
        label: c.label,
        winRate: c.best ? c.best.score.winRate : null,
        scoreLabel: c.best ? c.best.score.label : "",
      })),
    );
  };

  const analyzeRow = q<HTMLDivElement>("#analyze-row")!;
  const analyzeRun = q<HTMLButtonElement>("#analyze-run")!;
  const analyzeSeconds = q<HTMLSelectElement>("#analyze-seconds")!;
  const analyzeContinuous = q<HTMLInputElement>("#analyze-continuous")!;
  const analyzeEnginesBox = q<HTMLDivElement>("#analyze-engines")!;
  const analyzeMeta = q<HTMLElement>("#analyze-meta")!;
  const analyzeHint = q<HTMLElement>("#analyze-hint")!;
  const analyzeStatus = q<HTMLParagraphElement>("#analyze-status")!;
  const batchRun = q<HTMLButtonElement>("#analyze-batch-run")!;

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
    // multiPv はこのエンジンの候補手の本数。**一覧の高さを決めるのに要る**
    // （「全て表示」を切り替えたときに、枠を作り直さずに背を変えるため）。
    multiPv: number;
    // custom は label を人が付けたか。**名乗った名前を見出しに足すかの判断**
    // （`showEngineName`）。⚠️ **判断は Go 側の値を使うこと** —— 名前を付けたか
    // どうかは設定が持っている事実で、`label === name` かどうかとは別物。
    custom: boolean;
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
    // id は設定の登録 ID。**選択（下記）がどのエンジンの候補かを指す鍵。**
    id: string;
    // list は今この枠に出ている候補手。**選んだ手を足すときに読む。**
    //
    // ⚠️ **選んだ瞬間の写しを持たないこと。** 読み筋は深さが進むたびに伸びるので、
    // **足すのは押した時点の最新**でなければ「画面に出ているものと違う手順が入る」。
    list: AnalyzeLine[];
  }
  const engineCards = new Map<string, EngineCard>();

  // エンジンの色（評価値グラフの折れ線・見出しの色見本）。**2026-08-14。**
  //
  // ⚠️ **色はエンジンの登録に紐づく**（以前は「グラフに登場した順」で決めていた）。
  // 複数のエンジンを並べて読むのがこの画面の目的なので、**どの線がどのエンジンか**は
  // 見た目で覚えるもの。並べ替えたり 1 つ外したりするたびに色が入れ替わると、
  // **前に見ていた線と同じ色が別のエンジンを指す**ことになる。
  //
  // ⚠️ **決めるのは Go 側**（`SettingsService`。既定色の解決も向こう）。ここは
  // 受け取った写しで、**フロントに色の表を持たないこと**（2 か所に持つと、
  // 選べる色と既定で付く色が食い違う）。
  const engineColors = new Map<string, string>();
  // 設定タブで付けた名前（**色の丸のツールチップ**に出す。色だけでは読めない人が居る）。
  const engineNames = new Map<string, string>();
  // 候補手の本数（**解析に使うエンジンだけ**）。⚠️ **一覧に確保する高さを決めるのに要る**
  // ——本数はエンジンごとに違うので、**一番多いもの**に合わせる（`reserveLines`）。
  const engineMultiPV = new Map<string, number>();
  let engineColorOptions: EngineColorOption[] = [];
  // 設定に無いエンジンの色。**普通は通らない**（一覧に無いものは解析にも出ない）。
  const UNKNOWN_ENGINE_COLOR = "#b8c0d0";
  const colorOf = (engineId: string) => engineColors.get(engineId) ?? UNKNOWN_ENGINE_COLOR;
  // engineOf は手順リストの「誰が言った手か」の見た目（`study.ts` に渡す）。
  //
  // ⚠️ **色は折れ線と同じ 1 か所から引くこと**（違う色になったら、丸と線を
  // 結び付けられないので出す意味が無い）。
  const engineOf = (engineId: string) => ({
    color: colorOf(engineId),
    // 一覧から消えたエンジンでも丸は残る（手順のほうが長生きする）。**id を出す**
    // —— 空にすると「誰かが言った手」とだけ分かって誰かが分からない。
    label: engineNames.get(engineId) || engineId,
  });

  // 見出しの色見本を今の色に塗り直す。**枠を作り直さない**
  // （色を変えただけで候補手の一覧が消えると、何が起きたのか分からない）。
  const paintEngineColors = () => {
    for (const el of analyzeEnginesBox.querySelectorAll<HTMLElement>(".analyze-engine")) {
      const dot = el.querySelector<HTMLElement>(".analyze-engine-color");
      if (dot) {
        dot.style.background = colorOf(el.dataset.id ?? "");
      }
    }
  };

  // 色を選ばせる（見出しの色見本を押したとき）。**押した場所に出す**
  // （`window.confirm` を使わないのと同じ理由。popup.ts に寄せてある）。
  //
  // ⚠️ **入口をここに置いてあるのが要点。** 色を変えたくなるのは
  // **折れ線と候補手を見比べている最中**なので、設定タブまで行かせない。
  const askEngineColor = (id: string, x: number, y: number) => {
    const now = colorOf(id);
    openPopup(x, y, {
      label: "折れ線の色",
      items: [
        ...engineColorOptions.map((c) => ({
          label: c.label + (c.value === now ? "（今の色）" : ""),
          swatch: c.value,
          onPick: () => void setEngineColor(id, c.value),
        })),
        // ⚠️ **既定に戻す口を残すこと。** 既定色は登録順で決まるので、
        // 「元は何色だったか」をユーザーが覚えている必要が無いようにする。
        { label: "既定の色に戻す", onPick: () => void setEngineColor(id, "") },
      ],
    });
  };

  const setEngineColor = async (id: string, color: string) => {
    try {
      onSettings(await SettingsService.SetEngineColor(id, color));
      // ⚠️ **グラフも塗り直すこと**（描き直さないと前の色のまま）。
      // 点を取り直す必要は無いので、今持っているものをそのまま描き直す。
          } catch (err) {
      // ⚠️ **理由は解析タブに出すこと**（設定タブの行ではなく）。押したのは
      // こちらの画面なので、あちらに出しても読まれない。
      analyzeStatus.textContent = String(err instanceof Error ? err.message : err);
      analyzeStatus.hidden = false;
    }
  };

  // 選んでいる候補手（**枝にする相手**。2026-08-13）。
  //
  // ⚠️ **エンジンと順位で指す**（要素を覚えない）。候補の行は `analyze:info` が
  // 届くたびに作り直されるので、**要素を持つとすぐ迷子になる**。
  //
  // ⚠️ **1 つだけ。** エンジンをまたいで複数選べるようにしない —— 足すのは
  // 「この読み筋」という 1 本で、**どれを足したのかが分からなくなる**のが一番困る。
  let selectedLine: { engineId: string; rank: number } | null = null;
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
    // ⚠️ **選択も捨てる。** 候補は局面ごとの答えなので、局面が変わったあとも
    // 選ばれたままだと**別の局面の読み筋を足す**ことになる。
    selectedLine = null;
    // ⚠️ **盤の矢印も一緒に消すこと。** 別の局面の候補手が盤に残っていると、
    // **今の局面の読み筋として読まれる**（勝率バーを空に戻すのと同じ理由で、
    // むしろこちらのほうが盤の上にあるぶん目に入る）。
    onHint(null);
    engineCards.clear();
    analyzeEnginesBox.replaceChildren();
    // ⚠️ **勝率バーも一緒に空に戻すこと。** 別の局面の勝率が盤の上に残っていると、
    // **今の盤の形勢として読まれる**（評価値の一覧を消すのと同じ理由で、
    // むしろこちらのほうが目に入る位置にある）。
    // ⚠️ **枠は消さない**（`renderWinRate` が中立の見た目に戻すだけ）——
    // 盤の真上なので、出たり消えたりすると盤ごと動く。
    pushScores();
    analyzeMeta.textContent = "";
    analyzeStatus.hidden = true;
    analyzeStatus.textContent = "";
  };

  // linesPx は候補 n 本ぶんの一覧の高さ（px）。
  //
  // 1 行 28px + 行間 2px（**CSS の `--analyze-line-h` と揃えること**）。
  //
  // ⚠️ **頭打ちは「全て表示」で変わる**（2026-08-15。**エンジンごと**）:
  //
  //   切（既定）… **3 本ぶん**。溢れたぶんは枠の中でスクロール
  //   入        … **10 本ぶん**（＝選べる本数の上限。事実上「全部出す」）
  //
  // **既定を低くしてあるのは、枠が縦に伸びると同じ列の手順のリストが短くなるから。**
  // 伸ばすのは「今そうしたい」と言ったときだけにする。
  // ⚠️ **設定に無い本数（手で書いた 20 など）でもここで止まる** —— 止めないと
  // 枠が画面いっぱいまで伸びて、手順が見えなくなる。
  const LINES_CAP = 3;
  const LINES_CAP_ALL = 10;
  const linesPx = (n: number, all: boolean) => {
    const want = Math.min(Math.max(n || 1, 1), all ? LINES_CAP_ALL : LINES_CAP);
    return want * 28 + (want - 1) * 2;
  };

  // 「全て表示」の状態（エンジンごと）。**画面だけの状態**（`config.json` には
  // 持たない。視点・グラフの高さと同じ扱い）。
  //
  // ⚠️ **既定は入**（2026-08-15）。**読ませた候補は全部見えているのが素直**で、
  // 隠すほうが「今そうしたい」と言う操作。だから**入っていないことだけを覚える**
  // （Map に無い＝入）。
  //
  // ⚠️ **枠は解析のたびに作り直される**ので、**ここで覚えていないと 1 手ごとに
  // 既定へ戻る**（勝率バーで選んだエンジンを覚えているのと同じ理由）。
  const engineShowAll = new Map<string, boolean>();
  const showsAll = (id: string) => engineShowAll.get(id) ?? true;

  // sizeLines は**そのエンジンの本数**で一覧の高さを決める（2026-08-15）。
  //
  // ⚠️ **枠ごとに違ってよい。** 本数がエンジンごとになったので、一番多いものに
  // 揃えると**1 本しか出さないエンジンの下に 3 行ぶんの空白**が残る。
  // ⚠️ **1 つの枠の中では固定であることは変わらない** —— 候補の本数は深さごとに
  // 変わりうるので、届いた数で伸び縮みさせると**そのたびに画面が上下に動く**
  // （連続モードでは 1 手ごとに「消す → 起こす → 結果が届く」を繰り返す）。
  const sizeLines = (card: EngineCard) => {
    card.lines.style.height = `${linesPx(card.multiPv, showsAll(card.id))}px`;
  };

  // reserveLines は**枠がまだ無いとき**の一覧の高さを決める。
  //
  // ⚠️ **`--analyze-lines-h` は既定**（`.analyze-lines` の CSS が読む）。
  // 実際の高さは枠ごとに `sizeLines` が入れる。
  //
  // ⚠️ **枠全体（`--analyze-engines-h`）はここでは触らない**（2026-08-15）。
  // **あれはスプリットバーで人が決める値**になった —— 候補の本数で勝手に動くと、
  // **手で決めた高さが解析のたびに上書きされる**。
  //
  // ⚠️ **:root（documentElement）に入れること**（2026-08-12）。**カスタム
  // プロパティは下へしか継承しない**ので、枠の要素に入れると読めない側が出る。
  const reserveLines = () => {
    const counts = [...engineMultiPV.values()];
    // ⚠️ **既定（＝「全て表示」が入）で見積もること。** 枠の CSS の既定値
    // （`--analyze-card-h` 経由）がこれを読むので、切った状態で見積もると
    // **初回だけ枠が足りない**。
    document.documentElement.style.setProperty(
      "--analyze-lines-h", `${linesPx(Math.max(1, ...counts), true)}px`);
  };

  // ---- 解析結果と手順の境目（スプリットバー。2026-08-15）--------------------
  //
  // **書き換えるのは `--analyze-engines-h` ただ 1 つ**で、余りは手順のリストが
  // もらう（`.study-moves` は `flex: 1 1 0`）。評価値グラフ・解析の列のバーと
  // **同じ形**にしてある（掴む・上下キー・遅れて光る）。
  //
  // ⚠️ **候補の本数では動かさないこと**（2026-08-15 にそう決めた）。動かすと、
  // **手で決めた高さが解析のたびに上書きされる。**
  const analyzeSplit = q<HTMLDivElement>("#analyze-split")!;
  // 下限はカード 1 つぶん（**2 つ目のエンジンが見えなくても、1 つは読める**）。
  const ANALYZE_H_MIN = 60;
  let analyzeH = 0; // 0 = まだ人が決めていない（CSS の既定に任せる）

  // 上限は**手順のリストに残す最低限**から決める（列の高さは窓で変わるので、
  // px の定数ではなく**その場で測る**）。⚠️ **手順を 0 まで潰させないこと。**
  const analyzeMax = () => {
    const side = host.clientHeight;
    return Math.max(ANALYZE_H_MIN, (side > 0 ? side : window.innerHeight) - 160);
  };

  const setAnalyzeH = (px: number) => {
    const next = Math.min(Math.max(Math.round(px), ANALYZE_H_MIN), analyzeMax());
    if (next === analyzeH) {
      return;
    }
    analyzeH = next;
    // ⚠️ **:root に入れること**（`--analyze-card-h` 経由で他からも読まれる）。
    document.documentElement.style.setProperty("--analyze-engines-h", `${next}px`);
    analyzeSplit.setAttribute("aria-valuenow", String(next));
  };

  // ドラッグ。**下へ引くと解析結果が高くなる**（境目そのものを掴む感覚）。
  // ⚠️ **pointer capture を取ること** —— 掴んだままバーの外へ出るのが普通。
  analyzeSplit.addEventListener("pointerdown", (e) => {
    e.preventDefault();
    analyzeSplit.setPointerCapture(e.pointerId);
    analyzeSplit.classList.add("is-dragging");
    const startY = e.clientY;
    // ⚠️ **起点は「今の実寸」**（まだ人が決めていないときは CSS の既定なので、
    // 変数からは読めない）。測れば、どちらの経路でも掴んだ位置から動く。
    const startH = analyzeEnginesBox.clientHeight;
    const onMove = (ev: PointerEvent) => setAnalyzeH(startH + (ev.clientY - startY));
    const onUp = () => {
      analyzeSplit.classList.remove("is-dragging");
      analyzeSplit.removeEventListener("pointermove", onMove);
      analyzeSplit.removeEventListener("pointerup", onUp);
      analyzeSplit.removeEventListener("pointercancel", onUp);
    };
    analyzeSplit.addEventListener("pointermove", onMove);
    analyzeSplit.addEventListener("pointerup", onUp);
    analyzeSplit.addEventListener("pointercancel", onUp);
  });

  analyzeSplit.addEventListener("keydown", (e) => {
    const step = e.shiftKey ? 32 : 8;
    if (e.key === "ArrowUp") {
      e.preventDefault();
      setAnalyzeH((analyzeH || analyzeEnginesBox.clientHeight) - step);
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      setAnalyzeH((analyzeH || analyzeEnginesBox.clientHeight) + step);
    }
  });

  // 窓が低くなると上限も下がる。**はみ出したままにしないこと**
  // （⚠️ **決めていないうちは触らない** —— 0 を書き込むと下限に張り付く）。
  window.addEventListener("resize", () => {
    if (analyzeH > 0) {
      setAnalyzeH(analyzeH);
    }
  });
  // ⚠️ **取り直すのは `showSettings`**（本数は設定の一部になったので）。
  // ここでの 1 回は、設定が届く前の初期値。
  reserveLines();

  // 考える秒数は**設定に持つ**（2026-08-15）。連続解析では「手数 × 秒数」が
  // そのまま待ち時間になるので、**起動のたびに選び直させない**。
  // ⚠️ **連続モードのチェックとは扱いが違う**（あちらはその場かぎりの操作）。
  //
  // ⚠️ **設定ファイルには選択肢に無い値も入りうる**（手で書けば 15 でも通る）。
  // **今の値が一覧に無ければ足すこと** —— 足さないと `select.value` が空になり、
  // **選び直すまで画面が嘘をつく**（MultiPV と同じ話）。
  const showAnalyzeSeconds = (sec: number) => {
    const v = String(Math.max(0, sec));
    if (![...analyzeSeconds.options].some((o) => o.value === v)) {
      const opt = document.createElement("option");
      opt.value = v;
      opt.textContent = `${v}秒`;
      // 「無制限」（0）の手前に置く（**無制限は一番下**のままにする）。
      analyzeSeconds.insertBefore(opt, analyzeSeconds.options[analyzeSeconds.options.length - 1]);
    }
    analyzeSeconds.value = v;
    // ⚠️ **連続解析のボタンは秒数を出している**ので、一緒に描き直すこと。
    syncBatchButton();
  };

  analyzeSeconds.addEventListener("change", () => {
    void (async () => {
      try {
        onSettings(await SettingsService.SetAnalyzeSeconds(Number(analyzeSeconds.value) || 0));
      } catch (err) {
        analyzeStatus.textContent = String(err instanceof Error ? err.message : err);
        analyzeStatus.hidden = false;
      }
    })();
  });

  // 解析に参加するエンジンぶんの枠を先に作る。
  //
  // **起動を待っているあいだも見出しを出す**（エンジンによっては評価関数の読み込みで
  // 数秒かかる）。何も出ないと、走っていないのか遅いのかが分からない。
  const buildEngineCards = (
    engines: { id: string; label: string; name: string; custom: boolean; multiPv: number }[],
  ) => {
    // ⚠️ **選択は解析ごとに捨てる**（勝率バーのエンジンとは扱いが違う）。
    // あちらは「どのエンジンを見たいか」という好みなので残すが、こちらは
    // **その局面のその読み筋**を指しているので、持ち越すと中身が別物になる。
    selectedLine = null;
    // ⚠️ **盤の矢印も一緒に消す**（選択を捨てたのに矢印だけ残ると、
    // どの行の手なのかを指すものが画面から消える）。
    onHint(null);
    engineCards.clear();
    analyzeEnginesBox.replaceChildren();
    // ⚠️ **どのエンジンをバーに出すかは呼び出し側が持つ**（2026-09-08）。
    // 盤の上のバーは別の窓にありうるので、ここでは選択を持たない。
    for (const e of engines) {
      const card = document.createElement("section");
      card.className = "analyze-engine";
      card.dataset.id = e.id;
      card.innerHTML = `
        <div class="analyze-engine-head">
          <!-- 折れ線の色（2026-08-14）。**押すと変えられる。**
               ⚠️ **入口をここに置いてあるのが要点** —— 色を変えたくなるのは
               評価値グラフと候補手を見比べている最中なので、設定タブまで
               行かせない。色そのものは**エンジンの登録に紐づく**（並べ替えても
               入れ替わらない）。 -->
          <button class="analyze-engine-color" type="button"
                  title="評価値グラフの折れ線の色を変えます"
                  aria-label="折れ線の色を変える"></button>
          <span class="analyze-engine-name"></span>
          <!-- 候補手の本数（MultiPV。2026-08-15 にここへ移した）。
               ⚠️ **エンジンごとの設定**（速いエンジンは多めに、重いエンジンは 1 本）。
               本数を変えたくなるのは**候補手を読んでいる最中**なので、
               設定タブまで行かせない（折れ線の色と同じ考え方）。
               ⚠️ 対応していないエンジンでは 1 本のまま（異常ではない）。 -->
          <select class="analyze-engine-multipv"
                  aria-label="候補手の本数"
                  title="候補手を何本出させるか（MultiPV）。このエンジンの設定として保存します"></select>
          <!-- 全て表示（2026-08-15）。⚠️ **本数とは別物** —— あちらは
               「何本読ませるか」、こちらは**この枠に何本ぶんの高さを取るか**。
               ⚠️ **エンジンごと**（1 本しか出さないエンジンの枠まで伸ばさない）。
               切のときは 3 件まで（溢れたら枠の中でスクロール）。 -->
          <label class="analyze-engine-all"
                 title="このエンジンの候補手を全部出します（切ると 3 件まで。溢れたぶんは中でスクロール）">
            <input class="analyze-engine-all-input" type="checkbox" />
            <span>全て表示</span>
          </label>
          <span class="analyze-engine-meta note"></span>
        </div>
        <ol class="analyze-lines"></ol>
        <p class="analyze-engine-error note is-caution" hidden></p>
      `;
      const entry: EngineCard = {
        label: e.label,
        multiPv: e.multiPv,
        custom: e.custom,
        pending: true,
        startupMs: 0,
        reused: false,
        name: card.querySelector<HTMLElement>(".analyze-engine-name")!,
        meta: card.querySelector<HTMLElement>(".analyze-engine-meta")!,
        lines: card.querySelector<HTMLOListElement>(".analyze-lines")!,
        error: card.querySelector<HTMLElement>(".analyze-engine-error")!,
        best: null,
        id: e.id,
        list: [],
      };
      // 見出しは**設定タブで付けた名前**。エンジンが名乗る名前（`id name`）は
      // 繋いで初めて分かるので、届いたら括弧で足す（下の showEngineName）。
      showEngineName(entry, e.name ?? "");
      entry.meta.textContent = "エンジンを起動しています…";
      const dot = card.querySelector<HTMLButtonElement>(".analyze-engine-color")!;
      dot.addEventListener("click", (ev) => askEngineColor(e.id, ev.clientX, ev.clientY));
      fillMultiPV(card.querySelector<HTMLSelectElement>(".analyze-engine-multipv")!, e);
      // 「全て表示」。⚠️ **エンジンごと**で、**解析をまたいで残す**
      // （枠は 1 手ごとに作り直されるので、覚えていないと毎回切に戻る）。
      const all = card.querySelector<HTMLInputElement>(".analyze-engine-all-input")!;
      all.checked = showsAll(e.id);
      all.addEventListener("change", () => {
        engineShowAll.set(e.id, all.checked);
        // ⚠️ **枠を作り直さないこと**（読んでいた候補が消える）。背だけ変える。
        sizeLines(entry);
      });
      // ⚠️ **高さはこの枠の本数で決める**（他のエンジンに揃えない。2026-08-15）。
      sizeLines(entry);
      engineCards.set(e.id, entry);
      analyzeEnginesBox.appendChild(card);
    }
    // ⚠️ **作り直したら塗り直すこと**（連続モードでは 1 手ごとにここを通る）。
    paintEngineColors();
    // 起動を待つあいだの見た目（中立）に戻す。**押せるかどうかもここで決まる。**
    pushScores();
  };

  // 候補手の本数の選択肢（**1〜10**）。
  //
  // ⚠️ **設定ファイルにはこれ以外の値も入りうる**（手で書けば 20 でも通る）ので、
  // **今の値が一覧に無ければ足す** —— 足さないと `select.value` が空になり、
  // **選び直すまで画面が嘘をつく**（設定は 20 なのに 1 に見える）。
  const MULTIPV_CHOICES = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10];

  const fillMultiPV = (sel: HTMLSelectElement, e: { id: string; multiPv: number }) => {
    const now = Math.max(1, e.multiPv || 1);
    const values = MULTIPV_CHOICES.includes(now)
      ? MULTIPV_CHOICES
      : [...MULTIPV_CHOICES, now].sort((a, b) => a - b);
    sel.replaceChildren();
    for (const v of values) {
      const opt = document.createElement("option");
      opt.value = String(v);
      opt.textContent = `候補 ${v}`;
      sel.appendChild(opt);
    }
    sel.value = String(now);
    sel.addEventListener("change", () => {
      void setEngineMultiPV(e.id, Number(sel.value) || 1);
    });
  };

  // 本数を変えたら**その場で保存し、走っていれば解析し直す。**
  //
  // ⚠️ **やり直さないと「選んだのに増えない」**（本数は次の `go` から効くので、
  // 走っている探索は最後まで今の本数のまま）。深さは失うが、
  // **候補を増やしたのは今すぐ見たいから**なので、待たせるほうがおかしい。
  const setEngineMultiPV = async (id: string, n: number) => {
    try {
      onSettings(await SettingsService.SetEngineMultiPV(id, n));
    } catch (err) {
      // ⚠️ **理由は解析タブに出す**（押したのはこちらの画面。設定タブの行に
      // 出しても読まれない）。`setEngineColor` と同じ。
      analyzeStatus.textContent = String(err instanceof Error ? err.message : err);
      analyzeStatus.hidden = false;
      return;
    }
    if (analyzeRunning) {
      await startAnalyze();
      return;
    }
    // 走っていないときは枠を作り直さないので、**その枠の高さだけここで直す**
    // （結果が出たまま残っているので、作り直すと読んでいたものが消える）。
    // ⚠️ **枠全体の取り分は `showSettings` が `reserveLines` で取り直し済み。**
    const card = engineCards.get(id);
    if (card) {
      card.multiPv = n;
      sizeLines(card);
    }
  };

  // エンジンが名乗った名前を見出しに足す。**設定の名前は消さない**
  // （同じ exe を option 違いで 2 つ登録していると、名乗る名前だけでは区別が付かない）。
  //
  // ⚠️ **人が名前を付けていたら足さない**（2026-08-15）。そう呼びたくて付けた名前
  // なので、**横に別の名前を並べる理由が無い**（prokishi 越しだと `id name` に
  // プラグイン名まで並んで長い）。付けていないときの `label` は既定の解決結果
  // （ファイル名 /「同梱エンジン」）でしかないので、**名乗った名前のほうが情報がある**。
  //
  // ⚠️ **足さない場合も `title` には残すこと** —— 何を名乗ったかは繋いで初めて
  // 分かる情報で、**設定が正しい相手に繋がっているかの唯一の手掛かり**になる。
  const showEngineName = (card: EngineCard, name: string) => {
    const add = name && name !== card.label && !card.custom;
    card.name.textContent = add ? `${card.label}（${name}）` : card.label;
    card.name.title = name ? `${card.label} / エンジンの名乗り: ${name}` : card.label;
  };

  // paintSelection は選んでいる候補だけを光らせる（**作り直さない**）。
  //
  // ⚠️ **全部の枠を見ること。** 選択は 1 つだけなので、**他のエンジンの行から
  // 外す**のもここの仕事（別々に消すと、2 本選ばれているように見える）。
  const paintSelection = () => {
    for (const [id, card] of engineCards) {
      for (const li of card.lines.querySelectorAll<HTMLElement>(".analyze-line")) {
        const rank = Number(li.dataset.rank ?? "0");
        li.classList.toggle(
          "is-selected",
          !!selectedLine && selectedLine.engineId === id && selectedLine.rank === rank,
        );
      }
    }
    // ⚠️ **選んだ候補の 1 手目を盤にも出す**（2026-08-14。移動元 → 移動先の矢印）。
    // **光っている行と盤の矢印は必ず同じ手**でなければならないので、
    // **更新はここ 1 か所**にしてある（別々に呼ぶと食い違う）。
    onHint(selectedMoves()[0] ?? null);
  };

  // selectLine は候補手を選ぶ（**同じものをもう一度押したら外す**）。
  //
  // ⚠️ **選ぶだけで、指しも足しもしない。** 足すのは右クリックの「手順を追加」で、
  // **押しただけで手順が伸びない**というこの一覧の約束は変わらない。
  const selectLine = (engineId: string, rank: number) => {
    const same = selectedLine?.engineId === engineId && selectedLine.rank === rank;
    selectedLine = same ? null : { engineId, rank };
    paintSelection();
  };

  // selectedMoves は選んでいる候補の**今の**読み筋（無ければ空）。
  const selectedMoves = (): string[] => {
    if (!selectedLine) {
      return [];
    }
    const card = engineCards.get(selectedLine.engineId);
    return card?.list.find((l) => l.rank === selectedLine?.rank)?.moves ?? [];
  };

  const showAnalyzeProgress = (card: EngineCard, p: AnalyzeProgress["progress"]) => {
    const lines = p.lines ?? [];
    // 盤の上の勝率バーは**最善手（順位 1）の評価値**で描く（評価値グラフに
    // 残すのと同じ値）。⚠️ **候補手の一覧と同じ 1 か所で更新すること。**
    // **値はエンジンごとに覚え、描くのは今選ばれている 1 つだけ**（押すと変わる）。
    card.best = lines[0] ?? null;
    // ⚠️ **選んだ相手を引くのに要る**（読み筋は深さが進むと伸びるので、
    // **足すのは押した時点の最新**）。
    card.list = lines;
    pushScores();
    card.lines.replaceChildren();
    for (const [i, l] of lines.entries()) {
      const li = document.createElement("li");
      // ⚠️ **2 番手以降は一段小さく出す**（`is-sub`）。最善手と同じ大きさで
      // 並べると、どれが 1 番手なのかが順番でしか分からない。
      // 小さくするのは**評価値と次の 1 手だけ** —— その先の読み筋はもともと
      // 小さいので、そちらまで変えると 1 番手の行と揃わなくなる。
      li.className = i === 0 ? "analyze-line" : "analyze-line is-sub";
      // ⚠️ **順位を持たせること**（選択は「エンジン + 順位」で指す。要素は
      // `analyze:info` のたびに作り直されるので覚えられない）。
      li.dataset.rank = String(l.rank);
      li.classList.toggle(
        "is-selected",
        selectedLine?.engineId === card.id && selectedLine.rank === l.rank,
      );

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

      // ⚠️ **左クリックは「選ぶ」だけ**（2026-08-13）。**指さない・足さない。**
      //
      // ここに並んでいるのは**エンジンが読んだ枝**であって、本譜（＝実際に現れた
      // 指し手）ではない。**押しただけで手順が伸びる**と、枝と本譜の区別が
      // 曖昧になる（2026-08-12 に「押すと指す」を外したのはそのため）。
      // **辿るのは盤の上で駒を動かす操作。**
      //
      // **選ぶ意味は「これを枝にする」** —— 選んでから右クリックで足す。
      // ⚠️ **もう一度押したら外れること**（選びっぱなしにさせない）。
      li.addEventListener("click", () => selectLine(card.id, l.rank));
      //
      // ⚠️ **右クリックで「手順を追加」**（2026-08-13）。読み筋を**枝として**
      // 木に足す —— 指すのではないので、**今見ている局面は動かない**
      // （動くと走っている解析が別の局面のものになり、候補を続けて足せない）。
      // ⚠️ **候補が本譜と同じ手なら枝を増やさず、食い違うところで枝になる。**
      // その判断は Go 側（`StudyService.AddLine`）で、**フロントで手順を
      // 突き合わせないこと。**
      li.addEventListener("contextmenu", (e) => {
        // ⚠️ **webview の既定メニューを止める**（手順リスト・訂正の盤と同じ）。
        e.preventDefault();
        // ⚠️ **右クリックした行を選んでおくこと。** 別の行が選ばれたまま
        // メニューが出ると、**光っている行と足す行が食い違う。**
        if (selectedLine?.engineId !== card.id || selectedLine.rank !== l.rank) {
          selectedLine = { engineId: card.id, rank: l.rank };
          paintSelection();
        }
        // ⚠️ **足すのは「今選ばれている行の、今の読み筋」**（`selectedMoves`）。
        // 閉じ込めた `l` を使わないこと —— あれは**この行を描いた時点**の写しで、
        // 読み筋は深さが進むと伸びるので、**画面に出ているものと違う手順が入る。**
        const usis = selectedMoves();
        if (usis.length === 0) {
          return;
        }
        openPopup(e.clientX, e.clientY, {
          label: "候補手",
          items: [{
            label: `手順を追加（${usis.length}手）`,
            kind: "primary",
            onPick: () => void addLineToStudy(card.id, usis),
          }],
        });
      });
      li.title =
        "クリックで選択（その手を盤に矢印で出します）／" +
        "右クリックでこの読み筋を枝として手順に足せます";
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
    // **その場合は何も書かない**（2026-08-15。以前は「接続を使い回し」と出していたが、
    // **2 回目以降は毎回そうなる**ので、常に出ている文字になっていた）。
    // ⚠️ **区別そのものは捨てない** —— ツールチップには残す。
    if (card.startupMs > 0) {
      parts.push(`起動 ${(card.startupMs / 1000).toFixed(1)} 秒`);
    }
    card.meta.textContent = parts.join(" / ");
    card.meta.title = card.reused
      ? `${parts.join(" / ")}（繋ぎっぱなしの接続を使い回したので、起動を払っていません）`
      : parts.join(" / ");
    // ⚠️ **深さが進むと読み筋は伸び、1 手目も変わりうる。** 盤に出している矢印は
    // **今の読み筋の 1 手目**でなければ、光っている行と食い違う。
    onHint(selectedMoves()[0] ?? null);
  };

  // ---- 連続解析（旧「全て解析」。2026-08-12）-------------------------------
  //
  // **今見ている局面から最後の手までを、順にまとめて解析する。** 中身は
  // 「手順リストを 1 つずつ押しては、考える秒数だけ待つ」の繰り返しで、
  // **押す操作を人がやらなくてよくなるだけ**。棋譜を読み込んだ直後に一度かけると、
  // 評価値グラフが全部埋まる。
  //
  // ⚠️ **ボタンは手順の見出しの行にある**（解析の行ではない）。**解析する対象が
  // 「今の局面」ではなく「そこに書いてある手順」**なので、「1手戻す」と同じ高さに
  // 置いて、位置で対象を示している。
  //
  // ⚠️ **範囲は指定しない**（2026-08-12 に手数の欄を外した）。**今見ている局面から
  // 最後の手まで**を順に解析する。始点を決めるのは手順リストや評価値グラフを押す操作で、
  // **「どこから始めるか」は「今どこを見ているか」で既に決まっている** ——
  // 同じことを数字でもう一度言わせない（2 か所に持つと食い違う）。
  //
  // ⚠️ **解析の行の「連続」（連続モード。`analyzeContinuous`）とは別物。**
  // あちらは「手を進めるたびに今の局面を解析し直す」で、**局面を動かすのは人**。
  // こちらは**局面を動かすほうも自分でやる**（`GoTo` を順に押していく）。
  // 名前が似ているので、片方を直すときにもう片方と混同しないこと。
  //
  // ⚠️ **並べて走らせない。** 1 局面ずつ順に解析する —— エンジンのプロセスは
  // 1 回の解析のあいだだけ生きる作りなので、まとめて起こすと**手数ぶんの
  // プロセスが同時に立つ**（`USI_Hash` は GB 級になりうる）。
  //
  // ⚠️ **進めるのは「全部のエンジンが終わったら」**（`syncAnalyzeRunning`）。
  // 1 つ終わっただけで次へ行くと、残りのエンジンの結果が次の局面の裏で届く。
  //
  // **ここが順番を決めているだけで、評価値の記録は普段と同じ経路**（Go 側の
  // `recordEval`）。⚠️ **連続解析だけの特別な記録の道を作らないこと。**

  // 解析する最後の手数。**-1 なら走っていない。**
  let batchLast = -1;
  // 次に解析する手数。
  let batchNext = -1;
  // 次へ進んでいる最中か（done はエンジンの数だけ届くので、二重に進めない）。
  let batchStepping = false;
  // 今の経路の節点 id（`StudyState.line`）。**連続解析が次に進む先はここから取る。**
  //
  // ⚠️ **手数から `GoTo` の引数を作らないこと** —— 枝が入ってからは
  // 「手数 → 局面」が一意に決まらない。
  let studyLine: number[] = [];
  // 今見ている局面の棋譜の手数（`StudyState` 由来）。
  //
  // ⚠️ **`studyPly` は根からの手数**（＝`studyLine` の添字）で、`studyFirst` を
  // 足すと棋譜の手数になる。**連続解析の始点はこれ** —— カーソル位置がそのまま始点。
  // ⚠️ **`GoTo` に渡すのは手数ではなく `studyLine[ply]`**（節点の id）。
  let studyFirst = 0;
  let studyMoveCount = 0;
  let studyPly = 0;

  const batchActive = () => batchLast >= 0;

  // 連続解析を始められない理由。**空なら押せる。**
  const batchBlockedReason = (): string => {
    if (!analyzeReady) {
      return "";
    }
    // ⚠️ **「無制限」では次へ進めない**（1 手目で考え続けて終わらない）。
    // 押せないことより、**なぜ押せないか**が出ているほうが大事。
    if ((Number(analyzeSeconds.value) || 0) <= 0) {
      return "連続解析は、考える秒数を決めてから（「無制限」では次の手へ進めません）";
    }
    // ⚠️ **手が 1 つも無くても押せてよい**（根の局面だけを解析するのは正当）。
    return "";
  };

  const syncBatchButton = () => {
    const blocked = batchBlockedReason();
    // ⚠️ **幕の出し入れはここ 1 か所**（走っているかの判定が 2 か所に散ると、
    // 幕だけ残って何も触れなくなる）。
    // ⚠️ **幕そのものは呼び出し側が持つ**（2026-09-08）——**盤も塞ぐ必要がある**
    // ので、切り離した窓の中だけに被せても足りない。
    onBusy(batchActive(), "連続解析中…");
    // ⚠️ **どこから始まるかをボタン自身に出す**（2026-08-15。以前は「連続解析」の
    // 一言で、始点はツールチップにしか無かった）。始点は**今どこを見ているか**で
    // 決まるので、**押す前に読めないと押せない**（範囲の欄を置かない代わりの表示）。
    //
    // **秒数も出す**（「3秒毎2手目から解析」）。⚠️ **これが何分かかるかを決めている**
    // ので、押す前に**手数と一緒に**読めるのが要る（残り時間は手数 × 秒数）。
    // ⚠️ **秒数が決まっていないときは書かない**（「無制限」。0 秒と書かないこと）。
    batchRun.textContent = batchActive()
      ? "停止"
      : `${batchSecondsLabel()}${batchFrom()}手目から解析`;
    batchRun.classList.toggle("is-active", batchActive());
    // 走っている最中は止められる。走っていないときは、解析できる局面かつ
    // 秒数が決まっているときだけ押せる。
    batchRun.disabled = !batchActive() && (!analyzeReady || blocked !== "");
    batchRun.title = batchActive()
      ? "連続解析を止めます（そこまでの評価値は残ります）"
      : blocked || batchRangeText();
  };

  // batchFrom は連続解析が**最初に考えさせる手**の手数。
  //
  // ⚠️ **今見ている手の「次の手」**（＝ +1）。解析は「この局面で次に何を指すか」を
  // 出すものなので、**1 手目を見ているなら答えは 2 手目**になる。
  // CLAUDE.md の「SFEN の手数と棋譜の手数は 1 つずれる」と同じ話で、
  // **「次は x 手目」と書けば、どちらの数え方かを聞くまでもなく決まる。**
  //
  // ⚠️ **手順リストのチップや幕の進み具合とは 1 つずれる**（あちらは
  // **指した手**の番号で、リストの位置と一致していないと辿れない）。
  // **どちらも正しい** —— ボタンは「何を考えさせるか」、リストは「どこに居るか」。
  const batchFrom = () => studyFirst + studyPly + 1;
  // batchTo は最後に考えさせる手の手数（同じ数え方）。
  const batchTo = () => studyFirst + studyMoveCount + 1;

  // batchSeconds は 1 手に使う秒数（0 なら「無制限」＝連続解析はできない）。
  const batchSeconds = () => Math.max(0, Number(analyzeSeconds.value) || 0);
  const batchSecondsLabel = () => {
    const s = batchSeconds();
    return s > 0 ? `${s}秒毎` : "";
  };

  const batchRangeText = () => {
    const per = batchSeconds() > 0 ? `1 手あたり ${batchSeconds()} 秒。` : "";
    return (
      per +
      (batchFrom() === batchTo()
        ? `${batchFrom()}手目を考えさせます（手順の最後に居ます）`
        : `${batchFrom()}手目から${batchTo()}手目まで、1 手ずつ順に考えさせます` +
          `（今見ている局面から手順の最後まで）` +
          `。全部で ${durationText((batchTo() - batchFrom() + 1) * batchSeconds())}ほど`)
    );
  };

  // durationText は秒数を「3分20秒」の形にする。**0 なら空。**
  const durationText = (sec: number) => {
    const n = Math.max(0, Math.ceil(sec));
    if (n < 60) {
      return `${n}秒`;
    }
    const m = Math.floor(n / 60);
    const s = n % 60;
    return s === 0 ? `${m}分` : `${m}分${s}秒`;
  };

  // 連続解析の残り時間（**単純な掛け算**）。
  //
  // ⚠️ **これは目安であって予測ではない。** 掛けているのは「残りの手数 × 1 手の秒数」
  // だけで、**エンジンの起動・局面の移動・解析の後始末は入っていない**ので、
  // 実際は少し長くかかる。⚠️ **だから「約」を外さないこと。**
  //
  // ⚠️ **1 手ごとに引き直す**（`batchEndAt` を毎手入れ替える）。通しで 1 回だけ
  // 計算すると、上のぶんの遅れが積もって**最後は大きく外れる**。
  let batchEndAt = 0;
  let batchTick = 0;

  // 幕に出す「今どこまで来たか」。⚠️ **手数は指した手の番号**（手順リストと
  // 揃える。ボタンの「x手目から」とは 1 つずれるが、あちらは考えさせる手の番号）。
  const showBatchProgress = (n: number) => {
    const left = batchEndAt > 0 ? (batchEndAt - Date.now()) / 1000 : 0;
    // ⚠️ **見積もりを過ぎても「終わった」と書かないこと**（まだ走っている）。
    const rest = left > 0 ? `／残り約 ${durationText(left)}` : "／まもなく終わります";
    onBusy(true, `連続解析中… ${n} / ${batchLast}手目${rest}`);
  };

  // startBatchTick は残り時間を 1 秒ごとに描き直す。
  //
  // ⚠️ **止めるときは必ず消すこと**（`stopBatch`）。残すと、幕を畳んだあとも
  // 動き続けて**存在しない要素を書き換える**。
  const startBatchTick = (n: number) => {
    stopBatchTick();
    showBatchProgress(n);
    batchTick = window.setInterval(() => showBatchProgress(n), 1000);
  };

  const stopBatchTick = () => {
    if (batchTick) {
      window.clearInterval(batchTick);
      batchTick = 0;
    }
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

  // stopBatch は連続解析をやめる。**そこまでの評価値は残る**（設計原則3）。
  const stopBatch = (message: string) => {
    batchLast = -1;
    batchNext = -1;
    batchStepping = false;
    // ⚠️ **残り時間の更新を止めること**（残すと、幕を畳んだあとも動き続ける）。
    stopBatchTick();
    batchEndAt = 0;
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
        stopBatch(`連続解析: ${batchLast}手目まで終わりました`);
        return;
      }
      const n = batchNext;
      batchNext++;
      // ⚠️ **手順リストを押すのと同じ経路**（`GoTo`。手順は消さない）。
      // 「解析のために局面を動かす」専用の道を作らないこと。
      // ⚠️ **渡すのは節点の id**（今の経路の ply 番目）。手数ではない。
      const id = studyLine[n - studyFirst];
      if (id === undefined) {
        stopBatch("連続解析: 手順の終わりまで来ました");
        return;
      }
      onState(await StudyService.GoTo(id));
      // ⚠️ **`showStudy` の中の自動解析（連続モード）には任せない。**
      // あちらは「まだ解析していない局面なら」という条件で動くので、
      // **連続モードが切ってあると 1 手目で止まる。**
      await startAnalyze();
      analyzeMeta.textContent = `連続解析: ${n}〜${batchLast}手目のうち ${n}手目`;
      // ⚠️ **幕にも出すこと。** 下の行は幕越しで読みにくいので、
      // **どこまで進んだか**が分からないと、止めるかどうかを判断できない。
      //
      // 残り時間は**この手を含めた残り手数 × 1 手の秒数**（単純な掛け算）。
      // ⚠️ **1 手ごとに引き直す** —— 通しで 1 回だけ計算すると、起動や移動のぶんの
      // 遅れが積もって最後は大きく外れる。
      batchEndAt = Date.now() + (batchLast - n + 1) * batchSeconds() * 1000;
      startBatchTick(n);
      if (!analyzeRunning) {
        // 起動そのものに失敗した（エンジンが選ばれていない等）。
        // **ここで止めないと、残りの手でも同じ失敗を繰り返す。**
        stopBatch("");
      }
    } catch (err) {
      stopBatch(`連続解析を止めました: ${String(err instanceof Error ? err.message : err)}`);
    } finally {
      batchStepping = false;
    }
  };

  // cancelBatch は連続解析をやめる（**幕のボタンと「停止」の共通の口**）。
  //
  // ⚠️ **走っている解析も止めること。** 順番を止めるだけだと、今の 1 手の解析が
  // 秒数いっぱい走り続ける（止めたのに止まっていないように見える）。
  const cancelBatch = () => {
    stopBatch("連続解析を止めました");
    if (analyzeRunning) {
      void AnalyzeService.Stop();
    }
  };

  // ⚠️ **Esc でも止められること。** 幕が出ているあいだ他に押せるものは無いので、
  // 取り違えようが無い（普段の Esc はダイアログを閉じる操作で、そちらは
  // 幕が出ているあいだ開かない）。
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && batchActive()) {
      e.preventDefault();
      cancelBatch();
    }
  });

  batchRun.addEventListener("click", () => {
    if (batchActive()) {
      cancelBatch();
      return;
    }
    // ⚠️ **始点は「今どこを見ているか」**（カーソル位置）。範囲を打ち込ませない
    // ——手順リストや評価値グラフを押して戻れば、そこが始点になる。
    // 数え方は**棋譜の手数**（評価値グラフの横軸と同じ）。
    batchNext = studyFirst + studyPly;
    batchLast = studyFirst + studyMoveCount;
    syncBatchButton();
    void batchStep();
  });
  analyzeSeconds.addEventListener("change", syncBatchButton);

  const startAnalyze = async () => {
    analyzeStatus.hidden = true;
    analyzeStatus.textContent = "";
    engineCards.clear();
    analyzeEnginesBox.replaceChildren();
    pushScores();
    analyzeMeta.textContent = "エンジンを起動しています…";
    reserveLines();
    try {
      // ⚠️ **候補手の本数は渡さない**（エンジンごとの設定を Go 側が読む）。
      const st = await AnalyzeService.Start(Number(analyzeSeconds.value) || 0);
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
    // ⚠️ **「停止」は連続解析も止めること。** 止めたのに次の手が始まったら、
    // 止める手段が無い（連続モードで止めたら止まったままにするのと同じ話）。
    if (batchActive()) {
      stopBatch("連続解析を止めました");
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
    // ⚠️ **使われていない面は手を出さない**（2026-09-08）。切り離すと
    // ドック側のペインは隠れたまま生きているので、**両方が起こし合う**。
    if (!active) {
      return;
    }
    // ⚠️ **連続解析の最中は手を出さない。** あちらが局面と解析の順番を握って
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


  // 手順（棋譜）のツリービュー（2026-09-08 に `study.ts` から切り出した）。
  //
  // ⚠️ **盤とは別のモジュール。** 切り出したのは、**解析の列を別ウィンドウへ
  // 出せるようにするには、盤の無い窓でも手順が出せる必要がある**から。
  // ⚠️ **描き直しは `showStudy` の 1 か所から**（盤と手順が食い違わないように）。
  const moveListUI = mountMoveList({
    panel: studyMoves,
    // ⚠️ **盤も一緒に描き直すこと。** 手順を押すと局面が変わるので、
    // **手順だけ描き直すと盤が前の局面のまま残る。**
    onState,
    onError: (message) => setStatus(message),
    // 手順に出す「誰が言った手か」の色と名前。**折れ線と同じ色を引く**。
    engineOf,
  });

  // ⚠️ **残す/捨てるの判断は Go 側**（`StudyService.ReloadKifu`）。手順の
  // 突き合わせをこちらでやると、評価値を捨てる側（Go）と 2 か所に散る。
  //
  // ⚠️ **結果は必ず出すこと。** 手順が伸びていなければ画面はほとんど変わらないので、
  // 何も出さないと**押しても効いていないように見える**。差し替えが起きたときは
  // 自分で指した手が消えているので、なおさら黙って済ませない。
  studyReload.addEventListener("click", () => {
    void (async () => {
      studyReload.disabled = true;
      studyMoveStatus.textContent = "棋譜を取り直しています…";
      studyMoveStatus.hidden = false;
      studyMoveStatus.classList.remove("is-error");
      try {
        const got = await StudyService.ReloadKifu();
        onState(got.state);
        // ⚠️ **note が空でないことをエラー扱いしないこと**（貼り付けと同じ）。
        // 途中で止まってもそこまでの手順は正しく、その局面は解析できる。
        studyMoveStatus.textContent = got.note ? `${got.summary}（${got.note}）` : got.summary;
        studyMoveStatus.hidden = false;
      } catch (err) {
        // **今の手順は壊れていない**（Go 側が組み立てが通ってから入れ替える）ので、
        // 理由だけ出して検討を続けられるようにする。
        studyMoveStatus.textContent =
          `棋譜を取り直せませんでした: ${String(err instanceof Error ? err.message : err)}`;
        studyMoveStatus.hidden = false;
        studyMoveStatus.classList.add("is-error");
      } finally {
        studyReload.disabled = false;
      }
    })();
  });

  // addLineToStudy は候補手の読み筋を**枝として**手順に足す（候補手の右クリック）。
  //
  // ⚠️ **足しても今見ている局面は動かない**（指すのではない）。走っている解析も
  // そのままなので、**候補を続けて足せる**。
  // engineId は**その読み筋を出したエンジン**（手順リストで色の丸になる）。
  const addLineToStudy = async (engineId: string, moves: string[]) => {
    try {
      const got = await StudyService.AddLine(engineId, moves);
      onState(got.state);
      // ⚠️ **足した読み筋はその場で畳む**（2026-08-18。以前は「分かれ道に
      // なったときだけ」だった）。読み筋は 15 手ぶら下がることがあるので、
      // **開いたまま積むと手順が読めない** —— 足した手 1 行 +「＋」にして、
      // **候補どうしを隣り合わせて比べられる形**にする。
      // ⚠️ **他の候補は畳み直さないこと**（開いて読んでいる最中に閉じる）。
      moveListUI.foldAdded(got.firstId);
      // ⚠️ **1 手も増えないことがある**（候補が本譜と同じ手順のとき）。
      // **それは失敗ではない**ので、そう分かる文言にする。
      studyMoveStatus.textContent = got.added > 0
        ? `${got.added}手を枝として足しました${got.note ? `（${got.note}）` : ""}`
        : "その読み筋は既に手順にあります";
      studyMoveStatus.classList.remove("is-error");
      studyMoveStatus.hidden = false;
    } catch (err) {
      studyMoveStatus.textContent =
        `手順に足せませんでした: ${String(err instanceof Error ? err.message : err)}`;
      studyMoveStatus.classList.add("is-error");
      studyMoveStatus.hidden = false;
    }
  };

  // ---- 外向き -------------------------------------------------------------
  return {
    render(next: StudyState | null) {
      const loaded = !!next?.loaded;
      studyLoaded = loaded;
      studySfen = next?.sfen ?? "";
      // 連続解析が進む範囲。**棋譜の手数で数える**（評価値グラフの横軸と同じ）。
      // ⚠️ **`Move.Number` も `Ply` も根からの手数**なので、起点を足す。
      studyFirst = next?.first ?? 0;
      // ⚠️ **数えるのは「今の経路」**（木の全部の手ではない）。
      studyLine = next?.line ?? [];
      studyMoveCount = Math.max(0, studyLine.length - 1);
      // ⚠️ **連続解析の始点になる。** 手順リストで戻れば、そこから解析し直せる。
      studyPly = next?.ply ?? 0;
      // 器ごと出し入れする（局面が無いときは分ける相手も居ない）。
      analyzeRow.hidden = !loaded;
      // ⚠️ **局面があるあいだは枠を出しっぱなしにする**（中身が空でも）。
      // 解析のたびに畳むと、連続モードでは**1 手ごとに盤が上下に跳ねる**。
      analyzeEnginesBox.hidden = !loaded;
      // ⚠️ **境目のバーも枠と一緒に出し入れすること**（分ける相手が居ないのに
      // 線だけ浮く）。
      analyzeSplit.hidden = !loaded;
      // ⚠️ **取り直せるかは Go 側が持っている**（URL から読んだときだけ埋まる）。
      // 入力タブの URL 欄を見ないこと —— あちらは打ち換えられる。
      const src = next?.sourceUrl ?? "";
      studyReload.hidden = !loaded || src === "";
      // ⚠️ **アイコンだけのボタンなので、title を空にしないこと**
      // （文字が無いぶん、何のボタンかはこれでしか読めない）。
      studyReload.title = src
        ? `棋譜を再読み込み: ${src} から取り直します（食い違ったところから先だけ` +
          `差し替え、それより前の解析結果はそのまま残ります）`
        : "棋譜を再読み込み";
      // ⚠️ **合法手が出せなくても局面は生きている**（設計原則3）。**理由は出すこと** ——
      // 何も出さないと「駒を押しても光らない」の理由が分からない。
      setStatus(next?.legalError ?? "");
      // 確定した局面でも警告は出うる（詰将棋のように「論理的におかしくても正しい」
      // 局面があるため）。変な評価値が出たときの手掛かりになる。
      fillWarnings(studyWarnings, next?.warnings ?? []);
      moveListUI.render(loaded ? next : null);
      // ⚠️ **局面が変わったら解析の可否と結果を追随させる。**
      if (!loaded) {
        // 空に戻った。**仕掛けた記録も捨てる** —— 同じ局面をもう一度採ったときに、
        // 連続モードなのに解析が始まらない、ということが起きる。
        autoAnalyzed = "";
      }
      if (analyzedSfen && studySfen !== analyzedSfen) {
        // 採り直した・手を進めたので、前の評価値は今の盤の値ではなくなった。
        // ⚠️ **走っているなら止めること** —— 表示を消すだけだと、**もう誰も
        // 読まない局面のためにエンジンのプロセスが生き続ける。**
        //
        // ⚠️ **使われていない面からは止めないこと**（2026-09-09）。`Stop` は
        // **今走っているものを止める**（世代を選べない）ので、切り離して隠れた側が
        // 持っている「走っている」を鵜呑みにすると、**切り離した先の解析を
        // 打ち切る**。打ち切られた解析も `analyze:done` を出すため、
        // **向こうの連続解析はそれを 1 手ぶんと読んで次へ進む。**
        if (analyzeRunning && active) {
          void AnalyzeService.Stop();
        }
        clearAnalyzeResult();
        analyzeSeq = -1;
        analyzeRunning = false;
      }
      syncAnalyzeButton();
      // ⚠️ **`analyzeReady` を見るので、押せるかどうかを決めたあとに呼ぶこと。**
      syncBatchButton();
      autoAnalyze();
    },
    setEngines(engines: EngineSettings[], colors: EngineColorOption[], seconds: number) {
      engineColors.clear();
      engineNames.clear();
      engineMultiPV.clear();
      for (const e of engines) {
        engineColors.set(e.id, e.color);
        engineNames.set(e.id, e.name);
        // ⚠️ **数えるのは「解析に使う」ものだけ**（外した登録の本数で高さを取ると、
        // 出てこない候補手のぶん枠が余る）。
        if (e.enabled) {
          engineMultiPV.set(e.id, e.multiPv);
        }
      }
      engineColorOptions = colors;
      // ⚠️ **候補手の高さもここで取り直す**（本数は設定の一部）。
      reserveLines();
      // 考える秒数。⚠️ **既定の解決は Go 側**（`Config.ThinkSeconds`）。
      showAnalyzeSeconds(seconds);
      paintEngineColors();
    },
    setStatus(message: string) {
      setStatus(message);
    },
    reveal() {
      // **解析タブに来たら（連続モードなら）そのまま解析を始める。**
      // まだ解析していない局面のときだけ動く（止めた解析を勝手に起こし直さない）。
      autoAnalyze();
    },
    release() {
      // ⚠️ **連続解析も止めること**（走ったままタブを離れると、見えないところで
      // 局面が動き続ける）。
      stopBatch("");
      void AnalyzeService.Stop();
    },
    cancelBatch() {
      cancelBatch();
    },
    stepping() {
      return batchActive();
    },
    setActive(on: boolean) {
      if (active === on) {
        return;
      }
      active = on;
      if (!on) {
        // ⚠️ **走っているものは止めること。** 使われなくなった面が
        // エンジンを掴んだままだと、切り離した先の解析と取り合う。
        stopBatch("");
      }
    },
  };
}
