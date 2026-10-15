package app

import (
	"context"
	"slices"
	"testing"

	"github.com/ShinteLab/ikkyoku/position"
)

// ⚠️ **足し済みの手があれば、81 マスの読みは比べる相手の局面から説明し、足し済みの手で始まる答えの残りを
// 先端から足す手として返すこと**（2026-10-11。`followExplainFromBase`）。
//
// 比べる相手は平手（ぴったり合った 1 枚）、▲7六歩 は推測で足した手。撮った 1 枚は ▲7六歩 △3四歩 ▲2六歩 のあと。
func TestFollowProbeExplainsFromBase(t *testing.T) {
	s, _ := following(t)
	s.markBase()
	guessMoves(t, s, "7g7f")
	board := boardAfter(t, "7g7f", "3c3d", "2g2f")

	p, err := probe(t, s, board, realConf(board, 0.9))
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if p.base == nil {
		t.Fatalf("比べる相手の局面から説明していません: %+v", p)
	}
	if len(p.base.rests) != 1 || !slices.Equal(p.base.rests[0], []string{"3c3d", "2g2f"}) {
		t.Fatalf("足し済みの手で始まる答えの残り = %v, want [[3c3d 2g2f]]", p.base.rests)
	}
	if p.Kind != FollowUnique || len(p.Candidates) != 1 || !slices.Equal(p.Candidates[0].Moves, []string{"3c3d", "2g2f"}) {
		t.Fatalf("先端から足す手として返していません: %+v", p)
	}

	// 2 枚続けて同じ答えで、先端から足す。
	for i := 0; i < 2; i++ {
		if _, err := s.FollowAuto(board, realConf(board, 0.9), nil, []PromoEvidence{}); err != nil {
			t.Fatalf("FollowAuto: %v", err)
		}
	}
	if got := mainLine(s); !slices.Equal(got, []string{"7g7f", "3c3d", "2g2f"}) {
		t.Fatalf("本譜 = %v, want [7g7f 3c3d 2g2f]", got)
	}
}

// ⚠️ **比べる相手の局面からの答えが足し済みの手の途中で分かれたら、差し替えの関門にかけて 2 枚目で差し替えること**
// （2026-10-11。以前は末尾から戻す候補を別に探していた）。
func TestFollowRewindFromBaseReplacesShortTail(t *testing.T) {
	s, _ := following(t)
	s.markBase()
	guessMoves(t, s, "2h4h")
	board := boardAfter(t, "7g7f")

	a, err := s.FollowAuto(board, evenConf(0.9), nil, []PromoEvidence{})
	if err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if a.Rewound || !a.Pending {
		t.Fatalf("1 枚目で差し替えています（2 枚目で確かめるはず）: %+v", a)
	}
	if a, err = s.FollowAuto(board, evenConf(0.9), nil, []PromoEvidence{}); err != nil {
		t.Fatalf("FollowAuto: %v", err)
	}
	if !a.Rewound {
		t.Fatalf("2 枚目で差し替えていません: %+v", a)
	}
	if got := mainLine(s); !slices.Equal(got, []string{"7g7f"}) {
		t.Fatalf("本譜 = %v, want [7g7f]", got)
	}
	if !hasBranch(s, "2h4h") {
		t.Errorf("差し替えた元の手（▲4八飛）が枝に残っていません")
	}
}

// ⚠️ **比べる相手の局面から採れる答えが無ければ、比べる相手より前の推測の手も戻して差し替えること**（控え。2026-10-11）。
//
// ▲4八飛 を推測で足し、その盤とぴったり合った 1 枚を比べる相手にした（読み違いの 1 枚）。そのあと △3四歩 を推測で足した。
// 撮った 1 枚は ▲7六歩 △3四歩 のあと —— ▲4八飛 は指されていない。比べる相手（▲4八飛 のあと）からは説明が付かないので、
// 末尾から 2 手戻して ▲7六歩 △3四歩 に差し替える。
func TestFollowRewindPastBaseWhenBaseUnsettled(t *testing.T) {
	s, _ := following(t)
	guessMoves(t, s, "2h4h")
	s.markBase()
	guessMoves(t, s, "3c3d")
	board := boardAfter(t, "7g7f", "3c3d")

	var a FollowAuto
	for i := 0; i < 2; i++ {
		var err error
		if a, err = s.FollowAuto(board, evenConf(0.9), nil, []PromoEvidence{}); err != nil {
			t.Fatalf("FollowAuto: %v", err)
		}
	}
	if !a.Rewound {
		t.Fatalf("差し替えていません: %+v", a)
	}
	if got := mainLine(s); !slices.Equal(got, []string{"7g7f", "3c3d"}) {
		t.Fatalf("本譜 = %v, want [7g7f 3c3d]", got)
	}
	if !hasBranch(s, "2h4h") {
		t.Errorf("差し替えた元の手（▲4八飛）が枝に残っていません")
	}
}

// ⚠️ **足し済みの手で始まる答えがぴったり合うなら、差し替えず、先端から足すこと**（2026-10-11）。
//
// ▲7六歩 を推測で足し、その 1 枚を比べる相手にしたあと、△3四歩 を推測で足した。撮った 1 枚は ▲2六歩 まで進んでいる。
// 比べる相手から △3四歩 ▲2六歩 でぴったり説明が付くので、▲7六歩 も △3四歩 も疑う理由は無い。
func TestFollowRewindKeepsTailWhenBaseFits(t *testing.T) {
	s, _ := following(t)
	guessMoves(t, s, "7g7f")
	s.markBase()
	guessMoves(t, s, "3c3d")
	board := boardAfter(t, "7g7f", "3c3d", "2g2f")

	for i := 0; i < 2; i++ {
		a, err := s.FollowAuto(board, evenConf(0.9), nil, []PromoEvidence{})
		if err != nil {
			t.Fatalf("FollowAuto: %v", err)
		}
		if a.Rewound {
			t.Fatalf("%d 枚目: 合っている本譜を差し替えました: %+v", i+1, a)
		}
	}
	if got := mainLine(s); !slices.Equal(got, []string{"7g7f", "3c3d", "2g2f"}) {
		t.Fatalf("本譜 = %v, want [7g7f 3c3d 2g2f]", got)
	}
}

// ⚠️ **比べる相手の局面から修復でしか合わないなら、比べる相手より前の推測の手も戻して差し替えること**（2026-10-11。`fits`）。
//
// ▲2六歩 を推測で足し、その 1 枚を比べる相手にした（読み違いの 1 枚のつもり）。そのあと △3四歩 を推測で足した。
// 撮った 1 枚は ▲7六歩 △3四歩 のあと。比べる相手（▲2六歩 のあと）からは △3四歩 ▲7六歩 に 2六・2七 を覆して合わせるしか
// ない（歩は戻れない）が、末尾から 2 手戻せば ▲7六歩 △3四歩 でぴったり合う。
func TestFollowRewindPastBaseWhenBaseOnlyRepairs(t *testing.T) {
	s, _ := following(t)
	guessMoves(t, s, "2g2f")
	s.markBase()
	guessMoves(t, s, "3c3d")
	board := boardAfter(t, "7g7f", "3c3d")
	// 2六・2七 の読みは弱い（比べる相手からでも、修復の予算に収まる）。
	conf := evenConf(0.9)
	conf[5*9+7], conf[6*9+7] = 0.3, 0.3

	b, cost, _, _, err := s.src.followFrame(board, conf, nil)
	if err != nil {
		t.Fatalf("followFrame: %v", err)
	}
	bpos, since, line, ok := s.followBaseFrom()
	if !ok {
		t.Fatalf("前提: 比べる相手の局面から説明するはず")
	}
	ans, err := followExplainFromBase(context.Background(), bpos, since, line, b,
		position.ConnectOptions{Cost: cost, Tolerance: position.DefaultTolerance})
	if err != nil || len(ans.rests) == 0 || ans.r.Cost == 0 {
		t.Fatalf("前提: 比べる相手の局面からは修復で合うはず: %+v（%v）", ans, err)
	}
	var a FollowAuto
	for i := 0; i < 2; i++ {
		if a, err = s.FollowAuto(board, conf, nil, []PromoEvidence{}); err != nil {
			t.Fatalf("FollowAuto: %v", err)
		}
	}
	if !a.Rewound {
		t.Fatalf("差し替えていません: %+v", a)
	}
	if got := mainLine(s); !slices.Equal(got, []string{"7g7f", "3c3d"}) {
		t.Fatalf("本譜 = %v, want [7g7f 3c3d]", got)
	}
	if !hasBranch(s, "2g2f") {
		t.Errorf("差し替えた元の手（▲2六歩）が枝に残っていません")
	}
}
