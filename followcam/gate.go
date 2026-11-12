package followcam

import (
	"fmt"
	"image"

	"github.com/ShinteLab/ikkyoku"
	"github.com/ShinteLab/ikkyoku/log"
	"github.com/ShinteLab/ikkyoku/recognize"
)

// ふるいの判断（2026-09-18）。⚠️ **どれも実測で決めた数ではない。**
// 実機の中継で当たり具合を見て詰めること（追跡を止めたときに枚数を出している）。
const (
	// gateChangedRatio は「変化した」と見なす点の割合。
	//
	// **1 マスは盤の 1/81 ≈ 1.2%** で、指し手は動いた先と元の 2 マスが変わる。
	// ⚠️ **上げすぎないこと** —— **ふるいで落とした手は二度と戻らない**
	// （認識を 1 枚余分に読むほうがずっと軽い）。
	gateChangedRatio = 0.0015
	// gateMovingMax は静止を待つ周の上限。
	//
	// ⚠️ **待ち続けないための保険。** 盤の矩形の中に**動き続けるもの**
	// （寄りのカメラ・盤上に重なるテロップ）が入ると、静止判定だけでは
	// **永久に認識しない**という壊れ方をする。**止まらなくても、いずれ読む。**
	// ⚠️ **変わったマスの組で止まったと見なす（`gateReadCells`）ようになっても外さないこと**（2026-10-07）。
	// 比べる相手が無い・組が毎周変わる・速い経路で決まらなかった組（`cellsFailed`）が続く、のどれでも
	// 盤全体は止まらないので、最後に読むのはこれ。
	gateMovingMax = 8
	// gateSkipMax は**「変わっていない」で省き続けてよい周の数**（約 10 秒）。
	//
	// ⚠️ **これを外さないこと**（2026-09-18 に実機で踏んで足した）。
	// **指したのに「変わっていません」と出続けて、1 手も進まなかった** ——
	// 盤の駒と地色は明るさが近いので、**ふるいが外すことが実際にある。**
	//
	// ⚠️ **しきい値を詰めることで代えないこと。** どんな数にしても
	// **外したら永久に止まる**という作りのほうが問題で、
	// これは**外しても 10 秒で戻る**ことを保証する側。
	//
	// ⚠️ **長考中の無駄にはならない。** 10 秒に 1 枚の認識を払うだけで、
	// **省く前（毎周払う）とは桁が違う。**
	gateSkipMax = 40

	// 省いた理由（`Shot.Skipped`）。
	gateSkipUnchanged = "unchanged"
	gateSkipMoving    = "moving"

	// ふるいが読む側へ倒した理由（`gateRead`。2026-10-07）。**速い経路を使ってよいのは
	// 「変化して止まった」1 枚だけ** —— `gateReadSettled`（盤全体の画素が止まった）か
	// `gateReadCells`（盤全体は動いているが、変わったマスの組が 2 枚続けて同じ）。保険の 1 枚は
	// 81 マスを読んで答え合わせをする役で、止まらないまま読む 1 枚は手や頭が動いている最中かもしれない。
	gateReadFirst     = "first"
	gateReadSettled   = "settled"
	gateReadRestless  = "restless"
	gateReadInsurance = "insurance"
	// gateReadCells は**変わったマスの組が 2 枚続けて同じ**（2026-10-07）。ゲーム画面は駒を掴むと
	// 行き先が光り、「技有」の演出や盤の減光が重なるので、盤全体の画素は `moving` のまま止まらない
	// （実機で、指してから手が入るまで 1 手 10 秒かかった。待つ上限で 81 マスを読み、その確かめで
	// もう一度読んでいた）。指し手のマスは指し終えれば落ち着くので、組が同じなら止まったと見なす。
	// 手が盤の上を動いているあいだは組も毎周変わるので、ここでも弾ける。
	gateReadCells = "cells"
)

// gate は**この 1 枚を認識するか**を決める（空なら認識する）。
//
// ⚠️ **判断できないときは認識するほうへ倒すこと**（追う盤が決まっていない・
// 比べる相手が無い・大きさが違う）。**見落とすより 1 枚余分に読むほうが軽い。**
//
// ⚠️ **比べるのは盤の矩形の中だけ**（マスタが持っている）。画面全体で比べると、
// **消費時間の秒読みやテロップで毎周「変化あり」になり、ふるいが素通しになる。**
//
// ⚠️ **枠を動かしたときはここでは直さない。** 動かしている最中は画素が動くので
// `moving` が続き、`gateMovingMax` で認識に落ちて、そこでマスタが取り直される
// （`CaptureQuiet` の後段）。**同じ仕事を 2 か所でしない。**
//
// fc はこの 1 枚の、比べる相手から変わったマス（`cellsOf`）。**盤全体が動いていても、変わったマスの
// 組が前の 1 枚と同じなら止まったと見なす**（`gateReadCells`。2026-10-07）。⚠️ 速い経路で決まらなかった
// 組（`cellsFailed`）は見なさない。
func (s *Camera) gate(img image.Image, fc frameCells) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.fol.gateShots++
	anchor, prev := s.fol.boardAnchor, s.fol.gateFrame
	s.fol.gateFrame = img
	// 変わったマスの組は毎周入れ替える（次の周が「2 枚続けて同じ」かを見るため。比べられない周も入れる）。
	cellsStill := fc.same(s.fol.gateCells) && !fc.same(s.fol.cellsFailed)
	// unmoved は**読んで「駒は動いていない」と確かめた組のまま**か（`cellsUnmoved`。2026-10-08）。
	unmoved := len(fc.cells) > 0 && fc.same(s.fol.cellsUnmoved)
	// failed は**速い経路で決まらなかった組のまま**か（`cellsFailed`）。
	failed := len(fc.cells) > 0 && fc.same(s.fol.cellsFailed)
	s.fol.gateCells = fc

	// ⚠️ **ここで数えないこと**（2026-09-18）。この先に**盤の有無のふるい**
	// （`detectGate`）があり、そこでも落ちる。**数えるのは実際に認識した 1 枚だけ**
	// （`noteRead`）で、そうでないと省いた割合が実態より悪く見える。
	read := func(why string) string {
		s.fol.gateDirty, s.fol.gateMoving = false, 0
		s.fol.gateSkipped, s.fol.gateMaxSkip = 0, 0
		s.fol.gateRead = why
		return ""
	}
	if anchor.Board.Dx() <= 0 || prev == nil {
		return read(gateReadFirst)
	}
	ratio, ok := ikkyoku.FrameDiff(prev, img, anchor.Board)
	if !ok {
		return read(gateReadFirst)
	}
	insurance := func() string {
		// ⚠️ **ここが「黙って止まらない」ための保険**（2026-09-18）。
		// **ふるいが外していても、いずれ読む。**
		log.Follow.Info("変化が無いまま続いたので 1 枚読みます（ふるいの取りこぼしの確認）",
			"省いた周", s.fol.gateSkipped,
			"いちばん大きかった差", fmt.Sprintf("%.5f", s.fol.gateMaxSkip),
			"しきい値", gateChangedRatio)
		return read(gateReadInsurance)
	}
	// sameSkip は**駒は動いていない**として `same` で省く（81 マスは読まない）。
	// ⚠️ **「変わっていない」として数えること**（`gateSkipped`）。待つ上限（`gateMovingMax`）に
	// 数えると演出のあいだ 8 周ごとに 81 マスを読み、`read` で数え直すと保険（`gateSkipMax`）が
	// 二度と来ない —— 変わったマスの測り違いで手を見落としたとき、戻れなくなる。
	sameSkip := func() string {
		s.fol.gateDirty, s.fol.gateMoving = false, 0
		s.fol.gateSkipped++
		if s.fol.gateSkipped < gateSkipMax {
			return cellSkipSame
		}
		return insurance()
	}
	if ratio >= gateChangedRatio {
		if unmoved {
			// 駒を掴んで行き先が光っているあいだ（読んで確かめた組のまま。`cellsUnmoved`）。
			return sameSkip()
		}
		if cellsStill && len(fc.cells) > 0 {
			// 盤全体は動いているが、指し手のマスは落ち着いた（ゲーム画面の演出。`gateReadCells`）。
			log.Follow.Info("盤全体は動いていますが、変わったマスが 2 枚続けて同じなので止まったと見なします（追跡）",
				"変わったマス", len(fc.cells))
			return read(gateReadCells)
		}
		if cellsStill {
			// **比べる相手から、どのマスも変わっていない**まま 2 枚続いた（演出だけが動いている）。
			return sameSkip()
		}
		s.fol.gateDirty = true
		s.fol.gateMoving++
		if s.fol.gateMoving < gateMovingMax {
			return gateSkipMoving
		}
		return read(gateReadRestless) // 止まらないので、待つのをやめて読む
	}
	if !s.fol.gateDirty {
		s.fol.gateSkipped++
		if ratio > s.fol.gateMaxSkip {
			s.fol.gateMaxSkip = ratio
		}
		if s.fol.gateSkipped < gateSkipMax {
			return gateSkipUnchanged
		}
		return insurance()
	}
	if unmoved {
		// 変化して止まったが、読んで「駒は動いていない」と確かめた組のまま（読み直さない）。
		return sameSkip()
	}
	if failed {
		// 盤全体は止まったが、速い経路で決まらなかった組のまま（駒を掴んで考えている。2026-10-08）。
		// ⚠️ **盤全体が動いているときと同じく、組が変わるか待つ上限まで待つ**（すぐ読むと 1 周が塞がる）。
		s.fol.gateMoving++
		if s.fol.gateMoving < gateMovingMax {
			return gateSkipMoving
		}
		return read(gateReadRestless)
	}
	return read(gateReadSettled) // 変化したあと静止した ＝ 読むならこの 1 枚
}

// detectMissMax は**盤の有無のふるいで見送り続けてよい周の数**。
//
// ⚠️ **保険を外さないこと。** 検出だけでは判断の材料が少なく、
// **こちらだけで落とし続けると「黙って何も起きない」**になる
// （この追従で何度もやった壊れ方）。**見送りが続いたら 1 枚は必ず読む。**
const detectMissMax = 8

// detectGate は**81 マスの推論に入る前に、盤の有無だけで落とす**（2026-09-18）。
//
// **大盤（解説用）が映っているあいだ、画素は動き続ける**ので画素差分のふるいは
// 素通りする。**矩形がマスタと合わないなら推論に入らない。**
//
// ここで探した矩形は `Recognize` に渡していない（`Recognize` が同じ画像でもう一度探す。
// 盤の検出は約 0.04 秒）。
//
// ⚠️ **見つからなかったときに落とさないこと。** `DetectRegion` は
// `suteme.Recognize` と違って**画像全体へのフォールバックを持たない**ので、
// **枠を盤にぴったり合わせている人ほど厳しく出る**おそれがある。
// **判断できないときは読むほうへ倒す**（このふるい全体の作法と同じ）。
//
// 戻り値は**落とす種類**（空なら読む）と理由。
//
// ⚠️ **「盤が見つからない」と「別の盤」を分けること。** ユーザがすることが違う
// （待つ / 枠を直す）し、**画面にも別の顔で出る**。
func (s *Camera) detectGate(img image.Image, region ikkyoku.Region) (string, string) {
	s.mu.Lock()
	anchor, moved := s.fol.boardAnchor, s.fol.anchorRegion != region
	s.mu.Unlock()
	// ⚠️ **枠を動かしたら通すこと。** マスタは**画像の中の座標**なので、
	// 枠が動けば盤は同じでも必ず食い違う —— ここで落とすと
	// **マスタを取り直す後段（`CaptureQuiet`）に永久に辿り着かない**
	// （2026-09-15 に「枠をずらしたら死ぬ」を踏んだのと同じ形）。
	if anchor.Board.Dx() <= 0 || moved {
		return "", ""
	}

	got, err := recognize.DetectRegion(img)
	if err != nil {
		// **盤が見つからない**（CM・解説・寄り）。⚠️ **ここは弱い判断**なので、
		// 見送り続けずに時々は読む（`detectMissMax`）。
		return s.detectMiss(detectNone, "")
	}
	sig := recognize.Signature{Frame: img.Bounds(), Board: got.Rect}
	if same, why := anchor.Matches(sig); !same {
		return s.detectMiss(detectOff, why)
	}
	s.mu.Lock()
	s.fol.detectMisses = 0
	s.mu.Unlock()
	return "", ""
}

// 盤の有無のふるいが落とした種類。
const (
	// detectNone は**盤が見つからない**（CM・解説・寄り）。
	detectNone = "none"
	// detectOff は**追っている盤ではない**（大盤など）。
	detectOff = "off"
)

// detectMiss は見送りを数え、**続きすぎたら通す**（保険）。
func (s *Camera) detectMiss(kind, why string) (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fol.detectMisses++
	if s.fol.detectMisses >= detectMissMax {
		s.fol.detectMisses = 0
		return "", "" // 落とし続けない（読んで確かめる）
	}
	return kind, why
}

// noteRead は**実際に認識した 1 枚**を数える。
func (s *Camera) noteRead() {
	s.mu.Lock()
	s.fol.gateReads++
	s.mu.Unlock()
}

// resetGate はふるいを白紙に戻す（追う盤を決め直すとき。`AnchorBoard`。追跡の始まりと終わりは `captureFollow` ごと作り直す）。
//
// ⚠️ **枚数も 0 に戻すこと** —— 数えているのは**この回の追跡**で、
// 前の対局と混ぜると何を測ったのか分からなくなる。
func (s *Camera) resetGate() {
	s.fol.gateFrame, s.fol.gateDirty, s.fol.gateMoving, s.fol.detectMisses = nil, false, 0, 0
	s.fol.gateSkipped, s.fol.gateMaxSkip = 0, 0
	s.fol.gateShots, s.fol.gateReads = 0, 0
	s.fol.gateCells, s.fol.cellsFailed, s.fol.cellsUnmoved = frameCells{}, frameCells{}, frameCells{}
}

// noteQuiet は**結果の種類が変わったときだけ**ログに出す。
//
// ⚠️ **毎回出さないこと**（1 秒ごとに回るので読めなくなる）。
// ⚠️ **何も出さないのも駄目** —— 盤が映っていないあいだ Go 側が無言になると、
// **正常に見送っているのか止まっているのかがログから分からない**（実機で聞かれた）。
func (s *Camera) noteQuiet(r Shot) {
	kind, msg := "board", "盤を見つけました（追跡）"
	switch {
	// ⚠️ **読んでいない周を「盤が映っていません」と言わないこと**（2026-09-18）。
	// **原因が全く違うのに同じ顔で出る**のが、この追従で何度もやった壊れ方
	// （`TODO.md` 4 の ③）。省いたのは**変わっていないから**で、正常そのもの。
	case r.Skipped != "":
		kind, msg = "skip:"+r.Skipped, "変わっていないので認識を省いています（追跡）"
		switch r.Skipped {
		case gateSkipMoving:
			msg = "動いているので止まるのを待っています（追跡）"
		case cellSkipSame:
			msg = "先端と合っていた 1 枚から、どのマスも変わっていません（追跡）"
		}
	case r.Fast:
		kind, msg = "cells", "変わったマスだけで手を割り出します（追跡）"
	// ⚠️ **`OffBoard` を先に見ること**（2026-09-18）。盤の有無のふるいで落ちた周は
	// **読んでいないので SFEN が空**で、順番が逆だと**別の盤を「盤が映っていません」**
	// と言う（**原因が違うのに同じ顔で出る**、この追従で何度もやった壊れ方）。
	case r.OffBoard:
		kind, msg = "off", "別の盤が映っています（追跡・見送り）"
	case r.SFEN == "":
		kind, msg = "none", "盤が映っていません（追跡・見送り）"
	}
	s.mu.Lock()
	changed := s.fol.quietOutcome != kind
	s.fol.quietOutcome = kind
	s.mu.Unlock()
	if changed {
		log.Follow.Info(msg, "reason", r.OffBoardReason, "confidence", r.Confidence)
	}
}
