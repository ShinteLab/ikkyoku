// 起動はできたが足りないもの・できないこと（2026-10-04）。
//
// **メイン画面のツールバーの ⚠ と、押すと出る一覧。** 中身は Go 側の `IssueService` が
// 持ち（認識器が無い・棋譜データベースを開けない・設定ファイルが壊れている・前回は
// 異常終了した など）、ここは**写して描くだけ**。
//
// ⚠️ **問題が無いときはボタンごと出さない**（`hidden`）。いつも出ていると見慣れて、
// 出たときに気づかれない。
// ⚠️ **一覧は Go 側が直ったら消す**（`issues:changed`）。ここで「既読」にして隠さないこと
// —— 直っていないのに消えると、また「黙って動かない」に戻る。
// ⚠️ **吹き出しは `position: fixed` で body に置く**（`.hint-bubble` / `.popup-menu` と同じ理由。
// ツールバーの中に置くと、ツールバーの移動ハンドル（`--wails-draggable: drag`）の
// 上に乗って、押せなくなる・選べなくなる）。
// ⚠️ **文言は Go 側が持つ**（`_cmd/ikkyoku/issues.go`）。ここで言い換えないこと。
import { Events } from "@wailsio/runtime";
import { IssueService } from "../bindings/github.com/ShinteLab/ikkyoku/app";
import type { IssueReport } from "../bindings/github.com/ShinteLab/ikkyoku/app/models";

export function mountIssues(button: HTMLButtonElement): void {
  let report: IssueReport = { issues: [], logDir: "" };
  let pop: HTMLDivElement | null = null;

  const issues = () => report.issues ?? [];

  const close = () => {
    if (!pop) {
      return;
    }
    pop.remove();
    pop = null;
    button.setAttribute("aria-expanded", "false");
    document.removeEventListener("pointerdown", onOutside, true);
    document.removeEventListener("keydown", onKey, true);
  };

  const onOutside = (e: PointerEvent) => {
    const t = e.target as Node | null;
    if (t && (pop?.contains(t) || button.contains(t))) {
      return;
    }
    close();
  };

  const onKey = (e: KeyboardEvent) => {
    if (e.key === "Escape") {
      e.stopPropagation();
      close();
      button.focus();
    }
  };

  // 一覧を組む。⚠️ **textContent で入れること**（エラーの文はパスや外部の文言を含む）。
  const render = (box: HTMLDivElement) => {
    box.replaceChildren();
    const list = document.createElement("ul");
    list.className = "issues-list";
    for (const is of issues()) {
      const li = document.createElement("li");
      li.className = `issue ${is.level === "error" ? "is-error" : "is-warn"}`;
      const title = document.createElement("div");
      title.className = "issue-title";
      title.textContent = is.title;
      li.append(title);
      for (const [cls, text] of [
        ["issue-effect", is.effect],
        ["issue-detail", is.detail],
      ] as const) {
        if (!text) {
          continue;
        }
        const el = document.createElement("div");
        el.className = cls;
        el.textContent = text;
        li.append(el);
      }
      list.append(li);
    }
    box.append(list);
    if (report.logDir) {
      const log = document.createElement("div");
      log.className = "issues-log";
      const label = document.createElement("span");
      label.textContent = "ログ: ";
      const path = document.createElement("span");
      path.className = "issues-log-path";
      path.textContent = report.logDir;
      log.append(label, path);
      box.append(log);
    }
  };

  // ボタンの右端に揃えて下に出す。画面の左へはみ出すなら押し戻す。
  const place = (box: HTMLDivElement) => {
    const r = button.getBoundingClientRect();
    const margin = 8;
    box.style.top = `${Math.round(r.bottom + 6)}px`;
    box.style.maxHeight = `${Math.max(120, window.innerHeight - r.bottom - 6 - margin)}px`;
    const w = box.offsetWidth;
    const left = Math.min(r.right - w, window.innerWidth - w - margin);
    box.style.left = `${Math.max(margin, Math.round(left))}px`;
  };

  const open = () => {
    if (pop || issues().length === 0) {
      return;
    }
    pop = document.createElement("div");
    pop.className = "issues-pop";
    pop.setAttribute("role", "dialog");
    pop.setAttribute("aria-label", button.getAttribute("aria-label") ?? "");
    render(pop);
    document.body.append(pop);
    place(pop);
    button.setAttribute("aria-expanded", "true");
    document.addEventListener("pointerdown", onOutside, true);
    document.addEventListener("keydown", onKey, true);
  };

  const show = (next: IssueReport) => {
    report = next;
    const list = issues();
    button.hidden = list.length === 0;
    if (list.length === 0) {
      close();
      return;
    }
    const hasError = list.some((is) => is.level === "error");
    button.classList.toggle("is-error", hasError);
    button.classList.toggle("is-warn", !hasError);
    const label = `問題が ${list.length} 件あります`;
    button.setAttribute("aria-label", label);
    button.title = label;
    if (pop) {
      render(pop);
      place(pop);
    }
  };

  button.hidden = true;
  button.setAttribute("aria-haspopup", "dialog");
  button.setAttribute("aria-expanded", "false");
  button.addEventListener("click", () => {
    if (pop) {
      close();
    } else {
      open();
    }
  });

  // ⚠️ **イベントを先に受けてから読むこと** —— 逆だと、読んだあと受け始めるまでの間に
  // 変わった分を取りこぼす。⚠️ **読んでいるあいだにイベントが来たら、読んだ結果は捨てる**
  // （イベントのほうが新しい。どちらも一覧を丸ごと載せてくる）。
  let gotEvent = false;
  Events.On("issues:changed", (event: { data: IssueReport }) => {
    gotEvent = true;
    show(event.data);
  });
  void (async () => {
    try {
      const r = await IssueService.Report();
      if (!gotEvent) {
        show(r);
      }
    } catch {
      /* 読めなくても画面は動く（⚠ が出ないだけ）。 */
    }
  })();
}
