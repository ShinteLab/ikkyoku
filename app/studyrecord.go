package app

// 検討セッションの控え（2026-09-16。Step 1）。
//
// **開発中は再起動が多く、そのたびに積み上げた評価値が消えていた。**
// 中継を追いながら 100 手ぶん解析しても、落とせば全部やり直し。
//
// ⚠️ **これは棋譜ではない。** 棋譜として配る形（KIF）は `core/kifu` が決めるもので、
// **まだ木を持っていない**（ワークスペースの `TODO.md` 1）。ここが持つのは
// **ikkyoku が自分で読み直すためだけの控え**。⚠️ **交換形式にしないこと。**
//
// ⚠️ **棚（`kicho`）とは別の話。** 棚の主キーは `(source, source_id)` なので、
// **中継を撮っている最中は行が作れない**（まだ「棋譜」になっていない）。
// だから解析は**棋譜とは独立した記録**にしてあり、`GameID` は**空でよい**。

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/ShinteLab/ikkyoku/position"
)

// studyRecordVersion は控えの形の版。
//
// ⚠️ **読めない版は黙って捨てること**（復元しないだけで起動は通る。設計原則3）。
// 控えは「あると嬉しいもの」で、**無いと動かないものにしない。**
const studyRecordVersion = 1

// StudyEvalSeries はエンジン 1 つぶんの点（**木の全部**）。
//
// ⚠️ **`EvalSeries` と似ているが別物。** あちらは**今の経路の点だけ**を折れ線に
// する描画用で、こちらは**枝の点も残らず**持つ。経路で絞って控えると、
// **開き直したときに枝の評価値だけ消えている。**
//
// ⚠️ **エンジンの素性をここに写さないこと**（2026-09-16 に一度入れて外した）。
// exe のパスも名乗った名前も option も**設定（`Config.Engines`）にある**ので、
// **`EngineID` で引けば済む** —— 写すと**同じことが 2 か所に載り、片方だけ古くなる。**
// それが成り立つ条件が **ID を発行して使い回さないこと**で、
// `ikkyoku.NextEngineID` がそうしてある。
type StudyEvalSeries struct {
	// EngineID は設定の登録 ID（`EngineEntry.ID`）。**素性はここから引く。**
	EngineID string `json:"engineId"`
	// Label は凡例に出ていた名前。
	//
	// ⚠️ **これだけは写す。** 登録を消したあとでも**折れ線に名前が要る**
	// （生の id が凡例に並ぶと、どの線が何なのか読めない）。設定から引けるうちは
	// そちらが勝つので、これは**引けなくなったときの控え**。
	Label  string           `json:"label,omitempty"`
	Points []StudyEvalPoint `json:"points"`
}

// StudyEvalPoint は控える 1 点。
//
// ⚠️ **手数も棋譜手数も指し手の表記も持たない**（2026-09-16 に落とした）。
// **どれも同じ控えの中の木から引ける** —— `ID` が節点を指しているので、
// 手数は節点の手数、指し手は節点の表記そのもの。写すと**同じことが 2 か所に
// 載って食い違いうる**（木は「７六歩」、点は「同歩」というような）。
//
// ⚠️ **残っている 5 つは木から引けないもの**（エンジンが出した答え）。
// `WinRate` と `Label` は**計算し直さない** —— **点は「そのとき何と出たか」の
// 記録**で、あとでポナンザ定数や書式を変えても動かさない。
type StudyEvalPoint struct {
	// ID は手順ツリーの節点（**手数も指し手もここから引く**）。
	ID      int     `json:"id"`
	CP      int     `json:"cp,omitempty"`
	Mate    int     `json:"mate,omitempty"`
	WinRate float64 `json:"winRate"`
	Label   string  `json:"label,omitempty"`
	Depth   int     `json:"depth,omitempty"`
}

// StudyRecord は検討セッション 1 つぶんの控え。
type StudyRecord struct {
	Version int `json:"version"`
	// ID はこの検討の id（**自前で振る**。棚の id とは無関係）。
	//
	// ⚠️ **同一性を棋譜から借りないこと。** 棚の主キーは `(source, source_id)` で、
	// **中継を撮っている最中は決まらない** —— そこに合わせると、
	// 「棋譜になる前は残せない」という行き止まりに入る。
	ID string `json:"id"`
	// GameID は棚（`kicho`）の棋譜 id。**空でよい**（Step 2 で結ぶ）。
	GameID string `json:"gameId,omitempty"`
	// SavedAt は控えた時刻（Unix 秒）。**復元するのは一番新しいもの。**
	SavedAt int64 `json:"savedAt"`
	// Study は木そのもの（節点の id ごと）。
	Study position.StudySnapshot `json:"study"`
	// Game は対局の素性（対局者・棋戦）。
	//
	// ⚠️ **指し手は落とすこと**（`Moves` は空で控える）。**木のほうが本物**で、
	// 両方持つと**どちらが本当か分からなくなる**うえ、同じ手順を 2 回持つ。
	// 読んでいるのはヘッダ（対局者・手合割）だけ。
	Game position.Game `json:"game"`
	// SourceURL は棋譜の取得元（**URL から読んだときだけ**）。
	SourceURL string `json:"sourceUrl,omitempty"`
	// Evals は評価値の点（木の全部）。**どのエンジンだったかは `EngineID` で引く。**
	Evals []StudyEvalSeries `json:"evals"`
}

// newSessionID は検討の id を作る。
//
// **日時を頭に置く**のは、控えのファイル名がそのまま並ぶようにするため
// （復元は「一番新しいもの」で、開発中はディレクトリを直接覗く）。
//
// ⚠️ **後ろは時刻から作らないこと**（2026-09-16 にテストが落ちて気づいた）。
// ナノ秒を刻んでも、**続けて根を入れ替えれば同じ値になる**（Windows の時計は
// そこまで細かくない）。同じ id になると**前の検討の控えがそのまま上書きされる**
// ので、ここは乱数で分ける。⚠️ **乱数が取れなくても id を返すこと**
// （控えが作れないより、まず動くほうが大事。設計原則3）。
func newSessionID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s-%09d", time.Now().Format("20060102-150405"), time.Now().Nanosecond())
	}
	return fmt.Sprintf("%s-%s", time.Now().Format("20060102-150405"), hex.EncodeToString(b[:]))
}

// newSessionLocked は**別の対局が始まったこと**にする（根を入れ替えたとき）。
//
// ⚠️ **`evals.reset()` と必ず一緒に呼ぶこと。** 評価値を捨てるのは
// 「別の対局の話になった」ときだけなので、**片方だけ動かすと控えが混ざる**
// （前の対局の id に新しい木が書き戻る）。
func (s *StudyService) newSessionLocked() {
	s.evals.reset()
	s.session = newSessionID()
}

// sessionRecord は今の検討を控えにする（**まだ採っていなければ false**）。
//
// ⚠️ **公開しない。** 控えの出し入れはフロントの操作ではない。
func (s *StudyService) sessionRecord() (StudyRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return StudyRecord{}, false
	}
	snap, err := s.study.Snapshot()
	if err != nil {
		// 根が組み上がらない検討は存在しないはず（`Adopt` が断る）。
		s.logger.Warn("検討を控えられませんでした", "error", err)
		return StudyRecord{}, false
	}
	if s.session == "" {
		s.session = newSessionID()
	}
	game := s.game
	// ⚠️ **指し手は落とす**（木が本物。両方持つと必ず割れる）。
	game.Moves = nil
	return StudyRecord{
		Version:   studyRecordVersion,
		ID:        s.session,
		SavedAt:   time.Now().Unix(),
		Study:     snap,
		Game:      game,
		SourceURL: s.sourceURL,
		Evals:     s.evals.all(),
	}, true
}

// restoreSession は控えから検討を戻す（**起動時に 1 回だけ**）。
//
// ⚠️ **既に検討が始まっていたら何もしない。** 起動直後に呼ぶ前提で、
// **人が始めた検討を控えで上書きしない**（それは黙って作業を捨てるのと同じ）。
func (s *StudyService) restoreSession(rec StudyRecord) error {
	if rec.Version != studyRecordVersion {
		return fmt.Errorf("ikkyoku/app: 控えの版が違います: %d", rec.Version)
	}
	study, err := position.RestoreStudy(rec.Study)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study != nil {
		return fmt.Errorf("ikkyoku/app: 既に検討が始まっています")
	}
	s.study = study
	s.game = rec.Game
	s.sourceURL = rec.SourceURL
	s.session = rec.ID
	// ⚠️ **点の手数と指し手は木から引き直す**（控えには入っていない）。
	// **木に無い節点の点は捨てる** —— 描く先が無いので持っていても意味が無い。
	nodes := map[int]position.Node{}
	for _, n := range study.Nodes() {
		nodes[n.ID] = n
	}
	base := 0
	if n := study.Root().MoveNumber; n > 0 {
		base = n - 1
	}
	s.evals.load(rec.Evals, func(id int) (int, int, string, bool) {
		if id == 0 {
			return 0, base, "", true
		}
		n, ok := nodes[id]
		if !ok {
			return 0, 0, "", false
		}
		text := n.Text
		if text == "" {
			text = n.USI
		}
		return n.Number, base + n.Number, text, true
	})
	// ⚠️ **`rev` は進める**（別ウィンドウが描き直せるように）。⚠️ **`study:changed`
	// はここで出さない** —— 起動時なので、まだ聞いている窓が無い。
	s.rev++
	return nil
}
