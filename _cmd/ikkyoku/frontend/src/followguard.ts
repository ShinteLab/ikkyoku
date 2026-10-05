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
// | 本譜の先端で手を指す | **聞くが止めない**（見失ったときに抜けた手を人が補う操作。補えば追従はそこから繋ぎ直す）。`study.ts` |
//
// ⚠️ **状態の持ち主はメイン画面**（`mainscreen.ts` の `followOn`）。ここは
// `follow:state`（1 周ごとにアプリ全体へ流れる）を映すだけで、切り離した窓でも同じに効く。

import { Dialogs, Events } from "@wailsio/runtime";

let following = false;

Events.On("follow:state", (e: { data: { on: boolean } }) => {
  following = !!e.data?.on;
});

// noteFollowing は**メイン画面が自分の状態をここへ写す**（自分の出したイベントが
// 自分に届くかに頼らないため）。
export function noteFollowing(on: boolean): void {
  following = on;
}

// isFollowing は中継を追っているか。
export function isFollowing(): boolean {
  return following;
}

let stopHere: (() => void) | null = null;

// setFollowStopper はメイン画面が**追跡を止める関数**を渡す（同じ窓ならイベントを介さず止める）。
export function setFollowStopper(fn: () => void): void {
  stopHere = fn;
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
  const yes = "はい（追跡を止める）";
  const got = await Dialogs.Question({
    Title: "中継を追っています",
    Message: `${what}と、中継の追跡が正しく続けられなくなります。\n追跡を止めて続けますか？`,
    Buttons: [
      { Label: yes },
      // ⚠️ **既定は「いいえ」** —— Enter の連打で追跡が止まらないように。
      { Label: "いいえ", IsCancel: true, IsDefault: true },
    ],
  });
  if (got !== yes) {
    return false;
  }
  if (stopHere) {
    stopHere();
  } else {
    void Events.Emit("follow:stop", null);
  }
  following = false;
  return true;
}
