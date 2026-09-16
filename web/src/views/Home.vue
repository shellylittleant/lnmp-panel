<script setup lang="ts">
import { onMounted, ref } from "vue";
import { NCard, NDescriptions, NDescriptionsItem } from "naive-ui";
import { api } from "../api";

const st = ref<Record<string, unknown> | null>(null);
const err = ref("");
const svc = ref<Record<string, string>>({});

onMounted(async () => {
  const r = await api<Record<string, unknown>>("GET", "/status");
  if (!r.ok) {
    err.value = r.error.message;
    return;
  }
  st.value = r.data;
  svc.value = (r.data.services as Record<string, string>) || {};
});
</script>

<template>
  <n-card title="系统概览">
    <p v-if="err">{{ err }}</p>
    <n-descriptions v-else-if="st" :column="2" bordered>
      <n-descriptions-item label="主机名">{{ st.hostname }}</n-descriptions-item>
      <n-descriptions-item label="系统">{{ st.os || st.os_version }}</n-descriptions-item>
      <n-descriptions-item label="监听">{{ st.listen }}</n-descriptions-item>
      <n-descriptions-item label="路径前缀">{{ st.path_prefix }}</n-descriptions-item>
      <n-descriptions-item label="进程 PID">{{ st.pid }}</n-descriptions-item>
      <n-descriptions-item label="运行秒数">{{ st.uptime_sec }}</n-descriptions-item>
      <n-descriptions-item label="Nginx">{{ svc.nginx }}</n-descriptions-item>
      <n-descriptions-item label="PHP">{{ svc.php }}</n-descriptions-item>
      <n-descriptions-item label="MySQL">{{ svc.mysql }}</n-descriptions-item>
    </n-descriptions>
    <p v-else>加载中…</p>
  </n-card>
</template>
