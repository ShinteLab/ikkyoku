# `FollowCamera` の口を狭める（合図 4 つを見直す）

2026-11-13 の全体の見直しで出た改善点。**まだ実装していない。小さい。やる価値が薄いと分かったら、理由を
`_docs/design-follow.md` に 1 行残してこの文書を消してよい。**

## 何が問題か

`app.FollowCamera`（`app/followsession.go`）は 13 メソッドある。うち 4 つは、局面を知っている側（`app`）が
局面を知らない側（`followcam`）の**内部の覚え書きを外から書き換える合図**:

| メソッド | いつ呼ぶか（`followOnce`） | 撮る側がすること |
|---|---|---|
| `SetCellBase()` | 先端がこの 1 枚と合っている（`AtFrame` か `FrameFits`） | 直前の 1 枚を比べる相手にする |
| `CellsUnmoved()` | 光っているだけ（`LooksOnly`）／81 マスで「何も指していない」かつ `CellsLookUnmoved` | この組を「変わっていない」に数える |
| `CellsUnclear()` | 速い経路が `NoMoves` を返した | この組を決まらなかった組として覚え、待たせる |
| `ForgetUnmoved()` | 81 マスの読みで手が見つかった（控えた・足した） | 「動いていない」と覚えた組を忘れる |

呼ぶ順番と組み合わせの正しさを `followOnce`（135 行）が手で守っている。合図を 1 つ足すたびに、インターフェース・
本物・偽物（`fakeCamera`）の 3 か所が増える。

## 案

4 つを「この 1 枚をどう判定したか」を渡す 1 つの口にまとめる。

```go
// 撮る側へ返す、直前の 1 枚の判定（局面を知っている側が決める）。
type Verdict struct {
    AtFrame bool // 先端と合っている → 比べる相手にする
    Unmoved bool // 駒は動いていない → この組を「変わっていない」に数える
    Unclear bool // 説明できる手が無い → 組が変わるのを待つ
    Found   bool // 手が見つかった → 「動いていない」の覚えを忘れる
}
Judge(v Verdict)
```

型は `followcam` に置く（`Shot` と同じ。`app` が import する向き）。

## ⚠️ 先に確かめること（ここが通らなければやめる）

**4 つの合図は 1 周の同じ時点で呼ばれていない。** `app/followsession.go` の `followOnce` を読むこと。

- `CellsUnmoved`（`LooksOnly`）と `CellsUnclear`（`NoMoves`）は**速い経路の枝の中**で呼ばれ、そのあと同じ 1 周で
  `RecognizeQuiet` に進むことがある。`RecognizeQuiet` は `markCellsFailed` で決まらなかった組を上書きする
- `ForgetUnmoved` と `SetCellBase` は 1 周の終わり近くで呼ばれる
- `SetCellBase` は比べる相手を替えるとき、覚えた組を全部捨てる（`setCellBaseLocked`）

1 つにまとめて 1 周の終わりに 1 回だけ呼ぶ形にすると、**`RecognizeQuiet` との前後が変わる**。変わるなら、
それは振る舞いの変更で、この作業の範囲を超える。その場合の落としどころは「口は 1 つ（`Judge`）にするが、呼ぶ時点は
今と同じ 2 か所のまま」。それでもインターフェースは 13 → 10 になる。

## 崩さないこと

- ⚠️ **撮る側で「合っているか」「動いたか」を判断しない**（局面を知らない層。`followcam/AGENTS.md`）
- ⚠️ **比べる相手を替えたら、覚えた組を忘れる**・⚠️ **待つ上限（`gateMovingMax`）と保険（`gateSkipMax`）は外さない**
  （`_docs/cmd-capture.md` の「速い経路」）
- ⚠️ **`followcam/celldiff_test.go` の歯止めを弱めない**（`CellsUnclear` は盤全体が止まっていても待つ上限まで待つ・
  `CellsUnmoved` は「変わっていない」に数える・比べる相手を替えたら忘れる）

## 確かめ

- ルートで `GOWORK=off go test ./followcam/ ./app/`、全体の `go build ./... && go vet ./... && go test ./...`
- `app/followsession_test.go` の `fakeCamera` が、合図の呼ばれた順を今と同じに記録できること（順番を見ているテストがある）

## 関連

`_docs/todo/1-follow-session-type.md`（追従を別の型に分ける）と同じ場所を触る。**同時にやらない。**
