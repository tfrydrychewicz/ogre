// A stand-in for "some other website": a plain-HTTP server inside the test
// process, reached under a public-looking host name through a resolver rule.
// It needs no network, and the extension has no host access to it.
//
//   /page                an empty HTML page; tests add links and forms to it
//   /redirect?to=URL     302 to URL (a server redirect to a go link)
//   /meta?to=URL         a page that goes to URL by <meta http-equiv="refresh">
//
// The server lives and dies with the process that started it; stop() closes it.

import http from "node:http";

export const THIRD_PARTY_HOST = "thirdparty.example";
export const THIRD_PARTY_URL = `http://${THIRD_PARTY_HOST}`;

const escapeHtml = (text) => text.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);

/**
 * Starts the server on a free loopback port.
 * @returns {Promise<{port: number, resolverRule: string, stop(): Promise<void>}>} resolverRule is the
 *   value for --host-resolver-rules that sends THIRD_PARTY_HOST to this server
 */
export async function startThirdParty() {
  const server = http.createServer((request, response) => {
    const url = new URL(request.url, THIRD_PARTY_URL);
    const to = url.searchParams.get("to") ?? "";
    response.setHeader("Cache-Control", "no-store");
    if (url.pathname === "/redirect" && to) {
      response.writeHead(302, { Location: to }).end();
      return;
    }
    response.setHeader("Content-Type", "text/html; charset=utf-8");
    const refresh = url.pathname === "/meta" && to ? `<meta http-equiv="refresh" content="0;url=${escapeHtml(to)}">` : "";
    response.end(
      `<!doctype html><html><head><meta charset="utf-8"><title>third party</title><link rel="icon" href="data:,">${refresh}</head>` +
        `<body data-spike="third-party"><h1>third-party page</h1></body></html>\n`,
    );
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const { port } = server.address();
  return {
    port,
    resolverRule: `MAP ${THIRD_PARTY_HOST} 127.0.0.1:${port}`,
    stop: () =>
      new Promise((resolve) => {
        server.closeAllConnections();
        server.close(() => resolve());
      }),
  };
}
