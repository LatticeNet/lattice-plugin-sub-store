// A client for any implementation that speaks the oracle protocol
// (run.mjs): spawn the command, send one JSON request per line, and resolve
// each response by id.
import { spawn } from 'node:child_process';

// protocolLine is one protocol message: compact JSON and a "\n". JSON may
// carry U+2028 and U+2029 raw, and Node's readline splits a line at them
// from Node 24 on (Node 22 does not), so they are written escaped; JSON.parse
// reads the escape back as the same character. A corpus node may hold either
// in its name (clash-socks5-name-line-separators).
export function protocolLine(value) {
    return `${JSON.stringify(value).replace(/[\u2028\u2029]/g, (c) => (c === '\u2028' ? '\\u2028' : '\\u2029'))}\n`;
}

// readLines calls fn with each line of a text stream, split at "\n" only (a
// "\r" before it is dropped). A protocol message is one "\n"-terminated line,
// so a peer that writes U+2028 or U+2029 raw inside a JSON string, as
// JSON.stringify and a Go encoder's json.RawMessage do, still reads as one
// message, which readline from Node 24 on would split.
export function readLines(stream, fn) {
    let buf = '';
    let from = 0;
    stream.setEncoding('utf8');
    stream.on('data', (chunk) => {
        buf += chunk;
        let i;
        while ((i = buf.indexOf('\n', from)) >= 0) {
            const line = buf.slice(0, i);
            buf = buf.slice(i + 1);
            from = 0;
            fn(line.endsWith('\r') ? line.slice(0, -1) : line);
        }
        from = buf.length;
    });
    stream.on('end', () => {
        if (buf !== '') fn(buf.endsWith('\r') ? buf.slice(0, -1) : buf);
        buf = '';
    });
}

export class Runner {
    constructor(command, { cwd, env } = {}) {
        this.command = command;
        this.nextId = 1;
        this.pending = new Map();
        this.stderr = '';
        this.child = spawn(command, { shell: true, cwd, env: env ?? process.env, stdio: ['pipe', 'pipe', 'pipe'] });
        this.child.stderr.on('data', (d) => {
            this.stderr += d.toString();
            if (this.stderr.length > 20000) this.stderr = this.stderr.slice(-20000);
        });
        readLines(this.child.stdout, (line) => {
            let res;
            try {
                res = JSON.parse(line);
            } catch {
                return this.failAll(new Error(`runner wrote a line that is not JSON: ${line.slice(0, 200)}`));
            }
            const p = this.pending.get(res.id);
            if (!p) return this.failAll(new Error(`runner answered unknown id ${res.id}`));
            this.pending.delete(res.id);
            p.resolve(res);
        });
        this.exited = new Promise((resolve) => {
            this.child.on('exit', (code, signal) => {
                this.failAll(new Error(`runner exited (code ${code}, signal ${signal})\n${this.stderr}`));
                resolve(code);
            });
        });
    }

    failAll(err) {
        for (const p of this.pending.values()) p.reject(err);
        this.pending.clear();
    }

    request(body) {
        const id = this.nextId++;
        return new Promise((resolve, reject) => {
            this.pending.set(id, { resolve, reject });
            this.child.stdin.write(protocolLine({ ...body, id }));
        });
    }

    async close() {
        this.child.stdin.end();
        return this.exited;
    }
}
