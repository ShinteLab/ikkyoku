package app

// 棋譜の誤記を直す・指摘するのうち、棚（kicho）に触るもの（2026-11-19。`_docs/spec-kifu-fix.md`）。
//
// ⚠️ **kicho は持つ・繋ぐ・見せるだけ**で、判断も訂正もしない。直す操作と反則手の発見は ikkyoku（`kifufix.go`）。
// ⚠️ **指摘をエラーの印へ寄せて書かないこと**（kicho の約束。あちらは取り直しで消える）。

import (
	"fmt"
	"strings"

	"github.com/ShinteLab/ikkyoku/log"
	"github.com/ShinteLab/kicho"
	"github.com/ShinteLab/kicho/store"
)

// connectStudyFlags は解析タブへ棚の指摘を読む口と付ける口を差し込む（`NewKifuService` から）。
func (s *KifuService) connectStudyFlags() {
	if s.study == nil {
		return
	}
	s.study.flagsOf = s.studyFlags
	s.study.addFlag = func(gameID string, move int, reason, by string) error {
		lib, err := s.library()
		if err != nil {
			return err
		}
		ctx, cancel := kifuDBContext()
		defer cancel()
		_, err = lib.Flag(ctx, gameID, move, reason, by)
		return err
	}
}

// studyFlags は棚の棋譜の指摘と kicho のエラーの印（⚠️ **読めなくても空で返す**。設計原則3）。
func (s *KifuService) studyFlags(gameID string) ([]store.Flag, int) {
	lib, err := s.library()
	if err != nil {
		return nil, 0
	}
	ctx, cancel := kifuDBContext()
	defer cancel()
	flags, err := lib.Flags(ctx, gameID)
	if err != nil {
		log.Warn("指摘を読めませんでした", "gameId", gameID, "error", err)
		return nil, 0
	}
	errMove := 0
	if rec, err := lib.Get(ctx, gameID); err == nil {
		errMove = rec.ErrorMove
	}
	return flags, errMove
}

// FlagMove は解析タブの本譜の手に指摘を付ける（手順リストの右クリック →「指摘する…」。`By` は "user"）。
//
// 正しい手が分からないとき用（直せるなら「直しにする」）。⚠️ **棚の棋譜にしか付けられない**（付け先が要る）。
func (s *KifuService) FlagMove(id int, reason string) (StudyState, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return s.study.State(), fmt.Errorf("指摘の理由を書いてください")
	}
	gameID, move, err := s.study.flagTarget(id)
	if err != nil {
		return s.study.State(), err
	}
	lib, err := s.library()
	if err != nil {
		return s.study.State(), err
	}
	ctx, cancel := kifuDBContext()
	defer cancel()
	if _, err := lib.Flag(ctx, gameID, move, reason, flagByUser); err != nil {
		return s.study.State(), describeKifuError(err)
	}
	log.Info("指摘を付けました", "gameId", gameID, "move", move)
	return s.study.flagsChanged(), nil
}

// Unflag は指摘を外す（手順リストの右クリック →「指摘を外す」・棋譜タブの詳細）。
//
// ⚠️ **ikkyoku が付けた反則手の指摘も外せる**（実際に指された反則手ということもある）。外したことは控えに覚え、
// 同じ原本のままなら付け直さない。
func (s *KifuService) Unflag(flagID string) (StudyState, error) {
	lib, err := s.library()
	if err != nil {
		return s.study.State(), err
	}
	ctx, cancel := kifuDBContext()
	defer cancel()
	for _, f := range s.study.currentFlags() {
		if f.ID == flagID {
			s.study.dismissFlag(f)
		}
	}
	if err := lib.Unflag(ctx, flagID); err != nil {
		return s.study.State(), describeKifuError(err)
	}
	log.Info("指摘を外しました", "flag", flagID)
	return s.study.flagsChanged(), nil
}

// saveCorrection は**直した本譜を訂正版として棚に入れる**（投了の行の右クリック →「訂正版として登録」）。
//
//   - 親は**原本**。棚に無ければ先に原本を親として入れる（取得元があれば取り直して、無ければ貼り付けとして）
//   - 子は直した本譜の KIF（`SaveDerived(親, correction, …)`。対局名などは kicho が親で埋める）
//   - **盤に当たる誤記を置き換えた手には、親に指摘も付ける**（盤に当たらない誤記は kicho のエラーの印が既にある）
//
// ⚠️ **検討は親と結んだまま**にする（直しの表は親の控えにあり、直し直すのは親から。子は普通の KIF）。
func (s *KifuService) saveCorrection() (GameSummary, error) {
	text, err := s.study.exportKIF()
	if err != nil {
		return GameSummary{}, err
	}
	lib, err := s.library()
	if err != nil {
		return GameSummary{}, err
	}
	parent, err := s.correctionParent(lib)
	if err != nil {
		return GameSummary{}, err
	}
	ctx, cancel := kifuDBContext()
	defer cancel()
	child, err := lib.SaveDerived(ctx, parent, store.RelationCorrection, text)
	if err != nil {
		return GameSummary{}, describeKifuError(err)
	}
	existing, _ := lib.Flags(ctx, parent)
	for _, cf := range s.study.correctionFlags() {
		dup := false
		for _, f := range existing {
			if f.Move == cf.Move && f.Reason == cf.Reason {
				dup = true
			}
		}
		if dup {
			continue
		}
		if _, err := lib.Flag(ctx, parent, cf.Move, cf.Reason, flagByIkkyoku); err != nil {
			log.Warn("訂正版の指摘を付けられませんでした", "parent", parent, "move", cf.Move, "error", err)
		}
	}
	s.study.markRegistered(parent)
	s.study.saveNow()
	log.Info("訂正版を棚に登録しました", "parent", parent, "child", child.ID, "moves", child.Moves)
	return toSummary(child), nil
}

// correctionParent は訂正版の親（原本）の棚の id。棚に無ければ原本を入れる。
func (s *KifuService) correctionParent(lib *kicho.Library) (string, error) {
	gameID, key, original := s.study.correctionSource()
	if gameID != "" {
		return gameID, nil
	}
	if original == "" {
		return "", fmt.Errorf("原本がありません")
	}
	ctx, cancel := kifuNetContext()
	defer cancel()
	var rec store.Record
	var err error
	if source, sourceID, ok := splitSourceKey(key); ok {
		// **取得元があればその棋譜として入れる**（カードの「棋譜に保存」と同じ。取り直せる親になる）。
		var f kicho.Fetched
		if f, err = kifuFetcher.Refresh(ctx, source, sourceID); err == nil {
			rec, err = lib.Save(ctx, f)
		}
	} else {
		rec, err = lib.ImportKIF(ctx, original)
	}
	if err != nil {
		return "", fmt.Errorf("原本を棋譜タブに入れられませんでした: %w", describeKifuError(err))
	}
	s.study.setGameID(rec.ID)
	return rec.ID, nil
}

// currentFlags は今の棚の棋譜の指摘（読んだもの）。
func (s *StudyService) currentFlags() []store.Flag {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]store.Flag(nil), s.fix.flags...)
}

// flagsChanged は指摘を読み直して描き直す（**ロックの外で呼ぶこと**）。
func (s *StudyService) flagsChanged() (st StudyState) {
	defer func() { s.publish(st, nil) }()
	s.refreshFlags()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changed()
}

// correctionPending は訂正版として登録する検討か（直した手がある）。
func (s *StudyService) correctionPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hasFixesLocked()
}

// correctionSource は訂正版の親を決めるもの（棚の id・出どころの鍵・原本の本文）。
func (s *StudyService) correctionSource() (gameID, key, original string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gameID, s.sourceKey, s.fix.text
}

// correctionFlags は訂正版を登録するときに親へ付ける指摘。
func (s *StudyService) correctionFlags() []correctionFlag {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.study == nil {
		return nil
	}
	return s.correctionFlagsLocked()
}

// markRegistered は訂正版を登録したことを覚える（同じ本譜で 2 度登録しない）。親とは結んだまま。
func (s *StudyService) markRegistered(parent string) {
	s.mu.Lock()
	if s.study != nil {
		s.fix.registered = mainDigest(s.study.MainLine())
	}
	if s.gameID == "" {
		s.gameID = parent
	}
	st := s.changed()
	s.mu.Unlock()
	s.publish(st, nil)
}
