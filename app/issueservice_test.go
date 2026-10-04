package app

import "testing"

// IssueService の歯止めは 2 つ。
//
//   - **直ったら消えること**（Clear）。⚠ が残り続けると、新しい問題が増えても気づかれない
//   - **同じ鍵は場所を変えずに差し替えること**。認識器を指し直すたびに一覧の並びが
//     入れ替わると、開いている吹き出しの中身が跳ねる
func TestIssueServiceSetReplacesInPlace(t *testing.T) {
	s := NewIssueService()
	var got []IssueReport
	s.Emit = func(name string, data any) {
		if name != IssuesChanged {
			t.Fatalf("イベント名 = %q", name)
		}
		got = append(got, data.(IssueReport))
	}

	s.Set(Issue{Key: "a", Level: IssueError, Title: "A"})
	s.Set(Issue{Key: "b", Level: IssueWarn, Title: "B"})
	s.Set(Issue{Key: "a", Level: IssueWarn, Title: "A2"})

	r := s.Report()
	if len(r.Issues) != 2 || r.Issues[0].Key != "a" || r.Issues[0].Title != "A2" || r.Issues[1].Key != "b" {
		t.Fatalf("一覧 = %+v（a が先頭のまま差し替わること）", r.Issues)
	}
	if len(got) != 3 {
		t.Fatalf("イベントの数 = %d、3 であること", len(got))
	}

	// 中身が同じなら流さない（直し直しても吹き出しがちらつかないように）。
	s.Set(Issue{Key: "a", Level: IssueWarn, Title: "A2"})
	if len(got) != 3 {
		t.Fatalf("同じ中身で流れた: %d", len(got))
	}
}

func TestIssueServiceClear(t *testing.T) {
	s := NewIssueService()
	n := 0
	s.Emit = func(string, any) { n++ }

	s.Set(Issue{Key: "a", Title: "A"})
	s.Clear("missing")
	if n != 1 {
		t.Fatalf("無い鍵の Clear でイベントが流れた: %d", n)
	}
	s.Clear("a")
	if n != 2 || len(s.Report().Issues) != 0 {
		t.Fatalf("Clear で消えていない: n=%d issues=%+v", n, s.Report().Issues)
	}
}

// Report は写しを返すこと（画面へ渡した後に Set しても、渡した一覧が書き換わらない）。
func TestIssueServiceReportIsCopy(t *testing.T) {
	s := NewIssueService()
	s.SetLogDir(`C:\logs`)
	s.Set(Issue{Key: "a", Title: "A"})
	r := s.Report()
	s.Set(Issue{Key: "a", Title: "B"})
	if r.Issues[0].Title != "A" {
		t.Fatalf("渡した一覧が書き換わった: %+v", r.Issues)
	}
	if r.LogDir != `C:\logs` {
		t.Fatalf("LogDir = %q", r.LogDir)
	}
	// Emit が nil でも落ちない。
	s.Clear("a")
}
