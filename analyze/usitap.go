package analyze

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/ShinteLab/ikkyoku/log"
)

// usiTap はエンジン 1 接続ぶんの USI のやり取りを `log.USI` へ写す（2026-10-10）。
//
// **中継中にエンジンが `stop` に応えなくなり、CPU 100% のまま止められなくなった**
// （2026-10-10）。そのときのログには「前の解析が畳まれるのを待ちきれませんでした」しか
// 残っておらず、**何を送って何が返っていたか**が分からなかった。その穴を埋めるためのもの。
//
//   - 送った行と、`info` 以外の受けた行は Info で出す（解析 1 回で数行。配るビルドでも残る）
//   - ⚠️ **`info` は Debug**（探索中は毎秒何十行も出る）。代わりに**直前の行を覚えておき**、
//     困ったとき（`dump`）にまとめて出す。⚠️ **覚えるのは `info` を含む全部**（送った行と
//     受けた行の前後が分からないと、`stop` が届いたのかが読めない）
//   - ⚠️ **送る行は書く前に出す。** 書き込みが返らないこと自体が症状になりうる
//
// nil でも動く（同梱エンジンは写さない）。
type usiTap struct {
	engine string
	conn   int

	mu     sync.Mutex
	recent []tapLine // 直前の行（古い順。上限 tapRecent）
	partial []byte   // 受けた途中の行
	dumped bool      // 同じ接続で 2 回まとめて出さない
}

type tapLine struct {
	at   time.Time
	dir  string // "→"（送った）/ "←"（受けた）
	line string
}

// tapRecent は困ったときにまとめて出す直前の行の数。
const tapRecent = 60

func newUSITap(engine string, conn int) *usiTap {
	return &usiTap{engine: engine, conn: conn}
}

// wrap は Transport の読み書きの口に挟む。
func (t *usiTap) wrap(in io.Reader, out io.Writer) (io.Reader, io.Writer) {
	if t == nil {
		return in, out
	}
	return tapReader{t: t, r: in}, tapWriter{t: t, w: out}
}

func (t *usiTap) remember(dir, line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.recent) == tapRecent {
		copy(t.recent, t.recent[1:])
		t.recent = t.recent[:tapRecent-1]
	}
	t.recent = append(t.recent, tapLine{at: time.Now(), dir: dir, line: line})
}

func (t *usiTap) sent(line string) {
	t.remember("→", line)
	log.USI.Info("送ります", "engine", t.engine, "conn", t.conn, "at", stamp(time.Now()), "line", line)
}

func (t *usiTap) received(line string) {
	t.remember("←", line)
	if strings.HasPrefix(line, "info ") {
		log.USI.Debug("受けました", "engine", t.engine, "conn", t.conn, "at", stamp(time.Now()), "line", line)
		return
	}
	log.USI.Info("受けました", "engine", t.engine, "conn", t.conn, "at", stamp(time.Now()), "line", line)
}

// dump は困ったことを Warn で出し、直前のやり取りを続けて出す（**1 接続 1 回まで**）。
//
// Warn は本体のログにも出るので、本体を見た人が USI のログへ辿り着ける。
func (t *usiTap) dump(reason string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.dumped {
		t.mu.Unlock()
		return
	}
	t.dumped = true
	recent := append([]tapLine(nil), t.recent...)
	t.mu.Unlock()

	log.USI.Warn("エンジンとのやり取りが止まりました（直前の行を ikkyoku-usi のログに出します）",
		"engine", t.engine, "conn", t.conn, "reason", reason)
	for _, l := range recent {
		log.USI.Info("直前のやり取り", "engine", t.engine, "conn", t.conn,
			"at", stamp(l.at), "dir", l.dir, "line", l.line)
	}
}

// stamp はミリ秒までの時刻（ログの時刻は秒までなので、`stop` から何ミリ秒で返ったかが読めない）。
func stamp(at time.Time) string { return at.Format("15:04:05.000") }

type tapWriter struct {
	t *usiTap
	w io.Writer
}

// Write は 1 行ずつ来る（`client` は 1 コマンドを 1 回で書く）が、念のため行で割る。
func (w tapWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\r\n"), "\n") {
		w.t.sent(strings.TrimSpace(line))
	}
	return w.w.Write(p)
}

type tapReader struct {
	t *usiTap
	r io.Reader
}

func (r tapReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.t.feed(p[:n])
	}
	return n, err
}

// feed は受けた塊を行に割って写す（途中の行は次の塊まで持ち越す）。
func (t *usiTap) feed(b []byte) {
	t.mu.Lock()
	t.partial = append(t.partial, b...)
	var lines []string
	for {
		i := bytes.IndexByte(t.partial, '\n')
		if i < 0 {
			break
		}
		lines = append(lines, strings.TrimSpace(string(t.partial[:i])))
		t.partial = t.partial[i+1:]
	}
	t.mu.Unlock()
	for _, l := range lines {
		if l != "" {
			t.received(l)
		}
	}
}
