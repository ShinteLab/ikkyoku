# 追従の段取りを `StudyService` から別の型に分ける

2026-11-13 の全体の見直しで出た改善点。**まだ実装していない。** 終わったらこの文書を消し、
理由を `_docs/design-follow.md` に 1 項目、`TODO.md` 7 の行を消す。

## 何が問題か

`app` の中継の追従（`app/follow*.go`。6 ファイル・約 4,400 行）は、段取りも判断も `StudyService` のメソッドになっている。

- `StudyService` のメソッド約 98 個のうち 52 個が `follow*.go` にある
- `followSession`（`followsession.go`）が持つのは**状態だけ**で、メソッドは 1 つ。「中継セッションが持つ」のは名前のうえの話
- 長い関数: `followFrom` 251 行・`FollowCells` 238 行・`followProbe` 213 行（どれも `followstudy.go`）

困るのは 2 つ。**追従を単体で試せない**（手の木・控え・評価値・棋譜の取得を持った `StudyService` を丸ごと作る）ことと、
`TODO.md` 4 の ①（「追いつく」と「棋譜を起こす」を分ける）で追従を 2 つの仕事に割るとき、**どこまでが追従でどこからが手の木か**の
線がコードに無いこと。

## 確かめてある事実（2026-11-13）

`follow*.go`（テストを除く）が `StudyService` のフィールドに触れる回数:

| フィールド | 回数 | 中身 |
|---|---|---|
| `s.study` | 84 | 手の木（`position.Study`） |
| `s.mu` | 75 | `StudyService` のロック |
| `s.follow` | 56 | `followSession` |
| `s.Camera` | 7 | 撮る側（`followcam.Camera`） |
| `s.rev` | 4 | 局面の版 |
| `s.src` | 3 | 訂正タブ（`PositionService`） |
| `s.Emit` | 2 | イベント |

- 追従の外（`studyservice.go` など）から `s.follow…` を呼ぶ箇所は grep では出なかった（数え直すこと:
  `grep -nE "s\.follow" app/*.go | grep -v "app/follow"`）
- `followstudy.go` は 62 関数のうちメソッドが 24。**残り 38 はもうレシーバを持たない関数**
- 公開メソッドは 10 個: `StartFollow` / `StopFollow` / `ChooseFollow` / `FollowChoose` / `FollowHideRefused` /
  `FollowAuto` / `FollowConfirm` / `FollowCells` / `CellsLookUnmoved` / `FrameFits`

## 目標の形

`app` パッケージの中に非公開の型（仮に `follower`）を置き、追従の段取りと判断をそのメソッドにする。
`StudyService` は `follower` を 1 つ持ち、公開メソッド 10 個は**同じ名前のまま薄い取り次ぎ**として残す。

`follower` が手の木に触る口は、小さなインターフェース 1 つに絞る。必要なのはおおよそ次（実装の前に `s.study` の 84 か所を
洗って確定すること）:

- 本譜の先端の局面と手数を読む
- 手を足す（推測の印つき）
- 推測で足した末尾を差し替える（元の手は枝に残す）
- 状態を出す（`State` / `changed` / `publish`）

## 進め方（段ごとにコミット。どの段も振る舞いを変えない）

1. **洗い出す。** `s.study.` の呼び出しを種類ごとに数え、上のインターフェースの口を決める。口が 10 を超えるなら
   分け方が悪いので、ここで止めて相談する
2. **型を作って状態を移す。** `followSession` を `follower` に改名するか、`follower` が `followSession` を持つ。
   `Camera` も `follower` へ
3. **メソッドを移す。** 小さいファイルから（`followbase.go` → `followdrop.go` → `followrewind.go` → `followsession.go` →
   `followstudy.go`）。1 ファイル 1 コミット
4. **テストが `follower` を直に作れるようにする。** 偽の手の木で 1 本書けることを確かめる（これが分けた目的）

## 崩さないこと

- ⚠️ **パッケージは割らない。** 9 つの Service は非公開メソッドで繋がっていて、公開すると bindings に出てフロントの API になる
  （`app/AGENTS.md`）。分けるのは**型**
- ⚠️ **公開メソッドの名前とシグネチャを変えない。** フロントは `StudyService.StartFollow` / `StopFollow` / `ChooseFollow` を
  呼んでいる。`//wails:ignore` の有無も保つ
- ⚠️ **ロックは 2 つのまま**（ループの状態は `followSession` の mu、答えの控えは `StudyService.mu`。
  セッションの mu を持ったまま `StudyService` のメソッドを呼ばない。`_docs/app-follow.md` の「中継セッション」）
- ⚠️ **追っていないときに `FollowAuto` などを直に呼んでも動くこと**（持ち物はゼロ値で始まる。テストがそうしている）
- ⚠️ **止まり方は `followStop` の 1 か所**・**`StopFollow` は走っている 1 周の終わりを待つ**
- ⚠️ **判断の行を直さない。** しきい値・順番・早期リターンに触れたくなったら、それは別の作業

## 先に読むもの

`TODO.md` 4（追従の見直し。**足す前に読む**と書いてある）・`_docs/app-follow.md`・`_docs/design-follow.md` の
「中継を追う 1 回を Go の 1 か所が持つ」（第 1〜3 段）・`followcam/AGENTS.md`。

## 確かめ

- ルートで `GOWORK=off go build ./... && go vet ./... && go test ./...`（worktree では `GOWORK=off`）
- `_cmd/ikkyoku` で `go vet . && go test -skip Clipboard .`
- `_cmd/ikkyoku` で `wails3 generate bindings -ts -i`、`frontend` で `npx tsc --noEmit`（公開メソッドが変わっていないことの確かめ）
- 既存の `app/follow*_test.go`（約 3,500 行）を**書き換えずに**通すこと。書き換えが要るなら取り次ぎが足りていない

## 時期

`TODO.md` 4 に「作り直した分はどれも実機では未確認」とある。10-10〜11 の作り直しを実機で通す前にこれを重ねると、
壊れたときに原因を絞りにくい。**実機の確認が済んでいるかをユーザーに聞いてから始めること。**
`TODO.md` 4 の ① に手を付けるなら、その最初の段としてやるのがいちばん無駄が無い。
