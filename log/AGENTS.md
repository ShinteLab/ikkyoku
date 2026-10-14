# log の AGENTS.md

**ikkyoku のログ。** ikkyoku のコードは `log.Info(msg, "key", v)`（slog と同じ形）・
`log.Infof(format, ...)`（printf）などで出す。土台は `github.com/wenteasy/log`（v0.3.1〜）。
作り方の決まりごとの正は wenteasy/log の README「使う側のやり方（自分の log パッケージを作る）」（ひな形・よくある間違い）。この `log/` はその元になった形（アプリだけなので `SetLogger` は無い）。

| ファイル | 役割 |
|---|---|
| `log.go` | 出す関数（Trace / Debug / Info / Notice / Warn / Error と `…f`）と、**中継の追従のログ `log.Follow`・画面の診断のログ `log.Diag`**（wenteasy/log の名前付きの Logger `Named`。`*slog.Logger`）。出口は `slog.Default()` |
| `setup.go` | `Init`。出口を組んで `slog.Default()` を向ける（main の最初に 1 回）。`Dir` はログを書いている場所（**書けていなければ空**。画面の ⚠ に出す） |
| `crash.go` | `CatchCrash`（2026-10-04）。**異常終了（panic・ランタイムの致命的なエラー）の内容をログの隣の `ikkyoku_crash.log` にも書かせる**（`debug.SetCrashOutput`）。前回の記録が残っていれば `ikkyoku_crash_<日時>.log` に改名して返す（＝次の起動の ⚠「前回は異常終了しました」）。⚠️ **改名すること**（作り直すと前回の記録が消える）。⚠️ **書けなくても起動は止めない** |

## レベルの線引き（2026-10-11）

- **INFO は開発中に参考になるもの、NOTICE は常に残しておきたいもの。** 全体の既定は **dev で INFO、配る exe で NOTICE**
  （`Options.Root`。`_cmd/ikkyoku` が `devBuild` で選ぶ）。配る exe には NOTICE 以上と警告だけが残る
- **NOTICE にするのは「どれで動いたか」と起動・終了の区切りだけ**: 起動した版（「ikkyoku を起動します」）・終了・
  認識器・棋譜データベース・繋いだエンジン・デバッグの設定（中継の録画とログ）・ログの細かさ。⚠️ **「念のため常に出す」で
  NOTICE に上げないこと** —— それなら全部出せばよい、になる。何か起きたと分かるものは WARN で出す
- **全体のレベルは 画面の設定「ログの細かさ」（`SetRootLevel`。設定 `logLevel`）> `ikkyoku-log.json` の `root` >
  `Options.Root` の順**（`levelState`）。画面で変えるのは全体だけで、ファイルのパッケージごと・名前付きの Logger ごとの
  指定はそのまま効く。切り替えたその場で効く
- ⚠️ **追従のログを残すあいだ（`SetFollow`）は追従の行を INFO まで出す**（`min(INFO, 全体)`。追従の行は INFO なので、
  配る exe の NOTICE のままでは何も残らない）。`loggers` に `follow` があればそちらが勝つ。⚠️ **追従のレベルはパッケージごとの
  指定では変わらない**（細かくするなら `loggers` の `follow` か、画面で全体を DEBUG に）

## どこに出るか（`Init`）

- **ファイル**: **一時ディレクトリの `ikkyoku`**（`DefaultDir`。`%TEMP%\ikkyoku`）の `ikkyoku_<日付>.log`
  （日ごと。**1 週間（`KeepDays`）より古いものは起動時に消す**）。dev でも配る exe でも同じ場所（2026-10-11）。
  それまでは実行ファイルの隣で 14 日残していたが、配る exe の置き場所に記録が溜まるうえ、何日も前の記録を掘って
  デバッグすることは無い。⚠️ **ログの書き出し先を実行ファイルの隣に戻さない**
- **追従のファイル**（2026-10-10）: `log.Follow.Info(...)` で出したものは
  `ikkyoku-follow_<日付>.log` **だけ**に出す（**コンソールにも本体のファイルにも出さない**。
  中継を追っているあいだ 1 周ごとに何行も出て、ターミナルが埋まったため）。
  ⚠️ **Warn 以上は本体（ファイルとコンソール）にも出す**（追従のファイルはふだん見ないので、警告を見落とさないため）。
  ⚠️ **追跡を始めた・止めたは本体にも 1 行ずつ出す**（2026-10-10。「中継の追跡を始めました／止めました」。`_cmd/ikkyoku` の
  `AnchorBoard` / `EndFollow` が `log.Info` でも出す）—— 本体だけ読んでいて、いつ追っていたかが分からなかった。
  ファイルは書いたときに作る（追わなかった日は無い）。古いものを消すのは本体と同じ。行には `logger=follow` が付く。
  ⚠️ **残すのは設定「中継の録画とログを残す（デバッグ）」（`followDebug`）が入っているときだけ**（2026-10-11。
  `Options.Follow` / `SetFollow`。切り替えたその場で効く。dev でも配る exe でもこの設定だけで決まる）。切ってあるときは
  ファイルを作らず、Warn 以上と始めた・止めたの 1 行だけが本体に出る（始めた・止めたは INFO なので、配る exe の既定では出ない）。⚠️ **切り替えは `switchHandler` で**
  （`log.Follow` は Init より前に作られた Logger で、書くたびに派生する。派生も同じスイッチを見る）
  **中継の追従（Phase 6）のログは `log.Follow` で出すこと**（`app/follow*.go`・`_cmd/ikkyoku` の追従の周）。
  `log.Follow` は `*slog.Logger` なので `Infof` や `Trace` は無い（`Info` / `Debug` / `Warn` / `Error`、それ以外の段階は `Log`）。
  振り分けは `setup.go` の `namedRouter`（Named が `WithAttrs` で付ける印で名前のファイルへ）。
  分ける名前を増やすなら `setup.go` の `separated` に足す
- **診断のファイル**（2026-10-10）: `log.Diag`（`Named("diag")`）は画面まわりの診断（心拍・ウィンドウの表示状態。
  「メイン画面が真っ黒になる」の再現待ち。`_docs/blackscreen.md`）。**Wails のログ（`Options.Logger`）と
  標準の `log` の出力（WebView2 の `[WebView2] Eval failed` などはここから出る）も `Init` がここへ向ける。**
  ⚠️ **dev のときだけ残す**（`Options.Diag`。`_cmd/ikkyoku` は `devBuild` を渡す）—— `ikkyoku-diag_<日付>.log` に
  **既定で Debug まで**（黒くなるのは長時間動かしたときだけなので、レベルを上げてから待つのでは間に合わない）。
  配るビルドでは**残さない**（ファイルも作らない）。どちらでも ⚠️ **Warn 以上は本体に出す**。コンソールには出さない。
  `loggers` に `diag` を書けばそのレベルが勝つ（書かなければ dev の既定の Debug）
- **USI のファイル**（2026-10-10）: `log.USI`（`Named("usi")`）はエンジンとの USI のやり取り
  （`analyze/usitap.go` が出す）。`ikkyoku-usi_<日付>.log` へ。コンソールには出さない。送った行・受けた行は INFO なので
  **配る exe の既定（NOTICE）では残らない**（2026-10-11。2026-10-10 は配るビルドでも残すとしたが、INFO は開発中の参考という
  線引きに揃えた。頻繁に起きるなら開発中に直す。要るときは画面で INFO に下げる）。
  ⚠️ **Warn 以上は本体にも出す**（エンジンを落とした、など）。`info` は Debug なので既定では出ない
  （困ったときは analyze が直前の行をまとめて出す。`loggers` に `usi` を DEBUG で書けば全部出る）
- **コンソール**: `Options.Console` のときだけ標準エラーにも。`_cmd/ikkyoku` が
  **ビルドタグ `production` が無いとき**（＝ `wails3 dev` / `go run`）に立てる（`devbuild.go` / `prodbuild.go`）
- **レベル**: 呼び出し元の**パッケージごと**。実行ファイルの隣に `ikkyoku-log.json` があれば読む
  （`Options.LevelsDir`。ログの書き出し先とは別。⚠️ **設定を一時ディレクトリに置かない** —— OS の掃除で消える）。
  無ければ既定（全体は `Options.Root`。上の「レベルの線引き」）。⚠️ **既定でライブラリを個別に絞らないこと** —— うるさいなら
  そのライブラリ側でレベルを下げる（engine の USI の送受信は engine 側で Debug にしてある）

```json
{
  "root": "INFO",
  "packages": [
    {"name": "github.com/ShinteLab/ikkyoku/app", "level": "DEBUG"},
    {"name": "main.(*CaptureService).noteQuiet", "level": "DEBUG"},
    {"name": "github.com/ShinteLab/engine", "level": "DEBUG"}
  ],
  "loggers": [
    {"name": "follow", "level": "DEBUG"}
  ]
}
```

`name` はパッケージのパスか関数まで含めた名前（`_cmd/ikkyoku` は `main`）。一番深く当たったものが効く。
**`loggers` は名前付きの Logger**（`log.Follow` は `follow`）。あれば呼び出し元のパッケージに関係なくそのレベルで、
無ければパッケージで決まる（追従のログは、残すあいだは INFO まで。上の「レベルの線引き」。追従だけ細かく見たいときは
`loggers` の `follow` を DEBUG にする）。
レベルは TRACE / DEBUG / INFO / NOTICE / WARN / ERROR（`INFO+2` のような slog の書き方も可）。
⚠️ 読めない設定は**使わずに既定で続ける**（起動の 1 行の `levels` にどれを使ったかが出る）。
起動の 1 行（「ログを開始しました」）はレベルに関係なく必ず残る。

## ⚠️ 崩さないこと

- ⚠️ **Logger を引数で配らないこと。** 呼び出し元のパッケージは記録の位置（PC）から分かり、
  レベルはパッケージごとに絞れる。Service に `*slog.Logger` のフィールドを足さない
- ⚠️ **出す関数の中で別の関数を挟まないこと**（`output` は公開関数から直接呼ぶ）。
  記録の位置を段数で数えているので、挟むと全部のログがこのパッケージから出たことになり、
  パッケージごとのレベルが効かなくなる。**wenteasy/log の `Info` などを包まない**のも同じ理由
- ⚠️ **ikkyoku を使う側に、このパッケージの型を求めないこと。** レベルは `slog.Level`、
  出口は `slog.Default()` のまま。**ライブラリ（kicho など）へ渡すのも標準の `*slog.Logger`**
  （`slog.Default()`）。ライブラリはレベルを持たず、パッケージごとのレベルで絞る
- ⚠️ **ログが書けなくてもアプリは動かす**（設計原則3）。`Init` はファイルを作れなくても
  出せる出口だけで組み、理由をエラーで返す（起動は止めない）
- ⚠️ **配る exe（`-H windowsgui`）には標準エラーの行き先が無い。** 見える場所はファイルだけ

## テスト（`go test ./log/`）

`crash_test.go` —— 前回の記録を改名して残すこと・空のファイルは異常終了と見なさないこと・
古いログの片付けが記録を消さないこと・`Dir` が書けているときだけ場所を返すこと。
`log_test.go` —— ファイルに出ること・**`log.USI` が USI のファイルにだけ出ること（Warn 以上は本体にも）**・**`log.Follow` が追従のファイルにだけ出ること（`With` で派生しても。Warn 以上は本体にも）・
`loggers` の `follow` で追従だけレベルを変えられること・追従のファイルは書くまで作らないこと・
**設定で切ってあれば追従のファイルを作らず（Warn 以上だけ本体）、切り替えたその場で効くこと**（`TestFollowSwitch`）・
**レベルの設定は `LevelsDir` から読むこと**（`TestLevelsDir`）・
**全体のレベルが 画面 > ファイルの root > Options.Root の順で決まり、画面の設定がその場で効くこと**（`TestRootLevelOrder`）・
**追従のログを残すあいだは、全体が WARN でも追従の行が INFO まで出ること**（`TestFollowLevelWhenOn`）・**1 週間を過ぎたものだけ消すこと**（`TestRemoveOld`）・
**診断は dev では Debug まで診断のファイルに残り（標準の log も）、配るビルドでは残らず Warn 以上だけ本体に出ること・
`loggers` に `diag` が無ければ dev の既定が残ること**・**呼び出し元のパッケージでレベルが決まること**
（ここの関数を通しても位置がずれない歯止め）・読めない設定でも既定で続くこと・
ファイルを作れなくても止まらないこと・古いファイルだけ消すこと（日付はファイル名から読む）。
