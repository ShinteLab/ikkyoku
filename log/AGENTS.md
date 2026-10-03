# log の AGENTS.md

**ikkyoku のログ。** ikkyoku のコードは `log.Info(msg, "key", v)`（slog と同じ形）・
`log.Infof(format, ...)`（printf）などで出す。土台は `github.com/wenteasy/log`（v0.3.0〜）。

| ファイル | 役割 |
|---|---|
| `log.go` | 出す関数（Trace / Debug / Info / Notice / Warn / Error と `…f`）。出口は `slog.Default()` |
| `setup.go` | `Init`。出口を組んで `slog.Default()` を向ける（main の最初に 1 回） |

## どこに出るか（`Init`）

- **ファイル**: 実行ファイルの隣の `ikkyoku_<日付>.log`（日ごと。**14 日より古いものは起動時に消す**）。
  `wails3 dev` なら `_cmd/ikkyoku/bin/`（gitignore 済み・dev の監視対象外）
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
  ]
}
```

`name` はパッケージのパスか関数まで含めた名前（`_cmd/ikkyoku` は `main`）。一番深く当たったものが効く。
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

`log_test.go` —— ファイルに出ること・**呼び出し元のパッケージでレベルが決まること**
（ここの関数を通しても位置がずれない歯止め）・読めない設定でも既定で続くこと・
ファイルを作れなくても止まらないこと・古いファイルだけ消すこと（日付はファイル名から読む）。
