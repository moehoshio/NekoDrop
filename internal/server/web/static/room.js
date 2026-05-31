// Room page: live message stream, text sending, and file sharing.
(function () {
  "use strict";

  const roomKey = decodeURIComponent(
    window.location.pathname.replace(/^\/r\//, "")
  );

  const els = {
    roomName: document.getElementById("room-name"),
    messages: document.getElementById("messages"),
    empty: document.getElementById("empty"),
    statusDot: document.getElementById("status-dot"),
    statusText: document.getElementById("status-text"),
    composer: document.getElementById("composer"),
    textInput: document.getElementById("text-input"),
    fileInput: document.getElementById("file-input"),
    uploadBar: document.getElementById("upload-bar"),
    uploadText: document.getElementById("upload-text"),
  };

  els.roomName.textContent = roomKey;
  document.title = "NekoDrop · " + roomKey;

  let sender = "anonymous";
  try {
    const saved = localStorage.getItem("nekodrop.name");
    if (saved) sender = saved;
  } catch (e) {
    /* ignore storage errors */
  }

  const seen = new Set();

  function setStatus(online, text) {
    els.statusDot.classList.toggle("online", online);
    els.statusDot.classList.toggle("offline", !online);
    els.statusText.textContent = text;
  }

  function formatSize(bytes) {
    if (!bytes) return "0 B";
    const units = ["B", "KB", "MB", "GB"];
    let i = 0;
    let n = bytes;
    while (n >= 1024 && i < units.length - 1) {
      n /= 1024;
      i++;
    }
    return (i === 0 ? n : n.toFixed(1)) + " " + units[i];
  }

  function formatTime(iso) {
    const d = new Date(iso);
    if (isNaN(d.getTime())) return "";
    return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }

  function render(msg) {
    if (!msg || !msg.id || seen.has(msg.id)) return;
    seen.add(msg.id);
    els.empty.hidden = true;

    const item = document.createElement("div");
    item.className = "message";

    const meta = document.createElement("div");
    meta.className = "meta";
    const who = document.createElement("span");
    who.className = "who";
    who.textContent = msg.sender || "anonymous";
    const when = document.createElement("span");
    when.className = "when";
    when.textContent = formatTime(msg.time);
    meta.appendChild(who);
    meta.appendChild(when);
    item.appendChild(meta);

    if (msg.kind === "file") {
      const link = document.createElement("a");
      link.className = "file";
      link.href =
        "/api/files/" +
        encodeURIComponent(roomKey) +
        "/" +
        encodeURIComponent(msg.fileId);
      // textContent only: never inject untrusted strings as HTML.
      link.textContent = "⬇ " + (msg.fileName || "file");
      link.setAttribute("download", msg.fileName || "file");
      const size = document.createElement("span");
      size.className = "size";
      size.textContent = " (" + formatSize(msg.fileSize) + ")";
      const body = document.createElement("div");
      body.className = "body";
      body.appendChild(link);
      body.appendChild(size);
      item.appendChild(body);
    } else {
      const body = document.createElement("div");
      body.className = "body text";
      body.textContent = msg.text || "";
      item.appendChild(body);
    }

    const atBottom =
      els.messages.scrollHeight - els.messages.scrollTop - els.messages.clientHeight < 80;
    els.messages.appendChild(item);
    if (atBottom) els.messages.scrollTop = els.messages.scrollHeight;
  }

  function connect() {
    const source = new EventSource(
      "/api/stream/" + encodeURIComponent(roomKey)
    );
    source.onopen = function () {
      setStatus(true, "connected");
    };
    source.onmessage = function (e) {
      try {
        render(JSON.parse(e.data));
      } catch (err) {
        /* ignore malformed events */
      }
    };
    source.onerror = function () {
      setStatus(false, "reconnecting…");
      // EventSource reconnects automatically.
    };
  }

  async function sendText(text) {
    const res = await fetch("/api/messages/" + encodeURIComponent(roomKey), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sender: sender, text: text }),
    });
    if (!res.ok) throw new Error("send failed: " + res.status);
  }

  async function uploadFile(file) {
    els.uploadBar.hidden = false;
    els.uploadText.textContent = "Uploading " + file.name + "…";
    try {
      const data = new FormData();
      data.append("sender", sender);
      data.append("file", file);
      const res = await fetch("/api/files/" + encodeURIComponent(roomKey), {
        method: "POST",
        body: data,
      });
      if (!res.ok) {
        const msg = await res.text();
        throw new Error(msg || "upload failed");
      }
      els.uploadText.textContent = "Shared " + file.name;
    } catch (err) {
      els.uploadText.textContent = "Upload failed: " + err.message;
    } finally {
      setTimeout(function () {
        els.uploadBar.hidden = true;
      }, 2500);
    }
  }

  els.composer.addEventListener("submit", function (e) {
    e.preventDefault();
    const text = els.textInput.value.trim();
    if (!text) return;
    els.textInput.value = "";
    sendText(text).catch(function () {
      els.textInput.value = text;
      setStatus(false, "send failed");
    });
  });

  els.fileInput.addEventListener("change", function () {
    const file = els.fileInput.files && els.fileInput.files[0];
    if (file) uploadFile(file);
    els.fileInput.value = "";
  });

  connect();
})();
