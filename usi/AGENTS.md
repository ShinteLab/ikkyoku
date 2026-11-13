# usi の AGENTS.md

**同梱エンジンの口。** 同一プロセスの `engine` を `io.Pipe` で USI として繋ぎ、
外部エンジンと同じ USI クライアント（`core/usi/client`）から見えるようにする層。
⚠️ **クライアント本体は `core/usi/client`**（ここに書かない）。

## 今は残す。いつか消す（2026-11-13）

外部エンジン（Phase 4 の Step 2）が入った今も、**エンジンのパスが空のときはここを通る**
（`analyze.NewLocalSession`）。**外部エンジンを入れていない人でも解析が動く**のはこのおかげ
（設計原則3）。理由は `_docs/design-engine.md` の「同梱エンジンは今は残す」。

⚠️ **消せるのは次の 2 つが揃ってから。** 片方だけで消さないこと:

1. **`engine` が配る USI エンジンの exe ができ**（`engine/TODO.md` 4）、ikkyoku と一緒に配って、
   パスが空のときにそれを `client.Exec` で起こせること（＝外部エンジン無しでも解析が動く、を保つ）
2. **その exe にだけ `kingless` を立てる見分け方が決まっていること。** 攻方の玉が無い局面を
   渡してよいのは同梱エンジンだけ（ルートの `AGENTS.md`「崩さないこと」）で、今は
   `NewLocalSession` が立てている。`NewExecSession` で起こすと外部エンジンと見分けが付かない

消すときは、このパッケージと `engine/search` への依存が消える。`engine` への依存は
合法手生成（`legal/`）のために残る。

## 制約

- `localEngine` は `engine.ContextEngine` の実装（**これがあるから `stop` が効く**）。
  `engine/_samples` は import しない（`_` 始まりのサンプルで深さ 4 固定・オプション無し）
- ⚠️ **Lazy SMP（`Parallel`）は使わない。** 想定外の局面で worker の goroutine が
  panic するとアプリごと落ちる（`recover` は同じ goroutine でしか効かない）
- **名前は `usi` のまま**（`core/usi` と紛らわしく、`analyze` は `localusi` と別名で import している）。
  改名は import を変えるだけで得るものが少ない

## テスト

このパッケージにテストファイルは無い。同梱エンジンを実際に起こすのは `analyze/analyze_test.go`
（`NewLocalSession`）で、**繋がること**（ハンドシェイクから `bestmove` まで）も、
**`stop` が効くこと**（`TestAnalyzeCanceledStillReturns`。1 分の読みを 50ms で切って `Stopped` を見る）も
そこで見ている。
