# recognize の CLAUDE.md

**画像 → 盤面。`suteme` を呼ぶだけの層。** ⚠️ **認識器をここに書かない**（suteme の責務）。
⚠️ **Phase 3 の局面矯正層もここに入れない** —— あちらは**画像を一切持たない**層
（`position`）。境界は「`recognize` が返した盤面を受け取ったら、そこから先は画像を見ない」。

## 使う API（`github.com/ShinteLab/suteme`）

| 関数 | 用途 |
|---|---|
| `Recognize(img image.Image, opts ...Option) (*Result, error)` | **画像 → 盤面。これが本命の継ぎ目。** 盤面・信頼度・駒数・検証結果まで 1 回で返る |
| `LoadSFEN(img image.Image, opts ...Option) (string, error)` | 盤面文字列だけでよいとき。`Recognize` の薄い皮 |
| `LoadPredictor(dir string) (Predictor, error)` | dir から駒種推論器を読む（下記） |
| `SetPredictor(p Predictor)` | 以後の認識が使う推論器を差し替える。`nil` で既定探索に戻る |
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
| `training_data_v6.bin` / `model_v6.json` | `recognize.UsePredictorFrom` → `suteme.SetPredictor` | 駒種が読めない（致命的）|
| `strip_data_v1.bin` | `recognize.UseStripJudgeFrom` → `suteme.SetStripJudge` | **盤の位置が 1マス滑ったまま信頼度 100% で返る**（下記）|

`LoadSFEN` は駒種推論器を必要とし、`suteme` は既定で
**カレントディレクトリ → 実行ファイルのディレクトリ**の順に
`training_data_v3.json`（k-NN 優先）/ `model_v3.json` を探す。
どちらも 3.5MB 級で、**しかも育て続けるファイル**。

**実行ファイルの隣にコピーを置く運用は取らない。** 更新のたびにコピーし直す必要があり、
古いデータで認識する事故が起きる。代わりに `ikkyoku.Config` の `SutemeDataDir` で
場所を指し、`recognize.UsePredictorFrom` → `suteme.SetPredictor` で読み込む。
開発中は `suteme` のリポジトリを直接指しておけばよい:

```json
{ "sutemeDataDir": "D:/Go/Projects/shinte/suteme" }
```

（`os.UserConfigDir()/ikkyoku/config.json`。**JSON なのでパスはスラッシュ区切りで書く**）

`SutemeDataDir` が空なら `suteme` 既定の探索に任せる。**配布するときは
「バイナリに焼き込む」経路を使う**（次節）。

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

- **帯の判定器は駒種推論器が読めなくても読む。** 盤の位置を合わせるだけなら
  駒種は要らない（ガイド枠の自動フィット `recognize.DetectRegion` がそれ）
- **読めなかったときに `suteme.SetStripJudge(nil)` を呼ばないこと。**
  あれは「判定器を使わない」の意味で、`SetPredictor(nil)` のように既定探索へ
  戻るのとは逆。既定探索まで止まる
- 状態は `RecognizerStatus.StripSamples` / `StripError` で別に持つ。
  **`Ready` / `Error` にまとめない** —— まとめると帯データを置き忘れているのに
  「認識器: OK」と出て気づけない。画面では警告色（`.recognizer.is-warn`）で出す
- 回帰テストは `recognize/predictor_test.go`

## 認識器の読み込み元は 3 通り（**焼き込み / ディレクトリ / 既定探索**）

**exe 1 つで配れる形と、学習データを育てながら使う形の両方が要る。** 前節のとおり
開発中はディレクトリを指すのが正しいが、**配る相手の環境に `suteme` のリポジトリは無い。**
「exe の隣に .bin を 2 つ置いてください」は説明のいる運用なので、
**配布ビルドは認識器をバイナリに焼き込む**（`-tags embedmodel`）。

| 設定 `sutemeSource` | 読み込み元 |
|---|---|
| `"auto"`（既定。空も同じ）| `SutemeDataDir` があればそちら → 無ければ焼き込み → それも無ければ `suteme` 既定探索 |
| `"dir"` | `SutemeDataDir`（焼き込みがあっても使わない）|
| `"embed"` | 焼き込んだデータ（**焼き込みの無いビルドでは `dir` へ落ちる**）|

切り替えは**設定タブ「認識器の読み込み元」**。変えたその場で読み直す
（`SettingsService.SetSutemeSource` → `CaptureService.applyRecognizerSource`）。

- ⚠️ **`auto` を「焼き込み優先」にしないこと。** 焼き込みは配布用に固定したデータ、
  ディレクトリは育て続けるデータ。開発中（＝ディレクトリを指している状態）に
  焼き込みへ倒れると、**学習データを更新しても反映されない**という
  最も気づきにくい事故になる。指定してあるほうがユーザーの意思表示なのでそちらを採る
- ⚠️ **`"embed"` を選んでいても、焼き込みの無いビルドでは `dir` へ落とす。**
  設定ファイル（`config.json`）は配布ビルドと開発ビルドで共用されるので、
  「焼き込みで動かす設定のまま `wails3 dev` を動かす」は普通に起きる
- **どれも読めなくてもアプリは動く**（設計原則 3「段階的に劣化する」）。
  認識結果が空になるだけで、PNG の保存は成功したまま
- 実際にどこから読んだかは `RecognizerStatus.Mode`。**画面にはこちらを出すこと**
  （設定の値そのものではない）。⚠️ 焼き込みのとき `Source` は出所のラベルであって
  パスではないので、「実際に使われた推論器」との突き合わせ（`showPredictor` の
  「※設定と別の場所」）は `mode === "dir"` のときだけにしてある
- 解決は `resolveRecognizerSource`（`_cmd/ikkyoku/captureservice.go`）の 1 か所。
  回帰テストは `_cmd/ikkyoku/recognizersource_test.go`（**タグの有無どちらでも通る**
  ように、期待値を `recognize.EmbeddedAvailable()` で切り替えている）

### 焼き込みの中身とビルド手順

**データはこのリポジトリに置いていない**（`recognize/model/` は `.gitignore`）。
20MB 級のバイナリを、学習し直すたびにコミットすることになるため。
配布ビルドの前に `suteme` の `dist/` から持ってくる:

```powershell
cd ikkyoku\_cmd\ikkyoku
task model:copy         # suteme/dist → recognize/model/*.gz + source.txt
task build:embed        # model:copy + wails3 のビルド(EXTRA_TAGS=embedmodel)
```

**タグを渡す口は 3 つある**（beta.3 で確認済み。どれも同じ
`go build -tags production,embedmodel ... -ldflags="-w -s -H windowsgui"` になる）:

| | 備考 |
|---|---|
| `task build:embed` | **これを使う。** `model:copy` が前に付くので忘れない |
| `wails3 build -tags embedmodel` | CLI に `-tags`（カンマ区切り）がある。データのコピーは別途 |
| `task build EXTRA_TAGS=embedmodel` | Taskfile の変数を直接渡す形 |

- ⚠️ **`BUILD_FLAGS` を上書きしないこと。** あちらには `-tags production` も
  `-H windowsgui`（コンソールを出さない）も入っている。タグを足す口は `EXTRA_TAGS`
- ⚠️ **`wails3 dev` と `wails3 package` に `-tags` は無い。** 開発モードで焼き込みを
  試すなら `task build:embed` した exe を直接起動する

| 置き場所 | 中身 |
|---|---|
| `recognize/model/predictor.bin.gz` | `suteme/dist/training_data_v7.bin` を gzip したもの |
| `recognize/model/strip.bin.gz` | `suteme/dist/strip_data_v1.bin` を gzip したもの |
| `recognize/model/source.txt` | 出所（画面とログに出る。焼き込むと元のファイル名が残らないため）|

- ⚠️ **`suteme` の `dist/` は「配布用に書き出す」（`training.ExportCompact`）が作るもの。**
  リポジトリ直下の全件（`training_data_v7.bin`）ではなく、間引いた配布セットを配ること
- **gzip で持つ。** 実測 生 23.2MB → 9.9MB。exe は **13.4MB → 23.3MB**（+9.9MB。
  `wails3 build` の配布ビルド。`go build` だけの素の exe なら 19.2MB → 29.2MB）。
  `suteme` 側の口が `io.Reader` を取る（`PredictorFrom` / `StripJudgeFrom`）ので、
  展開したファイルを置く必要は無い（`recognize/embedded.go`）
- **焼き込み側のファイル名に版を入れていない**（`training_data_v7` → `predictor`）。
  版が上がるたびに `go:embed` の行を書き換えることになるため。どの版かは `source.txt`
- **`task model:copy` を忘れるとビルドが止まる**（`go:embed` がファイルを見つけられない）。
  それが狙い。古いデータや空のデータで配れてしまうより止まったほうがよい
- **ビルドが通ることでは足りない。** 中身が壊れていても `go:embed` は通るので、
  配布ビルドの前に `go test -tags embedmodel ./recognize/` で
  **実際に認識器として組み立てられること**を確かめる
- 通常のビルド（タグなし）には**データが入らない**。`recognize/embedded_off.go` が
  空の変数を返すだけなので、開発中に 20MB をリンクすることはない

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
- ⚠️ **地色だけで判断しないこと**（同じ中継でも露出で動く）。一番効くのは**大きさ**
- ⚠️ **既定の許容（大きさ 18% / 位置 0.8 マス / 色 34）は実測で決めた数ではない。**
  **実際の中継で当たり具合を見て調整すること**

⚠️ **本来は `suteme` の仕事**（画像の問いで、画素を持っているのはあちら）。
ここに置いてあるのは、**`suteme` が返している観測値だけで足りたから**。
**もっと確かな記述子が要るようになったら `suteme` に足すこと**（そのときも
**マスタを持たせない** —— あちらはステートレス・冪等が原則）。
