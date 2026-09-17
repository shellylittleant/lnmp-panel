import { createRouter, createWebHistory } from "vue-router";
import { panelPrefix } from "./api";
import Login from "./views/Login.vue";
import Layout from "./views/Layout.vue";
import Home from "./views/Home.vue";
import Settings from "./views/Settings.vue";
import Audit from "./views/Audit.vue";
import Sites from "./views/Sites.vue";
import SiteEdit from "./views/SiteEdit.vue";
import MySQL from "./views/MySQL.vue";
import Firewall from "./views/Firewall.vue";
import Logs from "./views/Logs.vue";
import Cron from "./views/Cron.vue";
import Migrate from "./views/Migrate.vue";
import PluginPage from "./views/PluginPage.vue";

export const router = createRouter({
  history: createWebHistory(panelPrefix() + "/"),
  routes: [
    { path: "/login", component: Login },
    {
      path: "/",
      component: Layout,
      children: [
        { path: "", component: Home },
        { path: "settings", component: Settings },
        { path: "audit", component: Audit },
        { path: "sites", component: Sites },
        { path: "sites/:id", component: SiteEdit },
        { path: "mysql", component: MySQL },
        { path: "firewall", component: Firewall },
        { path: "logs", component: Logs },
        { path: "cron", component: Cron },
        { path: "migrate", component: Migrate },
        { path: "templates", redirect: { path: "/sites", query: { tab: "tpl" } } },
        { path: "php", redirect: { path: "/settings", query: { tab: "env" } } },
        { path: "ha", redirect: { path: "/migrate", query: { tab: "ha" } } },
        { path: "plugins", redirect: { path: "/settings", query: { tab: "env" } } },
        { path: "files", component: PluginPage },
      ],
    },
  ],
});
