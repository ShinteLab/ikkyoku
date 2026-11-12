// 中継を追う（メイン画面の側。2026-11-13 に `mainscreen.ts` から切り出した）。
//
// 解析タブの「中継を追う」ボタン・視点の上の札・枠の下の帯（状態の行と知らせ）を描き、
// 枠のボタン（`follow:toggle`）と帯の候補（`follow:choose`）を Go へ渡す。
// ⚠️ **押して描くだけ** —— 1 周の段取りも状態（追っているか・見失いの数・控え・帯の候補）も
// Go の中継セッション（`app` の `followsession.go`）が持ち、`follow:update` で来る。
// 理由は `_docs/design-follow.md` の「中継セッション」、帯は `_docs/cmd-frame.md` の「枠の下の帯」。
//
// ⚠️ **受け渡しは `FollowControlOptions` / `FollowControlHandle` だけ**（`sidepane.ts` と同じ型）。
// 盤にも手順にも触れない（知らせを手順の下に出すときだけ `setSideStatus`）。
import { Events } from "@wailsio/runtime";
import { StudyService } from "../bindings/github.com/ShinteLab/ikkyoku/app";
import { noteFollowing } from "./followguard";

export interface FollowControlOptions {
  // studyFollowBtn は解析タブの「中継を追う / 追跡を止める」ボタン。
  studyFollowBtn: HTMLButtonElement;
  // studyLiveFollow は視点の上の札（追っているあいだだけ出す）。
  studyLiveFollow: HTMLElement;
  // setSideStatus は知らせの逃げ場（枠が出ていないときは手順の下に出す。`sidePane.setStatus`）。
  // ⚠️ **関数で渡す** —— 側の列はこれより後で組む（呼ばれるのはいつも組み終わったあと）。
  setSideStatus(text: string): void;
  // frameVisible は枠が出ているか（`frame:visible` の写し。持ち主は `mainscreen.ts` の `frame:visible`）。
  frameVisible(): boolean;
  // kifuFollowOn は棋譜の自動更新が入っているか（`StudyState.kifuFollow` の写し。持ち主は `studytab.ts` の `showStudy`）。
  kifuFollowOn(): boolean;
}

export interface FollowControlHandle {
  // publish は最後の状態を枠へ送り直す（枠が出たとき。`frame:visible`）。
  publish(): void;
  // start は中継を追い始める（追っていれば Go が何もしない）。
  start(): void;
  // setWaiting は次に来るはずの手数（`showStudy` が本譜の先端から割り出して入れる）。
  setWaiting(n: number): void;
}

export function mountFollowControl(opts: FollowControlOptions): FollowControlHandle {
  const { studyFollowBtn, studyLiveFollow } = opts;

  // ---- 中継を追う（2026-09-15。2026-10-11 に段取りを Go へ移した）-------------------
  //
  // **撮る → 認識する → 繋がるなら足す、を止めるまで繰り返すのは Go の中継セッション**（`app` の
  // `StudyService.StartFollow` / `StopFollow`。`followsession.go`）。ここは**押して、来た様子を描くだけ**。
  // ⚠️ **1 周の段取りをここに戻さないこと**（2026-10-11 に移した。判断が Go と画面に分かれていた。`_docs/design-follow.md` の「中継セッション」）。
  // ⚠️ **状態を持たない** —— 追っているか・見失いの数・控え・帯の候補は Go が持ち、`follow:update` で来る。
  // ここが持つのは**描くための写し**（`followOn` と最後の状態の行）だけ。
  //
  // ⚠️ **訂正タブへ飛ばさないこと。** 「訂正を挟むかどうか」をこちらが決めることになる。
  // **おかしくてもとにかく進み、違うと思ったら人が戻る**（推測で足した手には印が付いている。`Node.Guess`）。
  let followOn = false;

  // followWaiting は**次に来るはずの手数**（棋譜の数え方。0 は局面が無いとき）。
  //
  // ⚠️ **枠の札に出すのはこれ**（2026-09-15。時刻から替えた）。時刻は
  // 「1 周が回った」しか言わないが、**中継の画面にも手数が出ている**ので、
  // こちらが何手目を待っているかが分かれば**手を飛ばしたことに気づける**
  // （画面の手数と食い違っていたら、その差がそのまま取りこぼした手数）。
  let followWaiting = 0;

  // ⚠️ **状態は枠へも流すこと**（2026-09-15。実機で「枠側が録画しているか分からない」と出た）。
  // **追跡中に見ているのは中継**なので、状態も操作も**枠に無いと届かない**（メイン画面は裏に回っている）。
  //
  // ⚠️ **札（text）は「追跡中・n手目待ち」だけ**（2026-10-10）。周ごとの様子は**枠の下の帯の状態の行**（status）へ。
  // 札に混ぜると、札の長さが変わるたびにタイトルバーがチカチカした。状態の行は 1 行で切る（`frame.ts`）。
  // 止めた理由など**たまに知らせるもの**は `followNotice`（同じ帯の、状態の行の下）。
  let followStatus: { text: string; kind: FollowNoticeKind } = { text: "", kind: "" };
  // 引数なしは**最後の状態を送り直す**（枠が出たとき。`frame:visible`）。
  const publishFollow = (status?: string, kind: FollowNoticeKind = "") => {
    if (status === undefined) {
      ({ text: status, kind } = followStatus);
    }
    followStatus = { text: status, kind };
    noteFollowing(followOn);
    void Events.Emit("follow:state", {
      on: followOn,
      text: followOn ? followNote("追跡中") : "",
      status: followOn ? status : "",
      statusKind: followOn ? kind : "",
    });
  };

  // followNotice は中継の追従で**人に知らせる 1 行**を、**枠の下の帯**に出す（2026-10-07。空なら消す）。中身は Go が決める
  // （`follow:update` の notice）。ここが決めるのは**どこに出すか**と**いつ消すか**だけ。
  //
  // ⚠️ **枠が出ていないときは手順の下に出す**（見えないところに出しても届かない。枠を出さずにメイン画面から追い始めて、
  // 撮れずに止まったときがこれ。追っているあいだは枠を隠せない）。
  // fade は**しばらくして消す**か（人が自分で止めたときの「止めました」など。読めば済むもの）。
  type FollowNoticeKind = "" | "warn" | "error";
  const followNoticeFadeMs = 4000;
  let followNoticeTimer: number | undefined;
  let followNoticeInSide = false;
  // choices は**人に選んでもらう候補**（2026-10-09。打った駒が決まらないとき。帯にボタンで並ぶ）。
  type FollowNoticeChoice = { label: string; moves: string[] };
  const followNotice = (text: string, kind: FollowNoticeKind = "", fade = false, choices: FollowNoticeChoice[] = []) => {
    if (followNoticeTimer !== undefined) {
      window.clearTimeout(followNoticeTimer);
      followNoticeTimer = undefined;
    }
    if (followNoticeInSide) {
      followNoticeInSide = false;
      opts.setSideStatus("");
    }
    if (text && !opts.frameVisible()) {
      followNoticeInSide = true;
      opts.setSideStatus(text);
      void Events.Emit("follow:notice", { text: "", kind: "" });
      return;
    }
    void Events.Emit("follow:notice", { text, kind, choices });
    if (text && fade) {
      followNoticeTimer = window.setTimeout(() => followNotice(""), followNoticeFadeMs);
    }
  };

  // followNote は札に**次に来るはずの手数**を添える。
  //
  // ⚠️ **時刻も経過秒も出さないこと**（2026-09-15 に実機で外した）。どちらも**動いている証明にならない**うえ、
  // 札は狭いので**役に立つものを 1 つだけ出す**。
  const followNote = (label: string) =>
    followWaiting > 0 ? `${label}・${followWaiting}手目待ち` : label;

  // setFollowing は**入り切りを画面に描く**（ボタン・札）。⚠️ **入り切りを決めるのは Go**（`follow:update` の on）。
  const setFollowing = (on: boolean) => {
    followOn = on;
    studyFollowBtn.setAttribute("aria-pressed", on ? "true" : "false");
    // ⚠️ **「追跡中」はボタンに書かない**（2026-10-08。視点の上の札が言う）。
    // ボタンは**押すと何が起きるか**だけを書き、光らせもしない（同じ状態を 2 か所に出さない）。
    studyFollowBtn.textContent = on ? "追跡を止める" : "中継を追う";
    studyLiveFollow.hidden = !on;
  };

  // **Go の中継セッションの様子**（`follow:update`。`app` の `FollowUpdate`。⚠️ **型は bindings に出ないので手書き**）。
  type FollowUpdate = {
    on: boolean;
    waiting: number;
    status: string;
    statusKind: FollowNoticeKind;
    notice?: { text: string; kind: FollowNoticeKind; fade: boolean; choices?: FollowNoticeChoice[] | null } | null;
  };
  Events.On("follow:update", (e: { data: FollowUpdate }) => {
    const u = e.data;
    if (!u) {
      return;
    }
    if (u.on !== followOn) {
      setFollowing(u.on);
    }
    followWaiting = u.waiting ?? 0;
    publishFollow(u.status ?? "", u.statusKind ?? "");
    // notice が無ければ知らせは変えない。
    if (u.notice) {
      followNotice(u.notice.text ?? "", u.notice.kind ?? "", !!u.notice.fade, u.notice.choices ?? []);
    }
  });

  // ⚠️ **枠のボタンからも入り切りできること。** 押されたらメイン画面のボタンと同じことをする。
  Events.On("follow:toggle", () => studyFollowBtn.click());
  // **帯で選ばれた、打った駒の候補**（2026-10-09。`frame.ts` が投げる）。足すのは Go（`ChooseFollow`）。
  Events.On("follow:choose", (e: { data: { moves?: string[] } }) => {
    const moves = e.data?.moves;
    if (!followOn || !moves || moves.length === 0) {
      return;
    }
    void StudyService.ChooseFollow(moves);
  });

  // startFollow は中継を追い始める（追っていれば Go が何もしない）。
  // ⚠️ **画面の `followOn` で入り切りを決めないこと** —— 止めた知らせ（`follow:update`）は止め終わったあとに届くことがあり、
  // 確認で止めてすぐ「そのまま追跡」すると、まだ追っている写しのまま止める側に倒れる（`adoptToStudy`）。
  const startFollow = () => {
    // 前に止めたときの知らせ（「止めました」など）が残っていれば消す。
    followNotice("");
    // ⚠️ **棋譜の自動更新は止める**（2026-10-10。同じ対局を両方で追う意味はほぼ無いので、
    // 後から押したほうが勝つ。聞かない）。逆向きは `sidepane.ts`。理由は `_docs/app-kifu-load.md` の「自動更新」。
    if (opts.kifuFollowOn()) {
      void StudyService.SetKifuFollow(false)
        .then(() => opts.setSideStatus("棋譜の自動更新を止めて、中継の追跡を始めました"))
        .catch((err) => opts.setSideStatus(String(err instanceof Error ? err.message : err)));
    }
    // ⚠️ **押した瞬間の盤を「追う盤」として覚えるのは Go**（訂正してから追うなら訂正した 1 枚。撮る側の `AnchorBoard`）。
    // 決まらなければ Go が理由を帯に出して止まる。
    void StudyService.StartFollow().catch((err) => {
      followNotice(String(err instanceof Error ? err.message : err), "error");
    });
  };

  studyFollowBtn.addEventListener("click", () => {
    if (followOn) {
      void StudyService.StopFollow();
      return;
    }
    startFollow();
  });

  return {
    publish: () => publishFollow(),
    start: startFollow,
    setWaiting: (n) => {
      followWaiting = n;
    },
  };
}
