// followguard.ts は**中継を追っているあいだに、追従を壊す操作を聞いてから通す**（2026-10-05）。
//
// 追従は**本譜の先端**から中継に繋ぐので、本譜を動かす操作をされると繋ぐ先がずれて、
// 手を足し間違えるか「見失っています」になる。⚠️ **ロックはしない** —— 聞いて、
// 「はい」なら**操作を通して追跡を止める**（`confirmStopFollow`）。
//
// | 操作 | 扱い |
// |---|---|
// | この局面を解析する・棋譜を読む・新規対局・棚やカードから解析 | 根が入れ替わる → 聞いて止める |
// | 本線にする | 本譜の先端に続きを据える → 聞いて止める |
// | 本譜の手を消す | **聞くが止めない**（消したところから 4 手以内なら追従が繋ぎ直す）。`movelist.ts` |
// | 解析タブの盤で駒を動かす | **動かせない**（2026-10-07。追跡を止めてから動かす）。`study.ts` |
// | 棋譜の自動更新を入れる | **聞かずに止める**（2026-10-10。`stopFollow`。後から押したほうが勝つ。`sidepane.ts`） |
//
// ⚠️ **状態の持ち主は Go の中継セッション**（`app` の `StudyService`。2026-10-11 にメイン画面から移した）。ここは
// `follow:update`（Go が 1 周ごとにアプリ全体へ流す）を映すだけで、切り離した窓でも同じに効く。

import { Dialogs, Events } from "@wailsio/runtime";
import { StudyService } from "../bindings/github.com/ShinteLab/ikkyoku/app";

let following = false;
const watchers: ((on: boolean) => void)[] = [];

// setFollowing は**入り切りが変わったときだけ**見張りへ知らせる（`follow:state` は 1 周ごとに来る）。
const setFollowing = (on: boolean) => {
  if (on === following) {
    return;
  }
  following = on;
  for (const fn of watchers) {
    fn(on);
  }
};

Events.On("follow:update", (e: { data: { on: boolean } }) => {
  setFollowing(!!e.data?.on);
});

// noteFollowing は**メイン画面が受けた状態をここへ写す**（イベントの届く順に頼らないため）。
export function noteFollowing(on: boolean): void {
  setFollowing(on);
}

// onFollowingChange は追跡の入り切りが変わったときに呼ばれる（解析タブの盤が
// 「動かせる駒」の見た目を描き直すのに使う。2026-10-07）。
export function onFollowingChange(fn: (on: boolean) => void): void {
  watchers.push(fn);
}

// isFollowing は中継を追っているか。
export function isFollowing(): boolean {
  return following;
}


// confirmStopFollow は追跡中なら「〜すると追跡を止めます」と聞き、**はいなら止めて真**を返す。
// 追跡していなければ聞かずに真。
//
// what は**何をしようとしているか**（「この局面を解析する」など）。
// ⚠️ **止めてから操作を通すこと**（呼び出し側は真が返ってから操作する）—— 逆だと、
// 走っている 1 周が入れ替えたあとの局面へ手を足しにいく。
export async function confirmStopFollow(what: string): Promise<boolean> {
  if (!following) {
    return true;
  }
  // ⚠️ **ボタンの Label は "Yes" / "No" から変えないこと**（2026-10-07 に実機で踏んだ）。
  // Windows の Wails（beta.26）は OS 標準の MessageBox（MB_YESNO）で出すので、**付けた文字は
  // 画面に出ず**（OS の言語で「はい」「いいえ」になる）、押した結果は "Yes" / "No" で返る。
  // それを **Label と文字列で突き合わせて** Promise を解く作りなので、Label が "Yes" でないと
  // **はいを押しても Promise が解けず、追跡も止まらず操作も通らないまま**になる
  // （以前は「はい（追跡を止める）」と書いていてそうなった）。
  // ⚠️ **既定を「いいえ」にするのも Label "No" + IsDefault が条件**（MB_DEFBUTTON2 になる）
  // —— Enter の連打で追跡が止まらないように。
  // ボタンに意味を書けないので、**「はい」が何をするかは本文で言い切ること**。
  const got = await Dialogs.Question({
    Title: "中継を追っています",
    Message: `${what}と、中継の追跡が正しく続けられなくなります。\n追跡を止めて続けますか？`,
    Buttons: [
      { Label: "Yes" },
      { Label: "No", IsCancel: true, IsDefault: true },
    ],
  });
  if (got !== "Yes") {
    return false;
  }
  await stopFollow();
  return true;
}

// stopFollow は**聞かずに**追跡を止める（追っていなければ何もしない）。どの窓からでも Go を直に呼ぶ。
// ⚠️ **止め終わるのを待ってから操作を通すこと**（`StudyService.StopFollow` は走っている 1 周が終わるまで待って返る）。
// 待たないと、走っている 1 周が入れ替えた局面へ手を足す。
export async function stopFollow(): Promise<void> {
  if (!following) {
    return;
  }
  try {
    await StudyService.StopFollow();
  } finally {
    setFollowing(false);
  }
}
