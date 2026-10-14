// @ts-check
/**
 * The miss page. The page is complete without this module: the first
 * suggestion carries the autofocus attribute, so pressing Enter follows it
 * (PRD scenario 4). Not every browser honours autofocus on a link, so this
 * focuses it when nothing else has focus yet. It never takes focus away from
 * something the user already moved to.
 *
 * @module
 */

const first = document.querySelector("a[data-miss-first]");
if (first instanceof HTMLAnchorElement) {
  const active = document.activeElement;
  if (active === null || active === document.body || active === document.documentElement) {
    first.focus();
  }
}
