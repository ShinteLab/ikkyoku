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
