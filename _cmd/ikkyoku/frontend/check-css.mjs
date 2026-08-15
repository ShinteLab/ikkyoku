// style.css のコメントの閉じ忘れを検出する。
//
// **CSS は壊れても黙って動く。** コメントを閉じ忘れると、そこから次の `{` までが
// セレクタとして読まれ、**その直後のルールが丸ごと捨てられる**。実際に
// `.board-stage { position: relative }` が消えて、訂正 UI のグリッドが盤からずれた
// （position が効かないので、重ねる基準がページ全体になる）。
//
// tsc も vite も CSS の中身は見ないので、ここで見る。`npm run build` の前に走る。
import { readFileSync } from "node:fs";

const path = new URL("./public/style.css", import.meta.url);
const css = readFileSync(path, "utf8");

let depth = 0;
let line = 1;
const problems = [];
for (let i = 0; i < css.length; i++) {
  if (css.startsWith("/*", i)) {
    if (depth > 0) {
      problems.push(`${line} 行目: コメントの中で /* が始まっています`);
    }
    depth++;
    i++;
    continue;
  }
  if (css.startsWith("*/", i)) {
    if (depth === 0) {
      problems.push(`${line} 行目: 対応する /* の無い */ があります（直後のルールが捨てられます）`);
    } else {
      depth--;
    }
    i++;
    continue;
  }
  if (css[i] === "\n") {
    line++;
  }
}
if (depth > 0) {
  problems.push("ファイル末尾でコメントが閉じられていません");
}

if (problems.length > 0) {
  console.error("style.css のコメントが壊れています:");
  for (const p of problems) {
    console.error(`  - ${p}`);
  }
  process.exit(1);
}

// ---- markup の中のバッククォート --------------------------------------------
//
// **画面の markup は template literal の中に書いてある**（`root.innerHTML = ...`）。
// そこに**バッククォートを書くと、文字列がその場で終わる**（コメントの中でも
// 属性の中でも同じ）。
//
// tsc は止めてくれるが、出るのは「';' expected」という**まったく別の場所の
// 文法エラーに見えるもの**で、原因に辿り着くのに毎回時間がかかる（実際に
// 3 回踏んだ）。**何が起きたかをここで名指しする。**
//
// ⚠️ **見るのは `root.innerHTML = \`` から始まって、行頭 2 つ空けの \`; で
// 終わるところまで**（その 2 行自体は当然バッククォートを含むので飛ばす）。
// 中に `${...}` は普通に出てくるが、**あれはバッククォートを含まない**。
const files = ["./src/mainscreen.ts", "./src/frame.ts"];
const badTicks = [];
for (const file of files) {
  const lines = readFileSync(new URL(file, import.meta.url), "utf8").split("\n");
  let inMarkup = false;
  lines.forEach((text, i) => {
    if (!inMarkup) {
      inMarkup = /root\.innerHTML = `\s*$/.test(text);
      return;
    }
    if (/^\s*`;\s*$/.test(text)) {
      inMarkup = false;
      return;
    }
    if (text.includes("`")) {
      badTicks.push(`${file}:${i + 1}: ${text.trim()}`);
    }
  });
}
if (badTicks.length > 0) {
  console.error(
    "markup の中にバッククォートがあります" +
      "（template literal なので、文字列がそこで切れます）:",
  );
  for (const b of badTicks) {
    console.error(`  - ${b}`);
  }
  process.exit(1);
}
