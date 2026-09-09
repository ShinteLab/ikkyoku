package app

import (
	"testing"
	"time"
)

// 「考える時間を使い切ったか」の見分け（`Interrupted`）。
//
// ⚠️ **ここが歯止めているのは連続解析が一気に走り抜けること。** 打ち切られた
// 解析も `analyze:done` を出す（設計原則3: そこまでの評価値は残る）ので、
// **見分けられないと「1 手ぶん終わった」と読んで次の手へ進む** ——
// 5 秒 × 10 手が数秒で流れる。
func TestInterruptedRun(t *testing.T) {
	const sec = 5 * time.Second
	tests := []struct {
		name      string
		movetime  time.Duration
		elapsedMS int64
		stopped   bool
		want      bool
	}{
		// 頼んだ秒数を使い切った（時間切れ）。**普通の終わり方。**
		{"使い切った", sec, 5000, true, false},
		// ⚠️ **往復のぶんだけ手前で終わるのは正常**（`stop` を送るのはこちら）。
		{"わずかに手前", sec, 4700, true, false},
		// 外から止められた（別の解析が始まった・別の窓が Stop した）。
		{"打ち切られた", sec, 900, true, true},
		// ⚠️ **エンジンが自分で bestmove を返したときは正常**（詰みなど）。
		// ここを取り違えると、**詰みの局面で連続解析が止まる。**
		{"エンジンが自分で返した", sec, 120, false, false},
		// ⚠️ **「無制限」では判断しない**（止めて終わるのが普通の終わり方）。
		{"無制限", 0, 120, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := interruptedRun(tt.movetime, tt.elapsedMS, tt.stopped); got != tt.want {
				t.Errorf("interruptedRun(%v, %d, %v) = %v, want %v",
					tt.movetime, tt.elapsedMS, tt.stopped, got, tt.want)
			}
		})
	}
}
