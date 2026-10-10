# log の AGENTS.md

**ikkyoku のログ。** ikkyoku のコードは `log.Info(msg, "key", v)`（slog と同じ形）・
`log.Infof(format, ...)`（printf）などで出す。土台は `github.com/wenteasy/log`（v0.3.1〜）。

| ファイル | 役割 |
|---|---|
| `log.go` | 出す関数（Trace / Debug / Info / Notice / Warn / Error と `…f`）と、**中継の追従のログ `log.Follow`・画面の診断のログ `log.Diag`**（wenteasy/log の名前付きの Logger `Named`。`*slog.Logger`）。出口は `slog.Default()` |
| `setup.go` | `Init`。出口を組んで `slog.Default()` を向ける（main の最初に 1 回）。`Dir` はログを書いている場所（**書けていなければ空**。画面の ⚠ に出す） |
| `crash.go` | `CatchCrash`（2026-10-04）。**異常終了（panic・ランタイムの致命的なエラー）の内容をログの隣の `ikkyoku_crash.log` にも書かせる**（`debug.SetCrashOutput`）。前回の記録が残っていれば `ikkyoku_crash_<日時>.log` に改名して返す（＝次の起動の ⚠「前回は異常終了しました」）。⚠️ **改名すること**（作り直すと前回の記録が消える）。⚠️ **書けなくても起動は止めない** |

## どこに出るか（`Init`）

- **ファイル**: 実行ファイルの隣の `ikkyoku_<日付>.log`（日ごと。**14 日より古いものは起動時に消す**）。
  `wails3 dev` なら `_cmd/ikkyoku/bin/`（gitignore 済み・dev の監視対象外）
- **追従のファイル**（2026-10-10）: `log.Follow.Info(...)` で出したものは
  `ikkyoku-follow_<日付>.log` **だけ**に出す（**コンソールにも本体のファイルにも出さない**。
  中継を追っているあいだ 1 周ごとに何行も出て、ターミナルが埋まったため）。
  ⚠️ **Warn 以上は本体（ファイルとコンソール）にも出す**（追従のファイルはふだん見ないので、警告を見落とさないため）。
  ファイルは書いたときに作る（追わなかった日は無い）。古いものを消すのは本体と同じ。行には `logger=follow` が付く。
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
- **コンソール**: `Options.Console` のときだけ標準エラーにも。`_cmd/ikkyoku` が
  **ビルドタグ `production` が無いとき**（＝ `wails3 dev` / `go run`）に立てる（`devbuild.go` / `prodbuild.go`）
- **レベル**: 呼び出し元の**パッケージごと**。実行ファイルの隣に `ikkyoku-log.json` があれば読む。
  無ければ既定（全体を Info）。⚠️ **既定でライブラリを個別に絞らないこと** —— うるさいなら
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
無ければ追従のログもパッケージで決まる（追従だけ細かく見たいときは `loggers` の `follow` を DEBUG にする）。
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
`log_test.go` —— ファイルに出ること・**`log.Follow` が追従のファイルにだけ出ること（`With` で派生しても。Warn 以上は本体にも）・
`loggers` が無ければパッケージで、あれば `follow` の設定で決まること・追従のファイルは書くまで作らないこと・
**診断は dev では Debug まで診断のファイルに残り（標準の log も）、配るビルドでは残らず Warn 以上だけ本体に出ること・
`loggers` に `diag` が無ければ dev の既定が残ること**・**呼び出し元のパッケージでレベルが決まること**
（ここの関数を通しても位置がずれない歯止め）・読めない設定でも既定で続くこと・
ファイルを作れなくても止まらないこと・古いファイルだけ消すこと（日付はファイル名から読む）。
