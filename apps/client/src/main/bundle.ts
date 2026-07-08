import * as path from "node:path";

// resolveBundlePath maps an app:// request path to a file INSIDE the bundle dir,
// or null if it would escape. This is the security core of the app:// protocol:
// a page (or a compromised renderer) must never read arbitrary disk via
// app://../../etc/passwd. Pure + unit-tested (escape-rejection is not a comment).
export function resolveBundlePath(bundleDir: string, requestPath: string): string | null {
  // Normalize the URL path: strip query/hash, decode, default to index.html.
  let p = requestPath.split("?")[0].split("#")[0];
  try {
    p = decodeURIComponent(p);
  } catch {
    return null; // malformed percent-encoding
  }
  if (p === "" || p === "/") p = "/index.html";
  // Reject NUL and backslashes outright (Windows path confusion).
  if (p.includes("\0") || p.includes("\\")) return null;

  const root = path.resolve(bundleDir);
  const candidate = path.resolve(root, "." + (p.startsWith("/") ? p : "/" + p));
  // Containment check: candidate must be root itself or strictly under root +
  // the separator (guards the /root-evil vs /root prefix trap).
  if (candidate !== root && !candidate.startsWith(root + path.sep)) {
    return null;
  }
  return candidate;
}
