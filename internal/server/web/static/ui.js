// NekoDrop shared UI primitives.
//
// A tiny, dependency-free replacement for the browser's native window.confirm /
// window.alert dialogs, styled to match the rest of the app. Everything is
// promise-based so callers can `await` a decision, and every string is passed in
// by the caller (already localized) so this layer stays independent of i18n.
//
// Usage:
//   await NekoUI.confirm({ title, body, confirmLabel, cancelLabel, danger })
//     -> resolves true (confirmed) or false (cancelled/dismissed)
//   await NekoUI.confirm({ ..., checkbox: { label, checked } })
//     -> resolves { ok: boolean, checked: boolean }
//   await NekoUI.alert({ title, body, confirmLabel })
//     -> resolves once acknowledged
(function () {
  "use strict";

  let overlay = null;
  let card = null;
  let titleEl = null;
  let bodyEl = null;
  let checkRow = null;
  let checkInput = null;
  let checkLabel = null;
  let actions = null;
  let cancelBtn = null;
  let confirmBtn = null;
  let active = null; // { resolve, hasCheckbox, hasCancel }
  let lastFocus = null;

  function build() {
    overlay = document.createElement("div");
    overlay.className = "modal";
    overlay.setAttribute("role", "dialog");
    overlay.setAttribute("aria-modal", "true");
    overlay.hidden = true;

    card = document.createElement("div");
    card.className = "modal-card";

    titleEl = document.createElement("h2");
    titleEl.className = "modal-title";

    bodyEl = document.createElement("p");
    bodyEl.className = "modal-body";

    checkRow = document.createElement("label");
    checkRow.className = "modal-check check";
    checkRow.hidden = true;
    checkInput = document.createElement("input");
    checkInput.type = "checkbox";
    checkLabel = document.createElement("span");
    checkRow.appendChild(checkInput);
    checkRow.appendChild(checkLabel);

    actions = document.createElement("div");
    actions.className = "modal-actions";
    cancelBtn = document.createElement("button");
    cancelBtn.type = "button";
    cancelBtn.className = "ghost-button modal-cancel";
    confirmBtn = document.createElement("button");
    confirmBtn.type = "button";
    confirmBtn.className = "pill-button modal-confirm";
    actions.appendChild(cancelBtn);
    actions.appendChild(confirmBtn);

    card.appendChild(titleEl);
    card.appendChild(bodyEl);
    card.appendChild(checkRow);
    card.appendChild(actions);
    overlay.appendChild(card);
    document.body.appendChild(overlay);

    cancelBtn.addEventListener("click", () => finish(false));
    confirmBtn.addEventListener("click", () => finish(true));
    overlay.addEventListener("mousedown", function (e) {
      // A click on the dimmed backdrop (never the card) cancels.
      if (e.target === overlay) finish(false);
    });
    document.addEventListener("keydown", onKey, true);
  }

  function onKey(e) {
    if (!active) return;
    if (e.key === "Escape") {
      e.preventDefault();
      finish(active.hasCancel ? false : true);
    } else if (e.key === "Enter") {
      e.preventDefault();
      finish(true);
    }
  }

  function finish(ok) {
    if (!active) return;
    const a = active;
    active = null;
    overlay.hidden = true;
    card.classList.remove("danger");
    const result = a.hasCheckbox ? { ok: ok, checked: checkInput.checked } : ok;
    if (lastFocus && typeof lastFocus.focus === "function") {
      try { lastFocus.focus(); } catch (_) { /* ignore */ }
    }
    a.resolve(result);
  }

  function open(opts, hasCancel) {
    if (!overlay) build();
    // A second dialog resolves the first as a dismissal so state never leaks.
    if (active) finish(false);
    opts = opts || {};

    titleEl.textContent = opts.title || "";
    titleEl.hidden = !opts.title;
    bodyEl.textContent = opts.body || "";
    bodyEl.hidden = !opts.body;

    const hasCheckbox = !!(opts.checkbox && opts.checkbox.label);
    if (hasCheckbox) {
      checkLabel.textContent = opts.checkbox.label;
      checkInput.checked = !!opts.checkbox.checked;
    }
    checkRow.hidden = !hasCheckbox;

    confirmBtn.textContent = opts.confirmLabel || "OK";
    confirmBtn.className = "modal-confirm " + (opts.danger ? "danger-button" : "pill-button");
    card.classList.toggle("danger", !!opts.danger);
    cancelBtn.textContent = opts.cancelLabel || "Cancel";
    cancelBtn.hidden = !hasCancel;

    lastFocus = document.activeElement;
    overlay.hidden = false;
    // Focus the least destructive default: cancel when present, else confirm.
    (hasCancel && opts.danger ? cancelBtn : confirmBtn).focus();

    return new Promise(function (resolve) {
      active = { resolve: resolve, hasCheckbox: hasCheckbox, hasCancel: hasCancel };
    });
  }

  window.NekoUI = {
    confirm: function (opts) { return open(opts, true); },
    alert: function (opts) { return open(opts, false); },
  };
})();
