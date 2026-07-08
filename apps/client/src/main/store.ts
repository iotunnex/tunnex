import * as fs from "node:fs";
import * as path from "node:path";
import { app, safeStorage } from "electron";
import { CredentialStore, Persistence, SafeStorageLike } from "./credential";

// Build the real CredentialStore over Electron's safeStorage (Keychain / DPAPI /
// libsecret) and a file in userData. The raw credential file is 0600; but the
// OS-encryption is the real protection — the 0600 is defense in depth.
export function buildCredentialStore(allowInsecure: boolean): CredentialStore {
  const file = path.join(app.getPath("userData"), "credential.bin");
  const persist: Persistence = {
    read() {
      try {
        return fs.readFileSync(file);
      } catch {
        return null;
      }
    },
    write(b) {
      fs.mkdirSync(path.dirname(file), { recursive: true });
      fs.writeFileSync(file, b, { mode: 0o600 });
    },
    clear() {
      try {
        fs.rmSync(file);
      } catch {
        /* already gone */
      }
    },
  };
  const safe: SafeStorageLike = {
    isEncryptionAvailable: () => safeStorage.isEncryptionAvailable(),
    encryptString: (s) => safeStorage.encryptString(s),
    decryptString: (b) => safeStorage.decryptString(b),
  };
  return new CredentialStore(safe, persist, allowInsecure);
}
