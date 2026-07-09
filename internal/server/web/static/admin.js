// Admin panel: authenticate with the configured admin token, then manage
// site-wide state — disable channels, ban users, grant upload-size exemptions
// and edit the runtime configuration.
(function () {
  "use strict";

  const t = (k, v) => (window.NekoI18n ? window.NekoI18n.t(k, v) : k);

  const TOKEN_KEY = "nekodrop.adminToken";

  const loginForm = document.getElementById("login-form");
  const tokenInput = document.getElementById("admin-token");
  const loginError = document.getElementById("login-error");
  const lockBtn = document.getElementById("lock-btn");
  const overview = document.getElementById("overview");
  const overviewStats = document.getElementById("overview-stats");
  const configCard = document.getElementById("config-card");
  const configForm = document.getElementById("config-form");
  const configFields = document.getElementById("config-fields");
  const configNote = document.getElementById("config-note");
  const configError = document.getElementById("config-error");
  const channelsCard = document.getElementById("channels-card");
  const channelList = document.getElementById("admin-channels");
  const channelSearch = document.getElementById("channel-search");
  const usersCard = document.getElementById("users-card");
  const userList = document.getElementById("admin-users");
  const userSearch = document.getElementById("user-search");

  let token = "";
  try {
    token = window.sessionStorage.getItem(TOKEN_KEY) || "";
  } catch (e) { /* ignore */ }

  function api(path, opts) {
    opts = opts || {};
    opts.headers = Object.assign({ "X-Admin-Token": token }, opts.headers || {});
    return fetch(path, opts);
  }

  function showLogin(message) {
    stopOverviewRefresh();
    overview.hidden = true;
    configCard.hidden = true;
    channelsCard.hidden = true;
    usersCard.hidden = true;
    loginForm.hidden = false;
    if (message) {
      loginError.textContent = message;
      loginError.hidden = false;
    }
  }

  async function unlock() {
    const res = await api("/api/admin/overview").catch(() => null);
    if (!res || !res.ok) {
      try { window.sessionStorage.removeItem(TOKEN_KEY); } catch (e) { /* ignore */ }
      showLogin(res && res.status === 401 ? t("admin.bad_token") : t("admin.err"));
      return false;
    }
    loginForm.hidden = true;
    loginError.hidden = true;
    overview.hidden = false;
    configCard.hidden = false;
    channelsCard.hidden = false;
    usersCard.hidden = false;
    renderOverview(await res.json());
    startOverviewRefresh();
    await Promise.all([loadConfig(), loadChannels(), loadUsers()]);
    return true;
  }

  loginForm.addEventListener("submit", async function (e) {
    e.preventDefault();
    token = tokenInput.value.trim();
    if (!token) return;
    if (await unlock()) {
      try { window.sessionStorage.setItem(TOKEN_KEY, token); } catch (e2) { /* ignore */ }
      tokenInput.value = "";
    }
  });

  lockBtn.addEventListener("click", function () {
    token = "";
    try { window.sessionStorage.removeItem(TOKEN_KEY); } catch (e) { /* ignore */ }
    showLogin("");
  });

  // ---------- overview dashboard ----------
  // Stat tiles: the value in primary ink, the label in muted ink. No charts —
  // these are headline numbers refreshed on a steady poll while unlocked.

  function fmtBytes(n) {
    n = Number(n) || 0;
    if (n < 1024) return n + " B";
    const units = ["KiB", "MiB", "GiB", "TiB"];
    let u = -1;
    do {
      n /= 1024;
      u++;
    } while (n >= 1024 && u < units.length - 1);
    return (n >= 100 ? n.toFixed(0) : n.toFixed(1)) + " " + units[u];
  }

  function fmtUptime(sec) {
    sec = Math.max(0, Number(sec) || 0);
    const d = Math.floor(sec / 86400);
    const h = Math.floor((sec % 86400) / 3600);
    const m = Math.floor((sec % 3600) / 60);
    if (d > 0) return d + "d " + h + "h";
    if (h > 0) return h + "h " + m + "m";
    if (m > 0) return m + "m " + Math.floor(sec % 60) + "s";
    return Math.floor(sec) + "s";
  }

  let lastOverview = null;
  function renderOverview(data) {
    lastOverview = data || lastOverview;
    if (!lastOverview) return;
    const d = lastOverview;
    overviewStats.innerHTML = "";

    const section = (titleKey, tiles) => {
      const h = document.createElement("h3");
      h.className = "stat-section";
      h.textContent = t(titleKey);
      overviewStats.appendChild(h);
      const grid = document.createElement("div");
      grid.className = "stat-grid";
      tiles.forEach(([labelKey, value]) => {
        const tile = document.createElement("div");
        tile.className = "stat-tile";
        const v = document.createElement("div");
        v.className = "stat-value";
        v.textContent = String(value);
        const l = document.createElement("div");
        l.className = "stat-label";
        l.textContent = t(labelKey);
        tile.appendChild(v);
        tile.appendChild(l);
        grid.appendChild(tile);
      });
      overviewStats.appendChild(grid);
    };

    section("admin.sec_live", [
      ["admin.channels_count", d.channels],
      ["admin.users_count", d.users],
      ["admin.st_online", d.online],
      ["admin.st_connections", d.connections],
      ["admin.st_named", d.namedUsers],
      ["admin.st_migration", d.migrationUsers],
      ["admin.banned_channels", d.bannedChannels],
      ["admin.banned_users", d.bannedUsers],
    ]);
    section("admin.sec_content", [
      ["admin.st_messages", d.messages],
      ["admin.st_files", d.files],
      ["admin.st_announcements", d.announcements],
      ["admin.st_resident_bytes", fmtBytes(d.residentBytes)],
      ["admin.st_heap", fmtBytes(d.memHeapBytes)],
      ["admin.st_sys", fmtBytes(d.memSysBytes)],
      ["admin.st_goroutines", d.goroutines],
      ["admin.st_uptime", fmtUptime(d.uptimeSeconds)],
    ]);
    const storeTiles = [["admin.backend", d.backend]];
    if (d.store && d.store.persistent) {
      storeTiles.push(
        ["admin.st_db_size", fmtBytes(d.store.sizeBytes)],
        ["admin.st_store_messages", d.store.messages],
        ["admin.st_store_files", d.store.files],
        ["admin.st_store_file_bytes", fmtBytes(d.store.fileBytes)],
        ["admin.st_store_users", d.store.users],
        ["admin.st_store_channels", d.store.channels]
      );
    }
    section("admin.sec_store", storeTiles);
  }

  async function refreshOverview() {
    const res = await api("/api/admin/overview").catch(() => null);
    if (res && res.ok) renderOverview(await res.json());
  }

  // The dashboard refreshes itself while the panel is unlocked.
  let overviewTimer = null;
  function startOverviewRefresh() {
    if (overviewTimer === null) overviewTimer = window.setInterval(refreshOverview, 5000);
  }
  function stopOverviewRefresh() {
    if (overviewTimer !== null) {
      window.clearInterval(overviewTimer);
      overviewTimer = null;
    }
  }

  // ---------- runtime configuration ----------
  const CONFIG_FIELDS = [
    { key: "maxUploadBytes", label: "admin.cfg_max_upload" },
    { key: "maxChannelsPerUser", label: "admin.cfg_max_channels_per_user" },
    { key: "maxMessagesPerChannel", label: "admin.cfg_max_messages" },
    { key: "maxFileBytesPerChannel", label: "admin.cfg_max_file_bytes" },
    { key: "maxBytesPerChannel", label: "admin.cfg_max_channel_bytes" },
    { key: "maxSubscribersPerChannel", label: "admin.cfg_max_subscribers" },
    { key: "maxChannels", label: "admin.cfg_max_live_channels" },
    { key: "maxUsers", label: "admin.cfg_max_users" },
  ];

  function buildConfigForm(data) {
    configFields.innerHTML = "";
    CONFIG_FIELDS.forEach((f) => {
      const label = document.createElement("label");
      label.setAttribute("for", "cfg-" + f.key);
      label.textContent = t(f.label);
      const input = document.createElement("input");
      input.type = "number";
      input.id = "cfg-" + f.key;
      input.min = "0";
      input.dataset.key = f.key;
      input.placeholder = String(data.base[f.key]);
      const ov = data.overrides ? data.overrides[f.key] : null;
      input.value = ov == null ? "" : String(ov);
      configFields.appendChild(label);
      configFields.appendChild(input);
    });
  }

  async function loadConfig() {
    const res = await api("/api/admin/config").catch(() => null);
    if (res && res.ok) buildConfigForm(await res.json());
  }

  configForm.addEventListener("submit", async function (e) {
    e.preventDefault();
    configNote.hidden = true;
    configError.hidden = true;
    const patch = {};
    configFields.querySelectorAll("input[data-key]").forEach((input) => {
      const raw = input.value.trim();
      patch[input.dataset.key] = raw === "" ? null : Number(raw);
    });
    const res = await api("/api/admin/config", {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    }).catch(() => null);
    if (!res || !res.ok) {
      configError.textContent = res ? (await res.text()).trim() || t("admin.err") : t("admin.err");
      configError.hidden = false;
      return;
    }
    buildConfigForm(await res.json());
    configNote.textContent = t("admin.saved");
    configNote.hidden = false;
  });

  // ---------- shared row helpers ----------
  function badge(text, danger) {
    const span = document.createElement("span");
    span.className = "channel-badge" + (danger ? " admin-badge-danger" : "");
    span.textContent = text;
    return span;
  }

  // uploadControl renders the per-target upload-size exemption: a byte input
  // with apply/clear buttons that PATCH {maxUploadBytes: n|null}.
  function uploadControl(current, onPatch) {
    const wrap = document.createElement("span");
    wrap.className = "admin-upload";
    const input = document.createElement("input");
    input.type = "number";
    input.min = "1";
    input.placeholder = t("admin.upload_override_ph");
    input.title = t("admin.upload_override_ph");
    if (current != null) input.value = String(current);
    const apply = document.createElement("button");
    apply.type = "button";
    apply.className = "ghost-button";
    apply.textContent = t("admin.apply");
    apply.addEventListener("click", function () {
      const raw = input.value.trim();
      if (raw === "") return;
      onPatch({ maxUploadBytes: Number(raw) });
    });
    const clear = document.createElement("button");
    clear.type = "button";
    clear.className = "ghost-button";
    clear.textContent = t("admin.clear");
    clear.addEventListener("click", () => onPatch({ maxUploadBytes: null }));
    wrap.appendChild(input);
    wrap.appendChild(apply);
    wrap.appendChild(clear);
    return wrap;
  }

  // ---------- channels ----------
  async function loadChannels() {
    const q = channelSearch.value.trim();
    const res = await api("/api/admin/channels?q=" + encodeURIComponent(q)).catch(() => null);
    if (!res || !res.ok) return;
    const data = await res.json();
    renderChannels((data && data.channels) || []);
  }

  async function patchChannel(id, patch) {
    const res = await api("/api/admin/channels/" + encodeURIComponent(id), {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    }).catch(() => null);
    if (!res || !res.ok) {
      await NekoUI.alert({ title: t("admin.err"), body: res ? (await res.text()).trim() || t("admin.err") : t("admin.err"), confirmLabel: t("ui.confirm") });
    }
    await Promise.all([loadChannels(), refreshOverview()]);
  }

  function renderChannels(channels) {
    channelList.innerHTML = "";
    if (channels.length === 0) {
      const li = document.createElement("li");
      li.className = "channel-empty";
      li.textContent = t("admin.none");
      channelList.appendChild(li);
      return;
    }
    channels.forEach((c) => {
      const li = document.createElement("li");
      li.className = "admin-row";

      const head = document.createElement("div");
      head.className = "channel-top";
      const link = document.createElement("a");
      link.href = "/r/" + encodeURIComponent(c.key);
      link.className = "channel-name";
      link.textContent = c.name;
      const id = document.createElement("span");
      id.className = "channel-id";
      id.textContent = "#" + c.id;
      head.appendChild(link);
      head.appendChild(id);
      if (c.visibility === "private") head.appendChild(badge(t("badge.private")));
      if (c.bannedByAdmin) head.appendChild(badge(t("admin.disabled_badge"), true));
      li.appendChild(head);

      const meta = document.createElement("div");
      meta.className = "channel-meta";
      const online = document.createElement("span");
      online.className = "channel-online";
      online.textContent = "🟢 " + c.online + " " + t("directory.online");
      const members = document.createElement("span");
      members.textContent = (c.members || 0) + " " + t("directory.members");
      meta.appendChild(online);
      meta.appendChild(members);
      if (c.ownerUid) {
        const owner = document.createElement("span");
        owner.textContent = t("admin.owner") + " #" + c.ownerUid;
        meta.appendChild(owner);
      }
      li.appendChild(meta);

      const controls = document.createElement("div");
      controls.className = "admin-controls";
      controls.appendChild(uploadControl(
        c.maxUploadBytes == null ? null : c.maxUploadBytes,
        (patch) => patchChannel(c.id, patch)
      ));
      const banBtn = document.createElement("button");
      banBtn.type = "button";
      banBtn.className = c.bannedByAdmin ? "ghost-button" : "ghost-button admin-danger";
      banBtn.textContent = c.bannedByAdmin ? t("admin.ch_unban") : t("admin.ch_ban");
      banBtn.addEventListener("click", () => patchChannel(c.id, { banned: !c.bannedByAdmin }));
      controls.appendChild(banBtn);
      li.appendChild(controls);

      channelList.appendChild(li);
    });
  }

  // ---------- users ----------
  async function loadUsers() {
    const q = userSearch.value.trim();
    const res = await api("/api/admin/users?q=" + encodeURIComponent(q)).catch(() => null);
    if (!res || !res.ok) return;
    const data = await res.json();
    renderUsers((data && data.users) || []);
  }

  async function patchUser(uid, patch) {
    const res = await api("/api/admin/users/" + encodeURIComponent(uid), {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    }).catch(() => null);
    if (!res || !res.ok) {
      await NekoUI.alert({ title: t("admin.err"), body: res ? (await res.text()).trim() || t("admin.err") : t("admin.err"), confirmLabel: t("ui.confirm") });
    }
    await Promise.all([loadUsers(), refreshOverview()]);
  }

  function renderUsers(users) {
    userList.innerHTML = "";
    if (users.length === 0) {
      const li = document.createElement("li");
      li.className = "channel-empty";
      li.textContent = t("admin.none");
      userList.appendChild(li);
      return;
    }
    users.forEach((u) => {
      const li = document.createElement("li");
      li.className = "admin-row";

      const head = document.createElement("div");
      head.className = "channel-top";
      const name = document.createElement("span");
      name.className = "channel-name";
      name.textContent = u.name + "#" + u.uid;
      head.appendChild(name);
      if (u.migration) head.appendChild(badge(t("admin.migration_badge")));
      if (u.banned) head.appendChild(badge(t("mod.banned_badge"), true));
      li.appendChild(head);

      const controls = document.createElement("div");
      controls.className = "admin-controls";
      controls.appendChild(uploadControl(
        u.maxUploadBytes == null ? null : u.maxUploadBytes,
        (patch) => patchUser(u.uid, patch)
      ));
      const banBtn = document.createElement("button");
      banBtn.type = "button";
      banBtn.className = u.banned ? "ghost-button" : "ghost-button admin-danger";
      banBtn.textContent = u.banned ? t("mod.unban") : t("mod.ban");
      banBtn.addEventListener("click", () => patchUser(u.uid, { banned: !u.banned }));
      controls.appendChild(banBtn);
      li.appendChild(controls);

      userList.appendChild(li);
    });
  }

  // ---------- wiring ----------
  function debounce(fn, ms) {
    let timer = null;
    return function () {
      if (timer) window.clearTimeout(timer);
      timer = window.setTimeout(fn, ms);
    };
  }
  channelSearch.addEventListener("input", debounce(loadChannels, 300));
  userSearch.addEventListener("input", debounce(loadUsers, 300));
  document.getElementById("channel-refresh").addEventListener("click", loadChannels);
  document.getElementById("user-refresh").addEventListener("click", loadUsers);

  document.addEventListener("nekodrop:langchange", function () {
    renderOverview(null);
    if (!overview.hidden) {
      loadConfig();
      loadChannels();
      loadUsers();
    }
  });

  if (token) {
    unlock();
  } else {
    showLogin("");
  }
})();
