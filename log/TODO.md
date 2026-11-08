# log の TODO（レビュー待ち）

wenteasy/log の README「使う側のやり方（自分の log パッケージを作る）」（v0.3.2〜）と突き合わせて気付いた点。
2026-10-14、aibow のセッションで書いた。**直すかどうか・どう直すかは ikkyoku 側でレビューして決める**（ここに書いたのは材料だけ）。

**結論: README の決まりごとはすべて満たしている。** 出す関数は自前（`output`。位置はずれない）、使う側に見せるのは
`slog.Level` と `*slog.Logger` だけ、`Init` はファイルを作れなくても止まらず理由を返す、`io.Closer` は nil にしない、
開発中はコンソール・配る exe はファイル、Logger を引数で配らない。`go test ./log/` も通る。
むしろ README のほうが、この `log/` に追いついていない（下の「README に無いもの」）。

## 気付いた点（ikkyoku 側）

### 1. wenteasy/log の版

- ~~`go.mod` は v0.3.1~~ → **v0.4.0 にした**（2026-11-08。古いファイルの圧縮・削除を使う）

### 2. 名前付きのファイルを作れなかったことが、Init のエラーに出ない

- `Init` の名前付き（follow / diag / usi）の `NewRollingFileWriter` が失敗すると `continue` で黙って飛ばす
- ログは本体に出る（`namedRouter` の「出口が無い名前は本体へ」）ので失われないが、別のファイルに分かれていないことに気付けない
- README の決まりごと:「ファイルを作れなくても止めず、理由を error で返す」。本体のファイルと同じく `errs` に足すか決める
  （同じディレクトリなので、本体が作れて名前付きだけ失敗することは、まず無い）

### 3. `namedRouter` は名前の完全一致だけで振り分ける

- wenteasy/log の名前付きの Logger は `"follow.x"` のように `.` で区切ると `"follow"` の設定が効く（レベルの判定）が、
  `namedRouter` は `a.Equal(wlog.LoggerAttr(name))` の完全一致なので、`Named("follow.x")` は follow のファイルではなく本体へ出る
- 今は follow / diag / usi の 3 つだけで、`.` で区切った名前を使っていないので実害は無い。区切った名前を使うときに直す

### 4. `Init` の `if dir != ""` は常に真

- `Options.Dir` が空なら `DefaultDir()` を使うので、`dir` は空にならない。`if dir != ""` が残っていると、
  「Dir が空ならファイルに書かない」（README のひな形の形）と読み違えやすい。消すか、コメントで「常に真」と書く

## README に無いもの（wenteasy/log の README か、wenteasy/log 本体に戻す候補）

ikkyoku で実際に使って足したもの。ほかのプロジェクトでも要りそうなら、wenteasy/log 側に戻す。

- **置き場所は一時ディレクトリ**（`DefaultDir`。`%TEMP%\ikkyoku`。2026-10-11。配る exe の置き場所に記録が溜まる・
  何日も前の記録を掘ってデバッグすることは無い）。README の例は `%APPDATA%\<名前>\logs`。
  → **README に書いた**（wenteasy/log `444a65e`。次のタグから）:「置き場所はアプリで決める」、設定のファイルは一時フォルダに置かない
- **出口は一番低いレベル（Trace）で作り、全体のレベルは `Options.Root`（dev は Info、配る exe は Notice）**。
  README のひな形は出口を Info で固定している。→ README のひな形を Root を取る形にするか、「パッケージごとのレベル」の節に寄せる
- **レベルの決め方の順**（画面の設定 > `ikkyoku-log.json` の root > `Options.Root`）と、設定のファイルはログと別の場所（`LevelsDir`。
  実行ファイルの隣。一時ディレクトリは掃除で消える）
- **名前付きの Logger を別のファイルへ振り分ける**（`namedRouter`。Warn 以上は本体にも出す）と、設定で出口を切り替える（`switchHandler`）。
  → wenteasy/log に振り分けのハンドラとして入れる候補（README の「名前付きの Logger」の続き）
- **標準の log の出力の向け先**（`stdlog.SetOutput`。Wails の [WebView2] の行は標準の log で来る）と、閉じるときに戻すこと
  （戻さないと log → slog → log と回り続ける）。→ **README に一部書いた**（`444a65e`。WebView2 の行は標準の log で来ること・
  ikkyoku は名前付きの Logger へ向け直していること）。向け直し方と、閉じるときに戻すことは、まだ README に無い
- **古いファイルを消す**（`KeepDays` = 90。日付はファイル名から読む）と、**始まりの 1 行はレベルに関係なく残す**。
  → 圧縮・削除は v0.4.0 で入った（`SetCompressAfter` / `SetKeep`）。ただし**開いたファイルの名前しか片付けない**ので、
  開かない名前（配る exe の diag など）のために ikkyoku は起動時の `removeOld` を残している。
  **wenteasy/log 側で片付けを外から呼べる関数（dir・prefix・間隔・日数を渡す）にすれば `removeOld` も消せる**
- **異常終了を残す**（`CatchCrash`。`debug.SetCrashOutput`）。README は「ikkyoku の log にある」と名前だけ
