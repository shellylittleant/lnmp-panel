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
        { path: "php", component: Placeholder, props: { title: "PHP" } },
        { path: "mysql", component: Placeholder, props: { title: "数据库" } },
        { path: "firewall", component: Placeholder, props: { title: "防火墙" } },
        { path: "logs", component: Placeholder, props: { title: "日志" } },
        { path: "cron", component: Placeholder, props: { title: "定时任务" } },
        { path: "migrate", component: Placeholder, props: { title: "迁移" } },
        { path: "ha", component: Placeholder, props: { title: "热备" } },
        { path: "plugins", component: Placeholder, props: { title: "插件" } },
      ],
    },
  ],
});
