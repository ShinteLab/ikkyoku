# どのエンジンが何を返すか（**実測**。2026-09-12）

⚠️ **全部この手元で実際に動かして測った値。推測は 1 つも入っていない。**
⚠️ **再測しなくてよい。** 新しいエンジンを足したときだけ、同じやり方で測って足す。

## 攻方の玉が無い局面（＝詰将棋の普通の形）

使った局面は 1 手詰（▲５二金打）。`4k4/9/4P4/9/9/9/9/9/9 b G2r2b3g4s4n4l17p 1`

| エンジン | 通常の `go` | `go mate` |
|---|---|---|
| **同梱**（`engine` + `search`） | **解ける**（`G*5b` / `MateScore-1`） | `checkmate notimplemented` |
| **KomoringHeights 1.1.0** | `bestmove resign` | **`checkmate G*5b`**（数十 ms） |
| やねうら王 水匠5（NNUE） | **無反応** | **無反応** |
| tanuki- dr4 | — | **無反応** |
| Shinden3 | — | **無反応** |
| NAGISA V3.1 | — | **無反応** |

- ⚠️ **やねうら王系の「無反応」は落ちるのではない。** `readyok` まで進み、
  `position` も受け取り、**そのあと何も出さない**。**手前で弾いていないと解析が固まる**
  （`analyze.ensurePlayable` が外部エンジンに渡さない理由がこれ）
- **同梱エンジンは攻方の玉が無くても通常探索で解ける。** 攻方の玉を隅に置いた版と
  **手もスコアも一致した** ——⚠️ **玉を仮置きする案は利点が無い**（嘘の局面を渡すだけ）
- ⚠️ **仮置きは長手数で解が変わる。** 詰将棋では玉方が余り駒を全部持つので、
  **玉方が攻方の玉に王手をかけて手番を稼げてしまう**（攻め手順が別物になる）。
  **別の問題を解いて「解けました」と出すのが一番たちが悪い**

## 両玉が揃っている局面（比較用）

| エンジン | `go` | `go mate` |
|---|---|---|
| やねうら王 水匠5 | `score mate 1` / `bestmove G*5b` | 同じ（**`checkmate` ではなく `bestmove`**） |

⚠️ **`go mate` に `bestmove` で答えるエンジンがある**（やねうら王系）。
`client.Session.Mate` が**両方を終わりの合図として扱っている**のはこのため
（片方しか見ていないと、相手によっては**黙って返ってこない**）。

## KomoringHeights の細かい挙動

| 投げたもの | 返り |
|---|---|
| 詰みあり | `checkmate B*4d 1a1b R*2b 1b1a R*1b`（空白区切りの USI 手順） |
| **既に詰んでいる** | **`checkmate` だけ**（手順なし）。⚠️ **読み落とすと待ち続ける** |
| 詰みなし | `checkmate nomate` |
| `go mate infinite` → `stop` | 解けていれば `checkmate …` |
| 通常の `go` | `bestmove resign`（**通常解析には使えない**） |
| MultiPV 3（**`isready` の前に送る**） | `info … multipv 1/2/3 … pv …` が並ぶ（初手違いの別解＝**余詰**） |
| MultiPV 3（`isready` の**後**に送る） | **効かない**（候補は 1 本のまま） |

⚠️ **MultiPV は接続時に送るしかない**ので、詰将棋エンジンでは
**接続の指紋（`engineKey`）に MultiPV を入れて繋ぎ直させている**。
通常のエンジンでは今までどおり指紋から外す（本数を変えるたびに評価関数を読み直す）。

**option 一覧**（`usi` の応答より）: `Threads` / `MultiPV`(max 800) / `USI_Hash` /
`NodesLimit` / `PvInterval` / `RootIsAndNodeIfChecked` / `ScoreCalculation` /
`PostSearchLevel` / `GenerateAllLegalMoves` ほか。
**評価関数も定跡も要らない**（exe 1 つで動く）。

## 測り方

```bash
cd <エンジンのあるディレクトリ>
{ printf 'usi\nisready\nposition sfen <SFEN>\ngo mate 5000\n'; sleep 10; printf 'quit\n'; } \
  | ./<engine>.exe 2>&1 | grep -E "^checkmate|^bestmove"
```

- ⚠️ **`quit` をすぐ送らないこと**（考える前に終わる）。`sleep` で待つ
- ⚠️ **`setoption` は `isready` の前**（後から効かないエンジンがある。上記）
- 通常探索を見るなら `go mate 5000` を `go btime 0 wtime 0 byoyomi 1500` に替える
- **Go から測るなら `analyze.NewExecSession(path, options)` + `Session.Mate`。**
  ⚠️ **その使い捨てテストはリポジトリに残さないこと**（手元のエンジンに依存する
  テストを置くと、壊れたときに切り分けられなくなる。偽エンジンのテストは別にある）

## 使った詰将棋の局面（そのまま貼れる）

| 用途 | SFEN |
|---|---|
| 1 手詰・攻方の玉なし | `4k4/9/4P4/9/9/9/9/9/9 b G2r2b3g4s4n4l17p 1` |
| 5 手詰（余詰が複数ある） | `8k/9/9/9/9/9/9/9/9 b 2R2B4G4S4N4L18P 1` |
| 既に詰んでいる | `4k4/4G4/4P4/9/9/9/9/9/8K w 2r2b3g4s4n4l17p 1` |
| 詰みなし | `4k4/9/9/9/9/9/9/9/9 b 2r2b4g4s4n4l18p 1` |
