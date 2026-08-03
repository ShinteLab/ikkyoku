// 盤面に重ねる透過ガイド枠。
//
// 描くのはガイド枠(2px)だけ。ボタンなどの操作 UI は一切置かない
// (この画面に置いた要素はそのままキャプチャに写り込むため。撮る操作は panel.ts 側)。
// ガイド枠の太さは Go 側(_cmd/ikkyoku/captureservice.go の guideBorderPx)と
// 必ず一致させること。ここを変えたら Go 側の定数も直す。
export const GUIDE_BORDER_PX = 2;

export function mountFrame(root: HTMLElement): void {
  const guide = document.createElement("div");
  guide.className = "capture-guide";
  guide.style.borderWidth = `${GUIDE_BORDER_PX}px`;
  root.appendChild(guide);
}
