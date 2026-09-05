import { createApp } from "vue";

import "@latticenet/plugin-bridge/chassis.css";
import "../src/tokens.css";
import "../src/styles.css";
import DevApp from "./DevApp.vue";

createApp(DevApp).mount("#app");
