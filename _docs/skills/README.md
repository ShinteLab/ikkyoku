# _docs/skills

このリポジトリの**作業手順**（ビルドする・実機で確かめる）を置く場所。

| ディレクトリ | 中身 |
|---|---|
| `ikkyoku-build/` | ビルド・bindings の生成・配布ビルド（焼き込み）・worktree のジャンクション |
| `ikkyoku-verify/` | 実機で何を押すか（`references/checklist.md` = 未検証 / `references/verified.md` = 確認済み） |

- **ただの Markdown**（`SKILL.md` + `references/`）。どのコーディングエージェントでも、人が読んでもよい
- ⚠️ **リポジトリに `.claude/` を置かない**（Claude で使う前提になるため。`.gitignore` で無視している）
- Claude Code でスキルとして使うなら、手元で `.claude/skills/<名前>` から
  ここへジャンクション（シンボリックリンク）を張る。例（PowerShell、リポジトリ直下で）:

  ```powershell
  New-Item -ItemType Junction -Path .claude\skills\ikkyoku-verify -Target (Resolve-Path _docs\skills\ikkyoku-verify)
  ```

- ⚠️ **編集するのはここ**（git で管理しているのはこちらだけ）
