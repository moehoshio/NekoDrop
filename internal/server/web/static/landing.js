// Landing page: identity, join/create channels, and the live public directory.
(function () {
  "use strict";

  const t = (k, v) => (window.NekoI18n ? window.NekoI18n.t(k, v) : k);

  const form = document.getElementById("join-form");
  const codeInput = document.getElementById("code");
  const nameInput = document.getElementById("name");
  const randomButton = document.getElementById("random");
  const meLabel = document.getElementById("me-label");
  const meGuest = document.getElementById("me-guest");
  const createForm = document.getElementById("create-form");
  const createError = document.getElementById("create-error");
  const channelList = document.getElementById("channel-list");
  const mineCard = document.getElementById("mine-card");
  const mineList = document.getElementById("mine-list");
  const joinedCard = document.getElementById("joined-card");
  const joinedList = document.getElementById("joined-list");

  let me = null;

  // Restore a previously used display name.
  try {
    const saved = localStorage.getItem("nekodrop.name");
    if (saved) nameInput.value = saved;
  } catch (e) {
    /* ignore storage errors */
  }

  // Mirror the server's NormalizeKey so the URL matches the channel key.
  function slugify(raw) {
    return (raw || "")
      .trim()
      .toLowerCase()
      .replace(/[^\p{L}\p{N}\s\-_/]+/gu, "")
      .replace(/[\s\-_/]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 128);
  }

  function saveName() {
    try {
      const name = nameInput.value.trim();
      if (name) localStorage.setItem("nekodrop.name", name);
    } catch (e) {
      /* ignore storage errors */
    }
  }

  // Persist the chosen name to the server so the UID-bound identity is updated
  // before navigating into a channel.
  async function commitName() {
    const name = nameInput.value.trim();
    if (!name) return;
    saveName();
    try {
      await fetch("/api/me", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: name }),
      });
    } catch (e) {
      /* best effort */
    }
  }

  async function go(code) {
    const slug = slugify(code);
    if (!slug) {
      codeInput.focus();
      return;
    }
    await commitName();
    window.location.href = "/r/" + encodeURIComponent(slug);
  }

  form.addEventListener("submit", function (e) {
    e.preventDefault();
    go(codeInput.value);
  });

  randomButton.addEventListener("click", function () {
    const words = ["neko", "drop", "swift", "paw", "byte", "purr", "cloud", "mint"];
    const pick = () => words[Math.floor(Math.random() * words.length)];
    go(pick() + "-" + pick() + "-" + Math.floor(1000 + Math.random() * 9000));
  });

  // --- Identity ---
  function renderMe() {
    if (!me) return;
    meLabel.textContent = me.name + "#" + me.uid;
    // An unnamed visitor keeps the default name; flag that distinctly so it is
    // never confused with someone who deliberately chose a name.
    meGuest.hidden = !!me.named;
    meLabel.classList.toggle("is-guest", !me.named);
  }

  async function loadMe() {
    try {
      const res = await fetch("/api/me", { method: "GET" });
      if (res.ok) {
        me = await res.json();
        renderMe();
      }
    } catch (e) {
      meLabel.textContent = "…";
    }
  }

  // --- Create channel ---
  createForm.addEventListener("submit", async function (e) {
    e.preventDefault();
    createError.hidden = true;
    const name = document.getElementById("c-name").value.trim();
    if (!name) return;
    await commitName();

    const visibility = createForm.querySelector('input[name="visibility"]:checked').value;
    const payload = {
      name: name,
      description: document.getElementById("c-desc").value.trim(),
      visibility: visibility,
      listPublic: document.getElementById("c-list").checked,
      allowJoin: document.getElementById("c-join").checked,
      requireApproval: document.getElementById("c-approval").checked,
      allowSpeak: document.getElementById("c-speak").checked,
    };

    try {
      const res = await fetch("/api/channels", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      if (!res.ok) {
        createError.textContent = (await res.text()).trim() || t("landing.create_err");
        createError.hidden = false;
        return;
      }
      const channel = await res.json();
      window.location.href = "/r/" + encodeURIComponent(channel.key);
    } catch (err) {
      createError.textContent = t("landing.net_err");
      createError.hidden = false;
    }
  });

  // --- Account migration ---
  // Off by default, per user: enabling mints a persistent migration code that
  // another browser can enter to inherit this account. The status is loaded
  // lazily when the section is first opened.
  const migrateBox = document.getElementById("migrate-box");
  const migrateStatus = document.getElementById("migrate-status");
  const migrateCodeRow = document.getElementById("migrate-code-row");
  const migrateCode = document.getElementById("migrate-code");
  const migrateCopy = document.getElementById("migrate-copy");
  const migrateEnable = document.getElementById("migrate-enable");
  const migrateRegen = document.getElementById("migrate-regen");
  const migrateDisable = document.getElementById("migrate-disable");
  const migrateForm = document.getElementById("migrate-form");
  const migrateInput = document.getElementById("migrate-input");
  const migrateError = document.getElementById("migrate-error");
  const migrateDone = document.getElementById("migrate-done");

  let migration = null; // {enabled, code} once loaded

  function renderMigration() {
    if (!migration) return;
    migrateStatus.hidden = false;
    migrateStatus.textContent = migration.enabled
      ? t("landing.migrate_status_on")
      : t("landing.migrate_status_off");
    migrateCodeRow.hidden = !migration.enabled;
    migrateCode.value = migration.code || "";
    migrateEnable.hidden = migration.enabled;
    migrateRegen.hidden = !migration.enabled;
    migrateDisable.hidden = !migration.enabled;
  }

  async function loadMigration() {
    try {
      const res = await fetch("/api/me/migration");
      if (res.ok) {
        migration = await res.json();
        renderMigration();
      }
    } catch (e) {
      /* best effort */
    }
  }

  migrateBox.addEventListener("toggle", function () {
    if (migrateBox.open && migration === null) loadMigration();
  });

  async function setMigration(method) {
    try {
      const res = await fetch("/api/me/migration", { method: method });
      if (res.ok) {
        migration = await res.json();
        renderMigration();
      }
    } catch (e) {
      /* best effort */
    }
  }

  migrateEnable.addEventListener("click", () => setMigration("POST"));
  migrateRegen.addEventListener("click", () => setMigration("POST"));
  migrateDisable.addEventListener("click", () => setMigration("DELETE"));

  migrateCopy.addEventListener("click", async function () {
    migrateCode.select();
    try {
      await navigator.clipboard.writeText(migrateCode.value);
      migrateCopy.textContent = t("landing.migrate_copied");
      setTimeout(() => { migrateCopy.textContent = t("landing.migrate_copy"); }, 1500);
    } catch (e) {
      document.execCommand("copy"); // fallback for insecure contexts
    }
  });

  migrateForm.addEventListener("submit", async function (e) {
    e.preventDefault();
    migrateError.hidden = true;
    migrateDone.hidden = true;
    const code = migrateInput.value.trim();
    if (!code) return;
    if (!window.confirm(t("landing.migrate_confirm"))) return;
    try {
      const res = await fetch("/api/migrate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ code: code }),
      });
      if (!res.ok) {
        migrateError.textContent = res.status === 404
          ? t("landing.migrate_bad_code")
          : (await res.text()).trim() || t("landing.net_err");
        migrateError.hidden = false;
        return;
      }
      me = await res.json();
      renderMe();
      // Sync the name box to the adopted account so a later join/create does
      // not silently rename it to this browser's previously saved name.
      nameInput.value = me.named ? me.name : "";
      saveName();
      migrateInput.value = "";
      migration = null; // the adopted account's own status; reload lazily
      if (migrateBox.open) await loadMigration();
      migrateDone.textContent = t("landing.migrate_done", { name: me.name + "#" + me.uid });
      migrateDone.hidden = false;
      loadChannels(); // "your/joined channels" now reflect the adopted account
    } catch (err) {
      migrateError.textContent = t("landing.net_err");
      migrateError.hidden = false;
    }
  });

  // --- Public directory ---
  function channelEntry(c) {
    const li = document.createElement("li");
    li.className = "channel-item";

    const link = document.createElement("a");
    link.className = "channel-link";
    link.href = "/r/" + encodeURIComponent(c.key);

    const top = document.createElement("div");
    top.className = "channel-top";
    const nm = document.createElement("span");
    nm.className = "channel-name";
    nm.textContent = c.name;
    const id = document.createElement("span");
    id.className = "channel-id";
    id.textContent = "#" + c.id;
    top.appendChild(nm);
    top.appendChild(id);
    if (c.visibility === "private") {
      const lock = document.createElement("span");
      lock.className = "channel-badge";
      lock.textContent = t("badge.private");
      top.appendChild(lock);
    }
    link.appendChild(top);

    if (c.description) {
      const desc = document.createElement("p");
      desc.className = "channel-desc";
      desc.textContent = c.description;
      link.appendChild(desc);
    }

    const meta = document.createElement("div");
    meta.className = "channel-meta";
    const online = document.createElement("span");
    online.className = "channel-online";
    online.textContent = "🟢 " + c.online + " " + t("directory.online");
    const members = document.createElement("span");
    const n = c.members || 0;
    members.textContent = n + " " + (n === 1 ? t("directory.member") : t("directory.members"));
    meta.appendChild(online);
    meta.appendChild(members);
    link.appendChild(meta);

    li.appendChild(link);
    return li;
  }

  // Build a fresh list of <li> nodes, used to diff against the live DOM so the
  // auto-refresh never flickers when nothing changed.
  function renderList(target, channels, emptyKey) {
    const html = channels.map((c) => c.id + ":" + c.online + ":" + c.members + ":" + c.name).join("|");
    if (target.dataset.sig === html) return;
    target.dataset.sig = html;
    target.innerHTML = "";
    if (channels.length === 0) {
      const li = document.createElement("li");
      li.className = "channel-empty";
      li.textContent = t(emptyKey);
      target.appendChild(li);
      return;
    }
    channels.forEach((c) => target.appendChild(channelEntry(c)));
  }

  async function loadChannels() {
    try {
      const res = await fetch("/api/channels");
      const data = await res.json();
      const channels = (data && data.channels) || [];
      const mine = (data && data.mine) || [];
      const joined = (data && data.joined) || [];
      renderList(channelList, channels, "landing.no_public");
      if (mine.length > 0) {
        mineCard.hidden = false;
        renderList(mineList, mine, "landing.no_owned");
      } else {
        mineCard.hidden = true;
      }
      if (joined.length > 0) {
        joinedCard.hidden = false;
        renderList(joinedList, joined, "landing.no_joined");
      } else {
        joinedCard.hidden = true;
      }
    } catch (e) {
      channelList.dataset.sig = "";
      channelList.innerHTML = "";
      const li = document.createElement("li");
      li.className = "channel-empty";
      li.textContent = t("landing.load_err");
      channelList.appendChild(li);
    }
  }

  // The directory refreshes itself: a steady poll keeps online counts and new
  // channels current without a manual button.
  loadMe();
  loadChannels();
  setInterval(loadChannels, 4000);

  // Re-render language-dependent dynamic text when the language changes.
  document.addEventListener("nekodrop:langchange", function () {
    renderMe();
    renderMigration();
    channelList.dataset.sig = "";
    mineList.dataset.sig = "";
    joinedList.dataset.sig = "";
    loadChannels();
  });
})();
