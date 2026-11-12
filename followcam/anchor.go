package followcam

import (
	"fmt"
	"image"

	"github.com/ShinteLab/ikkyoku"
	"github.com/ShinteLab/ikkyoku/log"
	"github.com/ShinteLab/ikkyoku/recognize"
)

// ShotAnchor は**撮った 1 枚の盤の見た目**と、そのときの撮影範囲・画像・パス（2026-10-09）。
// ⚠️ **ゼロ値は「無い」**（`OK`）。持つのは `_cmd/ikkyoku` の `CaptureService`（`lastShot` / `followShot`）で、
// ここへは `Camera.Corrected` から入る。
type ShotAnchor struct {
	Sig    recognize.Signature
	Region ikkyoku.Region
	Img    image.Image
	Path   string
	// Session は根を採った検討の id（`followShot` のときだけ埋まる）。
	Session string
}

// OK は盤の見た目を持っているか（ゼロ値・盤の取れなかった 1 枚は偽）。
func (a ShotAnchor) OK() bool { return a.Sig.Board.Dx() > 0 }

// AnchorBoard は**「これから追う盤」（マスタ）を決めて覚える**（2026-09-15）。
//
// ⚠️ **記述子だけを覚える**（局面は覚えない）。手が進めば駒は動くので、
// **同じ盤かどうかの判断には使えない。**
//
// ⚠️ **覚えるのは人が「追う」と言ったときだけ。** 撮るたびに覚え直すと、
// **大盤に切り替わった 1 枚でマスタがそちらへ移ってしまう**（そこから先は
// ずっと大盤を追う）。
//
// **訂正してから追うときは、訂正した 1 枚の盤をマスタにする**（2026-10-09。`CaptureService.SetFollowShot` / `PickAnchor`）。
// ⚠️ **押した瞬間の 1 枚をマスタにしない** —— 押したときに棋士の手が盤に被っていて、
// 初手から「別の盤」になった（実機で踏んだ）。押した瞬間に盤が映っていなくても始められる。
// 押した瞬間の 1 枚を使うのは、訂正した 1 枚が無いとき（棋譜・新規対局で根を入れ替えた）と、
// 撮ったあとに枠を動かしたときだけ。返すのは今撮った 1 枚（最初の 1 周として流す）。
func (s *Camera) AnchorBoard() (Shot, error) {
	s.mu.Lock()
	s.fol.boardAnchor, s.fol.anchorRegion, s.fol.quietOutcome = recognize.Signature{}, ikkyoku.Region{}, ""
	// ⚠️ **ふるいも白紙に戻すこと**（2026-09-18）。この 1 枚は
	// **これから追う盤を決めるための 1 枚**なので、**必ず読む**。
	s.resetGate()
	s.mu.Unlock()

	r, err := s.CaptureQuiet()
	if err != nil {
		return r, err
	}
	region, err := s.region()
	if err != nil {
		return r, err
	}
	session := ""
	if s.Session != nil {
		session = s.Session()
	}
	shot := ShotAnchor{}
	if s.Corrected != nil {
		shot = s.Corrected()
	}
	live, liveOK := recognize.SignatureOf(r.Debug)
	sig, fromShot, err := PickAnchor(shot, session, live, liveOK, region)
	if err != nil {
		return r, err
	}
	// master は録画に残すマスタ画像（nil なら今撮った 1 枚）。
	var master image.Image
	if fromShot {
		master = shot.Img
		// ⚠️ **今撮った 1 枚もマスタと突き合わせる**（最初の 1 周として流すので、手が被った 1 枚や
		// 大盤を、確かめないまま繋ぎに渡さない）。
		if liveOK {
			if same, why := sig.Matches(live); !same {
				r.OffBoard, r.OffBoardReason = true, why
			}
		}
		log.Follow.Info("訂正した 1 枚の盤を追う盤にします", "path", shot.Path)
	} else if shot.OK() && shot.Session == session {
		log.Follow.Info("撮ったあとに枠が動いたので、今映っている盤を追う盤にします")
	}
	dir := s.startFollowDir(master)
	s.mu.Lock()
	s.fol.boardAnchor, s.fol.anchorRegion = sig, region
	s.mu.Unlock()
	if s.AfterAnchor != nil {
		go s.AfterAnchor()
	}
	log.Follow.Info("追う盤を決めました", "board", sig.Board, "color", sig.Color, "dir", dir)
	// ⚠️ **始めたことと止めたことは本体のログにも出す**（2026-10-10）。追従のログは別のファイル（`log.Follow`）で、
	// 本体だけ読んでいると、いつ追っていたのか（エンジンの優先度・CPU の取り合いが起きていた時間）が分からない。
	log.Info("中継の追跡を始めました", "dir", dir)
	return r, nil
}

// PickAnchor は**追う盤（マスタ）**を決める（2026-10-09）。fromShot は訂正した 1 枚を使ったか。
//
// shot は根を採った（訂正した）1 枚（`followShot`）、session は解析タブの今の検討の id、
// live / liveOK は押した瞬間の 1 枚の盤、region は今の撮影範囲。
//   - **訂正した 1 枚が今の根のもので、枠も動いていなければそれ**（押した瞬間の 1 枚は見ない）
//   - ⚠️ **撮影範囲が違えば使わない** —— 記述子は画像の中の座標なので、枠を動かしたあとの盤は
//     必ず食い違う（そこから全部見送られる）。枠を動かすのは狙いを直す操作なので、今の 1 枚で決め直す
//   - それ以外は押した瞬間の 1 枚。盤が映っていなければ始めない
func PickAnchor(shot ShotAnchor, session string, live recognize.Signature, liveOK bool, region ikkyoku.Region) (recognize.Signature, bool, error) {
	if shot.OK() && session != "" && shot.Session == session && shot.Region == region {
		return shot.Sig, true, nil
	}
	if !liveOK {
		return recognize.Signature{}, false, fmt.Errorf("盤が映っていないので、追う盤を決められません")
	}
	return live, false, nil
}
