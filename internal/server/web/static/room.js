// Channel page: identity, live stream, media previews + lightbox, @-mentions
// with notifications, announcements, presence, and owner/admin controls.
(function () {
  "use strict";

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
    previewToggle: document.getElementById("preview-toggle"),
    fileInput: document.getElementById("file-input"),
    uploadBar: document.getElementById("upload-bar"),
    uploadText: document.getElementById("upload-text"),
    joinBtn: document.getElementById("join-btn"),
    settingsBtn: document.getElementById("settings-btn"),
    notifyBtn: document.getElementById("notify-btn"),
    notifyCount: document.getElementById("notify-count"),
    mentionPop: document.getElementById("mention-pop"),
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
  function openLightbox(kind, url) {
    els.lightboxContent.innerHTML = "";
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
    els.lightboxContent.appendChild(node);
    els.lightbox.hidden = false;
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
    if (e.key === "Escape") { closeLightbox(); }
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
    } else {
      const textWrap = document.createElement("div");
      textWrap.className = "text";
      const mentionsMe = renderText(textWrap, msg.text || "");
      body.appendChild(textWrap);
      if (mentionsMe) item.classList.add("mentions-me");
      // Opt-in link previews generated on the sender's side.
      if (msg.preview) {
        URL_RE.lastIndex = 0;
        let u;
        while ((u = URL_RE.exec(msg.text || "")) !== null) {
          const kind = urlMediaKind(u[1]);
          if (kind) appendMediaPreview(body, kind, u[1]);
        }
      }
      if (mentionsMe && historyLoaded) notifyMention(item);
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
    document.title = "NekoDrop · " + (info.name || roomKey);
  }

  function applyRole(r) {
    role = r;
    const isMember = r.member || r.owner;
    els.joinBtn.hidden = isMember;
    els.joinBtn.textContent = r.pending ? "Requested ✓" : "Join";
    els.joinBtn.disabled = r.pending;
    els.settingsBtn.hidden = !r.admin;
    els.dangerSection.hidden = !r.owner;

    if (r.canSpeak) {
      els.textInput.disabled = false;
      els.fileInput.disabled = false;
      els.textInput.placeholder = "Type a message… use @ to mention";
    } else {
      els.textInput.disabled = true;
      els.fileInput.disabled = true;
      els.textInput.placeholder = isMember
        ? "Sending is disabled in this channel"
        : "Join this channel to send messages";
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
      return view;
    } catch (e) {
      return null;
    }
  }

  // ---------- live stream ----------
  let source = null;
  function connect() {
    source = new EventSource("/api/stream/" + encodeURIComponent(roomKey));
    source.onopen = () => setStatus(true, "connected");
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
          setStatus(false, "channel dissolved");
          showNotice("This channel has been dissolved by its owner. The path is now free to reuse.");
          if (source) source.close();
          els.composer.hidden = true;
          break;
      }
    };
    source.onerror = function () {
      setStatus(false, "reconnecting…");
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
      body: JSON.stringify({ sender: me.name, text: text, preview: els.previewToggle.checked }),
    });
    if (!res.ok) throw new Error((await res.text()).trim() || "send failed");
  }

  els.composer.addEventListener("submit", function (e) {
    e.preventDefault();
    const text = els.textInput.value.trim();
    if (!text) return;
    els.textInput.value = "";
    els.mentionPop.hidden = true;
    sendText(text).catch(function (err) {
      els.textInput.value = text;
      showNotice(err.message);
      setTimeout(() => { els.notice.hidden = true; }, 4000);
    });
  });

  async function uploadFile(file) {
    els.uploadBar.hidden = false;
    els.uploadText.textContent = "Uploading " + file.name + "…";
    try {
      const data = new FormData();
      data.append("sender", me.name);
      data.append("file", file);
      const res = await api("/api/files/" + encodeURIComponent(roomKey), { method: "POST", body: data });
      if (!res.ok) throw new Error((await res.text()).trim() || "upload failed");
      els.uploadText.textContent = "Shared " + file.name;
    } catch (err) {
      els.uploadText.textContent = "Upload failed: " + err.message;
    } finally {
      setTimeout(() => { els.uploadBar.hidden = true; }, 2500);
    }
  }
  els.fileInput.addEventListener("change", function () {
    const file = els.fileInput.files && els.fileInput.files[0];
    if (file) uploadFile(file);
    els.fileInput.value = "";
  });

  // ---------- join ----------
  els.joinBtn.addEventListener("click", async function () {
    els.joinBtn.disabled = true;
    try {
      const res = await api("/api/channels/" + encodeURIComponent(roomKey) + "/join", { method: "POST" });
      if (!res.ok) { showNotice((await res.text()).trim()); els.joinBtn.disabled = false; return; }
      const data = await res.json();
      if (data.pending) {
        els.joinBtn.textContent = "Requested ✓";
        showNotice("Your request to join was sent for approval.");
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
      els.settingsMsg.textContent = "Settings saved.";
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
      renderPending(data.pending || []);
      renderMembers();
    } else {
      showNotice((await res.text()).trim());
    }
  }

  function modButton(label, action, uid, cls) {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "mod-button" + (cls ? " " + cls : "");
    b.textContent = label;
    b.addEventListener("click", () => moderate(action, uid));
    return b;
  }

  function renderPending(list) {
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
      actions.appendChild(modButton("Approve", "approve", p.uid, "ok"));
      actions.appendChild(modButton("Reject", "reject", p.uid));
      li.appendChild(actions);
      els.pendingList.appendChild(li);
    });
  }

  function renderMembers() {
    els.memberList.innerHTML = "";
    const entries = [];
    participants.forEach((name, uid) => { if (uid !== me.uid) entries.push({ uid, name }); });
    if (entries.length === 0) {
      const li = document.createElement("li");
      li.className = "member-empty";
      li.textContent = "No other participants seen yet.";
      els.memberList.appendChild(li);
      return;
    }
    entries.sort((a, b) => a.name.localeCompare(b.name));
    entries.forEach((p) => {
      const li = document.createElement("li");
      const name = document.createElement("span");
      name.className = "member-name";
      name.textContent = p.name + "#" + p.uid;
      li.appendChild(name);
      const actions = document.createElement("span");
      actions.className = "member-actions";
      if (role && role.owner) {
        actions.appendChild(modButton("Admin", "promote", p.uid));
        actions.appendChild(modButton("Unadmin", "demote", p.uid));
      }
      actions.appendChild(modButton("Mute", "mute", p.uid));
      actions.appendChild(modButton("Unmute", "unmute", p.uid));
      actions.appendChild(modButton("Kick", "kick", p.uid));
      actions.appendChild(modButton("Ban", "ban", p.uid, "danger"));
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
      renderPending(view.pending || []);
    } catch (e) { /* ignore */ }
  }

  els.dissolveBtn.addEventListener("click", async function () {
    if (!window.confirm("Dissolve this channel? The path is freed and the channel ID is retired forever.")) return;
    const res = await api("/api/channels/" + encodeURIComponent(roomKey), { method: "DELETE" });
    if (res.ok || res.status === 204) {
      window.location.href = "/";
    } else {
      showNotice((await res.text()).trim());
    }
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
      setStatus(false, "private channel");
      showNotice("This channel is private. Join to view its contents.");
      els.empty.hidden = true;
      return;
    }
    connect();
  })();
})();
