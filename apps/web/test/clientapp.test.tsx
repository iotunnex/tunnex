import { describe, expect, it, beforeEach, afterEach, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { ClientApp } from "../src/client/ClientApp";

// ⛔ THE CLIENT SURFACE HAD NO TEST AT ALL, AND THAT IS HOW THE CONNECT BUTTON GOT INTO A STATE
// WHERE IT COULD ONLY THROW.
//
// `ClientApp` asked `tunnel.status()` and never `auth.status()`. A device with no credential
// therefore rendered "Disconnected" — a healthy idle state — with a Connect button. Pressing it
// threw `not_authenticated` out of the main process into a terminal log; the window did not move.
//
// > **THE BUTTON WAS NOT BROKEN. IT WAS OFFERED IN A STATE WHERE IT CANNOT WORK** — the same defect
// > as a revoked device being shown "Connect", which the state model already refuses by returning a
// > null action. The model was right; nothing was asking it the right question.
//
// The bridge is faked here rather than mocked at the module boundary: `desktop()` reads
// `window.tunnex`, which is exactly what the preload sets, so a fake object IS the contract.

type Bridge = NonNullable<Window["tunnex"]>;

function fakeBridge(over: {
  loggedIn?: boolean;
  expired?: boolean;
  up?: () => Promise<unknown>;
}): Bridge {
  return {
    auth: {
      login: vi.fn().mockResolvedValue({ fingerprint: "fp", expiresAt: "" }),
      logout: vi.fn().mockResolvedValue(undefined),
      status: vi.fn().mockResolvedValue({
        loggedIn: over.loggedIn ?? true,
        expired: over.expired ?? false,
        fingerprint: "abcdef0123",
        secureStorage: true,
      }),
    },
    diag: {
      logPath: vi.fn().mockResolvedValue("/tmp/main.log"),
      openLogs: vi.fn().mockResolvedValue(undefined),
      readLog: vi.fn().mockResolvedValue("[info] log line"),
      exportLog: vi.fn().mockResolvedValue("/tmp/export.txt"),
      appInfo: vi.fn().mockResolvedValue({
        version: "0.1.0",
        update: {
          kind: "disabled",
          reason: "Automatic updates are off in this build.",
          detail: "not signed yet",
        },
      }),
    },
    config: {
      getServerUrl: vi.fn().mockResolvedValue("https://vpn.example.com"),
      setServerUrl: vi.fn(),
    },
    tunnel: {
      up: over.up ?? vi.fn().mockResolvedValue({ state: "up" }),
      down: vi.fn().mockResolvedValue(undefined),
      status: vi.fn().mockResolvedValue({ state: "down" }),
      onStatusChanged: vi.fn().mockReturnValue(() => {}),
      importConfig: vi
        .fn()
        .mockResolvedValue({ address: "10.99.0.7/32", fullTunnel: false }),
      importedInfo: vi.fn().mockResolvedValue(null),
      forgetImported: vi.fn().mockResolvedValue(undefined),
    },
  } as unknown as Bridge;
}

beforeEach(() => {
  // jsdom has no canvas 2D context; the hyperdrive draws through it and must not throw.
  HTMLCanvasElement.prototype.getContext = vi.fn().mockReturnValue(null);
});

afterEach(() => {
  // ⚠ EXPLICIT. Auto-cleanup only runs with vitest globals; without it every render STACKS and the
  // queries find two headings — which reads as a component bug and is a harness bug.
  cleanup();
  delete window.tunnex;
  vi.restoreAllMocks();
});

describe("the client asks the SESSION, not only the tunnel", () => {
  it("⛔ no credential renders Not signed in — never a Connect button that can only throw", async () => {
    window.tunnex = fakeBridge({ loggedIn: false });
    render(<ClientApp />);
    await waitFor(() =>
      expect(screen.getByRole("heading").textContent).toContain(
        "Not signed in",
      ),
    );
    // The verb is browser re-auth. It must NOT be "Connect", and it must NOT collect a password:
    // the wireframe's own rule is that MFA touches the client only via browser re-auth.
    const btn = screen.getByRole("button", {
      name: /sign in with your browser/i,
    });
    expect(btn).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Connect" })).toBeNull();
    expect(document.querySelector('input[type="password"]')).toBeNull();
  });

  it("a valid session shows the tunnel state, so auth does not mask a working client", async () => {
    window.tunnex = fakeBridge({ loggedIn: true });
    render(<ClientApp />);
    await waitFor(() =>
      expect(screen.getByRole("heading").textContent).toContain("Disconnected"),
    );
  });

  it("an EXPIRED session is the design's own state, not signed-out", async () => {
    window.tunnex = fakeBridge({ loggedIn: true, expired: true });
    render(<ClientApp />);
    await waitFor(() =>
      expect(screen.getByRole("heading").textContent).toContain(
        "Session expired",
      ),
    );
  });

  it("⛔ a not_authenticated rejection becomes a STATE — the exact error that reached the log", async () => {
    // Reproduces the founder's terminal output:
    //   Error occurred in handler for 'tunnel:up': Error: not_authenticated
    // Before the fix this rejection was unhandled and the surface stayed on "Disconnected".
    const up = vi.fn().mockRejectedValue(new Error("not_authenticated"));
    window.tunnex = fakeBridge({ loggedIn: true, up });
    render(<ClientApp />);
    await waitFor(() =>
      expect(screen.getByRole("heading").textContent).toContain("Disconnected"),
    );
    fireEvent.click(screen.getByRole("button", { name: "Connect" }));
    await waitFor(() =>
      expect(screen.getByRole("heading").textContent).toContain(
        "Not signed in",
      ),
    );
  });

  it("any OTHER failure is shown verbatim rather than swallowed", async () => {
    const up = vi.fn().mockRejectedValue(new Error("helper_unreachable"));
    window.tunnex = fakeBridge({ loggedIn: true, up });
    render(<ClientApp />);
    await waitFor(() =>
      expect(
        (screen.getByRole("button", { name: "Connect" }) as HTMLButtonElement)
          .disabled,
      ).toBe(false),
    );
    fireEvent.click(screen.getByRole("button", { name: "Connect" }));
    await waitFor(() =>
      expect(screen.getByText(/helper_unreachable/)).toBeTruthy(),
    );
  });

  it("⛔ sign-out is REACHABLE — the step-3 flip left it only in the web dashboard", async () => {
    // DesktopSettings (sign out / change server) lives in the SPA the client no longer loads, so
    // the capability existed with no call site. Without a reset there is no way to re-authenticate.
    const b = fakeBridge({ loggedIn: true });
    window.tunnex = b;
    render(<ClientApp />);
    // ⛔ REACHED THROUGH THE NAV, NOT ASSUMED PRESENT. The control moved to its own pane, so the
    // test now proves the PATH to it as well as the control — which is what "reachable" means.
    fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
    const out = await screen.findByRole("button", { name: /sign out/i });
    fireEvent.click(out);
    await waitFor(() => expect(b.auth.logout).toHaveBeenCalled());
  });
});

describe("the numbers are measured and the verb is one word", () => {
  it("⛔ real rx/tx render as NUMBERS — the fields were hard-wired to n/a forever", async () => {
    // They were `useState({rx: null, tx: null, ...})` with a comment saying they would arrive "in
    // step 3". Step 3 came and went. Meanwhile the plot beside them drew invented samples.
    const b = fakeBridge({ loggedIn: true });
    b.tunnel.status = vi.fn().mockResolvedValue({
      state: "up",
      rx_bytes: 2048,
      tx_bytes: 1024,
      last_handshake_sec: Math.floor(Date.now() / 1000) - 5,
    });
    window.tunnex = b;
    render(<ClientApp />);
    await waitFor(() => expect(screen.getByText("2.00 KB")).toBeTruthy()); // BYTES IN
    expect(screen.getByText("1.00 KB")).toBeTruthy(); // BYTES OUT
    // The helper reports a HANDSHAKE; there is no packet counter anywhere in the chain.
    expect(screen.getByText(/LAST HANDSHAKE/)).toBeTruthy();
    expect(screen.queryByText(/PACKET RECEIVED/)).toBeNull();
    // ⛔ THE TUNNEL IP WAS ALREADY ON THE WIRE AND NOTHING SHOWED IT. TunnelController.withAddress
    // decorates every status with it precisely so the client can answer "what is my IP" — and the
    // preload TYPE omitted the field, so TypeScript denied the existence of data already arriving.
    expect(screen.getByText(/TUNNEL IP/)).toBeTruthy();
  });

  it("⛔ the verb is one word — no ' · tear down the mesh' suffix", async () => {
    const b = fakeBridge({ loggedIn: true });
    b.tunnel.status = vi.fn().mockResolvedValue({ state: "up" });
    window.tunnex = b;
    render(<ClientApp />);
    const btn = await screen.findByRole("button", { name: "Disconnect" });
    expect(btn.textContent).toBe("Disconnect");
  });

  it("⛔ BOTH canvases exist, and only one of them claims to measure anything", async () => {
    // This test asserted the mesh was GONE for exactly one revision. "Remove that hyperdrive thing"
    // named a FEATURE — a label, a fabricated plot and an animation — and I read it as a list of
    // all three parts. Only the label and the fabrication deserved removal.
    //
    // > **AN INSTRUCTION THAT NAMES A FEATURE IS NOT AN ENUMERATION OF ITS PARTS.** Each part gets
    // > dispositioned on its own; collapsing them into one verdict deletes things nobody asked to
    // > lose, and the test written to hold the deletion in place makes the mistake permanent.
    window.tunnex = fakeBridge({ loggedIn: true });
    const { container } = render(<ClientApp />);
    await waitFor(() => expect(screen.getByRole("heading")).toBeTruthy());
    const mesh = container.querySelector("#tnxHyper");
    const plot = container.querySelector("#tnxGraph");
    expect(mesh, "the mesh animation is missing").not.toBeNull();
    expect(plot, "the throughput plot is missing").not.toBeNull();
    // Decorative means DECLARED decorative: a canvas a screen reader announces as content, with no
    // content behind it, is the same false claim as the invented plot in a different medium.
    expect(mesh!.getAttribute("aria-hidden")).toBe("true");
    expect(plot!.getAttribute("aria-hidden")).toBe("true");
  });
});

describe("routing mode and the server, both reachable and both unambiguous", () => {
  it("⛔ NO checkbox whose label is the OPPOSITE of what it sets", async () => {
    // The control was `<input type="checkbox" checked={fullTunnel}>` labelled "Split tunnel".
    // Unchecked read as "split is off" — i.e. everything is tunnelled — while actually meaning
    // fullTunnel === false, which is SPLIT. The safe-looking state was the leaking one.
    window.tunnex = fakeBridge({ loggedIn: true });
    const { container } = render(<ClientApp />);
    fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
    expect(container.querySelector('input[type="checkbox"]')).toBeNull();
    // Two NAMED options; neither requires inferring meaning from a tick.
    const radios = container.querySelectorAll(
      'input[type="radio"][name="routing"]',
    );
    expect(radios).toHaveLength(2);
    expect(screen.getByText(/All traffic/)).toBeTruthy();
    expect(screen.getByText(/Only Tunnex routes/)).toBeTruthy();
  });

  it("each routing option says what it does to traffic, not just its name", async () => {
    window.tunnex = fakeBridge({ loggedIn: true });
    render(<ClientApp />);
    fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
    await waitFor(() =>
      expect(screen.getByText(/including your normal browsing/i)).toBeTruthy(),
    );
    expect(screen.getByText(/uses your normal connection/i)).toBeTruthy();
  });

  it("⛔ CHANGE SERVER is reachable — setServerUrl had no caller after the step-3 flip", async () => {
    // The verb has been on the preload allowlist since S6.2. Once the client stopped loading the
    // web dashboard, nothing called it: changing control plane meant deleting the app-data
    // directory by hand. A documented capability with no way to reach it.
    const b = fakeBridge({ loggedIn: true });
    b.config.setServerUrl = vi.fn().mockResolvedValue({
      url: "https://b.example.com",
      reloginRequired: true,
    });
    window.tunnex = b;
    render(<ClientApp />);
    fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
    fireEvent.click(
      await screen.findByRole("button", { name: /change server/i }),
    );
    const input = await screen.findByLabelText(/control-plane url/i);
    fireEvent.change(input, { target: { value: "https://b.example.com" } });
    fireEvent.click(screen.getByRole("button", { name: /switch server/i }));
    await waitFor(() =>
      expect(b.config.setServerUrl).toHaveBeenCalledWith(
        "https://b.example.com",
      ),
    );
  });

  it("⛔ the sign-out cost is stated BEFORE the switch, not discovered after", async () => {
    window.tunnex = fakeBridge({ loggedIn: true });
    render(<ClientApp />);
    fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
    fireEvent.click(
      await screen.findByRole("button", { name: /change server/i }),
    );
    // A credential is only valid for its issuing server; switching revokes it. The user must be
    // told that is the price while they can still cancel.
    expect(
      await screen.findByText(/signs you out and tears down the tunnel/i),
    ).toBeTruthy();
  });
});

describe("troubleshooting is reachable from the client", () => {
  it("⛔ a Logs control exists — the file had 30 lines of updater noise and none of the failures", async () => {
    // ~/Library/Logs/@tunnex/client/main.log existed, rotated, and was writable. electron-log was
    // imported by updater.ts ALONE, so the only code that logged had nothing to say, and the one
    // error anybody hit — not_authenticated on tunnel:up — appears in it zero times.
    //
    // > **A LOG FILE THAT EXISTS IS NOT LOGGING.** "Check the logs" reads as a real instruction,
    // > the file opens, it has content and timestamps, and the incident is simply absent — which
    // > looks like nothing went wrong rather than nothing was recorded.
    const b = fakeBridge({ loggedIn: true });
    window.tunnex = b;
    render(<ClientApp />);
    fireEvent.click(await screen.findByRole("button", { name: "Logs" }));
    fireEvent.click(await screen.findByRole("button", { name: /reveal/i }));
    await waitFor(() => expect(b.diag.openLogs).toHaveBeenCalled());
  });
});

describe("the home pane stays one screen", () => {
  it("⛔ settings are NOT on the connection screen — it was growing a section per request", async () => {
    // Routing mode, then a server form, then a row of footer buttons. Each defensible alone;
    // together a column you scroll to find anything in. A surface that only ever gains sections is
    // not a design, it is an accumulation.
    window.tunnex = fakeBridge({ loggedIn: true });
    const { container } = render(<ClientApp />);
    await waitFor(() =>
      expect(
        screen.getByRole("heading", { name: /connected|disconnected/i }),
      ).toBeTruthy(),
    );
    expect(screen.queryByRole("button", { name: /change server/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /sign out/i })).toBeNull();
    expect(container.querySelectorAll('input[type="radio"]')).toHaveLength(0);
    // What the home pane DOES answer: am I connected, and what do I press.
    expect(
      screen.getByRole("button", { name: /^(Connect|Disconnect|Cancel)$/ }),
    ).toBeTruthy();
  });

  it("every pane is reachable from the nav", async () => {
    window.tunnex = fakeBridge({ loggedIn: true });
    render(<ClientApp />);
    for (const name of ["Settings", "Logs", "Connection"]) {
      fireEvent.click(await screen.findByRole("button", { name }));
    }
    expect(
      screen
        .getByRole("button", { name: "Connection" })
        .getAttribute("aria-current"),
    ).toBe("page");
  });
});

describe("the log is visible IN the client, and export tells the truth", () => {
  it("⛔ shows the file's contents — 'reveal in Finder' is not troubleshooting", async () => {
    // A log a user cannot see is a log they will not read, and revealing a file is useless on a
    // machine where the problem is that the app will not start.
    const b = fakeBridge({ loggedIn: true });
    b.diag.readLog = vi
      .fn()
      .mockResolvedValue(
        "[info] tunnex client started\n[error] not_authenticated",
      );
    window.tunnex = b;
    render(<ClientApp />);
    fireEvent.click(await screen.findByRole("button", { name: "Logs" }));
    await waitFor(() =>
      expect(screen.getByText(/not_authenticated/)).toBeTruthy(),
    );
  });

  it("⛔ a CANCELLED export does not claim the file was saved", async () => {
    // exportLog resolves null when the save dialog is dismissed. Reporting success there is the
    // UI claiming an action it did not perform — the rule this repo already holds elsewhere.
    const b = fakeBridge({ loggedIn: true });
    b.diag.exportLog = vi.fn().mockResolvedValue(null);
    window.tunnex = b;
    render(<ClientApp />);
    fireEvent.click(await screen.findByRole("button", { name: "Logs" }));
    fireEvent.click(await screen.findByRole("button", { name: /export/i }));
    await waitFor(() => expect(b.diag.exportLog).toHaveBeenCalled());
    expect(screen.queryByText(/saved to/i)).toBeNull();
  });

  it("a completed export names the path it wrote", async () => {
    const b = fakeBridge({ loggedIn: true });
    b.diag.exportLog = vi
      .fn()
      .mockResolvedValue("/Users/x/tunnex-client-log.txt");
    window.tunnex = b;
    render(<ClientApp />);
    fireEvent.click(await screen.findByRole("button", { name: "Logs" }));
    fireEvent.click(await screen.findByRole("button", { name: /export/i }));
    await waitFor(() =>
      expect(screen.getByText(/tunnex-client-log\.txt/)).toBeTruthy(),
    );
  });

  it("an unreadable log renders the FAILURE, never an empty box", async () => {
    const b = fakeBridge({ loggedIn: true });
    b.diag.readLog = vi
      .fn()
      .mockResolvedValue("Could not read the log at /x: EACCES");
    window.tunnex = b;
    render(<ClientApp />);
    fireEvent.click(await screen.findByRole("button", { name: "Logs" }));
    await waitFor(() => expect(screen.getByText(/EACCES/)).toBeTruthy());
  });
});

describe("version, updates and the tagline", () => {
  it("⛔ the client can state its own version — the first thing support asks for", async () => {
    window.tunnex = fakeBridge({ loggedIn: true });
    render(<ClientApp />);
    fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
    await waitFor(() =>
      expect(screen.getByText(/Tunnex v0\.1\.0/)).toBeTruthy(),
    );
  });

  it("⛔ NO 'Check for updates' button while updates cannot work — and it says why", async () => {
    // AUTOUPDATE_ENABLED is false and PINNED false by security.test.ts: Squirrel.Mac cannot verify
    // an unsigned app, so an unsigned auto-updater installs code with no signature check. And
    // build.publish is null — there is no feed to query even once signing lands.
    //
    // > **A SCAFFOLD IS NOT A ROLLOUT.** updater.ts imports electron-updater, sets a logger and
    // > returns — enough to look finished and enough to log a line every launch, which is exactly
    // > what made it look wired.
    window.tunnex = fakeBridge({ loggedIn: true });
    render(<ClientApp />);
    fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
    await waitFor(() =>
      expect(screen.getByText(/Automatic updates are off/i)).toBeTruthy(),
    );
    expect(
      screen.queryByRole("button", { name: /check for updates/i }),
    ).toBeNull();
  });

  it("the button appears only when a check could actually run", async () => {
    const b = fakeBridge({ loggedIn: true });
    b.diag.appInfo = vi
      .fn()
      .mockResolvedValue({ version: "9.9.9", update: { kind: "ready" } });
    window.tunnex = b;
    render(<ClientApp />);
    fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /check for updates/i }),
      ).toBeTruthy(),
    );
  });

  it("⛔ the header shows no raw tray-appearance word — that was a debug readout", async () => {
    // It printed "grey" / "solid" beside the dot: internal vocabulary for how the MENU-BAR ICON is
    // drawn, shown three lines above a status word that already says "Connected". The dot stays and
    // carries the state in its label instead.
    const b = fakeBridge({ loggedIn: true });
    b.tunnel.status = vi.fn().mockResolvedValue({ state: "up" });
    window.tunnex = b;
    const { container } = render(<ClientApp />);
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: /connected/i })).toBeTruthy(),
    );
    for (const word of ["grey", "solid", "pulsing", "red"]) {
      expect(screen.queryByText(word)).toBeNull();
    }
    const dot = container.querySelector("[data-tray]");
    expect(dot?.getAttribute("aria-label")).toMatch(/^Status: /);
  });

  it("the tagline is rendered from ONE definition, shared with the web shell", async () => {
    window.tunnex = fakeBridge({ loggedIn: true });
    render(<ClientApp />);
    expect(screen.getByText(/Connect Everything\./)).toBeTruthy();
    expect(screen.getByText(/Trust Nothing\./)).toBeTruthy();
  });
});

describe("an imported .conf connects, and says what it gives up", () => {
  it("⛔ import is reachable — a .conf from the control plane had no way in", async () => {
    const b = fakeBridge({ loggedIn: true });
    window.tunnex = b;
    render(<ClientApp />);
    fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
    fireEvent.click(
      await screen.findByRole("button", { name: /import \.conf/i }),
    );
    await waitFor(() => expect(b.tunnel.importConfig).toHaveBeenCalled());
  });

  it("⛔ the CONNECTION screen says revocation does not apply — not just settings", async () => {
    // An imported profile has no deviceId, so RevocationMonitor has nothing to poll: an admin
    // revoking this device is not reflected in the client. A mode degraded only in documentation
    // looks identical to the safe one, so the warning sits where the user actually is.
    const b = fakeBridge({ loggedIn: true });
    b.tunnel.importedInfo = vi
      .fn()
      .mockResolvedValue({ address: "10.99.0.7/32", fullTunnel: true });
    window.tunnex = b;
    render(<ClientApp />);
    await waitFor(() =>
      expect(
        screen.getByText(/revocation and posture checks do not apply/i),
      ).toBeTruthy(),
    );
  });

  it("⛔ a CANCELLED picker imports nothing and says nothing", async () => {
    const b = fakeBridge({ loggedIn: true });
    b.tunnel.importConfig = vi.fn().mockResolvedValue(null);
    window.tunnex = b;
    render(<ClientApp />);
    fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
    fireEvent.click(
      await screen.findByRole("button", { name: /import \.conf/i }),
    );
    await waitFor(() => expect(b.tunnel.importConfig).toHaveBeenCalled());
    // ⚠ ASSERT WHAT STAYS TRUE, NOT WHAT STAYS ABSENT. The first version of this test only checked
    // that the warning banner was missing — which it is either way, so the mutation "treat cancel
    // as an import" passed it. The state that actually distinguishes the two is the section still
    // offering to import, and no error being claimed for a cancel.
    expect(screen.getByRole("button", { name: /import \.conf/i })).toBeTruthy();
    expect(
      screen.queryByRole("button", { name: /remove imported profile/i }),
    ).toBeNull();
    expect(screen.queryByText(/that did not work/i)).toBeNull();
  });

  it("a malformed .conf surfaces the parser's refusal rather than half-importing", async () => {
    // parseWgConf is strict because the result is handed to a ROOT helper.
    const b = fakeBridge({ loggedIn: true });
    b.tunnel.importConfig = vi
      .fn()
      .mockRejectedValue(new Error("malformed .conf line: Addres = x"));
    window.tunnex = b;
    render(<ClientApp />);
    fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
    fireEvent.click(
      await screen.findByRole("button", { name: /import \.conf/i }),
    );
    await waitFor(() =>
      expect(screen.getByText(/malformed \.conf line/)).toBeTruthy(),
    );
  });

  it("an imported profile can be removed, returning to the account path", async () => {
    const b = fakeBridge({ loggedIn: true });
    b.tunnel.importedInfo = vi
      .fn()
      .mockResolvedValue({ address: "10.99.0.7/32", fullTunnel: false });
    window.tunnex = b;
    render(<ClientApp />);
    fireEvent.click(await screen.findByRole("button", { name: "Settings" }));
    fireEvent.click(
      await screen.findByRole("button", { name: /remove imported profile/i }),
    );
    await waitFor(() => expect(b.tunnel.forgetImported).toHaveBeenCalled());
  });
});

describe("the window IS the card — one surface, not a card inside a frame", () => {
  it("⛔ no inner card chrome: no max-width, no radius, no outer margin", async () => {
    // This test asserted the OPPOSITE one revision ago, and both versions were right in turn.
    //
    // The design draws the client as a 440px card with `margin:0 auto` — which is how it MUST be
    // drawn in a wireframe, because a wireframe is a web page and the card needs a page to sit on.
    // Transcribed literally into a fixed-width window it produced a card floating inside a window
    // frame: two chromes, one of them meaningless.
    //
    // > **A DESIGN'S CONTAINER IS NOT ALWAYS PART OF THE DESIGN.** Some of what a wireframe shows is
    // > the wireframe's own medium. The 440px width was real; the page it was centred on was not.
    window.tunnex = fakeBridge({ loggedIn: true });
    const { container } = render(<ClientApp />);
    const root = container.firstElementChild as HTMLElement;
    expect(root.className).not.toMatch(/max-w-/);
    expect(root.className).not.toMatch(/rounded-/);
    expect(root.className).not.toMatch(/\bp-4\b/);
    // It fills the viewport instead — the OS window draws the frame.
    expect(root.className).toContain("h-dvh");
    // And there is exactly ONE root: no wrapper-inside-a-wrapper.
    expect(container.children).toHaveLength(1);
    expect(root.querySelector(".max-w-\\[440px\\]")).toBeNull();
  });
});
