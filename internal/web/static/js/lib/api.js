// @ts-check
/**
 * Talking to the Directory's API from its own pages.
 *
 * Every call is same-origin: the pages are served by the Directory, and the
 * content security policy allows connections to it alone (connect-src
 * 'self'). Writes carry Content-Type: application/json, which the server's
 * cross-site guard requires.
 *
 * @module
 */

/** The API's root path. */
export const API_ROOT = "/-/api/v1/";

/**
 * An answer of the API. data is the parsed JSON body, or null when there is
 * none (204 after a delete) or it is not JSON. An error answer (4xx, 5xx) is
 * an answer, not an exception: data is then {error: {code, message,
 * details}}, and the page shows error.message.
 *
 * @typedef {{ status: number, data: any }} ApiResponse
 */

/**
 * @typedef {object} ApiOptions
 * @property {string} [method] GET when omitted.
 * @property {unknown} [body] Sent as JSON. Leave out for GET and DELETE.
 * @property {AbortSignal} [signal] Aborting it rejects the call with an
 *   AbortError.
 */

/**
 * Calls the API.
 *
 * path is either below the API root ("links?q=jira", "resolve?path=...") or
 * a full path starting with "/" ("/-/api/v1/links/12"). Values put into the
 * path or query must be encoded by the caller (encodeURIComponent).
 *
 * It rejects only when no answer arrived: a network failure (TypeError) or
 * an abort (DOMException named "AbortError"). Every HTTP status resolves.
 *
 * @param {string} path
 * @param {ApiOptions} [options]
 * @returns {Promise<ApiResponse>}
 */
export async function apiFetch(path, { method = "GET", body, signal } = {}) {
  const url = path.startsWith("/") ? path : API_ROOT + path;
  /** @type {RequestInit} */
  const init = {
    method,
    credentials: "same-origin",
    cache: "no-store",
    headers: { Accept: "application/json" },
    signal,
  };
  if (body !== undefined) {
    init.headers = { Accept: "application/json", "Content-Type": "application/json" };
    init.body = JSON.stringify(body);
  }
  const response = await fetch(url, init);
  let data = null;
  const type = response.headers.get("Content-Type") || "";
  if (response.status !== 204 && type.startsWith("application/json")) {
    try {
      data = await response.json();
    } catch (err) {
      // An abort while the body is read must still reach the caller.
      if (err instanceof DOMException && err.name === "AbortError") throw err;
      data = null;
    }
  }
  return { status: response.status, data };
}

/**
 * The message of an API error answer, or a fallback for answers that carry
 * none (a proxy's error page, a bare 500).
 *
 * @param {ApiResponse} response
 * @param {string} [fallback]
 * @returns {string}
 */
export function errorMessage(response, fallback = "Something went wrong. Try again.") {
  const message = response.data && response.data.error && response.data.error.message;
  return typeof message === "string" && message !== "" ? message : fallback;
}

/**
 * Whether an exception is the rejection of an aborted call, which is never
 * worth showing: a newer call replaced it.
 *
 * @param {unknown} err
 * @returns {boolean}
 */
export function isAbort(err) {
  return err instanceof DOMException && err.name === "AbortError";
}

/**
 * The outcome of one call of a debounced function: current is false when a
 * newer call replaced this one, and value is then absent.
 *
 * @template R
 * @typedef {{ current: true, value: R } | { current: false }} Debounced
 */

/**
 * Wraps work done while the user types (plan section 3.6): calls are
 * delayed by ms, each new call aborts the previous one, and only the
 * newest call's answer is ever reported as current, whatever order the
 * answers arrive in.
 *
 * fn receives an AbortSignal first, then the call's arguments, and should
 * pass the signal to apiFetch. The returned function resolves to
 * {current: true, value} for the newest call and to {current: false} for
 * every call that a newer one replaced, including one whose fn rejected
 * because it was aborted. An error of the newest call rejects.
 *
 *   const search = debounced((signal, q) => apiFetch(`links?q=${encodeURIComponent(q)}`, { signal }));
 *   input.addEventListener("input", async () => {
 *     const result = await search(input.value);
 *     if (result.current) show(result.value);
 *   });
 *
 * cancel() aborts whatever is pending or running, for example when the
 * field is emptied; the pending call then resolves {current: false}.
 *
 * @template {unknown[]} A
 * @template R
 * @param {(signal: AbortSignal, ...args: A) => Promise<R>} fn
 * @param {number} [ms]
 * @returns {((...args: A) => Promise<Debounced<R>>) & { cancel: () => void }}
 */
export function debounced(fn, ms = 200) {
  /** @type {AbortController | null} */
  let controller = null;
  /** @type {ReturnType<typeof setTimeout> | undefined} */
  let timer;
  /** @type {((outcome: { current: false }) => void) | null} */
  let settlePending = null;
  let latest = 0;

  const cancel = () => {
    latest++;
    clearTimeout(timer);
    controller?.abort();
    controller = null;
    settlePending?.({ current: false });
    settlePending = null;
  };

  /** @param {A} args */
  const call = (...args) => {
    cancel();
    const id = latest;
    const own = new AbortController();
    controller = own;
    return /** @type {Promise<Debounced<R>>} */ (
      new Promise((resolve, reject) => {
        settlePending = resolve;
        timer = setTimeout(async () => {
          settlePending = null;
          try {
            const value = await fn(own.signal, ...args);
            resolve(id === latest ? { current: true, value } : { current: false });
          } catch (err) {
            if (id !== latest || isAbort(err)) resolve({ current: false });
            else reject(err);
          } finally {
            if (controller === own) controller = null;
          }
        }, ms);
      })
    );
  };

  return Object.assign(call, { cancel });
}
