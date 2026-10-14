// @ts-check
// Small DOM helpers shared by the extension's pages. Text goes in with
// textContent only: what the pages show includes addresses the user typed
// and messages built from a server's answer, and none of it is ever parsed
// as markup (plan section 3.6; make dom-guard checks this directory).

/**
 * The element with this id, or an error naming the id: a page whose markup
 * and script disagree should fail loudly, not half-work.
 *
 * @template {HTMLElement} T
 * @param {string} id
 * @param {new () => T} type the element's class, such as HTMLButtonElement
 * @returns {T}
 */
export function byId(id, type) {
  const element = document.getElementById(id);
  if (!(element instanceof type)) throw new Error(`#${id} is missing or is not a ${type.name}`);
  return element;
}

/** @typedef {"info" | "success" | "warning" | "error"} NoticeKind */

/**
 * Replaces what `container` shows with one notice: a title that says its
 * kind in words (color is never the only signal) and sentences under it.
 *
 * @param {HTMLElement} container usually a role="status" region
 * @param {NoticeKind} kind
 * @param {string} title for example "Connected"
 * @param {...string} paragraphs
 * @returns {HTMLElement} the notice, for callers that add a button to it
 */
export function showNotice(container, kind, title, ...paragraphs) {
  const notice = document.createElement("div");
  notice.className = `notice notice-${kind}`;
  const heading = document.createElement("strong");
  heading.className = "notice-title";
  heading.textContent = title;
  notice.append(heading);
  for (const text of paragraphs) {
    const paragraph = document.createElement("p");
    paragraph.textContent = text;
    notice.append(paragraph);
  }
  container.replaceChildren(notice);
  return notice;
}

/**
 * Empties a message area.
 *
 * @param {HTMLElement} container
 */
export function clearNotice(container) {
  container.replaceChildren();
}

/**
 * The keys that open a new tab on this computer, as words for instructions.
 *
 * @returns {string} "⌘T" on a Mac, "Ctrl+T" elsewhere
 */
export function newTabKeys() {
  const platform = /** @type {{ userAgentData?: { platform?: string } }} */ (navigator).userAgentData?.platform ?? navigator.platform;
  return /mac/i.test(platform) ? "⌘T" : "Ctrl+T";
}
