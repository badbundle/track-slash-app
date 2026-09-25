(() => {
  const createIcons = () => window.lucide && window.lucide.createIcons();
  const csrfToken = document.querySelector('meta[name="csrf-token"]')?.content || "";
  const status = document.querySelector("[data-passkey-status]");
  const showStatus = (message, kind = "error") => {
    if (!status) return;
    status.textContent = message || "";
    status.className = "rounded-md border px-3 py-2 text-sm";
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
  const setBusy = (button, busy) => {
    if (!button) return;
    button.disabled = busy;
    button.classList.toggle("opacity-60", busy);
    button.classList.toggle("cursor-wait", busy);
  };
  const passkeysSupported = () => !!(window.PublicKeyCredential && navigator.credentials);
  const assertPasskeysSupported = () => {
    if (!passkeysSupported()) throw new Error("This browser does not support passkeys.");
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
  const parseCreationOptions = (options) => {
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
  const parseRequestOptions = (options) => {
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
  const credentialToJSON = (credential) => {
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
      headers: { "Accept": "application/json", "Content-Type": "application/json", "X-CSRF-Token": csrfToken },
      body: JSON.stringify(payload || {}),
    });
    let body = null;
    try {
      body = await res.json();
    } catch (_) {}
    if (!res.ok) throw new Error((body && body.error) || "Passkey request failed.");
    return body || {};
  };
  const login = async (button) => {
    assertPasskeysSupported();
    const next = document.querySelector("input[name='next']")?.value || "/";
    const options = await postJSON("/login/passkey/options", {});
    const credential = await navigator.credentials.get({
      publicKey: parseRequestOptions(options.publicKey),
      mediation: options.mediation || "optional",
    });
    if (!credential) throw new Error("Passkey sign-in was canceled.");
    const result = await postJSON("/login/passkey", {
      ceremony_id: options.ceremony_id,
      credential: credentialToJSON(credential),
      next,
    });
    window.location.assign(result.next || "/");
  };
  const signup = async (button) => {
    assertPasskeysSupported();
    const form = button.closest("form");
    const username = form.querySelector("input[name='username']")?.value.trim() || "";
    const name = form.querySelector("input[name='name']")?.value.trim() || "";
    const next = form.querySelector("input[name='next']")?.value || "/";
    const terms = form.querySelector("input[name='accept_terms']");
    const acceptTerms = !terms || terms.checked;
    if (!username) throw new Error("Username required.");
    if (!acceptTerms) throw new Error("You must agree to the Preview Terms and acknowledge the Privacy Notice.");
    const options = await postJSON("/signup/passkey/options", { username, name, accept_terms: acceptTerms });
    const credential = await navigator.credentials.create({
      publicKey: parseCreationOptions(options.publicKey),
      mediation: options.mediation || "optional",
    });
    if (!credential) throw new Error("Passkey creation was canceled.");
    const result = await postJSON("/signup/passkey", {
      ceremony_id: options.ceremony_id,
      credential: credentialToJSON(credential),
      next,
      accept_terms: acceptTerms,
    });
    window.location.assign(result.next || "/");
  };
  document.addEventListener("click", (event) => {
    if (!(event.target instanceof Element)) return;
    const loginButton = event.target.closest("[data-passkey-login]");
    const signupButton = event.target.closest("[data-passkey-signup]");
    const button = loginButton || signupButton;
    if (!button) return;
    event.preventDefault();
    showStatus("");
    setBusy(button, true);
    const action = loginButton ? login(button) : signup(button);
    action.catch((err) => showStatus(err && err.message ? err.message : "Passkey request failed.")).finally(() => setBusy(button, false));
  });
  // The password form sits in a native <details> disclosure so it stays
  // keyboard operable and usable without this script. Opening it always lands
  // the user in the username field, and a browser that cannot do passkeys gets
  // it open from the start, since the password form is its only way in.
  const passwordLogin = document.querySelector("[data-password-login]");
  if (passwordLogin) {
    passwordLogin.addEventListener("toggle", () => {
      if (passwordLogin.open) passwordLogin.querySelector("input[name='username']")?.focus();
    });
    if (!passkeysSupported()) passwordLogin.open = true;
  }
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", createIcons);
  } else {
    createIcons();
  }
})();
