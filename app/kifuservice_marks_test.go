package app

import (
	"context"
	"testing"

	"github.com/ShinteLab/kicho/store"
)

// 2 手目が盤に当たらない棋譜（後手の歩を先手が動かしている。kicho の `brokenCSA` と同じ）。
// ⚠️ KIF ではエラーの印が付かない（kicho が盤に当てて確かめるのは KI2 と CSA）。
const testBrokenCSA = "PI\n+\n+7776FU\n+3334FU\n"

// kicho の印（エラー・指摘・親子）が一覧・詳細・カードにそのまま載ること（2026-11-19。`_docs/app-shelf.md` の「棋譜の印」）。
//
// ⚠️ 歯止めは —— **画面が本文を読まずに印を出せること**（値を運び損ねると印が黙って出なくなる）・
// **「要確認」が kicho の `NeedsAttention` に届くこと**・**棋譜全体への指摘は手数 0・"user" で付くこと**。
func TestKifuServiceCarriesKichoMarks(t *testing.T) {
	svc, _ := newTestKifuService(t)

	// 取得しただけのカード（貼り付け）にもエラーの印が載る。
	pasted, err := svc.PreviewPaste(testBrokenCSA)
	if err != nil {
		t.Fatalf("PreviewPaste: %v", err)
	}
	if pasted.Game.ErrorMove != 2 || pasted.Game.Error == "" {
		t.Fatalf("カードにエラーの印が載っていません: %d %q", pasted.Game.ErrorMove, pasted.Game.Error)
	}

	broken, err := svc.ImportKIF(testBrokenCSA)
	if err != nil {
		t.Fatalf("ImportKIF: %v", err)
	}
	if broken.ErrorMove != 2 || broken.Error == "" {
		t.Fatalf("一覧にエラーの印が載っていません: %+v", broken)
	}
	clean, err := svc.ImportKIF(testKIF)
	if err != nil {
		t.Fatalf("ImportKIF: %v", err)
	}

	// 「要確認」はエラーの印のある棋譜だけ。
	res, err := svc.Search(SearchQuery{NeedsAttention: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res.Games) != 1 || res.Games[0].ID != broken.ID {
		t.Fatalf("要確認に出る棋譜が違います: %+v", res.Games)
	}

	// 棋譜全体への指摘。
	if err := svc.FlagGame(clean.ID, "  "); err == nil {
		t.Errorf("理由が空なのに指摘できました")
	}
	if err := svc.FlagGame(clean.ID, "対局者名が違う"); err != nil {
		t.Fatalf("FlagGame: %v", err)
	}
	flags, err := svc.GameFlags(clean.ID)
	if err != nil {
		t.Fatalf("GameFlags: %v", err)
	}
	if len(flags) != 1 || flags[0].Move != 0 || flags[0].By != flagByUser ||
		flags[0].Reason != "対局者名が違う" || flags[0].Stale || flags[0].CreatedAt == "" {
		t.Fatalf("指摘の一覧が違います: %+v", flags)
	}
	d, err := svc.Get(clean.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if d.Flags != 1 || d.FlaggedMove != 0 || d.FlagsStale {
		t.Fatalf("詳細に指摘の集計が載っていません: %+v", d.GameSummary)
	}
	res, err = svc.Search(SearchQuery{NeedsAttention: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Matched != 2 {
		t.Fatalf("指摘を付けた棋譜が要確認に出ません: %+v", res.Games)
	}

	// 外すと要確認から消える。
	if _, err := svc.Unflag(flags[0].ID); err != nil {
		t.Fatalf("Unflag: %v", err)
	}
	if flags, _ := svc.GameFlags(clean.ID); len(flags) != 0 {
		t.Fatalf("指摘が外れていません: %+v", flags)
	}

	// 訂正版（子）。親には子の数、子には親との関係が載る。
	lib, err := svc.library()
	if err != nil {
		t.Fatal(err)
	}
	child, err := lib.SaveDerived(context.Background(), broken.ID, store.RelationCorrection, testKIF)
	if err != nil {
		t.Fatalf("SaveDerived: %v", err)
	}
	kids, err := svc.Children(broken.ID)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if len(kids) != 1 || kids[0].ID != child.ID || kids[0].ParentID != broken.ID ||
		kids[0].Relation != store.RelationCorrection || kids[0].ParentChanged || kids[0].ParentMissing {
		t.Fatalf("子の一覧が違います: %+v", kids)
	}
	parent, err := svc.Get(broken.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if parent.Children != 1 {
		t.Fatalf("親に子の数が載っていません: %+v", parent.GameSummary)
	}
	// 親を消すと子に「親は削除済み」が載る。
	if err := svc.Delete(broken.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	orphan, err := svc.Get(child.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !orphan.ParentMissing {
		t.Fatalf("親が消えたことが子に載っていません: %+v", orphan.GameSummary)
	}
}
