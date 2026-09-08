# usi の CLAUDE.md

**Step 1 の足場だけ。** 同一プロセスの `engine` を `io.Pipe` で USI として繋ぐ。
⚠️ **クライアント本体は `core/usi/client`**（ここに書かない）。**Step 2 で消える。**

## Step 1 の足場（**Step 2 で消える**）

`ikkyoku/usi/local.go` が同一プロセスの `engine` を `io.Pipe` で繋いでいる。
**`ikkyoku → engine` の Go 依存が残っているのはこのパッケージだけ**なので、
Step 2 で `client.Exec` に差し替えれば依存ごと消える
（`analyze.Session.open` を差し替えるだけ）。

- `localEngine` は `engine.ContextEngine` の実装（**これがあるから `stop` が効く**）。
  `engine/_samples` は import しない（`_` 始まりのサンプルで深さ 4 固定・オプション無し）
- ⚠️ **Lazy SMP（`Parallel`）は使わない。** 想定外の局面で worker の goroutine が
  panic するとアプリごと落ちる（`recover` は同じ goroutine でしか効かない）
