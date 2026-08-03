// MAIN world で動く通信フック。
//
// ページ本来の JS コンテキスト（isolated world ではない）で動かす必要があるため、
// manifest.json の content_scripts で "world": "MAIN" を指定している。
// isolated world からは window.WebSocket 等を差し替えてもページ側には効かない。
//
// ここでは診断のため WebSocket / fetch / XMLHttpRequest を素通しラップし、
// 流れた内容を window.postMessage で isolated world 側（bridge.js）に橋渡しする。
// MAIN world から chrome.runtime には直接アクセスできないための中継。
//
// 盤面かどうかの判定はしない。"盤面っぽいキーが含まれているか" の粗いヒントを
// 付けるだけで、最終判断は人間（side panel の表示を見て判断）に委ねる。

(() => {
  const SOURCE = "ikkyoku-net-sniff";
  const MAX_BODY_LEN = 2000;

  function post(record) {
    try {
      window.postMessage({ source: SOURCE, record }, "*");
    } catch (e) {
      // postMessage 自体の失敗は診断に影響しないので無視する。
    }
  }

  function truncate(text) {
    if (typeof text !== "string") return text;
    return text.length > MAX_BODY_LEN ? `${text.slice(0, MAX_BODY_LEN)}...(truncated)` : text;
  }

  function looksLikeBoardJson(text) {
    if (typeof text !== "string") return false;
    // 盤面情報っぽいキーが含まれるかどうかの粗い判定（誤検出があってよい診断用ヒント）。
    return /"(sfen|board|kifu|shogi|piece|teban|banmen|moves?)"/i.test(text);
  }

  // ---------- WebSocket ----------
  const NativeWebSocket = window.WebSocket;
  if (NativeWebSocket) {
    function PatchedWebSocket(url, protocols) {
      const ws = protocols === undefined ? new NativeWebSocket(url) : new NativeWebSocket(url, protocols);

      post({ kind: "websocket-open", url: String(url), time: Date.now() });

      ws.addEventListener("message", (ev) => {
        const isText = typeof ev.data === "string";
        const body = isText ? ev.data : `[binary ${(ev.data && (ev.data.byteLength ?? ev.data.size)) || "?"} bytes]`;
        post({
          kind: "websocket-message",
          url: String(url),
          time: Date.now(),
          body: truncate(body),
          looksLikeBoard: isText && looksLikeBoardJson(body),
        });
      });

      const nativeSend = ws.send.bind(ws);
      ws.send = function (data) {
        const isText = typeof data === "string";
        const body = isText ? data : `[binary ${(data && (data.byteLength ?? data.size)) || "?"} bytes]`;
        post({
          kind: "websocket-send",
          url: String(url),
          time: Date.now(),
          body: truncate(body),
          looksLikeBoard: isText && looksLikeBoardJson(body),
        });
        return nativeSend(data);
      };

      return ws;
    }
    PatchedWebSocket.prototype = NativeWebSocket.prototype;
    Object.setPrototypeOf(PatchedWebSocket, NativeWebSocket);
    for (const k of ["CONNECTING", "OPEN", "CLOSING", "CLOSED"]) {
      PatchedWebSocket[k] = NativeWebSocket[k];
    }
    window.WebSocket = PatchedWebSocket;
  }

  // ---------- fetch ----------
  const nativeFetch = window.fetch;
  if (nativeFetch) {
    window.fetch = async function (...args) {
      const res = await nativeFetch.apply(this, args);
      try {
        const first = args[0];
        const url = typeof first === "string" ? first : (first && first.url) || "";
        const ct = res.headers.get("content-type") || "";
        if (ct.includes("json") || ct.includes("text")) {
          res
            .clone()
            .text()
            .then((body) => {
              post({
                kind: "fetch",
                url: String(url),
                time: Date.now(),
                status: res.status,
                contentType: ct,
                body: truncate(body),
                looksLikeBoard: looksLikeBoardJson(body),
              });
            })
            .catch(() => {});
        } else {
          post({ kind: "fetch", url: String(url), time: Date.now(), status: res.status, contentType: ct, body: null, looksLikeBoard: false });
        }
      } catch (e) {
        // 傍受の失敗で本来の通信を壊さないよう握りつぶす。
      }
      return res;
    };
  }

  // ---------- XMLHttpRequest ----------
  const nativeOpen = XMLHttpRequest.prototype.open;
  const nativeSend2 = XMLHttpRequest.prototype.send;

  XMLHttpRequest.prototype.open = function (method, url, ...rest) {
    this.__ikkyokuUrl = url;
    return nativeOpen.call(this, method, url, ...rest);
  };

  XMLHttpRequest.prototype.send = function (...args) {
    this.addEventListener("loadend", () => {
      try {
        const ct = (this.getResponseHeader && this.getResponseHeader("content-type")) || "";
        let body = null;
        try {
          if (typeof this.responseText === "string") body = this.responseText;
        } catch (e) {
          body = "[non-text response]";
        }
        post({
          kind: "xhr",
          url: String(this.__ikkyokuUrl || ""),
          time: Date.now(),
          status: this.status,
          contentType: ct,
          body: truncate(body),
          looksLikeBoard: looksLikeBoardJson(body),
        });
      } catch (e) {
        // 無視
      }
    });
    return nativeSend2.apply(this, args);
  };
})();
