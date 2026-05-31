// Landing page: join an existing room or create a random one.
(function () {
  "use strict";

  const form = document.getElementById("join-form");
  const codeInput = document.getElementById("code");
  const nameInput = document.getElementById("name");
  const randomButton = document.getElementById("random");

  // Restore a previously used display name.
  try {
    const saved = localStorage.getItem("nekodrop.name");
    if (saved) nameInput.value = saved;
  } catch (e) {
    /* ignore storage errors */
  }

  function go(code) {
    const slug = slugify(code);
    if (!slug) {
      codeInput.focus();
      return;
    }
    try {
      const name = nameInput.value.trim();
      if (name) localStorage.setItem("nekodrop.name", name);
    } catch (e) {
      /* ignore storage errors */
    }
    window.location.href = "/r/" + encodeURIComponent(slug);
  }

  // Mirror the server's NormalizeKey so the URL matches the room key.
  function slugify(raw) {
    return (raw || "")
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9\s\-_/]+/g, "")
      .replace(/[\s\-_/]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 128);
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
})();
