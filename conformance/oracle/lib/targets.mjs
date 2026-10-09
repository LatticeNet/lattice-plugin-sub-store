// The 14 client targets. `id` is the harness name used for golden
// directories, conformance.json keys and specs/producers/<id>.md; `platform`
// is the name upstream's produce() takes. `form` selects the canonical
// structural form (lib/canonical.mjs); `bytes` marks targets whose goldens
// must also match byte for byte. Tier follows design 28: the first nine go
// native first.
export const TARGETS = [
    { id: 'uri', platform: 'URI', ext: 'txt', form: 'uri', bytes: true, tier: 1 },
    { id: 'v2ray', platform: 'V2Ray', ext: 'txt', form: 'v2ray', bytes: true, tier: 1 },
    { id: 'json', platform: 'JSON', ext: 'json', form: 'json', bytes: false, tier: 1 },
    { id: 'singbox', platform: 'sing-box', ext: 'json', form: 'json', bytes: false, tier: 1 },
    { id: 'clashmeta', platform: 'ClashMeta', ext: 'yaml', form: 'yaml', bytes: false, tier: 1 },
    { id: 'stash', platform: 'Stash', ext: 'yaml', form: 'yaml', bytes: false, tier: 1 },
    { id: 'shadowrocket', platform: 'Shadowrocket', ext: 'yaml', form: 'yaml', bytes: false, tier: 1 },
    { id: 'surge', platform: 'Surge', ext: 'txt', form: 'lines', bytes: false, tier: 1 },
    { id: 'quantumultx', platform: 'QX', ext: 'txt', form: 'qx', bytes: false, tier: 1 },
    { id: 'loon', platform: 'Loon', ext: 'txt', form: 'lines', bytes: false, tier: 2 },
    { id: 'surfboard', platform: 'Surfboard', ext: 'txt', form: 'lines', bytes: false, tier: 2 },
    { id: 'egern', platform: 'Egern', ext: 'yaml', form: 'yaml', bytes: false, tier: 2 },
    { id: 'surgemac', platform: 'SurgeMac', ext: 'txt', form: 'lines', bytes: false, tier: 2 },
    { id: 'clash', platform: 'Clash', ext: 'yaml', form: 'yaml', bytes: false, tier: 2 },
];

const byKey = new Map();
for (const t of TARGETS) {
    byKey.set(t.id.toLowerCase(), t);
    byKey.set(t.platform.toLowerCase(), t);
}

// findTarget accepts a harness id or an upstream platform name, in any case.
export function findTarget(name) {
    return byKey.get(String(name).toLowerCase()) || null;
}
