import http from 'k6/http';
import { check, fail } from 'k6';
import { Counter, Rate } from 'k6/metrics';

// Replays FlickAI guest → Google elevation against Postgres MergeUser:
// profile-setup forever credits, daily scan/chat burn, atomic merge + seal,
// retry-storm idempotency, and sign-out/new-guest churn onto the same auth UID.
const BASE_URL = __ENV.BASE_URL || 'http://comprehensive-example:8080';
const SCAN = 'receipt_scan';
const CHAT = 'receipt_chat';

const ledgerMismatch = new Counter('ledger_mismatch');
const sealedConsumeAllowed = new Counter('sealed_consume_allowed');
const mergeReplayMiss = new Counter('merge_replay_miss');
const mergeSuccess = new Counter('merge_success');
const mergeError = new Rate('merge_error');

const VUS = parseInt(__ENV.MERGE_VUS || '20', 10);
const ITERS = parseInt(__ENV.MERGE_ITERS || '5', 10);
const RUN = __ENV.MERGE_RUN || String(Date.now());

http.setResponseCallback(http.expectedStatuses(200, 409, 402));

export const options = {
  scenarios: {
    elevation: {
      executor: 'per-vu-iterations',
      vus: VUS,
      iterations: ITERS,
      maxDuration: __ENV.MERGE_MAX_DURATION || '3m',
      exec: 'elevation',
    },
    retry_storm: {
      executor: 'per-vu-iterations',
      vus: parseInt(__ENV.MERGE_RETRY_VUS || '10', 10),
      iterations: 3,
      maxDuration: '3m',
      exec: 'retryStorm',
    },
    abuse_churn: {
      executor: 'per-vu-iterations',
      vus: parseInt(__ENV.MERGE_CHURN_VUS || '8', 10),
      iterations: 3,
      maxDuration: '3m',
      exec: 'abuseChurn',
    },
  },
  thresholds: {
    ledger_mismatch: ['count==0'],
    sealed_consume_allowed: ['count==0'],
    merge_replay_miss: ['count==0'],
    merge_error: ['rate<0.02'],
    http_req_failed: ['rate<0.05'],
  },
};

function jsonReq(method, path, body) {
  const res = http.request(method, `${BASE_URL}${path}`, body ? JSON.stringify(body) : null, {
    headers: { 'Content-Type': 'application/json' },
    tags: { name: path },
  });
  return res;
}

function ensureUser(userId) {
  const res = jsonReq('POST', '/merge/ensure-user', { userId, tier: 'explorer' });
  if (res.status !== 200) {
    fail(`ensure-user ${userId}: ${res.status} ${res.body}`);
  }
}

function consume(userId, resource, amount, period) {
  return jsonReq('POST', '/merge/consume', { userId, resource, amount, period });
}

function topup(userId, resource, amount, key) {
  const res = jsonReq('POST', '/merge/topup', {
    userId,
    resource,
    amount,
    idempotencyKey: key,
  });
  if (res.status !== 200) {
    fail(`topup ${userId} ${resource}: ${res.status} ${res.body}`);
  }
}

function mergeUsers(source, target, key, seal) {
  return jsonReq('POST', '/merge/merge', {
    sourceUserId: source,
    targetUserId: target,
    resources: [SCAN, CHAT],
    periods: ['daily', 'forever'],
    idempotencyKey: key,
    sealSource: seal,
  });
}

function snapshot(source, target) {
  const res = http.get(`${BASE_URL}/merge/snapshot?source=${source}&target=${target}`, {
    tags: { name: '/merge/snapshot' },
  });
  if (res.status !== 200) {
    fail(`snapshot: ${res.status} ${res.body}`);
  }
  return res.json();
}

function remaining(u) {
  const limit = (u && u.limit) || 0;
  const used = (u && u.used) || 0;
  if (limit < 0) return 0;
  return Math.max(0, limit - used);
}

function expect(ok, label, detail) {
  if (!ok) {
    ledgerMismatch.add(1);
    console.error(`LEDGER ${label}: ${detail}`);
  }
  check(null, { [label]: () => ok });
}

function assertElevationLedger(before, after) {
  for (const resource of [SCAN, CHAT]) {
    const bSrcD = before.source[resource].daily;
    const bDstD = before.target[resource].daily;
    const aSrcD = after.source[resource].daily;
    const aDstD = after.target[resource].daily;
    expect(
      aDstD.used === bDstD.used + bSrcD.used,
      `${resource} daily dest used`,
      `got ${aDstD.used} want ${bDstD.used}+${bSrcD.used}`,
    );
    expect(aSrcD.used === 0, `${resource} daily source used`, `got ${aSrcD.used}`);
    expect(
      aDstD.limit === bDstD.limit,
      `${resource} daily dest limit unchanged`,
      `got ${aDstD.limit} want ${bDstD.limit}`,
    );

    const bSrcF = before.source[resource].forever;
    const bDstF = before.target[resource].forever;
    const aSrcF = after.source[resource].forever;
    const aDstF = after.target[resource].forever;
    const moved = remaining(bSrcF);
    expect(
      aDstF.limit === bDstF.limit + moved,
      `${resource} forever dest limit`,
      `got ${aDstF.limit} want ${bDstF.limit}+${moved}`,
    );
    expect(
      aDstF.used === bDstF.used,
      `${resource} forever dest used unchanged`,
      `got ${aDstF.used} want ${bDstF.used}`,
    );
    expect(
      aSrcF.used === bSrcF.used,
      `${resource} forever source used unchanged`,
      `got ${aSrcF.used} want ${bSrcF.used}`,
    );
    expect(
      aSrcF.limit === bSrcF.used,
      `${resource} forever source drained`,
      `got limit=${aSrcF.limit} used=${aSrcF.used}`,
    );
  }
  expect(after.sourceSealed === true, 'source sealed', `sealed=${after.sourceSealed}`);
}

function seedGuestAuth(guest, auth) {
  ensureUser(guest);
  ensureUser(auth);

  // FlickAI profile-setup forever credits on the guest shell.
  topup(guest, CHAT, 5, `${guest}:profile:chat`);
  topup(guest, SCAN, 1, `${guest}:profile:scan`);

  // Existing Google account already burned one daily scan.
  const authScan = consume(auth, SCAN, 1, 'daily');
  check(authScan, { 'auth pre-used scan': (r) => r.status === 200 });

  // Guest uses the app before elevation.
  check(consume(guest, SCAN, 2, 'daily'), { 'guest daily scans': (r) => r.status === 200 });
  check(consume(guest, CHAT, 2, 'daily'), { 'guest daily chat': (r) => r.status === 200 });
  check(consume(guest, CHAT, 1, 'forever'), { 'guest forever chat': (r) => r.status === 200 });
}

function assertGuestSealed(guest) {
  const res = consume(guest, SCAN, 1, 'daily');
  if (res.status !== 409) {
    sealedConsumeAllowed.add(1);
    console.error(`sealed guest ${guest} consume status=${res.status} body=${res.body}`);
  }
  check(res, { 'sealed guest consume rejected': (r) => r.status === 409 });
}

// Compose sets K6_VUS/K6_DURATION, which force the default executor.
export default function () {
  elevation();
}

export function elevation() {
  const guest = `guest-${RUN}-${__VU}-${__ITER}`;
  const auth = `auth-${RUN}-${__VU}-${__ITER}`;
  seedGuestAuth(guest, auth);

  const before = snapshot(guest, auth);
  const mergeRes = mergeUsers(guest, auth, `migrate:${guest}:${auth}`, true);
  if (mergeRes.status !== 200) {
    mergeError.add(1);
    fail(`merge: ${mergeRes.status} ${mergeRes.body}`);
  }
  mergeSuccess.add(1);
  const body = mergeRes.json();
  check(body, { 'first merge not replay': (b) => b && b.IdempotentReplay !== true && b.idempotentReplay !== true });

  const after = snapshot(guest, auth);
  assertElevationLedger(before, after);
  assertGuestSealed(guest);

  const replay = mergeUsers(guest, auth, `migrate:${guest}:${auth}`, true);
  if (replay.status !== 200) {
    mergeError.add(1);
    fail(`replay merge: ${replay.status} ${replay.body}`);
  }
  const replayBody = replay.json();
  const replayed = replayBody.IdempotentReplay === true || replayBody.idempotentReplay === true;
  if (!replayed) {
    mergeReplayMiss.add(1);
  }
  check(replayBody, { 'retry is durable replay': () => replayed });

  const afterReplay = snapshot(guest, auth);
  expect(
    afterReplay.target[SCAN].daily.used === after.target[SCAN].daily.used,
    'replay does not double daily used',
    `${afterReplay.target[SCAN].daily.used} vs ${after.target[SCAN].daily.used}`,
  );
  expect(
    afterReplay.target[CHAT].forever.limit === after.target[CHAT].forever.limit,
    'replay does not double forever limit',
    `${afterReplay.target[CHAT].forever.limit} vs ${after.target[CHAT].forever.limit}`,
  );
}

export function retryStorm() {
  const guest = `storm-guest-${RUN}-${__VU}-${__ITER}`;
  const auth = `storm-auth-${RUN}-${__VU}-${__ITER}`;
  seedGuestAuth(guest, auth);
  const before = snapshot(guest, auth);
  const key = `storm:${guest}:${auth}`;
  const payload = JSON.stringify({
    sourceUserId: guest,
    targetUserId: auth,
    resources: [SCAN, CHAT],
    periods: ['daily', 'forever'],
    idempotencyKey: key,
    sealSource: true,
  });
  const reqs = [];
  for (let i = 0; i < 8; i++) {
    reqs.push(['POST', `${BASE_URL}/merge/merge`, payload, {
      headers: { 'Content-Type': 'application/json' },
      tags: { name: '/merge/merge' },
    }]);
  }
  const results = http.batch(reqs);
  let ok = 0;
  let replays = 0;
  for (const res of results) {
    if (res.status !== 200) {
      mergeError.add(1);
      console.error(`storm merge ${res.status} ${res.body}`);
      continue;
    }
    ok += 1;
    const body = res.json();
    if (body.IdempotentReplay === true || body.idempotentReplay === true) {
      replays += 1;
    }
  }
  check(null, { 'all concurrent merges 200': () => ok === 8 });
  check(null, { 'at least one replay in storm': () => replays >= 1 });
  const after = snapshot(guest, auth);
  assertElevationLedger(before, after);
}

export function abuseChurn() {
  const auth = `churn-auth-${RUN}-${__VU}-${__ITER}`;
  const guest1 = `churn-guest1-${RUN}-${__VU}-${__ITER}`;
  const guest2 = `churn-guest2-${RUN}-${__VU}-${__ITER}`;

  seedGuestAuth(guest1, auth);
  const after1 = (() => {
    const before = snapshot(guest1, auth);
    const res = mergeUsers(guest1, auth, `churn:${guest1}:${auth}`, true);
    if (res.status !== 200) {
      mergeError.add(1);
      fail(`churn merge1: ${res.status} ${res.body}`);
    }
    const after = snapshot(guest1, auth);
    assertElevationLedger(before, after);
    return after;
  })();
  assertGuestSealed(guest1);

  // Sign-out / new anonymous shell, then elevate onto the same Google UID.
  ensureUser(guest2);
  topup(guest2, CHAT, 5, `${guest2}:profile:chat`);
  topup(guest2, SCAN, 1, `${guest2}:profile:scan`);
  check(consume(guest2, SCAN, 2, 'daily'), { 'guest2 daily scans': (r) => r.status === 200 });

  const before2 = snapshot(guest2, auth);
  const res2 = mergeUsers(guest2, auth, `churn:${guest2}:${auth}`, true);
  if (res2.status !== 200) {
    mergeError.add(1);
    fail(`churn merge2: ${res2.status} ${res2.body}`);
  }
  const after2 = snapshot(guest2, auth);
  assertElevationLedger(before2, after2);
  assertGuestSealed(guest2);
  assertGuestSealed(guest1);

  expect(
    after2.target[SCAN].daily.used === after1.target[SCAN].daily.used + 2,
    'auth absorbed second guest daily scans',
    `got ${after2.target[SCAN].daily.used} want ${after1.target[SCAN].daily.used}+2`,
  );
}

