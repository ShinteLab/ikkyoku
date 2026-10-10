package app

// **読んだマスと、候補の盤のマスが合うか**の判定（2026-10-11。`TODO.md` 4「作り直す順」2・観点 2）。
//
// 追従は「読んだ駒を、ある盤のマスと比べる」をあちこちでやっている —— 速い経路の行き先の確かめ（`followBackedByRead`）・
// 候補選び（`followPickByRead`）・差し替えで消える手と種類の確かめ（`followGoneSeen` / `followKindKeepsOld`）、81 マスの読みの
// 差し替えの比べ（`followSideCost` / `followKindCost`）と、指された跡（`followArrived`）。以前はそれぞれが自分で比べていて、
// **弱い読みをどう扱うか・成駒をどう扱うか・種類が読めない "?" をどう扱うか**が場所ごとに書かれていた（観点 2「どの観測を
// どれだけ信じるかが 1 か所に無い」）。ここに 1 つにまとめ、どこも `followJudge` の答えから決める。
//
// ⚠️ **決まりはここに書くこと**（呼ぶ側で例外を足さない）。呼ぶ側が決めるのは、どの答えを要るとするか
// （行き先の確かめは「先後が合う」が要る、差し替えの比べは「食い違う」を数える、など）だけ。
// ⚠️ **`position` の探索の中の比べ（`Connect` の修復の費用・`backed`・`sideBetter`）はここを使わない**（探索の作りの一部。
// 強い読みの線だけ `position.sureCellCost` と揃えてある）。

import (
	"strings"

	"github.com/ShinteLab/ikkyoku/position"
)

// followMatch は 1 つの観点で、読みと候補のマスが合うか。
type followMatch int8

const (
	// followUnknown は**読みからは言えない**（読めていない・弱い読み・比べる材料が無い）。
	followUnknown followMatch = 0
	// followAgree は合う。
	followAgree followMatch = 1
	// followDisagree は食い違う。
	followDisagree followMatch = -1
)

// followObs は**1 マスの読み**（解析の向き）。作るのは `followObsBoard`（81 マスの読み）と `followObsRead`（速い経路で
// 読んだマス）・`followObsPiece`（駒の字だけ）。ゼロ値は「読んでいない」。
type followObs struct {
	read  bool // 読んだか（偽なら何も言えない）
	empty bool // 空きと読んだか
	sided bool // 先後が読めたか（駒のとき）
	black bool // 先手の駒と読んだか（sided のときだけ意味がある）
	// mark は駒の種類（大文字の SFEN。成駒は "+P" の形）。読めなければ ""。
	mark string
	// weight は読みの確かさ（費用表の値。速い経路で読んだマスは 1）。
	weight float64
}

// followVerdict は `followJudge` の答え。
type followVerdict struct {
	// presence は駒の有無。
	presence followMatch
	// side は先後（どちらも駒で、先後が読めたときだけ）。
	side followMatch
	// kind は駒の種類（どちらも駒で、種類が読めたときだけ。先後は問わない）。
	kind followMatch
	// weight は読みの確かさ（食い違いを数えるときの重み）。
	weight float64
}

// followJudge は候補の盤のマス c と、そのマスの読み o を比べる。決まりは 3 つ:
//
//   - ⚠️ **弱い駒の読み（`followArrivedMinCost` 未満）は何も言わない**（2026-10-08。光の差し込んだ空きを「先手の香」0.40 と
//     読んだ 1 枚で、幻の駒を手で説明して差し替えた）。空きの読みは弱くても「駒が無い」とは言う（費用は確かさのとおり）
//   - ⚠️ **成駒の候補は、成っていない姿と読めたときだけ種類が食い違う**（成駒は学習データにほぼ入らず読めないのが普通。
//     別の駒に読めても、読めない "?" でも食い違いにはしない）。成駒と読めたら合う
//   - **種類が読めない "?" は種類について何も言わない**。成っていない駒の候補は、その駒と読めたときだけ種類が合う
func followJudge(c position.Cell, o followObs) followVerdict {
	v := followVerdict{weight: o.weight}
	if !o.read || (!o.empty && o.weight < followArrivedMinCost) {
		return followVerdict{}
	}
	if o.empty {
		v.presence = followAgree
		if !c.IsEmpty() {
			v.presence = followDisagree
		}
		return v
	}
	if c.IsEmpty() {
		v.presence = followDisagree
		return v
	}
	v.presence = followAgree
	if o.sided {
		v.side = followAgree
		if c.Black() != o.black {
			v.side = followDisagree
		}
	}
	if o.mark != "" {
		mark := strings.ToUpper(c.Mark())
		switch {
		case o.mark == mark:
			v.kind = followAgree
		case c.Promoted():
			if o.mark == strings.TrimPrefix(mark, "+") {
				v.kind = followDisagree
			}
		default:
			v.kind = followDisagree
		}
	}
	return v
}

// followObsBoard は**81 マスで読んだ盤 read のマス (r, f)** の読み（確かさは費用表 cost。nil なら 1）。
func followObsBoard(read *position.Board, cost *position.CellCost, r, f int) followObs {
	b, err := read.At(r, f)
	if err != nil {
		return followObs{}
	}
	w := 1.0
	if cost != nil {
		w = cost[r][f]
	}
	return followObsCell(b, w)
}

// followObsCell は**読んだ盤のマス b**（確かさ w）の読み。
func followObsCell(b position.Cell, w float64) followObs {
	if b.IsEmpty() {
		return followObs{read: true, empty: true, weight: w}
	}
	return followObs{read: true, sided: true, black: b.Black(), mark: strings.ToUpper(b.Mark()), weight: w}
}

// followObsRead は**速い経路で読んだマス**（解析の向きのマス i。`CellPiece` は撮った画像の向き）の読み。読んでいなければゼロ値。
//
// `CellPiece` の形: 空きは Piece も Side も ""、切り出せなければ Piece が "?" で Side が ""、種類が読めなくても向きが読めれば
// Piece が "?" で Side が入る。後手目線（rotated）なら先後を入れ替える（撮った画像の手前側が後手）。
func followObsRead(read []CellPiece, i int, rotated bool) followObs {
	cp, found := followReadAt(read, i, rotated)
	if !found {
		return followObs{}
	}
	o := followObsPiece(cp.Piece)
	if up, readable := cellPieceUp(cp); readable && !o.empty {
		o.sided, o.black = true, up != rotated
	}
	return o
}

// followObsPiece は**駒の字だけ**の読み（先後は読まない。撮った画像の向きの SFEN。空は ""、読めない "?"）。
func followObsPiece(piece string) followObs {
	switch piece {
	case "":
		return followObs{read: true, empty: true, weight: 1}
	case "?":
		return followObs{read: true, weight: 1}
	}
	return followObs{read: true, mark: strings.ToUpper(piece), weight: 1}
}
