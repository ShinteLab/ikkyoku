// service worker（接続ブローカー）
//
// 役割は以下の3つに限定する。
//   1. side panel をアイコンクリックで開く設定
//   2. content script（bridge.js）から届く通信傍受イベントのバッファリングと中継
//   3. chrome.tabs.captureVisibleTab の実行（この API は service worker から呼ぶ必要がある）
//
// 盤面認識・SFEN変換などのロジックはここには置かない（診断のみが目的）。
// また "履歴に依存しない" 原則により、ここで保持するログはあくまで診断用のバッファであり、
// 消えても（service worker が休止して再起動しても）致命的にならない設計にしてある。

const MAX_LOG_PER_TAB = 300;

// tabId -> 通信傍受イベントの配列（診断用の一時バッファ。永続化はしない）
const netLogs = new Map();

// ホットキーで side panel を「今まさに開いた」場合、panel 側のスクリプトはまだ
// onMessage リスナーを登録していない。そのまま sendMessage しても取りこぼすので、
// 依頼をここに置いておき、panel 初期化時に引き取らせる。
let pendingSnapshot = null;

chrome.runtime.onInstalled.addListener(() => {
  chrome.sidePanel.setPanelBehavior({ openPanelOnActionClick: true }).catch((err) => {
    console.warn("[ikkyoku] setPanelBehavior に失敗:", err);
  });
});

// キーボードショートカット（既定 Alt+S）。
// side panel を開いた上で「スナップショット実行」を依頼するメッセージを投げる。
// side panel が開いていない/リスナーがいない場合は黙って失敗する（catch で握りつぶす）。
chrome.commands.onCommand.addListener(async (command, tab) => {
  if (command !== "capture-snapshot") return;
  if (!tab || tab.id === undefined) return;

  // 先に依頼を置いてから開く。すでに開いていれば下の sendMessage が拾い、
  // これから開く場合は panel 初期化時に consume-pending-snapshot で引き取られる。
  pendingSnapshot = { tabId: tab.id, windowId: tab.windowId, time: Date.now() };

  try {
    await chrome.sidePanel.open({ tabId: tab.id });
  } catch (err) {
    // 対象タブで side panel を開けない場合はそのまま続行する。
    console.warn("[ikkyoku] sidePanel.open に失敗:", err);
  }

  chrome.runtime.sendMessage({ type: "run-snapshot-request", tabId: tab.id, windowId: tab.windowId }).catch(() => {
    // side panel が開いていないと受信者不在エラーになるが無視してよい。
    // その場合は pendingSnapshot 経由で拾われる。
  });
});

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (!message || typeof message.type !== "string") return undefined;

  switch (message.type) {
    case "net-event": {
      // bridge.js（content script）からの通信傍受イベント。sender.tab で発生元タブが分かる。
      const tabId = sender.tab && sender.tab.id;
      if (tabId === undefined) return undefined;

      if (!netLogs.has(tabId)) netLogs.set(tabId, []);
      const log = netLogs.get(tabId);
      log.push(message.payload);
      if (log.length > MAX_LOG_PER_TAB) log.shift();

      // side panel が開いていればリアルタイムに転送する（開いていなければ黙って失敗）。
      chrome.runtime.sendMessage({ type: "net-event-broadcast", tabId, payload: message.payload }).catch(() => {});
      return undefined;
    }

    case "get-net-log": {
      const log = netLogs.get(message.tabId) || [];
      sendResponse({ log });
      return true;
    }

    case "consume-pending-snapshot": {
      // side panel の初期化時に一度だけ呼ばれる。取り出したら消す（引き継ぐのは一度きり）。
      // 古い依頼を後から実行してしまわないよう 10 秒で失効させる。
      const req = pendingSnapshot && Date.now() - pendingSnapshot.time < 10_000 ? pendingSnapshot : null;
      pendingSnapshot = null;
      sendResponse({ request: req });
      return true;
    }

    case "clear-net-log": {
      netLogs.delete(message.tabId);
      sendResponse({ ok: true });
      return true;
    }

    case "capture-visible-tab": {
      // captureVisibleTab は service worker からのみ呼び出す。
      chrome.tabs
        .captureVisibleTab(message.windowId, { format: "png" })
        .then((dataUrl) => sendResponse({ ok: true, dataUrl }))
        .catch((err) => sendResponse({ ok: false, error: String((err && err.message) || err) }));
      return true; // 非同期で sendResponse するので true を返す
    }

    default:
      return undefined;
  }
});
