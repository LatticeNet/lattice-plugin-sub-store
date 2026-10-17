/**
 * The harness's half of design 28 S2 lane 6b: core's plans service
 * (propose, status, publish), the plugin's migrate_record and
 * discard_staged, the staged state the list reports, the structured delete
 * refusal of a fleet-bound record, the owner-credential withholding of a
 * legacy record's render and probe, and bind_selection, answered in the
 * plugin's and core's wire shapes over the records of planFixtures.ts.
 *
 * Inert without `?manifest=s2`. `?plans=` picks a state:
 *   denied    a token without proxy:admin and substore:admin on the plans
 *             service: the handshake omits plans.propose and plans.publish
 *   large     propose answers core's 1 MiB refusal
 *   failed    asia-fleet's plan was approved and its apply failed; the record
 *             was not promoted
 *   promoted  the same failure, and the list shows the record promoted
 *   partial   a multi-record migration stopped after promoting
 *             promoted-member; legacy-left is still staged
 *   stale     asia-fleet's plan went stale: its staged revision was replaced
 *   closure   asia-fleet's plan went stale: a combination started reading it
 *   bind      core serves bind_selection (operator decision 8 taken)
 *   sibling   the first migrate_record of home-graph, the sibling staged
 *             with relay-graph, fails; a retry stages it
 *   applied   a plan proposed in this sitting reads as approved and
 *             applied the next time its status is read (Check again)
 *   rejected  the same plan reads as rejected by the approver, not stale
 *
 * In every state, under `?fixture=plans` (lane 6c):
 *   provider members (S2-SR-1): a save of a record that is not fleet-bound
 *     but a fleet-bound combination gathers (openjobs-host, paid-extra)
 *     answers staged=true and waits in the staged store; its plain delete
 *     answers fleet_delete_requires_plan naming the combinations; and a
 *     delete plan's closure holds the combinations that read it. A
 *     fleet-bound record's own save still writes live here, so the drives
 *     written before staging keep working; the plugin stages those too.
 *   chain changes (S2-SR-2): every section of a plan carries chain
 *     {before, after, changes}, from the live and staged steps, in core's
 *     words; tokyo-scripted's staged revision adds a pass-through script
 *     and moves no line.
 *   the script network refusal (S2-SR-2): a fleet render whose chain holds
 *     a step that asks for the network (osaka-lookup) is refused, in each
 *     shape it can arrive in: the plugin's sentence on preview and
 *     preview_draft, core's sentence on bind.preview, and core's typed
 *     refusal as JSON after the code on plans.propose.
 *   get answers a waiting staged revision beside the live record, so the
 *     editor opens on it, as the plugin answers it.
 *
 * Never imported by `src/`; the shipped bundle is built from index.html alone.
 */
import type { MethodBinding } from "../src/client";
import { chainChanges, type ChainChange } from "../src/planFlow";
import { SCRIPT_NETWORK_CODE } from "../src/scriptNetwork";
import type { FleetRecord } from "./fleetFixtures";
import { planStagedRecords, type PlanShare } from "./planFixtures";
import { CATALOGUE_VERSION, catalogueRows } from "./fleetFixtures";
import { s2Manifest } from "./fleetHost";

/** Whether the harness serves the plans fixture (`?fixture=plans`). */
function plansFixture(): boolean {
  return new URLSearchParams(window.location.search).get("fixture") === "plans";
}

/** A chain change in core's words (lattice-server substore_svc_plans_chain.go). */
function coreChainLine(change: ChainChange): string {
  if (change.kind === "many") return `the steps changed: ${change.before} before, ${change.after} after, too many to list one by one`;
  if (change.kind === "more") return `and ${change.count} more step changes`;
  if (change.kind === "text") return change.text;
  const notes = change.notes.map((note) => (note.kind === "moved" ? `was step ${note.from}` : note.kind === "now-enabled" ? "now enabled" : note.kind === "now-disabled" ? "now disabled" : "disabled"));
  return `step ${change.position} ${change.verb}: ${change.type}${notes.length ? ` (${notes.join(", ")})` : ""}`;
}

/**
 * The first enabled step of a chain that asks for the network on a fleet
 * render: a Resolve Domain step, or a script whose body calls the network.
 * 1-based, or 0 when none does.
 */
function networkAskingStep(process: readonly unknown[]): { position: number; type: string } | null {
  for (const [index, raw] of process.entries()) {
    const step = raw as { type?: string; disabled?: boolean; args?: { content?: unknown } } | null;
    if (!step || step.disabled) continue;
    if (step.type === "Resolve Domain Operator") return { position: index + 1, type: step.type };
    if ((step.type === "Script Operator" || step.type === "Script Filter") && /\$substore\.http|\bfetch\(/.test(String(step.args?.content ?? ""))) return { position: index + 1, type: step.type };
  }
  return null;
}

export type PlansHarnessState = "ok" | "denied" | "large" | "failed" | "promoted" | "partial" | "stale" | "closure" | "bind" | "sibling" | "applied" | "rejected";

export function plansState(): PlansHarnessState {
  const asked = new URLSearchParams(window.location.search).get("plans");
  const known: PlansHarnessState[] = ["ok", "denied", "large", "failed", "promoted", "partial", "stale", "closure", "bind", "sibling", "applied", "rejected"];
  return (known as string[]).includes(asked ?? "") ? (asked as PlansHarnessState) : "ok";
}

/**
 * What a token without the plans scopes does not see (`?plans=denied`), and
 * what a read-only session (`?state=readonly`) does not see of S2: every
 * write the plan flow makes.
 */
export function plansWithheld(target: MethodBinding): boolean {
  const readonly = new URLSearchParams(window.location.search).get("state") === "readonly";
  const plans = target.service === "latticenet.sub-store/plans" && (target.method === "propose" || target.method === "publish");
  if (plansState() === "denied") return plans;
  if (!readonly) return false;
  return plans || ["migrate_record", "discard_staged", "bind_selection"].includes(target.method);
}

const LEGACY = new Set(["vpn-core", "vpn-core-graph"]);
const STALE_STAGED = "the staged revision this plan publishes was replaced or discarded since it was proposed; propose the new one";
const STALE_CLOSURE = "the records this plan covers changed since it was proposed: a collection started or stopped reading one of them; propose it again";
const FAILED = "apply_revision failed; the record may have been promoted: list shows its live revision. Propose again if it was not";
const REJECTED = "rejected by the approver: the identity should not receive these lines";

interface Staged {
  revision: string;
  at: string;
  source: string;
  migrated: boolean;
  process?: unknown[];
  /** The whole staged record, which get answers beside the live one; absent for a seeded revision that has only its steps. */
  record?: FleetRecord;
}

interface PlanRow {
  approval_id: string;
  status: string;
  reason?: string;
  stale?: boolean;
  action: string;
  mode: string;
  plan_sha256: string;
  subscription_id: string;
  from_revision: string;
  to_revision: string;
  created_at: string;
  updated_at: string;
  closure: string[];
}

type Handler = (payload: any) => unknown;

/** A revision as the plugin writes one: 16 bytes of a digest, hex (system-go digestOf). */
function hex(text: string): string {
  let out = "";
  let h = 2166136261;
  for (let round = 0; out.length < 32; round += 1) {
    for (const char of `${round}:${text}`) h = Math.imul(h ^ char.charCodeAt(0), 16777619) >>> 0;
    out += h.toString(16).padStart(8, "0");
  }
  return out.slice(0, 32);
}

function refused(code: string, message: string, extra: Record<string, unknown> = {}) {
  return { saved: false, refused: { code, message, ...extra } };
}

/** Plugs the plan answers into the harness's handler table. */
export function installPlans(handlers: Record<string, Handler>, records: FleetRecord[], shares: PlanShare[]): void {
  const state = plansState();
  const staged = new Map<string, Staged>();
  const live = new Map<string, string>();
  const plans: PlanRow[] = [];
  const texts = new Map<string, string>();
  let approvals = 0;
  const now = () => new Date().toISOString();
  const byId = (id: string) => records.find((record) => record.id === id);
  const liveRevision = (id: string) => live.get(id) ?? hex(`live:${id}`);
  const ASIA_STAGED = hex("staged:asia-fleet:1");
  const LEFT_STAGED = hex("staged:legacy-left:1");
  const PROMOTED_TO = hex("staged:promoted-member:1");

  if (byId("asia-fleet")) {
    staged.set("asia-fleet", {
      revision: ASIA_STAGED,
      at: new Date(Date.now() - 600_000).toISOString(),
      source: "fleet",
      migrated: false,
      process: [
        { type: "Structured Filter", args: { mode: "keep", match: "all", predicates: [{ field: "country", op: "in", values: ["HK", "JP", "SG", "TW"] }] } },
      ],
    });
  }
  if (byId("legacy-left")) {
    staged.set("legacy-left", { revision: LEFT_STAGED, at: new Date(Date.now() - 3_600_000).toISOString(), source: "fleet", migrated: true });
  }
  for (const seed of planStagedRecords()) {
    const record = byId(seed.id);
    if (!record) continue;
    staged.set(seed.id, {
      revision: hex(`staged:${seed.id}:1`),
      at: new Date(Date.now() - seed.minutesAgo * 60_000).toISOString(),
      source: record.source ?? "",
      migrated: false,
      process: seed.process,
      record: { ...record, process: seed.process },
    });
  }

  function addPlan(row: Omit<PlanRow, "approval_id" | "plan_sha256" | "created_at" | "updated_at">, text?: string): PlanRow {
    approvals += 1;
    const full: PlanRow = {
      approval_id: `apr_${String(approvals).padStart(4, "0")}${row.subscription_id.replace(/[^a-z]/g, "").slice(0, 6)}`,
      plan_sha256: `${approvals}`.padStart(64, "a"),
      created_at: new Date(Date.now() - 1_800_000 + approvals * 1000).toISOString(),
      updated_at: now(),
      ...row,
    };
    plans.unshift(full);
    if (text) texts.set(full.approval_id, text);
    return full;
  }

  // The plans the states start from.
  if (byId("asia-fleet") && state !== "partial") {
    const base = { action: "promote", mode: "promote", subscription_id: "asia-fleet", from_revision: liveRevision("asia-fleet"), to_revision: ASIA_STAGED, closure: ["asia-fleet", "asia-combination"] };
    if (state === "failed") addPlan({ ...base, status: "approved", reason: FAILED });
    else if (state === "promoted") {
      addPlan({ ...base, status: "approved", reason: FAILED });
      live.set("asia-fleet", base.to_revision);
      staged.delete("asia-fleet");
    } else if (state === "stale") addPlan({ ...base, status: "rejected", reason: STALE_STAGED, stale: true });
    else if (state === "closure") addPlan({ ...base, status: "rejected", reason: STALE_CLOSURE, stale: true });
    else addPlan({ ...base, status: "pending" });
  }
  if (state === "partial" && byId("promoted-member")) {
    const from = liveRevision("promoted-member");
    live.set("promoted-member", PROMOTED_TO);
    addPlan({
      action: "promote",
      mode: "promote",
      subscription_id: "promoted-member",
      from_revision: from,
      to_revision: PROMOTED_TO,
      closure: ["promoted-member", "legacy-left", "mixed-combo"],
      status: "approved",
      reason:
        "apply_revision failed at record legacy-left; records applied before it: promoted-member, with their shares' changes written. The record may have been promoted: list shows its live revision. Propose the rest",
    });
  }

  // ── facts ────────────────────────────────────────────────────────────────

  function gathers(collection: FleetRecord, member: FleetRecord): boolean {
    if (collection.kind !== "collection" || collection.id === member.id) return false;
    if ((collection.members ?? []).includes(member.id)) return true;
    const tags = new Set(member.tags ?? []);
    return (collection.member_tags ?? []).some((tag) => tags.has(tag));
  }
  function closureOf(id: string): string[] {
    const out = [id];
    for (let at = 0; at < out.length; at += 1) {
      const current = byId(out[at]!);
      if (!current) continue;
      for (const candidate of records) if (!out.includes(candidate.id) && gathers(candidate, current)) out.push(candidate.id);
    }
    return out;
  }
  function legacyReach(id: string, seen = new Set<string>()): boolean {
    if (seen.has(id)) return false;
    seen.add(id);
    const record = byId(id);
    if (!record) return false;
    if (record.kind !== "collection" && record.kind !== "file" && LEGACY.has(record.source ?? "")) return true;
    if (record.kind === "file") return record.node_source ? legacyReach(record.node_source, seen) : false;
    return records.some((member) => gathers(record, member) && legacyReach(member.id, seen));
  }
  function fleetReach(id: string, seen = new Set<string>()): boolean {
    if (seen.has(id)) return false;
    seen.add(id);
    const record = byId(id);
    if (!record) return false;
    if (record.kind !== "collection" && record.kind !== "file") return record.source === "fleet";
    if (record.kind === "file") return false;
    return records.some((member) => gathers(record, member) && fleetReach(member.id, seen));
  }
  const liveShares = (ids: string[]) => shares.filter((entry) => ids.includes(entry.subscription_id) && !(entry as { archived_at?: string }).archived_at);
  /** The fleet-bound collections that gather a record directly (the plugin's fleetGatherers). */
  function gatherersOf(record: FleetRecord): string[] {
    return records.filter((candidate) => candidate.kind === "collection" && gathers(candidate, record) && fleetReach(candidate.id)).map((candidate) => candidate.id).sort();
  }
  /** A record's steps on a side of a plan: its staged revision's, or its live ones. */
  const stepsAt = (id: string, side: "live" | "staged") => (side === "staged" ? staged.get(id)?.process ?? byId(id)?.process : byId(id)?.process) ?? [];
  /** A section's chain as core writes it: each side's digest, and the step changes between them. */
  function chainOf(id: string, before: "live" | "staged" | "", after: "live" | "staged" | "") {
    const a = before ? stepsAt(id, before) : [];
    const b = after ? stepsAt(id, after) : [];
    const digest = (steps: unknown[]) => hex(`chain:${JSON.stringify(steps)}`) + hex(`chain2:${JSON.stringify(steps)}`);
    const changes = before && after ? chainChanges(a, b).map(coreChainLine) : !before && after ? chainChanges([], b).map(coreChainLine) : [];
    return { ...(before ? { before: digest(a) } : {}), ...(after ? { after: digest(b) } : {}), ...(changes.length ? { changes } : {}) };
  }

  // ── the list ─────────────────────────────────────────────────────────────

  const list = handlers["subscription/list"]!;
  handlers["subscription/list"] = (payload) => {
    const reply = list(payload) as { subscriptions?: Record<string, unknown>[] };
    if (!s2Manifest() || !reply.subscriptions) return reply;
    reply.subscriptions = reply.subscriptions.map((row) => {
      const id = String(row.id);
      const next: Record<string, unknown> = { ...row };
      for (const key of Object.keys(next)) if (key.startsWith("staged_")) delete next[key];
      if (typeof next.revision === "string") next.revision = liveRevision(id);
      const flags = { ...((next.flags as Record<string, unknown>) ?? {}) };
      delete flags.fleet_bound;
      delete flags.owner_credentials;
      if (fleetReach(id)) flags.fleet_bound = true;
      if (legacyReach(id)) flags.owner_credentials = true;
      next.flags = Object.keys(flags).length ? flags : undefined;
      const entry = staged.get(id);
      if (entry) {
        next.staged_revision = entry.revision;
        next.staged_at = entry.at;
        next.staged_source = entry.source;
        if (entry.migrated) next.staged_migrated = true;
      }
      return next;
    });
    return reply;
  };

  // ── shares ───────────────────────────────────────────────────────────────

  handlers["shares/list"] = () => ({ shares: shares.filter((entry) => !(entry as { archived_at?: string }).archived_at) });

  // ── the plan text core writes, close enough to draw ─────────────────────

  const preview = () => handlers["bind/preview"]!;
  /** bind.preview over a record, with its staged chain in place when `useStaged`. */
  function bindFor(id: string, identity: string, useStaged: boolean): { entries: any[]; excluded: any[] } {
    const record = byId(id);
    // A provider record carries no fleet line: only its own nodes, which no identity diff counts as lines.
    if (record && record.kind !== "collection" && record.source !== "fleet" && !(useStaged && staged.get(id)?.source === "fleet")) {
      return {
        entries: [
          { index: 0, provider: true, name: "Provider HK 01", protocol: "trojan", server: "hk1.provider.example", port: 443, digest: "p-hk01" },
          { index: 1, provider: true, name: "Provider JP 02", protocol: "vmess", server: "jp2.provider.example", port: 8443, digest: "p-jp02" },
        ],
        excluded: [],
      };
    }
    const entry = staged.get(id);
    const before = record?.process;
    const swap = record && useStaged && entry?.process;
    try {
      if (swap) record!.process = entry!.process;
      return preview()({ subscription_id: id, identity_id: identity }) as { entries: any[]; excluded: any[] };
    } catch {
      return { entries: [], excluded: [] };
    } finally {
      if (swap) record!.process = before;
    }
  }

  function buildPlan(primary: string, promoted: string[], changes: Record<string, string>, archive: string[], mode: string, action: string) {
    const sections: any[] = [];
    // A delete's closure holds the collections that read the record, whose holders lose its nodes (S2-SR-1).
    const ids = [...new Set((mode === "delete" ? [primary] : promoted).flatMap((id) => closureOf(id)))];
    const totals = { shares: 0, unbound_shares: 0, identities: 0, added: 0, removed: 0, changed: 0, excluded: 0 };
    const assignments: any[] = [];
    const clearings: any[] = [];
    const archivals: any[] = [];
    for (const id of ids) {
      const sectionShares: any[] = [];
      const unbound: any[] = [];
      const identities = new Map<string, any>();
      for (const entry of liveShares([id])) {
        const assigned = changes[entry.share_id];
        const identity = assigned !== undefined ? assigned : entry.identity_id ?? "";
        // A delete plan archives the record's own shares (s2-plan section 2.6).
        const archived = archive.includes(entry.share_id) || (mode === "delete" && id === primary);
        if (archived) archivals.push({ share_id: entry.share_id, subscription_id: id, slug: entry.slug, display_name: entry.display_name, identity_id: entry.identity_id });
        if (assigned) assignments.push({ share_id: entry.share_id, subscription_id: id, slug: entry.slug, display_name: entry.display_name, identity_id: assigned, lines: 0 });
        if (assigned === "" && entry.identity_id) clearings.push({ share_id: entry.share_id, subscription_id: id, slug: entry.slug, display_name: entry.display_name, identity_id: entry.identity_id, lines: 0 });
        if (!identity) {
          unbound.push({ share_id: entry.share_id, slug: entry.slug, display_name: entry.display_name, enabled: true, fleet_credentials: false, ...(archived ? { archive: true } : {}) });
          totals.unbound_shares += 1;
          continue;
        }
        if (!identities.has(identity)) {
          // A combination over a promoted member receives that member's staged lines.
          const collection = byId(id);
          const members = !promoted.includes(id) && collection ? promoted.filter((member) => byId(member) && gathers(collection, byId(member)!)) : [];
          const bindAt = (useStaged: boolean) =>
            members.length
              ? members.map((member) => bindFor(member, identity, useStaged)).reduce((all, one) => ({ entries: [...all.entries, ...one.entries], excluded: [...all.excluded, ...one.excluded] }))
              : bindFor(id, identity, useStaged && promoted.includes(id));
          const bound = mode === "delete" ? { entries: [], excluded: [] } : bindAt(true);
          // A share that already served this identity is diffed against what it serves now; one given the identity by this plan gains every line.
          const before = mode === "promote" && entry.identity_id && !assigned && (members.length || promoted.includes(id)) ? bindAt(false) : null;
          const lineOf = (e: any) => e.line_uuid as string;
          const beforeLines = new Set((before?.entries ?? []).filter((e: any) => !e.provider).map(lineOf));
          const afterLines = new Set(bound.entries.filter((e: any) => !e.provider).map(lineOf));
          // A collection reading a deleted provider record loses that record's nodes; the record's own holders lose everything.
          const deleteLoss =
            id === primary
              ? [{ line_uuid: "", names: ["every line"], digest: "" }]
              : [
                  { line_uuid: "", names: ["Provider HK 01"], digest: "p-hk01" },
                  { line_uuid: "", names: ["Provider JP 02"], digest: "p-jp02" },
                ];
          identities.set(identity, {
            identity_id: identity,
            added: bound.entries.filter((e) => !e.provider && !beforeLines.has(lineOf(e))).map((e) => ({ line_uuid: e.line_uuid, names: [e.label || e.name], digest: e.digest })),
            removed:
              mode === "delete"
                ? deleteLoss
                : (before?.entries ?? []).filter((e: any) => !e.provider && !afterLines.has(lineOf(e))).map((e: any) => ({ line_uuid: e.line_uuid, names: [e.label || e.name], digest: e.digest })),
            changed: [],
            excluded: bound.excluded.map((e) => ({ line_uuid: e.line_uuid, name: e.name, reason: e.reason })),
            unchanged: [...afterLines].filter((line) => beforeLines.has(line)).length,
          });
        }
        const lines = identities.get(identity)!;
        const record = assignments.find((a) => a.share_id === entry.share_id);
        if (record) record.lines = lines.added.length;
        sectionShares.push({
          share_id: entry.share_id,
          slug: entry.slug,
          display_name: entry.display_name,
          identity_id: identity,
          enabled: true,
          added: lines.added.length,
          removed: lines.removed.length,
          changed: 0,
          excluded: lines.excluded.length,
          ...(assigned ? { assigned: true } : {}),
          ...(archived ? { archive: true } : {}),
        });
        totals.shares += 1;
      }
      for (const identity of identities.values()) {
        totals.identities += 1;
        totals.added += identity.added.length;
        totals.removed += identity.removed.length;
        totals.excluded += identity.excluded.length;
      }
      const role = mode === "delete" ? (id === primary ? "delete" : "dependent") : mode === "promote" && promoted.includes(id) ? "promote" : id === primary ? "self" : "dependent";
      // Every harness record has a live revision, so a promotion's chain always has its before side.
      const chain = role === "promote" ? chainOf(id, "live", "staged") : role === "delete" ? chainOf(id, "live", "") : chainOf(id, "live", "live");
      sections.push({
        subscription_id: id,
        role,
        from_revision: liveRevision(id),
        ...(promoted.includes(id) && staged.get(id) ? { to_revision: staged.get(id)!.revision } : {}),
        chain,
        totals: { ...totals },
        shares: sectionShares,
        unbound_shares: unbound,
        identities: [...identities.values()],
      });
    }
    const first = sections[0] ?? { shares: [], unbound_shares: [], identities: [], chain: undefined };
    return {
      kind: "sub-store.plan",
      plugin_id: "latticenet.sub-store",
      subscription_id: primary,
      from_revision: liveRevision(primary),
      to_revision: mode === "promote" ? staged.get(primary)?.revision ?? "" : mode === "assign" ? liveRevision(primary) : "",
      action,
      mode,
      totals,
      primary_totals: first.totals ?? totals,
      ...(first.chain ? { chain: first.chain } : {}),
      shares: first.shares,
      unbound_shares: first.unbound_shares,
      coverage: "An unbound share on a fleet-bound record answers the decoy.",
      identities: first.identities,
      records: sections,
      apply_order: promoted,
      closure: ids,
      assignments,
      clearings,
      archivals,
    };
  }

  // ── plans.propose, plans.publish, plans.status ───────────────────────────

  handlers["plans/propose"] = (request: {
    subscription_id: string;
    from_revision: string;
    to_revision?: string;
    action?: string;
    records?: { subscription_id: string; from_revision: string; to_revision: string }[];
    share_identities?: Record<string, string>;
    archive_shares?: string[];
  }) => {
    const primary = request.subscription_id;
    const record = byId(primary);
    if (!record) throw new Error(`not_found: record ${primary} is not in the store`);
    if (state === "large") throw new Error("the plan is 1310720 bytes, over 1048576; publish the change in smaller revisions");
    const changes = request.share_identities ?? {};
    const archive = request.archive_shares ?? [];
    for (const [shareId, identity] of Object.entries(changes)) {
      const entry = shares.find((candidate) => candidate.share_id === shareId);
      if (entry?.identity_id && identity) throw new Error(`identity_change_refused: share ${shareId} names ${entry.identity_id}; a plan moves an identity only to or from none`);
    }
    const action = request.action === "delete" ? "delete" : "promote";
    let mode = action === "delete" ? "delete" : request.to_revision === request.from_revision || request.to_revision === undefined ? "assign" : "promote";
    if (mode === "assign" && Object.values(changes).every((value) => value === "")) mode = Object.keys(changes).length || archive.length ? "clear" : "assign";
    const promoted = mode === "promote" ? [primary, ...(request.records ?? []).map((entry) => entry.subscription_id)] : [primary];
    if (mode === "promote") {
      for (const entry of [{ subscription_id: primary, to_revision: request.to_revision ?? "" }, ...(request.records ?? [])]) {
        if (staged.get(entry.subscription_id)?.revision !== entry.to_revision) {
          throw new Error(`revision_unknown: ${entry.to_revision} is not the staged revision of record ${entry.subscription_id}; propose the revision staged now`);
        }
      }
    }
    if (mode === "delete") {
      const explicit = records.filter((candidate) => candidate.kind === "collection" && (candidate.members ?? []).includes(primary)).map((candidate) => candidate.id);
      if (explicit.length) {
        throw new Error(
          `fleet_delete_has_explicit_dependents: collections ${explicit.join(", ")} list record ${primary} as a member, and a member that does not resolve fails the collection's fetch; remove it from them first, then propose the delete`,
        );
      }
    }
    // Core renders what each record of the plan would serve; a fleet render whose script asks for the network is refused (S2-SR-2).
    for (const id of mode === "promote" ? promoted : mode === "delete" ? [] : [primary]) {
      const asking = fleetReach(id) || staged.get(id)?.source === "fleet" ? networkAskingStep(stepsAt(id, mode === "promote" ? "staged" : "live")) : null;
      if (asking) {
        const typed = {
          code: SCRIPT_NETWORK_CODE,
          record: id,
          step: asking,
          message: `the plan's render of record ${id} was refused: step ${asking.position} (${asking.type}) asked for the network, and scripts on fleet renders run without one until isolation lands`,
        };
        throw new Error(`${SCRIPT_NETWORK_CODE}: ${JSON.stringify(typed)}`);
      }
    }
    const closure = mode === "clear" ? [primary] : [...new Set((mode === "delete" ? [primary] : promoted).flatMap((id) => closureOf(id)))];
    const required = Object.keys(changes).length > 0 || archive.length > 0 || liveShares(closure).some((entry) => !!entry.identity_id);
    if (!required) return { required: false, reason: "no live share in this record's fleet closure is fleet-bound; publish it" };
    const plan = buildPlan(primary, promoted, changes, archive, mode === "clear" ? "assign" : mode, action);
    const text = JSON.stringify(plan, null, 2);
    const row = addPlan(
      { status: "pending", action, mode, subscription_id: primary, from_revision: plan.from_revision, to_revision: plan.to_revision, closure },
      text,
    );
    return { required: true, approval_id: row.approval_id, plan_sha256: row.plan_sha256, plan: text, status: "pending" };
  };

  handlers["plans/status"] = ({ approval_id, subscription_id }: { approval_id?: string; subscription_id?: string }) => {
    const strip = ({ closure: _closure, ...row }: PlanRow) => row;
    if (approval_id) {
      const row = plans.find((plan) => plan.approval_id === approval_id);
      if (!row) throw new Error("not_found: plan not found");
      // ?plans=applied and ?plans=rejected: the approver has decided a plan proposed in this sitting.
      if (row.status === "pending" && texts.has(row.approval_id) && (state === "applied" || state === "rejected")) decide(row);
      return { plans: [strip(row)] };
    }
    return { plans: plans.filter((plan) => plan.subscription_id === subscription_id || plan.closure.includes(subscription_id ?? "")).map(strip) };
  };

  /** The approver's decision, as core records it: applied writes what the plan says; rejected changes nothing. */
  function decide(row: PlanRow): void {
    row.updated_at = now();
    if (state === "rejected") {
      row.status = "rejected";
      row.reason = REJECTED;
      return;
    }
    row.status = "applied";
    const plan = JSON.parse(texts.get(row.approval_id) ?? "{}") as { apply_order?: string[]; assignments?: { share_id: string; identity_id: string }[]; archivals?: { share_id: string }[] };
    if (row.mode === "promote") {
      for (const id of plan.apply_order ?? [row.subscription_id]) {
        const entry = staged.get(id);
        if (!entry) continue;
        live.set(id, entry.revision);
        const record = byId(id);
        if (record && entry.source) record.source = entry.source;
        staged.delete(id);
      }
    }
    for (const entry of plan.assignments ?? []) {
      const share = shares.find((candidate) => candidate.share_id === entry.share_id);
      if (share) (share as { identity_id?: string }).identity_id = entry.identity_id;
    }
    for (const entry of plan.archivals ?? []) {
      const share = shares.find((candidate) => candidate.share_id === entry.share_id);
      if (share) (share as { archived_at?: string }).archived_at = now();
    }
  }

  handlers["plans/publish"] = ({ subscription_id, revision, expected_revision, action }: { subscription_id: string; revision: string; expected_revision: string; action?: string }) => {
    const record = byId(subscription_id);
    if (!record) throw new Error(`not_found: record ${subscription_id} is not in the store`);
    if (action === "delete") {
      const index = records.findIndex((candidate) => candidate.id === subscription_id);
      records.splice(index, 1);
      for (const entry of shares) if (entry.subscription_id === subscription_id) (entry as { archived_at?: string }).archived_at = now();
      return { published: true, subscription_id, revision, action: "delete" };
    }
    const entry = staged.get(subscription_id);
    if (!entry || entry.revision !== revision) throw new Error(`revision_unknown: ${revision} is not the staged revision of record ${subscription_id}; propose the revision staged now`);
    if (liveRevision(subscription_id) !== expected_revision && !(expected_revision === "" && !live.has(subscription_id))) {
      throw new Error("conflict: expected_revision is not the record's live revision");
    }
    const closure = closureOf(subscription_id);
    if (entry.migrated && liveShares(closure).length) {
      throw new Error(`migration_requires_plan: record ${subscription_id}'s staged revision is a migration and its fleet closure has live shares; propose it with an identity or an archival for each share`);
    }
    if (entry.source === "fleet" && !fleetReach(subscription_id)) {
      const offending = liveShares(closure).filter((share) => !share.identity_id).map((share) => share.share_id);
      if (offending.length) {
        throw new Error(`shares_without_identity: shares ${offending.join(", ")} name no identity on a record this revision makes fleet-bound, so they would answer the decoy once it is live; propose it with an identity or an archival for each`);
      }
    }
    if (liveShares(closure).some((share) => !!share.identity_id)) throw new Error("plan_required: a live share in this record's fleet closure is fleet-bound; propose a plan (plans.propose)");
    live.set(subscription_id, entry.revision);
    if (entry.source) record.source = entry.source;
    if (entry.process) record.process = entry.process;
    staged.delete(subscription_id);
    return { published: true, subscription_id, revision, action: "promote" };
  };

  // ── migrate_record and discard_staged ────────────────────────────────────

  let siblingFailed = false;
  handlers["subscription/migrate_record"] = ({ subscription_id, if_revision }: { subscription_id: string; if_revision?: string }) => {
    if (state === "sibling" && subscription_id === "home-graph" && !siblingFailed) {
      siblingFailed = true;
      throw new Error("internal: the staged store could not be written; try again");
    }
    const record = byId(subscription_id);
    if (!record) throw new Error(`subscription "${subscription_id}" has no live record to migrate`);
    if (!LEGACY.has(record.source ?? "")) throw new Error(`subscription "${subscription_id}" is not a legacy vpn-core or vpn-core-graph record`);
    if (if_revision && if_revision !== liveRevision(subscription_id)) return { saved: false, conflict: { id: subscription_id, reason: "stale", revision: liveRevision(subscription_id) } };
    const collections = closureOf(subscription_id).slice(1).filter((id) => byId(id)?.kind === "collection");
    if (subscription_id === "late-legacy") {
      return refused("fleet_file_unavailable", `subscription "late-legacy" feeds the files late-config; fleet-bound files arrive in S3, so it cannot migrate yet`, {
        ids: ["late-legacy"],
        files: ["late-config"],
        collections,
      });
    }
    const files = records.filter((candidate) => candidate.kind === "file" && closureOf(subscription_id).includes(candidate.node_source ?? "")).map((candidate) => candidate.id);
    if (files.length) {
      return refused("fleet_file_unavailable", `subscription "${subscription_id}" feeds the files ${files.join(", ")}; fleet-bound files arrive in S3, so it cannot migrate yet`, {
        ids: [subscription_id],
        files,
        collections,
      });
    }
    const coMigrate = [
      ...new Set(
        collections.flatMap((id) => {
          const collection = byId(id)!;
          return records.filter((member) => member.id !== subscription_id && gathers(collection, member) && LEGACY.has(member.source ?? "")).map((member) => member.id);
        }),
      ),
    ].sort();
    const roots = record.entry_roots ?? [];
    const graph = record.source === "vpn-core-graph";
    const pin = { type: "Structured Filter", args: { mode: "keep", match: "all", predicates: [{ field: "line_uuid", op: "in", values: roots }] } };
    const process = graph && roots.length ? [pin, ...(record.process ?? [])] : [...(record.process ?? [])];
    const revision = hex(`staged:${subscription_id}:${Date.now()}`);
    staged.set(subscription_id, { revision, at: now(), source: "fleet", migrated: true, process, record: { ...record, source: "fleet", process } });
    const warnings: string[] = [];
    if (graph) {
      const rows = catalogueRows().filter((row) => roots.includes(row.line_uuid));
      const missing = roots.filter((root) => !rows.some((row) => row.line_uuid === root));
      for (const root of missing) warnings.push(`root ${root} is not in the catalogue`);
      (record.process ?? []).forEach((step, index) => {
        const typed = step as { type?: string; args?: { regex?: string[] } };
        if (typed.type !== "Regex Filter") return;
        const patterns = (typed.args?.regex ?? []).map((value) => new RegExp(value));
        const matched = rows.filter((row) => patterns.some((pattern) => pattern.test(row.label ?? ""))).length;
        if (matched === 0) warnings.push(`step ${index + 2} (Regex Filter) matches 0 of ${rows.length} lines under the fleet labels`);
      });
    }
    return {
      subscription_id,
      live_revision: liveRevision(subscription_id),
      staged_revision: revision,
      selection: graph ? { catalogue_version: CATALOGUE_VERSION, count: roots.length, line_uuids: roots } : { count: 0, every_bound_line: true },
      legacy: { source: record.source, vpn_identity: record.vpn_identity ?? "", entry_roots: roots, migrated_at: now() },
      dependents: { collections, co_migrate: coMigrate },
      warnings,
    };
  };

  handlers["subscription/discard_staged"] = ({ subscription_id, staged_revision }: { subscription_id: string; staged_revision: string }) => {
    const entry = staged.get(subscription_id);
    if (!entry || !staged_revision || entry.revision !== staged_revision) {
      return refused("staged_revision_mismatch", `subscription "${subscription_id}" has no staged revision ${staged_revision || "(none named)"}; list again`, {
        ids: [subscription_id],
        ...(entry ? { staged_revision: entry.revision } : {}),
      });
    }
    staged.delete(subscription_id);
    for (const plan of plans) {
      if (plan.subscription_id === subscription_id && plan.to_revision === staged_revision && plan.status === "pending") {
        plan.status = "rejected";
        plan.reason = STALE_STAGED;
        plan.stale = true;
      }
    }
    return { subscription_id, discarded: staged_revision };
  };

  // ── a fleet-bound record's delete, a legacy record's output ─────────────

  const remove = handlers["subscription/delete"]!;
  handlers["subscription/delete"] = (payload: { subscription_id: string }) => {
    if (!s2Manifest()) return remove(payload);
    if (fleetReach(payload.subscription_id)) {
      return refused(
        "fleet_delete_requires_plan",
        `subscription "${payload.subscription_id}" is fleet-bound: deleting it changes what its identity holders receive; propose it with plans.propose {action: "delete"}`,
        { ids: [payload.subscription_id] },
      );
    }
    const record = byId(payload.subscription_id);
    const gatherers = record ? gatherersOf(record) : [];
    if (gatherers.length) {
      return refused(
        "fleet_delete_requires_plan",
        `subscription "${payload.subscription_id}" is gathered by the fleet-bound collections ${gatherers.join(", ")}; delete it through plans.propose with action delete`,
        { ids: [payload.subscription_id], collections: gatherers },
      );
    }
    return remove(payload);
  };

  // ── a provider member's save stages; get answers the staged revision ─────

  const save = handlers["subscription/save"]!;
  handlers["subscription/save"] = (payload: { subscription: FleetRecord; if_revision?: string }) => {
    const next = payload.subscription;
    const current = byId(next.id);
    if (!s2Manifest() || !plansFixture() || !current || next.kind === "collection" || next.kind === "file" || next.source === "fleet" || fleetReach(next.id)) {
      return save(payload);
    }
    // Gathered at its live facts or at the ones this save writes (a tag that joins a member tag).
    const gatherers = [...new Set([...gatherersOf(current), ...gatherersOf({ ...current, ...next })])];
    if (!gatherers.length) return save(payload);
    const before = staged.get(next.id);
    const revision = hex(`staged:${next.id}:${Date.now()}`);
    staged.set(next.id, { revision, at: now(), source: next.source ?? current.source ?? "", migrated: false, process: next.process ?? [], record: { ...current, ...next } });
    for (const plan of plans) {
      if (before && plan.subscription_id === next.id && plan.to_revision === before.revision && plan.status === "pending") {
        plan.status = "rejected";
        plan.reason = STALE_STAGED;
        plan.stale = true;
      }
    }
    return {
      saved: true,
      staged: true,
      staged_revision: revision,
      base_revision: liveRevision(next.id),
      subscription: { ...current, ...next, revision },
      ...(before ? { discarded_staged: before.revision } : {}),
    };
  };

  const get = handlers["subscription/get"]!;
  handlers["subscription/get"] = (payload: { subscription_id: string }) => {
    const reply = get(payload) as { subscription: FleetRecord };
    const entry = s2Manifest() ? staged.get(payload.subscription_id) : undefined;
    if (!entry?.record) return reply;
    return {
      ...reply,
      live_revision: liveRevision(payload.subscription_id),
      staged: { revision: entry.revision, base_revision: liveRevision(payload.subscription_id), staged_at: entry.at, subscription: { ...entry.record, revision: entry.revision } },
    };
  };

  const withheld = (id: string) =>
    new Error(`owner_credentials_withheld: subscription "${id}" serves line owner credentials, which only core's refresh reads; migrate it to fleet`);

  const render = handlers["subscription/render"]!;
  handlers["subscription/render"] = (payload: { subscription_id: string }) => {
    if (s2Manifest() && legacyReach(payload.subscription_id)) throw withheld(payload.subscription_id);
    return render(payload);
  };
  const probe = handlers["subscription/probe"]!;
  handlers["subscription/probe"] = (payload: { subscription_id: string }) => {
    if (s2Manifest() && legacyReach(payload.subscription_id)) {
      return { subscription_id: payload.subscription_id, bytes: 0, stale: true, ok: false, error_code: "owner_credentials_withheld" };
    }
    return probe(payload);
  };
  /** The plugin's refusal of a fleet render whose script asked for the network (system-go/fleet_script_network.go). */
  function previewNetwork(payload: { subscription_id?: string; operators?: unknown[]; source?: string }): Error | null {
    const record = payload.subscription_id ? byId(payload.subscription_id) : undefined;
    const fleet = payload.source === "fleet" || (!!record && (record.source === "fleet" || fleetReach(record.id)));
    if (!fleet) return null;
    const chain = Array.isArray(payload.operators) ? payload.operators : record?.process ?? [];
    if (!networkAskingStep(chain)) return null;
    const label = record ? `${record.kind === "collection" ? "collection" : "subscription"} "${record.id}"` : "the fleet draft";
    return new Error(
      `${SCRIPT_NETWORK_CODE}: ${label} is fleet-bound, and a Script or Resolve Domain step asked for the network 1 time(s); a fleet-bound chain runs without a network until S3, so remove the network call or the step`,
    );
  }
  const previewHandler = handlers["subscription/preview"]!;
  handlers["subscription/preview"] = (payload: { subscription_id?: string; operators?: unknown[]; source?: string }) => {
    if (s2Manifest() && payload.subscription_id && legacyReach(payload.subscription_id)) throw withheld(payload.subscription_id);
    const refusal = s2Manifest() ? previewNetwork(payload) : null;
    if (refusal) throw refusal;
    return previewHandler(payload);
  };
  const previewDraftHandler = handlers["subscription/preview_draft"]!;
  handlers["subscription/preview_draft"] = (payload: { subscription_id?: string; operators?: unknown[]; source?: string }) => {
    const refusal = s2Manifest() ? previewNetwork(payload) : null;
    if (refusal) throw refusal;
    return previewDraftHandler(payload);
  };

  // ── bind.preview: a staged revision's chain, a document rewrite refused ───

  const bindPreview = handlers["bind/preview"]!;
  handlers["bind/preview"] = (payload: { subscription_id: string; identity_id: string; revision?: string }) => {
    // Binding builds the catalogue in core: with the catalogue unreadable, so is every preview.
    if (new URLSearchParams(window.location.search).get("fleet") === "error") throw new Error("catalogue_unavailable: the line catalogue could not be read");
    const record = byId(payload.subscription_id);
    const chain = (payload.revision && staged.get(payload.subscription_id)?.revision === payload.revision ? staged.get(payload.subscription_id)?.process : undefined) ?? record?.process ?? [];
    const rewrite = chain.findIndex((step) => (step as { type?: string; disabled?: boolean }).type === "Response Transformer" && !(step as { disabled?: boolean }).disabled);
    if (rewrite !== -1) {
      throw new Error(`fleet_response_chain_unavailable: subscription "${payload.subscription_id}": process step ${rewrite + 1} (Response Transformer) does not run on fleet records yet`);
    }
    const asking = networkAskingStep(chain);
    if (asking) {
      throw new Error(
        `${SCRIPT_NETWORK_CODE}: record ${payload.subscription_id}: step ${asking.position} (${asking.type}) asked for the network; scripts on fleet renders run without one until isolation lands`,
      );
    }
    if (record && chain !== record.process) {
      const before = record.process;
      record.process = chain;
      try {
        return bindPreview(payload);
      } finally {
        record.process = before;
      }
    }
    return bindPreview(payload);
  };

  // ── bind_selection (operator decision 8) ─────────────────────────────────

  handlers["bind/bind_selection"] = ({ identity_id, line_uuids }: { identity_id: string; line_uuids: string[] }) => {
    if (state !== "bind") throw new Error("sub-store/bind: unknown method bind_selection");
    const filed = (line_uuids ?? []).map((line_uuid, index) => ({ line_uuid, approval_id: `apr_bind_${index}`, op: index % 3 === 0 ? "update" : "add" }));
    return { batch: `bind-${identity_id}-${filed.length}`, filed, skipped: [], errors: [] };
  };
}
