// setupPageDataUrl builds the FIRST-RUN screen: a self-contained page (loaded as
// a data: URL, so it needs no bundled asset) that prompts for the server URL and
// hands it to main via the preload bridge. Main validates it against /healthz
// before accepting (the page just reports the outcome). It also surfaces the
// insecure-credential-storage state VISIBLY (decision (e)): if there is no OS
// keychain and no opt-in, the user is told login is blocked until they pass
// --allow-insecure-credential-storage (or use device-code, S6.2).
export function setupPageDataUrl(secureStorage: boolean, allowInsecure: boolean): string {
  const warn =
    !secureStorage && !allowInsecure
      ? `<p class="warn">No OS keychain is available on this system. Tunnex will NOT store a
         credential in plaintext. Re-launch with <code>--allow-insecure-credential-storage</code>
         to opt in explicitly, or use device-code login (coming in S6.2).</p>`
      : allowInsecure && !secureStorage
        ? `<p class="warn">Insecure credential storage is ENABLED (--allow-insecure-credential-storage):
           the credential will be written to disk without OS encryption.</p>`
        : "";
  const html = `<!doctype html><html><head><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'">
<title>Tunnex — Setup</title>
<style>
  body{font-family:system-ui;background:#0b0b12;color:#e2e8f0;display:grid;place-items:center;height:100vh;margin:0}
  .card{width:26rem;max-width:90vw}
  h1{font-size:1.25rem} label{display:block;font-size:.85rem;color:#94a3b8;margin:1rem 0 .35rem}
  input{width:100%;box-sizing:border-box;padding:.6rem;border-radius:.5rem;border:1px solid #ffffff1a;background:#12121c;color:#fff}
  button{margin-top:1rem;width:100%;padding:.65rem;border:0;border-radius:.5rem;background:#7c5cff;color:#fff;font-weight:600;cursor:pointer}
  button:disabled{opacity:.5;cursor:default}
  .err{color:#ff6b6b;font-size:.8rem;margin-top:.6rem;min-height:1rem}
  .warn{color:#e6b450;font-size:.8rem;border:1px solid #e6b45055;background:#e6b4500f;padding:.6rem;border-radius:.5rem;margin-top:1rem}
  code{background:#ffffff14;padding:.05rem .3rem;border-radius:.25rem}
</style></head>
<body><div class="card">
  <h1>Connect to your Tunnex server</h1>
  <p style="color:#94a3b8;font-size:.9rem">Enter the address of your self-hosted Tunnex control plane.</p>
  <label for="u">Server URL</label>
  <input id="u" type="url" placeholder="https://vpn.example.com" autofocus>
  <button id="go">Connect</button>
  <div class="err" id="err"></div>
  ${warn}
</div>
<script>
  const u = document.getElementById('u'), go = document.getElementById('go'), err = document.getElementById('err');
  go.onclick = async () => {
    err.textContent = ''; go.disabled = true; go.textContent = 'Checking…';
    try {
      await window.tunnex.config.setServerUrl(u.value);
      // main reloads the window into the SPA on success.
    } catch (e) {
      err.textContent = (e && e.message) ? e.message : 'Could not connect to that server.';
      go.disabled = false; go.textContent = 'Connect';
    }
  };
  u.addEventListener('keydown', (e) => { if (e.key === 'Enter') go.click(); });
</script>
</body></html>`;
  return "data:text/html;charset=utf-8," + encodeURIComponent(html);
}
