// @ts-check
/**
 * Building the page from untrusted text, safely.
 *
 * Names, descriptions, paths and destinations come from users. They enter
 * the DOM only as text nodes and attribute values set through DOM methods,
 * never as markup: the DOM's HTML-parsing setters are banned in this
 * directory, and "make dom-guard" fails on them.
 *
 * @module
 */

/**
 * A child of el(): text (made a text node), a node, nothing (skipped), or
 * a list of these.
 *
 * @typedef {string | number | Node | null | undefined | false | ChildList} Child
 * @typedef {Child[]} ChildList
 */

/**
 * Attributes of el(). A string or number value is set as an attribute; true
 * sets an empty attribute (disabled, hidden); false, null and undefined
 * leave it out. "value", "checked" and "selected" are set as properties, so
 * they hold after the user edits the control. "on" maps event names to
 * listeners.
 *
 * @typedef {{ [name: string]: string | number | boolean | null | undefined | Record<string, EventListener> }} Attrs
 */

const PROPERTIES = new Set(["value", "checked", "selected"]);
const URL_ATTRIBUTES = new Set(["href", "src", "action", "formaction"]);

/**
 * Creates an element with attributes and children.
 *
 *   el("a", { class: "golink", href: "/-/links/" + encodeURIComponent(name) }, "go/", name)
 *
 * It refuses, by throwing, what would bypass the content security policy
 * or carry script: a "style" attribute, on* attributes (use { on: {...} }),
 * and a URL attribute whose scheme is not http, https or mailto (relative
 * URLs are fine).
 *
 * @template {keyof HTMLElementTagNameMap} K
 * @param {K} tag
 * @param {Attrs | null} [attrs]
 * @param {...Child} children
 * @returns {HTMLElementTagNameMap[K]}
 */
export function el(tag, attrs, ...children) {
  const node = document.createElement(tag);
  for (const [name, value] of Object.entries(attrs || {})) {
    if (name === "on") {
      for (const [event, listener] of Object.entries(/** @type {Record<string, EventListener>} */ (value || {}))) {
        node.addEventListener(event, listener);
      }
      continue;
    }
    if (typeof value === "object" && value !== null) {
      throw new TypeError(`el: attribute ${name} needs a string, number or boolean`);
    }
    setAttr(node, name, value);
  }
  append(node, ...children);
  return node;
}

/**
 * Sets one attribute by the rules of el(); null, undefined or false
 * removes it.
 *
 * @param {Element} node
 * @param {string} name
 * @param {string | number | boolean | null | undefined} value
 */
export function setAttr(node, name, value) {
  const lower = name.toLowerCase();
  if (lower === "style" || lower.startsWith("on")) {
    throw new TypeError(`el: the attribute ${name} is not allowed; use classes and addEventListener`);
  }
  if (PROPERTIES.has(lower)) {
    /** @type {any} */ (node)[lower] = lower === "value" ? String(value ?? "") : Boolean(value);
    return;
  }
  if (value === false || value === null || value === undefined) {
    node.removeAttribute(name);
    return;
  }
  const text = value === true ? "" : String(value);
  if (URL_ATTRIBUTES.has(lower) && !safeURL(text)) {
    throw new TypeError(`el: refusing ${name}="${text}": only http, https, mailto and relative URLs`);
  }
  node.setAttribute(name, text);
}

/**
 * Whether a URL may be put into href or src: relative, or with a scheme of
 * http, https or mailto. A destination of a link is always http or https
 * (the server checks); this is the second line, against javascript: and
 * data: in particular.
 *
 * The URL is read the way a browser reads it, by the URL parser, and not by
 * a pattern: a browser drops tabs and line breaks anywhere in a URL and
 * control characters and spaces at its ends, so "java\tscript:" is a
 * javascript: URL to it. A relative URL is resolved against a fixed http
 * base, which gives it the scheme it would have on the page. Text the parser
 * refuses is refused.
 *
 * @param {string} url
 * @returns {boolean}
 */
export function safeURL(url) {
  let protocol;
  try {
    protocol = new URL(url, "http://relative.invalid/").protocol;
  } catch {
    return false;
  }
  return protocol === "http:" || protocol === "https:" || protocol === "mailto:";
}

/**
 * Appends children by the rules of el().
 *
 * @param {Node} parent
 * @param {...Child} children
 */
export function append(parent, ...children) {
  for (const child of children) {
    if (child === null || child === undefined || child === false) continue;
    if (Array.isArray(child)) append(parent, ...child);
    else if (child instanceof Node) parent.appendChild(child);
    else parent.appendChild(document.createTextNode(String(child)));
  }
}

/**
 * Removes every child of a node.
 *
 * @param {Node} node
 */
export function clear(node) {
  while (node.firstChild) node.removeChild(node.firstChild);
}

/**
 * Replaces every child of a node.
 *
 * @param {Node} node
 * @param {...Child} children
 */
export function replace(node, ...children) {
  clear(node);
  append(node, ...children);
}

/**
 * Finds an element by id and checks its type, for code that cannot work
 * without it; a missing or mistyped element is a bug in the template and
 * throws.
 *
 *   const input = byId("search", HTMLInputElement);
 *
 * @template {Element} T
 * @param {string} id
 * @param {{ new (...args: any[]): T }} type
 * @param {Document | DocumentFragment} [root]
 * @returns {T}
 */
export function byId(id, type, root = document) {
  const node = root.getElementById(id);
  if (!(node instanceof type)) {
    throw new Error(`#${id} is missing or is not a ${type.name}`);
  }
  return node;
}

/**
 * Says a short message to screen readers without moving focus, through
 * the polite live region the layout does not have to provide: one is
 * created on first use.
 *
 * @param {string} message
 */
export function announce(message) {
  let region = document.getElementById("announcer");
  if (!region) {
    region = el("div", { id: "announcer", class: "visually-hidden", "aria-live": "polite", role: "status" });
    document.body.appendChild(region);
  }
  // Clearing first makes the same message be read again.
  region.textContent = "";
  const node = region;
  setTimeout(() => {
    node.textContent = message;
  }, 50);
}
