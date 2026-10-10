package main

import (
	"image"
	"slices"
	"strconv"
	"strings"

	"github.com/ShinteLab/ikkyoku"
	ikkyokuapp "github.com/ShinteLab/ikkyoku/app"
	"github.com/ShinteLab/ikkyoku/log"
	"github.com/ShinteLab/ikkyoku/recognize"
)

// 追従の**速い経路**の撮る側（2026-10-07）。
//
// 認識（約 0.1 秒）を回さず、**本譜の先端とぴったり合っていた 1 枚**（`cellBase`）と
// 今の 1 枚を**マスごとに**比べて（`ikkyoku.CellDiff`。数ミリ秒）、変わったマスの番号だけを返す。
// どの手かを決めるのは解析タブ側（`StudyService.FollowCells`）で、決まらなければ中継セッションが
// 同じ 1 枚を 81 マスで読む（`RecognizeQuiet`）。理由は `_docs/design-follow.md` の「速い経路」。
//
// ⚠️ **使ってよいのは、ふるいが「変化して止まった」と言った 1 枚だけ**。止まったと見なすのは 2 通り:
// 盤全体の画素が止まった（`gateReadSettled`）か、**変わったマスの組が 2 枚続けて同じ**（`gateReadCells`。
// 2026-10-07。ゲーム画面は駒を掴むと行き先が光り、「技有」の演出や盤の減光が重なって、盤全体の画素は
// 止まらないまま指し手のマスだけが落ち着く）。手が盤の上を動いているあいだは変わったマスも毎周変わるので、
// どちらでも弾ける。⚠️ **手が止まって被った 1 枚は通りうる** —— それを足さないのは解析タブ側の
// 行き先の確かめ（`FollowCells`。行き先に指した側の駒が読めるか）と、2 枚で確かめる控え（`followPending`）。
// ⚠️ **速い経路で決まらなかった組は、もう「止まった」と見なさない**（`cellsFailed`）。駒を掴んだまま
// 行き先が光り続けると同じ組が続くので、見なすと 0.5 秒ごとに 81 マスを読むことになる。
// ⚠️ **比べる相手は解析タブが「先端と合っている」と言った 1 枚だけ**（`SetCellBase`）。
// 前に読んだ 1 枚と比べると、それが手の被った 1 枚だったとき残像が混ざる（2026-10-06 に実測）。
// ⚠️ **変わったマスが多ければ 81 マスを読む**（`cellFastMax`。手や頭が被った 1 枚）—— ただし多い組も一度は解析タブ側へ
// 渡し、空きのマスだけが光っているなら読まない（2026-10-08。`fastCells`）。
const (
	// cellChanged は「変わったマス」と見なす、マスの内側で変わった画素の割合。
	// 録画の実測（2026-10-06）で、指した手のマスは最小 0.72、ほかのマスは最大 0.077 だった。
	cellChanged = 0.3
	// cellFastMax は速い経路で手順を割り出す変わったマスの数の上限（2 手で 4 マス + 前の手の色付け 2 マス + 余裕）。
	// ⚠️ **解析タブ側の `followCellsMax` と同じ値にすること**（あちらが超えた組を 81 マスへ回す）。
	cellFastMax = 8

	// cellSkipSame は**比べる相手から、どのマスも変わっていない**（`CaptureResult.Skipped`）。
	// ⚠️ **`unchanged`（前の 1 枚と同じ）とは別物。** 中継セッションは `unchanged` で控えた手を確かめる
	// （`FollowConfirm`）ので、混ぜると「先端に戻った」1 枚で控えた手を足してしまう。
	cellSkipSame = "same"
)

// SetCellBase は**直前に撮った 1 枚を、速い経路の比べる相手にする**（2026-10-07）。
//
// 呼ぶのは中継セッション（`app` の `followsession.go`）で、解析タブ側が `FollowAuto.AtFrame`（本譜の先端がこの 1 枚とぴったり合う）を
// 返したときだけ。⚠️ **ここで「合っているか」を判断しないこと**（局面を知らない層なので）。
//
//wails:ignore
func (s *CaptureService) SetCellBase() {
	s.mu.Lock()
	s.setCellBaseLocked(s.lastQuiet)
	s.mu.Unlock()
}

// setCellBaseLocked は比べる相手を img に替える（nil なら捨てる）。s.mu を持って呼ぶこと。
//
// ⚠️ **前の相手と比べた変わったマスの組は捨てること**（`gateCells` / `cellsFailed`）。相手が違えば
// 同じ組でも意味が違う —— 残すと、替えた直後の 1 枚が「2 枚続けて同じ」と見なされたり、
// 決まらなかった組の印が別の相手の組に掛かったりする。版（`cellGen`）も進める（測っている最中に
// 替わった 1 枚を、ふるいが前の版の組と比べないため）。
func (s *CaptureService) setCellBaseLocked(img image.Image) {
	s.cellBase = img
	s.cellGen++
	s.gateCells, s.cellsFailed, s.cellsUnmoved = frameCells{}, frameCells{}, frameCells{}
}

// CellsUnclear は**直前に撮った 1 枚の変わったマスの組を決まらなかった組として覚え、組が変わるのを待たせる**（2026-10-08）。
//
// 呼ぶのは中継セッション（`app` の `followsession.go`）で、速い経路が `FollowAuto.NoMoves`（変わったマスを説明できる手順が 1 つも無い）を返したとき。
// ゲーム画面で駒を掴むと、その駒が浮いて行き先が光り、考えているあいだ同じ組が続く。そこですぐ 81 マスを読むと
// 1 周が 1〜5 秒塞がり、そのあいだに指した手と相手の応手がまとめて遅れた（1 組 10 秒前後。2026-10-08 に実機で踏んだ）。
// 待っているあいだに指せば組が変わり、速い経路ですぐ取れる。⚠️ **待つ上限（`gateMovingMax`）では読む**（`gate`）——
// 盤全体が止まっていても待つ側に数えるため、「変化した」印（`gateDirty`）を立てる。
//
//wails:ignore
func (s *CaptureService) CellsUnclear() {
	s.mu.Lock()
	img := s.lastQuiet
	s.mu.Unlock()
	fc := s.cellsOf(img)
	if !fc.ok || len(fc.cells) == 0 {
		return
	}
	s.markCellsFailed(fc)
	s.mu.Lock()
	s.gateDirty, s.gateMoving = true, 0
	s.mu.Unlock()
	log.Follow.Info("変わったマスを説明できる手がありません（組が変わるのを待ちます）", "変わったマス", len(fc.cells))
}

// ForgetUnmoved は**「駒は動いていない」と覚えた組を忘れる**（2026-10-08）。
//
// 呼ぶのは中継セッション（`app` の `followsession.go`）で、81 マスの読み（保険の 1 枚など）で手が見つかったとき（控えた・足した）。覚えたままだと、
// 次の 1 枚が同じ組のまま `same` で省かれ、中継セッションが控えを捨てて、確かめが次の保険（10 秒後）まで延びる
// （ゲーム画面の ▲7八金 で踏んだ）。
//
//wails:ignore
func (s *CaptureService) ForgetUnmoved() {
	s.mu.Lock()
	s.cellsUnmoved = frameCells{}
	s.mu.Unlock()
}

// CellsUnmoved は**直前に撮った 1 枚の変わったマスの組を、「駒は動いていない」組として覚える**（2026-10-08）。
//
// 呼ぶのは中継セッション（`app` の `followsession.go`）で、81 マスを読んで解析タブ側が「何も指していない」（`FollowAuto.Kind` が `same`）と返し、
// 変わったマスに駒が空いて見えるマスが無いとき（`StudyService.CellsLookUnmoved`）と、光っているだけ（`LooksOnly`）のとき。
// 駒を掴んで行き先が光っているあいだ、ふるいはこの組を「変わっていない」に数え（`gate`）、同じ組を何度も
// 81 マスで読み直さない（2026-10-08 に、同じ組を 20 秒で 5 回読んでいた）。保険（`gateSkipMax`）は残る。
// ⚠️ **ここで「動いていないか」を判断しないこと**（局面を知らない層なので）。
// ⚠️ **比べる相手を替えたら忘れる**（`setCellBaseLocked`）。
//
//wails:ignore
func (s *CaptureService) CellsUnmoved() {
	s.mu.Lock()
	img := s.lastQuiet
	s.mu.Unlock()
	fc := s.cellsOf(img)
	if !fc.ok || len(fc.cells) == 0 {
		return
	}
	s.mu.Lock()
	s.cellsUnmoved = fc
	s.mu.Unlock()
}

// frameCells は**1 枚の、比べる相手から変わったマス**（撮った画像の向きの 81 マスの番号。`cellsOf`。2026-10-07）。
type frameCells struct {
	cells []int
	// ok は比べられたか（比べる相手とマス割りがあり、画像の大きさが同じ）。偽なら cells は nil。
	ok bool
	// gen は比べた相手の版（`CaptureService.cellGen`）。
	gen int
}

// same は a と b が**同じ相手と比べた、同じ組**か（どちらかが比べられていなければ偽）。
func (a frameCells) same(b frameCells) bool {
	return a.ok && b.ok && a.gen == b.gen && slices.Equal(a.cells, b.cells)
}

// cellsOf は**比べる相手から変わったマスを測る**（測るだけ。ログも判断もしない。数ミリ秒）。
//
// 1 枚につき 1 回だけ測り、ふるい（`gate`。2 枚続けて同じ組か）と速い経路（`fastCells`）の両方に渡す。
func (s *CaptureService) cellsOf(img image.Image) frameCells {
	s.mu.Lock()
	base, rects, region, bounds, gen := s.cellBase, s.cellRects, s.cellRegion, s.cellBounds, s.cellGen
	s.mu.Unlock()
	if img == nil || base == nil || len(rects) != 81 || !bounds.Eq(img.Bounds()) {
		return frameCells{}
	}
	ratios, ok := ikkyoku.CellDiff(base, img, rects, region)
	if !ok {
		return frameCells{}
	}
	cells := []int{}
	for i, v := range ratios {
		if v >= cellChanged {
			cells = append(cells, i)
		}
	}
	return frameCells{cells: cells, ok: true, gen: gen}
}

// markCellsFailed は fc を**速い経路で決まらなかった組**として覚える（`cellsFailed`）。
//
// 覚えておくと、同じ組が 2 枚続いてもふるいは「止まった」と見なさない（盤全体が止まるか、組が変わるか、
// 待つ上限 `gateMovingMax` で読むまで待つ）。⚠️ **比べる相手を替えたら忘れる**（`setCellBaseLocked`）。
func (s *CaptureService) markCellsFailed(fc frameCells) {
	if !fc.ok {
		return
	}
	s.mu.Lock()
	s.cellsFailed = frameCells{cells: slices.Clone(fc.cells), ok: true, gen: fc.gen}
	s.mu.Unlock()
}

// ReadCells は**直前に撮った 1 枚の、指定したマスの駒だけを読む**（2026-10-07）。
//
// 速い経路で成・不成や打った駒の種類が分かれたとき、中継セッションが `FollowAuto.ReadCells` を渡して呼ぶ。
// 81 マスを読み直す代わりに、違いの出るマスだけを読む。
// 手順が 1 通りに決まったときも、行き先に指した側の駒が来たかを確かめるために呼ばれる（同日。`app.FollowCells`）。
// ⚠️ **撮り直さない**（変わったマスを測った 1 枚と同じ 1 枚を読む）。
// ⚠️ **駒の向き（`CellPiece.Side`）を落とさないこと**（種類が読めない "?" でも向きで先後を確かめる）。
//
//wails:ignore
func (s *CaptureService) ReadCells(cells []int) ([]ikkyokuapp.CellPiece, error) {
	s.mu.Lock()
	img, rects, region := s.lastQuiet, s.cellRects, s.cellRegion
	s.mu.Unlock()
	pieces, err := recognize.ReadCells(img, region, rects, cells)
	if err != nil {
		return nil, err
	}
	out := make([]ikkyokuapp.CellPiece, len(cells))
	names := make([]string, len(cells))
	for i, c := range cells {
		out[i] = ikkyokuapp.CellPiece{Cell: c, Piece: pieces[i].Piece, Side: pieces[i].Side}
		names[i] = cellName(c) + "=" + pieces[i].Piece
		if pieces[i].Piece == "?" && pieces[i].Side != "" {
			names[i] += "(" + pieces[i].Side + ")" // 種類は読めないが向きは読めた
		}
	}
	log.Follow.Info("違いの出るマスを読みました（追跡）", "マス", strings.Join(names, ","))
	return out, nil
}

// ComparePieces は**動いた駒の、動く前と動いた先の見た目の近さ**を測る（2026-10-07。成・不成の証拠）。
//
// 組ごとに、動く前のマス（From）は速い経路の比べる相手の 1 枚（`cellBase`）から、動いた先のマス（To）は
// 直前に撮った 1 枚（`lastQuiet`）から、マス割り（`cellRects`）で切り出して `ikkyoku.PieceSimilarity` で比べる。
// マスは撮った画像の向きの番号（`app.CellPair`。中継セッションが `FollowAuto.Compare` をそのまま渡す）。
// 測れない組は -1（比べる相手もマス割りも無い・画像の大きさが違う・一様なマス）。
//
// ⚠️ **測るだけ。成ったかどうかは決めない**（しきい値は `app` の `promoChangedBelow` / `promoSameFrom`）。
// ⚠️ **撮り直さない**（変わったマスを測った・81 マスを読んだ 1 枚と同じ 1 枚を見る）。
//
//wails:ignore
func (s *CaptureService) ComparePieces(pairs []ikkyokuapp.CellPair) []float64 {
	s.mu.Lock()
	base, cur, rects, bounds := s.cellBase, s.lastQuiet, s.cellRects, s.cellBounds
	s.mu.Unlock()
	out := make([]float64, len(pairs))
	names := make([]string, len(pairs))
	usable := base != nil && cur != nil && len(rects) == 81 &&
		bounds.Eq(base.Bounds()) && bounds.Eq(cur.Bounds())
	// ⚠️ **`Same` の組はどちらも今の 1 枚で見る**（2026-10-09。打った駒と盤上の駒の比べ）。比べる相手の 1 枚は要らない。
	usableSame := cur != nil && len(rects) == 81 && bounds.Eq(cur.Bounds())
	for i, p := range pairs {
		out[i] = -1
		names[i] = cellName(p.From) + ">" + cellName(p.To) + "="
		from, ok := base, usable
		if p.Same {
			from, ok = cur, usableSame
		}
		if ok && p.From >= 0 && p.From < 81 && p.To >= 0 && p.To < 81 {
			if v, ok := ikkyoku.PieceSimilarity(from, rects[p.From], cur, rects[p.To]); ok {
				out[i] = v
			}
		}
		names[i] += strconv.FormatFloat(out[i], 'f', 3, 64)
	}
	log.Follow.Info("動く前と後の駒の見た目を比べました（追跡）", "組", strings.Join(names, ","))
	return out
}

// noteCellRects は 81 マスを読んだときのマス割りを控える（速い経路はこれでマスを切る）。
func (s *CaptureService) noteCellRects(img image.Image, d *recognize.Debug) {
	if d == nil || len(d.Cells) != 81 {
		return
	}
	rects := make([]image.Rectangle, 81)
	for _, c := range d.Cells {
		if c.Row < 0 || c.Row > 8 || c.Col < 0 || c.Col > 8 {
			return
		}
		rects[c.Row*9+c.Col] = c.Rect
	}
	s.mu.Lock()
	s.cellRects, s.cellRegion, s.cellBounds = rects, d.Region, img.Bounds()
	s.mu.Unlock()
}

// fastCells は**この 1 枚を速い経路で扱ってよいか**を返す（2026-10-07）。fc は `cellsOf` で測ったもの。
//
// 真になるのは、比べられて（`fc.ok`）、ふるいが「変化して止まった」と言った 1 枚（`gateReadSettled` か
// `gateReadCells`）のときだけ。偽なら 81 マスを読む（そのときも呼ぶ側は `fc.cells` を `FrameFits` のために返す）。
// ⚠️ **変わったマスが多い（`cellFastMax` を超える）組も渡すこと**（2026-10-08）。駒台の駒をつまむと打てるマスが
// 15 マスほど光る。光っているだけか（空きのマスだけが変わったか）は局面を知っている解析タブ側が決め
// （`StudyService.FollowCells` の `LooksOnly`）、そうでなければ手順を割り出さずに返す —— 中継セッションがその 1 枚を
// 81 マスで読み、`RecognizeQuiet` が決まらなかった組として覚える（今までと同じ）。
func (s *CaptureService) fastCells(fc frameCells) bool {
	if !fc.ok {
		return false
	}
	s.mu.Lock()
	why := s.gateRead
	s.mu.Unlock()
	if why != gateReadSettled && why != gateReadCells {
		return false
	}
	if len(fc.cells) > cellFastMax {
		log.Follow.Info("変わったマスが多いです（光っているだけか確かめ、違えば 81 マスを読みます）（追跡）", "変わったマス", len(fc.cells))
		return true
	}
	if len(fc.cells) > 0 {
		names := make([]string, 0, len(fc.cells))
		for _, i := range fc.cells {
			names = append(names, cellName(i))
		}
		log.Follow.Info("変わったマス（先端と合っていた 1 枚と）", "マス", strings.Join(names, ","), "ふるい", why)
	}
	return true
}

// cellName は 81 マスの番号を、撮った画像の向きの USI のマス（"7g"）にする（ログ用）。
func cellName(i int) string {
	if i < 0 || i > 80 {
		return "?"
	}
	return string([]byte{byte('9' - i%9), byte('a' + i/9)})
}
