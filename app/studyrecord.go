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

// StudyEngine は**その評価値を出したエンジン**（記録した時点の写し）。
//
// ⚠️ **控えを開き直したときに「この数字を信じてよいか」を決める材料。**
// 折れ線の凡例に出す 1 語（`Label`）だけでは、**同じ名前で中身を入れ替えた**
// 登録と区別が付かない。
//
// ⚠️ **記録した時点の写しであること。** あとで設定を変えても動かさない ——
// 勝率をポナンザ定数の変更で計算し直さないのと同じで、**点は「そのとき何と
// 出たか」の記録**。
type StudyEngine struct {
	// ID は設定の登録 ID（**点の鍵**）。
	ID string `json:"id"`
	// Label は凡例に出ていた名前（`EngineEntry.DisplayName()` の結果）。
	Label string `json:"label,omitempty"`
	// Name は**人が付けた名前**。⚠️ **`EngineName` より前に出すもの。**
	Name string `json:"name,omitempty"`
	// EngineName は**エンジンが名乗った名前**（`usi` の `id name`）。
	//
	// ⚠️ **人が付けた名前とは別物。** 同じ exe を option 違いで 2 つ登録すると
	// **名乗りは同じ**になるので、これだけでは区別できない。
	EngineName string `json:"engineName,omitempty"`
	// Path は exe のパス（**同梱エンジンなら空**）。
	Path string `json:"path,omitempty"`
	// Color は折れ線の色（`#rrggbb`）。**解決済みの値を持つ**ので、
	// 登録が消えていても控えのとおりに描ける。
	Color string `json:"color,omitempty"`
	// MultiPV はそのとき出させた候補手の本数。
	MultiPV int `json:"multiPv,omitempty"`
	// Options はそのとき読ませた option（**人が設定した値だけ**）。
	//
	// ⚠️ **宣言された既定値は入れない**（`OptionSpecs`）。あれはエンジンの版に
	// 属する話で、**この木に効いていたのは人が入れた値のほう**。
	Options map[string]string `json:"options,omitempty"`
}

// StudyEvalSeries はエンジン 1 つぶんの点（**木の全部**）。
//
// ⚠️ **`EvalSeries` と似ているが別物。** あちらは**今の経路の点だけ**を折れ線に
// する描画用で、こちらは**枝の点も残らず**持つ。経路で絞って控えると、
// **開き直したときに枝の評価値だけ消えている。**
type StudyEvalSeries struct {
	EngineID string      `json:"engineId"`
	Label    string      `json:"label,omitempty"`
	Points   []EvalPoint `json:"points"`
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
	// Engines は**どのエンジンだったか**（`Evals` と同じ並び）。
	Engines []StudyEngine `json:"engines"`
	// Evals は評価値の点（木の全部）。
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

// noteEngine は**そのエンジンが何だったか**を控える（`AnalyzeService` から）。
//
// ⚠️ **公開しない**（Service の公開メソッドはフロントの API になる）。
func (s *StudyService) noteEngine(e StudyEngine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evals.note(e)
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
	evals := s.evals.all()
	engines := make([]StudyEngine, 0, len(evals))
	for _, se := range evals {
		if e, ok := s.evals.engines[se.EngineID]; ok {
			engines = append(engines, e)
			continue
		}
		// **控えが無くても id と凡例の名前だけは残す**（設計原則3）——
		// 折れ線は描ける。分からないのは「どの exe だったか」だけ。
		engines = append(engines, StudyEngine{ID: se.EngineID, Label: se.Label})
	}
	return StudyRecord{
		Version:   studyRecordVersion,
		ID:        s.session,
		SavedAt:   time.Now().Unix(),
		Study:     snap,
		Game:      game,
		SourceURL: s.sourceURL,
		Engines:   engines,
		Evals:     evals,
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
	s.evals.load(rec.Evals, rec.Engines)
	// ⚠️ **`rev` は進める**（別ウィンドウが描き直せるように）。⚠️ **`study:changed`
	// はここで出さない** —— 起動時なので、まだ聞いている窓が無い。
	s.rev++
	return nil
}
