<script setup lang="ts">
import { onMounted, ref } from "vue";
import { NAlert, NButton, NCard, NSpace, NSwitch, useMessage } from "naive-ui";
import { api, waitJob } from "../api";
import PHP from "./PHP.vue";

const msg = useMessage();
const env = ref<any>(null);
const busy = ref("");
const plugins = ref<{ id: string; name: string; version: string; enabled: boolean; error?: string }[]>([]);

async function load() {
  const r = await api<any>("GET", "/env");
  if (r.ok) env.value = r.data;
  const p = await api<{ items: typeof plugins.value }>("GET", "/plugins");
  if (p.ok) plugins.value = p.data.items || [];
}

onMounted(load);

async function togglePlugin(id: string, enabled: boolean) {
  const r = await api("PUT", "/plugins/" + id, { enabled });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success(enabled ? "已启用" : "已停用");
  await load();
}

async function runJob(title: string, path: string) {
  busy.value = title;
  try {
    const r = await api<{ job_id: string }>("POST", path, {});
    if (!r.ok) {
      msg.error(r.error.message);
      return;
    }
    await waitJob(r.data.job_id, (ph, d) => (busy.value = `${title}：${ph} ${d}`));
    msg.success(title + "完成");
    await load();
  } catch (e: any) {
    msg.error(e.message || String(e));
  } finally {
    busy.value = "";
  }
}
</script>

<template>
  <n-space vertical>
    <n-card title="Nginx">
      <p v-if="env">{{ env.nginx_installed ? "已安装 " + (env.nginx_version || "") : "未安装" }}</p>
      <n-button type="primary" :disabled="!!busy" @click="runJob('安装 Nginx', '/env/nginx/install')">从 Ubuntu 源安装 Nginx</n-button>
      <span v-if="busy" style="margin-left: 8px">{{ busy }}</span>
    </n-card>
    <PHP />
    <n-card title="MySQL">
      <n-alert type="info" title="按架构分源">
        x86_64 用 MySQL 官方 apt 8.4 LTS；arm64 用 Ubuntu 源 mysql-server 8.0。
      </n-alert>
      <p v-if="env">
        计划来源：{{ env.mysql_source === "official" ? "官方 8.4 LTS" : "Ubuntu mysql-server 8.0" }}；{{ env.mysql }}
      </p>
      <n-button type="primary" :disabled="!!busy" @click="runJob('安装 MySQL', '/env/mysql/install')">安装 MySQL</n-button>
    </n-card>
    <n-card title="插件">
      <p v-if="!plugins.length">当前二进制未编入官方插件。发布构建使用 <code>go build -tags plugin_all</code>。</p>
      <n-space v-else vertical>
        <div v-for="it in plugins" :key="it.id" style="display: flex; align-items: center; gap: 12px">
          <n-switch :value="it.enabled" @update:value="(v: boolean) => togglePlugin(it.id, v)" />
          <span>{{ it.name }}（{{ it.id }} {{ it.version }}）</span>
          <n-button v-if="it.enabled && it.id === 'filexfer'" size="small" @click="$router.push('/files')">打开文件上传下载</n-button>
          <span v-if="it.error" style="color: #d03050">{{ it.error }}</span>
        </div>
      </n-space>
    </n-card>
  </n-space>
</template>
