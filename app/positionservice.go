package app

import (
	"fmt"
	"log/slog"
	"sync"

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
	logger *slog.Logger

	mu sync.Mutex
	// pos は訂正中の局面。まだ何も読んでいなければ nil。
	pos *position.Position
	// origin は読み込んだときの盤面 SFEN。**Reset で戻す先**であり、
	// 「認識結果から変えたか」の判定にも使う。
	origin string
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
}

func NewPositionService(logger *slog.Logger) *PositionService {
	return &PositionService{logger: logger}
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
func (s *PositionService) Load(boardSFEN string) (EditState, error) {
	p, err := position.FromBoardSFEN(boardSFEN)

	s.mu.Lock()
	s.pos = p
	s.origin = p.BoardSFEN()
	st := s.state()
	s.mu.Unlock()

	if err != nil {
		// 読めなかったところがあっても訂正は始められる。理由だけ返す。
		s.logger.Warn("盤面を完全には読めませんでした", "error", err)
		return st, fmt.Errorf("盤面を完全には読めませんでした: %w", err)
	}
	return st, nil
}

// State は今の状態を返す（何も変えない）。フロントの初期表示用。
func (s *PositionService) State() EditState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state()
}

// Reset は訂正を捨てて認識結果に戻す。
func (s *PositionService) Reset() (EditState, error) {
	s.mu.Lock()
	origin := s.origin
	s.mu.Unlock()
	if origin == "" {
		return s.State(), nil
	}
	return s.Load(origin)
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
// 画像の向きのまま置いておく（CLAUDE.md「反転するのはエンジンへ渡す境界だけ」）。
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
		SeenTurn:   int(s.pos.Turn),
		MoveNumber: s.pos.MoveNumber,
		Cells:      cells,
		Inventory:  s.pos.Inventory(),
		Warnings:   warnings,
		Dirty:      board != s.origin,
	}
}
