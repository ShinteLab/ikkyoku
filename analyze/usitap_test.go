package analyze

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

// USI のやり取りの写しは**読み書きを素通しにすること**（写すために中身を変えたら繋がらない）、
// **塊の途中で切れた行も 1 行として覚えること**、**直前の行は上限までで古いものから捨てること**。
func TestUSITapPassesThroughAndSplitsLines(t *testing.T) {
	tap := newUSITap("fake.exe", 1)
	src := "id name Fake\nusi" + "ok\ninfo depth 1 score cp 3\nbestmove 7g7f\n"
	// 行の途中で切れる塊で読ませる。
	in, out := tap.wrap(&chunkReader{s: src, n: 5}, &bytes.Buffer{})
	got, err := io.ReadAll(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != src {
		t.Errorf("読んだ中身が変わっています: %q", got)
	}
	if _, err := io.WriteString(out, "stop\n"); err != nil {
		t.Fatal(err)
	}
	if b := out.(tapWriter).w.(*bytes.Buffer).String(); b != "stop\n" {
		t.Errorf("書いた中身が変わっています: %q", b)
	}

	var lines []string
	for _, l := range tap.recent {
		lines = append(lines, l.dir+l.line)
	}
	want := "←id name Fake|←usiok|←info depth 1 score cp 3|←bestmove 7g7f|→stop"
	if strings.Join(lines, "|") != want {
		t.Errorf("覚えた行:\n got = %s\nwant = %s", strings.Join(lines, "|"), want)
	}

	for i := range tapRecent + 5 {
		tap.sent(fmt.Sprint("line ", i))
	}
	if len(tap.recent) != tapRecent || tap.recent[0].line != "line 5" {
		t.Errorf("上限 %d で古いものから捨てていません: %d 行・先頭 %q", tapRecent, len(tap.recent), tap.recent[0].line)
	}
}

// nil の写し（同梱エンジン）でも動くこと。
func TestUSITapNil(t *testing.T) {
	var tap *usiTap
	r, w := tap.wrap(strings.NewReader("x"), io.Discard)
	if _, ok := r.(*strings.Reader); !ok {
		t.Errorf("nil なら口をそのまま返すこと: %T", r)
	}
	_ = w
	tap.dump("nil でも落ちない")
}

type chunkReader struct {
	s string
	n int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.s == "" {
		return 0, io.EOF
	}
	n := min(c.n, len(c.s), len(p))
	copy(p, c.s[:n])
	c.s = c.s[n:]
	return n, nil
}
