export interface CallableInterface {
  service: string;
  methods: string[];
}

export interface HostInit {
  version: string;
  pluginId: string;
  pluginVersion: string;
  pluginRoute: string;
  locale: string;
  colorScheme: string;
  designTokens: Record<string, string>;
  interfaces: CallableInterface[];
}

type Pending = {
  resolve: (value: unknown) => void;
  reject: (reason: Error) => void;
  timer: ReturnType<typeof setTimeout>;
};

type PluginMessage =
  | { type: "lattice.plugin.ready"; nonce: string }
  | { type: "lattice.plugin.call"; nonce: string; id: string; service: string; method: string; payload: unknown }
  | { type: "lattice.plugin.cancel"; nonce: string; id: string }
  | { type: "lattice.plugin.resize"; nonce: string; height: number };

const TOKEN_NAMES = new Set([
  "--background", "--foreground", "--card", "--card-foreground", "--muted",
  "--muted-foreground", "--border", "--primary", "--primary-foreground",
  "--destructive", "--ring",
]);
const EXPECTED_PLUGIN_ID = "latticenet.sub-store";
const EXPECTED_ROUTE = "sub-store";
const READY_RETRY_MS = 500;
const READY_ATTEMPT_LIMIT = 16;

export class BridgeClient {
  readonly nonce: string;
  readonly init: Promise<HostInit>;

  private readonly win: Window;
  private readonly pending = new Map<string, Pending>();
  private initResolve!: (value: HostInit) => void;
  private initReject!: (reason: Error) => void;
  private sequence = 0;
  private disposed = false;
  private readyAttempts = 0;
  private readyTimer: ReturnType<typeof setTimeout> | undefined;

  constructor(win: Window) {
    this.win = win;
    this.nonce = readNonce(win.location.hash);
    this.init = new Promise<HostInit>((resolve, reject) => {
      this.initResolve = resolve;
      this.initReject = reject;
    });
    this.init.catch(() => {});
    this.onMessage = this.onMessage.bind(this);
    this.win.addEventListener("message", this.onMessage);
    this.postReady();
  }

  call<T>(service: string, method: string, payload: unknown, timeoutMs = 15_000): { promise: Promise<T>; cancel: () => void } {
    if (this.disposed) throw new Error("plugin bridge is disposed");
    const id = `substore-${++this.sequence}`;
    let cancel = () => {};
    const promise = new Promise<T>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        this.post({ type: "lattice.plugin.cancel", nonce: this.nonce, id });
        reject(new Error("Request timed out"));
      }, timeoutMs);
      this.pending.set(id, {
        resolve: resolve as (value: unknown) => void,
        reject,
        timer,
      });
      cancel = () => {
        const pending = this.pending.get(id);
        if (!pending) return;
        clearTimeout(pending.timer);
        this.pending.delete(id);
        this.post({ type: "lattice.plugin.cancel", nonce: this.nonce, id });
        pending.reject(new Error("Request cancelled"));
      };
      this.post({ type: "lattice.plugin.call", nonce: this.nonce, id, service, method, payload });
    });
    promise.catch(() => {});
    return { promise, cancel };
  }

  resize(height: number): void {
    if (!this.disposed && Number.isFinite(height)) {
      this.post({ type: "lattice.plugin.resize", nonce: this.nonce, height: Math.ceil(height) });
    }
  }

  dispose(): void {
    this.failBridge(new Error("Plugin host disconnected"));
  }

  private onMessage(event: MessageEvent): void {
    if (this.disposed || event.source !== this.win.parent || !isRecord(event.data) || event.data.nonce !== this.nonce) return;
    const message = event.data;
    switch (message.type) {
      case "lattice.host.init": {
        const init = parseInit(message);
        if (!init) return;
        this.clearReadyTimer();
        applyTheme(init.colorScheme, init.designTokens);
        this.initResolve(init);
        return;
      }
      case "lattice.host.theme":
        if (typeof message.colorScheme === "string" && isStringRecord(message.designTokens)) {
          applyTheme(message.colorScheme, message.designTokens);
        }
        return;
      case "lattice.host.result":
        this.finish(message.id, undefined, message.result);
        return;
      case "lattice.host.error":
        if (typeof message.id === "string") {
          this.finish(message.id, new Error(typeof message.message === "string" ? message.message : "Plugin call failed"));
        } else {
          this.failBridge(new Error(typeof message.message === "string" ? message.message : "Plugin host rejected initialization"));
        }
        return;
      case "lattice.host.dispose":
        this.dispose();
    }
  }

  private finish(value: unknown, error?: Error, result?: unknown): void {
    if (typeof value !== "string") return;
    const pending = this.pending.get(value);
    if (!pending) return;
    clearTimeout(pending.timer);
    this.pending.delete(value);
    if (error) pending.reject(error);
    else pending.resolve(result);
  }

  private post(message: PluginMessage): void {
    this.win.parent.postMessage(message, "*");
  }

  private postReady(): void {
    if (this.disposed || this.readyAttempts >= READY_ATTEMPT_LIMIT) return;
    this.readyAttempts += 1;
    this.post({ type: "lattice.plugin.ready", nonce: this.nonce });
    if (this.readyAttempts < READY_ATTEMPT_LIMIT) {
      this.readyTimer = setTimeout(() => this.postReady(), READY_RETRY_MS);
    }
  }

  private clearReadyTimer(): void {
    if (this.readyTimer !== undefined) clearTimeout(this.readyTimer);
    this.readyTimer = undefined;
  }

  private failBridge(error: Error): void {
    if (this.disposed) return;
    this.disposed = true;
    this.clearReadyTimer();
    this.win.removeEventListener("message", this.onMessage);
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timer);
      pending.reject(error);
    }
    this.pending.clear();
    this.initReject(error);
  }
}

export function canCall(init: HostInit | undefined, service: string, method: string): boolean {
  return init?.interfaces.some((contract) => contract.service === service && contract.methods.includes(method)) === true;
}

function readNonce(hash: string): string {
  const nonce = new URLSearchParams(hash.replace(/^#/, "")).get("lattice_nonce");
  if (!nonce || nonce.length < 16 || nonce.length > 128) throw new Error("Missing plugin channel nonce");
  return nonce;
}

function parseInit(message: Record<string, unknown>): HostInit | undefined {
  if (typeof message.version !== "string" || typeof message.pluginId !== "string" ||
      typeof message.pluginVersion !== "string" || typeof message.pluginRoute !== "string" ||
      typeof message.locale !== "string" || typeof message.colorScheme !== "string" ||
      !isStringRecord(message.designTokens) || !Array.isArray(message.interfaces) ||
      message.version !== "1" || message.pluginId !== EXPECTED_PLUGIN_ID ||
      message.pluginRoute !== EXPECTED_ROUTE) return undefined;
  const interfaces: CallableInterface[] = [];
  for (const value of message.interfaces) {
    if (!isRecord(value) || typeof value.service !== "string" || !Array.isArray(value.methods) ||
        !value.methods.every((method) => typeof method === "string")) return undefined;
    interfaces.push({ service: value.service, methods: value.methods as string[] });
  }
  return {
    version: message.version,
    pluginId: message.pluginId,
    pluginVersion: message.pluginVersion,
    pluginRoute: message.pluginRoute,
    locale: message.locale,
    colorScheme: message.colorScheme,
    designTokens: message.designTokens,
    interfaces,
  };
}

function applyTheme(colorScheme: string, tokens: Record<string, string>): void {
  if (typeof document === "undefined") return;
  document.documentElement.style.colorScheme = colorScheme === "dark" ? "dark" : "light";
  document.documentElement.dataset.theme = colorScheme === "dark" ? "dark" : "light";
  for (const [name, value] of Object.entries(tokens)) {
    if (TOKEN_NAMES.has(name)) document.documentElement.style.setProperty(name, value);
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isStringRecord(value: unknown): value is Record<string, string> {
  return isRecord(value) && Object.values(value).every((item) => typeof item === "string");
}
