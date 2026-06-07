// NekoDrop internationalisation (i18n).
//
// A tiny, dependency-free translation layer shared by every page. Static markup
// is translated declaratively through data-i18n* attributes; dynamic strings in
// the page scripts go through window.NekoI18n.t(). The chosen language is
// remembered in localStorage and defaults to the browser's preference.
(function () {
  "use strict";

  const DICT = {
    en: {
      "lang.label": "Language",
      // Landing
      "app.tagline": "Drop a code, share files and messages instantly.",
      "landing.you_are": "You are",
      "landing.guest_hint": "(unnamed guest — set a name below)",
      "landing.code_label": "Channel code or path",
      "landing.code_ph": "e.g. team-cats",
      "landing.name_label": "Your name",
      "landing.optional": "(optional)",
      "landing.name_ph": "Guest",
      "landing.join": "Join channel",
      "landing.random": "Or create a random room",
      "landing.create_summary": "Create a channel you control",
      "landing.c_name_label": "Channel name",
      "landing.c_name_ph": "Team Cats",
      "landing.desc_label": "Description",
      "landing.desc_ph": "What is this channel about?",
      "landing.visibility": "Visibility",
      "landing.vis_public": "Public — anyone can read, join to speak",
      "landing.vis_private": "Private — members only",
      "landing.show_public": "Show in the public channel list",
      "landing.allow_join": "Allow people to join",
      "landing.require_approval": "Require approval to join",
      "landing.allow_speak": "Allow members to speak",
      "landing.create": "Create channel",
      "landing.create_err": "Could not create channel.",
      "landing.net_err": "Network error. Please try again.",
      "landing.hint": "Everyone who enters the same code joins the same channel. Share text and files; other members can download whatever you drop.",
      "landing.your_channels": "Your channels",
      "landing.public_channels": "Public channels",
      "landing.loading": "Loading…",
      "landing.no_public": "No public channels yet. Create one above!",
      "landing.load_err": "Could not load channels.",
      "landing.no_owned": "You haven't created any channels yet.",
      "directory.online": "online",
      "directory.member": "member",
      "directory.members": "members",
      "badge.private": "🔒 private",
      // Room
      "room.leave": "Leave channel",
      "room.online_title": "People online",
      "room.mentions": "Mentions",
      "room.join": "Join",
      "room.requested": "Requested ✓",
      "room.settings": "Channel settings",
      "room.connecting": "connecting…",
      "room.connected": "connected",
      "room.reconnecting": "reconnecting…",
      "room.private_status": "private channel",
      "room.dissolved_status": "channel dissolved",
      "room.empty": "No messages yet. Say hi or drop a file!",
      "room.msg_ph": "Type a message… use @ to mention",
      "room.send_disabled": "Sending is disabled in this channel",
      "room.join_to_send": "Join this channel to send messages",
      "room.preview": "preview",
      "room.send": "Send",
      "room.share_file": "Share a file",
      "room.attach_hint": "Paste or drop images & files to attach",
      "room.attach_send": "Send attachments",
      "room.attach_clear": "Clear",
      "room.private_notice": "This channel is private. Join to view its contents.",
      "room.join_pending": "Your request to join was sent for approval.",
      "room.dissolved_notice": "This channel has been dissolved by its owner. The path is now free to reuse.",
      "room.uploading": "Uploading",
      "room.shared": "Shared",
      "room.upload_failed": "Upload failed",
      // Settings drawer
      "settings.title": "Channel settings",
      "settings.name": "Name",
      "settings.desc": "Description",
      "settings.visibility": "Visibility",
      "settings.public": "Public",
      "settings.private": "Private",
      "settings.show_public": "Show in public channel list",
      "settings.allow_join": "Allow people to join",
      "settings.require_approval": "Require approval to join",
      "settings.allow_speak": "Allow members to speak",
      "settings.save": "Save settings",
      "settings.saved": "Settings saved.",
      "settings.announce_h": "Post an announcement",
      "settings.announce_ph": "Write an announcement…",
      "settings.announce_btn": "Publish announcement",
      "settings.pending_h": "Pending join requests",
      "settings.members_h": "Members & moderation",
      "settings.members_hint": "Manage members below, or click a name in chat to mention them.",
      "settings.no_members": "No members yet.",
      "settings.danger_h": "Danger zone",
      "settings.dissolve": "Dissolve this channel",
      "settings.dissolve_hint": "The path is released for reuse; the channel ID is retired forever.",
      "settings.dissolve_confirm": "Dissolve this channel? The path is freed and the channel ID is retired forever.",
      // Moderation
      "mod.approve": "Approve",
      "mod.reject": "Reject",
      "mod.admin": "Admin",
      "mod.unadmin": "Unadmin",
      "mod.mute": "Mute",
      "mod.unmute": "Unmute",
      "mod.kick": "Kick",
      "mod.ban": "Ban",
      "mod.unban": "Unban",
      "mod.owner": "owner",
      "mod.admin_badge": "admin",
      "mod.muted_badge": "muted",
      "mod.banned_badge": "banned",
      "mod.done": "Done.",
    },
    zh: {
      "lang.label": "語言",
      "app.tagline": "輸入代碼，即時分享檔案與訊息。",
      "landing.you_are": "您是",
      "landing.guest_hint": "（未命名訪客 — 請在下方設定名稱）",
      "landing.code_label": "頻道代碼或路徑",
      "landing.code_ph": "例如 team-cats",
      "landing.name_label": "您的名稱",
      "landing.optional": "（選填）",
      "landing.name_ph": "訪客",
      "landing.join": "加入頻道",
      "landing.random": "或建立隨機房間",
      "landing.create_summary": "建立由您掌控的頻道",
      "landing.c_name_label": "頻道名稱",
      "landing.c_name_ph": "Team Cats",
      "landing.desc_label": "描述",
      "landing.desc_ph": "這個頻道是關於什麼的？",
      "landing.visibility": "可見性",
      "landing.vis_public": "公開 — 任何人可閱讀，加入後可發言",
      "landing.vis_private": "私人 — 僅限成員",
      "landing.show_public": "顯示在公開頻道列表",
      "landing.allow_join": "允許他人加入",
      "landing.require_approval": "加入需要審核",
      "landing.allow_speak": "允許成員發言",
      "landing.create": "建立頻道",
      "landing.create_err": "無法建立頻道。",
      "landing.net_err": "網路錯誤，請再試一次。",
      "landing.hint": "輸入相同代碼的人都會加入同一個頻道。分享文字與檔案；其他成員可以下載您上傳的任何內容。",
      "landing.your_channels": "您的頻道",
      "landing.public_channels": "公開頻道",
      "landing.loading": "載入中…",
      "landing.no_public": "目前還沒有公開頻道。在上方建立一個吧！",
      "landing.load_err": "無法載入頻道。",
      "landing.no_owned": "您尚未建立任何頻道。",
      "directory.online": "在線",
      "directory.member": "位成員",
      "directory.members": "位成員",
      "badge.private": "🔒 私人",
      "room.leave": "離開頻道",
      "room.online_title": "在線人數",
      "room.mentions": "提及",
      "room.join": "加入",
      "room.requested": "已申請 ✓",
      "room.settings": "頻道設定",
      "room.connecting": "連線中…",
      "room.connected": "已連線",
      "room.reconnecting": "重新連線中…",
      "room.private_status": "私人頻道",
      "room.dissolved_status": "頻道已解散",
      "room.empty": "還沒有訊息。打聲招呼或上傳檔案吧！",
      "room.msg_ph": "輸入訊息… 使用 @ 提及他人",
      "room.send_disabled": "此頻道已停用發言",
      "room.join_to_send": "加入此頻道以發送訊息",
      "room.preview": "預覽",
      "room.send": "發送",
      "room.share_file": "分享檔案",
      "room.attach_hint": "貼上或拖放圖片與檔案以附加",
      "room.attach_send": "發送附件",
      "room.attach_clear": "清除",
      "room.private_notice": "此頻道為私人頻道。加入以查看內容。",
      "room.join_pending": "您的加入申請已送出待審核。",
      "room.dissolved_notice": "此頻道已被擁有者解散。該路徑現在可重新使用。",
      "room.uploading": "上傳中",
      "room.shared": "已分享",
      "room.upload_failed": "上傳失敗",
      "settings.title": "頻道設定",
      "settings.name": "名稱",
      "settings.desc": "描述",
      "settings.visibility": "可見性",
      "settings.public": "公開",
      "settings.private": "私人",
      "settings.show_public": "顯示在公開頻道列表",
      "settings.allow_join": "允許他人加入",
      "settings.require_approval": "加入需要審核",
      "settings.allow_speak": "允許成員發言",
      "settings.save": "儲存設定",
      "settings.saved": "設定已儲存。",
      "settings.announce_h": "發布公告",
      "settings.announce_ph": "撰寫公告…",
      "settings.announce_btn": "發布公告",
      "settings.pending_h": "待審核的加入申請",
      "settings.members_h": "成員與管理",
      "settings.members_hint": "在下方管理成員，或點擊聊天中的名稱以提及。",
      "settings.no_members": "目前還沒有成員。",
      "settings.danger_h": "危險區域",
      "settings.dissolve": "解散此頻道",
      "settings.dissolve_hint": "路徑將被釋放以供重用；頻道 ID 將永久退役。",
      "settings.dissolve_confirm": "確定要解散此頻道嗎？路徑將被釋放，頻道 ID 將永久退役。",
      "mod.approve": "核准",
      "mod.reject": "拒絕",
      "mod.admin": "設為管理",
      "mod.unadmin": "取消管理",
      "mod.mute": "禁言",
      "mod.unmute": "取消禁言",
      "mod.kick": "踢出",
      "mod.ban": "封禁",
      "mod.unban": "解除封禁",
      "mod.owner": "擁有者",
      "mod.admin_badge": "管理員",
      "mod.muted_badge": "已禁言",
      "mod.banned_badge": "已封禁",
      "mod.done": "已完成。",
    },
  };

  const STORAGE_KEY = "nekodrop.lang";

  function detect() {
    try {
      const saved = localStorage.getItem(STORAGE_KEY);
      if (saved && DICT[saved]) return saved;
    } catch (e) {
      /* ignore */
    }
    const nav = (navigator.language || "en").toLowerCase();
    if (nav.indexOf("zh") === 0) return "zh";
    return "en";
  }

  let lang = detect();

  function t(key, vars) {
    const table = DICT[lang] || DICT.en;
    let s = table[key];
    if (s == null) s = (DICT.en[key] != null ? DICT.en[key] : key);
    if (vars) {
      Object.keys(vars).forEach((k) => {
        s = s.replace(new RegExp("\\{" + k + "\\}", "g"), vars[k]);
      });
    }
    return s;
  }

  function apply(root) {
    const scope = root || document;
    scope.querySelectorAll("[data-i18n]").forEach((el) => {
      el.textContent = t(el.getAttribute("data-i18n"));
    });
    scope.querySelectorAll("[data-i18n-placeholder]").forEach((el) => {
      el.setAttribute("placeholder", t(el.getAttribute("data-i18n-placeholder")));
    });
    scope.querySelectorAll("[data-i18n-title]").forEach((el) => {
      el.setAttribute("title", t(el.getAttribute("data-i18n-title")));
    });
    document.documentElement.setAttribute("lang", lang === "zh" ? "zh-Hant" : "en");
  }

  function setLang(next) {
    if (!DICT[next]) return;
    lang = next;
    try {
      localStorage.setItem(STORAGE_KEY, next);
    } catch (e) {
      /* ignore */
    }
    apply();
    document.dispatchEvent(new CustomEvent("nekodrop:langchange", { detail: { lang: lang } }));
  }

  // Wire any language <select> on the page and keep it in sync.
  function bindSelectors() {
    document.querySelectorAll("select.lang-select").forEach((sel) => {
      sel.value = lang;
      sel.addEventListener("change", () => setLang(sel.value));
    });
  }

  window.NekoI18n = {
    t: t,
    apply: apply,
    setLang: setLang,
    get lang() {
      return lang;
    },
  };

  function init() {
    apply();
    bindSelectors();
  }
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
