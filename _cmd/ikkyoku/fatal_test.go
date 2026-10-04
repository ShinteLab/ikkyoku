package main

import (
	"strings"
	"testing"

	"github.com/ShinteLab/ikkyoku"
	ikkyokuapp "github.com/ShinteLab/ikkyoku/app"
)

// fatalMessage（2026-10-04）の歯止め。
//
//   - **WebView2 が原因なら名指しすること** —— 画面が 1 枚も出ない原因として一番ありそうで、
//     利用者が自分で直せる唯一のもの
//   - **panic のスタックトレースを箱に全部載せないこと**（頭だけ。全部はログ）
//   - **ログの場所を載せること**（次に開く場所。書けていなければ載せない）
func TestFatalMessage(t *testing.T) {
	m := fatalMessage("unable to initialise WebView2 for window \"ikkyoku\"", false, `C:\app`)
	if !strings.HasPrefix(m, "ikkyoku を起動できませんでした。") {
		t.Fatalf("書き出し: %q", m)
	}
	if !strings.Contains(m, "WebView2 ランタイム") || !strings.Contains(m, "ログ: C:\\app") {
		t.Fatalf("WebView2 の案内かログの場所が無い: %q", m)
	}

	m = fatalMessage(strings.Repeat("x", fatalReasonMax*3), true, "")
	if !strings.HasPrefix(m, "ikkyoku は動作を続けられなくなったため終了します。") {
		t.Fatalf("起動後の書き出し: %q", m)
	}
	if strings.Contains(m, "ログ:") || strings.Contains(m, "WebView2") {
		t.Fatalf("要らないものが載っている: %q", m)
	}
	if n := len([]rune(m)); n > fatalReasonMax+100 {
		t.Fatalf("長すぎる: %d 文字", n)
	}
}

// 認識器の ⚠ の歯止め。**直ったら消えること**（設定タブで指し直したあと ⚠ が残ると、
// ほかの問題が増えても気づかれない）と、**帯の判定データは別の問題として出すこと**
// （駒種は読めているのに「認識器が無い」と出すと、直す場所を間違える）。
func TestSyncRecognizerIssue(t *testing.T) {
	issues := ikkyokuapp.NewIssueService()
	keys := func() []string {
		var ks []string
		for _, is := range issues.Report().Issues {
			ks = append(ks, is.Key)
		}
		return ks
	}

	syncRecognizerIssue(issues, RecognizerStatus{Mode: ikkyoku.SutemeSourceDir, Error: "no data", StripError: "no strip"})
	if got := keys(); len(got) != 2 || got[0] != issueRecognizer || got[1] != issueStrip {
		t.Fatalf("鍵 = %v", got)
	}

	syncRecognizerIssue(issues, RecognizerStatus{Mode: ikkyoku.SutemeSourceDir, Ready: true, StripError: "no strip"})
	if got := keys(); len(got) != 1 || got[0] != issueStrip {
		t.Fatalf("駒種が読めたのに残っている: %v", got)
	}

	syncRecognizerIssue(issues, RecognizerStatus{Mode: ikkyoku.SutemeSourceEmbed, Ready: true, StripSamples: 10})
	if got := keys(); len(got) != 0 {
		t.Fatalf("直ったのに残っている: %v", got)
	}
}

// 棋譜データベースの ⚠ も、開き直せたら消えること。
func TestSyncKifuDBIssue(t *testing.T) {
	issues := ikkyokuapp.NewIssueService()
	syncKifuDBIssue(issues, ikkyokuapp.KifuDBStatus{Path: `C:\x.db`, Error: "locked"})
	r := issues.Report()
	if len(r.Issues) != 1 || r.Issues[0].Key != issueKifuDB || !strings.Contains(r.Issues[0].Detail, `C:\x.db`) {
		t.Fatalf("一覧 = %+v", r.Issues)
	}
	syncKifuDBIssue(issues, ikkyokuapp.KifuDBStatus{Path: `C:\x.db`, Ready: true})
	if n := len(issues.Report().Issues); n != 0 {
		t.Fatalf("開けたのに残っている: %d", n)
	}
}
