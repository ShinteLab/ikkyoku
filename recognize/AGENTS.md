# recognize の AGENTS.md

**画像 → 盤面。`suteme` を呼ぶだけの層。** ⚠️ **認識器をここに書かない**（suteme の責務）。
⚠️ **Phase 3 の局面矯正層もここに入れない** —— あちらは**画像を一切持たない**層
（`position`）。境界は「`recognize` が返した盤面を受け取ったら、そこから先は画像を見ない」。

## 使う API（`github.com/ShinteLab/suteme`）

| 関数 | 用途 |
|---|---|
| `Recognize(img image.Image, opts ...Option) (*Result, error)` | **画像 → 盤面。これが本命の継ぎ目。** 盤面・信頼度・駒数・検証結果まで 1 回で返る |
| `LoadSFEN(img image.Image, opts ...Option) (string, error)` | 盤面文字列だけでよいとき。`Recognize` の薄い皮 |
| `LoadPredictor(dir string) (Predictor, error)` | dir から駒種推論器を読む（下記） |
| `SetPredictor(p Predictor)` | 以後の認識が使う推論器を差し替える。`nil` で既定探索に戻る（⚠️ ikkyoku は既定探索を使わない。`FromImage` が手前で断る） |
| `LoadStripData(path string) ([]StripSample, error)` / `NewStripJudge` | 盤の縁の帯の判定器を読む（下記） |
| `SetStripJudge(j *StripJudge)` | 以後の検出が使う帯の判定器。⚠️ **`nil` は「使わない」**。既定探索に戻すのは `ResetStripJudge` |

**`ValidatePieces` / `PieceValidation` は無くなった。** 検証は `Recognize` に統合され、
`Result.Warnings()`（日本語メッセージ）/ `Result.Violations`（種類つき）/ `Result.HandTotal` で取る。

### オプション（`suteme.Option`）— ikkyoku は既定のまま呼ぶ

`suteme` の既定は「全部調べるが、エラーにはしない」。**これが ikkyoku の設計原則3
「段階的に劣化すること」とそのまま一致する**ので、`recognize.FromImage` はオプションを
何も渡さない。駒数が合わない盤面でも撮った 1 局面は解析させたく、「おかしい」は
`Warnings` として UI に出して訂正 UI（Phase 5）で直す、という流れになる。

| オプション | 使うか |
|---|---|
| `WithErrorOn(c sfen.Check)` / `WithStrict()` | **使わない。** 違反をエラーにすると「撮ったのに何も出ない」になる |
| `WithHandTo(side)` | **使わない。** 先後の割り振りは盤面から決まらない（設計原則5）。人間が決める |
| `WithTurn` / `WithMoveNumber` | 未使用。手番を持つのは Phase 3 の局面矯正層の仕事 |
| `WithPredictor(p)` | 未使用。学習データの指定は `SetPredictor` 側で一括して行っている（下記） |
| `WithRegion` / `WithRect` | 未使用。ガイド枠で盤だけを切り出している前提。将来ガイド枠の座標を渡す余地はある |

`recognize.FromImage` は `...recognize.Option`（= `suteme.Option` の型エイリアス）を
受け取るので、必要になった呼び出し側がその場で指定できる。**ikkyoku 側でオプションを
包み直さないこと**（チェックの種類は `core/sfen` の語彙で、包むと二重定義になる）。

## ⚠️ 学習データの置き場所は ikkyoku が決める

⚠️ **`SutemeDataDir` から読むものは 2 つある。**

| ファイル | 読む先 | 無いとどうなるか |
|---|---|---|
| `training_data_v*.bin` / `model_v*.json` | `recognize.LoadDir` → `Set.Use` → `suteme.SetPredictor` | 駒種が読めない（その段は使えない）|
| `strip_data_v1.bin` | 同じ `LoadDir` の 1 組 → `suteme.SetStripJudge` | **盤の位置が 1マス滑ったまま信頼度 100% で返る**（下記）|

`LoadSFEN` は駒種推論器を必要とし、`suteme` は既定で
**カレントディレクトリ → 実行ファイルのディレクトリ**の順に
`training_data_v3.json`（k-NN 優先）/ `model_v3.json` を探す。
どちらも 3.5MB 級で、**しかも育て続けるファイル**。

**実行ファイルの隣にコピーを置く運用は取らない。** 更新のたびにコピーし直す必要があり、
古いデータで認識する事故が起きる。代わりに `ikkyoku.Config` の `SutemeDataDir` で
場所を指し、`recognize.LoadDir` → `Set.Use` で読み込む。
開発中は `suteme` のリポジトリを直接指しておけばよい:

```json
{ "sutemeDataDir": "D:/Go/Projects/shinte/suteme" }
```

（`os.UserConfigDir()/ikkyoku/config.json`。**JSON なのでパスはスラッシュ区切りで書く**）

`SutemeDataDir` が空ならこの段は無い（⚠️ **suteme 既定の探索には任せない**。2026-10-04）。
**配布するときは「バイナリに焼き込む」経路を使う**（次節）。

- **ファイルのシンボリックリンクで代用しようとしないこと。** Windows では管理者権限
  （または開発者モード）が要る。ジャンクションはディレクトリ専用で、`suteme` が探すのは
  「そのディレクトリ直下の 2 ファイル」なので代用にならない
- **`suteme` は一度読んだ推論器をキャッシュする。** 学習データを更新しても、
  再読み込みするまで反映されない。メイン画面の「認識器を再読み込み」
  （`CaptureService.ReloadRecognizer`）がその入口。訂正 → 学習データ更新 → 撮り直す、
  というループにアプリの再起動を挟まないため
- 起動時に先に読み込んでおく（撮った瞬間に 3.5MB の読み込みで待たされないように）。
  表示のためだけに読み直さないよう、状態取得は `CaptureService.Recognizer`（読み込まない）に分けてある

### 盤の縁の帯の判定器（`strip_data_v1.bin`）を落とさないこと

**片方だけ配線していて実際に事故った**（2026-08-22）。`SutemeDataDir` は
駒種推論器にしか繋がっておらず、帯の判定器は `suteme` 既定の探索
（カレント → 実行ファイルの隣）に落ちていた。ikkyoku はどちらでもないので、
**駒種は最新の学習データ・盤の位置合わせは学習前**という状態で動いていた。

効くのは**盤の外枠線が画像の外に出ているキャプチャ**。枠が盤の縁ぴったり
（あるいは内側）だと、検出した窓が 1マス滑っても格子線には乗るので
`ValidateBoard` では見分けが付かない。`suteme` がこれを直す唯一の手立てが
帯の判定器（`unslipByJudge`）で、**判定器が無ければ何もしない**。

実測（`captures/20260822-095350.png`、1203x961。盤の下端 y=968 が画像の外）:

| | 検出した外枠 | 信頼度 |
|---|---|---|
| 判定器 なし | `(223, 32)-(974,875)` | **1.00**（1マス上へ滑っている）|
| 判定器 あり | `(223,125)-(974,968)` | 1.00 |

同じ画像を `suteme-training`（カレントが `suteme` のリポジトリ＝判定器を拾える）で
開くと盤が合う、という食い違いがこれ。**ズレの原因を撮り方や認識器に求める前に、
まず帯の判定器が読めているかを見ること**（メイン画面の「認識器」の行に出る）。

- ⚠️ **推論器と判定器は同じ段から 1 組で差し替える**（`recognize.Set.Use`。2026-10-04）。
  **判定器が無い組では判定器を「使わない」にする**（`SetStripJudge(nil)`）—— 前の組の判定器を
  残すとちぐはぐになり、既定の探索へ戻すと exe の隣やカレントディレクトリを見に行く。
  判定器が無くても組としては使う（認識はできる。盤の位置が 1 マス滑ることがあるだけ）
- どの段も読めないときは `recognize.Clear` で外す（判定器も外す。盤の位置合わせ
  `DetectRegion` は判定器なしで動く）
- 状態は `RecognizerStatus.StripSamples` / `StripError` で別に持つ。
  **`Ready` / `Error` にまとめない** —— まとめると帯データを置き忘れているのに
  「認識器: OK」と出て気づけない。画面では警告色（`.recognizer.is-warn`）で出す
- 回帰テストは `recognize/predictor_test.go`

## 認識器は 3 段（**学習データ / 配布モデル / 焼き込み**。2026-10-04）

**決定の理由は `_docs/design-capture.md` の「決定: 認識器は 3 段」。** ここは触るときの制約。

| 段 | 読む口 | 設定 |
|---|---|---|
| 1. 学習データ（生の .bin） | `LoadDir` | `sutemeDataDir` |
| 2. 配布モデル（suteme の配布用の書き出し: `training_data_v8.bin(.gz)` / `strip_data_v1.bin(.gz)` / `export.json`） | `LoadPack`（置かれていなければ `ErrNoPack`） | `sutemeModelDir`（空なら `%LOCALAPPDATA%\ikkyoku\model`） |
| 3. 焼き込み（2 と同じ形。`_cmd/ikkyoku/model/` を埋め込んで `SetEmbedded` で渡す） | `LoadEmbedded` | — |

どれも**読んで 1 組（`Set`）を返すだけ**で、差し替えるのは `Set.Use`。どの段を使うかは
`_cmd/ikkyoku/captureservice.go` の `loadRecognizer` / `recognizerOrder`。

- ⚠️ **置かれていない（`ErrNoPack`）と読めないを分けること。** 既定の置き場所が空なのは
  普通の状態で、⚠ に出すことではない
- ⚠️ **配布モデルと焼き込みの読み方は共用**（`LoadPackFS`。置いたものは `os.DirFS`、焼き込みは
  埋め込んだ `fs.FS`）。片方だけ直すと、置いたモデルと焼き込みで振る舞いが変わる
- ⚠️ **配布セットの形は suteme の書き出しそのもの**（ikkyoku 独自の名前を持たない）。
  **読めるファイル名は exe に入っている suteme の版で決まる**（`suteme.DefaultDataFile`）ので、
  版が違う書き出しは「版が合わない」と言って断る（`ErrNoPack` ではない。⚠ に出して下へ落ちる）。
  `.gz` を先に見て、無ければ圧縮しない版を読む
- ⚠️ **日時は書き出しの記録 `export.json` の `date` で読む**（suteme の `training.ExportInfo`）。
  無ければゼロで、**古いと見なさない**（`Set.OlderThan`）
- ⚠️ **`FromImage` は認識器を差し替えていなければ suteme を呼ばない**（`ErrNoRecognizer`）。
  呼ぶと suteme 既定の探索に落ちる

切り替えは**設定タブ「認識器の読み込み元」**。変えたその場で読み直す
（`SettingsService.SetSutemeSource` → `CaptureService.applyRecognizerSource`）。

- ⚠️ **`auto` を「焼き込み優先」にしないこと。** 焼き込みは配布用に固定したデータ、
  学習データは育て続けるデータ。開発中（＝学習データを指している状態）に
  焼き込みへ倒れると、**学習データを更新しても反映されない**という
  最も気づきにくい事故になる。指定してあるほうがユーザーの意思表示なのでそちらを採る
- ⚠️ **見始めた段から下を見たあと、上の段へも回る**（`recognizerOrder`）。「焼き込みから」を
  選んでいても、焼き込みの無いビルドでは学習データ・配布モデルで動く。
  設定ファイル（`config.json`）は配布ビルドと開発ビルドで共用されるので、
  「焼き込みで動かす設定のまま `wails3 dev` を動かす」は普通に起きる
- **どれも読めなくてもアプリは動く**（設計原則 3「段階的に劣化する」）。
  認識結果が空になるだけで、PNG の保存は成功したまま
- 実際にどこから読んだかは `RecognizerStatus.Mode`。**画面にはこちらを出すこと**
  （設定の値そのものではない）。⚠️ 焼き込みのとき `Source` は出所のラベルであって
  パスではないので、「実際に使われた推論器」との突き合わせ（`showPredictor` の
  「※設定と別の場所」）は `mode === "dir"` のときだけにしてある
- 解決は `recognizerOrder` / `loadRecognizer`（`_cmd/ikkyoku/captureservice.go`）の 1 か所。
  回帰テストは `_cmd/ikkyoku/recognizersource_test.go`（見る順と落ち方）と
  `recognize/predictor_test.go`（1 組の読み方・`ErrNoPack`・版違い・日時・焼き込み・`FromImage` の門番）

### 焼き込みの中身とビルド手順（2026-10-04 に `_cmd/ikkyoku/model/` へ移した）

**焼き込むのは `_cmd/ikkyoku/model/`（手元のモデル）で、`go:embed` を持つのは `_cmd/ikkyoku`**
（`embedmodel_on.go`）。`recognize` は `SetEmbedded(fs.FS)` で中身を受け取って読むだけ
（`embedded.go`）。⚠️ **`recognize` に `go:embed` を戻さないこと** —— 焼き込むモデルは
アプリ（exe）の持ち物で、置き場所もアプリの横（wails3 でビルドする場所）が自然。以前は
`recognize/model/` に置いていた（ルートの `.gitignore` に `/recognize/model/` だけ残してある）。

**手元のモデルを作るのは suteme**（ikkyoku のビルドは suteme を見ない）:

```powershell
cd ..\suteme
go run ./_cmd/suteme-training -export -gzip -out ..\ikkyoku\_cmd\ikkyoku\model   # 手元を作り直す
cd ..\ikkyoku\_cmd\ikkyoku
wails3 task build:embed                        # 手元をそのまま焼き込む(EXTRA_TAGS=embedmodel)
wails3 task build:embed MODEL_DIR=<フォルダ>     # 置いてある配布モデルを手元に写してから焼き込む
```

- ⚠️ **ビルドで suteme を見ないこと**（以前はビルドのたびに `suteme/dist` を持ってきていた）。
  何が焼き込まれるかがビルドした瞬間の suteme の状態で決まってしまう。**手元を入れ替えるのは
  suteme で書き出したときと `MODEL_DIR` だけ**（手順はスキル `ikkyoku-build`）
- ⚠️ **手元（`_cmd/ikkyoku/model/`）はフォルダごと git に入れない**（`_cmd/ikkyoku/.gitignore`）。
  名前で無視すると、ファイル名が変わったときに git に入る（以前の `*.gz` / `source.txt` がそうだった）
- **手元の形は配布モデルと同じ**（suteme の配布用の書き出しそのもの: `training_data_v8.bin.gz` /
  `strip_data_v1.bin.gz` / `export.json`）。zip にして配れば、受け取った人は既定の置き場所に
  展開するだけで使える
- **gzip で持つ**（suteme の `-gzip`）。実測 生 24.8MB → 10.6MB。`suteme` 側の口が `io.Reader` を
  取る（`PredictorFrom` / `StripJudgeFrom`）ので、展開したファイルを置く必要は無い
- **手元が空ならビルドが止まる**（`build:embed` の頭で suteme の書き出し方を出す。
  `wails3 build -tags embedmodel` なら `go:embed` がファイルを見つけられずに止まる）。それが狙い
- **ビルドが通ることでは足りない。** 中身が壊れていても `go:embed` は通るので、
  配布ビルドの前に `cd _cmd\ikkyoku; go test -tags embedmodel -skip Clipboard .`
  （`embedmodel_test.go`）で**実際に認識器として組み立てられること**を確かめる
- 通常のビルド（タグなし）には**データが入らない**（`embedmodel_off.go` が nil を返す）。
  開発中に 10MB をリンクすることはなく、`model/` が空でもビルドが通る

**タグを渡す口は 3 つある**（beta.3 で確認済み。どれも同じ
`go build -tags production,embedmodel ... -ldflags="-w -s -H windowsgui"` になる）:

| | 備考 |
|---|---|
| `task build:embed` | **これを使う。** 頭で手元のモデルを確かめ、何を焼き込むかを出す |
| `wails3 build -tags embedmodel` | CLI に `-tags`（カンマ区切り）がある。手元のモデルは確かめない |
| `task build EXTRA_TAGS=embedmodel` | Taskfile の変数を直接渡す形 |

- ⚠️ **`BUILD_FLAGS` を上書きしないこと。** あちらには `-tags production` も
  `-H windowsgui`（コンソールを出さない）も入っている。タグを足す口は `EXTRA_TAGS`
- ⚠️ **`wails3 dev` と `wails3 package` に `-tags` は無い。** 開発モードで焼き込みを
  試すなら `task build:embed` した exe を直接起動する

## 分かっていること（設計に効く）

- **`Recognize` が駒数保存則で持ち駒を逆算する。**
  盤上の駒数から「駒台にあるはずの枚数」(`Result.HandTotal`) を出す。つまり
  **駒台を画像認識しなくても持ち駒が埋まる。** ただし **先後の割り振りは付かない**
  （`WithHandTo` で寄せられるが、それは便宜的な決め打ち）。Phase 3 の設計原則
  「手番は局面から決まらない」と同根の制約で、**どちらも UI で人間が決めるのが最も安い**

  ⚠️ **これは「駒台は撮らなくてよい」という意味ではない。** 逆算で代用できているのは
  **今の `suteme` に駒台を読むロジックが無いから**であって、駒台の情報が要らないからではない。
  駒台が読めれば先後の割り振りが画像から付く（＝逆算では原理的に出ない情報が手に入る）。
  **ikkyoku 側が盤しか撮れないわけでもない**（ガイド枠を広げれば駒台も入る）。
  suteme に駒台のロジックが入ったら、撮る範囲も含めてここは見直す前提で読むこと
- `Result.Warnings()` がそのまま「ここが怪しい」の提示に使える。
  **矯正層の第一歩は自前で書くのではなく、これを UI に出すこと**。
  種類で選り分けたいときは `Result.Violations` / `Result.Filter(sfen.Check)`
- **`Result.Confidence` は「盤を映していない画面を撮った」の検出に効く。**
  局面の中身がおかしい（＝`Warnings`）のとは別の軸で、認識結果を疑う入口になる
- **`Result.Black` / `Result.White` は盤上に見えている駒数**（成駒はベース駒に合算）。
  `HandTotal` の逆算根拠なので、訂正 UI で「どの駒を数え間違えたか」を突き合わせられる
- **`DetectBoard` はガイド枠の自動フィットに転用した**（枠のツールバーの □ ボタン →
  `CaptureService.FitFrame`）。**枠を盤に寄せること自体が認識精度に効く**（下記）。
  詳細は「ガイド枠の自動フィット」節
- **`Result.Debug` が「その答えをどう出したか」を返す。**（`Recognize` が常に埋める）
  `Confidence` は「怪しい」までしか言えず、**座標がずれているのか駒種を外しているのかを
  切り分けられない**。`Debug` はそこに要る材料で、ikkyoku は次を使っている:

  | フィールド | ikkyoku での使い道 |
  |---|---|
  | `Region` / `Cells[].Rect` | **撮った画像に重ねる**（「認識詳細情報」）。外枠だけだと 1 マスのずれが見えないので格子まで描く |
  | `RegionSource` | 領域の決め方。**`whole` は異常ではない**（ガイド枠が盤にぴったり合っているほどこの経路になる）ので警告にしない |
  | `Cells[].Confidence` | 低いマスを重ね表示で目立たせる。`Warnings` は結果しか言わないので、**どの駒が犯人か**はこれでしか出せない。訂正 UI（Phase 5）で最初に直す候補にもなる |
  | `Predictor` | **実際に読まれた学習データ**（種別・パス・サンプル数）。config に書いたディレクトリと違って「本当に何を読んだか」なので、育て続けるデータで**古いまま認識していた事故**に気づける |

  `ImageBounds` は重ね表示の座標系に、`Confidence` は `Result.Confidence` と同値。
  `BoardColor` は今のところ使っていない。**認識の判断には使わないこと**（観測用）


- `recognize` パッケージ（ルートモジュール）が `suteme.Recognize` を呼び、`Board` にまとめる。
  **認識器はここに書かない**（suteme の責務）。
  **⚠️ Phase 3 の局面矯正層をここに入れない**（2026-08-07 に方針変更。以前は「ここに入る」と
  書いてあった）。`recognize` は**画像を知っている層**で、訂正から先が扱うのは
  **画像を一切持たない「とある局面」**（誰と誰の対局か・何手目か・そこから何を選んだか）。
  混ぜると、局面を持ち回るコードが画像とその座標系を引きずる。
  画像は撮った PNG がディスクに残っていれば足りる（学習への還元もパスを渡すだけ）
- `CaptureService.Capture` が撮った直後に呼び、`CaptureResult` に
  `sfen` / `confidence` / `warnings` / `handTotal` / `recognizeError` / `debug` を載せて返す
- **`Debug` は `suteme.Debug` の型エイリアス**（`recognize.Debug`）。`Option` と同じく
  **包み直さない**。矩形もクラスも suteme が出した値そのもので、同じ形の型を定義し直すと
  変換のぶんだけ嘘が入る余地が増える。フロントも生成された bindings の型をそのまま使う
- **認識に失敗してもキャプチャは成功として扱う**（設計原則3）。PNG の保存は済んでおり、
  盤が出ない代わりに理由が UI に出るだけ。`recognizeError` がその理由。
  **既定のオプションでは、`recognizeError` が入るのは「盤そのものが取れなかった」ときだけ**
  （駒数が合わない程度では `warnings` に載るだけで盤は描かれる）
- `recognize.FromImage` は `WithErrorOn` 等でエラーにした場合でも `Board` を埋めて返す。
  「どこがおかしいか」を出しつつ盤も見せられるようにするため


## 想定される躓き

- **`suteme` の認識精度はまだ十分ではない。**
  ABEMA の CG 盤は毎回同じ描画なので本来はテンプレートマッチでほぼ解けるはずだが、
  現行の `suteme` がその前提でチューニングされているとは限らない。
  **精度が出なくても Phase 2 の失敗ではない。「今どれくらいか」が分かれば成功**
- **撮り溜めた PNG がそのまま `suteme` の学習データになる。**
  訂正 UI（Phase 5）で人間が直した結果が正解ラベルになるので、
  訂正 UI は認識精度の改善サイクルの一部でもある
- 実盤のカメラ映像（対局室の俯瞰）と CG 盤は**難易度が桁違い**。
  同じパイプラインに押し込まず、まず CG 盤で成立させること

## 追っている盤かどうかを見る（`Signature`。2026-09-15）

⚠️ **中継には大盤（解説用の盤）が映る。** そちらは**将棋の局面としては矛盾しない**
ので、**盤面だけを見ていては弾けない** —— 解説が本譜から 1 手の変化を並べていたら、
**その手をそのまま棋譜に足してしまう**（実機で出た話）。

**だから「盤面が繋がるか」とは別に「同じ盤が映っているか」を見る。**
使うのは `suteme` が既に返している観測値だけ（**盤の矩形**と**地色**）。

- ⚠️ **記述子に局面（駒の並び）を入れないこと。** 手が進めば変わるので、
  **同じ盤かどうかの判断には使えない**
- ⚠️ **マスタ（どの盤を追っているか）はここが持たない。** 取り出して比べるだけで、
  状態は呼び出し側（`_cmd/ikkyoku` の `CaptureService.boardAnchor`）。
  **画像を認識する層に「前の画像」を持たせない**（設計原則1）
- ⚠️ **判断できないときは「同じ」に倒す**（設計原則3）。記述子が取れないのは
  盤が映っていないときで、**それは一致度が既に落としている** ——
  重ねて落とすと**理由が 2 か所から出て画面が分からなくなる**
- ⚠️ **位置のずれはマス何個ぶんかで測る**（画素で持たない）。盤の大きさは中継に
  よって違うので、画素で書くと**小さく映る中継では緩すぎ、大きく映る中継では厳しすぎる**
- ⚠️ **地色では落とさない**（2026-09-15 に実機で外した）。**ゲーム画面は対局ごとに
  背景と照明が変わり、盤の地色もそれに引きずられる** —— 落とす条件にしていたせいで、
  **同じ盤を「別の盤」と言って追跡が丸ごと見送られた**（1 手目から進まなかった）。
  **判断に使うのは大きさと位置だけ。**
  ⚠️ **偽陽性より偽陰性のほうが重い** —— 別の盤を拾っても `position.Rank` の
  一致度と差がもう一度落とすが、**弾いたらそこで終わり**（黙って何も起きなくなる）。
  `ColorGap` は**ログに出すためだけ**に残してある（⚠️ **`Matches` に戻さないこと**）
- ⚠️ **記述子は「画像の中の座標」。枠を動かすと必ず食い違う**（2026-09-15）。
  **枠を動かすのは狙いを直す操作**であって追う盤を変える操作ではないので、
  **撮影範囲が変わったらマスタを取り直す**（`CaptureService.anchorRegion`）。
  これが無いと「盤が映っていないのかも」と枠をずらした瞬間に追跡が死ぬ（実機で踏んだ）
- ⚠️ **地色だけで判断しないこと**（同じ中継でも露出で動く）。一番効くのは**大きさ**
- ⚠️ **既定の許容（大きさ 18% / 位置 0.8 マス / 色 34）は実測で決めた数ではない。**
  **実際の中継で当たり具合を見て調整すること**

⚠️ **本来は `suteme` の仕事**（画像の問いで、画素を持っているのはあちら）。
ここに置いてあるのは、**`suteme` が返している観測値だけで足りたから**。
**もっと確かな記述子が要るようになったら `suteme` に足すこと**（そのときも
**マスタを持たせない** —— あちらはステートレス・冪等が原則）。
