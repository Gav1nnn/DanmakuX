import http from 'k6/http';
import ws from 'k6/ws';
import { check, sleep } from 'k6';
import { Counter, Trend } from 'k6/metrics';

export const options = {
  scenarios: {
    danmaku_ws: {
      executor: 'constant-vus',
      vus: Number(__ENV.VUS || 20),
      duration: __ENV.DURATION || '20s',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<500'],
  },
};

const BASE_HTTP = __ENV.BASE_HTTP || 'http://127.0.0.1:8080';
const BASE_WS = __ENV.BASE_WS || inferWSBase(BASE_HTTP);
const ROOM_COUNT = Number(__ENV.ROOM_COUNT || 3);
const ROOM_PREFIX = __ENV.ROOM_PREFIX || 'room-k6-';
const SOCKET_LIFE_MS = Number(__ENV.SOCKET_LIFE_MS || 8000);
const SEND_INTERVAL_MS = Number(__ENV.SEND_INTERVAL_MS || 300);
const MESSAGES_PER_SOCKET = Number(__ENV.MESSAGES_PER_SOCKET || 20);

export const authOk = new Counter('auth_ok');
export const authFailed = new Counter('auth_failed');
export const wsOk = new Counter('ws_ok');
export const wsFailed = new Counter('ws_failed');
export const sentMessages = new Counter('sent_messages');
export const recvMessages = new Counter('recv_messages');
export const danmakuLatency = new Trend('danmaku_latency_ms');

function inferWSBase(baseHTTP) {
  if (baseHTTP.startsWith('https://')) {
    return baseHTTP.replace(/^https:/, 'wss:');
  }
  return baseHTTP.replace(/^http:/, 'ws:');
}

function pickRoom() {
  return `${ROOM_PREFIX}${Math.floor(Math.random() * ROOM_COUNT) + 1}`;
}

function auth(userId) {
  const res = http.post(
    `${BASE_HTTP}/api/v1/auth/guest`,
    JSON.stringify({ user_id: userId }),
    {
      headers: { 'Content-Type': 'application/json' },
    }
  );

  const ok = check(res, {
    'auth status 200': (r) => r.status === 200,
    'auth token exists': (r) => !!r.json('token'),
  });

  if (ok) {
    authOk.add(1);
    return res.json('token');
  }

  authFailed.add(1);
  return '';
}

export default function () {
  const userId = `u-k6-${__VU}-${__ITER}-${Date.now()}`;
  const roomId = pickRoom();
  const token = auth(userId);
  if (!token) {
    sleep(1);
    return;
  }

  const wsUrl = `${BASE_WS}/ws?room_id=${encodeURIComponent(roomId)}&token=${encodeURIComponent(token)}`;
  const sentAtByContent = {};
  let sentCount = 0;
  let socketOk = false;
  let closed = false;

  const res = ws.connect(wsUrl, null, function (socket) {
    socket.on('open', function () {
      socketOk = true;
      wsOk.add(1);

      socket.setInterval(() => {
        if (closed) {
          return;
        }
        const content = `k6|${userId}|${__VU}|${__ITER}|${sentCount}|${Date.now()}`;
        sentAtByContent[content] = Date.now();
        socket.send(
          JSON.stringify({
            type: 'danmaku',
            content,
          })
        );
        sentMessages.add(1);
        sentCount += 1;

        if (sentCount >= MESSAGES_PER_SOCKET) {
          closed = true;
          socket.close();
        }
      }, SEND_INTERVAL_MS);

      socket.setTimeout(() => {
        closed = true;
        socket.close();
      }, SOCKET_LIFE_MS);
    });

    socket.on('message', function (raw) {
      let msg;
      try {
        msg = JSON.parse(raw);
      } catch {
        return;
      }

      if (msg.type !== 'danmaku') {
        return;
      }

      recvMessages.add(1);
      const sentAt = sentAtByContent[msg.content];
      if (sentAt) {
        danmakuLatency.add(Date.now() - sentAt);
        delete sentAtByContent[msg.content];
      }
    });

    socket.on('close', function () {});
  });

  check(res, {
    'ws status 101': (r) => r && r.status === 101,
  });

  if (!socketOk) {
    wsFailed.add(1);
  }

  sleep(1);
}
