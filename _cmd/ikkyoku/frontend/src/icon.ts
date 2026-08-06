// react-icons のアイコンを、このアプリ(素の TypeScript)で使うための薄い橋渡し。
//
// react-icons が配っているのは React コンポーネントだけで、SVG 文字列を直接取り出す
// 公開 API は無い。アイコンの実体は `GenIcon` で組んだ React 要素なので、React の
// レンダラを通さないと SVG にならない(コンポーネントを素の関数として呼ぶと
// useContext(IconContext) が dispatcher 未設定で落ちる)。
//
// そのため react + react-dom/server を入れて、**マウント時に一度だけ**静的マークアップへ
// 変換して innerHTML に埋める。React でアプリを書くわけではなく、ここが唯一の React の
// 使用箇所。UI の更新は今までどおり素の DOM 操作でやる(react-dom のクライアント側
// ランタイムは一切読み込まれない)。
//
// SVG のサイズ・色は CSS 側で決める。ここでは size / color を渡さず、react-icons の
// 既定(width/height="1em"、色は currentColor)のまま出すので、置いた要素の
// font-size と color にそのまま追従する。
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { IconType } from "react-icons";

// アイコンは装飾。意味はボタン側の aria-label / title が持つので、支援技術からは隠す。
export function iconMarkup(icon: IconType): string {
  return renderToStaticMarkup(createElement(icon, { "aria-hidden": true, focusable: "false" }));
}
