// Channel page: identity, live stream, media previews + lightbox, @-mentions
// with notifications, announcements, presence, and owner/admin controls.
(function () {
  "use strict";

  const t = (k, v) => (window.NekoI18n ? window.NekoI18n.t(k, v) : k);

  const roomKey = decodeURIComponent(window.location.pathname.replace(/^\/r\//, ""));

  const els = {
    roomName: document.getElementById("room-name"),
    roomId: document.getElementById("room-id"),
    roomDesc: document.getElementById("room-desc"),
    roomPrivate: document.getElementById("room-private"),
    online: document.getElementById("online"),
    messages: document.getElementById("messages"),
    empty: document.getElementById("empty"),
    statusDot: document.getElementById("status-dot"),
    statusText: document.getElementById("status-text"),
    announcements: document.getElementById("announcements"),
    notice: document.getElementById("notice"),
    composer: document.getElementById("composer"),
    textInput: document.getElementById("text-input"),
    fileInput: document.getElementById("file-input"),
    uploadBar: document.getElementById("upload-bar"),
    uploadText: document.getElementById("upload-text"),
    joinBtn: document.getElementById("join-btn"),
    settingsBtn: document.getElementById("settings-btn"),
    notifyBtn: document.getElementById("notify-btn"),
    notifyCount: document.getElementById("notify-count"),
    mentionPop: document.getElementById("mention-pop"),
    // attachments
    attachBar: document.getElementById("attach-bar"),
    attachList: document.getElementById("attach-list"),
    attachSend: document.getElementById("attach-send"),
    attachClear: document.getElementById("attach-clear"),
    // drawer
    drawer: document.getElementById("settings-drawer"),
    settingsClose: document.getElementById("settings-close"),
    settingsForm: document.getElementById("settings-form"),
    settingsMsg: document.getElementById("settings-msg"),
    announceForm: document.getElementById("announce-form"),
    announceText: document.getElementById("announce-text"),
    pendingSection: document.getElementById("pending-section"),
    pendingList: document.getElementById("pending-list"),
    memberList: document.getElementById("member-list"),
    dangerSection: document.getElementById("danger-section"),
    dissolveBtn: document.getElementById("dissolve-btn"),
    // lightbox
    lightbox: document.getElementById("lightbox"),
    lightboxClose: document.getElementById("lightbox-close"),
    lightboxContent: document.getElementById("lightbox-content"),
  };

  document.title = "NekoDrop · " + roomKey;

  let me = { uid: "", name: "anonymous" };
  let channel = null;
  let role = null;
  let historyLoaded = false;
  const seen = new Set();
  const participants = new Map(); // uid -> name
  let roster = []; // authoritative member list from the server (admin view)
  const mentionHits = []; // DOM nodes mentioning me, for the bell
  let unread = 0;

  const MENTION_RE = /@[^\s#@]+#(\d+)/g;
  const URL_RE = /(https?:\/\/[^\s]+)/g;

  // ---------- helpers ----------
  function api(path, opts) {
    return fetch(path, Object.assign({ headers: {} }, opts));
  }

  function setStatus(online, text) {
    els.statusDot.classList.toggle("online", online);
    els.statusDot.classList.toggle("offline", !online);
    els.statusText.textContent = text;
  }

  function formatSize(bytes) {
    if (!bytes) return "0 B";
    const units = ["B", "KB", "MB", "GB"];
    let i = 0, n = bytes;
    while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
    return (i === 0 ? n : n.toFixed(1)) + " " + units[i];
  }

  function formatTime(iso) {
    const d = new Date(iso);
    if (isNaN(d.getTime())) return "";
    return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }

  function fileURL(fileId, inline) {
    return "/api/files/" + encodeURIComponent(roomKey) + "/" +
      encodeURIComponent(fileId) + (inline ? "?inline=1" : "");
  }

  function remember(uid, name) {
    if (uid && name) participants.set(uid, name);
  }

  // ---------- lightbox ----------
  function showLightbox(node) {
    els.lightboxContent.innerHTML = "";
    els.lightboxContent.appendChild(node);
    els.lightbox.hidden = false;
  }
  function openLightbox(kind, url) {
    let node;
    if (kind === "video") {
      node = document.createElement("video");
      node.src = url;
      node.controls = true;
      node.autoplay = true;
    } else {
      node = document.createElement("img");
      node.src = url;
      node.alt = "preview";
    }
    showLightbox(node);
  }
  function closeLightbox() {
    els.lightbox.hidden = true;
    els.lightboxContent.innerHTML = "";
  }
  els.lightboxClose.addEventListener("click", closeLightbox);
  els.lightbox.addEventListener("click", function (e) {
    if (e.target === els.lightbox) closeLightbox();
  });
  document.addEventListener("keydown", function (e) {
    if (e.key !== "Escape") return;
    if (!els.lightbox.hidden) { closeLightbox(); }
    else if (!els.drawer.hidden) { closeDrawer(); }
  });

  // ---------- rendering ----------
  // Render text with @mentions highlighted; returns true if it mentions me.
  function renderText(container, text) {
    let mentionsMe = false;
    let last = 0;
    MENTION_RE.lastIndex = 0;
    let m;
    while ((m = MENTION_RE.exec(text)) !== null) {
      if (m.index > last) {
        container.appendChild(document.createTextNode(text.slice(last, m.index)));
      }
      const span = document.createElement("span");
      span.className = "mention";
      span.textContent = m[0];
      if (m[1] === me.uid) {
        span.classList.add("mention-self");
        mentionsMe = true;
      }
      container.appendChild(span);
      last = m.index + m[0].length;
    }
    if (last < text.length) {
      container.appendChild(document.createTextNode(text.slice(last)));
    }
    return mentionsMe;
  }

  function mediaKind(ctype, name) {
    const t = (ctype || "").toLowerCase();
    if (t.indexOf("image/") === 0) return "image";
    if (t.indexOf("video/") === 0) return "video";
    if (t.indexOf("audio/") === 0) return "audio";
    const ext = (name || "").toLowerCase().split(".").pop();
    if (["png", "jpg", "jpeg", "gif", "webp", "bmp", "svg"].includes(ext)) return "image";
    if (["mp4", "webm", "ogg", "mov", "mkv"].includes(ext)) return "video";
    if (["mp3", "wav", "oga", "m4a", "flac"].includes(ext)) return "audio";
    return "";
  }

  function urlMediaKind(url) {
    const clean = url.split("?")[0].toLowerCase();
    const ext = clean.split(".").pop();
    if (["png", "jpg", "jpeg", "gif", "webp", "bmp"].includes(ext)) return "image";
    if (["mp4", "webm", "mov"].includes(ext)) return "video";
    return "";
  }

  function appendMediaPreview(body, kind, url) {
    if (kind === "image") {
      const img = document.createElement("img");
      img.className = "media-preview";
      img.loading = "lazy";
      img.src = url;
      img.addEventListener("click", () => openLightbox("image", url));
      body.appendChild(img);
    } else if (kind === "video") {
      const v = document.createElement("video");
      v.className = "media-preview";
      v.src = url;
      v.controls = true;
      body.appendChild(v);
    } else if (kind === "audio") {
      const a = document.createElement("audio");
      a.className = "media-audio";
      a.src = url;
      a.controls = true;
      body.appendChild(a);
    }
  }

  function render(msg) {
    if (!msg || !msg.id || seen.has(msg.id)) return;
    seen.add(msg.id);
    els.empty.hidden = true;
    remember(msg.senderUid, msg.sender);

    const item = document.createElement("div");
    item.className = "message";
    item.dataset.id = msg.id;

    const meta = document.createElement("div");
    meta.className = "meta";
    const who = document.createElement("button");
    who.type = "button";
    who.className = "who";
    who.textContent = (msg.sender || "anonymous") + (msg.senderUid ? "#" + msg.senderUid : "");
    who.title = "Mention or moderate";
    who.addEventListener("click", () => onAuthorClick(msg.senderUid, msg.sender));
    const when = document.createElement("span");
    when.className = "when";
    when.textContent = formatTime(msg.time);
    meta.appendChild(who);
    meta.appendChild(when);
    item.appendChild(meta);

    const body = document.createElement("div");
    body.className = "body";

    // A message may carry text, a file, or both: a file sent with a describing
    // caption is a single message that renders the caption alongside the file.
    let mentionsMe = false;
    if (msg.text) {
      const textWrap = document.createElement("div");
      textWrap.className = "text";
      mentionsMe = renderText(textWrap, msg.text);
      body.appendChild(textWrap);
    }

    if (msg.kind === "file") {
      const kind = mediaKind(msg.fileType, msg.fileName);
      if (kind) appendMediaPreview(body, kind, fileURL(msg.fileId, true));
      const link = document.createElement("a");
      link.className = "file";
      link.href = fileURL(msg.fileId, false);
      link.textContent = "⬇ " + (msg.fileName || "file");
      link.setAttribute("download", msg.fileName || "file");
      const size = document.createElement("span");
      size.className = "size";
      size.textContent = " (" + formatSize(msg.fileSize) + ")";
      const row = document.createElement("div");
      row.className = "file-row";
      row.appendChild(link);
      row.appendChild(size);
      body.appendChild(row);
    }

    // Opt-in inline previews of links/media found in the text (sender's choice).
    if (msg.preview && msg.text) {
      URL_RE.lastIndex = 0;
      let u;
      while ((u = URL_RE.exec(msg.text)) !== null) {
        const kind = urlMediaKind(u[1]);
        if (kind) appendMediaPreview(body, kind, u[1]);
      }
    }

    if (mentionsMe) {
      item.classList.add("mentions-me");
      if (historyLoaded) notifyMention(item);
    }

    item.appendChild(body);

    const atBottom =
      els.messages.scrollHeight - els.messages.scrollTop - els.messages.clientHeight < 80;
    els.messages.appendChild(item);
    if (atBottom) els.messages.scrollTop = els.messages.scrollHeight;
  }

  function renderAnnouncement(a) {
    if (!a || !a.id) return;
    if (els.announcements.querySelector('[data-ann="' + a.id + '"]')) return;
    els.announcements.hidden = false;
    const box = document.createElement("div");
    box.className = "announcement";
    box.dataset.ann = a.id;
    const head = document.createElement("div");
    head.className = "ann-head";
    head.textContent = "📢 " + (a.authorName || "admin") + (a.authorUid ? "#" + a.authorUid : "");
    const body = document.createElement("div");
    body.className = "ann-body";
    body.textContent = a.text;
    box.appendChild(head);
    box.appendChild(body);
    els.announcements.appendChild(box);
  }

  // ---------- mentions / notifications ----------
  function notifyMention(item) {
    unread++;
    mentionHits.push(item);
    els.notifyBtn.hidden = false;
    els.notifyCount.textContent = String(unread);
    els.notifyCount.hidden = false;
  }
  els.notifyBtn.addEventListener("click", function () {
    const target = mentionHits[mentionHits.length - 1];
    if (target) {
      target.scrollIntoView({ behavior: "smooth", block: "center" });
      target.classList.add("flash");
      setTimeout(() => target.classList.remove("flash"), 1500);
    }
    unread = 0;
    els.notifyCount.textContent = "0";
    els.notifyCount.hidden = true;
  });

  // ---------- mention picker ----------
  let mentionAnchor = -1;
  function updateMentionPop() {
    const val = els.textInput.value;
    const pos = els.textInput.selectionStart;
    const upto = val.slice(0, pos);
    const at = upto.lastIndexOf("@");
    if (at < 0 || (at > 0 && !/\s/.test(upto[at - 1]))) {
      els.mentionPop.hidden = true;
      mentionAnchor = -1;
      return;
    }
    const query = upto.slice(at + 1).toLowerCase();
    if (/\s/.test(query)) { els.mentionPop.hidden = true; return; }
    const matches = [];
    participants.forEach((name, uid) => {
      if (uid === me.uid) return;
      if (!query || name.toLowerCase().includes(query) || uid.includes(query)) {
        matches.push({ uid, name });
      }
    });
    if (matches.length === 0) { els.mentionPop.hidden = true; return; }
    mentionAnchor = at;
    els.mentionPop.innerHTML = "";
    matches.slice(0, 6).forEach((p) => {
      const li = document.createElement("li");
      li.textContent = p.name + "#" + p.uid;
      li.addEventListener("mousedown", function (e) {
        e.preventDefault();
        insertMention(p);
      });
      els.mentionPop.appendChild(li);
    });
    els.mentionPop.hidden = false;
  }
  function insertMention(p) {
    const val = els.textInput.value;
    const pos = els.textInput.selectionStart;
    const before = val.slice(0, mentionAnchor);
    const after = val.slice(pos);
    const token = "@" + p.name + "#" + p.uid + " ";
    els.textInput.value = before + token + after;
    const caret = (before + token).length;
    els.textInput.setSelectionRange(caret, caret);
    els.mentionPop.hidden = true;
    els.textInput.focus();
  }
  els.textInput.addEventListener("input", updateMentionPop);
  els.textInput.addEventListener("keyup", function (e) {
    if (["ArrowLeft", "ArrowRight", "Home", "End"].includes(e.key)) updateMentionPop();
  });
  els.textInput.addEventListener("blur", function () {
    setTimeout(() => { els.mentionPop.hidden = true; }, 150);
  });

  // Click a chat author to mention them (and, for admins, offer moderation).
  function onAuthorClick(uid, name) {
    if (!uid || uid === me.uid) return;
    remember(uid, name);
    const token = "@" + name + "#" + uid + " ";
    els.textInput.value += (els.textInput.value && !/\s$/.test(els.textInput.value) ? " " : "") + token;
    els.textInput.focus();
  }

  // ---------- channel info & roles ----------
  function applyChannel(info) {
    channel = info;
    els.roomName.textContent = info.name || roomKey;
    els.roomId.textContent = info.id ? "#" + info.id : "";
    els.roomDesc.textContent = info.description || "";
    els.roomPrivate.hidden = info.visibility !== "private";
    els.roomPrivate.textContent = t("badge.private");
    document.title = "NekoDrop · " + (info.name || roomKey);
  }

  function applyRole(r) {
    role = r;
    const isMember = r.member || r.owner;
    els.joinBtn.hidden = isMember;
    els.joinBtn.textContent = r.pending ? t("room.requested") : t("room.join");
    els.joinBtn.disabled = r.pending;
    els.settingsBtn.hidden = !r.admin;
    els.dangerSection.hidden = !r.owner;

    if (r.canSpeak) {
      els.textInput.disabled = false;
      els.fileInput.disabled = false;
      els.textInput.placeholder = t("room.msg_ph");
    } else {
      els.textInput.disabled = true;
      els.fileInput.disabled = true;
      els.textInput.placeholder = isMember ? t("room.send_disabled") : t("room.join_to_send");
    }
  }

  function showNotice(text) {
    els.notice.textContent = text;
    els.notice.hidden = false;
  }

  async function loadChannel() {
    try {
      const res = await api("/api/channels/" + encodeURIComponent(roomKey));
      const view = await res.json();
      applyChannel(view.channel);
      applyRole(view.role);
      if (view.role.uid) me.uid = view.role.uid;
      if (view.members) roster = view.members;
      return view;
    } catch (e) {
      return null;
    }
  }

  // ---------- live stream ----------
  let source = null;
  function connect() {
    source = new EventSource("/api/stream/" + encodeURIComponent(roomKey));
    source.onopen = () => setStatus(true, t("room.connected"));
    source.onmessage = function (e) {
      let ev;
      try { ev = JSON.parse(e.data); } catch (err) { return; }
      switch (ev.type) {
        case "message": render(ev.message); break;
        case "announcement": renderAnnouncement(ev.announcement); break;
        case "presence":
          els.online.textContent = "🟢 " + (ev.online || 0);
          if (!historyLoaded) historyLoaded = true; // history fully replayed
          break;
        case "channel":
          if (ev.channel) { applyChannel(ev.channel); refreshRole(); }
          break;
        case "dissolved":
          setStatus(false, t("room.dissolved_status"));
          showNotice(t("room.dissolved_notice"));
          if (source) source.close();
          els.composer.hidden = true;
          break;
      }
    };
    source.onerror = function () {
      setStatus(false, t("room.reconnecting"));
    };
  }

  async function refreshRole() {
    const view = await loadChannel();
    if (view && view.role.admin) loadDrawerState();
  }

  // ---------- sending ----------
  async function sendText(text) {
    const res = await api("/api/messages/" + encodeURIComponent(roomKey), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      // Links & media are always rendered inline now that the preview toggle is gone.
      body: JSON.stringify({ sender: me.name, text: text, preview: true }),
    });
    if (!res.ok) throw new Error((await res.text()).trim() || "send failed");
  }

  els.composer.addEventListener("submit", function (e) {
    e.preventDefault();
    const text = els.textInput.value.trim();
    els.mentionPop.hidden = true;
    if (pending.length > 0) {
      // Send the file(s) with the typed text as a caption, so a file and its
      // description arrive as a single message.
      els.textInput.value = "";
      sendAttachments(text);
      return;
    }
    if (!text) return;
    els.textInput.value = "";
    sendText(text).catch(function (err) {
      els.textInput.value = text;
      showNotice(err.message);
      setTimeout(() => { els.notice.hidden = true; }, 4000);
    });
  });

  // ---------- attachments (paste / drop / pick, with preview) ----------
  // Files are staged and previewed before sending rather than uploaded the
  // instant they are chosen, so a mis-paste can be removed first.
  let pending = []; // { file, url } entries awaiting send

  function stageFiles(fileList) {
    const files = Array.prototype.slice.call(fileList || []);
    if (files.length === 0) return;
    files.forEach((file) => {
      const isImage = (file.type || "").indexOf("image/") === 0;
      pending.push({ file: file, url: isImage ? URL.createObjectURL(file) : "" });
    });
    renderAttachments();
  }

  function clearAttachments() {
    pending.forEach((p) => { if (p.url) URL.revokeObjectURL(p.url); });
    pending = [];
    renderAttachments();
  }

  function removeAttachment(i) {
    const p = pending[i];
    if (p && p.url) URL.revokeObjectURL(p.url);
    pending.splice(i, 1);
    renderAttachments();
  }

  function renderAttachments() {
    els.attachList.innerHTML = "";
    if (pending.length === 0) {
      els.attachBar.hidden = true;
      return;
    }
    els.attachBar.hidden = false;
    pending.forEach((p, i) => {
      const li = document.createElement("li");
      li.className = "attach-item";

      // Clicking the thumbnail or name previews the file's contents/info before
      // it is sent, so a mis-attached file can be caught early.
      const open = document.createElement("button");
      open.type = "button";
      open.className = "attach-open";
      open.title = t("room.preview");
      if (p.url) {
        const img = document.createElement("img");
        img.className = "attach-thumb";
        img.src = p.url;
        img.alt = p.file.name;
        open.appendChild(img);
      } else {
        const icon = document.createElement("span");
        icon.className = "attach-icon";
        icon.textContent = fileEmoji(p.file);
        open.appendChild(icon);
      }
      const meta = document.createElement("span");
      meta.className = "attach-meta";
      meta.textContent = p.file.name + " (" + formatSize(p.file.size) + ")";
      open.appendChild(meta);
      open.addEventListener("click", () => previewStaged(i));
      li.appendChild(open);

      const rm = document.createElement("button");
      rm.type = "button";
      rm.className = "attach-remove";
      rm.textContent = "✕";
      rm.title = t("room.attach_remove");
      rm.addEventListener("click", () => removeAttachment(i));
      li.appendChild(rm);
      els.attachList.appendChild(li);
    });
  }

  // Pick a representative emoji for a non-image staged file.
  function fileEmoji(file) {
    const kind = mediaKind(file.type, file.name);
    if (kind === "video") return "🎬";
    if (kind === "audio") return "🎵";
    if (isTextFile(file)) return "📝";
    return "📄";
  }

  function isTextFile(file) {
    if ((file.type || "").indexOf("text/") === 0) return true;
    const ext = (file.name || "").toLowerCase().split(".").pop();
    return ["txt", "md", "markdown", "json", "csv", "log", "js", "ts", "css",
      "html", "xml", "yml", "yaml", "ini", "conf", "sh", "py", "go"].includes(ext);
  }

  // Preview a staged file (before sending): media opens in the lightbox; small
  // text files show their contents; anything else shows its name, type and size.
  function previewStaged(i) {
    const p = pending[i];
    if (!p) return;
    const kind = mediaKind(p.file.type, p.file.name);
    if (kind === "image" || kind === "video") {
      const url = p.url || URL.createObjectURL(p.file);
      if (!p.url) p.url = url; // cache so it is revoked on clear/remove
      openLightbox(kind, url);
      return;
    }
    if (isTextFile(p.file) && p.file.size <= 256 * 1024) {
      const reader = new FileReader();
      reader.onload = () => showFileInfo(p.file, String(reader.result || ""));
      reader.onerror = () => showFileInfo(p.file, null);
      reader.readAsText(p.file.slice(0, 256 * 1024));
      return;
    }
    showFileInfo(p.file, null);
  }

  function showFileInfo(file, text) {
    const box = document.createElement("div");
    box.className = "lightbox-info";
    const h = document.createElement("h3");
    h.textContent = file.name;
    box.appendChild(h);
    const meta = document.createElement("p");
    meta.className = "lb-meta";
    meta.textContent = (file.type || "unknown type") + " · " + formatSize(file.size);
    box.appendChild(meta);
    if (text != null) {
      const pre = document.createElement("pre");
      pre.textContent = text;
      box.appendChild(pre);
      if (file.size > 256 * 1024) {
        const note = document.createElement("p");
        note.className = "lb-note";
        note.textContent = "…";
        box.appendChild(note);
      }
    } else {
      const note = document.createElement("p");
      note.className = "lb-note";
      note.textContent = t("room.no_inline_preview");
      box.appendChild(note);
    }
    showLightbox(box);
  }

  async function uploadOne(file, caption) {
    els.uploadBar.hidden = false;
    els.uploadText.textContent = t("room.uploading") + " " + file.name + "…";
    const data = new FormData();
    data.append("sender", me.name);
    data.append("file", file);
    if (caption) {
      data.append("text", caption);
      data.append("preview", "1");
    }
    const res = await api("/api/files/" + encodeURIComponent(roomKey), { method: "POST", body: data });
    if (!res.ok) throw new Error((await res.text()).trim() || t("room.upload_failed"));
    els.uploadText.textContent = t("room.shared") + " " + file.name;
  }

  // Send the staged files. An optional caption is attached to the first file so
  // a file and its description form a single message; remaining files are sent
  // on their own.
  async function sendAttachments(caption) {
    if (pending.length === 0) return;
    const items = pending.slice();
    clearAttachments();
    try {
      for (let i = 0; i < items.length; i++) {
        await uploadOne(items[i].file, i === 0 ? caption : "");
      }
    } catch (err) {
      els.uploadText.textContent = t("room.upload_failed") + ": " + err.message;
    } finally {
      setTimeout(() => { els.uploadBar.hidden = true; }, 2500);
    }
  }

  els.fileInput.addEventListener("change", function () {
    stageFiles(els.fileInput.files);
    els.fileInput.value = "";
  });
  // The standalone "send attachments" button sends files on their own; a caption
  // is only attached when sending through the composer with text typed in.
  els.attachSend.addEventListener("click", function () {
    const caption = els.textInput.value.trim();
    els.textInput.value = "";
    sendAttachments(caption);
  });
  els.attachClear.addEventListener("click", clearAttachments);

  // Paste images/files straight from the clipboard.
  document.addEventListener("paste", function (e) {
    if (els.textInput.disabled) return;
    const items = (e.clipboardData && e.clipboardData.files) || [];
    if (items.length > 0) {
      e.preventDefault();
      stageFiles(items);
    }
  });

  // Drag & drop files anywhere onto the chat.
  ["dragover", "drop"].forEach((type) => {
    document.addEventListener(type, function (e) {
      if (els.textInput.disabled || !e.dataTransfer) return;
      const types = Array.prototype.slice.call(e.dataTransfer.types || []);
      if (e.type === "drop" || types.indexOf("Files") >= 0) {
        e.preventDefault();
        if (e.type === "drop") stageFiles(e.dataTransfer.files);
      }
    });
  });

  // ---------- join ----------
  els.joinBtn.addEventListener("click", async function () {
    els.joinBtn.disabled = true;
    try {
      const res = await api("/api/channels/" + encodeURIComponent(roomKey) + "/join", { method: "POST" });
      if (!res.ok) { showNotice((await res.text()).trim()); els.joinBtn.disabled = false; return; }
      const data = await res.json();
      if (data.pending) {
        els.joinBtn.textContent = t("room.requested");
        showNotice(t("room.join_pending"));
        setTimeout(() => { els.notice.hidden = true; }, 4000);
      } else {
        // Membership may unlock a private stream; reload to reconnect cleanly.
        window.location.reload();
      }
    } catch (e) {
      els.joinBtn.disabled = false;
    }
  });

  // ---------- settings drawer ----------
  function openDrawer() { els.drawer.hidden = false; loadDrawerState(); }
  function closeDrawer() { els.drawer.hidden = true; }
  els.settingsBtn.addEventListener("click", openDrawer);
  els.settingsClose.addEventListener("click", closeDrawer);
  els.drawer.addEventListener("click", function (e) { if (e.target === els.drawer) closeDrawer(); });

  function fillSettingsForm() {
    if (!channel) return;
    document.getElementById("s-name").value = channel.name || "";
    document.getElementById("s-desc").value = channel.description || "";
    const vis = channel.visibility === "private" ? "private" : "public";
    const radio = els.settingsForm.querySelector('input[name="s-visibility"][value="' + vis + '"]');
    if (radio) radio.checked = true;
    document.getElementById("s-list").checked = !!channel.listPublic;
    document.getElementById("s-join").checked = !!channel.allowJoin;
    document.getElementById("s-approval").checked = !!channel.requireApproval;
    document.getElementById("s-speak").checked = !!channel.allowSpeak;
  }

  els.settingsForm.addEventListener("submit", async function (e) {
    e.preventDefault();
    const payload = {
      name: document.getElementById("s-name").value.trim(),
      description: document.getElementById("s-desc").value.trim(),
      visibility: els.settingsForm.querySelector('input[name="s-visibility"]:checked').value,
      listPublic: document.getElementById("s-list").checked,
      allowJoin: document.getElementById("s-join").checked,
      requireApproval: document.getElementById("s-approval").checked,
      allowSpeak: document.getElementById("s-speak").checked,
    };
    try {
      const res = await api("/api/channels/" + encodeURIComponent(roomKey), {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      if (!res.ok) throw new Error((await res.text()).trim());
      applyChannel(await res.json());
      els.settingsMsg.textContent = t("settings.saved");
      els.settingsMsg.hidden = false;
      setTimeout(() => { els.settingsMsg.hidden = true; }, 2500);
    } catch (err) {
      els.settingsMsg.textContent = "Error: " + err.message;
      els.settingsMsg.hidden = false;
    }
  });

  els.announceForm.addEventListener("submit", async function (e) {
    e.preventDefault();
    const text = els.announceText.value.trim();
    if (!text) return;
    try {
      const res = await api("/api/channels/" + encodeURIComponent(roomKey) + "/announcements", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ text: text }),
      });
      if (!res.ok) throw new Error((await res.text()).trim());
      els.announceText.value = "";
    } catch (err) {
      showNotice("Announcement failed: " + err.message);
    }
  });

  async function moderate(action, uid) {
    const res = await api("/api/channels/" + encodeURIComponent(roomKey) + "/moderate", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action: action, uid: uid }),
    });
    if (res.ok) {
      const data = await res.json();
      if (data.channel) applyChannel(data.channel);
      if (data.members) roster = data.members;
      renderPending(data.pending || []);
      renderMembers();
      // Confirm the action took effect, so moderation is never silent.
      showNotice(t("mod.done"));
      setTimeout(() => { els.notice.hidden = true; }, 1500);
    } else {
      showNotice((await res.text()).trim());
    }
  }

  function modButton(labelKey, action, uid, cls) {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "mod-button" + (cls ? " " + cls : "");
    b.textContent = t(labelKey);
    b.addEventListener("click", () => moderate(action, uid));
    return b;
  }

  function badge(textKey, cls) {
    const s = document.createElement("span");
    s.className = "member-badge " + cls;
    s.textContent = t(textKey);
    return s;
  }

  let lastPending = [];
  function renderPending(list) {
    lastPending = list || [];
    els.pendingSection.hidden = !list || list.length === 0;
    els.pendingList.innerHTML = "";
    (list || []).forEach((p) => {
      const li = document.createElement("li");
      const name = document.createElement("span");
      name.className = "member-name";
      name.textContent = p.name + "#" + p.uid;
      li.appendChild(name);
      const actions = document.createElement("span");
      actions.className = "member-actions";
      actions.appendChild(modButton("mod.approve", "approve", p.uid, "ok"));
      actions.appendChild(modButton("mod.reject", "reject", p.uid));
      li.appendChild(actions);
      els.pendingList.appendChild(li);
    });
  }

  // Render the moderation roster from the server's authoritative member list,
  // showing each person's current standing and only the actions that make sense
  // for them. This is what makes moderation visibly take effect.
  function renderMembers() {
    els.memberList.innerHTML = "";
    const entries = (roster || []).filter((m) => m.uid && m.uid !== me.uid);
    if (entries.length === 0) {
      const li = document.createElement("li");
      li.className = "member-empty";
      li.textContent = t("settings.no_members");
      els.memberList.appendChild(li);
      return;
    }
    entries.forEach((m) => {
      const li = document.createElement("li");

      const head = document.createElement("span");
      head.className = "member-head";
      const name = document.createElement("span");
      name.className = "member-name";
      name.textContent = m.name + "#" + m.uid;
      head.appendChild(name);
      if (m.owner) head.appendChild(badge("mod.owner", "owner"));
      else if (m.admin) head.appendChild(badge("mod.admin_badge", "admin"));
      if (m.banned) head.appendChild(badge("mod.banned_badge", "danger"));
      else if (m.muted) head.appendChild(badge("mod.muted_badge", "warn"));
      li.appendChild(head);

      const actions = document.createElement("span");
      actions.className = "member-actions";
      if (role && role.owner && !m.owner) {
        actions.appendChild(m.admin
          ? modButton("mod.unadmin", "demote", m.uid)
          : modButton("mod.admin", "promote", m.uid));
      }
      actions.appendChild(m.muted
        ? modButton("mod.unmute", "unmute", m.uid, "ok")
        : modButton("mod.mute", "mute", m.uid));
      actions.appendChild(modButton("mod.kick", "kick", m.uid));
      actions.appendChild(m.banned
        ? modButton("mod.unban", "unban", m.uid, "ok")
        : modButton("mod.ban", "ban", m.uid, "danger"));
      li.appendChild(actions);
      els.memberList.appendChild(li);
    });
  }

  async function loadDrawerState() {
    fillSettingsForm();
    renderMembers();
    try {
      const res = await api("/api/channels/" + encodeURIComponent(roomKey));
      const view = await res.json();
      if (view.members) roster = view.members;
      renderPending(view.pending || []);
      renderMembers();
    } catch (e) { /* ignore */ }
  }

  els.dissolveBtn.addEventListener("click", async function () {
    if (!window.confirm(t("settings.dissolve_confirm"))) return;
    const res = await api("/api/channels/" + encodeURIComponent(roomKey), { method: "DELETE" });
    if (res.ok || res.status === 204) {
      window.location.href = "/";
    } else {
      showNotice((await res.text()).trim());
    }
  });

  // Re-render dynamic, script-generated text when the language changes.
  document.addEventListener("nekodrop:langchange", function () {
    if (channel) applyChannel(channel);
    if (role) applyRole(role);
    renderPending(lastPending);
    renderMembers();
  });

  // ---------- bootstrap ----------
  async function loadMe() {
    try {
      const res = await api("/api/me");
      if (res.ok) {
        me = await res.json();
        remember(me.uid, me.name);
      }
    } catch (e) { /* ignore */ }
  }

  (async function init() {
    await loadMe();
    const view = await loadChannel();
    if (view && view.role && !view.role.canRead) {
      // Private channel we cannot read yet: gate behind a join prompt.
      setStatus(false, t("room.private_status"));
      showNotice(t("room.private_notice"));
      els.empty.hidden = true;
      return;
    }
    connect();
  })();
})();
