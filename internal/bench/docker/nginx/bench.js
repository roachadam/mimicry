// njs handlers for the benchmark's nginx: POST /echo returns the request
// body; /_stats the container's CPU time (cgroup v2) and CPU count, as the
// Go server's /_stats does (internal/bench/stats).
import fs from 'fs';

function echo(r) {
    r.headersOut['Content-Type'] = r.headersIn['Content-Type'] || 'application/octet-stream';
    r.return(200, r.requestText || '');
}

function countCPUs(list) {
    var n = 0;
    var parts = list.trim().split(',');
    for (var i = 0; i < parts.length; i++) {
        if (parts[i] === '') {
            continue;
        }
        var bounds = parts[i].split('-');
        n += bounds.length === 1 ? 1 : Number(bounds[1]) - Number(bounds[0]) + 1;
    }
    return n;
}

function stats(r) {
    let usage = -1;
    try {
        const m = fs.readFileSync('/sys/fs/cgroup/cpu.stat', 'utf8').match(/usage_usec (\d+)/);
        if (m) {
            usage = Number(m[1]);
        }
    } catch (e) {
        // No cgroup v2: unknown.
    }

    let cpus = 0;
    try {
        cpus = countCPUs(fs.readFileSync('/sys/fs/cgroup/cpuset.cpus.effective', 'utf8'));
    } catch (e) {
        // Unknown.
    }

    r.headersOut['Content-Type'] = 'application/json';
    r.return(200, JSON.stringify({
        cpuMicros: usage,
        cpus: cpus,
        tcpConns: 0,
        quicConns: 0,
    }));
}

export default {echo, stats};
