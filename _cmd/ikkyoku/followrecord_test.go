package main

import (
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ShinteLab/ikkyoku/log"
)

// ⚠️ **中継の録画は 1 週間（`log.KeepDays`）より前に始めたものだけ消し、録画の名前でないものは消さないこと**（2026-10-11）。
// それまでは消す仕組みが無く、`%APPDATA%\ikkyoku\captures\follow` に 102 回分・2GB 溜まっていた。
func TestRemoveOldRecordings(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	old := now.AddDate(0, 0, -(log.KeepDays + 1)).Format(followRecordLayout)
	recent := now.AddDate(0, 0, -(log.KeepDays - 1)).Format(followRecordLayout)
	for _, name := range []string{old, recent, "memo"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(root, name, "master.png"), []byte("x"), 0o644)
	}

	removeOldRecordings(root, now)

	if _, err := os.Stat(filepath.Join(root, old)); !os.IsNotExist(err) {
		t.Errorf("1 週間より前の録画 %s が残っています", old)
	}
	for _, name := range []string{recent, "memo"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("%s は消さないはず: %v", name, err)
		}
	}
}

// ⚠️ **設定「中継の録画とログを残す（デバッグ）」が切ってあれば、録画を 1 枚も残さないこと**（2026-10-11。dev でも配る exe でも）。
// 追っている途中で切ったら、次の 1 枚から残さない。
func TestFollowRecordingNeedsDebugSetting(t *testing.T) {
	s := &CaptureService{}
	s.fol.lastQuiet = image.NewRGBA(image.Rect(0, 0, 4, 4))
	if dir := s.startFollowDir(nil); dir != "" {
		t.Fatalf("設定が切ってあるのに録画の場所を作りました: %s", dir)
	}

	dir := t.TempDir()
	s.fol.followDir = dir
	if p := s.SaveFollowFrame(1, []string{"7g7f"}, false); p != "" {
		t.Errorf("設定が切ってあるのに手を決めた 1 枚を残しました: %s", p)
	}
	if p := s.SaveFollowMiss(1, "same"); p != "" {
		t.Errorf("設定が切ってあるのに繋がらなかった周を残しました: %s", p)
	}

	s.followDebug = true
	if p := s.SaveFollowFrame(1, []string{"7g7f"}, false); p == "" {
		t.Errorf("設定を入れたのに手を決めた 1 枚を残しません")
	}
}
