function errorResponse(status, message) {
  return new Response(JSON.stringify({ error: message }), {
    status,
    headers: {
      'content-type': 'application/json; charset=utf-8',
      'cache-control': 'no-store',
    },
  });
}

function parseApiOrigin(value) {
  if (typeof value !== 'string' || !value.trim()) {
    return null;
  }

  let origin;
  try {
    origin = new URL(value.trim());
  } catch {
    return null;
  }

  if (
    !['http:', 'https:'].includes(origin.protocol)
    || origin.username
    || origin.password
    || origin.search
    || origin.hash
    || origin.pathname.replace(/\/+$/, '') !== '/api'
  ) {
    return null;
  }

  origin.pathname = '/api';
  return origin;
}

export async function onRequest(context) {
  const configuredApiOrigin = context.env.API_ORIGIN;
  if (typeof configuredApiOrigin !== 'string' || !configuredApiOrigin.trim()) {
    return errorResponse(500, 'API proxy is not configured');
  }
  const upstreamUrl = parseApiOrigin(configuredApiOrigin);
  if (!upstreamUrl) {
    return errorResponse(500, 'API_ORIGIN must be an absolute URL with exactly one /api path prefix');
  }

  const matchedPath = context.params.path;
  const rawSegments = Array.isArray(matchedPath)
    ? matchedPath.flatMap((segment) => String(segment).split('/'))
    : typeof matchedPath === 'string'
      ? matchedPath.split('/')
      : [];
  const segments = rawSegments.filter(Boolean);
  if (segments[0] === 'api') {
    return errorResponse(400, 'API proxy path must be relative to /api');
  }

  upstreamUrl.pathname = `/api${segments.length ? `/${segments.map(encodeURIComponent).join('/')}` : ''}`;
  upstreamUrl.search = new URL(context.request.url).search;

  const headers = new Headers(context.request.headers);
  headers.delete('host');

  // Keep Cloudflare Access credentials server-side. Browser-supplied values must
  // never be forwarded, otherwise clients could choose the upstream identity.
  headers.delete('cf-access-client-id');
  headers.delete('cf-access-client-secret');

  const accessClientID = context.env.CF_ACCESS_CLIENT_ID;
  const accessClientSecret = context.env.CF_ACCESS_CLIENT_SECRET;
  const hasAccessClientID = Boolean(accessClientID);
  const hasAccessClientSecret = Boolean(accessClientSecret);

  if (hasAccessClientID !== hasAccessClientSecret) {
    return errorResponse(500, 'API proxy access credentials are incomplete');
  }

  if (hasAccessClientID) {
    headers.set('CF-Access-Client-Id', accessClientID);
    headers.set('CF-Access-Client-Secret', accessClientSecret);
  }

  const method = context.request.method;
  const requestInit = {
    method,
    headers,
    redirect: 'manual',
  };

  if (method !== 'GET' && method !== 'HEAD') {
    requestInit.body = context.request.body;
  }

  try {
    const upstreamResponse = await fetch(upstreamUrl.toString(), requestInit);
    const responseHeaders = new Headers(upstreamResponse.headers);
    const isCacheableFileRequest =
      (method === 'GET' || method === 'HEAD')
      && segments[0] === 'files';
    const shouldPreserveFileCache =
      isCacheableFileRequest && upstreamResponse.ok;

    if (shouldPreserveFileCache) {
      // The backend owns the cache policy for public files. Keep the proxy
      // conservative if a successful file response unexpectedly omits it.
      if (!responseHeaders.has('cache-control')) {
        responseHeaders.set('cache-control', 'no-store');
      }
    } else {
      responseHeaders.set('cache-control', 'no-store');
    }

    return new Response(upstreamResponse.body, {
      status: upstreamResponse.status,
      statusText: upstreamResponse.statusText,
      headers: responseHeaders,
    });
  } catch {
    return errorResponse(502, 'API upstream is temporarily unreachable');
  }
}
