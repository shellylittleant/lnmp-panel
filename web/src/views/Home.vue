<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import { NCard, NDescriptions, NDescriptionsItem, NProgress, NSpace } from "naive-ui";
import { api } from "../api";

const st = ref<Record<string, unknown> | null>(null);
const err = ref("");
const svc = ref<Record<string, string>>({});
const metrics = ref<{
  cpu: number;
  mem_used: number;
  mem_total: number;
  disk_used: number;
  disk_total: number;
  load1: number;
  net_in: number;
  net_out: number;
  at: string;
} | null>(null);
const hist = ref<{ at: string; cpu: number }[]>([]);
let timer: number | undefined;

function pct(used: number, total: number) {
  if (!total) return 0;
  return Math.min(100, Math.round((used / total) * 1000) / 10);
}
function fmtBytes(n: number) {
  if (!n) return "0";
  const u = ["B", "KiB", "MiB", "GiB", "TiB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return v.toFixed(1) + " " + u[i];
}

async function load() {
  const r = await api<Record<string, unknown>>("GET", "/status");
  if (!r.ok) {
    err.value = r.error.message;
    return;
  }
  st.value = r.data;
  svc.value = (r.data.services as Record<string, string>) || {};
  const m = await api<typeof metrics.value>("GET", "/metrics/current");
  if (m.ok) metrics.value = m.data;
  const to = new Date().toISOString();
  const from = new Date(Date.now() - 3600_000).toISOString();
  const h = await api<{ items: { at: string; cpu: number }[] }>(
    "GET",
    `/metrics/history?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}&step=raw`
  );
  if (h.ok) hist.value = (h.data.items || []).slice(-40);
}

onMounted(() => {
  load();
  timer = window.setInterval(load, 5000);
});
onUnmounted(() => {
  if (timer) clearInterval(timer);
});

const spark = () => {
  const pts = hist.value.map((x) => x.cpu || 0);
  if (pts.length < 2) return "";
  const w = 320;
  const h = 48;
  const max = Math.max(100, ...pts);
  return pts
    .map((v, i) => {
      const x = (i / (pts.length - 1)) * w;
      const y = h - (v / max) * h;
      return `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
};
</script>

<template>
  <n-space vertical>
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
    <n-card title="资源指标">
      <div v-if="metrics">
        <p>采样 {{ metrics.at }}　load {{ metrics.load1 }}</p>
        <p>CPU {{ Number(metrics.cpu || 0).toFixed(1) }}%</p>
        <n-progress type="line" :percentage="Math.min(100, Number(Number(metrics.cpu || 0).toFixed(1)))" />
        <p>内存 {{ fmtBytes(metrics.mem_used) }} / {{ fmtBytes(metrics.mem_total) }}</p>
        <n-progress type="line" :percentage="pct(metrics.mem_used, metrics.mem_total)" />
        <p>磁盘 {{ fmtBytes(metrics.disk_used) }} / {{ fmtBytes(metrics.disk_total) }}</p>
        <n-progress type="line" :percentage="pct(metrics.disk_used, metrics.disk_total)" />
        <p>网络累计 入 {{ fmtBytes(metrics.net_in) }}　出 {{ fmtBytes(metrics.net_out) }}</p>
        <svg v-if="spark()" viewBox="0 0 320 48" width="320" height="48" style="margin-top: 8px">
          <path :d="spark()" fill="none" stroke="#2080f0" stroke-width="1.5" />
        </svg>
      </div>
      <p v-else>尚无采样（调度器每 15 秒写入）</p>
    </n-card>
  </n-space>
</template>
