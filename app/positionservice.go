package app

import (
	"fmt"
	"sync"

	"github.com/ShinteLab/ikkyoku/log"
	"github.com/ShinteLab/ikkyoku/position"
)

// PositionService は訂正中の局面を持つ Service。
//
// **1 つだけ状態を持つ**（今直している局面）。ルートパッケージ側（`ikkyoku` /
// `position`）は状態を持たない約束なので、「今どれを直しているか」というアプリの
// 状態はここに置く。**検討セッションもいずれここに乗る**（今は 1 局面だけ）。
//
// **永続化しない**（2026-08-07 決定）。メモリ上に持つだけで、保存はエクスポートの
// 予定。1 時間で捨ててよい情報のために保存形式を先に決めない。
//
// フロントとのやり取りは**毎回 EditState を返す**形にしてある。操作のたびに
// 「盤・在庫・警告」を全部返せば、フロント側に局面の写しを持たなくて済む
// （持つと、Go 側の盤とフロントの盤がずれたときに直しようがない）。
type PositionService struct {
	mu sync.Mutex
	// pos は訂正中の局面。まだ何も読んでいなければ nil。
	pos *position.Position
	// origin は読み込んだときの盤面 SFEN。**Reset で戻す先**であり、
	// 「認識結果から変えたか」の判定にも使う。
	origin string
	// originHandsFixed は**戻す先が手合割の局面か**（2026-09-12）。
	//
	// ⚠️ **盤面 SFEN だけでは戻せない。** `origin` から作り直すと
	// `FromBoardSFEN`（＝駒台を逆算する）に戻ってしまい、**手合割から並べ始めた
	// 局面が、訂正を捨てた拍子に確定できなくなる**（落とした駒が未決に戻る）。
	originHandsFixed bool
	// nearWhite は**撮った画像が後手目線だった**か（＝手前に写っているのが後手）。
	//
	// ⚠️ **表示視点（盤の絵を裏から眺める `flip` 属性）とは別物。** あちらは
	// 見え方の好みで SFEN は 1 文字も変わらないが、こちらは**盤面そのものが
	// 上下逆に写っていた**という事実で、解析へ渡すときに 180 度回すことになる。
	//
	// ⚠️ **これを立てても訂正タブの盤は反転しない**（訂正は「見えているものを
	// 直す」作業で、学習ラベルも画素と一致していなければならない）。回すのは
	// `adoptPosition` の 1 か所だけ。
	nearWhite bool
	// confidence は**撮った盤面に対する認識器のマスごとの確信度**（行優先）。
	//
	// **中継を追うときの「どのマスなら覆してよいか」の根拠**（`followCost`）。
	// ⚠️ **nil なら認識を通っていない**（手合割・詰将棋）ので、**修復をしない**。
	// 人が並べた盤を機械が覆してよい理由は無い。
	// ⚠️ **撮った向きのまま持つこと**（盤と揃える）。解析へ渡すときに
	// **盤と一緒に回す**（`followCost`）。
	confidence *[9][9]float64
}

func NewPositionService() *PositionService {
	return &PositionService{}
}

// EditCell は 1 マスの見え方。フロントが盤に重ねる当たり判定と、
// ドラッグ中のゴースト表示に使う。
type EditCell struct {
	// Mark は SFEN のマス表記（"P" / "+p" / 空マスは ""）。
	Mark string `json:"mark"`
	// Name は日本語（"先手の歩" / "空"）。ホバーと読み上げ用。
	Name string `json:"name"`
	// Empty は空マスか。Mark == "" と同じだが、フロントで判定を間違えないよう明示する。
	Empty bool `json:"empty"`
}

// EditState は訂正の状態一式。**操作のたびにこれを丸ごと返す。**
type EditState struct {
	// Loaded は局面を読み込んでいるか（まだ何も撮っていなければ false）。
	Loaded bool `json:"loaded"`
	// BoardSFEN は盤面部分の SFEN。手番が未決でも取れる。
	BoardSFEN string `json:"boardSfen"`
	// SFEN は局面全体の SFEN。**手番が未決なら空**（決めていない手番を勝手に
	// 先手へ倒さないため。position.Position.SFEN と同じ理由）。
	SFEN string `json:"sfen"`
	// LabelSFEN は**画像のラベルとしての** SFEN。**局面が確定していなくても必ず入る。**
	//
	// ⚠️ **表示にも解析にも使わないこと。** 用途は suteme への学習データ登録だけで、
	// 手番が未決でも `b` と書き、決まっているぶんの持ち駒を必ず載せる
	// （`position.Position.LabelSFEN` の注記を読むこと）。**画面に出すのは SFEN**。
	LabelSFEN string `json:"labelSfen"`
	// LabelNotes は LabelSFEN を組み立てるために妥協した点（手番を先手にした・
	// 先後未決の持ち駒を落とした）。**送る前にユーザーへ出す**（黙って捨てない）。
	LabelNotes []string `json:"labelNotes"`
	// AnalyzeSFEN は**解析タブへ渡す**局面の SFEN。
	//
	// ⚠️ **後手目線（NearWhite）のときは SFEN と違う** —— 盤・先後・駒台・手番を
	// まとめて 180 度回したものになる。**画面に出しているのは撮った向きの SFEN**
	// なので、渡る先が違うことをユーザーに見せるためにこちらも返す。
	// **先手目線なら SFEN と同じ**（違いが出たときだけ画面に出す）。
	AnalyzeSFEN string `json:"analyzeSfen"`
	// NearWhite は**撮った画像が後手目線**か（手前に写っているのが後手）。
	//
	// ⚠️ **盤の絵は反転しない。** 反転するのは解析へ渡す局面だけで、
	// 訂正タブは撮ったとおりを描き続ける（学習ラベルは画素と一致していること）。
	NearWhite bool `json:"nearWhite"`
	// Turn は 0=不明 / 1=先手番 / 2=後手番。**対局としての先後**であって、
	// 画面の上下ではない（`NearWhite` のときは手前が後手番になる）。
	Turn      int    `json:"turn"`
	TurnLabel string `json:"turnLabel"`
	// SeenTurn は**見た目の手番**（0=不明 / 1=上向きの駒の側 / 2=逆向きの側）。
	//
	// 駒台の角の ▲/△ をどちら側で光らせるかがこれで決まる。後手目線なら
	// `Turn` とは逆になる（手前に写っているのが後手なので）。
	//
	// ⚠️ **`Turn` からフロントで組み立て直さないこと** —— 目線との掛け合わせなので、
	// 2 か所で計算すると片方だけ裏返る。⚠️ **こちらを解析や表示に使わないこと**
	// （出すべきは対局としての先後＝`Turn`）。
	SeenTurn int `json:"seenTurn"`
	// MoveNumber は手数。0 は不明。
	MoveNumber int `json:"moveNumber"`
	// Cells は 81 マス（SFEN 記述順。rank*9+file）。
	Cells []EditCell `json:"cells"`
	// Inventory は「存在するはずの駒」の在庫。**訂正 UI の駒箱はこれを並べる。**
	Inventory []position.Stock `json:"inventory"`
	// HandsFixed は**駒台を逆算しない**か（駒落ち・詰将棋。2026-09-12）。
	//
	// 既定は false で、盤に無い駒は「どちらかの駒台にあるはず」と逆算される
	// （認識の訂正はこれが拠り所）。⚠️ **落ちている駒がある局面ではその逆算が
	// 成り立たない** —— 飛車落ちの飛車はどちらの駒台にも無いので、逆算したままだと
	// **未決が永久に消えず、SFEN が組み上がらない＝解析タブへ渡せない**。
	// 立てると、行き場の無い枚数は `Stock.Unused`（この対局で使わない駒）に回る。
	HandsFixed bool `json:"handsFixed"`
	// MateProblem は**詰将棋か**（2026-09-12）。
	//
	// 立っていると、盤にも駒台にも無い駒は**全部が玉方（後手）の持駒**として
	// 駒台に出る（`Inventory` の `HandWhite` に入っている）。**属性なので
	// 盤を直すたびに追随する。** ⚠️ **`HandsFixed` とは排他。**
	// ⚠️ **手番は攻方＝先手・目線は先手固定**（`SetMateProblem` が決める）ので、
	// **画面はその 2 つを選ばせないこと**（選べると、決まっているものを
	// 選ばせることになる）。
	MateProblem bool `json:"mateProblem"`
	// Warnings は局面として成立していない点。**エラーではない**（直している最中は
	// 壊れていて当たり前）。
	Warnings []string `json:"warnings"`
	// Dirty は認識結果から変更したか。「訂正を捨てて戻す」を出すかの判断に使う。
	Dirty bool `json:"dirty"`
}

// Load は認識結果（盤面部分の SFEN）を読み込んで訂正を始める。
//
// **撮るたびに呼ぶ。** 1 回のキャプチャは他と独立している（設計原則1）ので、
// 前の訂正内容を引き継がない。壊れた盤面でも読めた分で始める（設計原則3）。
func (s *PositionService) Load(boardSFEN string, cellConfidence []float64) (EditState, error) {
	return s.loadBoard(boardSFEN, confGrid(cellConfidence))
}

// confGrid は 81 個の確信度を表にする（**行優先**。数が合わなければ nil）。
//
// ⚠️ **足りない配列を 0 で埋めないこと** —— 確信度 0 は「まったく自信が無い」
// という**意味のある値**なので、「知らない」と区別が付かなくなる。
func confGrid(v []float64) *[9][9]float64 {
	if len(v) != 81 {
		return nil
	}
	var g [9][9]float64
	for i, x := range v {
		g[i/9][i%9] = x
	}
	return &g
}

func (s *PositionService) loadBoard(boardSFEN string, conf *[9][9]float64) (EditState, error) {
	p, err := position.FromBoardSFEN(boardSFEN)

	s.mu.Lock()
	s.pos = p
	s.origin = p.BoardSFEN()
	// **撮った局面は駒台を逆算する**（訂正の拠り所）。戻す先もそちら。
	s.originHandsFixed = false
	s.confidence = conf
	st := s.state()
	s.mu.Unlock()

	if err != nil {
		// 読めなかったところがあっても訂正は始められる。理由だけ返す。
		log.Warn("盤面を完全には読めませんでした", "error", err)
		return st, fmt.Errorf("盤面を完全には読めませんでした: %w", err)
	}
	return st, nil
}

// LoadHandicap は**手合割の初期局面から訂正を始める**（入力タブの「駒を落として
// 並べる」。2026-09-12）。
//
// **独自ハンデのための入口。** 手合割のテンプレートは `core/kifu` が持っているが、
// 「飛車と左香を落として歩を 2 枚抜く」のような取り決めは表に無い。**近い手合割から
// 始めて訂正タブで足し引きする**のが、表を増やさずに任意のハンデを作る道になる。
//
// ⚠️ **画像は無い。** 撮った 1 枚から始める `Load` と違い、こちらには元画像が無いので、
// **呼び出し側（フロント）は撮った画像・認識の情報・学習への送信を片付けること**
// （前のキャプチャのものが残っていると、**別の画像のラベルとして訂正結果を送れて
// しまう**）。⚠️ **学習に送れるのは撮った局面だけ**という線引きは崩さない。
//
// ⚠️ **目線は先手目線に戻す。** 画像が無いのだから「撮った画像がどちら目線か」は
// 意味を持たない（残っていると、解析へ渡すときに勝手に 180 度回る）。
//
// ⚠️ **`HandsFixed` が立った状態で始まる**（`position.NewPosition`）。駒落ちは
// 盤にも駒台にも無い駒がある局面なので、逆算を始めると未決が消えず確定できない。
// 画面の「手合割」のチェックが**最初から入っている**状態になる。
func (s *PositionService) LoadHandicap(handicap string) (EditState, error) {
	p, err := position.NewPosition(handicap)
	if err != nil {
		return s.State(), err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pos = p
	// **戻る先はこの初期局面**（「訂正を捨てて戻す」で並べ直せる）。
	s.origin = p.BoardSFEN()
	s.originHandsFixed = true
	s.nearWhite = false
	// ⚠️ **認識を通っていないので確信度は無い**（人が並べた盤を機械が覆さない）。
	s.confidence = nil
	return s.state(), nil
}

// LoadEmpty は**空の盤から並べ始める**（入力タブの「詰将棋を並べる」。2026-09-12）。
//
// **詰将棋のための入口。** 詰将棋には初期局面が無い（人が並べる）ので、手合割の
// ような表は引けない。⚠️ **「新しく対局を始める」に空の盤の option を足さないこと**
// —— あちらは**確定した局面を解析タブへ渡す**経路で、行き先が違う。
//
// ⚠️ **最初から詰将棋として始まる**（`MateProblem`）。手番は攻方＝先手、
// 余った駒は全部 玉方（後手）の持駒になるので、**駒を置いた時点で確定している**
// （駒台を埋める作業が要らない）。**手合割とは逆**なので混同しないこと。
//
// ⚠️ **画像は無い**（`LoadHandicap` と同じ。撮った画像・認識の情報・学習への
// 送信は呼び出し側が片付けること）。
func (s *PositionService) LoadEmpty() (EditState, error) {
	p := position.New(nil)
	p.MateProblem = true
	p.Turn = position.TurnBlack
	p.MoveNumber = 1
	// **空の盤なので、40 枚から玉 2 枚を除いた全部が玉方の持駒になる。**
	// ⚠️ **攻方の駒は「足りない駒」の枠から置く**（あちらは在庫を見ないので、
	// 玉方の駒台が全部持っていても置ける）。
	p.NormalizeMateHands()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pos = p
	s.origin = p.BoardSFEN()
	s.originHandsFixed = false
	s.nearWhite = false
	// ⚠️ **認識を通っていないので確信度は無い**（人が並べた盤を機械が覆さない）。
	s.confidence = nil
	return s.state(), nil
}

// SetMateProblem は**この盤を詰将棋として扱う**か（2026-09-12）。
//
// **操作ではなく属性。** 立てると、盤にも駒台にも無い駒は**全部が玉方（後手）の
// 持駒**になり、**盤を直すたびに自動で追随する**（1 枚外せばその駒がそのまま
// 玉方の持駒になる）。まとめて寄せる操作は要らない（一度入れて外した）。
//
// **撮った局面にも効く。** 詰将棋の画面を撮って、ここを立てれば余った駒は
// そのまま玉方へ回る（駒台を 1 枚ずつ埋める作業が要らない）。
//
// 立てるときに**一緒に決めるもの**（どちらも詰将棋では定数なので、人に選ばせない）:
//
//   - ⚠️ **手番は攻方＝先手**（詰将棋は攻方から指す）
//   - ⚠️ **目線は先手固定**（`nearWhite` を落とす）。詰将棋の図は攻方が手前と
//     決まっているので、撮った画像の向きを疑う必要が無い
//
// ⚠️ **手合割（`HandsFixed`）とは排他。** どちらも「盤にも駒台にも無い駒」の
// 解釈で、手合割は**存在しない**、詰将棋は**玉方が持っている**という別の答え。
// **片方を立てたらもう片方を外す**（両方立った状態を作らない）。
//
// ⚠️ **外しても手番と目線は戻さない**（人が決めた値として残す。戻すと、
// 押し間違えて外しただけで手番が消える）。
func (s *PositionService) SetMateProblem(mate bool) (EditState, error) {
	st, err := s.edit(func(p *position.Position) error {
		p.MateProblem = mate
		if mate {
			p.HandsFixed = false
			p.Turn = position.TurnBlack
		}
		return nil
		// ⚠️ **駒台を載せ直すのは `edit` のほう**（1 操作ごとに走る）。
		// ここでだけ寄せると、**チェックした瞬間だけ正しくて、あとは追随しない**。
	})
	if err != nil || !mate {
		return st, err
	}
	s.mu.Lock()
	s.nearWhite = false
	st = s.state()
	s.mu.Unlock()
	return st, nil
}

// State は今の状態を返す（何も変えない）。フロントの初期表示用。
func (s *PositionService) State() EditState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state()
}

// Reset は訂正を捨てて読み込んだときの局面に戻す。
//
// ⚠️ **手合割から並べ始めた局面は手合割のまま戻す**（2026-09-12）。`Load` は
// 盤面 SFEN から駒台を逆算する（＝撮った局面の読み方）ので、そのままだと
// **落とした駒が未決に戻り、捨てた拍子に確定できなくなる**。
func (s *PositionService) Reset() (EditState, error) {
	s.mu.Lock()
	origin, fixed, conf := s.origin, s.originHandsFixed, s.confidence
	s.mu.Unlock()
	if origin == "" {
		return s.State(), nil
	}
	// ⚠️ **確信度は持ち越すこと。** 戻す先は同じ認識結果なので、捨てると
	// 「訂正を捨てて戻す」を押しただけで**修復が効かなくなる**。
	st, err := s.loadBoard(origin, conf)
	if err != nil || !fixed {
		return st, err
	}
	// ⚠️ **戻す先の印も立て直すこと。** `Load` が「撮った局面」として false に
	// するので、ここで戻さないと**2 回目の Reset で手合割が外れる。**
	s.mu.Lock()
	s.originHandsFixed = true
	s.mu.Unlock()
	return s.SetHandsFixed(true)
}

// Move は盤の中で駒を動かす（盤 → 盤のドラッグ＆ドロップ）。
//
// ⚠️ **移動先に駒があれば入れ替える**（取るのではない。`position.Move` を読むこと）。
func (s *PositionService) Move(fromRank, fromFile, toRank, toFile int) (EditState, error) {
	return s.edit(func(p *position.Position) error {
		return p.Move(fromRank, fromFile, toRank, toFile)
	})
}

// Place は駒箱から盤へ置く（駒箱 → 盤のドラッグ＆ドロップ）。
//
// **在庫が尽きていても置ける。** 余計な駒を外す前に正しい駒を置けないと詰むため
// （position.Place の注記を参照）。上限超過は Warnings と在庫の負の値に出る。
func (s *PositionService) Place(rank, file, piece int, black, promoted bool) (EditState, error) {
	return s.edit(func(p *position.Position) error {
		return p.Place(rank, file, piece, black, promoted)
	})
}

// Remove はマスを空にする（盤 → 駒箱のドラッグ＆ドロップ）。
// **認識が作った余計な駒を外す操作。** 先後は決めない（駒台の未割り当てに入る）。
func (s *PositionService) Remove(rank, file int) (EditState, error) {
	return s.edit(func(p *position.Position) error { return p.Remove(rank, file) })
}

// ToHand は盤の駒を駒台へ移す（盤 → 先手/後手の駒台のドラッグ＆ドロップ）。
// **外すのと先後を決めるのが 1 操作。**
func (s *PositionService) ToHand(rank, file int, black bool) (EditState, error) {
	return s.edit(func(p *position.Position) error { return p.ToHand(rank, file, black) })
}

// FromHand は駒台の駒を盤へ置く（駒台 → 盤のドラッグ＆ドロップ）。
// **その側の駒台に無ければエラー**（見本から置く Place とはそこが違う）。
func (s *PositionService) FromHand(rank, file, piece int, black bool) (EditState, error) {
	return s.edit(func(p *position.Position) error { return p.FromHand(rank, file, piece, black) })
}

// CycleCell は 1 マスを「先手不成 → 先手成 → 後手不成 → 後手成 → …」と回す。
// **訂正 UI の右クリックはこれ 1 つ**（先後と成/不成を左右のクリックで分けない）。
func (s *PositionService) CycleCell(rank, file int) (EditState, error) {
	return s.edit(func(p *position.Position) error { return p.CycleCell(rank, file) })
}

// TogglePromoted は成/不成を切り替える。
func (s *PositionService) TogglePromoted(rank, file int) (EditState, error) {
	return s.edit(func(p *position.Position) error { return p.TogglePromoted(rank, file) })
}

// FlipSide は駒の先後を入れ替える。
func (s *PositionService) FlipSide(rank, file int) (EditState, error) {
	return s.edit(func(p *position.Position) error { return p.FlipSide(rank, file) })
}

// SetTurn は手番を決める（0=不明 / 1=先手番 / 2=後手番）。
//
// **盤面からは決まらないので、ここが人間の入口。**
//
// ⚠️ **受け取るのは「対局としての先後」**（画面の上下ではない）。撮った画像が
// 後手目線なら、手前に写っている側が後手なので、**見た目の手番とは逆になる**。
// `Position` は撮った向きのまま（＝見た目）で揃えてあるので、ここで翻訳する。
// **翻訳はこのファイルの中だけ**にすること（散らすと必ずどこかで裏返る）。
func (s *PositionService) SetTurn(turn int) (EditState, error) {
	return s.edit(func(p *position.Position) error {
		switch turn {
		case int(position.TurnUnknown), int(position.TurnBlack), int(position.TurnWhite):
			p.Turn = s.asSeenTurn(position.Turn(turn))
			return nil
		}
		return fmt.Errorf("手番の値が不正です: %d", turn)
	})
}

// SetViewpoint は**撮った画像がどちら目線か**を決める（true なら手前が後手）。
//
// ⚠️ **盤は 1 マスも動かさない。** 直す対象は撮った画像そのものなので、
// ここで盤面や先後を書き換えると、学習ラベルが画素と一致しなくなる。
// 効くのは「解析へ渡すときに回すかどうか」と、**手番の見え方**だけ。
//
// ⚠️ **手番は変えない**（目線と手番は独立した 2 つの事実）。`Position` が持って
// いるのは見た目の手番なので、目線が変わったら**そちらを入れ替えて**、
// 画面に出る「対局としての手番」を保つ。
func (s *PositionService) SetViewpoint(nearWhite bool) (EditState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nearWhite == nearWhite {
		return s.state(), nil
	}
	s.nearWhite = nearWhite
	if s.pos != nil {
		s.pos.Turn = flipTurn(s.pos.Turn)
	}
	return s.state(), nil
}

// asSeenTurn は「対局としての手番」を**見た目の手番**に直す（後手目線なら入れ替え）。
// gameTurn はその逆。⚠️ **どちらも入れ替えるだけなので中身は同じ**だが、
// 呼ぶ側でどちら向きの変換かが読めるように名前を分けてある。
func (s *PositionService) asSeenTurn(t position.Turn) position.Turn {
	if s.nearWhite {
		return flipTurn(t)
	}
	return t
}

func (s *PositionService) gameTurn(t position.Turn) position.Turn { return s.asSeenTurn(t) }

// flipTurn は手番を入れ替える。**未決は未決のまま**（決めていないことを決めない）。
func flipTurn(t position.Turn) position.Turn {
	switch t {
	case position.TurnBlack:
		return position.TurnWhite
	case position.TurnWhite:
		return position.TurnBlack
	}
	return position.TurnUnknown
}

// SetMoveNumber は手数を決める（0 は不明）。
func (s *PositionService) SetMoveNumber(n int) (EditState, error) {
	return s.edit(func(p *position.Position) error {
		if n < 0 {
			return fmt.Errorf("手数が負です: %d", n)
		}
		p.MoveNumber = n
		return nil
	})
}

// SetHandsFixed は**駒台の逆算をやめる / 再開する**（駒落ち・詰将棋。2026-09-12）。
//
// **「盤にも駒台にも無い駒」をどう解釈するかの 1 つの選択。**
//
//	false（既定）… どちらかの駒台にあるはず（＝先後が未決。決まるまで確定しない）
//	true          … この対局には**存在しない**（＝落とした駒・詰将棋で使わない駒）
//
// ⚠️ **これは訂正の操作ではない**（盤も駒台も 1 枚も動かない）。動くのは
// 「足りない駒」の解釈だけで、**既に駒台へ割り振ったぶんはそのまま残る**
// （人が決めたものを、解釈を変えた拍子に捨てない）。
//
// ⚠️ **戻せること。** false に戻せば逆算が復活し、使わない駒だったぶんが
// また未決として出てくる（押し間違えても失うものが無い）。
//
// ⚠️ **エラーにする側ではない。** 立てなくても訂正は続けられる（確定できない
// だけ）し、立てたまま平手を直しても警告が減るだけで止まらない（設計原則3）。
func (s *PositionService) SetHandsFixed(fixed bool) (EditState, error) {
	return s.edit(func(p *position.Position) error {
		p.HandsFixed = fixed
		if fixed {
			// ⚠️ **詰将棋とは排他**（同じ問いへの別の答え）。
			p.MateProblem = false
		}
		return nil
	})
}

// SetHand は駒台のうち片側の枚数を n 枚にする。
// **駒台の先後も盤面からは決まらない**ので、これも人間の入口。
// ドラッグ以外の入口（未割り当てを一括で寄せる操作）として残してある。
//
// **既に足りている駒でも載せられる**（Place が在庫を見ないのと同じ。
// 止めると駒台から先後を決める操作が詰む）。多すぎるぶんは Warnings に出る。
func (s *PositionService) SetHand(piece int, black bool, n int) (EditState, error) {
	return s.edit(func(p *position.Position) error { return p.SetHand(piece, black, n) })
}

// adoptPosition は**対局としての正しい向きに直した写し**を返す（StudyService 用）。
// 2 つめの戻り値は回したか（＝撮った画像が後手目線だったか）。まだ何も読んで
// いなければ nil。
//
// ⚠️ **写しであることが要点。** 解析タブは確定した局面を根に持つので、採ったあとに
// こちらを訂正しても向こうが動いてはいけない（動くと、出ている評価値がどの局面の
// 値なのか分からなくなる）。
//
// **確定しているかの判定はここでしない。** それは受け取る側（StudyService.Adopt）の
// 話で、こちらは未決のままの局面も普通に持ち続ける（訂正の途中は未決で当たり前）。
//
// ⚠️ **回すのはここだけ。** 撮った局面も、訂正中の盤も、suteme へ送るラベルも
// 画像の向きのまま置いておく（`_docs/design-position.md` の「視点」）。
func (s *PositionService) adoptPosition() (*position.Position, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pos == nil {
		return nil, false
	}
	if !s.nearWhite {
		return s.pos.Clone(), false
	}
	return s.pos.Rotate180(), true
}

// 費用表の定数。⚠️ **どちらも実測で決めた数ではない**（`position.DefaultTolerance`
// と一緒に、実際の中継で当たり具合を見て調整すること）。
const (
	// humanEditCost は**人が直したマス**の費用。予算では絶対に届かない値にする。
	//
	// ⚠️ **人が決めたものを機械が覆してよい理由が無い。** 1 マス直したのに
	// 修復で戻されると、**直しても直しても戻る**という一番たちの悪い壊れ方になる。
	humanEditCost = 1000
	// minCellCost は確信度がいくら低くても払う下限。
	//
	// ⚠️ **0 のマスを作らないこと。** 認識器が推論しなかったマス（空マスなど）は
	// 確信度 0 で返るので、そのまま使うと**いくらでもただで覆せる** ——
	// **予算という歯止めが効かなくなる。**
	minCellCost = 0.15
)

// followCost は「認識結果を覆すのにかかる費用」の表を返す（`position.Connect` 用）。
//
// 2 つ目の戻り値は**修復してよいか**。⚠️ **認識を通っていない盤（手合割・詰将棋）
// では false** —— 人が並べたものを機械が覆す理由が無い。
//
// ⚠️ **`adoptPosition` と同じ向きに回して返すこと。** 盤だけ回して費用を回さないと、
// **別のマスの確信度で判断する**ことになる（画面からは気づけない）。
// followRotated は**撮った画像を解析の向きへ 180 度回して読むか**（後手目線。`followFrame` と同じ判断）。
func (s *PositionService) followRotated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nearWhite
}

func (s *PositionService) followCost() (*position.CellCost, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pos == nil || s.confidence == nil {
		return nil, false
	}
	// **人が直したマスを見つけるために、読み込んだときの盤と突き合わせる。**
	origin, err := position.FromSFEN(s.origin)
	if err != nil {
		origin = nil
	}
	c := &position.CellCost{}
	for r := 0; r < 9; r++ {
		for f := 0; f < 9; f++ {
			v := s.confidence[r][f]
			if v < minCellCost {
				v = minCellCost
			}
			if origin != nil && cellEdited(s.pos.Board, origin, r, f) {
				v = humanEditCost
			}
			c[r][f] = v
		}
	}
	if s.nearWhite {
		c = c.Rotate180()
	}
	return c, true
}

// followFrame は**撮った 1 枚から追従用の盤と費用表を作る**（2026-09-15）。
//
// ⚠️ **訂正タブの状態を一切変えないこと。** 以前は追従の 1 周ごとに `Load` を
// 呼んでいたので、**人が訂正タブで作業していると 1 秒ごとに中継の盤で
// 上書きされた**（実機で踏んだ —— 学習データを登録しようとして消えた）。
// **訂正タブは人の作業場であって、追従の一時バッファではない。**
//
// ⚠️ **目線（`nearWhite`）だけは見る。** あれは**人が決めた設定**であって
// 局面ではない —— 撮った画像が後手目線なら、追従でも同じだけ回す必要がある。
//
// ⚠️ **`humanEditCost` は出てこない**（`followCost` との違い）。ここには
// **人が直したマスという概念が無い**（誰も触っていない 1 枚なので）。
// 人が直した盤で繋ぐのは訂正タブの「本譜に繋ぐ」＝ `followCost` の側。
//
// hidden は**手や頭が被って見えないマス**（suteme の `CellDebug.Hidden`。81 個・行優先。2026-10-06）。
// ⚠️ **費用表とは別に返すこと** —— 見えないマスを費用 0 にして表に混ぜると、
// `minCellCost` の約束（ただで覆せるマスを作らない）と区別が付かなくなる。
// ⚠️ **数が合わなければ nil に倒す**（全部見えていることにする。今までどおり）。
func (s *PositionService) followFrame(boardSFEN string, conf []float64, hidden []bool) (*position.Board, *position.CellCost, *position.CellMask, bool, error) {
	b, err := position.FromSFEN(boardSFEN)
	if err != nil {
		return nil, nil, nil, false, err
	}
	grid := confGrid(conf)
	unseen := hiddenMask(hidden)

	s.mu.Lock()
	rotate := s.nearWhite
	s.mu.Unlock()

	var cost *position.CellCost
	if grid != nil {
		c := &position.CellCost{}
		for r := 0; r < 9; r++ {
			for f := 0; f < 9; f++ {
				// ⚠️ **0 のマスを作らないこと**（`minCellCost` と同じ理由） ——
				// 確信度 0 のマスをただで覆せると、予算という歯止めが効かなくなる。
				v := grid[r][f]
				if v < minCellCost {
					v = minCellCost
				}
				c[r][f] = v
			}
		}
		cost = c
	}
	if rotate {
		b = b.Rotate180()
		if cost != nil {
			cost = cost.Rotate180()
		}
		// ⚠️ **見えないマスも盤と一緒に回すこと**（費用表と同じ。忘れると別のマスを捨てる）。
		unseen = unseen.Rotate180()
	}
	return b, cost, unseen, rotate, nil
}

// hiddenMask は 81 個の「見えない」を表にする（**行優先**。数が合わない・1 つも立っていなければ nil）。
//
// ⚠️ **1 つも立っていなければ nil を返すこと** —— nil は「全部見えている」で、
// 今までの振る舞いと 1 ビットも変わらない（`position` 側も nil を一番安く通す）。
func hiddenMask(v []bool) *position.CellMask {
	if len(v) != 81 {
		return nil
	}
	m := &position.CellMask{}
	found := false
	for i, h := range v {
		if h {
			m[i/9][i%9], found = true, true
		}
	}
	if !found {
		return nil
	}
	return m
}

// cellEdited はそのマスが読み込んだときから変わっているか（＝人が直したか）。
func cellEdited(cur, origin *position.Board, rank, file int) bool {
	a, err1 := cur.At(rank, file)
	b, err2 := origin.At(rank, file)
	if err1 != nil || err2 != nil {
		return false
	}
	return a != b
}

// edit は 1 操作を適用して新しい状態を返す共通処理。
// **操作が失敗しても状態は返す**（フロントが画面を更新できないと、何が起きたか
// 分からないまま古い盤が残る）。
func (s *PositionService) edit(fn func(*position.Position) error) (EditState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pos == nil {
		return s.state(), fmt.Errorf("まだ局面がありません")
	}
	err := fn(s.pos)
	// ⚠️ **詰将棋は 1 操作ごとに駒台を載せ直す**（2026-09-12）。これが「操作では
	// なく属性」の実装そのもので、**盤から 1 枚外せばその駒がそのまま玉方の
	// 持駒になる**（攻方の駒を盤に置けば、そのぶん玉方の持駒が減る）。
	// ⚠️ **失敗したときも呼ぶこと** —— 約束（玉方 = 合計 − 攻方）は局面の
	// 不変条件で、操作が通ったかとは関係が無い。
	s.pos.NormalizeMateHands()
	return s.state(), err
}

// state はロックを取った状態で呼ぶこと。
func (s *PositionService) state() EditState {
	if s.pos == nil {
		return EditState{
			Cells: []EditCell{}, Inventory: []position.Stock{},
			Warnings: []string{}, LabelNotes: []string{},
			NearWhite: s.nearWhite,
		}
	}
	cells := make([]EditCell, 0, 81)
	for rank := 0; rank < 9; rank++ {
		for file := 0; file < 9; file++ {
			c, err := s.pos.Board.At(rank, file)
			if err != nil {
				cells = append(cells, EditCell{Name: "空", Empty: true})
				continue
			}
			cells = append(cells, EditCell{Mark: c.Mark(), Name: c.Name(), Empty: c.IsEmpty()})
		}
	}
	full, _ := s.pos.SFEN() // 手番が未決なら空のまま返す(エラーは状態そのもの)
	// 解析へ渡すのは**対局としての向きに直した**局面（後手目線なら 180 度回す）。
	// 先手目線なら full と同じ文字列になる。
	analyzeSFEN := full
	if s.nearWhite && full != "" {
		analyzeSFEN, _ = s.pos.Rotate180().SFEN()
	}
	turn := s.gameTurn(s.pos.Turn)
	label, labelNotes := s.pos.LabelSFEN()
	if labelNotes == nil {
		labelNotes = []string{}
	}
	warnings := s.pos.Warnings()
	if warnings == nil {
		warnings = []string{}
	}
	board := s.pos.BoardSFEN()
	return EditState{
		Loaded:      true,
		BoardSFEN:   board,
		SFEN:        full,
		LabelSFEN:   label,
		LabelNotes:  labelNotes,
		AnalyzeSFEN: analyzeSFEN,
		NearWhite:   s.nearWhite,
		Turn:        int(turn),
		TurnLabel:   turn.String(),
		// 見た目の手番（Position が持っているのはこちら）。
		SeenTurn:    int(s.pos.Turn),
		MoveNumber:  s.pos.MoveNumber,
		Cells:       cells,
		Inventory:   s.pos.Inventory(),
		HandsFixed:  s.pos.HandsFixed,
		MateProblem: s.pos.MateProblem,
		Warnings:    warnings,
		Dirty:       board != s.origin,
	}
}
