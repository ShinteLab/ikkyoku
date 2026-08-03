// isolated world で動く content script（既定の world）。
//
// 役割は2つ。
//   1. MAIN world（main-world-hook.js）から window.postMessage で届く通信傍受イベントを
//      chrome.runtime.sendMessage で service worker（background.js）へ中継する
//   2. side panel からの要求に応じて DOM 走査 / video 要素のキャプチャを実行する
//
// DOM 要素の取得自体は isolated world でも通常どおり行える（DOM は共有される。
// 共有されないのは JS のオブジェクト・関数・プロトタイプ）。

(() => {
  const SOURCE = "ikkyoku-net-sniff";

  // ---------- 1. MAIN world からの通信傍受イベントの中継 ----------
  window.addEventListener("message", (ev) => {
    if (ev.source !== window) return;
    const data = ev.data;
    if (!data || data.source !== SOURCE || !data.record) return;
    chrome.runtime.sendMessage({ type: "net-event", payload: data.record }).catch(() => {
      // service worker が休止中などで届かなくても診断の継続には影響しない。
    });
  });

  // ---------- 2. DOM 走査 ----------
  function summarizeElement(el) {
    const rect = el.getBoundingClientRect();
    return {
      tag: el.tagName.toLowerCase(),
      id: el.id || null,
      className: (el.className && String(el.className)) || null,
      width: Math.round(rect.width),
      height: Math.round(rect.height),
    };
  }

  function scanDom() {
    const canvases = Array.from(document.querySelectorAll("canvas")).map(summarizeElement);
    const videos = Array.from(document.querySelectorAll("video")).map((v) => ({
      ...summarizeElement(v),
      videoWidth: v.videoWidth,
      videoHeight: v.videoHeight,
      readyState: v.readyState,
      paused: v.paused,
    }));

    // 盤面 / 将棋関連らしい class・id を持つ要素を粗く拾う。
    // これは "怪しい要素の候補提示" であり、確定的な盤面判定ではない。
    const keywordPattern = /(shogi|board|banmen|kifu|goban)/i;
    const candidates = Array.from(document.querySelectorAll("[class],[id]"))
      .filter((el) => keywordPattern.test(el.className ? String(el.className) : "") || keywordPattern.test(el.id || ""))
      .slice(0, 30)
      .map(summarizeElement);

    return { canvasCount: canvases.length, canvases, videoCount: videos.length, videos, candidates, url: location.href };
  }

  // ---------- 3. video 要素のキャプチャ ----------
  function analyzePixels(imageData) {
    const data = imageData.data;
    const n = data.length / 4;
    let sum = 0;
    for (let i = 0; i < data.length; i += 4) {
      // 輝度は簡易的に RGB 平均で近似する（診断用途なので厳密な輝度式は使わない）。
      sum += (data[i] + data[i + 1] + data[i + 2]) / 3;
    }
    const mean = sum / n;

    let variance = 0;
    for (let i = 0; i < data.length; i += 4) {
      const lum = (data[i] + data[i + 1] + data[i + 2]) / 3;
      variance += (lum - mean) * (lum - mean);
    }
    variance /= n;

    return { mean, variance };
  }

  function captureVideos() {
    const videos = Array.from(document.querySelectorAll("video"));
    if (videos.length === 0) {
      return [{ ok: false, reason: "no-video", message: "ページ内に <video> 要素が見つかりません。" }];
    }

    return videos.map((video, index) => {
      const info = {
        index,
        videoWidth: video.videoWidth,
        videoHeight: video.videoHeight,
        readyState: video.readyState,
        paused: video.paused,
      };

      if (!video.videoWidth || !video.videoHeight) {
        return { ...info, ok: false, reason: "no-dimensions", message: "video の実寸が 0 です。まだ再生されていない可能性があります。" };
      }

      const canvas = document.createElement("canvas");
      canvas.width = video.videoWidth;
      canvas.height = video.videoHeight;
      const ctx = canvas.getContext("2d");

      try {
        ctx.drawImage(video, 0, 0, canvas.width, canvas.height);
      } catch (e) {
        return { ...info, ok: false, reason: "draw-error", message: `drawImage に失敗: ${String(e)}` };
      }

      let imageData;
      try {
        imageData = ctx.getImageData(0, 0, canvas.width, canvas.height);
      } catch (e) {
        // DRM (Widevine/EME) 保護されたコンテンツを描画すると canvas が "汚染" され、
        // getImageData / toDataURL が SecurityError になる。これが Phase 0 で最も知りたい結果の1つ。
        return {
          ...info,
          ok: false,
          reason: "security-error",
          message: "SecurityError: canvas が汚染されています。DRM (Widevine/EME) による保護が原因の可能性が高いです。",
        };
      }

      const { mean, variance } = analyzePixels(imageData);
      const isBlack = mean < 5 && variance < 5;

      let dataUrl = null;
      try {
        dataUrl = canvas.toDataURL("image/png");
      } catch (e) {
        return {
          ...info,
          ok: false,
          reason: "security-error",
          message: "toDataURL が SecurityError になりました。DRM により canvas が汚染されています。",
        };
      }

      return {
        ...info,
        ok: true,
        mean,
        variance,
        isBlack,
        judgement: isBlack
          ? "ほぼ真っ黒（平均輝度・分散とも極めて低い）。DRM (Widevine/EME) で保護され黒フレームになっている可能性が高い。"
          : "映像らしきピクセルが取得できている（DRM で保護されていないか、保護が効いていない可能性）。",
        dataUrl,
      };
    });
  }

  chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
    if (!message || typeof message.type !== "string") return undefined;

    if (message.type === "scan-dom") {
      sendResponse(scanDom());
      return true;
    }

    if (message.type === "capture-video") {
      sendResponse({ results: captureVideos() });
      return true;
    }

    return undefined;
  });
})();
