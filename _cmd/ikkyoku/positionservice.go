package main

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
	// Turn は 0=不明 / 1=先手番 / 2=後手番。
	Turn      int    `json:"turn"`
	TurnLabel string `json:"turnLabel"`
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
func (s *PositionService) SetTurn(turn int) (EditState, error) {
	return s.edit(func(p *position.Position) error {
		switch turn {
		case int(position.TurnUnknown), int(position.TurnBlack), int(position.TurnWhite):
			p.Turn = position.Turn(turn)
			return nil
		}
		return fmt.Errorf("手番の値が不正です: %d", turn)
	})
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

// positionSFEN は今の局面の**確定した** SFEN を返す（AnalyzeService 用）。
//
// ⚠️ **手番や駒台の先後が未決ならエラー。** エンジンに渡せるのは確定した局面だけで、
// ここで先手に倒すと「決めていない手番でエンジンが読んだ」ことになる（設計原則5）。
// 盤を見るだけ・訂正するだけなら未決のままでよいので、止めるのはこの経路だけ。
//
// **局面はフロントを経由させない。** 解析するのは常に「今ここが持っている局面」で、
// SFEN を渡してもらう形にすると、訂正した直後に古い局面を解析する経路ができる。
func (s *PositionService) positionSFEN() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pos == nil {
		return "", fmt.Errorf("まだ局面がありません")
	}
	return s.pos.SFEN()
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
		Loaded:     true,
		BoardSFEN:  board,
		SFEN:       full,
		LabelSFEN:  label,
		LabelNotes: labelNotes,
		Turn:       int(s.pos.Turn),
		TurnLabel:  s.pos.Turn.String(),
		MoveNumber: s.pos.MoveNumber,
		Cells:      cells,
		Inventory:  s.pos.Inventory(),
		Warnings:   warnings,
		Dirty:      board != s.origin,
	}
}
