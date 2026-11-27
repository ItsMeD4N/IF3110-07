import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend, Rate, Counter } from 'k6/metrics';
import exec from 'k6/execution';

const BASE_URL = (__ENV.BASE_URL || 'http://localhost:8080').replace(/\/$/, '');
const DB_IDS_ENV = (__ENV.DB_IDS || '').split(',').map((s) => parseInt(s.trim(), 10)).filter((n) => n > 0);
const EDITOR = __ENV.EDITOR_USER || 'bob';
const LEADERS = (__ENV.LEADERS || 'alice,bob').split(',').map((s) => s.trim()).filter(Boolean);
const PASSWORD = __ENV.SEED_PASSWORD || 'Password#123';
const NORMAL_VUS = parseInt(__ENV.NORMAL_VUS || '10', 10);
const NORMAL_JOB_VUS = parseInt(__ENV.NORMAL_JOB_VUS || '2', 10);
const JOB_TIMEOUT_S = parseInt(__ENV.JOB_TIMEOUT_S || '180', 10);
const HOLD = __ENV.HOLD || '1m';
const RAMP = __ENV.RAMP || '30s';

const LEVELS = [
  { name: 'warmup', factor: 1, ramp: RAMP, hold: '0s' },
  { name: 'normal', factor: 1, ramp: '0s', hold: HOLD },
  { name: 'stress_x2', factor: 2, ramp: RAMP, hold: HOLD },
  { name: 'stress_x5', factor: 5, ramp: RAMP, hold: HOLD },
  { name: 'stress_x10', factor: 10, ramp: RAMP, hold: HOLD },
  { name: 'recovery', factor: 1, ramp: RAMP, hold: HOLD },
  { name: 'cooldown', factor: 0, ramp: RAMP, hold: '0s' },
];

function seconds(d) {
  const m = /^(\d+)(ms|s|m)$/.exec(d);
  if (!m) return 0;
  const n = parseInt(m[1], 10);
  return m[2] === 'm' ? n * 60 : m[2] === 'ms' ? n / 1000 : n;
}

function buildStages(base) {
  const stages = [];
  for (const lvl of LEVELS) {
    const target = Math.round(base * lvl.factor);
    if (seconds(lvl.ramp) > 0) stages.push({ duration: lvl.ramp, target });
    if (seconds(lvl.hold) > 0) stages.push({ duration: lvl.hold, target });
  }
  return stages;
}

const STAGE_WINDOWS = (() => {
  let t = 0;
  return LEVELS.map((lvl) => {
    const start = t;
    t += seconds(lvl.ramp) + seconds(lvl.hold);
    return { name: lvl.name, start, end: t };
  });
})();

function currentStage() {
  const elapsed = (Date.now() - exec.scenario.startTime) / 1000;
  for (const w of STAGE_WINDOWS) {
    if (elapsed >= w.start && elapsed < w.end) return w.name;
  }
  return 'cooldown';
}

const thresholds = {
  'http_req_failed{kind:api}': ['rate<0.01'],
  'http_req_duration{kind:api}': ['p(95)<800'],
  'http_req_duration{kind:api,stage:normal}': ['p(95)<300'],
  'http_req_failed{kind:submit}': ['rate<0.05'],
  migration_job_success: ['rate>0.95'],
  migration_job_completion_seconds: ['p(95)<120'],
};
for (const lvl of LEVELS) {
  thresholds[`http_req_duration{kind:api,stage:${lvl.name}}`] = thresholds[`http_req_duration{kind:api,stage:${lvl.name}}`] || ['p(95)<5000'];
  thresholds[`http_req_failed{kind:api,stage:${lvl.name}}`] = ['rate<=1'];
  thresholds[`http_reqs{kind:api,stage:${lvl.name}}`] = ['count>=0'];
  thresholds[`migration_job_completion_seconds{stage:${lvl.name}}`] = ['p(95)>=0'];
  thresholds[`migration_queue_wait_seconds{stage:${lvl.name}}`] = ['p(95)>=0'];
  thresholds[`migration_job_success{stage:${lvl.name}}`] = ['rate>=0'];
  thresholds[`migration_jobs_completed{stage:${lvl.name}}`] = ['count>=0'];
}

export const options = {
  scenarios: {
    api_read: {
      executor: 'ramping-vus',
      exec: 'apiRead',
      startVUs: 0,
      stages: buildStages(NORMAL_VUS),
      gracefulRampDown: '10s',
    },
    migration_jobs: {
      executor: 'ramping-vus',
      exec: 'migrationJob',
      startVUs: 0,
      stages: buildStages(NORMAL_JOB_VUS),
      gracefulRampDown: `${JOB_TIMEOUT_S}s`,
    },
  },
  thresholds,
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

const jobCompletion = new Trend('migration_job_completion_seconds');
const queueWait = new Trend('migration_queue_wait_seconds');
const jobRunDuration = new Trend('migration_job_run_seconds');
const jobSuccess = new Rate('migration_job_success');
const jobsCompleted = new Counter('migration_jobs_completed');
const jobsSubmitted = new Counter('migration_jobs_submitted');

const sessions = {};
const warned = {};

function warnOnce(name, res) {
  const key = `${name}:${res.status}`;
  if (warned[key]) return;
  warned[key] = true;
  console.warn(`[vu ${exec.vu.idInTest}] ${name} -> HTTP ${res.status}: ${String(res.body || '').slice(0, 200)}`);
}

function csrfToken(jar) {
  const cookies = jar.cookiesForURL(`${BASE_URL}/`);
  return (cookies.csrf_token && cookies.csrf_token[0]) || '';
}

function login(username) {
  if (sessions[username]) return sessions[username];
  const jar = new http.CookieJar();
  http.get(`${BASE_URL}/api/csrf-token`, { jar, tags: { kind: 'auth', name: 'GET /api/csrf-token' } });
  const res = http.post(
    `${BASE_URL}/api/auth/login`,
    JSON.stringify({ username, password: PASSWORD }),
    {
      jar,
      headers: { 'Content-Type': 'application/json', Accept: 'application/json', 'X-CSRF-Token': csrfToken(jar) },
      tags: { kind: 'auth', name: 'POST /api/auth/login' },
    },
  );
  if (!check(res, { [`login ${username} 200`]: (r) => r.status === 200 })) {
    return null;
  }
  sessions[username] = jar;
  return jar;
}

function params(jar, kind, name, stage) {
  return {
    jar,
    headers: { 'Content-Type': 'application/json', Accept: 'application/json', 'X-CSRF-Token': csrfToken(jar) },
    tags: { kind, name, stage },
  };
}

export function setup() {
  const health = http.get(`${BASE_URL}/api/health`, { tags: { kind: 'setup', name: 'GET /api/health' } });
  if (health.status !== 200) {
    exec.test.abort(`server tidak bisa dihubungi di ${BASE_URL} (status ${health.status}); jalankan server Go dulu (cd server; go run .)`);
  }
  const jar = login(EDITOR);
  if (!jar) {
    exec.test.abort(`login ${EDITOR} gagal; jalankan devseed dan cek SEED_PASSWORD`);
  }
  for (const u of LEADERS) {
    if (!login(u)) exec.test.abort(`login leader ${u} gagal`);
  }
  const res = http.get(`${BASE_URL}/api/databases`, { jar, headers: { Accept: 'application/json' }, tags: { kind: 'setup', name: 'GET /api/databases' } });
  if (res.status !== 200) {
    exec.test.abort(`GET /api/databases = ${res.status}`);
  }
  const editable = (res.json('databases') || [])
    .filter((d) => d.access_level === 'view_edit' || (d.permissions || []).includes('db.migrations.execute'))
    .map((d) => ({ id: d.id, name: d.name }));
  if (!editable.length) {
    exec.test.abort(`${EDITOR} tidak punya akses View + Edit ke database mana pun; jalankan devseed dengan SEED_TARGET_URL dan SEED_TARGET_URL_2`);
  }
  let dbIds = editable.map((d) => d.id);
  if (DB_IDS_ENV.length) {
    const unknown = DB_IDS_ENV.filter((id) => !dbIds.includes(id));
    if (unknown.length) {
      exec.test.abort(`DB_IDS ${unknown.join(',')} tidak bisa diakses ${EDITOR}. Database yang tersedia: ${editable.map((d) => `${d.id}=${d.name}`).join(', ')}`);
    }
    dbIds = DB_IDS_ENV;
  }
  console.log(`database target: ${editable.filter((d) => dbIds.includes(d.id)).map((d) => `${d.id}=${d.name}`).join(', ')}`);
  return { dbIds };
}

function pickDB(data) {
  const ids = data.dbIds;
  return ids[(exec.vu.idInTest + exec.vu.iterationInScenario) % ids.length];
}

export function apiRead(data) {
  const stage = currentStage();
  const jar = login(EDITOR);
  if (!jar) {
    sleep(1);
    return;
  }
  const db = pickDB(data);
  const reqs = [
    ['GET /api/databases', `${BASE_URL}/api/databases`],
    ['GET /api/databases/:id/schema', `${BASE_URL}/api/databases/${db}/schema?schema=public`],
    ['GET /api/databases/:id/migrations/executions', `${BASE_URL}/api/databases/${db}/migrations/executions?hide_rollback=true&limit=50`],
    ['GET /api/databases/:id/jobs', `${BASE_URL}/api/databases/${db}/jobs?limit=20`],
    ['GET /api/databases/:id/lock', `${BASE_URL}/api/databases/${db}/lock`],
  ];
  for (const [name, url] of reqs) {
    const res = http.get(url, params(jar, 'api', name, stage));
    if (!check(res, { [`${name} 200`]: (r) => r.status === 200 })) warnOnce(name, res);
  }
  sleep(0.5 + Math.random() * 0.5);
}

function terminal(status) {
  return ['completed', 'failed', 'partial', 'partially_reverted', 'uncertain', 'cancelled'].includes(status);
}

function fail(stage, name, res) {
  warnOnce(name, res);
  jobSuccess.add(false, { stage });
  sleep(2);
}

export function migrationJob(data) {
  const stage = currentStage();
  const editor = login(EDITOR);
  const leaders = LEADERS.map((u) => login(u));
  if (!editor || leaders.some((j) => !j)) {
    sleep(1);
    return;
  }
  const db = pickDB(data);
  const suffix = `${exec.vu.idInTest}_${exec.vu.iterationInScenario}_${Date.now()}`;
  const table = `public.k6_load_${suffix}`;

  const defRes = http.post(
    `${BASE_URL}/api/databases/${db}/migrations/definitions`,
    JSON.stringify({
      name: `k6 load ${suffix}`,
      execution_mode: 'normal',
      sql_up: `CREATE TABLE ${table} (id INTEGER PRIMARY KEY, payload TEXT NOT NULL);\nINSERT INTO ${table} SELECT g, md5(g::text) FROM generate_series(1, 5000) g;`,
      sql_down: `DROP TABLE IF EXISTS ${table};`,
    }),
    params(editor, 'workflow', 'POST definitions', stage),
  );
  if (!check(defRes, { 'definition 201': (r) => r.status === 201 })) {
    fail(stage, 'POST definitions', defRes);
    return;
  }
  const migrationID = defRes.json('id');

  const reqRes = http.post(
    `${BASE_URL}/api/databases/${db}/migrations/definitions/${migrationID}/approval-requests`,
    JSON.stringify({ action: 'up', note: 'k6 stress test' }),
    params(editor, 'workflow', 'POST approval-requests', stage),
  );
  if (!check(reqRes, { 'approval request 201': (r) => r.status === 201 })) {
    fail(stage, 'POST approval-requests', reqRes);
    return;
  }
  const requestID = reqRes.json('id');

  for (const jar of leaders) {
    const res = http.post(
      `${BASE_URL}/api/databases/${db}/approval-requests/${requestID}/approve`,
      JSON.stringify({ comment: 'k6 approve' }),
      params(jar, 'workflow', 'POST approve', stage),
    );
    if (!check(res, { 'approve 200/409': (r) => r.status === 200 || r.status === 409 })) warnOnce('POST approve', res);
  }

  const submitAt = Date.now();
  const execRes = http.post(
    `${BASE_URL}/api/databases/${db}/migrations/definitions/${migrationID}/execute`,
    JSON.stringify({ idempotency_key: `k6-${suffix}` }),
    params(editor, 'submit', 'POST execute', stage),
  );
  if (!check(execRes, { 'execute 202': (r) => r.status === 202 })) {
    fail(stage, 'POST execute', execRes);
    return;
  }
  jobsSubmitted.add(1, { stage });
  const jobID = execRes.json('job_id');

  let job = null;
  const deadline = submitAt + JOB_TIMEOUT_S * 1000;
  while (Date.now() < deadline) {
    sleep(0.5);
    const res = http.get(`${BASE_URL}/api/databases/${db}/jobs/${jobID}`, params(editor, 'poll', 'GET job', stage));
    if (res.status !== 200) continue;
    job = res.json('job');
    if (job && terminal(job.status)) break;
  }

  const ok = !!job && job.status === 'completed';
  if (job && !ok) console.warn(`job ${jobID} berakhir ${job.status}: ${job.last_error || ''}`);
  jobSuccess.add(ok, { stage });
  if (!job || !terminal(job.status)) return;

  jobCompletion.add((Date.now() - submitAt) / 1000, { stage });
  if (ok) jobsCompleted.add(1, { stage });
  if (job.started_at && job.created_at) {
    queueWait.add(Math.max(0, (Date.parse(job.started_at) - Date.parse(job.created_at)) / 1000), { stage });
  }
  if (job.started_at && job.completed_at) {
    jobRunDuration.add(Math.max(0, (Date.parse(job.completed_at) - Date.parse(job.started_at)) / 1000), { stage });
  }
}

function metric(data, name, stat) {
  const m = data.metrics[name];
  if (!m || !m.values) return null;
  return m.values[stat];
}

function fmt(v, digits = 2) {
  return v === null || v === undefined || Number.isNaN(v) ? '-' : Number(v).toFixed(digits);
}

export function handleSummary(data) {
  const durationS = data.state.testRunDurationMs / 1000;
  const lines = [];
  lines.push('| Tingkat beban | API req | API RPS | API p50 (ms) | API p95 (ms) | API error rate | Job selesai | Job/detik | Queue wait p95 (s) | Job selesai p95 (s) | Job sukses |');
  lines.push('| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |');
  for (const w of STAGE_WINDOWS) {
    const len = Math.max(1, w.end - w.start);
    const reqs = metric(data, `http_reqs{kind:api,stage:${w.name}}`, 'count');
    const jobs = metric(data, `migration_jobs_completed{stage:${w.name}}`, 'count');
    lines.push(`| ${w.name} | ${fmt(reqs, 0)} | ${fmt(reqs === null ? null : reqs / len)} | ${fmt(metric(data, `http_req_duration{kind:api,stage:${w.name}}`, 'med'), 1)} | ${fmt(metric(data, `http_req_duration{kind:api,stage:${w.name}}`, 'p(95)'), 1)} | ${fmt(metric(data, `http_req_failed{kind:api,stage:${w.name}}`, 'rate'), 4)} | ${fmt(jobs, 0)} | ${fmt(jobs === null ? null : jobs / len, 3)} | ${fmt(metric(data, `migration_queue_wait_seconds{stage:${w.name}}`, 'p(95)'))} | ${fmt(metric(data, `migration_job_completion_seconds{stage:${w.name}}`, 'p(95)'))} | ${fmt(metric(data, `migration_job_success{stage:${w.name}}`, 'rate'), 3)} |`);
  }
  lines.push('');
  lines.push(`Total durasi: ${fmt(durationS, 0)} s, total request: ${fmt(metric(data, 'http_reqs', 'count'), 0)}, throughput keseluruhan: ${fmt(metric(data, 'http_reqs', 'rate'))} req/s`);
  lines.push(`API p50/p95: ${fmt(metric(data, 'http_req_duration{kind:api}', 'med'), 1)} / ${fmt(metric(data, 'http_req_duration{kind:api}', 'p(95)'), 1)} ms, error rate API: ${fmt(metric(data, 'http_req_failed{kind:api}', 'rate'), 4)}`);
  lines.push(`Job dikirim: ${fmt(metric(data, 'migration_jobs_submitted', 'count'), 0)}, job selesai: ${fmt(metric(data, 'migration_jobs_completed', 'count'), 0)}, sukses: ${fmt(metric(data, 'migration_job_success', 'rate'), 3)}`);
  lines.push(`Queue wait p50/p95: ${fmt(metric(data, 'migration_queue_wait_seconds', 'med'))} / ${fmt(metric(data, 'migration_queue_wait_seconds', 'p(95)'))} s, durasi eksekusi job p95: ${fmt(metric(data, 'migration_job_run_seconds', 'p(95)'))} s`);
  const failed = Object.entries(data.metrics)
    .filter(([, m]) => m.thresholds && Object.values(m.thresholds).some((t) => !t.ok))
    .map(([k]) => k);
  lines.push(`Threshold gagal: ${failed.length ? failed.join(', ') : 'tidak ada'}`);
  const text = lines.join('\n') + '\n';
  const stamp = new Date().toISOString().replace(/[:.]/g, '-');
  return {
    stdout: text,
    [`loadtest/k6/summary-${stamp}.md`]: text,
  };
}
