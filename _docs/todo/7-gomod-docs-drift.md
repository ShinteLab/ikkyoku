# `go.mod` と文書の食い違いを直す

2026-11-13 の全体の見直しで出た点。**まだ直していない。小さいが踏みやすい。**
終わったらこの文書を消し、`TODO.md` 7 の行を消す。4 つは独立しているので、1 つずつ直してよい。

⚠️ **`go.mod` を触る前にスキル `ikkyoku-build` を読むこと**（`_docs/skills/ikkyoku-build/SKILL.md`。go.mod が 2 つあること・
兄弟のモジュールの上げ方・`go mod tidy` のあとの確かめ）。worktree では `GOWORK=off` を付ける。

## 1. suteme の版が 2 つの `go.mod` でずれている

| ファイル | 版 |
|---|---|
| `go.mod`（ルート） | `github.com/ShinteLab/suteme v0.2.7` |
| `_cmd/ikkyoku/go.mod` | `github.com/ShinteLab/suteme v0.2.8` |

`_cmd/ikkyoku` はルートを `replace` で見ているので、**アプリは 0.2.8 で動く**（大きいほうが選ばれる）。
一方ルートの `go test ./...` は **0.2.7 で回っている**。認識器の振る舞いが版で違えば、テストとアプリで食い違う。

直し方: ルートを 0.2.8 に上げる。

```bash
GOWORK=off go get github.com/ShinteLab/suteme@v0.2.8
```

```bash
GOWORK=off go mod tidy
```

- ⚠️ **上げる前に、そのタグが本当にあるか確かめる**（`git ls-remote --tags https://github.com/ShinteLab/suteme`）。
  無い版を取りに行くと proxy が「無い」を 30 分ほど覚える（ワークスペースの `AGENTS.md`）
- ⚠️ **`go mod tidy` のあと、require の行が消えていないか見る**（`git diff go.mod`）
- 確かめ: ルートで `go build ./... && go vet ./... && go test ./...`。`recognize` と `followcam` のテストが通ること
- 0.2.7 と 0.2.8 の差が何かは見ていない。**上げてテストが落ちたら、落ちた内容をそのまま報告する**（黙って合わせない）

## 2. Wails の版の記述が古い

`_cmd/ikkyoku/go.mod` は `github.com/wailsapp/wails/v3 v3.0.0`。文書は「beta.26」と書いている:

- ルートの `AGENTS.md`（スキルの表の `wails3` の行・「ビルドと実機確認」の「Wails は手元の CLI に追従している（beta.26）」）
- `_cmd/ikkyoku/AGENTS.md`（「2026-10-04 時点では CLI も `go.mod` も `v3.0.0-beta.26`」）
- `_docs/skills/ikkyoku-build/SKILL.md`（スキル `wails3` の行）
- ワークスペースの `AGENTS.md`（`../AGENTS.md`。**別のリポジトリ `ShinteLab/shinte`** なので、直すならそちらでコミットする）

直し方:

1. **手元の CLI の版を確かめる**（`wails3 version`）。`go.mod` と同じ v3.0.0 なら、文書を「v3.0.0（2026-MM-DD 時点）」に直す。
   違うなら、どちらに合わせるかをユーザーに聞く（生成物の bindings が食い違うことがある）
2. `_cmd/ikkyoku/AGENTS.md` の beta.3 のときの話（`wails3 init -t vanilla` が React を作る）は**そのとき確かめた記録**なので消さない。
   「v3.0.0 では再確認していない」と添える
3. `_docs/blackscreen.md` の「beta.3 → beta.26」は調査の記録なので触らない

## 3. `_cmd/ikkyoku/go.mod` の `replace` のコメントが古い

```
// ikkyoku はタグ未発行のため相対パスの replace で参照する(親 CLAUDE.md の運用に準拠)。
replace github.com/ShinteLab/ikkyoku => ../../
```

指示ファイルは `AGENTS.md`（`CLAUDE.md` は無い）。`replace` を使う理由も「タグ未発行だから」ではなく、
**同じリポジトリの中のネストモジュールだから**（ルートと一緒に直すので、タグを待てない）。コメントだけ直す。
⚠️ **`replace` そのものは外さない。**

## 4. ルートの `go.mod` の require のまとまりが崩れている

- `github.com/ShinteLab/kicho v0.2.1` が、ほかの直接依存と別の（3 つ目の）ブロックにある
- 同じブロックに `golang.org/x/text v0.40.0` が `// indirect` なしで入っている（直接 import しているなら 1 つ目のブロックへ）

動作には影響しない。`go mod tidy` は行の置き場所を直さないので、手で 1 つ目のブロック（直接依存）へ移してから
`GOWORK=off go mod tidy` を流し、差分が置き場所だけになることを確かめる。1 と一緒にやると `go.mod` を触るのが 1 回で済む。

## 確かめ（まとめ）

- ルートで `GOWORK=off go build ./... && go vet ./... && go test ./...`
- `_cmd/ikkyoku` で `GOWORK=off go vet . && go test -skip Clipboard .`
- `git diff go.mod go.sum _cmd/ikkyoku/go.mod _cmd/ikkyoku/go.sum` を読み、意図した行だけが動いていること
