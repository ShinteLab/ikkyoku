// 棋譜の印（2026-11-19。`_docs/app-shelf.md` の「棋譜の印」）。棋譜タブの行・詳細と入力タブのカードが使う。
//
// ⚠️ **kicho の値をそのまま出す**（`GameSummary` の `errorMove` / `flags` / `parentId` など）。
// **画面で本文を読んで判定しないこと。** 並びは kicho アプリ（`ErrorBadge` / `FlagBadge` / `LineageTags`）に揃える:
// `⚠ N手目`・`指摘 N`・`訂正版`・`親が更新`・`親は削除済み`・`派生 N`。
import type { GameSummary } from "../bindings/github.com/ShinteLab/ikkyoku/app/models";

// RELATION_LABELS は親との関係の表示名（kicho の `store.Relation*`）。
const RELATION_LABELS: Record<string, string> = {
  correction: "訂正版",
};

const badge = (cls: string, text: string, title = ""): HTMLElement => {
  const s = document.createElement("span");
  s.className = `kifu-mark ${cls}`;
  s.textContent = text;
  if (title) s.title = title;
  return s;
};

// errorMarks は盤に当たらない手の印（`⚠ N手目`。理由はバルーン）。**カードに出るのはこれだけ。**
export function errorMarks(g: Pick<GameSummary, "errorMove" | "error">): HTMLElement[] {
  if (!g.errorMove) return [];
  return [badge("is-error", `⚠ ${g.errorMove}手目`, g.error)];
}

// shelfMarks は棚の棋譜の印の全部（エラー・指摘・親子）。
export function shelfMarks(g: GameSummary): HTMLElement[] {
  const out = errorMarks(g);
  if (g.flags > 0) {
    const from = g.flaggedMove > 0 ? `${g.flaggedMove}手目から` : "棋譜全体";
    out.push(
      badge(
        g.flagsStale ? "is-flag is-stale" : "is-flag",
        `指摘 ${g.flags}`,
        g.flagsStale ? `${from}（指摘のあとに本文が変わっています）` : from,
      ),
    );
  }
  const isChild = g.parentId !== "" || g.relation !== "";
  if (isChild) {
    out.push(badge("is-lineage", RELATION_LABELS[g.relation] || g.relation || "派生"));
    if (g.parentChanged) out.push(badge("is-lineage is-warn", "親が更新"));
    if (g.parentMissing) out.push(badge("is-lineage is-warn", "親は削除済み"));
  }
  if (g.children > 0) out.push(badge("is-lineage", `派生 ${g.children}`));
  return out;
}

// FLAG_BY_LABELS は指摘を付けたもの（kicho の `Flag.By`）の表示名。知らない値はそのまま出す。
const FLAG_BY_LABELS: Record<string, string> = {
  ikkyoku: "一局",
  user: "人",
};

export const flagByLabel = (by: string): string => FLAG_BY_LABELS[by] ?? (by || "-");
