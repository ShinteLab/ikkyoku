package main

import (
	"path/filepath"
	"strings"

	"github.com/ShinteLab/ikkyoku/log"
)

// 「中継を追うあいだは盤の読みを優先する」（設定 `followFirst`。2026-10-07）の判断。
//
// 追っているあいだだけ、ikkyoku が起動した外部エンジンのプロセスの優先度を「通常以下」に下げる。
// エンジンは空いた CPU では今までどおり全力で回り、81 マスの読みが要るときだけ読みが先に CPU を取る
// （エンジンと取り合うと 2 秒強が 8 秒に延び、成る手で見失った）。
//
// ⚠️ **既定は解析を優先**（設定が偽なら何もしない）—— 解析がこのアプリの本命。
// ⚠️ **止めたら「通常」に戻すこと**（`ClearBoardAnchor`）。
// ⚠️ **追っているあいだに新しく起きたエンジンも下げる**ので、81 マスを読むたびに合わせ直す（数ミリ秒）。
// ⚠️ **同梱エンジンには効かない**（ikkyoku と同じプロセスで動く）。

// applyFollowFirst は設定が切り替わったときに呼ばれる（`SettingsService.OnFollowFirst`）。
// ⚠️ **設定のロックを持ったまま呼ばれる**ので、合わせ直しは別のゴルーチンで行う（先で設定を読むため）。
func (s *CaptureService) applyFollowFirst(v bool) {
	s.mu.Lock()
	s.followFirst = v
	s.mu.Unlock()
	go s.syncEnginePriority()
}

// syncEnginePriority は**今あるべき優先度**にエンジンを合わせる（追っていて設定が真なら下げ、そうでなければ戻す）。
func (s *CaptureService) syncEnginePriority() {
	s.mu.Lock()
	want := s.followFirst && s.boardAnchor.Board.Dx() > 0
	lowered, names := s.enginesLowered, s.engineNames
	s.mu.Unlock()
	if !want && !lowered {
		return
	}
	set := map[string]bool{}
	if names != nil {
		for _, p := range names() {
			if p != "" {
				set[strings.ToLower(filepath.Base(p))] = true
			}
		}
	}
	if len(set) == 0 {
		return
	}
	n, err := setEnginePriority(set, want)
	if err != nil {
		log.Warn("エンジンの優先度を変えられません", "error", err)
		return
	}
	s.mu.Lock()
	s.enginesLowered = want
	s.mu.Unlock()
	if want != lowered {
		if want {
			log.Info("追っているあいだ、外部エンジンの優先度を下げました（盤の読みを優先）", "プロセス", n)
		} else {
			log.Info("外部エンジンの優先度を戻しました", "プロセス", n)
		}
	}
}
