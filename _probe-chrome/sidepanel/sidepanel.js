// side panel のロジック。
//
// このファイルは拡張の特権コンテキスト（side panel ページ）で動く。
// 通信傍受ログの表示、DOM 走査 / タブキャプチャ / video キャプチャの実行結果表示をまとめて担う。
// 盤面認識・SFEN 変換などのロジックはここには置かない（診断結果を人間が読むだけ）。
//
// 「履歴に依存しない」原則: このページが保持する状態（ネットログ・直近の結果）は
// あくまで表示用のキャッシュであり、閉じれば消えてよいものに限定している。

let currentTab = null; // { id, windowId, url }

const netLogEl = document.getElementById("net-log");
const netCountEl = document.getElementById("net-count");
const netOnlyBoardEl = document.getElementById("net-only-board");
const targetTabEl = document.getElementById("target-tab");

let netEntries = [];

// ---------- 対象タブの把握 ----------
async function refreshCurrentTab() {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab) {
    currentTab = null;
    targetTabEl.textContent = "対象タブ: 見つかりません";
    return;
  }
  currentTab = { id: tab.id, windowId: tab.windowId, url: tab.url || "" };

  const isAbema = /^https:\/\/abema\.tv\//.test(currentTab.url);
  targetTabEl.textContent = `対象タブ: ${currentTab.url}${isAbema ? "" : "（ABEMA以外。content script は動きません）"}`;

  await loadNetLog();
}

chrome.tabs.onActivated.addListener(() => {
  refreshCurrentTab();
});
chrome.tabs.onUpdated.addListener((tabId, info) => {
  if (currentTab && tabId === currentTab.id && info.status === "loading") {
    // ページ遷移したら通信ログは新しい文脈のものになるので UI 側もクリアする。
    netEntries = [];
    renderNetLog();
  }
});

// ---------- 1. 通信の傍受 ----------
async function loadNetLog() {
  if (!currentTab) return;
  const res = await chrome.runtime.sendMessage({ type: "get-net-log", tabId: currentTab.id }).catch(() => null);
  netEntries = (res && res.log) || [];
  renderNetLog();
}

function formatEntry(record) {
  const time = new Date(record.time).toLocaleTimeString("ja-JP", { hour12: false });
  const head = `[${time}] ${record.kind} ${record.status ?? ""} ${record.url}`;
  const body = record.body ? record.body : "";
  return { head, body };
}

function renderNetLog() {
  const onlyBoard = netOnlyBoardEl.checked;
  const list = onlyBoard ? netEntries.filter((e) => e.looksLikeBoard) : netEntries;

  netCountEl.textContent = `${list.length} / ${netEntries.length} 件`;
  netLogEl.innerHTML = "";

  // 新しいものを上に。
  for (let i = list.length - 1; i >= 0; i--) {
    const record = list[i];
    const { head, body } = formatEntry(record);
    const div = document.createElement("div");
    div.className = "log-entry" + (record.looksLikeBoard ? " board-hit" : "");
    const meta = document.createElement("div");
    meta.className = "meta";
    meta.textContent = head + (record.looksLikeBoard ? "  ← 盤面っぽいキーを含む" : "");
    div.appendChild(meta);
    if (body) {
      const bodyEl = document.createElement("div");
      bodyEl.className = "body";
      bodyEl.textContent = body;
      div.appendChild(bodyEl);
    }
    netLogEl.appendChild(div);
  }
}

document.getElementById("net-clear").addEventListener("click", async () => {
  if (!currentTab) return;
  await chrome.runtime.sendMessage({ type: "clear-net-log", tabId: currentTab.id }).catch(() => {});
  netEntries = [];
  renderNetLog();
});
netOnlyBoardEl.addEventListener("change", renderNetLog);

// background からのリアルタイム転送を受け取る。
chrome.runtime.onMessage.addListener((message) => {
  if (!message || typeof message.type !== "string") return;

  if (message.type === "net-event-broadcast") {
    if (!currentTab || message.tabId !== currentTab.id) return;
    netEntries.push(message.payload);
    if (netEntries.length > 300) netEntries.shift();
    renderNetLog();
    return;
  }

  if (message.type === "run-snapshot-request") {
    // ホットキー経由。対象タブを合わせてから全診断を実行する。
    refreshCurrentTab().then(runAll);
  }
});

// ---------- 2. DOM の走査 ----------
const domResultEl = document.getElementById("dom-result");

function renderDomResult(result) {
  if (!result) {
    domResultEl.innerHTML = `<p class="ng">DOM 走査に失敗しました（content script が読み込まれていない可能性）。</p>`;
    return;
  }
  const lines = [];
  lines.push(`<canvas> 要素: <b>${result.canvasCount}</b> 個`);
  lines.push(`<video> 要素: <b>${result.videoCount}</b> 個`);
  lines.push(`将棋関連キーワードを含む要素（候補・粗い判定）: <b>${result.candidates.length}</b> 個`);
  domResultEl.innerHTML = `<p>${lines.join("<br/>")}</p><pre>${escapeHtml(JSON.stringify(result, null, 2))}</pre>`;
}

async function runDomScan() {
  if (!currentTab) {
    renderDomResult(null);
    return;
  }
  domResultEl.innerHTML = "<p>調べています…</p>";
  const result = await chrome.tabs.sendMessage(currentTab.id, { type: "scan-dom" }).catch(() => null);
  renderDomResult(result);
}
document.getElementById("dom-scan").addEventListener("click", runDomScan);

// ---------- 3. タブのキャプチャ ----------
const tabCaptureResultEl = document.getElementById("tab-capture-result");

function analyzeDataUrlPixels(dataUrl) {
  return new Promise((resolve, reject) => {
    const img = new Image();
    img.onload = () => {
      const canvas = document.createElement("canvas");
      canvas.width = img.naturalWidth;
      canvas.height = img.naturalHeight;
      const ctx = canvas.getContext("2d");
      ctx.drawImage(img, 0, 0);
      try {
        const imageData = ctx.getImageData(0, 0, canvas.width, canvas.height);
        const data = imageData.data;
        const n = data.length / 4;
        let sum = 0;
        for (let i = 0; i < data.length; i += 4) sum += (data[i] + data[i + 1] + data[i + 2]) / 3;
        const mean = sum / n;
        let variance = 0;
        for (let i = 0; i < data.length; i += 4) {
          const lum = (data[i] + data[i + 1] + data[i + 2]) / 3;
          variance += (lum - mean) * (lum - mean);
        }
        variance /= n;
        resolve({ mean, variance, width: canvas.width, height: canvas.height });
      } catch (e) {
        reject(e);
      }
    };
    img.onerror = reject;
    img.src = dataUrl;
  });
}

function makeDownloadLink(dataUrl, filename) {
  const a = document.createElement("a");
  a.className = "download";
  a.href = dataUrl;
  a.download = filename;
  a.textContent = `PNG をダウンロード（${filename}）`;
  return a;
}

async function runTabCapture() {
  if (!currentTab) {
    tabCaptureResultEl.innerHTML = `<p class="ng">対象タブがありません。</p>`;
    return;
  }
  tabCaptureResultEl.innerHTML = "<p>キャプチャしています…</p>";

  const res = await chrome.runtime
    .sendMessage({ type: "capture-visible-tab", windowId: currentTab.windowId })
    .catch((err) => ({ ok: false, error: String(err) }));

  if (!res || !res.ok) {
    tabCaptureResultEl.innerHTML = `<p class="ng">失敗: ${escapeHtml((res && res.error) || "不明なエラー")}</p>`;
    return;
  }

  let analysis = null;
  try {
    analysis = await analyzeDataUrlPixels(res.dataUrl);
  } catch (e) {
    // 分析に失敗しても画像自体は表示する。
  }

  tabCaptureResultEl.innerHTML = "";
  const p = document.createElement("p");
  if (analysis) {
    const isBlack = analysis.mean < 5 && analysis.variance < 5;
    p.innerHTML = isBlack
      ? `<span class="ng">ほぼ真っ黒（平均輝度 ${analysis.mean.toFixed(1)} / 分散 ${analysis.variance.toFixed(1)}）。DRM で保護されている可能性があります。</span>`
      : `<span class="ok">映像が取れています（平均輝度 ${analysis.mean.toFixed(1)} / 分散 ${analysis.variance.toFixed(1)}）。</span>`;
  } else {
    p.textContent = "画素の分析に失敗しました（画像は表示できています）。";
  }
  tabCaptureResultEl.appendChild(p);

  const img = document.createElement("img");
  img.src = res.dataUrl;
  tabCaptureResultEl.appendChild(img);

  tabCaptureResultEl.appendChild(document.createElement("br"));
  tabCaptureResultEl.appendChild(makeDownloadLink(res.dataUrl, `ikkyoku-tabcapture-${Date.now()}.png`));
}
document.getElementById("tab-capture").addEventListener("click", runTabCapture);

// ---------- 4. video 要素のキャプチャ ----------
const videoCaptureResultEl = document.getElementById("video-capture-result");

async function runVideoCapture() {
  if (!currentTab) {
    videoCaptureResultEl.innerHTML = `<p class="ng">対象タブがありません。</p>`;
    return;
  }
  videoCaptureResultEl.innerHTML = "<p>キャプチャしています…</p>";

  const res = await chrome.tabs.sendMessage(currentTab.id, { type: "capture-video" }).catch(() => null);
  if (!res || !res.results) {
    videoCaptureResultEl.innerHTML = `<p class="ng">失敗しました（content script が読み込まれていない可能性）。</p>`;
    return;
  }

  videoCaptureResultEl.innerHTML = "";
  res.results.forEach((r, i) => {
    const block = document.createElement("div");
    block.className = "video-block";

    if (!r.ok) {
      block.innerHTML = `<p class="ng">video[${r.index ?? i}]: 失敗（${escapeHtml(r.reason || "")}）<br/>${escapeHtml(r.message || "")}</p>`;
      videoCaptureResultEl.appendChild(block);
      return;
    }

    const p = document.createElement("p");
    p.innerHTML = r.isBlack
      ? `<span class="ng">video[${r.index}]: ほぼ真っ黒（平均輝度 ${r.mean.toFixed(1)} / 分散 ${r.variance.toFixed(1)}）。${escapeHtml(r.judgement)}</span>`
      : `<span class="ok">video[${r.index}]: ${escapeHtml(r.judgement)}（平均輝度 ${r.mean.toFixed(1)} / 分散 ${r.variance.toFixed(1)}）</span>`;
    block.appendChild(p);

    if (r.dataUrl) {
      const img = document.createElement("img");
      img.src = r.dataUrl;
      block.appendChild(img);
      block.appendChild(document.createElement("br"));
      block.appendChild(makeDownloadLink(r.dataUrl, `ikkyoku-videocapture-${r.index}-${Date.now()}.png`));
    }

    videoCaptureResultEl.appendChild(block);
  });
}
document.getElementById("video-capture").addEventListener("click", runVideoCapture);

// ---------- 全診断まとめて実行 ----------
async function runAll() {
  await loadNetLog();
  await runDomScan();
  await runTabCapture();
  await runVideoCapture();
}
document.getElementById("run-all").addEventListener("click", runAll);

// ---------- ユーティリティ ----------
function escapeHtml(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

// 初期化
(async () => {
  await refreshCurrentTab();

  // ホットキーで「開くと同時に実行」された場合、上の onMessage リスナー登録前に
  // 依頼が飛んでいて取りこぼしている。background に預けられた依頼を引き取る。
  const res = await chrome.runtime.sendMessage({ type: "consume-pending-snapshot" }).catch(() => null);
  if (res && res.request) runAll();
})();
