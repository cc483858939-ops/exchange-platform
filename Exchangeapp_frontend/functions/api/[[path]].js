function errorResponse(status, message) {
  return new Response(JSON.stringify({ error: message }), {
    status,
    headers: {
      'content-type': 'application/json; charset=utf-8',
      'cache-control': 'no-store',
    },
  });
}

export async function onRequest(context) {
  const apiOrigin = context.env.API_ORIGIN;

  if (!apiOrigin) {
    return errorResponse(500, 'API proxy is not configured');
  }

  const matchedPath = context.params.path;
  const segments = Array.isArray(matchedPath)
    ? matchedPath
    : matchedPath
      ? [matchedPath]
      : [];

  const upstreamUrl = new URL(apiOrigin);
  upstreamUrl.pathname =
    `${upstreamUrl.pathname.replace(/\/+$/, '')}/` +
    segments.map(encodeURIComponent).join('/');
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
    responseHeaders.set('cache-control', 'no-store');

    return new Response(upstreamResponse.body, {
      status: upstreamResponse.status,
      statusText: upstreamResponse.statusText,
      headers: responseHeaders,
    });
  } catch {
    return errorResponse(502, 'API upstream is temporarily unreachable');
  }
}
