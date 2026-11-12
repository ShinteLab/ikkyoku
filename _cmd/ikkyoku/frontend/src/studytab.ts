// 解析タブ（2026-11-13 に `mainscreen.ts` から切り出した）。
//
// **確定した局面の面。** 盤・駒台・SFEN・**勝率バー**と**対局者名**（黒地の右クリックで別々に隠せる）・
// 視点・十字キー・**連続解析の幕**・**投了**と、側の列（`sidepane.ts`）・評価値グラフ（`evalgraphpane.ts`）・
// 中継を追う（`followcontrol.ts`）・寸法と切り離し（`studylayout.ts`）を組むところ。中身の HTML もここ。
// **先に `_docs/ui/layout.md` を読むこと**（盤の大きさの式・投了のボタン・隠せる）。
//
// ⚠️ **受け渡しは `StudyTabOptions` / `StudyTabHandle` だけ**（`sidepane.ts` と同じ型）。
// ⚠️ **訂正タブとは別の局面**（Go 側も PositionService / StudyService に分かれている）。
// 繋ぐのは「この局面を解析する」の 1 回だけで、その口（`adoptToStudy`）は**両方のタブに触るので
// `mainscreen.ts` に残してある** —— こちらは `showStudy` と `setViewpoint` を貸すだけ。
// ⚠️ **局面の写しを持たない**（frontend/src/AGENTS.md の 5）。`studyRev` などは描き方の控えで、局面ではない。
// ⚠️ **局面があるかを他の要素の `hidden` から読まないこと**（`studyLoaded` の 1 つで持つ。切り離すと別の理由で `hidden` になる）。
import { Events } from "@wailsio/runtime";
import { FiExternalLink } from "react-icons/fi";
import {
  AnalyzeService,
  SettingsService,
  StudyService,
} from "../bindings/github.com/ShinteLab/ikkyoku/app";
import type { AppSettings, StudyState } from "../bindings/github.com/ShinteLab/ikkyoku/app/models";
import type { Stock } from "../bindings/github.com/ShinteLab/ikkyoku/position/models";
import { iconMarkup } from "./icon";
import { mountEvalPane } from "./evalgraphpane";
import { enableVeilDrag } from "./veildrag";
import type { EvalMode } from "./evalgraph";
import { mountSidePane, type EngineScore } from "./sidepane";
import { openPopup } from "./popup";
import { mountStudyBoard } from "./study";
import { mountStudyLayout } from "./studylayout";
import { mountFollowControl } from "./followcontrol";
import { isFollowing, onFollowingChange } from "./followguard";

export interface StudyTabOptions {
  // screen はメイン画面（黒地の右クリックを聞く相手と、寸法の部品を引く範囲。`studylayout.ts` は `#panel-study` を引く）。
  screen: HTMLElement;
  // isShown は解析タブを見ているか（十字キーは解析タブに居るときだけ。訂正タブにも盤がある）。
  isShown(): boolean;
  // frameVisible は枠が出ているか（中継を追う知らせの出し先。持ち主は `mainscreen.ts` の `frame:visible`）。
  frameVisible(): boolean;
  // onSettings は側の列で設定を書き換えたとき（設定タブを描き直す）。
  onSettings(settings: AppSettings): void;
}

export interface StudyTabHandle {
  // board は解析タブの盤（駒の字の王/玉・左馬を当てる相手。訂正タブの盤と両方に当てる）。
  board: HTMLElement;
  // showStudy は解析タブの表示一式を描く。⚠️ **タブを開いてから呼ぶこと**（呼び出し側。scrollIntoView もグリッドも隠れていると効かない）。
  showStudy(state: StudyState): void;
  // setStatus は解析の行に一言出す（空で消す。`sidePane.setStatus`）。
  setStatus(text: string): void;
  // setViewpoint は**解析タブの**視点（「自分がどちら側か」。新規対局の手番・採ったときの撮った盤の目線）。
  setViewpoint(black: boolean): void;
  // startFollow / publishFollow は中継を追う（`followcontrol.ts` の start / publish）。
  startFollow(): void;
  publishFollow(): void;
  // applySettings は設定のうち解析タブに効くもの（起動直後の形もここで決まる）。
  applySettings(settings: AppSettings): void;
  // leave は解析タブを離れるとき（連続解析を止め、エンジンを手放す）。
  leave(): void;
  // setTabShown は解析タブが開いているか（棋譜の自動更新を刻むか。切り離していても呼ぶ）。
  setTabShown(on: boolean): void;
  // shown は解析タブを開いた瞬間（隠れているあいだ測れなかったものを測り直す）。
  shown(): void;
}

export function mountStudyTab(root: HTMLElement, opts: StudyTabOptions): StudyTabHandle {
  // ⚠️ **この中の HTML コメントにバッククォートを書かないこと**（`mainscreen.ts` の同じ注記。`check-css.mjs` が見ている）。
  root.innerHTML = `
    <div class="board-area">
      <!-- 盤と駒台の配置。**後手の駒台は盤の左上、先手の駒台は右下**
           (訂正タブと同じ並び＝実際の将棋盤と同じ)。
           ⚠️ **こちらは読み取り専用**（駒を掴めない）。訂正タブの
           「足りない駒」はここには無い —— 未決が残った局面はそもそも
           このタブに来ない（StudyService.Adopt が断る）。 -->
      <div id="study-board-with-hands" class="board-with-hands">
        <!-- 勝率バー（2026-08-12）。**盤の真上・盤と同じ幅。**
             評価値を読むより先に「どちらがどれくらい良いか」が目に入るほうが速い
             （数字は下の候補手の行にある）。**左が後手（青）・右が先手（赤）**で、
             盤の向き（後手が上）とは別に、横棒として一般的な並びに合わせてある。

             ⚠️ **盤と同じグリッドの列に入れてあるのが要点**（board-area の
             直下に置くと、右の手順リストのぶん盤が左に寄っているので**盤と
             左右がずれる**）。列の幅＝盤の幅なので、width: 100% で揃う。

             勝率 = 1 / (1 + exp(-評価値 / ポナンザ定数))。定数は設定タブ（既定 1500）。
             ⚠️ **計算はフロントでしない**（Go 側の analyze.WinRate。式を 2 か所に
             持つと、定数を変えたときに片方だけ古い値で描く）。

             ⚠️ **出すのは 1 つのエンジンだけ**（押すと切り替わる）。
             **合成ではない** —— 複数走っていても、今見ているのはどれか 1 つ。
             ⚠️ **数字とエンジン名は出さない**（盤の上に文字を積むと、そのぶん
             盤が小さくなる）。値はカーソルを当てたときに出す。 -->
        <!-- 対局者（2026-08-13）。**グリッドの左右の列**（駒台の上の空きセル）に
             出す。勝率バーは盤と同じ幅のまま **1 行目の真ん中の列**なので、
             その左右がちょうど空いている。

             ⚠️ **左が後手・右が先手**（バーの左右と同じ並び）。
             ⚠️ **視点を反転しても入れ替えないこと** —— 勝率バーそのものが
             反転しないので、名前だけ動くと帯との対応が壊れる。
             **入れ替わるのは帯の左右を反転したときだけ**（2026-10-02。黒地の
             右クリック。applyWinRateFlip が帯と名前を一緒に動かす）。

             ⚠️ **名前が無いときの「後手」「先手」は表示側の既定。**
             Go 側は空を返す（名前が分かっているのか、既定を出しているだけ
             なのかを区別できるようにするため）。 -->
        <span id="player-name-white" class="player-name is-white" hidden></span>
        <div id="winrate-row" class="winrate-row" hidden>
          <button id="winrate-bar" class="winrate-bar is-empty" type="button">
            <span class="winrate-track">
              <span id="winrate-white" class="winrate-white"></span>
              <!-- 真ん中（互角）の目印。**これが無いと、どちらへ傾いているのかが
                   バーの端との比較でしか読めない。** -->
              <span class="winrate-mid" aria-hidden="true"></span>
            </span>
          </button>
        </div>
        <span id="player-name-black" class="player-name is-black" hidden></span>
        <!-- 視点（2026-08-13 に右の列から移した）。**表示だけの反転で、
             局面には効かない。**

             ⚠️ **置き場所はグリッドの右の列の真ん中のセル**（先手の対局者名の下・
             先手の駒台の上）。ここは元から空いているので、**盤の上下にも
             右の列にも行が増えない**（＝盤が小さくならない）。

             ⚠️ **視点を反転しても列を移さないこと。** 駒台は左右が入れ替わるが、
             **押すたびにボタンが飛ぶと、もう一度押すのに探すことになる**。
             どちら側が手前かはボタンの文字が言っている。

             中継の札（2026-10-08）。**視点のボタンの上に積む**（同じ空きのセル）。
             画面を撮って追っているあいだは赤、URL の棋譜を自動更新しているあいだは緑
             （ユーザー指定）。**両方入っていれば 2 枚とも出す**（別物なので 1 枚にまとめない）。
             ⚠️ **押せない**（入り切りは「中継を追う」と自動更新のアイコンが持つ）。 -->
        <div class="view-side">
          <span id="study-live-follow" class="live-badge is-follow" hidden
                title="中継の画面を撮って、進んだ手を本譜に足しています">中継を追跡中</span>
          <span id="study-live-kifu" class="live-badge is-kifu" hidden
                title="棋譜の URL を取り直して、新しい手に付いていきます">棋譜を自動更新中</span>
          <button id="study-flip" class="ghost-btn view-flip" type="button" hidden></button>
        </div>
        <div id="study-hand-white-slot" class="hand-slot" hidden></div>
        <div id="study-stage" class="board-stage">
          <!-- ⚠️ fluid: 置き場所の幅いっぱいに広げる（core/web の属性。2026-08-12）。
               これが無いと**盤は固有サイズの 560px 止まり**で、盤を主役に
               大きく見せられない。訂正タブの盤には付けていない（あちらは
               撮った画像を並べる面なので、盤だけ大きくしても仕方がない）。 -->
          <shogi-board id="study-board" fluid hidden></shogi-board>
        </div>
        <div id="study-hand-black-slot" class="hand-slot" hidden></div>
        <!-- 今見ている局面の SFEN（2026-08-14 に手順の列から**盤の下**へ戻した）。
             ⚠️ **盤の下に行を積んでも盤は小さくしない** —— 評価値グラフの箱を
             38px 詰めたぶんの空きに収める（.board-area は align-items: center
             なので、そこは元から遊んでいる）。**そのため 1 行に収めること**
             （4 行目 + gap 6px で 23px ほど。⚠️ **折り返させると盤が押し出される**）。

             ⚠️ **盤と同じグリッドの列に入れる**（勝率バーと同じ理由）。
             .board-area の直下に置くと、**右の列のぶん盤が左に寄っている**ので
             盤と左右がずれる。

             溢れたぶんは省略して title に出す（**選んでコピーはできる**）。 -->
        <div id="study-sfen-row" class="study-sfen-row" hidden>
          <!-- 中継を追う（2026-09-15）。**押しているあいだ撮り続けて、繋がるなら
               人に聞かずに手を足す。**

               ⚠️ **訂正タブへは飛ばさない。** 「訂正を挟むかどうか」を
               こちらが決めることになる（それは人が決めること）。
               **おかしくてもとにかく進み、違うと思ったら人が戻る。**

               ⚠️ **置き場所が盤の下なのは、行を増やさないため**（SFEN の行に
               相乗り）。盤の大きさは式で決まるので、行を足すと盤が縮む。 -->
          <button id="study-follow" class="ghost-btn is-tiny" type="button" hidden
                  aria-pressed="false"
                  title="中継の画面を撮り続けて、進んだ手を本譜に足します（枠を出しておいてください）">中継を追う</button>
          <span class="field-label">SFEN</span>
          <code id="study-sfen" class="sfen" title="今見ている局面">-</code>
        </div>
      </div>
      <!-- 縦のスプリットバー（2026-08-13）。**盤と、右の解析の列の境目。**
           ドラッグで盤の大きさを変える。⚠️ **変えているのは
           --study-side-min ただ 1 つ**で、盤の大きさはその式から付いてくる。

           ⚠️ **掴んだ時点で列の幅が固定される**（2026-09-12。--study-side-max）。
           そこから先は右へ引けば列が下限（300px）まで狭まり、**盤が伸びない
           ぶんは余白になって盤が中央へ寄る**（列は右端のまま）。
           判定は**実際の盤の幅を測って**行う（式の定数を JS に写さない）。 -->
      <div id="study-split" class="split-bar is-vertical" role="separator"
           aria-orientation="vertical" aria-label="盤と解析の列の幅" tabindex="0"
           title="ドラッグで盤と解析の列の幅を変えます。左右キーでも動きます">
        <!-- 解析の列の折り畳み（2026-08-13）。⚠️ **バーの上に置くのが要点** ——
             畳むと列ごと消えるので、列の中に置いたら戻す手段が無くなる
             （評価値グラフで見出しの行を残しているのと同じ話）。
             ⚠️ **押してもドラッグが始まらないようにすること**
             （pointerdown を止める）。 -->
        <button id="study-split-toggle" class="split-toggle" type="button"
                aria-expanded="true"></button>
      </div>
      <!-- 盤の右の列（2026-08-12 に作り替えた）。**解析のものは全部ここに入る**
           —— 解析の行・エンジンごとの結果・手順。⚠️ **SFEN は盤の下**
           （2026-08-14 に戻した）、**評価値グラフは盤の下・横いっぱい**。

           ⚠️ **盤の下に行を積まないこと。** 盤の大きさは
           「ウィンドウの高さ − 上下に積んだ行」で決まるので、**下に積むほど
           盤が小さくなる**（実際、エンジンの結果と評価値グラフを下に置いていた
           ときは 560px の盤すら出せていなかった）。横に置けば、盤の高さを
           削るのはウィンドウの高さだけになる。

           手順は「駒をクリック → 動かせる位置が光る → そこをクリックで指す」の
           結果が縦に積まれる列（棋譜ソフトと同じ並び）。⚠️ **チップを押すと
           戻れるが、手順は消えない**（進め直せる）。消えるのは戻った先で別の手を
           指したとき。「1手戻す」は指し間違えの取り消しなので**手順からも消す**。 -->
      <!-- ⚠️ 中身は sidepane.ts が組む（2026-09-08）。別ウィンドウへ
           切り離せるようにするため、器だけをここに置いて中身は向こうが持つ
           （切り離した窓には同じ中身が入る。片方だけ直せないように）。
           ⚠️ この中はテンプレートリテラルなので、コメントにも
           バッククォートを書かないこと（文字列がそこで終わる）。 -->
      <div id="study-side" class="study-side" hidden></div>
      <p id="study-placeholder" class="board-placeholder">
        訂正タブで「この局面を解析する」を押すと、ここに局面が出ます。
      </p>
    </div>
    <!-- 評価値グラフ（2026-08-12）。**手順の 1 手ごとの最善手の評価値**を
         折れ線にする。**「次善手を選んだらどう転ぶか」を辿った結果が
         どう転んだか**を見せる面で、この画面の目的そのもの。

         ⚠️ **エンジンごとに別の折れ線**（合成しない。平均も多数決も取らない）。
         ⚠️ **高さは CSS で固定すること**（--eval-graph-h）。連続モードでは
         1 手ごとに点が増えるので、中身で伸び縮みすると盤ごと画面が跳ねる。 -->
    <div id="eval-graph-row" class="eval-graph-row" hidden>
      <!-- スプリットバー（2026-08-13）。**盤とグラフの境目。**
           ドラッグでグラフの高さを変える。⚠️ **変えているのは
           --eval-graph-h ただ 1 つ**で、盤の大きさはその式から自動で
           付いてくる（--board-size がこれを引いている）。
           ⚠️ **一番下まで下げると畳む**（＝高さ 0）ので、
           「畳む」と「高さを変える」は**同じ 1 つの値**。 -->
      <div id="eval-graph-split" class="split-bar" role="separator"
           aria-orientation="horizontal" aria-label="評価値グラフの高さ" tabindex="0"
           title="ドラッグで高さを変えます（一番下まで下げると畳みます）。上下キーでも動きます">
        <!-- 折り畳み。**畳むと高さ 0** になり、そのぶん盤が大きくなる。
             ⚠️ **縦のバーと同じくバーの上に載せる**（2026-08-13 に見出しの行から
             移した）。畳んでも残るのはバーと見出しの行の両方だが、**戻す入口は
             バーの上に統一する** —— 2 本のバーで操作の形が違うと、どちらが
             どうだったかを覚えることになる。
             ⚠️ **押してもドラッグが始まらないようにすること**（pointerdown を止める）。 -->
        <button id="eval-graph-toggle" class="split-toggle" type="button"
                aria-expanded="true"></button>
      </div>
      <div class="eval-graph-head">
        <!-- 縦軸（2026-09-13）。**評価値と勝率は同じ点の別の読み方。**
             評価値は「どれくらい差が付いたか」、勝率は「その差がどれくらい
             勝ちに効くか」で、**同じ +500 でも序盤と終盤では意味が違う。**

             ⚠️ **見出しの札の代わりに置いてある** —— どれで見ているかが
             グラフの題そのもの。⚠️ **今どれかが読めること**（aria-pressed）:
             軸が変わると折れ線の形も変わるので、分からないと数字を読み違える。
             ⚠️ **記録は 1 つも変わらない**（点が評価値も勝率も持っている）。

             ⚠️ **「圧縮」は「評価値」の代わりではない。** 等間隔の目盛りには
             「傾きがそのまま点差の動き」という読み方があるので、**素の
             評価値グラフは残すこと。** ⚠️ **「勝率」もその代わりではない**
             （0 の近くを広げるために足したものではなく、勝率で見たい人の軸）。 -->
        <span id="eval-graph-mode" class="eval-graph-mode" role="group"
              aria-label="縦軸">
          <button type="button" data-mode="eval" class="is-on" aria-pressed="true"
                  title="評価値（センチポーン）そのまま。目盛りは等間隔で、±3000 で頭打ちです">評価値</button>
          <button type="button" data-mode="scaled" aria-pressed="false"
                  title="圧縮: ±500 までで上下半分、±3000（まぁ勝ち）までで 8 割、±30000（詰み・必至）までを端に畳みます。目盛りの数字は評価値のままです">圧縮</button>
          <button type="button" data-mode="winrate" aria-pressed="false"
                  title="先手の勝率（0〜100%）で見ます。評価値からの変換で、定数は設定タブ">勝率</button>
        </span>
        <!-- 横軸の範囲。**既定は「全て」＝ 指した手が全部見えている状態。**
             ⚠️ **1 から始まるとは限らない** —— 根は初期局面とは限らないので、
             撮った 40 手目の局面から始めたなら 40 手目から始まる。

             **絞るのはグラフの上をドラッグする**のが本筋で、選んだ範囲は
             そのまま下の欄に入る（＝見えている数字が今の範囲）。
             「1-150 の中のどこに居るか」で読みたいときは欄に直接書く。 -->
        <label class="eval-graph-all"
               title="全て: 指した手が全部見える範囲にします。外すと右の欄の手数がそのまま横軸になります">
          <input id="eval-graph-all" type="checkbox" checked />
          <span>全て</span>
        </label>
        <!-- 横軸の範囲の欄。⚠️ **「全て」のときも隠さないこと**（2026-09-10）——
             今どの範囲を見ているのかが画面から読めなくなる。「全て」のあいだは
             触れなくして、実際の範囲（根の手数〜最終手）を書き込む。
             ⚠️ **「自由入力」という札は要らない** —— チェックを外せば
             触れるようになる欄そのものが、そう言っている。 -->
        <span id="eval-graph-fields" class="eval-graph-fields">
          <input id="eval-graph-from" class="eval-graph-num" type="number"
                 min="0" max="999" step="1" value="1" title="左端の手数" />
          <span class="eval-graph-dash">-</span>
          <input id="eval-graph-to" class="eval-graph-num" type="number"
                 min="1" max="999" step="1" value="150" title="右端の手数" />
        </span>
        <!-- どの色がどのエンジンか。**グラフの中に重ねない**（目盛りと重なるうえ、
             折れ線の描ける範囲がそのぶん狭くなる）。 -->
        <span id="eval-graph-legend" class="eval-graph-legend"></span>
        <!-- 触った位置の中身。**ツールチップだけにしない**（点の上にぴったり
             乗せないと出ないので、線を目で追いながらは読めない）。 -->
        <span id="eval-graph-readout" class="note eval-graph-readout"></span>
        <!-- 切り離し（2026-09-09 にバーの上からここへ移した）。
             ⚠️ **切り離した窓の「ドックに戻す」と同じ場所**にすること ——
             出す/戻すが同じ位置にあると、どちらの状態でも探す場所が変わらない。
             ⚠️ 畳む（高さ 0）とは別物。畳むのは「今は見ない」で、
             こちらは別の窓で見る（盤の大きさに効かなくなる）。 -->
        <button id="eval-graph-detach" class="icon-btn" type="button"
                title="切り離す: 評価値グラフを別ウィンドウに出します（盤の大きさに効かなくなります）"
                aria-label="評価値グラフを切り離す"></button>
      </div>
      <div id="eval-graph" class="eval-graph"
           title="押すとその局面に戻ります（手順は消えません）。横にドラッグするとその範囲に絞ります。押したあとは ← / → で 1 手ずつ動かせます"></div>
    </div>
    <!-- 連続解析のあいだ被せる幕（2026-08-14）。**触れなくするのが目的。**
         連続解析は 1 手ずつ局面を動かしながら走るので、その最中に盤や
         手順を触ると**自分の操作と連続解析が同じ局面を取り合う**
         （押した手からまた解析が進んでいくように見える）。

         ⚠️ **止める口をこの中に置くこと。** 幕は下を全部塞ぐので、
         側の列の「停止」も押せなくなる。**出口が無い幕にしない。**
         ⚠️ **薄くすること** —— 下で盤と評価値グラフが 1 手ずつ進むのが
         見えていないと、待っているあいだ何が起きているのか分からない。 -->
    <div id="batch-veil" class="veil" hidden>
      <div class="veil-box" title="ドラッグで動かせます（盤や評価値グラフが隠れているとき）">
        <p id="batch-veil-note" class="veil-note">連続解析中…</p>
        <button id="batch-veil-cancel" class="veil-btn" type="button">解析をキャンセル</button>
      </div>
    </div>
  `;

  const { screen } = opts;

  // ---- エンジン解析（Phase 4）。**解析タブの中身。** --------------------------
  //
  // **確定した局面にだけかかる。** 確定していない局面はそもそも解析タブに来ない
  // （Go 側の StudyService.Adopt が断る）ので、以前あった「訂正中は押せない」という
  // 判定はフロントから消えた —— **タブを分けたことで構造上そこに手が届かない。**
  //
  // 反復深化なので**深さが 1 つ終わるたびに答えが更新される**。終わるまで黙って
  // いると数秒固まって見えるので、途中経過をそのまま出して育つ様子を見せる。
  //
  // ⚠️ **局面を採り直したら結果を消す。** 評価値は「その局面の」値で、盤が変わった
  // あとも残っていると別の局面の値を今の盤の評価として読ませることになる。
  // ---- 解析タブの盤 --------------------------------------------------------
  //
  // **訂正タブとは別の <shogi-board>。** 同じ盤を出し入れして使い回さないこと
  // （タブごとに別の局面が出ているのが正しい状態で、片方を動かしたらもう片方も
  // 動く、という作りにすると「今どちらの局面を見ているか」が分からなくなる）。
  //
  // ⚠️ **重ねるグリッドは訂正タブのものを流用しない**（別のタブの別の盤）。
  // ここのグリッド（`study.ts`）は**合法手だけ**を扱い、クリック 2 回で指す。
  const studyStage = root.querySelector<HTMLElement>("#study-stage")!;
  const studyBoard = root.querySelector<HTMLElement>("#study-board")!;
  const studyPlaceholder = root.querySelector<HTMLParagraphElement>("#study-placeholder")!;
  const studySfenOut = root.querySelector<HTMLElement>("#study-sfen")!;
  const studySfenRow = root.querySelector<HTMLElement>("#study-sfen-row")!;
  const studyFollowBtn = root.querySelector<HTMLButtonElement>("#study-follow")!;
  // 盤の脇の駒台（読み取り専用）。**訂正タブの駒台とは別物**で、
  // ドラッグの入口も「足りない駒」も持たない。
  const studyHandSlots = {
    black: root.querySelector<HTMLElement>("#study-hand-black-slot")!,
    white: root.querySelector<HTMLElement>("#study-hand-white-slot")!,
  };
  // 盤の右の列（解析の行・エンジンの結果・手順）。SFEN と評価値グラフは盤の下。
  // ⚠️ **中身は局面があるときだけ出す**（無いときは盤の代わりに案内を出す）。
  const studySide = root.querySelector<HTMLDivElement>("#study-side")!;
  // 中身は訂正タブと同じ .hand-zone だが、**見出しは出さない**（`is-readonly`）。
  // 盤との位置関係そのものが「どちらの駒台か」の説明になっているので、
  // 掴む相手でもない箱に文字を足すと、そのぶん駒台が縦に伸びるだけになる。
  // 読み上げ用に aria-label だけ持たせる。
  const studyHandZones = [true, false].map((black) => {
    const zone = document.createElement("div");
    zone.className = "hand-zone is-readonly";
    zone.dataset.black = String(black);
    zone.setAttribute("aria-label", `${black ? "先手" : "後手"}の駒台`);
    // 手番のマーク（2026-08-13）。**駒台の外側の角に絶対配置**なので高さを食わない
    // （盤の大きさは `--board-size` の式で決まるので、行を積むと盤が小さくなる）。
    // ⚠️ **常に両側に置いて手番側だけ光らせる**（出たり消えたりさせない）。
    // ⚠️ **解析タブでは手番がここにしか出ていない** —— SFEN 行の値は
    // ツールチップなので、ホバーしないと読めない。
    zone.innerHTML = `<span class="turn-mark">${black ? "▲" : "△"}</span>` +
      `<div class="hand-chips"></div>`;
    (black ? studyHandSlots.black : studyHandSlots.white).appendChild(zone);
    return zone;
  });
  // 投了（2026-10-09）。**駒台の外側の縁、手番のマークの横の角**（奥の駒台なら上、
  // 手前の駒台なら下。置き場所は style.css の `.resign-btn` と `_docs/ui/layout.md` の「投了のボタン」）。
  //
  // **投了すると終局になり、投了の行の右クリックから棋譜タブに登録できる。** 対局の
  // 途中で誤って登録する事故を防ぐため、登録の口は投了の行にしか無い（`movelist.ts`）。
  // ⚠️ **駒台の中・盤の真ん中寄りに戻さないこと**（2026-10-09 に 2 度置いて外した）。
  // 駒台の中は駒を取ろうとして押し、盤の真ん中寄りは視点のボタンと間違えて押す。
  // **外側の縁は誰も押さない**（手番のマークしか無い）。
  // ⚠️ **駒台の中の要素として置き、絶対配置で外側へ出す**（手番のマークと同じ）——
  // 視点を反転すると駒台ごと動き、高さも食わない（盤が小さくならない）。
  // ⚠️ **押せるのは手番の側だけ**（KIF の「91 投了」も手番の側の投了と読まれる）。
  // **両側に常に置いて押せない側は `disabled`**（手番のマークと同じく出たり消えたりさせない）。
  // ⚠️ **投了できるかは Go 側**（`StudyState.canResign`。本譜の先端で、終局していないとき）。
  // ⚠️ **中継を追っているあいだは押させない**（盤の駒と同じ。追従の状態は Go 側に無い）。
  // ⚠️ **押したら確認を出す**（初めは「やめる」。Enter の連打で投了しない）。
  const studyResignBtns = studyHandZones.map((zone) => {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "resign-btn";
    b.textContent = "投了";
    b.hidden = true;
    b.dataset.black = zone.dataset.black;
    // ⚠️ **駒台のクリック（打つ駒を選ぶ）に流さない**（study.ts は `.stock-chip` しか見ないが念のため）。
    b.addEventListener("click", (e) => {
      e.stopPropagation();
      askResign(b, e);
    });
    zone.appendChild(b);
    return b;
  });
  // 投了のボタンの出し入れに要る値（**局面の写しではない**。showStudy が入れる）。
  let studyCanResign = false;
  let studyTurnNow = 0;
  let studyFromURL = false;
  let studyLoadedForResign = false;
  let studyDropped = false;
  const resignSide = (b: HTMLButtonElement): boolean => b.dataset.black === "true";
  const sideName = (black: boolean) =>
    black ? (studyDropped ? "▲下手" : "▲先手") : (studyDropped ? "△上手" : "△後手");
  const syncResign = () => {
    for (const b of studyResignBtns) {
      const black = resignSide(b);
      const side = sideName(black);
      const mine = studyTurnNow === (black ? 1 : 2);
      // URL から読んだ棋譜は終局を取得元が決めるので、ボタンごと出さない。
      b.hidden = !studyLoadedForResign || studyFromURL;
      b.disabled = !(studyCanResign && mine && !isFollowing());
      b.title = isFollowing()
        ? "中継を追っているあいだは投了できません"
        : !mine
          ? `${side}の手番ではありません`
          : studyCanResign
            ? `${side}が投了します`
            : "本譜の最後の手で、終局していないときだけ投了できます";
    }
  };
  onFollowingChange(() => syncResign());
  const askResign = (b: HTMLButtonElement, e: MouseEvent) => {
    if (b.disabled || isFollowing()) {
      return;
    }
    openPopup(e.clientX, e.clientY, {
      label: "投了",
      // ⚠️ **初期フォーカスは「やめる」**（手順を消す確認と同じ）。
      focus: 1,
      items: [
        { label: `${sideName(resignSide(b))}が投了する`, onPick: () => void resignStudy() },
        { label: "やめる", onPick: () => {} },
      ],
    });
  };
  const resignStudy = async () => {
    if (isFollowing()) {
      return;
    }
    try {
      showStudy(await StudyService.Resign());
      sidePane.setStatus("");
    } catch (err) {
      sidePane.setStatus(String(err instanceof Error ? err.message : err));
    }
  };
  // 視点のボタン（解析タブ）。**盤のグリッドの右の列**に置いてある
  // （先手の対局者名の下・先手の駒台の上）。
  const studyFlip = root.querySelector<HTMLButtonElement>("#study-flip")!;
  // 中継の札（2026-10-08。視点のボタンの上）。画面を撮って追っている / 棋譜の自動更新。
  // ⚠️ **映すだけ**（入り切りの持ち主は `setFollowing` と `StudyState.kifuFollow`）。
  const studyLiveFollow = root.querySelector<HTMLElement>("#study-live-follow")!;
  const studyLiveKifu = root.querySelector<HTMLElement>("#study-live-kifu")!;
  // 棋譜の自動更新が入っているか（`StudyState.kifuFollow` の写し。**書き換えるのは `showStudy` だけ**）。
  let kifuFollowOn = false;

  // 視点（手前が先手 / 手前が後手）。**表示だけの反転で、局面には効かない。**
  // 切り替えの中身は下の「視点」の節にまとめてある（ここは値の置き場所だけ）。
  //
  // ⚠️ **持っているのは解析タブのぶんだけ**（2026-08-18）。訂正タブは
  // **撮ったとおりを描く面**なので反転しない（ボタンごと外した）。解析タブは
  // 撮った画像が後手目線なら**180 度回した局面**を持つので、撮った見え方に
  // 揃えるにはこちらを反転する（採るときに `adoptToStudy` が合わせる）。
  // ⚠️ **訂正タブにも反転を戻さないこと** —— 戻すと、後手目線で採った瞬間に
  // どちらを裏返すのかが 2 通りになり、撮った画像と見比べられなくなる。
  let studyFlipped = false;
  // 解析タブの駒台に最後に描いた中身。**視点を切り替えたときに並べ直すため**に持つ
  // （局面の写しではない —— 駒台の並び順だけがここに依存している）。
  let studyHands: Stock[] = [];
  // 最後に描いた状態の版（`StudyState.rev`。2026-09-08）。
  //
  // ⚠️ **局面の写しではない。** `study:changed` は**メソッドの戻り値とは別の経路**で
  // 届くので順番が入れ替わりうる（十字キーで手を続けて辿ると、古い局面のイベントが
  // 後から届く）。**既に描いた版より新しいときだけ描く**ための番号。
  let studyRev = 0;
  // 解析タブに局面があるか。
  //
  // ⚠️ **他の要素の `hidden` から読まないこと**（2026-09-09 に踏んだ）。
  // 切り離すと `.study-side` も `#eval-graph-row` も**別の理由で hidden になる**ので、
  // 片方から読むと**もう片方を戻したときに「局面が無い」と誤読する**
  // （実際、解析の列を切り離しているとグラフがドックへ戻らなかった）。
  let studyLoaded = false;

  // followWaitFrom は本譜の先端から「次に来るはずの手数」を割り出す。
  //
  // ⚠️ **`moveNumber` を使わないこと** —— あちらは**今見ている局面**の手数で、
  // 枝を読んでいると本譜の先端とずれる（追っているのは本譜の先端のほう）。
  // ⚠️ **`first` を足すこと** —— `Node.Number` は**根からの**手数で、棋譜の手数ではない。
  const followWaitFrom = (st: StudyState) => {
    if (!st.loaded) {
      return 0;
    }
    const tip = (st.nodes ?? []).find((n) => n.id === st.mainTip);
    return (st.first ?? 0) + (tip?.number ?? 0) + 1;
  };

  // ---- 盤の周り（勝率バー・対局者・連続解析の幕）----------------------------
  //
  // ⚠️ **ここは盤の側**（2026-09-08 に側の列と分けた）。**候補手も手順も持たない** ——
  // 切り離すと側の列は別の窓にあるので、**そちらの要素をここから触らないこと。**
  const evalGraphRow = root.querySelector<HTMLDivElement>("#eval-graph-row")!;
  // 連続解析のあいだ被せる幕（2026-08-14）。**触れなくするのが目的。**
  // ⚠️ **出口（止める口）を中に置くこと。** 幕は下を全部塞ぐので、
  // 側の列の「停止」も押せなくなる。
  const batchVeil = root.querySelector<HTMLElement>("#batch-veil")!;
  const batchVeilNote = root.querySelector<HTMLElement>("#batch-veil-note")!;
  const batchVeilCancel = root.querySelector<HTMLButtonElement>("#batch-veil-cancel")!;
  // ⚠️ **箱は真ん中に出して、邪魔ならドラッグで避けてもらう**（2026-09-14。
  // 以前は左の真ん中へ決め打ちで寄せていた）。**どこが邪魔かは窓の形と
  // 「今何を見ているか」で変わる**ので、寄せ先を決め打ちにしない。
  enableVeilDrag(batchVeil, root.querySelector<HTMLElement>("#batch-veil .veil-box")!);

  const winrateRow = root.querySelector<HTMLDivElement>("#winrate-row")!;
  const playerNames = {
    black: root.querySelector<HTMLElement>("#player-name-black")!,
    white: root.querySelector<HTMLElement>("#player-name-white")!,
  };

  // showPlayers は勝率バーの左右に対局者を出す。
  //
  // ⚠️ **Go 側は空を「先手」「後手」で埋めない**（名前が分かっているのか、
  // 既定を出しているだけなのかが区別できなくなる）。**既定の文言はここが持つ。**
  // ⚠️ **▲△ は名前があっても付ける**（名前だけではどちらか分からない）。
  //
  // ⚠️ **駒落ちでは既定を「下手／上手」にする**（2026-09-12。`StudyState.handicap`
  // が空でなければ駒落ち）。**替わるのは名前が無いときの既定だけ**で、
  // ▲△ も左右も側も動かさない（下手＝先手＝▲のまま）。
  const showPlayers = (black: string, white: string, handicap: string) => {
    const dropped = handicap !== "";
    const blackName = black || (dropped ? "下手" : "先手");
    const whiteName = white || (dropped ? "上手" : "後手");
    playerNames.black.textContent = `▲${blackName}`;
    playerNames.white.textContent = `△${whiteName}`;
    playerNames.black.title = blackName;
    playerNames.white.title = whiteName;
  };

  const winrateBar = root.querySelector<HTMLButtonElement>("#winrate-bar")!;
  const winrateWhite = root.querySelector<HTMLElement>("#winrate-white")!;
  // 評価値グラフの折れ線の色（**エンジンごと**）。⚠️ **側の列とは別に持つ** ——
  // 切り離すとあちらは別の窓に居るので、引きに行けない。出所は同じ設定なので、
  // **どちらも `showSettings` から入れ直す**。
  const graphColors = new Map<string, string>();

  // 今バーに出しているエンジン（**押すと次へ回る**）。
  //
  // ⚠️ **解析をまたいで残すこと。** 連続モードでは 1 手ごとに作り直されるので、
  // 毎回 1 つ目へ戻ると切り替えた意味が無い。
  let winrateEngineId = "";
  // 側の列から届いた**エンジンごとの最善手**。⚠️ **ここで cp から勝率を
  // 計算し直さないこと**（式も定数も Go 側。設定で定数を変えたときに片方だけ古くなる）。
  let winrateScores: EngineScore[] = [];

  // 勝率バーの左右を入れ替えているか（2026-10-02。黒地の右クリック）。
  // 立っていれば**左が先手（赤）・右が後手（青）**。
  //
  // ⚠️ **盤の視点（`studyFlipped`）とは別物。連動させないこと** —— 帯は盤ではなく
  // 横棒なので上下の視点と対応が無い。こちらは「横棒をどちら向きで読みたいか」の好み。
  let winrateFlipped = false;

  // applyWinRateFlip は帯と対局者名の左右を入れ替える（**画面だけ**。保存は呼び出し側）。
  //
  // ⚠️ **対局者名も一緒に動かすこと** —— 名前は帯の左右に並んでいて ▲△ で帯の色と
  // 対応しているので、帯だけ動くと名前が反対側の色の隣に居ることになる。
  // ⚠️ **駒台は動かさない**（盤の一部。遠い側が左上・近い側が右下のまま）。
  // ⚠️ **値の向きは触らない** —— 入れ替えるのは**どちらの端から描くか**（CSS）だけで、
  // `winrateWhite` の幅は後手の勝率のまま。
  const applyWinRateFlip = () => {
    winrateRow.classList.toggle("is-flipped", winrateFlipped);
    playerNames.black.classList.toggle("is-swapped", winrateFlipped);
    playerNames.white.classList.toggle("is-swapped", winrateFlipped);
    // title の並びも見た目の左右に合わせる。
    renderWinRate();
  };

  // 勝率バーに出しているエンジンを**側の列のカードに薄い枠で示す**（2026-10-02）。
  //
  // ⚠️ **選んでいるのはここ**（`winrateEngineId`）で、側の列は言われたカードに
  // 印を付けるだけ。**ドックしている列には直に、切り離した窓には
  // `study:winrate-engine` で**渡す（切り離した窓は切り離す前から生きているので、
  // 変わるたびに送っておけば出たときに揃っている）。
  // ⚠️ **印を付けるのはエンジンが 2 つ以上のときだけ**（1 つなら見れば分かる）。
  // **帯を隠しているあいだも付けない**（見えていない帯の持ち主を示しても意味が無い）。
  // `markWinRateCard` は側の列ができてから差し替える（ここより下で作るため）。
  let markWinRateCard = (_id: string) => {};
  let winrateMarkSent: string | null = null;
  const syncWinRateMark = () => {
    const one =
      winrateScores.find((e) => e.id === winrateEngineId) ?? winrateScores[0];
    const id = one && winrateScores.length > 1 && !winrateHidden ? one.id : "";
    markWinRateCard(id);
    if (id !== winrateMarkSent) {
      winrateMarkSent = id;
      void Events.Emit("study:winrate-engine", id);
    }
  };

  // renderWinRate は選ばれているエンジンの勝率をバーに描く。
  //
  // ⚠️ **既定は左が後手（青）・右が先手（赤）。符号はここでいじらないこと**
  // （逆に伸びると「エンジンが間違えている」としか読めない壊れ方になる）。
  // **左右の入れ替え**（`winrateFlipped`）は CSS が描く端を変えるだけで、
  // ここが入れる値（後手の幅）は同じ。
  // ⚠️ **出すのは 1 つだけ**（押すと切り替わる）。並べると盤の上に段が積まれて
  // そのぶん盤が小さくなるうえ、**形勢を一目で見るための帯**なので読む対象が増える。
  const renderWinRate = () => {
    const one =
      winrateScores.find((e) => e.id === winrateEngineId) ?? winrateScores[0];
    // **押して切り替えられるのは 2 つ以上あるときだけ。**
    const many = winrateScores.length > 1;
    winrateBar.classList.toggle("is-switchable", many);
    winrateBar.disabled = !many;
    syncWinRateMark();

    if (!one || one.winRate === null) {
      // **まだ値が無いあいだは中立の見た目にして、50% と書かない**
      // ——「互角」と「まだ分からない」は別物。
      winrateBar.classList.add("is-empty");
      winrateWhite.style.width = "50%";
      winrateBar.title = one
        ? `${one.label}: 解析するとここに形勢が出ます`
        : "解析するとここに形勢が出ます";
      return;
    }
    const black = Math.min(Math.max(one.winRate, 0), 1);
    const white = 1 - black;
    winrateBar.classList.remove("is-empty");
    winrateWhite.style.width = `${(white * 100).toFixed(1)}%`;
    // ⚠️ **数字は画面に出さず、カーソルを当てたときだけ出す**（盤の上に文字を
    // 積むと、そのぶん盤が小さくなる）。
    // 並びは見た目の左右に合わせる（入れ替えているなら先手が先）。
    const whiteText = `後手 ${Math.round(white * 100)}%`;
    const blackText = `先手 ${Math.round(black * 100)}%`;
    const head = winrateFlipped
      ? `${blackText} ／ ${whiteText}`
      : `${whiteText} ／ ${blackText}`;
    winrateBar.title =
      `${head}（評価値 ${one.scoreLabel}）／ ${one.label}` +
      (many ? "　押すと別のエンジンに切り替わります" : "");
  };

  // 押すと次のエンジンに切り替える（登録順で回る）。
  winrateBar.addEventListener("click", () => {
    if (winrateScores.length < 2) {
      return;
    }
    const at = winrateScores.findIndex((e) => e.id === winrateEngineId);
    winrateEngineId = winrateScores[(at + 1) % winrateScores.length].id;
    renderWinRate();
  });

  // ---- 勝率バーを隠す（2026-09-10）------------------------------------------
  //
  // **中継を観ながら使うので「評価値を見たくない」場面がある。** 盤の真上の帯は
  // 目に入るのを避けようが無いので、消せるようにした。切り替えるのは
  // **盤の右クリック**（設定タブには置いていない —— 見えているものを消す操作は、
  // その場で切り替えるほうが素直）。
  //
  // ⚠️ **解析は止めない。** 消えるのは帯だけで、候補手も評価値も右の列には
  // 今までどおり出る（**見たくないものだけを消す**）。
  //
  // ⚠️ **帯と対局者名は別々に消せる**（2026-09-10。1 行目に並んでいる 2 つ）。
  // **帯だけ消して名前は残す**（誰の対局かは見ていたい）も、**両方消す**
  // （盤を大きくしたい）も、どちらも普通の使い方なので 1 つにまとめない。
  let winrateHidden = false;
  let playersHidden = false;

  // ⚠️ **`is-toprow-empty` だけはここが持つ**（ほかの `#panel-study` の class と CSS 変数は studylayout.ts）。
  const panelStudy = root;
  // applyStudyTopRow は**盤の上の 1 行**（帯・対局者名）の出し入れ。
  // **画面だけに効かせる**（保存は呼び出し側）。
  //
  // ⚠️ **出す条件をここ 1 か所にまとめてあること。** 「局面がある」と
  // 「隠していない」の掛け算なので、`syncStudyChrome` からもここを呼ぶ ——
  // 2 か所に書くと、**隠したまま別の局面を読み込んだ瞬間に戻る**。
  //
  // ⚠️ **盤の取り分（`--winrate-h`）を返すのは、行が空になったときだけ。**
  // 片方でも出ているなら 1 行目は残るので、引く量だけ減らすと**そのぶん盤が
  // 縦にはみ出す**（対局者名は 24px あり、帯の 34px とほとんど変わらない）。
  const applyStudyTopRow = () => {
    winrateRow.hidden = winrateHidden || !studyLoaded;
    // 帯を隠したらカードの印も外す（出したら戻す）。
    syncWinRateMark();
    playerNames.black.hidden = playersHidden || !studyLoaded;
    playerNames.white.hidden = playersHidden || !studyLoaded;
    // ⚠️ **`studyLoaded` は見ないこと。** 局面が無いあいだも行の高さは
    // 予約したままにする（出たり消えたりすると盤ごと上下に動く）。
    const empty = winrateHidden && playersHidden;
    if (panelStudy.classList.contains("is-toprow-empty") === empty) {
      return;
    }
    panelStudy.classList.toggle("is-toprow-empty", empty);
    // 盤の大きさが変わったので、重ねたグリッドと横の遊びを取り直す
    // （評価値グラフの切り離しと同じ後始末）。
    studyBoardUI.relayout();
    evalGraphUI.relayout();
    layout.settle();
  };

  // 解析タブの**空いているところ**（盤・駒台・帯・右の列のどれでもない黒地）の
  // 右クリックで出すメニュー（2026-09-10）。
  //
  // ⚠️ **盤と駒台の上では出さない。** あちらの右クリックは**掴んだ駒を離す**
  // 操作（2026-08-29）で、指す先を探している最中にメニューが出ると
  // **やめる操作がメニューを閉じる操作に化ける。**
  //
  // ⚠️ **`window.confirm` と同じで、押した場所に出すこと**（`popup.ts`）。
  const openBoardMenu = (x: number, y: number) => {
    openPopup(x, y, {
      label: "盤の表示",
      // ⚠️ **1 つにまとめないこと**（2026-09-10）。**帯だけ消して名前は残す**
      // 使い方があるので、独立したチェック 2 つにしてある。
      items: [
        {
          label: "評価値バー",
          checked: !winrateHidden,
          onPick: () => {
            winrateHidden = !winrateHidden;
            // **先に画面へ効かせる**（押した手応えを保存の往復まで待たせない）。
            applyStudyTopRow();
            // ⚠️ **設定に残すこと** —— 画面の組み方の好みなので、次の起動でも
            // 同じ形で始まってほしい（切り離しと同じ扱い）。
            void SettingsService.SetHideWinRateBar(winrateHidden);
          },
        },
        {
          label: "対局者名",
          checked: !playersHidden,
          onPick: () => {
            playersHidden = !playersHidden;
            applyStudyTopRow();
            void SettingsService.SetHidePlayerNames(playersHidden);
          },
        },
        {
          // 2026-10-02。⚠️ **隠す 2 つとは別のチェック**（帯を消したまま向きだけ
          // 変えても、次に出したときにその向きで出ればよい）。
          label: "評価値バーの左右を反転",
          checked: winrateFlipped,
          onPick: () => {
            winrateFlipped = !winrateFlipped;
            applyWinRateFlip();
            void SettingsService.SetFlipWinRateBar(winrateFlipped);
          },
        },
      ],
    });
  };

  // isStudyBackdrop は「解析タブの、何も置いていないところを押したか」。
  //
  // ⚠️ **判定は「押した先が入れ物そのものか」で行う。除外リストを持たないこと** ——
  // 盤・駒台・帯・ボタン・右の列・幕はどれも**中身の要素が受ける**ので、入れ物まで
  // 抜けてくるのは何も置いていないところを押したときだけ。除外リストにすると、
  // **行を足すたびに書き足すことになり、書き忘れた場所でだけメニューが出る。**
  //
  // ⚠️ **`#panel-study` の中に限ること。** `.board-area` は訂正タブにもある。
  const isStudyBackdrop = (t: EventTarget | null): boolean => {
    const el = t as HTMLElement | null;
    if (!el?.closest?.("#panel-study")) {
      return false;
    }
    return (
      el.id === "panel-study" ||
      el.id === "study-board-with-hands" ||
      el.classList.contains("board-area")
    );
  };

  screen.addEventListener("contextmenu", (e) => {
    if (!isStudyBackdrop(e.target)) {
      return;
    }
    // ⚠️ **webview の既定メニューは止める**（盤まわりでは意味が無いうえ、
    // こちらのメニューと二重に出る）。
    e.preventDefault();
    openBoardMenu(e.clientX, e.clientY);
  });

  // syncStudyChrome は**盤の周りの出し入れ**（局面があるかどうかだけで決まる）。
  //
  // ⚠️ **まだ結果が無くても枠は出す。** 出たり消えたりすると盤が上下に動くうえ、
  // 勝率バーは盤の**上**なので、動くと盤ごと押し下げる。
  const syncStudyChrome = (loaded: boolean) => {
    // ⚠️ **盤の上の 1 行（帯・対局者名）は `applyStudyTopRow` に任せること。**
    // 「隠してあるなら局面があっても出さない」の判断を 2 か所に書くと、
    // **隠したまま別の局面を読み込んだ瞬間に戻る**（`studyLoaded` は
    // 呼び出し元が先に入れてある）。
    applyStudyTopRow();
    // 視点のボタンも盤と一緒（盤が出ていないのに向きだけ変えても意味が無い）。
    studyFlip.hidden = !loaded;
    // 側の列・縦のバー・評価値グラフの行（⚠️ **切り離しているあいだは出さない**。式は studylayout.ts の 1 か所）。
    layout.showPanes(loaded);
  };

  // showStudy は解析タブの表示一式を描く。
  //
  // ⚠️ **盤の側だけ**（2026-09-08）。**候補手・手順・解析の操作は `sidepane.ts`** で、
  // ここは `render` を呼ぶだけ —— **別ウィンドウへ切り離しても同じものが動く**
  // ようにするため、側の列の中身をここから触らないこと。
  //
  // ⚠️ **盤の操作から呼ばれたとき（`fromBoard`）だけは盤を描き直さない**
  // （あちらが既に描いている。同じ状態を 2 か所から描くと、どちらが今か分からなくなる）。
  const showStudy = (st: StudyState, o?: { fromBoard?: boolean }) => {
    // ⚠️ **描いた版を控えること。** これが遅れて届いた `study:changed` を弾く鍵。
    studyRev = Math.max(studyRev, st.rev ?? 0);
    const loaded = !!st.loaded;
    studyLoaded = loaded;
    studyStage.hidden = !loaded;
    studyBoard.hidden = !loaded;
    studyPlaceholder.hidden = loaded;
    // 駒台は局面の一部なので、局面があるあいだは**空でも出す**
    // （持ち駒が 0 枚であることも局面の情報）。
    studyHandSlots.black.hidden = !loaded;
    studyHandSlots.white.hidden = !loaded;
    // SFEN も盤と一緒（盤の下の行なので、局面が無いのに枠だけ残さない）。
    studySfenRow.hidden = !loaded;
    if (loaded) {
      studyBoard.setAttribute("sfen", st.boardSfen);
      // 手番と手数は SFEN に入っているが、読むのに要るのは文字のほう。
      const n = st.moveNumber > 0 ? ` / ${st.moveNumber}手目` : "";
      studySfenOut.textContent = `${st.sfen}`;
      // ⚠️ **SFEN そのものも title に入れること**（2026-08-14）。盤の下は 1 行
      // しか無いので、長い局面は**画面では末尾が切れる**。
      studySfenOut.title = `${st.sfen}
${st.turnLabel}${n}`;
      showStudyHand(st.hands ?? []);
    } else {
      studySfenOut.textContent = "-";
      showStudyHand([]);
    }
    // ⚠️ **手番のマークは局面と一緒に更新する。** 1 手ごとに入れ替わるので、
    // 落とすと**前の手番のまま光り続ける**（連続モードでは毎手ずれる）。
    showStudyTurn(loaded ? st.turn : 0);
    // 投了のボタンも手番と一緒（押せるのは手番の側だけ）。
    studyCanResign = loaded && !!st.canResign;
    studyTurnNow = loaded ? st.turn : 0;
    studyFromURL = loaded && (st.sourceUrl ?? "") !== "";
    studyLoadedForResign = loaded;
    studyDropped = loaded && (st.handicap ?? "") !== "";
    syncResign();
    // ⚠️ **局面と一緒に更新する。** 根を入れ替えると対局者も変わる。
    showPlayers(st.black ?? "", st.white ?? "", st.handicap ?? "");
    if (!o?.fromBoard) {
      studyBoardUI.render(loaded ? st : null);
    }
    // ⚠️ **側の列は必ずここで描くこと。** `fromBoard` は「盤が自分で描いた」の
    // 意味で、**手順も候補手も描かれていない。**
    sidePane.render(loaded ? st : null);
    // 盤の周りの出し入れ（勝率バー・対局者・視点・評価値グラフ・側の列）。
    syncStudyChrome(loaded);
    // 中継を追うボタンも局面があるときだけ（繋ぐ先が無いと足せない）。
    studyFollowBtn.hidden = !loaded;
    // 棋譜の自動更新の札。⚠️ **条件は自動更新のアイコンと同じ**（URL から読んだときだけ。sidepane.ts）。
    kifuFollowOn = loaded && (st.sourceUrl ?? "") !== "" && !!st.kifuFollow;
    studyLiveKifu.hidden = !kifuFollowOn;
    // ⚠️ **待っている手数はここで取ること。** 追跡のループの中だけで数えると、
    // **人が手順を消したり分岐にしたりしたときに古いまま**になる
    // （`study:changed` もここへ来る）。
    followUI.setWaiting(followWaitFrom(st));
    // 局面が変わったら点を取り直す（**戻った位置の縦線も動く**）。
    refreshEvalGraph();
  };

  // ⚠️ **別ウィンドウとの連動の土台**（2026-09-08）。`StudyService` は局面を
  // 変えるたびに `study:changed` を流す（`app.Event.Emit` は**アプリ全体**に届く）ので、
  // **自分が呼んでいない変更**にも気づける。
  //
  // ⚠️ **戻り値で描く経路は残してある。** 呼んだ窓はその場で描けるほうが速いし、
  // **描くのは同じ `showStudy` に同じ `StudyState` を渡すだけ**なので、
  // 2 つの描き方が生まれるわけではない（下の版の判定で二度描きにならない）。
  //
  // ⚠️ **版が古いイベントは捨てること。** イベントと戻り値は別の経路なので
  // **順番が入れ替わりうる** —— 捨てないと、十字キーで手を続けて辿ったときに
  // **古い局面が後から届いて盤が戻る**。
  Events.On("study:changed", (event: { data: StudyState }) => {
    const st = event.data;
    if (!st || (st.rev ?? 0) <= studyRev) {
      return;
    }
    showStudy(st);
  });

  // 解析タブの駒台の角のマークを手番に合わせる（1=先手番 / 2=後手番 / 0=局面なし）。
  //
  // ⚠️ **`showStudyHand` と分けてあるのは、視点の切り替えで駒台だけを並べ直す
  // 経路があるから**（あちらは手番を知らない）。手番はここ 1 本で更新する。
  const showStudyTurn = (turn: number) => {
    for (const zone of studyHandZones) {
      const black = zone.dataset.black === "true";
      const mark = zone.querySelector<HTMLElement>(".turn-mark")!;
      const mine = turn === (black ? 1 : 2);
      const name = black ? "先手" : "後手";
      mark.classList.toggle("is-active", mine);
      mark.title = mine ? `${name}番です` : `${name}の駒台`;
    }
  };

  // 解析タブの駒台。**未決は残っていない**（確定した局面なので）。
  //
  // ⚠️ **駒種 1 つにつき駒 1 枚を出し、枚数は数字で添える**（2026-08-12 に変えた）。
  // 以前は訂正タブと同じく**枚数ぶん並べていた**が、歩が溜まると駒台から溢れ、
  // **スクロールバーが出て幅を食い、1 行に入る駒が 3 枚から 2 枚に減って更に溢れる**
  // という悪循環になっていた。駒種は最大 8 つなので、この形なら**溢れが原理的に
  // 起きない**（多くの将棋ソフトと同じ見せ方でもある）。
  // ⚠️ **訂正タブの駒台は今までどおり枚数ぶん並べる** —— あちらは 1 枚ずつ掴んで
  // 動かす面なので、駒の数と操作の対象が一致しているほうがよい。
  const showStudyHand = (inv: Stock[]) => {
    // ⚠️ **視点を切り替えたときに並べ直すため、最後に描いた中身を覚えておく。**
    // 覚えないと、反転しても駒台だけが前の向きの並びのまま残る。
    studyHands = inv;
    for (const zone of studyHandZones) {
      const black = zone.dataset.black === "true";
      const chips = zone.querySelector<HTMLDivElement>(".hand-chips")!;
      chips.replaceChildren();
      let total = 0;
      // 並びは Go 側の Inventory の順（歩香桂銀金角飛王）。
      // **駒が 180 度回っている側は逆順**（そちら側から読んで同じ並びになる）。
      // ⚠️ **回っているのは「後手」ではなく「奥の側」**なので、視点を反転すると
      // 逆順にする相手も入れ替わる（`black === flipped`）。
      for (const s of black === studyFlipped ? [...inv].reverse() : inv) {
        const n = black ? s.handBlack : s.handWhite;
        if (n <= 0) {
          continue;
        }
        total += n;
        const chip = document.createElement("div");
        chip.className = black ? "stock-chip" : "stock-chip is-white";
        // ⚠️ **piece を持たせること。** 駒台の駒はマスを持たないので、
        // 「打てる位置」を合法手（`legal.Move.drop`）と突き合わせる鍵がこれしかない。
        chip.dataset.piece = String(s.piece);
        chip.append(black ? s.letter : s.letter.toLowerCase());
        // ⚠️ **見出しは dataset にも持たせる。** `study.ts` が「押せない理由」を
        // 足した title を組み立てるので、素の見出しが要る（title へ直に足すと、
        // 描き直すたびに理由が積み重なる）。
        chip.dataset.label = `${black ? "先手" : "後手"}の${s.name}（${n}枚）`;
        chip.title = chip.dataset.label;
        // 1 枚のときは数字を出さない（実際の駒台と同じで、見れば分かる）。
        if (n > 1) {
          const count = document.createElement("span");
          count.className = "stock-count";
          count.textContent = String(n);
          chip.appendChild(count);
        }
        chips.appendChild(chip);
      }
      zone.classList.toggle("is-empty", total === 0);
    }
    // ⚠️ **文字の要約（「先手 歩2 / 後手 角1」）は出さない**（2026-08-12 に外した）。
    // 盤の脇に駒そのものが並んでいるので同じことを 2 度言っており、
    // **盤の下に行を積むぶんだけ盤が小さくなっていた。**
  };

  // ---- 中継を追う（followcontrol.ts）------------------------------------------
  //
  // ⚠️ **押して描くだけ**（1 周の段取りと状態は Go の中継セッション。`follow:update` を受けて札と帯へ流す）。
  const followUI = mountFollowControl({
    studyFollowBtn,
    studyLiveFollow,
    setSideStatus: (text) => sidePane.setStatus(text),
    frameVisible: opts.frameVisible,
    kifuFollowOn: () => kifuFollowOn,
  });

  // 解析タブの盤の操作（手を進める UI）。**合法手だけ。**
  //
  // ⚠️ **訂正タブの mountEditor とは別物。** あちらはドラッグで自由に置く面で、
  // こちらはクリック 2 回で合法手だけを辿る面。**同じグリッドを流用しないこと。**
  const studyBoardUI = mountStudyBoard({
    stage: studyStage,
    handSlots: studyHandSlots,
    // 指したあとの局面は showStudy がそのまま描く（盤・駒台・SFEN・警告・手順）。
    onState: (st) => showStudy(st, { fromBoard: true }),
    // 空文字は「理由を消す」（駒を掴み直したときなど）。**出しっぱなしにしないこと** ——
    // 前の操作の理由が残っていると、今の操作が失敗したように見える。
    onError: (message) => sidePane.setStatus(message),
  });

  // ---- 十字キーの上下で手順を辿る（2026-08-18）----------------------------
  //
  // **↑ で 1 手戻り、↓ で 1 手進む。** 手順リストの上下の並びと同じ向きなので、
  // 押した方向とカーソルの動く向きが一致する。**盤の右のリストを押すのと同じ操作**
  // （`GoTo`）で、**手順は 1 手も消えない。**
  //
  // ⚠️ **どこを押していても効く**（リストにフォーカスを当てさせない）。手を辿る
  // ときに見ているのは**盤**なので、先にリストを掴ませるのは 1 手多い。
  // そのぶん**横取りしてはいけない相手**を並べて外してある（下記）。
  //
  // ⚠️ **Delete キー（2026-09-26）もここで受ける** —— 今見ている手から下を消す
  // （手順リストの右クリック →「以降の手を削除」と同じ。**1 手だけなら聞かずに
  // 消し、2 手以上なら確認が出る**。判断は `movelist.ts` の `dropAt`）。
  // ⚠️ **手順リストにフォーカスがあるときだけ、にはしない。** 手のボタンは押すと
  // 描き直されてフォーカスが外れるので、それでは**押した直後に効かない**。
  // 止める相手は ↑ / ↓ と同じ（欄の中の Delete は文字を消す操作）。
  // ⚠️ **手順を切り離しているときはあちらの窓が受ける**（`sidePane.dropCurrent` が
  // 「この窓に手順が出ているか」を見て何もしない）。
  const studyStep = (e: KeyboardEvent) => {
    const drop = e.key === "Delete";
    const delta = e.key === "ArrowUp" ? -1 : e.key === "ArrowDown" ? 1 : 0;
    if (!delta && !drop) {
      return;
    }
    // ⚠️ **解析タブに居るときだけ。** 訂正タブにも盤があるので、
    // タブを見ずに動かすと**見えていない盤の局面が変わる**。
    if (!opts.isShown()) {
      return;
    }
    // ⚠️ **修飾キー付きは触らない**（ブラウザや OS 側の操作なので横取りしない）。
    if (e.ctrlKey || e.altKey || e.metaKey || e.shiftKey) {
      return;
    }
    // ⚠️ **連続解析が走っているあいだは動かさない**（2026-08-14 の幕と同じ理由）。
    // あちらが 1 手ずつ局面を動かしているので、横から動かすと**自分の操作と
    // 連続解析が同じ局面を取り合う**。幕はポインタしか塞げない。
    if (sidePane.stepping()) {
      return;
    }
    // ⚠️ **小さなダイアログが開いているあいだも動かさない**（成る/成らず・
    // 手順を消す・手順を追加）。局面が変われば `study.ts` が黙って閉じるので、
    // **聞かれている最中に盤が進んで、答えが別の手に効く**ように見える。
    if (document.querySelector(".popup-menu")) {
      return;
    }
    // ⚠️ **上下キーを本来の意味で使う相手から奪わないこと。** 数値欄・選択・
    // テキスト欄と、**3 本のスプリットバー**（`role="separator"`。上下キーで
    // 大きさを変える）がそれで、奪うと**掴んでいるつもりの操作が盤を動かす**。
    const el = e.target as HTMLElement | null;
    const tag = el?.tagName ?? "";
    if (
      tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT" ||
      el?.isContentEditable || el?.closest('[role="separator"]')
    ) {
      return;
    }
    // ⚠️ **ここまで来たら既定の動作は止める**（パネルが縦にスクロールする）。
    e.preventDefault();
    if (drop) {
      sidePane.dropCurrent();
      return;
    }
    studyBoardUI.step(delta);
  };
  document.addEventListener("keydown", studyStep);

  // 再読み込み（2026-08-13）。**URL の側を正**にして手順を最新にする。
  //

  // ⚠️ **置き場所は解析の行の右端**（2026-09-09 にバーの上から移した）——
  // **切り離した窓の「ドックに戻す」と同じ場所**。出す/戻すが同じ位置にあると、
  // どちらの状態でも探す場所が変わらない。
  // ⚠️ **アイコンだけなので、意味は `aria-label` と `title` が持つ。**
  const studySplitDetach = document.createElement("button");
  studySplitDetach.id = "study-side-detach";
  studySplitDetach.className = "icon-btn";
  studySplitDetach.type = "button";
  studySplitDetach.innerHTML = iconMarkup(FiExternalLink);
  studySplitDetach.setAttribute("aria-label", "候補手を切り離す");
  studySplitDetach.title =
    "切り離す: 候補手を別ウィンドウに出します（盤の大きさに効かなくなります）";

  // 手順の側の「切り離す」（2026-09-12）。⚠️ **置き場所は手順の見出しの行の右端**
  // —— 切り離した窓の「ドックに戻す」と同じ場所。**候補手の側と作法を揃えること。**
  // ⚠️ **候補手のボタンと 1 つにまとめないこと** —— どちらを外に出すかは
  // そのときの読み方で変わるので、**別々に押せることが要る。**
  const studyMovesDetach = document.createElement("button");
  studyMovesDetach.id = "study-moves-detach";
  studyMovesDetach.className = "icon-btn";
  studyMovesDetach.type = "button";
  studyMovesDetach.innerHTML = iconMarkup(FiExternalLink);
  studyMovesDetach.setAttribute("aria-label", "手順を切り離す");
  studyMovesDetach.title =
    "切り離す: 手順を別ウィンドウに出します（盤の大きさに効かなくなります）";

  // 盤の右の列（2026-09-08 に `sidepane.ts` へ切り出した）。
  //
  // ⚠️ **中身をここから触らないこと。** 別ウィンドウへ切り離せるようにするための
  // 切り出しなので、**ここが知っているのは「渡す 5 つ」だけ**にする。
  const sidePane = mountSidePane({
    host: studySide,
    // 手順の操作で局面が変わったとき（**盤も一緒に描き直す**）。
    onState: (st) => showStudy(st),
    // 候補手を選んだら盤に矢印を出す。⚠️ **切り離すと盤は別の窓**なので、
    // 側の列から直に触らせない（ここが唯一の受け口）。
    onHint: (usi) => studyBoardUI.showHint(usi),
    // 勝率バーの材料（**エンジンごとの最善手**）。
    onScores: (scores) => {
      winrateScores = scores;
      renderWinRate();
    },
    // 連続解析の幕。⚠️ **盤も塞ぐこと**（1 手ずつ動かしている最中に触られると、
    // 自分の操作と連続解析が同じ局面を取り合う）。
    // ⚠️ **評価値グラフの窓にも知らせること**（2026-09-09 に踏んだ）。あちらは
    // **点を押すと `GoTo` が飛ぶ**ので、塞がないと連続解析と局面を取り合う。
    // ⚠️ **配るのは「この列が使われているとき」だけ**（切り離していれば向こうが
    // 配る）。両方が配ると、**1 手ごとに幕が瞬く。**
    onBusy: (on, note) => {
      batchVeil.hidden = !on;
      batchVeilNote.textContent = note;
      if (!layout.sideDetached()) {
        void Events.Emit("study:busy", { on, note });
      }
    },
    // 設定を書き換えたら設定タブも描き直す（色・候補手の本数）。
    onSettings: (st) => opts.onSettings(st),
    // ⚠️ **その面の中の同じ場所に置くもの。** ドックしているときは「切り離す」、
    // 切り離した窓では「ドックに戻す」——**同じ場所**にすること。
    analyzeAction: studySplitDetach,
    movesAction: studyMovesDetach,
  });
  // 勝率バーのエンジンの印（`syncWinRateMark`）。側の列ができたのでここで繋ぐ。
  markWinRateCard = (id) => sidePane.setWinRateEngine(id);
  // ⚠️ **幕の出口はここ**（幕は下を全部塞ぐので、側の列の「停止」も押せない）。
  batchVeilCancel.addEventListener("click", () => sidePane.cancelBatch());
  // メイン画面は入力タブで始まる（解析タブを開くまで棋譜の自動更新を刻まない）。
  sidePane.setTabShown(false);

  // ⚠️ **別の窓の幕からも止められること**（`study:cancel`）。評価値グラフの窓には
  // 「停止」も手順も無いので、**出口がここに繋がっていないと窓を閉じるしかない。**
  // ⚠️ **持ち主だけが応じること** —— 切り離しているあいだ連続解析を持っているのは
  // 向こうの窓で、こちらが応じても止める相手が居ない。
  Events.On("study:cancel", () => {
    if (layout.sideDetached()) {
      return;
    }
    sidePane.cancelBatch();
  });

  // 評価値グラフ（2026-08-12）。**手順の 1 手ごとの最善手の評価値**を折れ線にする。
  //
  // ⚠️ **点はここに溜めない。** 持っているのは Go 側（`StudyService.Evals`）で、
  // **手順を切ったときにどこまで捨てるかを知っているのはあちらだけ**。
  // フロントにも溜めると、戻って別の手を指したときに片方だけ古い値が残る。
  const evalGraphUI = mountEvalPane({
    host: root.querySelector<HTMLElement>("#eval-graph")!,
    all: root.querySelector<HTMLInputElement>("#eval-graph-all")!,
    from: root.querySelector<HTMLInputElement>("#eval-graph-from")!,
    to: root.querySelector<HTMLInputElement>("#eval-graph-to")!,
    mode: root.querySelector<HTMLElement>("#eval-graph-mode")!,
    legend: root.querySelector<HTMLElement>("#eval-graph-legend")!,
    readout: root.querySelector<HTMLElement>("#eval-graph-readout")!,
    // 折れ線の色は**エンジンごと**（登場順ではない）。⚠️ **設定を直に読ませない** ——
    // 既定色の解決は Go 側で済んでおり、ここは受け取った写しを引くだけ。
    colorOf: (id) => graphColors.get(id) ?? "#b8c0d0",
    // **押したらその局面へ戻る**（手順のチップと同じ「戻って見る」操作。手順は消さない）。
    // ⚠️ **渡ってくるのは節点の id**（手数ではない。枝があると同じ手数が何個もある）。
    onSeek: (id) => {
      void (async () => {
        try {
          showStudy(await StudyService.GoTo(id));
        } catch (err) {
          sidePane.setStatus(String(err instanceof Error ? err.message : err));
        }
      })();
    },
    // 縦軸を切り替えたら**保存する**（次の起動も、切り離した窓も同じ軸で始まる）。
    // ⚠️ **保存できなくても画面はそのまま**（設計原則3）。次の起動で戻るだけで、
    // 今見えている折れ線は正しい。
    onMode: (m) => {
      void SettingsService.SetEvalGraphAxis(m).catch(() => {});
    },
    // ← / → で手を動かしてよいか（2026-09-26）。**止める条件は ↑ / ↓ と同じ**
    // （連続解析の最中・小さなダイアログが出ているあいだ。`studyStep`）。
    canStep: () => !sidePane.stepping() && !document.querySelector(".popup-menu"),
  });

  // ⚠️ **取り直しの実装は `evalgraphpane.ts` に移した**（2026-09-08）。
  // **切り離した窓と同じものを使う**ので、片方だけ直して挙動が食い違うことがない。
  const refreshEvalGraph = () => evalGraphUI.refresh();

  // ⚠️ **解析のイベントはここでも拾うこと**（2026-09-08）。折れ線は解析が
  // 進むたびに伸びるが、**側の列は別ウィンドウに居ることがある**ので、
  // あちら経由にすると切り離した瞬間にグラフが止まる。
  // ⚠️ **途中経過は間引く**（深さが 1 つ進むたびに、しかもエンジンの数だけ届く）。
  Events.On("analyze:info", () => evalGraphUI.refreshSoon());
  Events.On("analyze:done", () => evalGraphUI.refresh());
  Events.On("analyze:failed", () => evalGraphUI.refresh());

  // ---- 解析タブの寸法と切り離し（studylayout.ts）----------------------------
  //
  // 評価値グラフの高さ・盤と解析の列の幅・列の折り畳み・別ウィンドウへの切り離し。
  // ⚠️ **ここ（盤・側の列・グラフを組んだ直後）で組むこと** —— 組んだ時点で
  // アイコンと `aria-*` を入れ、測り直しも呼ぶ。
  const layout = mountStudyLayout({
    root: screen,
    studyStage,
    studySide,
    evalGraphRow,
    studyLoaded: () => studyLoaded,
    studyBoardUI,
    evalGraphUI,
    sidePane,
    studySplitDetach,
    studyMovesDetach,
  });

  // ---- 切り離した窓からの知らせ（**盤に効くもの**）--------------------------
  //
  // ⚠️ **フロントの `Events.Emit` は Go を経由して全部の窓へ配られる**
  // （`EmitEvent` → `dispatchEventToWindows`）。ドックしているあいだは
  // **コールバックで直に渡している**ので、こちらは飛んでこない。
  //
  // ⚠️ **ドックしているあいだは聞かないこと**（2026-09-09 に踏んだ）。切り離しの窓は
  // **切り離していなくても作られていて中身が動いている**ので、こちらが自分の
  // コールバックで描いているものを**向こうの描き直しが上書きする** ——
  // 実際、連続解析の最中に**幕が 1 手ごとに消えてはまた出た**（チカチカする）。
  // **同じ値を 2 経路で描かない**、が元からの約束（向こうも出さないようにしてある）。
  const fromSideWindow = () => layout.sideDetached();
  Events.On("study:hint", (event: { data: string | null }) => {
    if (!fromSideWindow()) {
      return;
    }
    studyBoardUI.showHint(event.data ?? null);
  });
  Events.On("study:scores", (event: { data: EngineScore[] }) => {
    if (!fromSideWindow()) {
      return;
    }
    winrateScores = event.data ?? [];
    renderWinRate();
  });
  // ⚠️ **幕は盤にも被せること** —— 連続解析は 1 手ずつ局面を動かすので、
  // その最中に盤を触ると自分の操作と取り合う。
  Events.On("study:busy", (event: { data: { on: boolean; note: string } }) => {
    if (!fromSideWindow()) {
      return;
    }
    batchVeil.hidden = !event.data?.on;
    batchVeilNote.textContent = event.data?.note ?? "";
  });

  // ⚠️ **「訂正に戻る」ボタンは無くした**（2026-08-12）。**上のタブで戻れる**うえ、
  // タブを離れたときの後始末（走っている解析を止めてエンジンを手放す）は
  // `selectTab` が既にやっているので、ボタンは同じことを 2 つ目の入口でしていた。
  // **盤の上の段ごと外して、そのぶん盤を大きくしてある。**

  // ---- 視点（手前が先手 / 手前が後手）--------------------------------------
  //
  // **表示だけの反転で、局面には一切効かない。** 反転するのは
  // `<shogi-board>` の絵（`flip` 属性）・駒台の置き場所と向き・盤に重ねる
  // グリッドの読み替えだけで、**Go 側は視点を知らない**。
  //
  // ⚠️ **モデルは反転しない。** SFEN も先後も手番も指す手（USI）もそのままで、
  // エンジンに渡すものも変わらない。`_docs/design-position.md` の「視点」（＝撮った画像と盤面が一致していること。学習データのラベルは
  // 画素と一致していなければならない）は**そのまま生きている**。
  //
  // ⚠️ **そのぶん見え方とモデルが反対になる。** 読み替えは
  // `editor.ts` / `study.ts` の `applyFlip`（見た目の位置 → 局面のマス）と、
  // ここの CSS クラス（駒台の置き場所と駒の向き）の 2 か所だけに閉じてある。
  // **他の場所で「反転しているなら…」と分岐を足さないこと。**
  //
  // ⚠️ **反転すると座標の表示も変わる**（筋が左から 1・2・…、段が下から
  // 一・二・… になるので **左下が 1一**）。盤の絵の座標は core/web が、
  // マスのツールチップは局面座標から作る側が、それぞれ勝手に付いてくる。
  //
  // **その場かぎりの値**（config.json には持たない）。起動のたびに
  // 「手前が先手」で始まる —— 中継の原則がそちらで、切り替えは
  // 連続モードのチェックと同じくその場の操作だから。
  //
  // ⚠️ **訂正タブと解析タブで 1 つの値。** 片方だけ反転していると、
  // 採った局面が上下逆に出てきて何が起きたのか分からなくなる
  // （値そのものは `showStudyHand` より前で宣言してある）。
  const studyBoardWithHands = root.querySelector<HTMLElement>("#study-board-with-hands")!;

  // paintFlipButton はボタン 1 つぶんの見た目（文字・ツールチップ・押下状態）。
  const paintFlipButton = (b: HTMLButtonElement, flip: boolean) => {
    b.textContent = flip ? "手前: 後手" : "手前: 先手";
    b.title = flip
      ? "手前が後手（先手が奥）。押すと手前が先手に戻ります。盤の向きが変わるだけで、局面は変わりません"
      : "手前が先手（後手が奥）。押すと手前が後手になります。盤の向きが変わるだけで、局面は変わりません";
    b.setAttribute("aria-pressed", String(flip));
  };

  // ⚠️ **訂正タブは反転しない**（2026-08-18 にボタンごと外した）。ここに
  // `board.toggleAttribute("flip", ...)` を戻さないこと —— 盤は撮ったとおりで固定する。
  const applyViewpoint = () => {
    studyBoard.toggleAttribute("flip", studyFlipped);
    studyBoardWithHands.classList.toggle("is-flipped", studyFlipped);
    studyBoardUI.setFlip(studyFlipped);
    // 駒台は「逆順に並べる側」が入れ替わるので並べ直す。
    showStudyHand(studyHands);
    paintFlipButton(studyFlip, studyFlipped);
  };

  // setStudyViewpoint は**解析タブの**視点を決める。**「自分がどちら側か」を渡す**
  // （新規対局の「あなたの手番」と、採ったときの「撮った盤の目線」がここに入る）。
  const setStudyViewpoint = (black: boolean) => {
    if (studyFlipped === !black) {
      return;
    }
    studyFlipped = !black;
    applyViewpoint();
  };

  // 反転中なら「手前が先手」に戻し、そうでなければ「手前が後手」にする。
  studyFlip.addEventListener("click", () => setStudyViewpoint(studyFlipped));
  // ⚠️ **一度は通すこと。** ボタンの文字（「手前: 先手」）はここで入れているので、
  // 通さないとラベルが空のボタンが出る。
  applyViewpoint();

  // applySettings は設定のうち**解析タブに効くもの**を入れる（`mainscreen.ts` の applySettings から）。
  const applySettings = (s: AppSettings) => {
    // エンジンの色（評価値グラフ・見出しの色見本）。**設定が唯一の出所**で、
    // 既定色の解決も Go 側が済ませてある（`EngineSettings.Color` は常に入っている）。
    // ⚠️ **評価値グラフの置き場所も設定から受け取る**（2026-09-08）。
    // Go 側は起動時にも `graph:detached` を出すが、**その時点でフロントはまだ
    // 購読していない**ので、起動直後の形はここで決まる。
    layout.applyGraphDetached(!!s.evalGraphDetached);
    layout.applySideDetached(!!s.studyPaneDetached);
    layout.applyMovesDetached(!!s.movePaneDetached);
    // ⚠️ **評価値グラフの縦軸も設定から受け取る**（2026-09-13）。切り替えるのは
    // グラフの見出しだが、**起動直後にどちらで始まるかはここで決まる**
    // （切り離した窓で切り替えたときも `settings:changed` でここを通る）。
    evalGraphUI.setMode(s.evalGraphAxis as EvalMode);
    // ⚠️ **盤の上の 1 行の出し入れも設定から受け取る**（2026-09-10）。切り替えは
    // 黒地の右クリックだが、**起動直後にどちらで始まるかはここで決まる。**
    winrateHidden = !!s.hideWinRateBar;
    playersHidden = !!s.hidePlayerNames;
    applyStudyTopRow();
    winrateFlipped = !!s.flipWinRateBar;
    applyWinRateFlip();
    // ⚠️ **評価値グラフの折れ線の色はここが持つ**（側の列とは別）。
    // 切り離すと側の列は別の窓に居るので、**あちらから引けない**。
    graphColors.clear();
    for (const e of s.engines ?? []) {
      graphColors.set(e.id, e.color);
    }
    // ⚠️ **側の列へは押し込むこと**（色・名前・候補手の本数・考える秒数）。
    // **設定が唯一の出所**で、既定色の解決も Go 側が済ませてある。
    sidePane.setEngines(s.engines ?? [], s.engineColors ?? [], s.analyzeSeconds, s.batchSeconds);
    // 棋譜の自動更新の間隔（設定ファイルだけで決まる。既定の解決は Go 側）。
    sidePane.setKifuFollowMinutes(s.kifuFollowMinutes);
    // 手順の下のコメント欄（2026-10-04）。**起動直後にどちらで始まるかはここで決まる。**
    sidePane.setShowComment(!!s.showMoveComment);
  };

  return {
    board: studyBoard,
    showStudy: (st) => showStudy(st),
    setStatus: (text) => sidePane.setStatus(text),
    setViewpoint: setStudyViewpoint,
    startFollow: () => followUI.start(),
    publishFollow: () => followUI.publish(),
    applySettings,
    leave: () => {
      // ⚠️ **連続解析は持ち主に止めさせること**（2026-09-12）。切り離していると
      // 回しているのは**別の窓**なので、こちらで `Stop()` しても
      // **今の 1 手が止まるだけで次の手が始まる**（`cancelBatch` が持ち主でなければ
      // `study:cancel` を飛ばす）。
      // ⚠️ **走っているときだけ頼むこと** —— 走っていないのに頼むと、
      // 向こうの面に「連続解析を止めました」とだけ出る。
      if (sidePane.stepping()) {
        sidePane.cancelBatch();
      }
      sidePane.release();
      void AnalyzeService.Release();
    },
    setTabShown: (on) => sidePane.setTabShown(on),
    shown: () => {
      studyBoardUI.relayout();
      // ⚠️ **グラフも測り直す。** 大きさは `clientWidth` で測っており、
      // **`display: none` の中では 0 になる**（測らないと出ないか、前回の
      // 大きさのまま残る）。盤のグリッドと同じ落とし穴。
      evalGraphUI.relayout();
      // ⚠️ **縦のスプリットバーの遊びもここで消す。** 幅の判定は**実際の盤を
      // 測って**行うので、**隠れているあいだは決められない**（測ると 0）。
      // これが無いと、開いた直後の 1 回目のドラッグが空振りする。
      layout.settle();
      // **解析タブに来たら（連続モードなら）そのまま解析を始める。**
      // まだ解析していない局面のときだけ動く（止めた解析を勝手に起こし直さない）。
      // ⚠️ **切り離しているあいだは向こうが持っている**（こちらは眠っている）。
      if (!layout.sideDetached()) {
        sidePane.reveal();
      }
    },
  };
}
