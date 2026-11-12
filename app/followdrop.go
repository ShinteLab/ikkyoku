package app

// 打った駒の種類が決まらないとき（2026-10-09）。
//
// **実機の症状**（中継）: ▲3六香打 の香を、認識器が 4 枚とも「先手の歩」（確信度 0.81〜1.00）と読んだ。盤から分かるのは
// 「3六 に先手の駒が打たれた」までで、どの駒かは読んだ種類で選ぶが、歩は持ち駒に無いので打てる 3 通り（香・桂・銀）が同点になり、
// 「一意でないのに 1 本選ばない」で 2 分以上待った。後ろの △4五香打 も、3六 を説明できないので繋がらなかった。
//
// 決め方は 2 段（ユーザーの判断）:
//
//  1. **盤上の同じ側の駒と見た目を比べる**（`followDropPairs` → `followcam.Camera.ComparePieces` → `followPickDrop`）。
//     成・不成を見た目で決めたのと同じ考え方で、認識器の読み違いに左右されない（比べる相手は盤上の実物）
//  2. **それでも決まらなければ人に聞く**（`DropChoices` → 枠の下の帯 → `FollowChoose`）。中継を観ている人なら一目で分かる
//
// ⚠️ **決まらないまま 1 本選ばない**（打った駒の誤りはその駒が動くまで盤に表れず、差し替えで直せないことがある）。

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ShinteLab/core/sfen"

	"github.com/ShinteLab/ikkyoku/followcam"
	"github.com/ShinteLab/ikkyoku/log"
	"github.com/ShinteLab/ikkyoku/position"
)

// 打った駒を見た目で決めるしきい値（2026-10-09）。⚠️ **当て推量**（実機で詰めること）。
// 同じ駒どうしの近さは 0.98 前後（`followBackedByLooks` の実測）。
const (
	// dropLooksMin は「この種類だ」と言い切る近さの下限。
	dropLooksMin = 0.80
	// dropLooksMargin は 1 位と 2 位の種類の近さの差の下限（差が付かなければ決めない）。
	dropLooksMargin = 0.10
	// dropLooksRefs は種類ごとに比べる盤上の駒の数の上限。
	dropLooksRefs = 3
)

// followDropSplit は**下見の候補が、ある 1 手の打つ駒の種類だけで分かれている**形。
type followDropSplit struct {
	// idx はその手の位置（候補の手順の何手目か）。
	idx int
	// to は打ったマス（解析の向きの 81 マスの番号）。
	to int
	// cands は候補の手順（打つ駒の種類ごとに 1 本）。
	cands [][]string
}

// followDropOnly は下見の候補が**同じ手順で、ある 1 手の打つ駒の種類だけが違う**なら、その形を返す（そうでなければ nil）。
// ⚠️ **並べきれなかった（`More`）なら nil**（見えていない候補がある）。
func followDropOnly(p FollowProbe) *followDropSplit {
	if len(p.Candidates) < 2 || p.More {
		return nil
	}
	first := p.Candidates[0].Moves
	idx := -1
	var cands [][]string
	for _, c := range p.Candidates {
		if len(c.Moves) != len(first) {
			return nil
		}
		for i, m := range c.Moves {
			if m == first[i] {
				continue
			}
			// 違うのは打つ手どうしで、打ったマスが同じときだけ。
			if len(m) != 4 || len(first[i]) != 4 || m[1] != '*' || first[i][1] != '*' || m[2:] != first[i][2:] {
				return nil
			}
			if idx >= 0 && idx != i {
				return nil
			}
			idx = i
		}
		if !slices.ContainsFunc(cands, func(x []string) bool { return slices.Equal(x, c.Moves) }) {
			cands = append(cands, slices.Clone(c.Moves))
		}
	}
	if idx < 0 || len(cands) < 2 {
		return nil
	}
	mv := first[idx]
	r, f := int(mv[3]-'a'), 9-int(mv[2]-'0')
	if r < 0 || r > 8 || f < 0 || f > 8 {
		return nil
	}
	return &followDropSplit{idx: idx, to: r*9 + f, cands: cands}
}

// followDropPairs は、打ったマスと、**盤上にある同じ側の各候補の種類の駒**の組（撮った画像の向き。`Same`）を返す。
// letters は組ごとの種類（`ComparePieces` の答えを種類へ戻すため。pairs と同じ並び）。
//
// 比べる駒は、打つ直前の局面（本譜の先端から候補の手順の打つ手の前まで）で**指した側の成っていない同じ種類の駒**で、
// 打つ手のあとの手が動かしていないもの（今の 1 枚のそのマスに写っている）。
// ⚠️ **比べる駒が無い種類が 1 つでもあれば ok は偽**（その種類を外せないので見た目では決められない。人に聞く）。
func (s *StudyService) followDropPairs(ds *followDropSplit, rotated bool) (pairs []followcam.CellPair, letters []byte, ok bool) {
	s.mu.Lock()
	if s.study == nil {
		s.mu.Unlock()
		return nil, nil, false
	}
	_, tip, err := s.study.MainTip()
	s.mu.Unlock()
	if err != nil {
		return nil, nil, false
	}
	pos, ok := followReplay(tip, ds.cands[0][:ds.idx])
	if !ok {
		return nil, nil, false
	}
	black := pos.Turn == position.TurnBlack
	after := ds.cands[0][ds.idx+1:]
	to := ds.to
	if rotated {
		to = 80 - to // 解析の向き → 撮った画像の向き
	}
	for _, cand := range ds.cands {
		letter := cand[ds.idx][0]
		code, _ := sfen.ParsePieceLetter(letter)
		n := 0
		for i := 0; i < 81 && n < dropLooksRefs; i++ {
			c, err := pos.Board.At(i/9, i%9)
			if err != nil || c.IsEmpty() || c.Promoted() || c.Black() != black || c.Piece() != code || i == ds.to {
				continue
			}
			if slices.ContainsFunc(after, func(m string) bool { return usiTouches(m, i) }) {
				continue
			}
			from := i
			if rotated {
				from = 80 - i
			}
			pairs = append(pairs, followcam.CellPair{From: from, To: to, Same: true})
			letters = append(letters, letter)
			n++
		}
		if n == 0 {
			return nil, nil, false
		}
	}
	return pairs, letters, true
}

// followPickDrop は見た目の証拠から**打った駒の種類**を決める（決まらなければ decided は偽）。
// 種類ごとに盤上の駒との近さの最大を取り、1 位が `dropLooksMin` 以上で 2 位と `dropLooksMargin` 以上離れていれば決める。
// ⚠️ **測れなかった種類があれば決めない**（その種類を外す根拠が無い）。
func followPickDrop(pairs []followcam.CellPair, letters []byte, promo []PromoEvidence) (letter byte, sims string, decided bool) {
	best := map[byte]float64{}
	for i, p := range pairs {
		for _, e := range promo {
			if e.Same && e.From == p.From && e.To == p.To {
				if v, ok := best[letters[i]]; !ok || e.Sim > v {
					best[letters[i]] = e.Sim
				}
			}
		}
	}
	var order []byte
	for _, l := range letters {
		if !slices.Contains(order, l) {
			order = append(order, l)
		}
	}
	var parts []string
	top, second := -2.0, -2.0
	for _, l := range order {
		v, ok := best[l]
		if !ok {
			return 0, "", false
		}
		parts = append(parts, fmt.Sprintf("%c=%.3f", l, v))
		switch {
		case v > top:
			top, second, letter = v, top, l
		case v > second:
			second = v
		}
	}
	sims = strings.Join(parts, ",")
	if top < dropLooksMin || top-second < dropLooksMargin {
		return 0, sims, false
	}
	return letter, sims, true
}

// followSetDrop は読んだ盤の打ったマスを、決めた種類の指した側の駒に書き換える（確信度 1。覆させない）。
// `followApplyPromo` と同じく、**下見の前に盤を書き換えて**下見・控え・確かめが同じ盤を見るようにする。
func followSetDrop(board *position.Board, cost *position.CellCost, ds *followDropSplit, letter byte, black bool) bool {
	code, _ := sfen.ParsePieceLetter(letter)
	c, err := position.NewCell(code, black, false)
	if err != nil || board.Set(ds.to/9, ds.to%9, c) != nil {
		return false
	}
	if cost != nil {
		cost[ds.to/9][ds.to%9] = 1
	}
	return true
}

// followDropOffer は**人に聞いている、打った駒の候補**（`FollowChoose` の合鍵）。
type followDropOffer struct {
	tip   int
	cands [][]string
}

// offerDrop は打った駒の候補を人に聞く形で返す（`FollowAuto.DropChoices`）。
// ⚠️ **控えは捨てる**（どれを打ったかが決まるまで、先へ足す手は無い）。
func (s *StudyService) offerDrop(ds *followDropSplit, p FollowProbe, unseen *position.CellMask) FollowAuto {
	s.setPending(nil)
	s.mu.Lock()
	var choices []FollowChoice
	if s.study != nil {
		root, main := rootSFEN(s.study), s.study.MainLine()
		for _, c := range ds.cands {
			choices = append(choices, FollowChoice{Moves: slices.Clone(c), Text: followText(root, main, c)})
		}
	}
	prev := s.follow.dropOffer
	s.follow.dropOffer = &followDropOffer{tip: -1, cands: ds.cands}
	if s.study != nil {
		s.follow.dropOffer.tip = mainTipID(s.study)
	}
	same := prev != nil && prev.tip == s.follow.dropOffer.tip && slices.EqualFunc(prev.cands, ds.cands, slices.Equal[[]string])
	s.mu.Unlock()
	if !same {
		var texts []string
		for _, c := range choices {
			texts = append(texts, strings.Join(c.Text, " "))
		}
		log.Follow.Info("打った駒が決まりません（人に聞きます）", "候補", strings.Join(texts, " / "))
	}
	return FollowAuto{Kind: FollowChoices, Reason: "打った駒が決まりません", DropChoices: choices,
		Fit: p.Fit, Unseen: unseen.Count(), State: s.State()}
}

// FollowChoose は**人が選んだ、打った駒の候補**を本譜の先に足す（2026-10-09。枠の下の帯で選ぶ）。
//
// moves は `FollowAuto.DropChoices` のどれか 1 つの `Moves`。⚠️ **聞いたときから本譜の先端が動いていたら断る**
// （違う局面の先に足さない）。⚠️ **1 手なら推測の印を付けない**（人が決めた手。差し替えで戻さない）。
// 2 手以上なら、打つ手のほかは追従が割り出した手なので全部に印を付ける。
//
//wails:ignore
func (s *StudyService) FollowChoose(moves []string) (FollowAuto, error) {
	s.mu.Lock()
	off := s.follow.dropOffer
	s.follow.dropOffer = nil
	var tip, rev int
	atTip := false
	if s.study != nil {
		tip, rev = mainTipID(s.study), s.rev
		atTip = s.study.CurrentID() == tip
	}
	var root string
	var main []string
	if s.study != nil {
		root, main = rootSFEN(s.study), s.study.MainLine()
	}
	s.mu.Unlock()
	if off == nil || off.tip != tip || !slices.ContainsFunc(off.cands, func(c []string) bool { return slices.Equal(c, moves) }) {
		return FollowAuto{}, fmt.Errorf("選べる手が変わりました。追従が次の 1 枚でもう一度聞きます")
	}
	guess := len(moves) > 1
	applied, err := s.followGraft(moves, rev, atTip, guess)
	if err != nil {
		return FollowAuto{}, err
	}
	s.setPending(nil)
	text := followText(root, main, moves)
	logAdded("人が選んだ（打った駒）", len(main)+1, moves, text, guess, "枠の下の帯で選んだ")
	return FollowAuto{Kind: FollowUnique, Applied: true, Moves: slices.Clone(moves), Text: text,
		Added: applied.Added, Guess: guess, Fit: 1, Note: applied.Note, State: applied.State}, nil
}

// followDropLooks は FollowAuto の下見 p が**打つ駒の種類だけで分かれている**ときの扱い（2026-10-09）。
//
// done が真なら、この周の答えはそれで終わり（見た目を比べるよう頼んだ／人に聞いた）。偽で p2 が返れば、見た目で
// 決めた種類を盤に書き込んで下見し直したもの（以降は今までどおり。2 枚続けて同じ答えで足す）。
func (s *StudyService) followDropLooks(p FollowProbe, board *position.Board, cost *position.CellCost,
	unseen *position.CellMask, promo []PromoEvidence, rotated bool) (a FollowAuto, p2 *FollowProbe, done bool, err error) {
	ds := followDropOnly(p)
	if ds == nil {
		return FollowAuto{}, nil, false, nil
	}
	pairs, letters, ok := s.followDropPairs(ds, rotated)
	if !ok {
		// 比べる駒が盤上に無い種類がある。見た目では決められないので人に聞く。
		return s.offerDrop(ds, p, unseen), nil, true, nil
	}
	if promo == nil {
		log.Follow.Info("打った駒の種類で分かれたので、盤上の駒と見た目を比べます", "候補", len(ds.cands), "組", len(pairs))
		return FollowAuto{Kind: FollowChoices, Reason: "打った駒を見た目で確かめます", Compare: pairs,
			Fit: p.Fit, Unseen: unseen.Count(), State: s.State()}, nil, true, nil
	}
	letter, sims, decided := followPickDrop(pairs, letters, promo)
	if !decided {
		log.Follow.Info("打った駒を見た目で決められません", "近さ", sims)
		return s.offerDrop(ds, p, unseen), nil, true, nil
	}
	s.mu.Lock()
	var black bool
	if s.study != nil {
		if _, tip, e := s.study.MainTip(); e == nil {
			if pos, ok := followReplay(tip, ds.cands[0][:ds.idx]); ok {
				black = pos.Turn == position.TurnBlack
			}
		}
	}
	s.mu.Unlock()
	if !followSetDrop(board, cost, ds, letter, black) {
		return s.offerDrop(ds, p, unseen), nil, true, nil
	}
	log.Follow.Info("見た目を比べて打った駒を決めました", "マス", squareText(ds.to/9, ds.to%9), "駒", string(letter), "近さ", sims)
	np, e := s.followProbe(board, cost, unseen, cost != nil, rotated)
	if e != nil {
		return FollowAuto{}, nil, true, e
	}
	if followDropOnly(np) != nil {
		// 書き込んでもまだ分かれる（ほかのマスの読みの事情）。人に聞く。
		return s.offerDrop(ds, np, unseen), nil, true, nil
	}
	return FollowAuto{}, &np, false, nil
}
