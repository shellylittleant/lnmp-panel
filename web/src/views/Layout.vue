<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { NAlert, NLayout, NLayoutSider, NLayoutContent, NMenu, NButton, NSpace } from "naive-ui";
import type { MenuOption } from "naive-ui";
import { api, setCsrf } from "../api";

const router = useRouter();
const route = useRoute();
const user = ref("");
const haRole = ref("");
let haTimer: number | undefined;

const menu: MenuOption[] = [
  { label: "概览", key: "/" },
  { label: "网站", key: "/sites" },
  { label: "数据库", key: "/mysql" },
  { label: "防火墙", key: "/firewall" },
  { label: "日志", key: "/logs" },
  { label: "定时任务", key: "/cron" },
  { label: "迁移与热备", key: "/migrate" },
  { label: "设置", key: "/settings" },
  { label: "审计", key: "/audit" },
];

const menuValue = computed(() => {
  const p = route.path;
  if (p === "/" || p === "") return "/";
  if (p.startsWith("/sites")) return "/sites";
  if (p.startsWith("/mysql")) return "/mysql";
  if (p.startsWith("/firewall")) return "/firewall";
  if (p.startsWith("/logs")) return "/logs";
  if (p.startsWith("/cron")) return "/cron";
  if (p.startsWith("/migrate")) return "/migrate";
  if (p.startsWith("/settings")) return "/settings";
  if (p.startsWith("/audit")) return "/audit";
  return p;
});

async function refreshHA() {
  const r = await api<{ role: string }>("GET", "/ha/status");
  if (r.ok) haRole.value = r.data.role;
}

onMounted(async () => {
  const r = await api<{ username: string; csrf_token: string }>("GET", "/auth/session");
  if (!r.ok) {
    router.replace("/login");
    return;
  }
  user.value = r.data.username;
  setCsrf(r.data.csrf_token);
  await refreshHA();
  haTimer = window.setInterval(refreshHA, 5000);
});

onUnmounted(() => {
  if (haTimer) clearInterval(haTimer);
});

async function logout() {
  await api("POST", "/auth/logout", {});
  router.replace("/login");
}

function go(key: string) {
  router.push(key);
}
</script>

<template>
  <n-layout has-sider style="height: 100%">
    <n-layout-sider bordered width="200" content-style="padding: 12px 0">
      <div class="brand">LNMP 面板</div>
      <n-menu :options="menu" :value="menuValue" @update:value="go" />
    </n-layout-sider>
    <n-layout>
      <div class="top">
        <n-space>
          <span>{{ user }}</span>
          <n-button size="small" @click="logout">退出</n-button>
        </n-space>
      </div>
      <n-alert v-if="haRole === 'standby'" type="warning" :bordered="false" class="ha-banner">
        本机为备机，配置由主机同步
      </n-alert>
      <n-layout-content content-style="padding: 20px">
        <router-view />
      </n-layout-content>
    </n-layout>
  </n-layout>
</template>

<style scoped>
.brand {
  padding: 8px 20px 16px;
  font-weight: 600;
}
.top {
  display: flex;
  justify-content: flex-end;
  padding: 10px 16px;
  border-bottom: 1px solid #eee;
}
.ha-banner {
  margin: 0;
  border-radius: 0;
}
</style>
