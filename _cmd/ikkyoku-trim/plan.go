package main

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"sort"
)

// Plan は 1 段目が見つけたものと、2 段目が切ってつなぐ区間（JSON に書く。手で直して `-segments` で渡し直せる）。
type Plan struct {
	Inputs []Input `json:"inputs"`
	// Before / After は変化の前後に残した秒。FPS は変化を探したコマ数。Threshold / Scene は変化と見なした線。
	Before    float64 `json:"before"`
	After     float64 `json:"after"`
	FPS       float64 `json:"fps"`
	Threshold float64 `json:"threshold"`
	Scene     float64 `json:"scene"`
	// Switch は対局の盤が映らなくなった・戻ったときに短く残した秒。
	Switch float64 `json:"switch"`
	// Boards は盤の矩形を決めた・決め直した記録（どこを見て変化を測ったか）。
	Boards []BoardNote `json:"boards"`
	// Changes は変化と見なしたコマ。
	Changes []Change `json:"changes"`
	// Quiet は変化と見なさなかったコマの、盤の矩形の中の割合の分布（線を詰める材料）。
	Quiet Histogram `json:"quiet"`
	// Segments は残す区間（動画ごとの秒）。⚠️ **2 段目はこれだけを見る**。
	Segments []Segment `json:"segments"`
	// Total はつないだ録画の長さ、Kept は残す長さ（秒）。
	Total float64 `json:"total"`
	Kept  float64 `json:"kept"`
}

// BoardNote は盤の矩形を決めたときの記録。
type BoardNote struct {
	File       string          `json:"file"`
	Time       float64         `json:"time"`
	Rect       image.Rectangle `json:"rect"`
	Confidence float64         `json:"confidence,omitempty"`
	// How は "region"（-region で指定）・"detect"（最初に見つけた盤を対局の盤にした）・
	// "scaled"（画面の大きさが違う動画に変わったので、対局の盤を比で写した）。
	How string `json:"how"`
}

// Change は変化と見なしたコマ 1 つ。
type Change struct {
	File string `json:"file"`
	// Time はその動画の中の秒、At はつないだ録画の中の秒。
	Time float64 `json:"time"`
	At   float64 `json:"at"`
	// Board は対局の盤の矩形の中の割合、Scene は画面全体の割合（どちらも測っていなければ -1）。
	Board float64 `json:"board"`
	Scene float64 `json:"scene"`
	// Why は "盤"（対局の盤の中が変わった）・"対局の盤が映らなくなった"・"対局の盤が戻った"。
	Why string `json:"why"`
	// Before / After はこの変化の前後に残す秒（盤の変化は -before / -after、映らなくなった・戻ったは短い）。
	Before float64 `json:"before"`
	After  float64 `json:"after"`
}

// Segment は残す区間 1 つ（その動画の中の秒）。
type Segment struct {
	File  string  `json:"file"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Histogram は割合の分布（Edges[i] 以上 Edges[i+1] 未満が Counts[i]。最後は Edges の最後以上）。
type Histogram struct {
	Edges  []float64 `json:"edges"`
	Counts []int     `json:"counts"`
	// Max は見た割合の最大。
	Max float64 `json:"max"`
}

func newHistogram() Histogram {
	edges := []float64{0, 0.0001, 0.0002, 0.0005, 0.001, 0.0015, 0.003, 0.01, 0.03, 0.1}
	return Histogram{Edges: edges, Counts: make([]int, len(edges))}
}

func (h *Histogram) add(v float64) {
	if v > h.Max {
		h.Max = v
	}
	i := sort.Search(len(h.Edges), func(i int) bool { return h.Edges[i] > v }) - 1
	if i < 0 {
		i = 0
	}
	h.Counts[i]++
}

// buildSegments は変化の時刻（つないだ録画の中の秒）から、残す区間を動画ごとに組む。
//
// 変化 1 つにつき [At - Before - step, At + After]（step は 1 コマの間隔。変わる直前のコマも含める）を残し、
// 重なる・接する区間はまとめ、つないだ録画の外は切り捨て、動画の境目で分ける。
func buildSegments(inputs []Input, changes []Change, step float64) ([]Segment, float64) {
	if len(inputs) == 0 {
		return nil, 0
	}
	last := inputs[len(inputs)-1]
	total := last.Offset + last.Duration
	type span struct{ a, b float64 }
	var raw []span
	for _, c := range changes {
		a, b := max(c.At-c.Before-step, 0), min(c.At+c.After, total)
		if b > a {
			raw = append(raw, span{a, b})
		}
	}
	// ⚠️ **始まりの順に並べてからまとめる**（変化ごとに前後の長さが違うので、後の変化の区間が前から始まりうる）。
	sort.Slice(raw, func(i, j int) bool { return raw[i].a < raw[j].a })
	var spans []span
	for _, s := range raw {
		if n := len(spans); n > 0 && s.a <= spans[n-1].b+step {
			spans[n-1].b = max(spans[n-1].b, s.b)
			continue
		}
		spans = append(spans, s)
	}
	var segs []Segment
	kept := 0.0
	for _, s := range spans {
		kept += s.b - s.a
		for _, in := range inputs {
			a, b := max(s.a, in.Offset), min(s.b, in.Offset+in.Duration)
			if b-a <= 0 {
				continue
			}
			segs = append(segs, Segment{File: in.File, Start: a - in.Offset, End: b - in.Offset})
		}
	}
	return segs, kept
}

func writePlan(path string, p Plan) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func readPlan(path string) (Plan, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Plan{}, err
	}
	var p Plan
	if err := json.Unmarshal(b, &p); err != nil {
		return Plan{}, fmt.Errorf("%s を読めません: %w", path, err)
	}
	if len(p.Segments) == 0 {
		return Plan{}, fmt.Errorf("%s に区間がありません", path)
	}
	return p, nil
}
