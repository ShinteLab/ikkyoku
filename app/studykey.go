package app

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// 検討の「出どころの鍵」（2026-09-26）。
//
// **棋譜タブ（棚）に入っていない検討を、あとから同じ棋譜に結び直すための鍵。**
// 控え（`StudyRecord.SourceKey`）に書いておき、次の 2 つで引く:
//
//	同じ棋譜をもう一度「解析する」  … 前の検討の続きから開く（中継カード・貼り付け）
//	同じ棋譜を棋譜タブに入れた      … その控えに棚の棋譜 id を書き足す（`GameID`）
//
// **なぜ要るか。** 中継は数時間かけて完成し、そのあいだに別の棋譜を解析するのが
// 普通。控えは起動時に「一番新しいもの」しか戻らないので、**鍵が無いと
// カードから開き直すたびに解析がゼロから**になる（追いつくまで数分かかる）。
// 棚の棋譜 id は**終局して保存するまで付かない**ので、それより前から使える鍵が要る。
//
// ⚠️ **同一性を棚の id から借りない**、という `StudyRecord.ID` の約束はそのまま。
// これは**あとから結ぶための手掛かり**であって、検討の id ではない。

// sourceKeyOf は取得元（kicho の `source` / `source_id`）から鍵を作る。
//
// **中継カードの鍵。** 連盟・読売なら取り直しても同じ値になる（kicho の
// `(source, source_id)` は棚の主キーでもある）ので、**カードの「解析する」と
// 「保存」と棋譜タブの行が同じ鍵で繋がる。** どちらかが空なら鍵は無い。
func sourceKeyOf(source, sourceID string) string {
	source, sourceID = strings.TrimSpace(source), strings.TrimSpace(sourceID)
	if source == "" || sourceID == "" {
		return ""
	}
	return "src:" + source + ":" + sourceID
}

// pasteKeyOf は貼り付けた KIF の本文から鍵を作る（**同じ本文なら同じ鍵**）。
//
// ⚠️ **本文そのものを控えに書かないこと**（ハッシュで足りる）。
// ⚠️ **改行コードと前後の空白だけ揃える** —— 貼り付け元によって CRLF / LF が
// 入れ替わるのは普通にあるので、そこで別物にしない。中身を解釈して揃えるのは
// やめておく（揃え方を間違えると**別の棋譜を同じ検討に結ぶ**）。
func pasteKeyOf(text string) string {
	t := strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if t == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(t))
	return "kif:" + hex.EncodeToString(sum[:])
}
