(() => {
  const createIcons = () => window.lucide && window.lucide.createIcons();
  const csrfToken = document.querySelector('meta[name="csrf-token"]')?.content || "";
  const csrfHeaders = (headers = {}) => csrfToken ? { ...headers, "X-CSRF-Token": csrfToken } : headers;
  const ensureCSRFFormToken = (form) => {
    if (!(form instanceof HTMLFormElement) || !csrfToken || ["get", "dialog"].includes((form.method || "get").toLowerCase())) return;
    let input = form.querySelector('input[name="csrf_token"]');
    if (!input) {
      input = document.createElement("input");
      input.type = "hidden";
      input.name = "csrf_token";
      form.appendChild(input);
    }
    input.value = csrfToken;
  };
  const escapeHTML = (value) => String(value || "").replace(/[&<>"']/g, (char) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  })[char]);
  const markdownLabel = (value) => String(value || "attachment").replace(/\\/g, "\\\\").replace(/]/g, "\\]");
  const safeInlineImage = (contentType) => ["image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/bmp"].includes(String(contentType || "").toLowerCase());
  const attachmentMarkdownSnippet = (object) => {
    const ref = object.ref || "";
    const label = markdownLabel(object.filename || ref || "attachment");
    return safeInlineImage(object.content_type) ? `![${label}](${ref})` : `[${label}](${ref})`;
  };
  const formatBytes = (bytes) => {
    const n = Number(bytes) || 0;
    if (n < 1024) return `${n} B`;
    const units = ["KB", "MB", "GB", "TB"];
    let value = n;
    for (const unit of units) {
      value = value / 1024;
      if (value < 1024) return `${value >= 10 ? value.toFixed(0) : value.toFixed(1)} ${unit}`;
    }
    return `${(value / 1024).toFixed(0)} PB`;
  };
  const localTimeFormatter = new Intl.DateTimeFormat(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
    timeZoneName: "short",
  });
  const localizeTime = (element) => {
    const value = new Date(element.getAttribute("datetime") || "");
    if (Number.isNaN(value.getTime())) return;
    const formatted = localTimeFormatter.format(value);
    element.textContent = formatted;
    element.title = formatted;
  };
  const localizeTimes = (root = document) => {
    if (root instanceof Element && root.matches("[data-local-time]")) localizeTime(root);
    root.querySelectorAll("[data-local-time]").forEach(localizeTime);
  };
  const tooltipSelector = "[data-tooltip], button[aria-label], a[aria-label], summary[aria-label], label[aria-label], input[type='button'][aria-label], input[type='submit'][aria-label]";
  let appTooltip = null;
  let activeTooltipTarget = null;
  let tooltipFrame = null;
  let previousTooltipDescription = null;
  const ensureAppTooltip = () => {
    if (appTooltip) return appTooltip;
    appTooltip = document.createElement("div");
    appTooltip.id = "app-tooltip";
    appTooltip.setAttribute("role", "tooltip");
    appTooltip.setAttribute("data-app-tooltip", "");
    appTooltip.hidden = true;
    document.body.appendChild(appTooltip);
    return appTooltip;
  };
  const textNodeIsVisible = (node, control) => {
    let element = node.parentElement;
    while (element && control.contains(element)) {
      const style = window.getComputedStyle(element);
      if (
        element.getAttribute("aria-hidden") === "true"
        || style.display === "none"
        || style.visibility === "hidden"
        || Number(style.opacity) === 0
        || style.clip === "rect(0px, 0px, 0px, 0px)"
        || style.clipPath === "inset(50%)"
      ) return false;
      if (element === control) break;
      element = element.parentElement;
    }
    const range = document.createRange();
    range.selectNodeContents(node);
    return Array.from(range.getClientRects()).some((rect) => rect.width > 0 && rect.height > 0);
  };
  const hasVisibleControlText = (control) => {
    const walker = document.createTreeWalker(control, NodeFilter.SHOW_TEXT);
    let node = walker.nextNode();
    while (node) {
      if (node.textContent.trim() && textNodeIsVisible(node, control)) return true;
      node = walker.nextNode();
    }
    return false;
  };
  const closestTooltipTarget = (source) => source instanceof Element ? source.closest(tooltipSelector) : null;
  const tooltipTargetFor = (source) => {
    const target = closestTooltipTarget(source);
    if (!target || target.hasAttribute("data-tooltip-disabled") || hasVisibleControlText(target)) return null;
    const label = (target.getAttribute("data-tooltip") || target.getAttribute("aria-label") || "").trim();
    return label ? { target, label } : null;
  };
  const restoreTooltipDescription = (target) => {
    if (!target) return;
    if (previousTooltipDescription === null) {
      target.removeAttribute("aria-describedby");
    } else {
      target.setAttribute("aria-describedby", previousTooltipDescription);
    }
    previousTooltipDescription = null;
  };
  const hideAppTooltip = () => {
    if (tooltipFrame !== null) {
      window.cancelAnimationFrame(tooltipFrame);
      tooltipFrame = null;
    }
    if (activeTooltipTarget) {
      activeTooltipTarget.removeAttribute("data-app-tooltip-target");
      restoreTooltipDescription(activeTooltipTarget);
    }
    activeTooltipTarget = null;
    if (!appTooltip) return;
    appTooltip.removeAttribute("data-visible");
    appTooltip.hidden = true;
  };
  const showAppTooltip = (match) => {
    if (!match) return;
    const tooltip = ensureAppTooltip();
    if (activeTooltipTarget === match.target) {
      tooltip.textContent = match.label;
      return;
    }
    hideAppTooltip();
    activeTooltipTarget = match.target;
    previousTooltipDescription = match.target.getAttribute("aria-describedby");
    const descriptions = new Set((previousTooltipDescription || "").split(/\s+/).filter(Boolean));
    descriptions.add(tooltip.id);
    match.target.setAttribute("aria-describedby", Array.from(descriptions).join(" "));
    match.target.setAttribute("data-app-tooltip-target", "");
    tooltip.textContent = match.label;
    tooltip.hidden = false;
    tooltip.removeAttribute("data-visible");
    tooltipFrame = window.requestAnimationFrame(() => {
      tooltipFrame = null;
      if (activeTooltipTarget === match.target) tooltip.setAttribute("data-visible", "");
    });
  };
  const resizeTextarea = (textarea) => {
    const minRows = Number.parseInt(textarea.dataset.autogrowMinRows || textarea.getAttribute("rows") || "2", 10);
    textarea.dataset.autogrowMinRows = String(minRows);
    textarea.rows = minRows;
    while (textarea.scrollHeight > textarea.clientHeight && textarea.rows < 100) textarea.rows += 1;
  };
  const resizeTextareas = (root = document) => {
    root.querySelectorAll("[data-autogrow-textarea]").forEach(resizeTextarea);
  };
  const syncCheckboxReveal = (reveal) => {
    const toggle = reveal.querySelector("[data-checkbox-reveal-toggle]");
    const panel = reveal.querySelector("[data-checkbox-reveal-panel]");
    if (!toggle || !panel) return;
    const open = toggle.checked;
    panel.hidden = !open;
    toggle.setAttribute("aria-expanded", open ? "true" : "false");
    panel.querySelectorAll("input, select, textarea").forEach((control) => {
      control.disabled = !open;
      if (!open) control.value = "";
    });
  };
  const syncCheckboxReveals = (root = document) => {
    if (root instanceof Element && root.matches("[data-checkbox-reveal]")) {
      syncCheckboxReveal(root);
    }
    root.querySelectorAll("[data-checkbox-reveal]").forEach(syncCheckboxReveal);
  };
  const syncDisclosureIcon = (toggle, open) => {
    const icon = toggle.querySelector("[data-disclosure-icon]");
    if (!icon) return;
    icon.setAttribute("data-lucide", open ? "chevron-up" : "chevron-down");
    createIcons();
  };
  const setDisclosureOpen = (toggle, open) => {
    const targetID = toggle.getAttribute("aria-controls");
    const panel = targetID && document.getElementById(targetID);
    if (!panel) return;
    panel.hidden = !open;
    toggle.setAttribute("aria-expanded", open ? "true" : "false");
    const label = toggle.querySelector("[data-disclosure-label]");
    if (label) {
      const text = open ? "Hide issues" : "Show issues";
      label.textContent = text;
      toggle.setAttribute("aria-label", text);
    }
    syncDisclosureIcon(toggle, open);
  };
  let clientModalTrigger = null;
  const modalFocusableElements = (modal) => Array.from(modal.querySelectorAll("button:not([disabled]), [href], input:not([type='hidden']):not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex='-1'])"))
    .filter((element) => element instanceof HTMLElement && !element.hasAttribute("hidden") && element.getAttribute("aria-hidden") !== "true");
  const trapModalFocus = (event, modal) => {
    if (event.key !== "Tab" || !(modal instanceof HTMLElement)) return false;
    const focusable = modalFocusableElements(modal);
    if (focusable.length === 0) {
      event.preventDefault();
      modal.focus();
      return true;
    }
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && (document.activeElement === first || !modal.contains(document.activeElement))) {
      event.preventDefault();
      last.focus();
      return true;
    }
    if (!event.shiftKey && (document.activeElement === last || !modal.contains(document.activeElement))) {
      event.preventDefault();
      first.focus();
      return true;
    }
    return false;
  };
  const focusClientModal = (modal) => {
    if (!(modal instanceof HTMLElement)) return;
    if (!(clientModalTrigger instanceof HTMLElement) || !clientModalTrigger.isConnected) {
      clientModalTrigger = document.querySelector(`[data-modal-open="${modal.id}"], [data-modal-return="${modal.id}"]`);
    }
    if (clientModalTrigger instanceof HTMLElement) clientModalTrigger.setAttribute("aria-expanded", "true");
    const target = modal.querySelector("[autofocus]") || modal.querySelector("input:not([type='hidden']), select, textarea, button:not([disabled]), [href]");
    if (target instanceof HTMLElement) {
      target.focus();
      return;
    }
    modal.focus();
  };
  const setClientModalOpen = (modal, open, trigger = null) => {
    if (!(modal instanceof HTMLElement)) return;
    modal.classList.toggle("hidden", !open);
    modal.classList.toggle("grid", open);
    document.querySelectorAll(`[data-modal-open="${modal.id}"], [data-modal-return="${modal.id}"]`).forEach((button) => {
      button.setAttribute("aria-expanded", open ? "true" : "false");
    });
    if (open) {
      clientModalTrigger = trigger || document.querySelector(`[data-modal-open="${modal.id}"], [data-modal-return="${modal.id}"]`);
      window.setTimeout(() => focusClientModal(modal), 0);
      return;
    }
    modal.querySelectorAll("form").forEach((form) => form.reset());
    const returnFocus = clientModalTrigger;
    clientModalTrigger = null;
    if (returnFocus instanceof HTMLElement) window.setTimeout(() => returnFocus.focus(), 0);
  };
  const showAttachmentStatus = (textarea, message) => {
    const status = textarea && textarea.closest("section") && textarea.closest("section").querySelector("[data-attachment-status]");
    if (!status) return;
    status.textContent = message;
    status.classList.toggle("hidden", !message);
  };
  const insertAttachmentMarkdown = (textarea, attachment) => {
    const object = attachment.object || {};
    const snippet = attachmentMarkdownSnippet(object);
    const start = textarea.selectionStart ?? textarea.value.length;
    const end = textarea.selectionEnd ?? textarea.value.length;
    const prefix = start > 0 && textarea.value[start - 1] !== "\n" ? "\n" : "";
    const suffix = textarea.value[end] && textarea.value[end] !== "\n" ? "\n" : "";
    textarea.value = `${textarea.value.slice(0, start)}${prefix}${snippet}${suffix}${textarea.value.slice(end)}`;
    const next = start + prefix.length + snippet.length + suffix.length;
    textarea.selectionStart = next;
    textarea.selectionEnd = next;
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
  };
  const attachmentRowHTML = (uploadUrl, attachment) => {
    const object = attachment.object || {};
    const ref = object.ref || "";
    const contentUrl = `${uploadUrl}/${encodeURIComponent(ref)}/content`;
    const inlineUrl = `${contentUrl}?inline=1`;
    const deleteUrl = `${uploadUrl}/${encodeURIComponent(ref)}`;
    const isImage = safeInlineImage(object.content_type);
    const icon = isImage ? "image" : "paperclip";
    const markdown = attachmentMarkdownSnippet(object);
    const preview = isImage
      ? `<a href="${escapeHTML(contentUrl)}" class="block h-12 w-16 shrink-0 overflow-hidden rounded-md border border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-950">
          <img src="${escapeHTML(inlineUrl)}" alt="" loading="lazy" class="h-full w-full object-cover">
        </a>`
      : `<span class="grid h-10 w-10 shrink-0 place-items-center rounded-md border border-slate-200 bg-white text-slate-500 dark:border-slate-800 dark:bg-slate-950 dark:text-slate-400">
          <i data-lucide="${icon}" class="h-4 w-4" aria-hidden="true"></i>
        </span>`;
    return `
      <div data-attachment-ref="${escapeHTML(ref)}" class="flex min-w-0 items-center gap-2 rounded-md border border-slate-200 bg-slate-50 px-2.5 py-2 text-xs dark:border-slate-800 dark:bg-slate-950/60">
        ${preview}
        <div class="min-w-0 flex-1">
          <a href="${escapeHTML(contentUrl)}" class="block truncate font-medium text-slate-700 hover:text-indigo-700 dark:text-slate-200 dark:hover:text-indigo-200">${escapeHTML(object.filename || ref)}</a>
          <div class="mt-1 flex min-w-0 flex-wrap items-center gap-2">
            <span class="shrink-0 text-[11px] text-slate-500 dark:text-slate-400">${escapeHTML(formatBytes(object.byte_size))}</span>
            <code class="truncate rounded bg-slate-100 px-1 py-0.5 font-mono text-[10px] text-slate-500 dark:bg-slate-900 dark:text-slate-400">${escapeHTML(ref)}</code>
          </div>
        </div>
        <button type="button" data-attachment-copy-markdown data-markdown="${escapeHTML(markdown)}" aria-label="Copy attachment Markdown" class="inline-flex h-6 shrink-0 items-center gap-1 rounded-md border border-slate-200 bg-white px-1.5 text-slate-600 hover:bg-slate-50 focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:ring-offset-2 dark:border-slate-800 dark:bg-slate-950 dark:text-slate-300 dark:hover:bg-slate-800 dark:focus:ring-offset-slate-900">
          <i data-lucide="copy" class="h-3.5 w-3.5" aria-hidden="true"></i>
          <span data-copy-label class="hidden text-[11px] font-medium sm:inline">Markdown</span>
        </button>
        <a href="${escapeHTML(contentUrl)}" aria-label="Download attachment" class="grid h-6 w-6 shrink-0 place-items-center rounded-md border border-slate-200 bg-white text-slate-600 hover:bg-slate-50 focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:ring-offset-2 dark:border-slate-800 dark:bg-slate-950 dark:text-slate-300 dark:hover:bg-slate-800 dark:focus:ring-offset-slate-900">
          <i data-lucide="download" class="h-3.5 w-3.5" aria-hidden="true"></i>
        </a>
        <button type="button" data-attachment-remove data-attachment-delete-url="${escapeHTML(deleteUrl)}" aria-label="Remove attachment" class="grid h-6 w-6 shrink-0 place-items-center rounded-md border border-rose-200 bg-white text-rose-600 hover:bg-rose-50 focus:outline-none focus:ring-2 focus:ring-rose-500 focus:ring-offset-2 dark:border-rose-900 dark:bg-slate-950 dark:text-rose-300 dark:hover:bg-rose-950/40 dark:focus:ring-offset-slate-900">
          <i data-lucide="trash-2" class="h-3.5 w-3.5" aria-hidden="true"></i>
        </button>
      </div>
    `;
  };
  const appendAttachmentRow = (textarea, attachment) => {
    const selector = textarea.dataset.attachmentList;
    const list = selector && document.querySelector(selector);
    if (!list) return;
    const rows = list.querySelector("[data-attachment-rows]");
    if (!rows) return;
    const uploadUrl = textarea.dataset.attachmentUploadUrl;
    rows.insertAdjacentHTML("beforeend", attachmentRowHTML(uploadUrl, attachment));
    list.classList.remove("hidden");
    const count = list.querySelector("[data-attachment-count]");
    if (count) count.textContent = String(rows.querySelectorAll("[data-attachment-ref]").length);
    createIcons();
  };
  const uploadAttachments = async (textarea, files) => {
    const uploadUrl = textarea.dataset.attachmentUploadUrl;
    if (!uploadUrl || files.length === 0) return;
    showAttachmentStatus(textarea, "");
    for (const file of files) {
      const data = new FormData();
      data.append("file", file);
      const res = await fetch(uploadUrl, {
        method: "POST",
        body: data,
        credentials: "same-origin",
        headers: csrfHeaders({ "Accept": "application/json" }),
      });
      if (!res.ok) {
        let message = "Attachment upload failed.";
        try {
          const body = await res.json();
          if (body && body.error) message = body.error;
        } catch (_) {}
        showAttachmentStatus(textarea, message);
        continue;
      }
      const attachment = await res.json();
      insertAttachmentMarkdown(textarea, attachment);
      appendAttachmentRow(textarea, attachment);
    }
  };
  const writeClipboardText = async (text) => {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(text);
      return;
    }
    const textarea = document.createElement("textarea");
    textarea.value = text;
    textarea.setAttribute("readonly", "");
    textarea.className = "clipboard-fallback";
    document.body.appendChild(textarea);
    textarea.select();
    try {
      document.execCommand("copy");
    } finally {
      textarea.remove();
    }
  };
  const setPasskeyStatus = (panel, message, kind = "error") => {
    const status = panel && panel.querySelector("[data-passkey-status]");
    if (!status) return;
    status.textContent = message || "";
    status.className = "mt-3 rounded-md border px-3 py-2 text-sm";
    if (!message) {
      status.classList.add("hidden");
      return;
    }
    status.classList.add(
      kind === "ok" ? "border-emerald-200" : "border-red-200",
      kind === "ok" ? "bg-emerald-50" : "bg-red-50",
      kind === "ok" ? "text-emerald-800" : "text-red-700",
      kind === "ok" ? "dark:border-emerald-900" : "dark:border-red-900",
      kind === "ok" ? "dark:bg-emerald-950" : "dark:bg-red-950",
      kind === "ok" ? "dark:text-emerald-200" : "dark:text-red-200",
    );
  };
  const setPasswordLoginStatus = (panel, message, kind = "error") => {
    const status = panel && panel.querySelector("[data-password-login-status]");
    if (!status) return;
    status.textContent = message || "";
    status.className = "mt-3 rounded-md border px-3 py-2 text-sm";
    if (!message) {
      status.classList.add("hidden");
      return;
    }
    status.classList.add(
      kind === "ok" ? "border-emerald-200" : "border-red-200",
      kind === "ok" ? "bg-emerald-50" : "bg-red-50",
      kind === "ok" ? "text-emerald-800" : "text-red-700",
      kind === "ok" ? "dark:border-emerald-900" : "dark:border-red-900",
      kind === "ok" ? "dark:bg-emerald-950" : "dark:bg-red-950",
      kind === "ok" ? "dark:text-emerald-200" : "dark:text-red-200",
    );
  };
  const setPasskeyBusy = (panel, busy) => {
    if (!panel) return;
    panel.querySelectorAll("[data-passkey-add], [data-passkey-revoke]").forEach((button) => {
      button.disabled = busy;
      button.classList.toggle("opacity-60", busy);
      button.classList.toggle("cursor-wait", busy);
    });
  };
  const setPasswordLoginBusy = (panel, busy) => {
    if (!panel) return;
    panel.querySelectorAll("[data-password-login-action]").forEach((button) => {
      button.disabled = busy;
      button.classList.toggle("opacity-60", busy);
      button.classList.toggle("cursor-wait", busy);
    });
  };
  const assertPasskeysSupported = () => {
    if (!window.PublicKeyCredential || !navigator.credentials) {
      throw new Error("This browser does not support passkeys.");
    }
  };
  const decodeBase64URL = (value) => {
    const base64 = String(value || "").replace(/-/g, "+").replace(/_/g, "/");
    const padded = base64 + "=".repeat((4 - (base64.length % 4)) % 4);
    const binary = window.atob(padded);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);
    return bytes.buffer;
  };
  const encodeBase64URL = (buffer) => {
    const bytes = new Uint8Array(buffer);
    let binary = "";
    for (const byte of bytes) binary += String.fromCharCode(byte);
    return window.btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
  };
  const parsePasskeyCreationOptions = (options) => {
    if (PublicKeyCredential.parseCreationOptionsFromJSON) {
      return PublicKeyCredential.parseCreationOptionsFromJSON(options);
    }
    const next = { ...options, challenge: decodeBase64URL(options.challenge) };
    next.user = { ...options.user, id: decodeBase64URL(options.user.id) };
    next.excludeCredentials = (options.excludeCredentials || []).map((credential) => ({
      ...credential,
      id: decodeBase64URL(credential.id),
    }));
    return next;
  };
  const parsePasskeyRequestOptions = (options) => {
    if (PublicKeyCredential.parseRequestOptionsFromJSON) {
      return PublicKeyCredential.parseRequestOptionsFromJSON(options);
    }
    const next = { ...options, challenge: decodeBase64URL(options.challenge) };
    next.allowCredentials = (options.allowCredentials || []).map((credential) => ({
      ...credential,
      id: decodeBase64URL(credential.id),
    }));
    return next;
  };
  const passkeyCredentialJSON = (credential) => {
    const response = credential.response;
    const out = {
      id: credential.id,
      rawId: encodeBase64URL(credential.rawId),
      type: credential.type,
      clientExtensionResults: credential.getClientExtensionResults ? credential.getClientExtensionResults() : {},
    };
    if (credential.authenticatorAttachment) out.authenticatorAttachment = credential.authenticatorAttachment;
    if (response.attestationObject) {
      out.response = {
        attestationObject: encodeBase64URL(response.attestationObject),
        clientDataJSON: encodeBase64URL(response.clientDataJSON),
      };
      if (response.getTransports) out.response.transports = response.getTransports();
      if (response.getAuthenticatorData) out.response.authenticatorData = encodeBase64URL(response.getAuthenticatorData());
      if (response.getPublicKey) {
        const key = response.getPublicKey();
        if (key) out.response.publicKey = encodeBase64URL(key);
      }
      if (response.getPublicKeyAlgorithm) out.response.publicKeyAlgorithm = response.getPublicKeyAlgorithm();
      return out;
    }
    out.response = {
      authenticatorData: encodeBase64URL(response.authenticatorData),
      clientDataJSON: encodeBase64URL(response.clientDataJSON),
      signature: encodeBase64URL(response.signature),
    };
    if (response.userHandle) out.response.userHandle = encodeBase64URL(response.userHandle);
    return out;
  };
  const postJSON = async (url, payload) => {
    const res = await fetch(url, {
      method: "POST",
      credentials: "same-origin",
      headers: csrfHeaders({ "Accept": "application/json", "Content-Type": "application/json" }),
      body: JSON.stringify(payload || {}),
    });
    let body = null;
    try {
      body = await res.json();
    } catch (_) {}
    if (!res.ok) throw new Error((body && body.error) || "Request failed.");
    return body || {};
  };
  const deleteJSON = async (url, payload) => {
    const res = await fetch(url, {
      method: "DELETE",
      credentials: "same-origin",
      headers: csrfHeaders({ "Accept": "application/json", "Content-Type": "application/json" }),
      body: JSON.stringify(payload || {}),
    });
    let body = null;
    try {
      body = await res.json();
    } catch (_) {}
    if (!res.ok) throw new Error((body && body.error) || "Request failed.");
    return body || {};
  };
  const pushApplicationServerKey = (value) => {
    const padding = "=".repeat((4 - value.length % 4) % 4);
    const raw = window.atob((value + padding).replace(/-/g, "+").replace(/_/g, "/"));
    return Uint8Array.from(raw, (char) => char.charCodeAt(0));
  };
  const setPushStatus = (panel, message, error = false) => {
    const status = panel && panel.querySelector("[data-push-status]");
    if (!status) return;
    status.textContent = message || "";
    status.classList.toggle("hidden", !message);
    status.classList.toggle("border-red-200", !!message && error);
    status.classList.toggle("bg-red-50", !!message && error);
    status.classList.toggle("text-red-700", !!message && error);
    status.classList.toggle("dark:border-red-900", !!message && error);
    status.classList.toggle("dark:bg-red-950", !!message && error);
    status.classList.toggle("dark:text-red-200", !!message && error);
    status.classList.toggle("border-emerald-200", !!message && !error);
    status.classList.toggle("bg-emerald-50", !!message && !error);
    status.classList.toggle("text-emerald-800", !!message && !error);
    status.classList.toggle("dark:border-emerald-900", !!message && !error);
    status.classList.toggle("dark:bg-emerald-950", !!message && !error);
    status.classList.toggle("dark:text-emerald-200", !!message && !error);
  };
  const setPushBusy = (panel, busy) => {
    panel.querySelectorAll("[data-push-enable], [data-push-disable]").forEach((button) => {
      button.disabled = busy;
    });
  };
  const setPushBrowserState = (panel, state, message) => {
    const label = panel.querySelector("[data-push-browser-state]");
    const enable = panel.querySelector("[data-push-enable]");
    const disable = panel.querySelector("[data-push-disable]");
    if (label) label.textContent = message;
    if (enable) enable.classList.toggle("hidden", state !== "available");
    if (disable) disable.classList.toggle("hidden", state !== "subscribed");
  };
  const pushRegistration = () => navigator.serviceWorker.register("/service-worker.js", { scope: "/" });
  const browserPushSupportError = () => {
    if (!window.isSecureContext) return "Browser notifications require HTTPS or localhost.";
    if (!("serviceWorker" in navigator) || !("PushManager" in window) || !("Notification" in window)) {
      return "This browser does not support Web Push notifications.";
    }
    return "";
  };
  const serverPushSubscriptionActive = async (endpoint) => {
    const res = await fetch(`/settings/push/subscription?endpoint=${encodeURIComponent(endpoint)}`, {
      credentials: "same-origin",
      headers: { "Accept": "application/json" },
    });
    if (!res.ok) throw new Error("Unable to check this browser subscription.");
    const body = await res.json();
    return body.subscribed === true;
  };
  const syncPushNotifications = async (root = document) => {
    const panel = root instanceof Element && root.matches("[data-push-notifications]")
      ? root
      : root.querySelector("[data-push-notifications]");
    if (!panel || panel.dataset.pushEnabled !== "true") return;
    const unsupported = browserPushSupportError();
    if (unsupported) {
      setPushBrowserState(panel, "unsupported", unsupported);
      return;
    }
    try {
      const registration = await pushRegistration();
      const subscription = await registration.pushManager.getSubscription();
      if (!subscription) {
        if (Notification.permission === "denied") {
          setPushBrowserState(panel, "blocked", "Notifications are blocked in this browser's site settings.");
        } else {
          setPushBrowserState(panel, "available", "Not subscribed on this browser.");
        }
        return;
      }
      if (await serverPushSubscriptionActive(subscription.endpoint)) {
        setPushBrowserState(panel, "subscribed", "Subscribed on this browser.");
      } else {
        setPushBrowserState(panel, "available", "This browser is not linked to your account. Select Enable to link it.");
      }
    } catch (err) {
      setPushBrowserState(panel, "error", "Unable to inspect this browser's notification subscription.");
      setPushStatus(panel, err && err.message ? err.message : "Notification check failed.", true);
    }
  };
  const enablePushNotifications = async (panel) => {
    const unsupported = browserPushSupportError();
    if (unsupported) throw new Error(unsupported);
    const registration = await pushRegistration();
    let subscription = await registration.pushManager.getSubscription();
    if (!subscription) {
      const permission = await Notification.requestPermission();
      if (permission !== "granted") throw new Error("Notification permission was not granted.");
      subscription = await registration.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: pushApplicationServerKey(panel.dataset.pushPublicKey || ""),
      });
    }
    await postJSON("/settings/push/subscription", subscription.toJSON());
    setPushStatus(panel, "Browser notifications enabled.");
    await syncPushNotifications(panel);
  };
  const disablePushNotifications = async (panel) => {
    const registration = await pushRegistration();
    const subscription = await registration.pushManager.getSubscription();
    if (!subscription) {
      setPushBrowserState(panel, "available", "Not subscribed on this browser.");
      return;
    }
    let serverError = null;
    try {
      await deleteJSON("/settings/push/subscription", { endpoint: subscription.endpoint });
    } catch (err) {
      serverError = err;
    }
    await subscription.unsubscribe();
    if (serverError) throw serverError;
    setPushStatus(panel, "Browser notifications disabled.");
    await syncPushNotifications(panel);
  };
  const createPasskeyReauthToken = async (panel) => {
    assertPasskeysSupported();
    const options = await postJSON("/settings/passkeys/reauth/passkey/options", {});
    const credential = await navigator.credentials.get({
      publicKey: parsePasskeyRequestOptions(options.publicKey),
      mediation: options.mediation || "optional",
    });
    if (!credential) throw new Error("Passkey verification was canceled.");
    const result = await postJSON("/settings/passkeys/reauth/passkey", {
      ceremony_id: options.ceremony_id,
      credential: passkeyCredentialJSON(credential),
    });
    panel.dataset.reauthToken = result.reauth_token || "";
    return panel.dataset.reauthToken;
  };
  const passkeyCanceledError = () => {
    const err = new Error("Passkey change canceled.");
    err.passkeyCanceled = true;
    return err;
  };
  const showPasskeyPasswordError = (modal, message) => {
    const error = modal && modal.querySelector("[data-passkey-password-error]");
    if (!error) return;
    error.textContent = message || "";
    error.classList.toggle("hidden", !message);
  };
  const createPasswordReauthToken = (panel) => new Promise((resolve, reject) => {
    const modal = panel.querySelector("[data-passkey-password-modal]");
    const form = modal && modal.querySelector("[data-passkey-password-form]");
    const input = modal && modal.querySelector("[data-passkey-current-password]");
    const submit = modal && modal.querySelector("[data-passkey-password-submit]");
    if (!modal || !form || !input || !submit) {
      reject(new Error("Current password form missing."));
      return;
    }
    const clientModal = document.querySelector("#passkey-create[data-client-modal]:not(.hidden)");
    const returnFocus = document.activeElement;

    const setSubmitBusy = (busy) => {
      submit.disabled = busy;
      submit.classList.toggle("opacity-60", busy);
      submit.classList.toggle("cursor-wait", busy);
    };
    const setModalOpen = (open) => {
      modal.classList.toggle("hidden", !open);
      modal.classList.toggle("grid", open);
    };
    let settled = false;
    const cleanup = () => {
      setModalOpen(false);
      if (clientModal instanceof HTMLElement) {
        clientModal.inert = false;
        clientModal.removeAttribute("aria-hidden");
      }
      input.value = "";
      setSubmitBusy(false);
      showPasskeyPasswordError(modal, "");
      form.removeEventListener("submit", onSubmit);
      modal.querySelectorAll("[data-passkey-password-cancel]").forEach((button) => button.removeEventListener("click", onCancel));
      modal.removeEventListener("click", onBackdropClick);
      document.removeEventListener("keydown", onKeydown);
      if (returnFocus instanceof HTMLElement && returnFocus.isConnected) window.setTimeout(() => returnFocus.focus(), 0);
    };
    const cancel = () => {
      if (settled) return;
      settled = true;
      cleanup();
      reject(passkeyCanceledError());
    };
    const onCancel = (event) => {
      event.preventDefault();
      event.stopPropagation();
      cancel();
    };
    const onBackdropClick = (event) => {
      if (event.target === modal) cancel();
    };
    const onKeydown = (event) => {
      if (trapModalFocus(event, modal)) return;
      if (event.key === "Escape" && !modal.classList.contains("hidden")) {
        event.preventDefault();
        cancel();
      }
    };
    const onSubmit = async (event) => {
      event.preventDefault();
      if (settled) return;
      const password = input.value || "";
      if (!password.trim()) {
        showPasskeyPasswordError(modal, "Enter your current password.");
        input.focus();
        return;
      }
      setSubmitBusy(true);
      try {
        const result = await postJSON("/settings/passkeys/reauth/password", { current_password: password });
        panel.dataset.reauthToken = result.reauth_token || "";
        const token = panel.dataset.reauthToken;
        settled = true;
        cleanup();
        resolve(token);
      } catch (err) {
        if (settled) return;
        showPasskeyPasswordError(modal, err && err.message ? err.message : "Current password not accepted.");
        input.select();
        setSubmitBusy(false);
      }
    };

    showPasskeyPasswordError(modal, "");
    input.value = "";
    if (clientModal instanceof HTMLElement) {
      clientModal.inert = true;
      clientModal.setAttribute("aria-hidden", "true");
    }
    setModalOpen(true);
    form.addEventListener("submit", onSubmit);
    modal.querySelectorAll("[data-passkey-password-cancel]").forEach((button) => button.addEventListener("click", onCancel));
    modal.addEventListener("click", onBackdropClick);
    document.addEventListener("keydown", onKeydown);
    window.setTimeout(() => input.focus(), 0);
  });
  const getPasskeyReauthToken = async (panel) => {
    if (panel.dataset.reauthToken) return panel.dataset.reauthToken;
    if (panel.querySelector("[data-passkey-password-modal]")) return createPasswordReauthToken(panel);
    return createPasskeyReauthToken(panel);
  };
  const addPasskey = async (panel) => {
    assertPasskeysSupported();
    const token = await getPasskeyReauthToken(panel);
    const name = panel.querySelector("[data-passkey-name]")?.value.trim() || "";
    let options;
    try {
      options = await postJSON("/settings/passkeys/options", { name, reauth_token: token });
    } finally {
      delete panel.dataset.reauthToken;
    }
    const credential = await navigator.credentials.create({
      publicKey: parsePasskeyCreationOptions(options.publicKey),
      mediation: options.mediation || "optional",
    });
    if (!credential) throw new Error("Passkey creation was canceled.");
    await postJSON("/settings/passkeys", {
      ceremony_id: options.ceremony_id,
      credential: passkeyCredentialJSON(credential),
    });
    window.location.reload();
  };
  const revokePasskey = async (panel, button) => {
    const token = await getPasskeyReauthToken(panel);
    const id = button.dataset.passkeyId || "";
    if (!id) throw new Error("Passkey id missing.");
    try {
      await postJSON(`/settings/passkeys/${encodeURIComponent(id)}/revoke`, { reauth_token: token });
    } finally {
      delete panel.dataset.reauthToken;
    }
    window.location.reload();
  };
  const updatePasswordLogin = async (button) => {
    const passkeysPanel = document.querySelector("[data-passkeys-panel]");
    const passwordPanel = button.closest("[data-password-login-panel]");
    if (!passkeysPanel) throw new Error("Passkey settings missing.");
    const token = await getPasskeyReauthToken(passkeysPanel);
    try {
      await postJSON("/settings/password-login", {
        enabled: button.dataset.passwordLoginEnabled === "true",
        reauth_token: token,
      });
    } finally {
      delete passkeysPanel.dataset.reauthToken;
    }
    setPasswordLoginStatus(passwordPanel, "Password login updated.", "ok");
    window.location.reload();
  };
  const showCopied = (button) => {
    const label = button.querySelector("[data-copy-label]");
    if (!label) return;
    const previous = label.textContent;
    label.textContent = "Copied";
    window.setTimeout(() => {
      label.textContent = previous || "Markdown";
    }, 1200);
  };
  const active = ["bg-indigo-50", "text-indigo-700", "ring-1", "ring-indigo-100", "dark:bg-indigo-950/50", "dark:text-indigo-200", "dark:ring-indigo-900"];
  const inactive = ["text-slate-600", "dark:text-slate-300"];
  const links = () => document.querySelectorAll("[data-sidebar-link]");
  const sidebarToggle = document.querySelector("[data-sidebar-collapse-toggle]");
  const sidebarStorageKey = "track-slash.sidebar.collapsed";
  const applySidebarCollapsed = (collapsed) => {
    document.documentElement.toggleAttribute("data-sidebar-collapsed", collapsed);
    if (!sidebarToggle) return;
    sidebarToggle.setAttribute("aria-expanded", collapsed ? "false" : "true");
    sidebarToggle.setAttribute("aria-label", collapsed ? "Expand sidebar" : "Collapse sidebar");
    const icon = sidebarToggle.querySelector("[data-sidebar-collapse-icon]");
    if (icon) {
      icon.setAttribute("data-lucide", collapsed ? "panel-left-open" : "panel-left-close");
      createIcons();
    }
  };
  if (sidebarToggle) {
    let collapsed = document.documentElement.hasAttribute("data-sidebar-collapsed");
    try {
      collapsed = window.localStorage.getItem(sidebarStorageKey) === "true";
    } catch (_) {}
    applySidebarCollapsed(collapsed);
    sidebarToggle.addEventListener("click", () => {
      const collapsed = !document.documentElement.hasAttribute("data-sidebar-collapsed");
      applySidebarCollapsed(collapsed);
      try {
        window.localStorage.setItem(sidebarStorageKey, collapsed ? "true" : "false");
      } catch (_) {}
    });
  }
  const mobileSidebar = document.querySelector("[data-mobile-sidebar]");
  const mobileSidebarToggle = document.querySelector("[data-mobile-sidebar-toggle]");
  const mobileSidebarClose = document.querySelector("[data-mobile-sidebar-close]");
  const mobileSidebarBackdrop = document.querySelector("[data-mobile-sidebar-backdrop]");
  const mobileAppBar = document.querySelector("[data-mobile-app-bar]");
  const mainContent = document.getElementById("main");
  const mobileSidebarBreakpoint = window.matchMedia("(min-width: 768px)");
  let mobileSidebarOpen = false;
  let mobileSidebarReturnFocus = mobileSidebarToggle;
  const syncMobileSidebar = () => {
    if (!mobileSidebar) return;
    const open = mobileSidebarOpen && !mobileSidebarBreakpoint.matches;
    const visible = open || mobileSidebarBreakpoint.matches;
    document.documentElement.toggleAttribute("data-mobile-sidebar-open", open);
    mobileSidebar.setAttribute("aria-hidden", visible ? "false" : "true");
    mobileSidebar.inert = !visible;
    if (mobileAppBar) mobileAppBar.inert = open;
    if (mainContent) mainContent.inert = open;
    if (mobileSidebarToggle) mobileSidebarToggle.setAttribute("aria-expanded", open ? "true" : "false");
    if (mobileSidebarBackdrop) {
      mobileSidebarBackdrop.hidden = !open;
      mobileSidebarBackdrop.setAttribute("aria-hidden", open ? "false" : "true");
    }
  };
  const openMobileSidebar = () => {
    if (!mobileSidebar || mobileSidebarBreakpoint.matches) return;
    if (document.activeElement instanceof HTMLElement) mobileSidebarReturnFocus = document.activeElement;
    mobileSidebarOpen = true;
    syncMobileSidebar();
    window.requestAnimationFrame(() => {
      if (mobileSidebarOpen && mobileSidebarClose) mobileSidebarClose.focus();
    });
  };
  const closeMobileSidebar = (restoreFocus = true) => {
    const wasOpen = mobileSidebarOpen;
    mobileSidebarOpen = false;
    syncMobileSidebar();
    if (restoreFocus && wasOpen && mobileSidebarReturnFocus instanceof HTMLElement && mobileSidebarReturnFocus.isConnected) {
      mobileSidebarReturnFocus.focus();
    }
  };
  if (mobileSidebarToggle) mobileSidebarToggle.addEventListener("click", openMobileSidebar);
  if (mobileSidebarClose) mobileSidebarClose.addEventListener("click", () => closeMobileSidebar());
  if (mobileSidebarBackdrop) mobileSidebarBackdrop.addEventListener("click", () => closeMobileSidebar());
  if (mobileSidebar) {
    mobileSidebar.addEventListener("click", (event) => {
      if (!(event.target instanceof Element) || !event.target.closest("a[href]")) return;
      closeMobileSidebar();
    });
  }
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || !mobileSidebarOpen) return;
    event.preventDefault();
    closeMobileSidebar();
  });
  const focusFirstDesktopSidebarControl = () => {
    const target = mobileSidebar ? mobileSidebar.querySelector("[data-nav-link]") : null;
    if (target instanceof HTMLElement) target.focus();
  };
  const handleMobileSidebarBreakpoint = (event) => {
    const activeElement = document.activeElement;
    const focusWasInSidebar = activeElement instanceof Element && mobileSidebar && mobileSidebar.contains(activeElement);
    const focusWasInAppBar = activeElement instanceof Element && mobileAppBar && mobileAppBar.contains(activeElement);
    mobileSidebarOpen = false;
    syncMobileSidebar();
    if (event.matches && (focusWasInSidebar || focusWasInAppBar)) {
      focusFirstDesktopSidebarControl();
    } else if (!event.matches && focusWasInSidebar && mobileSidebarToggle) {
      mobileSidebarToggle.focus();
    }
  };
  if (mobileSidebarBreakpoint.addEventListener) {
    mobileSidebarBreakpoint.addEventListener("change", handleMobileSidebarBreakpoint);
  } else {
    mobileSidebarBreakpoint.addListener(handleMobileSidebarBreakpoint);
  }
  syncMobileSidebar();
  const setNavLoading = (link, loading) => {
    const icon = link.querySelector("[data-nav-icon]");
    const loader = link.querySelector("[data-nav-loader]");
    if (!icon || !loader) return;
    icon.classList.toggle("hidden", loading);
    loader.classList.toggle("hidden", !loading);
  };
  let reopenIssueListControls = false;
  const rememberIssueListControls = (target) => {
    const controls = target.closest("[data-issue-list-controls]");
    reopenIssueListControls = !!controls && controls.hasAttribute("open");
  };
  const restoreIssueListControls = (root) => {
    if (!reopenIssueListControls) return;
    const controls = root.matches && root.matches("[data-issue-list-controls]")
      ? root
      : root.querySelector("[data-issue-list-controls]");
    if (controls) controls.setAttribute("open", "");
    reopenIssueListControls = false;
  };
  let changelogSocket = null;
  let changelogTopic = "";
  let changelogReconnect = null;
  let changelogRefreshTimer = null;
  const closeChangelogSocket = () => {
    if (changelogReconnect) {
      window.clearTimeout(changelogReconnect);
      changelogReconnect = null;
    }
    if (changelogSocket) {
      changelogSocket.onclose = null;
      changelogSocket.close();
    }
    changelogSocket = null;
    changelogTopic = "";
  };
  const scheduleChangelogRefresh = (panel) => {
    if (changelogRefreshTimer) window.clearTimeout(changelogRefreshTimer);
    changelogRefreshTimer = window.setTimeout(() => {
      const current = document.querySelector("[data-project-changelog]");
      if (!current || current.dataset.projectId !== panel.dataset.projectId) return;
      if (window.htmx && current.dataset.refreshUrl) {
        window.htmx.ajax("GET", current.dataset.refreshUrl, { target: "#main", swap: "innerHTML" });
      }
    }, 150);
  };
  const syncChangelogRealtime = () => {
    if (document.body.dataset.authenticated !== "true") {
      closeChangelogSocket();
      return;
    }
    const panel = document.querySelector("[data-project-changelog]");
    if (!panel || !panel.dataset.projectId) {
      closeChangelogSocket();
      return;
    }
    const topic = `project:${panel.dataset.projectId}`;
    if (changelogSocket && changelogTopic === topic && changelogSocket.readyState <= WebSocket.OPEN) return;
    closeChangelogSocket();
    changelogTopic = topic;
    const activeTopic = topic;
    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    const socket = new WebSocket(`${protocol}//${window.location.host}/realtime`);
    changelogSocket = socket;
    socket.addEventListener("open", () => {
      socket.send(JSON.stringify({ action: "subscribe", topic: activeTopic }));
      scheduleChangelogRefresh(panel);
    });
    socket.addEventListener("message", (event) => {
      let msg;
      try {
        msg = JSON.parse(event.data);
      } catch (_) {
        return;
      }
      if (msg && msg.type === "resync") {
        scheduleChangelogRefresh(panel);
        return;
      }
      if (msg && msg.entity === "project_changelog" && msg.project_id === panel.dataset.projectId) {
        scheduleChangelogRefresh(panel);
      }
    });
    socket.addEventListener("close", () => {
      if (changelogTopic !== activeTopic || !document.querySelector("[data-project-changelog]")) return;
      changelogReconnect = window.setTimeout(syncChangelogRealtime, 1000);
    });
  };
  const setActiveNav = (next) => {
    links().forEach((link) => {
      link.classList.remove(...active);
      link.classList.add(...inactive);
      link.removeAttribute("aria-current");
    });
    if (!next) return;
    next.classList.remove(...inactive);
    next.classList.add(...active);
    next.setAttribute("aria-current", "page");
  };
  const sidebarLinkForState = (state) => {
    if (!state || !state.dataset.sidebarView) return null;
    return Array.from(links()).find((link) => (
      link.dataset.sidebarView === state.dataset.sidebarView
      && link.dataset.sidebarProjectId === state.dataset.sidebarProjectId
    )) || null;
  };
  // The account pages are listed only in the account menu, which htmx never
  // swaps, so its current page follows whatever view lands in #main.
  const accountMenuActive = ["bg-indigo-50", "text-indigo-700", "dark:bg-indigo-950/50", "dark:text-indigo-200"];
  const accountMenuInactive = ["text-slate-700", "hover:bg-slate-100", "dark:text-slate-200", "dark:hover:bg-slate-800"];
  const syncAccountMenuActive = (state) => {
    const view = state ? state.dataset.sidebarView : "";
    document.querySelectorAll("[data-account-menu-link]").forEach((link) => {
      const current = !!view && link.dataset.accountView === view;
      link.classList.remove(...(current ? accountMenuInactive : accountMenuActive));
      link.classList.add(...(current ? accountMenuActive : accountMenuInactive));
      if (current) {
        link.setAttribute("aria-current", "page");
      } else {
        link.removeAttribute("aria-current");
      }
    });
  };
  const syncSidebarActive = () => {
    const state = mainContent ? mainContent.querySelector("[data-sidebar-view]") : null;
    setActiveNav(sidebarLinkForState(state));
    syncAccountMenuActive(state);
  };
  document.body.addEventListener("htmx:configRequest", (event) => {
    if (csrfToken) event.detail.headers["X-CSRF-Token"] = csrfToken;
  });
  document.body.addEventListener("submit", (event) => ensureCSRFFormToken(event.target), true);
  document.body.addEventListener("htmx:beforeRequest", (event) => {
    hideAppTooltip();
    rememberIssueListControls(event.target);
    const link = event.target.closest("[data-sidebar-link]");
    if (link) {
      links().forEach((item) => setNavLoading(item, false));
      setActiveNav(link);
      setNavLoading(link, true);
    }
    const accountMenuLink = event.target.closest("[data-account-menu-link]");
    if (accountMenuLink) {
      const menu = accountMenuLink.closest("details");
      if (menu) menu.removeAttribute("open");
    }
  });
  document.body.addEventListener("htmx:afterRequest", (event) => {
    const link = event.target.closest("[data-sidebar-link]");
    if (link) {
      setNavLoading(link, false);
    }
    if (event.detail && event.detail.successful === false) syncSidebarActive();
  });
  const resetSearchOptions = (search) => {
    search.querySelectorAll("[data-search-option]").forEach((option) => {
      option.hidden = false;
    });
  };
  const setSearchOptionsOpen = (search, open) => {
    if (!search || !search.hasAttribute("data-search-collapsible")) return;
    const options = search.querySelector("[data-search-options]");
    if (options) options.hidden = !open;
  };
  const autocompleteScope = (search) => search.closest("[data-autocomplete-scope]") || search;
  const setAutocompleteTargets = (search, name, value) => {
    if (!search || !name) return;
    autocompleteScope(search).querySelectorAll("input, select, textarea").forEach((candidate) => {
      if (candidate.name === name) {
        candidate.value = value;
      }
    });
  };
  const filterSearchOptions = (input) => {
    const search = input.closest("[data-search]");
    if (!search) return;
    const query = input.value.trim().toLowerCase();
    search.querySelectorAll("[data-search-option]").forEach((option) => {
      const text = (option.dataset.searchText || "").toLowerCase();
      option.hidden = query !== "" && !text.includes(query);
    });
  };
  const closeOpenDropdowns = (target) => {
    document.querySelectorAll("details[data-close-on-outside][open]").forEach((details) => {
      if (!details.contains(target)) details.removeAttribute("open");
    });
    document.querySelectorAll("[data-search-collapsible]").forEach((search) => {
      if (!search.contains(target)) setSearchOptionsOpen(search, false);
    });
    document.querySelectorAll("[data-option-dropdown-root]").forEach((dropdown) => {
      if (dropdown.contains(target)) return;
      const toggle = dropdown.querySelector("[data-option-dropdown-toggle]");
      if (toggle instanceof HTMLElement) toggle.click();
    });
  };
  document.body.addEventListener("input", (event) => {
    if (!(event.target instanceof Element)) return;
    const textarea = event.target.closest("[data-autogrow-textarea]");
    if (textarea) resizeTextarea(textarea);
    const input = event.target.closest("[data-search-input]");
    if (!input) return;
    const search = input.closest("[data-search]");
    if (search && search.dataset.searchClearTarget) {
      setAutocompleteTargets(search, search.dataset.searchClearTarget, "");
    }
    filterSearchOptions(input);
    if (search) setSearchOptionsOpen(search, !search.hasAttribute("data-project-search"));
  });
  document.body.addEventListener("change", (event) => {
    if (!(event.target instanceof Element)) return;
    const toggle = event.target.closest("[data-checkbox-reveal-toggle]");
    if (!toggle) return;
    const reveal = toggle.closest("[data-checkbox-reveal]");
    if (reveal) syncCheckboxReveal(reveal);
  });
  document.body.addEventListener("focusin", (event) => {
    if (!(event.target instanceof Element)) return;
    showAppTooltip(tooltipTargetFor(event.target));
    const input = event.target.closest("[data-search-input]");
    if (!input) return;
    const search = input.closest("[data-search]");
    filterSearchOptions(input);
    setSearchOptionsOpen(search, !!search && !search.hasAttribute("data-project-search"));
  });
  document.body.addEventListener("focusout", (event) => {
    const target = closestTooltipTarget(event.target);
    if (!target || activeTooltipTarget !== target) return;
    window.queueMicrotask(() => {
      if (activeTooltipTarget !== target) return;
      if (target.matches(":hover") || target.contains(document.activeElement)) return;
      hideAppTooltip();
    });
  });
  document.body.addEventListener("pointerover", (event) => {
    if (event.pointerType === "touch") return;
    const match = tooltipTargetFor(event.target);
    if (!match) return;
    if (event.relatedTarget instanceof Node && match.target.contains(event.relatedTarget)) return;
    showAppTooltip(match);
  });
  document.body.addEventListener("pointerout", (event) => {
    const target = closestTooltipTarget(event.target);
    if (!target || activeTooltipTarget !== target) return;
    if (event.relatedTarget instanceof Node && target.contains(event.relatedTarget)) return;
    if (target.contains(document.activeElement)) return;
    hideAppTooltip();
  });
  document.body.addEventListener("click", (event) => {
    if (!(event.target instanceof Element)) return;
    hideAppTooltip();
    const modalOpen = event.target.closest("[data-modal-open]");
    if (modalOpen) {
      event.preventDefault();
      setClientModalOpen(document.getElementById(modalOpen.dataset.modalOpen || ""), true, modalOpen);
      return;
    }
    const modalClose = event.target.closest("[data-modal-close]");
    if (modalClose) {
      event.preventDefault();
      setClientModalOpen(modalClose.closest("[data-client-modal]"), false);
      return;
    }
    const modalBackdrop = event.target.closest("[data-client-modal]");
    if (modalBackdrop === event.target) {
      setClientModalOpen(modalBackdrop, false);
      return;
    }
    const disclosureToggle = event.target.closest("[data-disclosure-toggle]");
    if (disclosureToggle) {
      event.preventDefault();
      setDisclosureOpen(disclosureToggle, disclosureToggle.getAttribute("aria-expanded") !== "true");
      return;
    }
    const copyMarkdown = event.target.closest("[data-attachment-copy-markdown]");
    if (copyMarkdown) {
      event.preventDefault();
      writeClipboardText(copyMarkdown.dataset.markdown || "").then(() => showCopied(copyMarkdown)).catch(() => {});
      return;
    }
    const removeAttachment = event.target.closest("[data-attachment-remove]");
    if (removeAttachment) {
      const list = removeAttachment.closest("[data-attachment-list][data-attachment-editing]");
      if (list && removeAttachment.dataset.attachmentDeleteUrl) {
        event.preventDefault();
        fetch(removeAttachment.dataset.attachmentDeleteUrl, { method: "DELETE", credentials: "same-origin", headers: csrfHeaders() }).then((res) => {
          if (!res.ok) return;
          const row = removeAttachment.closest("[data-attachment-ref]");
          if (row) row.remove();
          const rows = list.querySelectorAll("[data-attachment-ref]");
          const count = list.querySelector("[data-attachment-count]");
          if (count) count.textContent = String(rows.length);
          list.classList.toggle("hidden", rows.length === 0);
        });
        return;
      }
    }
    const passkeyAction = event.target.closest("[data-passkey-add], [data-passkey-revoke]");
    const pushAction = event.target.closest("[data-push-enable], [data-push-disable]");
    if (pushAction) {
      const panel = pushAction.closest("[data-push-notifications]");
      if (panel) {
        event.preventDefault();
        setPushStatus(panel, "");
        setPushBusy(panel, true);
        const action = pushAction.hasAttribute("data-push-enable")
          ? enablePushNotifications(panel)
          : disablePushNotifications(panel);
        action.catch((err) => {
          setPushStatus(panel, err && err.message ? err.message : "Browser notification update failed.", true);
          syncPushNotifications(panel);
        }).finally(() => setPushBusy(panel, false));
        return;
      }
    }
    if (passkeyAction) {
      const panel = passkeyAction.closest("[data-passkeys-panel]");
      if (panel) {
        event.preventDefault();
        const statusScope = passkeyAction.closest("[data-client-modal]") || panel;
        setPasskeyStatus(statusScope, "");
        setPasskeyBusy(panel, true);
        const action = passkeyAction.hasAttribute("data-passkey-add")
          ? addPasskey(panel)
          : revokePasskey(panel, passkeyAction);
        action.catch((err) => {
          if (err && err.passkeyCanceled) return;
          setPasskeyStatus(statusScope, err && err.message ? err.message : "Passkey request failed.");
        }).finally(() => setPasskeyBusy(panel, false));
        return;
      }
    }
    const passwordLoginAction = event.target.closest("[data-password-login-action]");
    if (passwordLoginAction) {
      const passwordPanel = passwordLoginAction.closest("[data-password-login-panel]");
      const passkeysPanel = document.querySelector("[data-passkeys-panel]");
      if (passwordPanel && passkeysPanel) {
        event.preventDefault();
        setPasswordLoginStatus(passwordPanel, "");
        setPasskeyStatus(passkeysPanel, "");
        setPasskeyBusy(passkeysPanel, true);
        setPasswordLoginBusy(passwordPanel, true);
        updatePasswordLogin(passwordLoginAction).catch((err) => {
          if (err && err.passkeyCanceled) return;
          setPasswordLoginStatus(passwordPanel, err && err.message ? err.message : "Password login update failed.");
        }).finally(() => {
          setPasskeyBusy(passkeysPanel, false);
          setPasswordLoginBusy(passwordPanel, false);
        });
        return;
      }
    }
    closeOpenDropdowns(event.target);
    const option = event.target.closest("[data-search-option]");
    if (!option) return;
    const search = option.closest("[data-search]");
    const input = search && search.querySelector("[data-search-input]");
    if (!input) return;
    input.value = option.dataset.value || "";
    if (option.dataset.targetName && search) {
      setAutocompleteTargets(search, option.dataset.targetName, option.dataset.targetValue || option.dataset.value || "");
    }
    resetSearchOptions(search);
    setSearchOptionsOpen(search, false);
    const form = input.form || input.closest("form");
    if (!form) return;
    if (form.requestSubmit) {
      form.requestSubmit();
    } else {
      ensureCSRFFormToken(form);
      form.submit();
    }
  });
  document.body.addEventListener("keydown", (event) => {
    if (!(event.target instanceof Element)) return;
    const passkeyPasswordModal = document.querySelector("[data-passkey-password-modal]:not(.hidden)");
    if (passkeyPasswordModal) return;
    const openClientModal = document.querySelector("[data-client-modal]:not(.hidden)");
    if (openClientModal && trapModalFocus(event, openClientModal)) return;
    if (event.key === "Escape") {
      hideAppTooltip();
      if (openClientModal) {
        event.preventDefault();
        setClientModalOpen(openClientModal, false);
        return;
      }
      setSearchOptionsOpen(event.target.closest("[data-search]"), false);
    }
    const field = event.target.closest("[data-submit-shortcut='meta-enter']");
    if (!field || event.key !== "Enter" || (!event.metaKey && !event.ctrlKey) || event.shiftKey || event.altKey) return;
    const form = field.form || field.closest("form");
    if (!form) return;
    event.preventDefault();
    if (form.requestSubmit) {
      form.requestSubmit();
    } else {
      ensureCSRFFormToken(form);
      form.submit();
    }
  });
  document.body.addEventListener("dragover", (event) => {
    if (!(event.target instanceof Element)) return;
    const textarea = event.target.closest("[data-attachment-dropzone]");
    if (!textarea) return;
    event.preventDefault();
    textarea.classList.add("border-indigo-300", "bg-indigo-50", "dark:border-indigo-900", "dark:bg-indigo-950/30");
  });
  document.body.addEventListener("dragleave", (event) => {
    if (!(event.target instanceof Element)) return;
    const textarea = event.target.closest("[data-attachment-dropzone]");
    if (!textarea) return;
    textarea.classList.remove("border-indigo-300", "bg-indigo-50", "dark:border-indigo-900", "dark:bg-indigo-950/30");
  });
  document.body.addEventListener("drop", (event) => {
    if (!(event.target instanceof Element)) return;
    const textarea = event.target.closest("[data-attachment-dropzone]");
    if (!textarea) return;
    event.preventDefault();
    textarea.classList.remove("border-indigo-300", "bg-indigo-50", "dark:border-indigo-900", "dark:bg-indigo-950/30");
    uploadAttachments(textarea, Array.from(event.dataTransfer ? event.dataTransfer.files : []));
  });
  // Project insight charts: the server renders every mark; this layer adds the
  // crosshair, tooltips, keyboard stepping, touch taps, and legend toggles.
  const insightChartStates = new WeakMap();
  const insightNumber = (value) => {
    const rounded = Math.round(Number(value) * 10) / 10;
    return Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1);
  };
  const insightCoord = (value) => String(Math.round(value * 10) / 10);
  const insightState = (chart) => {
    let state = insightChartStates.get(chart);
    if (state) return state;
    let data = {};
    try {
      data = JSON.parse(chart.getAttribute("data-insight-chart") || "{}");
    } catch {
      data = {};
    }
    const labels = Array.isArray(data.labels) ? data.labels : [];
    const series = Array.isArray(data.series) ? data.series : [];
    let last = -1;
    series.forEach((item) => (item.values || []).forEach((value, index) => {
      if (value !== null && value !== undefined && index > last) last = index;
    }));
    state = { data: { ...data, labels, series }, hidden: new Set(), index: null, last, armedPoint: null };
    insightChartStates.set(chart, state);
    return state;
  };
  const insightTemplate = (name) => {
    const template = document.querySelector(`template[data-insight-tooltip-${name}]`);
    const node = template?.content.firstElementChild?.cloneNode(true);
    return node instanceof HTMLElement ? node : document.createElement("p");
  };
  const insightTooltipRow = (swatch, value, label) => {
    const row = insightTemplate("row");
    const key = row.querySelector("[data-key]");
    if (key) key.classList.add(...String(swatch || "").split(/\s+/).filter(Boolean));
    const valueNode = row.querySelector("[data-value]");
    if (valueNode) valueNode.textContent = value;
    const labelNode = row.querySelector("[data-label]");
    if (labelNode) labelNode.textContent = label;
    return row;
  };
  const insightVisibleSeries = (state) => state.data.series.filter((item) => !state.hidden.has(item.key));
  const insightFraction = (state, index) => {
    const count = state.data.labels.length;
    if (state.data.kind === "bars") return count > 0 ? (index + 0.5) / count : 0.5;
    return count > 1 ? index / (count - 1) : 0.5;
  };
  // The tooltip sits in an SVG foreignObject and moves by its x and y
  // attributes, so nothing writes inline styles (the CSP forbids them).
  const placeInsightTooltip = (plot, tooltip, x, y) => {
    const box = tooltip.closest("[data-insight-tooltip-box]");
    if (!box) return;
    box.classList.remove("hidden");
    const width = plot.clientWidth;
    const height = plot.clientHeight;
    const tipWidth = tooltip.offsetWidth;
    const tipHeight = tooltip.offsetHeight;
    let left = x + 12;
    if (left + tipWidth > width) left = x - 12 - tipWidth;
    left = Math.max(Math.min(left, width - tipWidth), Math.min(0, width - tipWidth));
    let top = y === null ? 4 : y - tipHeight - 10;
    if (top < 0) top = y === null ? 4 : Math.min(y + 12, Math.max(0, height - tipHeight));
    box.setAttribute("x", String(Math.round(left)));
    box.setAttribute("y", String(Math.round(top)));
  };
  const hideInsight = (chart) => {
    const state = insightState(chart);
    state.index = null;
    state.armedPoint = null;
    chart.querySelector("[data-insight-tooltip-box]")?.classList.add("hidden");
    chart.querySelector("[data-insight-crosshair]")?.classList.add("hidden");
    chart.querySelectorAll("[data-insight-marker]").forEach((marker) => marker.classList.add("hidden"));
    chart.querySelectorAll("[data-insight-column][data-active]").forEach((column) => column.removeAttribute("data-active"));
  };
  const showInsightIndex = (chart, index, announce = false) => {
    const state = insightState(chart);
    const plot = chart.querySelector("[data-insight-plot]");
    const tooltip = chart.querySelector("[data-insight-tooltip]");
    if (!plot || !tooltip || state.last < 0) return;
    index = Math.max(0, Math.min(state.last, index));
    state.index = index;
    const { data } = state;
    const title = insightTemplate("title");
    title.textContent = data.labels[index] || "";
    const nodes = [title];
    const spoken = [title.textContent];
    const visible = insightVisibleSeries(state);
    visible.forEach((item) => {
      const raw = item.values?.[index];
      const text = item.text?.[index] || (raw === null || raw === undefined ? "–" : insightNumber(raw));
      nodes.push(insightTooltipRow(item.swatch, text, item.label));
      spoken.push(`${item.label} ${text}`);
    });
    (data.details || []).forEach((detail) => {
      const node = insightTemplate("detail");
      node.textContent = `${detail.label}: ${detail.values?.[index] ?? "–"}`;
      nodes.push(node);
      spoken.push(node.textContent);
    });
    tooltip.replaceChildren(...nodes);
    const fraction = insightFraction(state, index);
    const percent = `${insightCoord(fraction * 100)}%`;
    const crosshair = chart.querySelector("[data-insight-crosshair]");
    if (crosshair) {
      crosshair.setAttribute("x1", percent);
      crosshair.setAttribute("x2", percent);
      crosshair.classList.remove("hidden");
    }
    let stack = 0;
    state.data.series.forEach((item) => {
      const marker = chart.querySelector(`[data-insight-marker="${CSS.escape(item.key)}"]`);
      if (!marker) return;
      const raw = item.values?.[index];
      if (state.hidden.has(item.key) || raw === null || raw === undefined || !data.top) {
        marker.classList.add("hidden");
        return;
      }
      const value = data.kind === "area" ? (stack += Number(raw)) : Number(raw);
      marker.setAttribute("cx", percent);
      marker.setAttribute("cy", `${insightCoord((1 - value / data.top) * 100)}%`);
      marker.classList.remove("hidden");
    });
    chart.querySelectorAll("[data-insight-column]").forEach((column) => {
      if (Number(column.getAttribute("data-insight-column")) === index) {
        column.setAttribute("data-active", "");
      } else {
        column.removeAttribute("data-active");
      }
    });
    placeInsightTooltip(plot, tooltip, fraction * plot.clientWidth, null);
    if (announce) {
      const live = chart.querySelector("[data-insight-live]");
      if (live) live.textContent = spoken.join(", ");
    }
  };
  const showInsightPoint = (chart, point, announce = false) => {
    const plot = chart.querySelector("[data-insight-plot]");
    const tooltip = chart.querySelector("[data-insight-tooltip]");
    const dot = point.querySelector("circle:last-of-type");
    if (!plot || !tooltip || !dot) return;
    const title = insightTemplate("title");
    title.textContent = point.getAttribute("data-point-title") || "";
    const nodes = [title, insightTooltipRow(insightState(chart).data.series?.[0]?.swatch || "bg-indigo-500", point.getAttribute("data-point-value") || "", "cycle time")];
    String(point.getAttribute("data-point-detail") || "").split("\n").filter(Boolean).forEach((line) => {
      const node = insightTemplate("detail");
      node.textContent = line;
      nodes.push(node);
    });
    tooltip.replaceChildren(...nodes);
    const plotBox = plot.getBoundingClientRect();
    const dotBox = dot.getBoundingClientRect();
    placeInsightTooltip(plot, tooltip, dotBox.left + dotBox.width / 2 - plotBox.left, dotBox.top + dotBox.height / 2 - plotBox.top);
    if (announce) {
      const live = chart.querySelector("[data-insight-live]");
      if (live) live.textContent = point.getAttribute("aria-label") || "";
    }
  };
  const insightIndexAt = (chart, clientX) => {
    const state = insightState(chart);
    const plot = chart.querySelector("[data-insight-plot]");
    const count = state.data.labels.length;
    if (!plot || count === 0) return 0;
    const box = plot.getBoundingClientRect();
    const fraction = Math.max(0, Math.min(1, (clientX - box.left) / Math.max(1, box.width)));
    if (state.data.kind === "bars") return Math.min(count - 1, Math.floor(fraction * count));
    return Math.round(fraction * (count - 1));
  };
  const insightLinePath = (values, top, count) => {
    let path = "";
    let pen = false;
    values.forEach((value, index) => {
      if (value === null || value === undefined) {
        pen = false;
        return;
      }
      const x = count > 1 ? (index / (count - 1)) * 1000 : 500;
      path += `${pen ? "L" : "M"}${insightCoord(x)},${insightCoord(1000 - (value / top) * 1000)}`;
      pen = true;
    });
    return path;
  };
  const restackInsightAreas = (chart) => {
    const state = insightState(chart);
    const { data } = state;
    if (data.kind !== "area" || !data.top) return;
    const count = data.labels.length;
    let base = new Array(count).fill(0);
    data.series.forEach((item) => {
      if (state.hidden.has(item.key)) return;
      const upper = base.map((value, index) => value + Number(item.values?.[index] ?? 0));
      const x = (index) => insightCoord(count > 1 ? (index / (count - 1)) * 1000 : 500);
      const y = (value) => insightCoord(1000 - (value / data.top) * 1000);
      let area = upper.map((value, index) => `${index === 0 ? "M" : "L"}${x(index)},${y(value)}`).join("");
      for (let index = count - 1; index >= 0; index -= 1) area += `L${x(index)},${y(base[index])}`;
      const key = CSS.escape(item.key);
      chart.querySelector(`[data-insight-area][data-series="${key}"]`)?.setAttribute("d", `${area}Z`);
      chart.querySelector(`[data-insight-line][data-series="${key}"]`)?.setAttribute("d", insightLinePath(upper, data.top, count));
      base = upper;
    });
  };
  const toggleInsightSeries = (chart, button) => {
    const state = insightState(chart);
    const key = button.getAttribute("data-insight-toggle") || "";
    const show = state.hidden.has(key);
    if (show) {
      state.hidden.delete(key);
    } else {
      state.hidden.add(key);
    }
    button.setAttribute("aria-pressed", show ? "true" : "false");
    chart.querySelectorAll(`[data-series="${CSS.escape(key)}"]`).forEach((element) => element.classList.toggle("hidden", !show));
    restackInsightAreas(chart);
    if (state.index !== null) showInsightIndex(chart, state.index);
  };
  const initInsightChart = (chart) => {
    if (chart.hasAttribute("data-insight-ready")) return;
    chart.setAttribute("data-insight-ready", "");
    const plot = chart.querySelector("[data-insight-plot]");
    chart.querySelectorAll("[data-insight-toggle]").forEach((button) => {
      button.addEventListener("click", () => toggleInsightSeries(chart, button));
    });
    if (!plot) return;
    if (chart.getAttribute("data-insight-kind") === "scatter") {
      let pointerType = "mouse";
      chart.querySelectorAll("[data-insight-point]").forEach((point) => {
        point.addEventListener("pointerdown", (event) => { pointerType = event.pointerType; });
        point.addEventListener("pointerenter", (event) => {
          if (event.pointerType === "mouse") showInsightPoint(chart, point);
        });
        point.addEventListener("pointerleave", (event) => {
          if (event.pointerType === "mouse") hideInsight(chart);
        });
        point.addEventListener("focus", () => showInsightPoint(chart, point, true));
        point.addEventListener("blur", () => hideInsight(chart));
        point.addEventListener("click", (event) => {
          // A first tap shows the details; tapping the same point again opens it.
          const state = insightState(chart);
          if (pointerType === "mouse" || pointerType === "pen" || state.armedPoint === point) return;
          event.preventDefault();
          showInsightPoint(chart, point);
          state.armedPoint = point;
        });
      });
      return;
    }
    const track = (event) => showInsightIndex(chart, insightIndexAt(chart, event.clientX));
    plot.addEventListener("pointermove", track);
    plot.addEventListener("pointerdown", track);
    plot.addEventListener("pointerleave", (event) => {
      if (event.pointerType === "mouse" && document.activeElement !== plot) hideInsight(chart);
    });
    plot.addEventListener("focus", () => {
      const state = insightState(chart);
      showInsightIndex(chart, state.index ?? state.last, true);
    });
    plot.addEventListener("blur", () => hideInsight(chart));
    plot.addEventListener("keydown", (event) => {
      const state = insightState(chart);
      const current = state.index ?? state.last;
      let next = null;
      if (event.key === "ArrowLeft") next = current - 1;
      if (event.key === "ArrowRight") next = current + 1;
      if (event.key === "Home") next = 0;
      if (event.key === "End") next = state.last;
      if (event.key === "Escape") {
        hideInsight(chart);
        return;
      }
      if (next === null) return;
      event.preventDefault();
      showInsightIndex(chart, next, true);
    });
  };
  const initInsightCharts = (root = document) => {
    if (root instanceof Element && root.matches("[data-insight-chart]")) initInsightChart(root);
    root.querySelectorAll("[data-insight-chart]").forEach(initInsightChart);
  };
  document.addEventListener("pointerdown", (event) => {
    if (event.pointerType === "mouse" || !(event.target instanceof Element)) return;
    document.querySelectorAll("[data-insight-chart][data-insight-ready]").forEach((chart) => {
      if (!chart.contains(event.target)) hideInsight(chart);
    });
  });
  document.body.addEventListener("htmx:afterSwap", (event) => {
    createIcons();
    localizeTimes(event.target);
    resizeTextareas(event.target);
    syncCheckboxReveals(event.target);
    restoreIssueListControls(event.target);
    syncSidebarActive();
    syncChangelogRealtime();
    syncPushNotifications(event.target);
    initInsightCharts(event.target);
    window.setTimeout(() => focusClientModal(document.querySelector("[data-client-modal]:not(.hidden)")), 0);
  });
  document.body.addEventListener("htmx:historyRestore", syncSidebarActive);
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", () => {
      createIcons();
      localizeTimes();
      resizeTextareas();
      syncCheckboxReveals();
      syncSidebarActive();
      syncChangelogRealtime();
      syncPushNotifications();
      initInsightCharts();
      focusClientModal(document.querySelector("[data-client-modal]:not(.hidden)"));
    });
  } else {
    createIcons();
    localizeTimes();
    resizeTextareas();
    syncCheckboxReveals();
    syncSidebarActive();
    syncChangelogRealtime();
    syncPushNotifications();
    initInsightCharts();
    focusClientModal(document.querySelector("[data-client-modal]:not(.hidden)"));
  }
})();
