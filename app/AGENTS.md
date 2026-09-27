# app の AGENTS.md

**フロントに公開する Service**（設定・訂正・解析・棋譜・駒の字・学習送信・計測）。
⚠️ **wails3 を import しない** —— Wails の口（ダイアログ・イベント）は `FilePicker` /
`EventEmitter` として `_cmd` から差し込む。⚠️ **8 つの Service は非公開メソッドで互いに
繋がっているのでパッケージを割らない**（公開すると bindings に出てフロント API になる）。⚠️ **`PositionService`（訂正タブ）と
`StudyService`（解析タブ）は別の局面**で、繋がるのは `Adopt` の 1 か所だけ。

⚠️ **「受け渡しは `Adopt` の 1 か所だけ」は訂正タブとの受け渡しの話。**
入力の口が増えること自体は想定どおりで、棋譜は訂正タブを経由せず
`StudyService.LoadKifu` が直接根を入れ替える（**棋譜の局面は初期局面と手順で
一意に決まる**ので、直すものが無い）。

## ⚠️ 触る前に読む文書（**自動では読まれない**）

**このディレクトリの制約は、話題ごとに `_docs/app-*.md` へ分けてある**（2026-09-28。
1 枚で 1,600 行あった）。**この表で触るファイルを引き、その文書を開いてから直すこと。**
下に残してあるのは**どの Service を触っても効く**ものだけ。

| 触るもの | 読む文書 |
|---|---|
| `StudyService.LoadKifu` / `LoadKifuURL` / `ReloadKifu` / `FollowKifu`・`kifufetch.go` | `_docs/app-kifu-load.md`（棋譜を読む・URL・再読み込み・自動更新） |
| `StudyService.NewGame`・`PositionService.LoadHandicap` / `LoadEmpty` / `SetHandsFixed` / `SetMateProblem` | `_docs/app-handicap-mate.md`（新規対局・手合割・詰将棋。全体像はスキル `ikkyoku-handicap-mate`） |
| `kifuservice.go`（棚・カード・仮の一覧） | `_docs/app-shelf.md` |
| `PositionService.SetViewpoint` / `adoptPosition`（目線） | `_docs/app-viewpoint.md` |
| `settings.go`（エンジンの登録・option）・`analyzeservice.go` | `_docs/app-engines.md` |
| `followstudy.go`（`FollowProbe` / `FollowApply` / `FollowAuto`） | `_docs/app-follow.md`（調整とデバッグはスキル `ikkyoku-follow`） |
| `StudyService.PlayLine`・`evalgraph.go` | `_docs/app-study.md` |
| `studystore.go` / `studyrecord.go` / `studykey.go` / `studyexport.go` | `_docs/app-studystore.md`（検討の控え・棚と結ぶ・出どころの鍵） |
| `fontservice.go` | `piecefont/AGENTS.md`（テストの意図は下の「テスト」） |
| `trainingservice.go` | `training/AGENTS.md` |
| 画面の振る舞い | `_cmd/ikkyoku/frontend/src/AGENTS.md` → `_docs/ui/*.md` |

⚠️ **書き足すときも同じ置き場所に。** ここ（`app/AGENTS.md`）に積むと、また 1 枚で
読めない長さに戻る。**ここに足してよいのは「どの Service を触っても効く」制約だけ。**

## 訂正タブ ⇄ 解析タブの継ぎ目

- **訂正タブに居ること自体が訂正モード。** トグルは廃した（`editor.ts` の
  `setEditing` は無い）。**モードを戻さないこと** —— タブが意味の境界になっている
- **受け渡しは `StudyService.Adopt` の 1 か所だけ。** 確定しているかの判定も
  そこがする（`position.Position.SFEN()` が組み上がるか）。⚠️ **フロントで同じ
  判定を書かない**（2 か所に散る）
  ⚠️ **違反（二歩・玉 1 枚・駒数超過）は見ない。** 断るのは**未決が残っているとき**
  だけ —— 見ると**詰将棋が確定できなくなる**（`_docs/design-position.md`「詰将棋のような
  『正常でない局面』も確定できること」）
- **解析タブ → 訂正タブへ戻れる。** 戻って直し、もう一度確定すればよい。そのとき
  **前の手順と解析結果は捨てる**（別の局面の話になるため）。捨てるのは `Adopt` が
  根ごと入れ替えることで担保しており、フロントの片付けに依存していない
- ⚠️ **撮っても解析タブは消さない**（2026-08-22 に変えた。それまでは撮れた時点で
  `StudyService.Clear` していた）。**撮ることと、解析している局面を捨てることは
  別の操作** —— **訂正した局面を suteme へ送るために撮る**使い方（学習データを
  育てるループ。「決定: 訂正した局面は学習にも回す」の節）では、撮るたびに
  検討が消えると**解析しながら盤を追加する**ことができない。棋譜を読んで解析して
  いる最中に 1 枚撮ったときも同じで、**あちらは訂正タブを通らない入口**なのに
  巻き添えで消えていた。
  - ⚠️ **捨てるのは「この局面を解析する」を押したとき**（`StudyService.Adopt` が
    根ごと入れ替える）。**そこが訂正タブと解析タブの唯一の継ぎ目**なので、
    撮った時点で先回りして消す必要が無い。**`showShot` に戻さないこと**
  - **走っている解析は止まる**（撮ると訂正タブへ移り、`selectTab` がエンジンを
    手放す）。⚠️ **そこまでの評価値は残る** —— タブを移ったときと同じ約束
  - ⚠️ **「古い評価値が今の 1 枚に対する評価に見える」は残る懸念だが、
    それは訂正タブの話ではない** —— 撮ったあと開くのは訂正タブで、解析タブには
    その局面の盤・SFEN・手順が出ている。**採るまでは別の局面**という元からの
    線引き（`PositionService` / `StudyService`）そのもの

## ⚠️ Service への差し込みは公開フィールド（メソッドにしない）

**`KifuService.Store`**（2026-09-16 に `SetStudyStore` から直した）。
**Service の公開メソッドはそのままフロントの API になる**ので、メソッドで差すと
**`StudyStore` まで bindings のモデルに出てくる。**
`SettingsService.PickFile` / `AnalyzeService.Emit` / `StudyService.Emit` と同じ形にする。

⚠️ **bindings を生成したら Methods と Models の数を見ること** —— 意図せず増えて
いたら、公開したくないものが漏れている。

## 局面が変わったら知らせる（`study:changed`）— **2026-09-08**

**`StudyService` は局面を変えるたびにイベントを流す。** `app.Event.Emit` は
**アプリ全体**に届くので、**呼んでいない窓もこれで気づける**。

```
以前:  メイン画面 ──GoTo()──> StudyService ──戻り値──> メイン画面が描く
今:    メイン画面 ──GoTo()──> StudyService ─┬─戻り値──────> 呼んだ窓がその場で描く
                                            └─study:changed─> **全部の窓**が描く
```

⚠️ **ペインを別ウィンドウへ切り離すための土台**（2 枚目・3 枚目の窓はこれに乗る）。
**今は見た目が 1px も変わらない** —— メイン画面は今までどおり戻り値でも描いており、
イベントは自分の描いた版と同じなので弾かれる。

- ⚠️ **変えたら必ず出すこと。** 出す場所は `Adopt` / `LoadKifu`（＝`loadKifuFrom`）/
  `LoadKifuURL` / `ReloadKifu` / `NewGame` / `Play` / `PlayLine` / `AddLine` / `Branch` /
  `Promote` / `DropFrom` / `GoTo` / `Clear`。**足したメソッドで忘れると、
  そこだけ別の窓が更新されない**（画面からは「たまに古い」に見える）
- ⚠️ **失敗したときは出さない**（状態は変わっていない）。判断は `publish` の 1 か所
- ⚠️ **ロックを外してから出すこと。** 先はフロントなので、ロックを持ったまま渡すと
  そこから戻ってきた呼び出しと噛み合う余地がある。**`defer` は LIFO** なので、
  各メソッドの**先頭**に `defer func() { s.publish(...) }()` を置いて
  `s.mu.Unlock()` より後に走らせている。**登録の順番を入れ替えないこと**
- ⚠️ **`StudyState.Rev`（版）を落とさないこと。** イベントと戻り値は**別の経路**なので
  **順番が入れ替わりうる** —— 受け取る側は「**既に描いた版より新しいときだけ描く**」で
  弾く（`mainscreen.ts` の `studyRev`）。無いと、十字キーで手を続けて辿ったときに
  **古い局面が後から届いて盤が戻る**
- ⚠️ **`rev` を進めるのは `changed()` だけ。** 読むだけの `state()` では進めない ——
  進めると**何も変えていないのに全部の窓が描き直す**
- ⚠️ **棋譜の読み込みは「取得元まで入った 1 回」で出すこと**（`loadKifuFrom` に
  一本化した）。以前は `LoadKifu` が読んでから取得元を入れ直していたので、
  イベントにすると**取得元が空の状態が 1 回ぶん外へ漏れる**
  （別の窓で再読み込みのアイコンが出たり消えたりする）
- ⚠️ **`Emit` が nil でも動くこと**（設計原則3）。呼んだ窓は戻り値で描けるので、
  効かなくなるのは**窓どうしの連動だけ**
- ⚠️ **評価値グラフ用のイベントは足していない。** あちらは `analyze:info` /
  `analyze:done`（既にアプリ全体へ流れている）＋この `study:changed` で足りる

## テスト（`go test ./...`）

⚠️ **ここに書いてあるのは「何の歯止めか」。消すときはその歯止めが要らなくなったのかを先に確かめること。**
**テストの意図は、そのテストが守っている文書の「テスト」節にある。**

| テスト | 文書 |
|---|---|
| `kifufetch_test.go` / `kifufollow_test.go` | `_docs/app-kifu-load.md` |
| `kifuservice_test.go` | `_docs/app-shelf.md` |
| `viewpoint_test.go` | `_docs/app-viewpoint.md` |
| `settings_test.go` / `analyzeservice_test.go` | `_docs/app-engines.md` |
| `handsfixed_test.go` / `mateengine_test.go` | `_docs/app-handicap-mate.md` |
| `followstudy_test.go` | `_docs/app-follow.md` |
| `studyservice_test.go`（`PlayLine`）・`evalgraph_test.go` / `evalgraph_end_test.go`・`players_test.go` | `_docs/app-study.md` |
| `studystore_test.go` / `studyexport_test.go` / `studykey_test.go` | `_docs/app-studystore.md` |

- `fontservice_test.go` — **駒の字**（2026-08-16）。⚠️ **一番の要点は
  「同梱に戻れること」** —— 登録を消しても、選んでいたフォントが消えても、
  **同梱の字で盤が描けること**（設計原則3）。ほかは:
  登録すると**そのまま使う状態になる**こと（登録は「この字で見たい」なので
  もう一度選ばせない）、同じ書体を 2 回足しても増えないこと、
  ⚠️ **family が同梱（`ShogiSFEN`）と必ず違うこと**（同名で上書きすると
  **同梱に戻せなくなる**）、⚠️ **見本の family が毎回変わること**
  （使い回すと**前のフォントのまま**に見える）、
  ⚠️ **一覧に無い ID を指しているときだけ同梱として返し、
  登録が一覧にあるなら焼けなくても選択は残すこと**（同梱を選んでいるように
  見せると、どれを選んだのか分からなくなる）、
  一覧が**字の足りないフォントも返すこと**（消すと、探しているのか対象外なのかが
  画面から分からない）、字が足りないものは**足りない字を出して断ること**。
  ⚠️ **`SettingsService` は `path` を一時ディレクトリに向けて手で組むこと**
  （`NewSettingsService` は `os.UserConfigDir` を見るので**本物の設定ファイルを
  書き換える**）。端末のフォントを要る部分は skip する（`piecefont` と同じ理由）。
  **王/玉と馬/左馬**（`pieceStyle`）もここ: ⚠️ **一番の要点は
  「盤に渡す属性と、自前で描く駒に渡す値が食い違わないこと」** ——
  「後手だけ玉」で**先手側が `normal` のままであること**（`K` と `k` は
  フォント上で同じグリフなので、まとめて当てると先後を分けられない）。
  ほかは**両方のときは属性の値が空**であること（＝値なし。両方を指す決まった
  書き方）、⚠️ **玉と左馬が 1 つの値にまとまること**
  （`font-feature-settings` は個別の値が積み上がらない）、
  **当てるものが無くても `"normal"`**（空文字は CSS の値として不正）、
  玉を戻しても左馬が残ること（別の設定なので巻き添えにしない）。
  **駒の字の色**（`SetPieceInk`）もここ: 既定が解決済みで返ること・
  ⚠️ **既定と同じ値を書き残さないこと**（既定を変えたときに古い値で固まる）・
  ⚠️ **読めない色と範囲外を断らずに丸め、丸めた結果をそのまま返すこと**
  （画面に何が起きたかが出る）・**色を変えても玉/左馬が巻き添えにならないこと**
