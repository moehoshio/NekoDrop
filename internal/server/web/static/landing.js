// Landing page: identity, join/create channels, and the public directory.
(function () {
  "use strict";

  const form = document.getElementById("join-form");
  const codeInput = document.getElementById("code");
  const nameInput = document.getElementById("name");
  const randomButton = document.getElementById("random");
  const meLabel = document.getElementById("me-label");
  const createForm = document.getElementById("create-form");
  const createError = document.getElementById("create-error");
  const channelList = document.getElementById("channel-list");
  const refreshButton = document.getElementById("refresh");

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
  async function loadMe() {
    try {
      const res = await fetch("/api/me", { method: "GET" });
      if (res.ok) {
        me = await res.json();
        meLabel.textContent = me.name + "#" + me.uid;
      }
    } catch (e) {
      meLabel.textContent = "anonymous";
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
        createError.textContent = (await res.text()).trim() || "Could not create channel.";
        createError.hidden = false;
        return;
      }
      const channel = await res.json();
      window.location.href = "/r/" + encodeURIComponent(channel.key);
    } catch (err) {
      createError.textContent = "Network error. Please try again.";
      createError.hidden = false;
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
      lock.textContent = "🔒 private";
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
    online.textContent = "🟢 " + c.online + " online";
    const members = document.createElement("span");
    members.textContent = c.members + " member" + (c.members === 1 ? "" : "s");
    meta.appendChild(online);
    meta.appendChild(members);
    link.appendChild(meta);

    li.appendChild(link);
    return li;
  }

  async function loadChannels() {
    channelList.innerHTML = "";
    try {
      const res = await fetch("/api/channels");
      const data = await res.json();
      const channels = (data && data.channels) || [];
      if (channels.length === 0) {
        const li = document.createElement("li");
        li.className = "channel-empty";
        li.textContent = "No public channels yet. Create one above!";
        channelList.appendChild(li);
        return;
      }
      channels.forEach((c) => channelList.appendChild(channelEntry(c)));
    } catch (e) {
      const li = document.createElement("li");
      li.className = "channel-empty";
      li.textContent = "Could not load channels.";
      channelList.appendChild(li);
    }
  }

  refreshButton.addEventListener("click", loadChannels);

  loadMe();
  loadChannels();
})();
