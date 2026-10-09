// A client for any implementation that speaks the oracle protocol
// (run.mjs): spawn the command, send one JSON request per line, and resolve
// each response by id.
import { spawn } from 'node:child_process';
import readline from 'node:readline';

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
        readline.createInterface({ input: this.child.stdout, crlfDelay: Infinity }).on('line', (line) => {
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
            this.child.stdin.write(`${JSON.stringify({ ...body, id })}\n`);
        });
    }

    async close() {
        this.child.stdin.end();
        return this.exited;
    }
}
