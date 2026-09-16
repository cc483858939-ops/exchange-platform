import http from 'k6/http';
import { check, fail, sleep } from 'k6';

const baseURL = String(__ENV.BASE_URL || 'http://127.0.0.1:3000').replace(/\/+$/, '');
const password = __ENV.LOADTEST_USER_PASSWORD;
const profile = String(__ENV.LOADTEST_PROFILE || 'baseline').trim().toLowerCase();
const userCount = parseUserCount(__ENV.LOADTEST_USER_COUNT || '10');
const userPrefix = String(__ENV.LOADTEST_USER_PREFIX || 'loadtestv1').trim();

if (!password || password.trim() === '') {
  throw new Error('LOADTEST_USER_PASSWORD is required');
}
if (!Number.isInteger(userCount) || userCount < 1 || userCount > 20) {
  throw new Error('LOADTEST_USER_COUNT must be an integer between 1 and 20');
}
if (!/^loadtest[A-Za-z0-9_-]*$/.test(userPrefix)) {
  throw new Error('LOADTEST_USER_PREFIX must start with loadtest and contain only letters, digits, underscore, and hyphen');
}
if (profile !== 'smoke' && profile !== 'baseline') {
  throw new Error('LOADTEST_PROFILE must be smoke or baseline');
}

const profileOptions = profile === 'smoke'
  ? { vus: 1, duration: '30s' }
  : {
      stages: [
        { duration: '30s', target: 5 },
        { duration: '60s', target: 20 },
        { duration: '60s', target: 50 },
        { duration: '30s', target: 0 },
      ],
    };

export const options = {
  ...profileOptions,
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<500'],
  },
};

function parseUserCount(raw) {
  const value = Number.parseInt(String(raw).trim(), 10);
  return Number.isFinite(value) && String(value) === String(raw).trim() ? value : NaN;
}

function isSuccessful(response) {
  return response.status >= 200 && response.status < 300;
}

function requestParams(token, name) {
  return {
    headers: {
      Authorization: `Bearer ${token}`,
    },
    tags: { name },
  };
}

function syntheticUsername(index) {
  return `${userPrefix}_${String(index).padStart(4, '0')}`;
}

function retryAfterMessage(response) {
  const retryAfter = response.headers['Retry-After'] || response.headers['retry-after'];
  return retryAfter ? ` Retry-After=${retryAfter}s.` : '';
}

export function setup() {
  const healthResponse = http.get(`${baseURL}/healthz`, { tags: { name: 'healthz' } });
  if (!check(healthResponse, { 'healthz returned 200': (response) => response.status === 200 })) {
    fail(`Health check failed with HTTP ${healthResponse.status}.`);
  }

  const tokens = [];
  for (let index = 1; index <= userCount; index += 1) {
    const response = http.post(
      `${baseURL}/api/auth/login`,
      JSON.stringify({ username: syntheticUsername(index), password }),
      {
        headers: { 'Content-Type': 'application/json' },
        tags: { name: 'login-setup' },
      },
    );

    if (response.status === 429) {
      fail(`Authentication setup was rate-limited. Wait for Retry-After before rerunning.${retryAfterMessage(response)}`);
    }
    if (!isSuccessful(response)) {
      fail(`Authentication setup failed with HTTP ${response.status}.`);
    }

    let token;
    try {
      token = response.json('access_token');
    } catch (_) {
      fail('Authentication setup returned an invalid response.');
    }
    if (!token) {
      fail('Authentication setup returned no access token.');
    }
    tokens.push(token);
  }

  return { tokens };
}

export default function (data) {
  const token = data.tokens[(__VU - 1) % data.tokens.length];

  const recommendationsResponse = http.get(
    `${baseURL}/api/recommendations/posts?limit=20`,
    requestParams(token, 'recommendations'),
  );
  check(recommendationsResponse, {
    'recommendations returned 2xx': isSuccessful,
  });

  if (isSuccessful(recommendationsResponse)) {
    let recommendations;
    try {
      recommendations = recommendationsResponse.json('items');
    } catch (_) {
      recommendations = [];
    }
    if (Array.isArray(recommendations) && recommendations.length > 0) {
      const item = recommendations[(__VU + __ITER) % recommendations.length];
      const postID = item && item.post && item.post.id;
      if (postID) {
        const postResponse = http.get(
          `${baseURL}/api/posts/${postID}`,
          requestParams(token, 'post-detail'),
        );
        check(postResponse, {
          'post-detail returned 2xx': isSuccessful,
        });
      }
    }
  }

  const secondaryRoute = (__VU + __ITER) % 3;
  let secondaryPath;
  let secondaryName;
  if (secondaryRoute === 0) {
    secondaryPath = '/api/feed/following?limit=20';
    secondaryName = 'following';
  } else if (secondaryRoute === 1) {
    secondaryPath = '/api/me/notifications?limit=20';
    secondaryName = 'notifications';
  } else {
    secondaryPath = '/api/me/bookmarks?limit=20';
    secondaryName = 'bookmarks';
  }

  const secondaryResponse = http.get(
    `${baseURL}${secondaryPath}`,
    requestParams(token, secondaryName),
  );
  check(secondaryResponse, {
    [`${secondaryName} returned 2xx`]: isSuccessful,
  });

  sleep(1);
}
