// @ts-check
/**
 * The setup page. It is complete without this module; the module only
 * reveals and wires the buttons that copy a command, a folder or the search
 * shortcut's URL.
 *
 * @module
 */

import { wireCopyButtons } from "./lib/copy.js";

wireCopyButtons(document);
