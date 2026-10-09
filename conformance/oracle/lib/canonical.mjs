// Canonical structural forms. A produced document is parsed back into a
// structure with sorted object keys, so two producers that emit the same
// configuration in a different key order compare equal. URI and V2Ray are
// additionally compared byte for byte by the checker; their structural form
// exists for readable diffs and for the allowlist normalisations.
//
// Forms:
//   json   JSON.parse, keys sorted.
//   yaml   YAML 1.2 core schema parse, keys sorted.
//   uri    one entry per non-empty line: scheme, userinfo, host, port, path,
//          query pairs sorted by key then value, fragment; percent-decoded.
//          vmess:// bodies that are Base64 JSON and ssr:// bodies are decoded.
//   v2ray  Base64-decoded, then as uri.
//   lines  Surge, Surge for Mac, Surfboard and Loon "name = type, a, b, k=v"
//          lines: name, type, positional args and a sorted params map.
//          Section headers, module headers and comments are kept as entries.
//   qx     Quantumult X "type=host:port, k=v, ..., tag=name" lines: type,
//          address, positional args and a sorted params map.
// Anything that fails to parse becomes { unparsed: <text> }.
import YAML from 'yaml';

export function sortKeys(value) {
    if (Array.isArray(value)) return value.map(sortKeys);
    if (value && typeof value === 'object') {
        const out = {};
        for (const key of Object.keys(value).sort()) out[key] = sortKeys(value[key]);
        return out;
    }
    return value;
}

// stableJSON is the on-disk form of every golden JSON file.
export function stableJSON(value) {
    return `${JSON.stringify(sortKeys(value), null, 2)}\n`;
}

export function canonical(form, text) {
    if (typeof text !== 'string') return { unparsed: String(text) };
    try {
        switch (form) {
            case 'json':
                return sortKeys(JSON.parse(text));
            case 'yaml':
                return sortKeys(YAML.parse(text) ?? null);
            case 'uri':
                return uriLines(text);
            case 'v2ray':
                return uriLines(decodeBase64(text.trim()));
            case 'lines':
                return kvLines(text);
            case 'qx':
                return qxLines(text);
            default:
                throw new Error(`unknown form ${form}`);
        }
    } catch {
        return { unparsed: text };
    }
}

function decodeBase64(s) {
    const clean = s.replace(/\s+/g, '').replace(/-/g, '+').replace(/_/g, '/');
    if (!/^[A-Za-z0-9+/]*={0,2}$/.test(clean)) throw new Error('not base64');
    return Buffer.from(clean, 'base64').toString('utf8');
}

// tryBase64Text returns decoded text only when s is Base64 that decodes to
// printable UTF-8, so plain userinfo is left alone.
function tryBase64Text(s) {
    if (!s || !/^[A-Za-z0-9+/_-]+={0,2}$/.test(s)) return null;
    let text;
    try {
        text = decodeBase64(s);
    } catch {
        return null;
    }
    if (!text || /[\u0000-\u0008\u000e-\u001f�]/.test(text)) return null;
    return text;
}

function safeDecode(s) {
    if (s == null) return null;
    try {
        return decodeURIComponent(s);
    } catch {
        return s;
    }
}

function parseQuery(qs) {
    if (!qs) return [];
    return qs
        .split('&')
        .filter((p) => p !== '')
        .map((p) => {
            const i = p.indexOf('=');
            return i < 0 ? [safeDecode(p), null] : [safeDecode(p.slice(0, i)), safeDecode(p.slice(i + 1))];
        })
        .sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : String(a[1]) < String(b[1]) ? -1 : String(a[1]) > String(b[1]) ? 1 : 0));
}

function uriLines(text) {
    return text
        .split('\n')
        .map((l) => l.replace(/\r$/, ''))
        .filter((l) => l.trim() !== '')
        .map(canonicalURI);
}

export function canonicalURI(line) {
    const m = /^([A-Za-z][A-Za-z0-9+.-]*):\/\/(.*)$/s.exec(line);
    if (!m) return { raw: line };
    const scheme = m[1].toLowerCase();
    let rest = m[2];
    let fragment = null;
    const hash = rest.indexOf('#');
    if (hash >= 0) {
        fragment = safeDecode(rest.slice(hash + 1));
        rest = rest.slice(0, hash);
    }
    let query = [];
    const qm = rest.indexOf('?');
    let beforeQuery = rest;
    if (qm >= 0) {
        query = parseQuery(rest.slice(qm + 1));
        beforeQuery = rest.slice(0, qm);
    }
    if (scheme === 'vmess') {
        const decoded = tryBase64Text(beforeQuery.replace(/\/$/, ''));
        if (decoded) {
            try {
                return { scheme, body: sortKeys(JSON.parse(decoded)), query, fragment };
            } catch {
                return { scheme, body: decoded, query, fragment };
            }
        }
    }
    if (scheme === 'ssr') {
        const decoded = tryBase64Text(beforeQuery);
        if (decoded) return { scheme, body: canonicalSSR(decoded), query, fragment };
    }
    const at = beforeQuery.lastIndexOf('@');
    const userinfo = at >= 0 ? beforeQuery.slice(0, at) : null;
    const hostPart = at >= 0 ? beforeQuery.slice(at + 1) : beforeQuery;
    const slash = hostPart.indexOf('/');
    const hostPort = slash >= 0 ? hostPart.slice(0, slash) : hostPart;
    const path = slash >= 0 ? hostPart.slice(slash) : '';
    let host = hostPort;
    let port = null;
    if (hostPort.startsWith('[')) {
        const close = hostPort.indexOf(']');
        host = hostPort.slice(1, close);
        if (hostPort[close + 1] === ':') port = hostPort.slice(close + 2);
    } else {
        const colon = hostPort.lastIndexOf(':');
        if (colon >= 0) {
            host = hostPort.slice(0, colon);
            port = hostPort.slice(colon + 1);
        }
    }
    const out = { scheme, userinfo: safeDecode(userinfo), host: host.toLowerCase(), port, path: safeDecode(path), query, fragment };
    const decodedUser = userinfo && !userinfo.includes(':') ? tryBase64Text(safeDecode(userinfo)) : null;
    if (decodedUser && decodedUser.includes(':')) out.userinfo_base64 = decodedUser;
    return out;
}

function canonicalSSR(decoded) {
    const q = decoded.indexOf('/?');
    const main = q >= 0 ? decoded.slice(0, q) : decoded;
    const params = q >= 0 ? parseQuery(decoded.slice(q + 2)) : [];
    return {
        main,
        params: params.map(([k, v]) => [k, v != null ? tryBase64Text(v) ?? v : v]),
    };
}

// splitTopLevel splits on sep outside quotes, brackets, braces and parens.
export function splitTopLevel(s, sep) {
    const out = [];
    let depth = 0;
    let quote = null;
    let cur = '';
    for (const ch of s) {
        if (quote) {
            cur += ch;
            if (ch === quote) quote = null;
            continue;
        }
        if (ch === '"') {
            quote = ch;
            cur += ch;
            continue;
        }
        if (ch === '[' || ch === '{' || ch === '(') depth++;
        if (ch === ']' || ch === '}' || ch === ')') depth = Math.max(0, depth - 1);
        if (ch === sep && depth === 0) {
            out.push(cur);
            cur = '';
            continue;
        }
        cur += ch;
    }
    out.push(cur);
    return out;
}

function unquote(s) {
    const t = s.trim();
    if (t.length >= 2 && t[0] === '"' && t[t.length - 1] === '"') return t.slice(1, -1);
    return t;
}

function addParam(params, key, value) {
    if (Object.prototype.hasOwnProperty.call(params, key)) {
        params[key] = [].concat(params[key], value);
    } else {
        params[key] = value;
    }
}

function kvLines(text) {
    const out = [];
    let section = null;
    for (const line of text.split('\n')) {
        const t = line.replace(/\r$/, '').trim();
        if (t === '') continue;
        if (t.startsWith('#!')) {
            out.push({ header: t });
            continue;
        }
        if (/^\[.*\]$/.test(t)) {
            section = t;
            out.push({ section: t });
            continue;
        }
        if (t.startsWith('#') || t.startsWith('//') || t.startsWith(';')) {
            out.push({ comment: t });
            continue;
        }
        const eq = t.indexOf('=');
        if (eq < 0) {
            out.push({ raw: t });
            continue;
        }
        const name = t.slice(0, eq).trim();
        const body = t.slice(eq + 1);
        if (section && !/^\[proxy\]$/i.test(section)) {
            out.push({ section, key: name, value: body.trim() });
            continue;
        }
        const tokens = splitTopLevel(body, ',').map((x) => x.trim());
        const args = [];
        const params = {};
        for (const tok of tokens.slice(1)) {
            const m = /^([A-Za-z0-9_-]+)\s*=\s*(.*)$/s.exec(tok);
            if (m) addParam(params, m[1], unquote(m[2]));
            else args.push(unquote(tok));
        }
        out.push(sortKeys({ name: unquote(name), type: tokens[0], args, params }));
    }
    return out;
}

function qxLines(text) {
    const out = [];
    for (const line of text.split('\n')) {
        const t = line.replace(/\r$/, '').trim();
        if (t === '') continue;
        if (/^\[.*\]$/.test(t)) {
            out.push({ section: t });
            continue;
        }
        if (t.startsWith('#') || t.startsWith(';')) {
            out.push({ comment: t });
            continue;
        }
        const tokens = splitTopLevel(t, ',').map((x) => x.trim());
        const m = /^([A-Za-z0-9-]+)\s*=\s*(.*)$/s.exec(tokens[0]);
        if (!m) {
            out.push({ raw: t });
            continue;
        }
        const args = [];
        const params = {};
        for (const tok of tokens.slice(1)) {
            const i = tok.indexOf('=');
            if (i > 0) addParam(params, tok.slice(0, i).trim(), tok.slice(i + 1).trim());
            else args.push(tok);
        }
        out.push(sortKeys({ type: m[1], address: m[2], args, params }));
    }
    return out;
}
