import { ref } from "vue";

import { BINDINGS, type MethodBinding } from "../src/client";
import type { HostContext } from "../src/host";

/**
 * A stand-in for the dashboard host, for looking at the UI in a browser.
 *
 * It answers the same wire shapes the plugin returns, so what renders here is
 * what renders in production — the point is to see the screens, not to
 * approximate them. Anything it cannot answer throws, because a mock that
 * silently returns undefined teaches the UI to tolerate nonsense.
 *
 * Never imported by `src/`; the shipped bundle is built from index.html alone.
 */

interface StoredRecord {
  id: string;
  kind?: string;
  name: string;
  display_name?: string;
  remark?: string;
  tags?: string[];
  source?: string;
  vpn_identity?: string;
  url?: string;
  content?: string;
  ua?: string;
  members?: string[];
  member_tags?: string[];
  failure_mode?: string;
  target?: string;
  process?: unknown[];
  origin?: unknown;
}

const OPERATORS = [
  "Add Proxies From Subscription Operator",
  "Conditional Filter",
  "Flag Operator",
  "Handle Duplicate Operator",
  "Quick Setting Operator",
  "Regex Delete Operator",
  "Regex Filter",
  "Regex Rename Operator",
  "Regex Sort Operator",
  "Region Filter",
  "Remove Duplicate Filter",
  "Resolve Domain Operator",
  "Script Filter",
  "Script Operator",
  "Sort Operator",
  "Type Filter",
  "Useless Filter",
];

const SCRIPTING = new Set(["Script Operator", "Script Filter"]);

/** Shaped like a real deployment: a fleet source, a provider, and a paste. */
const records: StoredRecord[] = [
  {
    id: "home-nodes",
    name: "Home nodes",
    tags: ["home", "self"],
    source: "vpn-core",
    target: "",
    process: [
      { type: "Useless Filter" },
      { type: "Quick Setting Operator", args: { udp: true } },
      { type: "Regex Rename Operator", args: { value: [["^HK", "🇭🇰 Hong Kong"]] } },
    ],
  },
  {
    id: "provider-a",
    name: "Provider A",
    tags: ["paid"],
    source: "remote",
    url: "https://example.invalid/subscribe",
    ua: "Surge",
    process: [{ type: "Region Filter", args: { value: ["HK", "JP"], keep: true } }],
  },
  {
    id: "pasted-backup",
    name: "Pasted backup",
    tags: ["backup"],
    source: "local",
    content: "vless://11111111-1111-1111-1111-111111111111@a.example:443#node-a",
    process: [],
  },
  {
    id: "everything",
    kind: "collection",
    name: "Everything",
    tags: ["home"],
    members: ["home-nodes", "provider-a"],
    member_tags: ["backup"],
    failure_mode: "strict",
    target: "Clash",
    process: [{ type: "Remove Duplicate Filter" }, { type: "Sort Operator", args: { value: "asc" } }],
  },
];

let settings: Record<string, unknown> = { default_target: "", default_ua: "" };

function listView(rec: StoredRecord) {
  const steps = (rec.process ?? []) as { disabled?: boolean }[];
  return {
    id: rec.id,
    kind: rec.kind || "sub",
    name: rec.name,
    display_name: rec.display_name,
    remark: rec.remark,
    tags: rec.tags,
    source: rec.source,
    has_url: Boolean(rec.url),
    has_inline_content: Boolean(rec.content),
    members: rec.members,
    member_tags: rec.member_tags,
    target: rec.target,
    step_count: steps.length,
    disabled_step_count: steps.filter((s) => s.disabled).length,
    imported: Boolean(rec.origin),
  };
}

const HANDLERS: Record<string, (payload: any) => unknown> = {
  "subscription/list": () => ({ subscriptions: records.map(listView) }),
  "subscription/operators": () => ({
    operators: OPERATORS.map((type) => ({ type, scripting: SCRIPTING.has(type) })),
  }),
  "subscription/get": ({ subscription_id }) => {
    const found = records.find((r) => r.id === subscription_id);
    if (!found) throw new Error(`subscription "${subscription_id}" was not found`);
    return { subscription: found };
  },
  "subscription/save": ({ subscription }) => {
    const index = records.findIndex((r) => r.id === subscription.id);
    if (index === -1) records.push(subscription);
    else records[index] = subscription;
    return { subscription, saved: true };
  },
  "subscription/delete": ({ subscription_id }) => {
    const index = records.findIndex((r) => r.id === subscription_id);
    if (index === -1) throw new Error(`subscription "${subscription_id}" was not found`);
    records.splice(index, 1);
    return { id: subscription_id, deleted: true };
  },
  "subscription/fetch": ({ subscription_id }) => ({ subscription_id, bytes: 4096 }),
  "subscription/preview": () => ({
    nodes: [
      { name: "🇭🇰 Hong Kong 01", type: "vless" },
      { name: "🇭🇰 Hong Kong 02", type: "vless" },
      { name: "🇯🇵 Tokyo 01", type: "trojan" },
      { name: "🇸🇬 Singapore 01", type: "vmess" },
    ],
    count: 4,
  }),
  "subscription/get_settings": () => settings,
  "subscription/save_settings": (payload) => {
    settings = { ...settings, ...payload };
    return settings;
  },
  "subscription/export": () => ({ backup: JSON.stringify({ version: 1, records }, null, 2) }),
  "subscription/import": () => ({ imported: records.map((r) => r.id) }),
  "subscription/migrate": () => ({ imported: ["migrated-one"], skipped: { "migrated-two": "no nodes" } }),
};

/** Slight latency so loading states are visible rather than theoretical. */
function delay<T>(value: T): Promise<T> {
  return new Promise((resolve) => setTimeout(() => resolve(value), 180));
}

export function createFakeHost(): HostContext {
  const init = ref<any>();
  const bootError = ref("");

  // The handshake lands late on purpose: a screen that loads before it and
  // never retries is the bug this harness exists to make visible.
  setTimeout(() => {
    init.value = {
      version: "1",
      pluginId: "latticenet.sub-store",
      route: "sub-store",
      interfaces: [
        {
          service: "latticenet.sub-store/subscription",
          methods: [
            "fetch", "render", "operators", "preview", "list", "get", "save", "delete",
            "migrate", "export", "import", "get_settings", "save_settings", "publish",
          ],
        },
        {
          service: "latticenet.sub-store/engine",
          methods: [
            "convert", "transform_response", "save_pipeline", "get_pipeline",
            "list_pipelines", "delete_pipeline", "run_pipeline",
          ],
        },
      ],
    };
  }, 400);

  const bridge = {
    call<T>(service: string, method: string, payload: unknown) {
      const key = `${service.split("/").pop()}/${method}`;
      const handler = HANDLERS[key];
      const promise = handler
        ? delay(handler((payload ?? {}) as any) as T)
        : Promise.reject(new Error(`the dev harness has no answer for ${key}`));
      return { promise, cancel: () => {} };
    },
    resize() {},
    dispose() {},
    init: Promise.resolve(undefined),
  } as unknown as HostContext["bridge"];

  return {
    bridge,
    init,
    bootError,
    available: (target: MethodBinding) =>
      init.value?.interfaces.some(
        (contract: any) =>
          contract.service === target.service && contract.methods.includes(target.method),
      ) === true,
    resize: async () => {},
  };
}

export { BINDINGS };
