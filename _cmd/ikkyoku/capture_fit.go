package main

// CaptureService のうち、**ガイド枠の自動フィット**（盤を探して枠を合わせる）（2026-11-13 に captureservice.go から分けた）。
//
// ⚠️ **寸法と幾何は `ikkyoku/guide`**。ここは枠の HWND と画面を触るぶんだけ。

import (
	"fmt"
	"image"
	"time"

	"github.com/ShinteLab/ikkyoku"
	"github.com/ShinteLab/ikkyoku/guide"
	"github.com/ShinteLab/ikkyoku/log"
	"github.com/ShinteLab/ikkyoku/recognize"
)

// FitResult はガイド枠の自動フィットの結果。
//
// **見つからなかったことはエラーではない。** 盤が映っていない画面に枠を置いている
// ことも、認識が外すこともある(設計原則3)。そのときは Fitted=false と理由を返し、
// 枠は 1px も動かさない。error になるのはキャプチャ自体ができなかったときだけ。
type FitResult struct {
	Fitted     bool    `json:"fitted"`
	Confidence float64 `json:"confidence"`
	Message    string  `json:"message"`
}

// FitFrame は**画面に出ている盤**を探して、そこへガイド枠を合わせる。
//
// **枠を盤に合わせること自体が認識の精度に効く。** suteme は「盤だけが写っている画像」
// なら素直に解けるので、枠が盤に合っているほど当たりが良くなる。位置合わせを楽にする
// だけの機能ではない。
//
// 探すのは**枠の内側 → 枠がいるディスプレイ全体**の順(findBoard)。「枠の内側だけ」を
// やめたのは、枠を先に盤へ近づけておく必要があり、**それができるなら手で合わせるのと
// あまり変わらない**ため。今は「その画面に今出ている盤に合わせる」という機能になっている。
//
// 画面全体を撮るときは**自分のウィンドウを画面から消してから撮る**:
//
//   - 枠は Hide() して撮り、撮り終えたら戻す。赤いガイド枠が写ると格子の検出を汚す
//   - メイン画面は消さず、撮った画像の上でその矩形を塗り潰す(maskWindows)。
//     **メイン画面には `<shogi-board>` が本物の盤を描いている**ので、放っておくと
//     中継の盤より綺麗なそちらが選ばれる
//
// 隠して撮り直すぶん、押してから結果が出るまでに一呼吸ある(枠が一瞬消える)。
// **撮る操作(Capture)にこの方式を持ち込まないこと。** あちらはタイミングに依存しない
// 決定論的な領域算出が要点で、こちらはユーザーが押した 1 回きりの操作なので待てる。
//
// 枠の位置はユーザーが手で合わせたものなので、**怪しい検出結果では動かさない**。
// 判断は recognize.DetectRegion の信頼度・最小サイズ・「枠の中で半分に縮む候補は採らない」
// の 3 つ。
func (s *CaptureService) FitFrame() (FitResult, error) {
	region, scale, err := s.captureRegion()
	if err != nil {
		return FitResult{}, err
	}
	b, conf, err := s.findBoard(region)
	if err != nil {
		return FitResult{}, err
	}
	if b.Empty() {
		return FitResult{
			Confidence: conf,
			Message:    "盤が見つかりませんでした",
		}, nil
	}
	if b.Dx() < guide.MinBoardPx || b.Dy() < guide.MinBoardPx {
		log.Info("検出した盤が小さすぎるので合わせませんでした",
			"width", b.Dx(), "height", b.Dy(), "confidence", conf)
		return FitResult{
			Confidence: conf,
			Message:    "検出した盤が小さすぎます",
		}, nil
	}
	// 位置・サイズは記録(frameGeom)ではなく今の値を読む。記録は「終了時に読めない」
	// 問題への対策で、動作中の値は正しい(geometry.go)。
	x, y := s.wins.frame.Position()
	w, h := s.wins.frame.Size()
	cur := guide.Window{X: x, Y: y, Width: w, Height: h}
	// **盤ぴったりではなく、少し外側に合わせる**(fitMarginCellRatio 参照)。
	next, moved := guide.Geometry(cur, region, guide.WithMargin(b), guide.Slop(b), scale)
	if !moved {
		return FitResult{
			Fitted:     true,
			Confidence: conf,
			Message:    fmt.Sprintf("既に合っています(信頼度 %.2f)", conf),
		}, nil
	}
	nx, ny, nw, nh := next.X, next.Y, next.Width, next.Height

	s.wins.frame.SetSize(nw, nh)
	s.wins.frame.SetPosition(nx, ny)
	// 移動・リサイズのイベントは飛ぶはずだが、保存される値がこの操作を取りこぼすと
	// 次回起動で元の位置に戻る。ここで明示的に記録しておく。
	s.wins.frameGeom.record(s.wins.frame)

	log.Info("ガイド枠を盤に合わせました",
		"confidence", conf,
		"from", fmt.Sprintf("%d,%d,%dx%d", x, y, w, h),
		"to", fmt.Sprintf("%d,%d,%dx%d", nx, ny, nw, nh))
	return FitResult{
		Fitted:     true,
		Confidence: conf,
		Message:    fmt.Sprintf("盤に合わせました(信頼度 %.2f)", conf),
	}, nil
}

// findBoard は盤を探し、**スクリーン座標・物理ピクセル**の矩形と信頼度を返す。
// 見つからなければ空の矩形を返す(error はキャプチャ自体に失敗したときだけ)。
//
// **枠の内側 → 画面全体、の順に探す。**
//
// 画面全体だけにしないのは、**小さく切り出した画像のほうが確実に当たる**ため。
// 実測(デスクトップのスクリーンショットに盤を合成して検出)では、盤だけを切り出せば
// ほぼ 1.00 で当たるものが、2560x1440 の画面全体では 12 回中 3 回見つからず、
// 1 回は「半分の周期」の誤検出になった。**枠が既に盤を囲んでいるなら、
// その中で探すほうが速くて確実。**
//
// 逆に画面全体を見ないと、枠を先に盤へ近づけておく必要があり、それができるなら
// 手で合わせるのと変わらない。両方やるのはそのため。
func (s *CaptureService) findBoard(region ikkyoku.Region) (image.Rectangle, float64, error) {
	// 1) 枠の内側。ここは枠を隠す必要が無い(ガイド枠もツールバーも領域の外)。
	if img, err := ikkyoku.Capture(region); err == nil {
		det, err := recognize.DetectRegion(img)
		b := det.Rect.Sub(img.Bounds().Min).Add(image.Pt(region.X, region.Y))
		switch {
		case err != nil:
			log.Debug("枠の内側には盤がありませんでした", "confidence", det.Confidence)
		case s.looksLikePartOfBoard(b, region, det.Confidence):
			// 縦横とも半分に縮む候補は「盤の一部」を掴んでいる可能性が高い。
			// 採らずに画面全体の探索へ回す(そちらで本来の盤が見つかることがある)。
		default:
			log.Info("枠の内側で盤を見つけました",
				"rect", b.String(), "confidence", det.Confidence)
			return b, det.Confidence, nil
		}
	} else {
		log.Warn("枠の内側を撮れませんでした", "error", err)
	}

	// 2) 画面全体(枠がいるディスプレイ 1 枚)。
	disp, err := s.frameDisplay(region)
	if err != nil {
		return image.Rectangle{}, 0, err
	}
	img, err := s.captureWithoutSelf(disp)
	if err != nil {
		return image.Rectangle{}, 0, err
	}
	det, err := recognize.DetectRegion(img)
	if err != nil {
		log.Info("画面に盤が見つかりませんでした",
			"display", disp.String(), "confidence", det.Confidence, "error", err)
		return image.Rectangle{}, det.Confidence, nil
	}
	b := det.Rect.Sub(img.Bounds().Min).Add(image.Pt(disp.X, disp.Y))
	log.Info("画面の中に盤を見つけました", "rect", b.String(), "confidence", det.Confidence)
	return b, det.Confidence, nil
}

// looksLikePartOfBoard は「枠が囲んでいる盤の一部」を掴んだ疑いがあるかを返す。
//
// 9x9 のグリッド検出には**マス 2 つぶんを 1 マスとみなす**当たり方があり、
// 格子線が 1 本おきに一致するうえ盤の内側は色が均一なので、信頼度 1.00 のまま
// 盤の 1/4 が返る(guide.MinShrinkRatio 参照)。縦横の**両方**がちょうど半分になるのが
// 特徴なので、大きさで見分ける。片辺だけ小さいのは「枠の縦横比が盤と違う」という
// 普通の状態なので弾かない。
//
// **枠の内側を探すときだけの判定。** 画面全体から探すときは、見つけた盤が
// 今の枠と無関係な場所にあるので比べる意味が無い。
func (s *CaptureService) looksLikePartOfBoard(board image.Rectangle, region ikkyoku.Region, conf float64) bool {
	// **大きさの線引きは ikkyoku/guide**（この節の理由もあちらのコメントにある）。
	// ⚠️ **ログはここに残すこと** —— 採るかどうかは幾何の話だが、
	// 「採らなかった」と知らせるのは GUI の仕事。
	if !guide.TooSmall(board, region) {
		return false
	}
	log.Info("枠の内側で見つけた盤が小さすぎるので採りませんでした",
		"board", fmt.Sprintf("%dx%d", board.Dx(), board.Dy()),
		"region", fmt.Sprintf("%dx%d", region.Width, region.Height),
		"confidence", conf)
	return true
}

// frameDisplay は枠がいるディスプレイ全体の領域を返す。
//
// **探すのは 1 枚だけ。** 全モニタをまとめて撮ると、ディスプレイごとに DPI が違う
// 環境で「見つけた盤のあるモニタ」と「枠のいるモニタ」の換算係数が食い違う
// (scale は枠の HWND から取っているため)。中継とガイド枠は同じ画面にあるのが自然なので、
// 枠のいるディスプレイに絞る。
func (s *CaptureService) frameDisplay(region ikkyoku.Region) (ikkyoku.Region, error) {
	center := image.Pt(region.X+region.Width/2, region.Y+region.Height/2)
	for _, d := range ikkyoku.ListDisplays() {
		if center.In(d.Bounds) {
			return d.Region(), nil
		}
	}
	// モニタ構成の隙間などで中心がどこにも入らないとき。撮れないよりはましなので
	// プライマリに落とす(見つからなければ「盤が見つかりません」になるだけ)。
	log.Warn("枠がどのディスプレイにも属していません。プライマリを探します", "region", region.String())
	return ikkyoku.PrimaryRegion()
}

// fitHideSettle は枠を隠してから撮るまでの待ち時間。
//
// Hide() が返った時点では画面の合成結果に反映されているとは限らない。
// 60Hz で 3 フレームぶん見ておけば、下にある中継が塗り直される時間としては十分。
// **短くしすぎると、消したはずの赤い枠が写る。**
const fitHideSettle = 50 * time.Millisecond

// captureWithoutSelf は自分のウィンドウを取り除いた画面を撮る。
//
// 枠は隠して撮り、すぐ戻す。メイン画面は隠さず、撮った画像の上で塗り潰す
// (maskWindows)。**隠すと z 順やフォーカスが動くので、消す必要があるだけの
// メイン画面にはやらない。** 枠だけ隠すのは、ガイド枠が「探す対象の上に重なる線」
// そのもので、塗り潰すと盤まで消えてしまうため。
func (s *CaptureService) captureWithoutSelf(disp ikkyoku.Region) (image.Image, error) {
	frame := s.wins.frame
	hidden := false
	if frame.IsVisible() {
		frame.Hide()
		hidden = true
		time.Sleep(fitHideSettle)
	}

	img, err := ikkyoku.Capture(disp)

	if hidden {
		frame.Show()
	}
	if err != nil {
		return nil, err
	}
	s.maskWindows(img, disp)
	return img, nil
}
