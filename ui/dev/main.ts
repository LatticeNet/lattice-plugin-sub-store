import { createApp } from "vue";

import "@latticenet/plugin-bridge/chassis.css";
import "../src/tokens.css";
import "../src/styles.css";
import DevApp from "./DevApp.vue";

/*
 * The console puts its own origin in the frame's fragment (`host_origin`), and
 * the plugin only asks it to navigate when that is there. The harness had
 * none, so every "open the share form" control stayed hidden here and a review
 * read the Files layer as having no Publish at all. The harness is its own
 * console, so it names its own origin, before the app reads it.
 */
const fragment = new URLSearchParams(window.location.hash.replace(/^#/, ""));
if (!fragment.get("host_origin")) {
  fragment.set("host_origin", window.location.origin);
  window.history.replaceState(window.history.state, "", `${window.location.pathname}${window.location.search}#${fragment}`);
}

createApp(DevApp).mount("#app");
