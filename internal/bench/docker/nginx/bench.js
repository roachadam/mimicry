// njs handlers for the benchmark's nginx: POST /echo returns the request
// body; /_stats the container's CPU time (cgroup v2) and CPU count, as the
// Go server's /_stats does (internal/bench/stats), and each nginx process's
// CPU time by PID: nginx serves a connection from one worker on one core,
// so a worker near a core's worth is the limit whatever the container's
// share (target.BusiestWorker).
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

// workers returns each nginx process's CPU time in microseconds, by PID:
// /proc/<pid>/schedstat's time on CPU (ns), else /proc/<pid>/stat's utime
// and stime (clock ticks, 100 a second).
function workers() {
    var out = {};
    var pids = [];
    try {
        pids = fs.readdirSync('/proc');
    } catch (e) {
        return out;
    }
    for (var i = 0; i < pids.length; i++) {
        var pid = pids[i];
        if (!/^[0-9]+$/.test(pid)) {
            continue;
        }
        try {
            if (fs.readFileSync('/proc/' + pid + '/comm', 'utf8').trim() !== 'nginx') {
                continue;
            }
            var us = -1;
            try {
                us = Math.floor(Number(fs.readFileSync('/proc/' + pid + '/schedstat', 'utf8').split(' ')[0]) / 1000);
            } catch (e) {
                var stat = fs.readFileSync('/proc/' + pid + '/stat', 'utf8');
                var f = stat.slice(stat.lastIndexOf(')') + 2).split(' ');
                us = (Number(f[11]) + Number(f[12])) * 10000;
            }
            out[pid] = us;
        } catch (e) {
            // Gone meanwhile.
        }
    }
    return out;
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
        workers: workers(),
    }));
}

export default {echo, stats};
