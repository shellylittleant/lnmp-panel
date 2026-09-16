import { createRouter, createWebHistory } from "vue-router";
import { panelPrefix } from "./api";
import Login from "./views/Login.vue";
import Layout from "./views/Layout.vue";
import Home from "./views/Home.vue";
import Settings from "./views/Settings.vue";
import Audit from "./views/Audit.vue";
import Placeholder from "./views/Placeholder.vue";
import Sites from "./views/Sites.vue";
import SiteEdit from "./views/SiteEdit.vue";
import Templates from "./views/Templates.vue";
import PHP from "./views/PHP.vue";
import MySQL from "./views/MySQL.vue";
import Firewall from "./views/Firewall.vue";
import Logs from "./views/Logs.vue";
import Cron from "./views/Cron.vue";

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
        { path: "templates", component: Templates },
        { path: "php", component: PHP },
        { path: "mysql", component: MySQL },
        { path: "firewall", component: Firewall },
        { path: "logs", component: Logs },
        { path: "cron", component: Cron },
        { path: "migrate", component: Placeholder, props: { title: "迁移" } },
        { path: "ha", component: Placeholder, props: { title: "热备" } },
        { path: "plugins", component: Placeholder, props: { title: "插件" } },
      ],
    },
  ],
});
