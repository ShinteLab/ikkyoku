package usi

import (
	"context"
	"io"
	"log/slog"

	shogi "github.com/ShinteLab/engine"
	"github.com/ShinteLab/engine/search"
)

// ⚠️ **このファイルは Step 2 で消える足場。**
//
// 案 B（ikkyoku を USI クライアントにする）の Step 1 は、**プロセスを起こさずに**
// 同一プロセスの `engine` を `io.Pipe` で直結する（`_docs/phase4-engine-usi.md`）。
// 狙いは「案 A と同じコストで案 B の形を手に入れる」ことで、
// **ikkyoku 側のコードは最初から USI クライアント**になっている。
//
//	Step 1  ikkyoku ──io.Pipe──> engine.NewUSI(...).Start()   ※同一プロセス（ここ）
//	Step 2  ikkyoku ──os/exec──> 外部エンジン .exe
//	Step 3  ikkyoku ──os/exec──> prokishi.exe                 ※設定のみ
//
// **Step 2 に進んだら、このファイルごと消して `os/exec` の Transport に差し替える。**
// `ikkyoku → engine` の Go 依存が残っているのはここだけなので、消せば依存も切れる
// （`engine` は「USI を話す exe」として繋がる相手の 1 つになる。engine/TODO.md）。

// localDepth は同梱エンジンの探索深さ。
//
// engine の `_samples.ThinkEngine` は 4 固定。こちらは検討用途なので少し深くするが、
// **打ち切りは stop 側が握る**ので、ここは「時間内に届けば嬉しい深さ」でよい。
const localDepth = 8

// Local は同一プロセスの `engine` を USI で話す相手として立ち上げる（Step 1）。
//
// **プロセス管理は一切要らない。** 起動・終了・タイムアウト・異常終了の処理が
// 出てくるのは Step 2 から。
func Local(ctx context.Context) (*Session, error) {
	// ikkyoku → engine（コマンド）
	cmdR, cmdW := io.Pipe()
	// engine → ikkyoku（応答）
	outR, outW := io.Pipe()

	u := shogi.NewUSI(outW, cmdR, &localEngine{})
	go func() {
		// Start は quit / EOF で返る。返ったらパイプを閉じて、
		// こちら側の読み取りにも EOF を伝える。
		if err := u.Start(); err != nil {
			slog.Debug("同梱エンジンが終了しました", "error", err)
		}
		_ = outW.Close()
		_ = cmdR.Close()
	}()

	s, err := Open(ctx, Transport{
		In:  outR,
		Out: cmdW,
		Close: func() error {
			// quit は Session.Close が送っている。ここは口を閉じるだけ。
			_ = cmdW.Close()
			return outR.Close()
		},
	})
	if err != nil {
		_ = cmdW.Close()
		_ = outR.Close()
		return nil, err
	}
	return s, nil
}

// localEngine は `engine` の探索を USI の口に繋ぐだけの実装。
//
// **`engine/_samples` を import しない。** あちらは `_` 始まりのサンプルで、
// 深さ 4 固定・オプション無し。ここは検討用途の設定を自分で持ちたいので、
// 30 行ほどを自前で持つほうが素直（どのみち Step 2 で消える）。
type localEngine struct{}

func (e *localEngine) GetName() string    { return "ikkyoku (engine 同梱)" }
func (e *localEngine) GetVersion() string { return "0.0.0" }
func (e *localEngine) GetAuthor() string  { return "ShinteLab" }

func (e *localEngine) GetBest(b *shogi.Board) (*shogi.Action, error) {
	return e.GetBestContext(context.Background(), b, nil)
}

// GetBestContext は ContextEngine の実装。**これがあるから stop が効く**
// （usi.go の go ハンドラがこちらを優先し、ctx のキャンセルで探索を畳む）。
func (e *localEngine) GetBestContext(ctx context.Context, b *shogi.Board, info func(shogi.Info)) (*shogi.Action, error) {
	opt := search.Options{Depth: localDepth, TT: true}
	if info != nil {
		opt.Info = func(si search.Info) {
			info(shogi.Info{Depth: si.Depth, ScoreCP: si.ScoreCP, Nodes: si.Nodes, PV: si.PV})
		}
	}
	// ⚠️ **Lazy SMP（Parallel）は使わない。** 想定外の局面で worker の goroutine が
	// panic するとアプリごと落ちる（recover は同じ goroutine でしか効かない）。
	// 速さより「撮った 1 枚で落ちないこと」（設計原則3）。
	res, err := search.BestContext(ctx, b, opt)
	if err != nil {
		if err == search.ErrNoMoves {
			// 合法手が無い＝詰んでいる。USI では投了。
			return shogi.ResignAction(), nil
		}
		return nil, err
	}
	return res.Action, nil
}
