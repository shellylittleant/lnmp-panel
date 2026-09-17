<script setup lang="ts">
import { onMounted, ref } from "vue";
import { NAlert, NButton, NCard, NSpace, useMessage } from "naive-ui";
import { api, waitJob } from "../api";
import PHP from "./PHP.vue";
import Placeholder from "./Placeholder.vue";

const msg = useMessage();
const env = ref<any>(null);
const busy = ref("");

async function load() {
  const r = await api<any>("GET", "/env");
  if (r.ok) env.value = r.data;
}

onMounted(load);

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
    <Placeholder title="插件" />
  </n-space>
</template>
