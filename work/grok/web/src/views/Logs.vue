<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import { useRouter } from "vue-router";
import {
  NAlert,
  NButton,
  NCard,
  NDataTable,
  NForm,
  NFormItem,
  NInput,
  NSelect,
  NSpace,
  NTag,
  useMessage,
} from "naive-ui";
import type { DataTableColumns, SelectOption } from "naive-ui";
import { api, panelPrefix } from "../api";

const msg = useMessage();
const router = useRouter();
const sources = ref<{ id: string; name: string; path: string; kind: string }[]>([]);
const source = ref("journald");
const q = ref("");
const level = ref("");
const lines = ref<{ text: string; level: string }[]>([]);
const following = ref(false);
const stats = ref<any>(null);
const kind = ref("access");
const site = ref("");
const keepDays = ref(14);
const maxSize = ref("100M");
const rotateRaw = ref("");
const drifted = ref(false);
const force = ref(false);
const truncated = ref(false);
let es: EventSource | null = null;

const levelOpts: SelectOption[] = [
  { label: "全部", value: "" },
  { label: "error", value: "error" },
  { label: "warn", value: "warn" },
  { label: "info", value: "info" },
];
const kindOpts: SelectOption[] = [
  { label: "访问", value: "access" },
  { label: "SSH 失败", value: "ssh" },
  { label: "慢 SQL", value: "slowsql" },
];

const lineCols: DataTableColumns<(typeof lines.value)[0]> = [
  { title: "级别", key: "level", width: 80, render: (r) => r.level },
  { title: "内容", key: "text", ellipsis: { tooltip: true } },
];

const srcOpts = () => sources.value.map((s) => ({ label: s.name, value: s.id }));

async function loadSources() {
  const r = await api<{ items: typeof sources.value }>("GET", "/logs/sources");
  if (r.ok) sources.value = r.data.items || [];
}

async function query() {
  stopFollow();
  const params = new URLSearchParams({ source: source.value, limit: "200" });
  if (q.value) params.set("q", q.value);
  if (level.value) params.set("level", level.value);
  if (site.value) params.set("site", site.value);
  const r = await api<{ items: typeof lines.value }>("GET", "/logs/query?" + params.toString());
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  lines.value = r.data.items || [];
}

function follow() {
  stopFollow();
  following.value = true;
  const params = new URLSearchParams({ source: source.value });
  if (site.value) params.set("site", site.value);
  es = new EventSource(panelPrefix() + "/api/v1/logs/follow?" + params.toString());
  es.onmessage = (ev) => {
    try {
      const ln = JSON.parse(ev.data) as { text: string; level: string };
      lines.value = [ln, ...lines.value].slice(0, 500);
    } catch {
      /* ignore */
    }
  };
  es.onerror = () => {
    void api("GET", "/auth/session").then((r) => {
      if (!r.ok) {
        stopFollow();
        void router.replace("/login");
      }
    });
  };
}

function stopFollow() {
  following.value = false;
  if (es) {
    es.close();
    es = null;
  }
}

async function loadStats() {
  const params = new URLSearchParams({ kind: kind.value });
  if (site.value) params.set("site", site.value);
  const r = await api<any>("GET", "/logs/stats?" + params.toString());
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  stats.value = r.data.live;
  truncated.value = !!(r.data.truncated || r.data.live?.truncated);
}

async function loadRotate() {
  const r = await api<any>("GET", "/logs/rotate");
  if (!r.ok) return;
  keepDays.value = r.data.keep_days;
  maxSize.value = r.data.max_size;
  rotateRaw.value = r.data.content || "";
  drifted.value = r.data.drifted;
}

function setKeepDays(v: string) {
  keepDays.value = Number(v) || 14;
}

async function saveRotate() {
  const r = await api("PUT", "/logs/rotate", {
    keep_days: keepDays.value,
    max_size: maxSize.value,
    force: force.value,
  });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已按表单保存 logrotate");
  await loadRotate();
}

async function saveRotateRaw() {
  const r = await api("PUT", "/logs/rotate", {
    keep_days: keepDays.value,
    max_size: maxSize.value,
    content: rotateRaw.value,
    force: force.value,
  });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已保存 logrotate 原文");
  await loadRotate();
}

onMounted(async () => {
  await loadSources();
  await loadRotate();
  await query();
  await loadStats();
});
onUnmounted(stopFollow);

function kv(m: Record<string, number> | undefined) {
  if (!m) return [];
  return Object.entries(m)
    .sort((a, b) => b[1] - a[1])
    .slice(0, 10);
}
</script>

<template>
  <n-space vertical>
    <n-card title="日志查看">
      <n-form inline>
        <n-form-item label="来源">
          <n-select v-model:value="source" :options="srcOpts()" style="width: 260px" />
        </n-form-item>
        <n-form-item label="站点 key">
          <n-input v-model:value="site" placeholder="可选" style="width: 140px" />
        </n-form-item>
        <n-form-item label="关键字">
          <n-input v-model:value="q" style="width: 180px" />
        </n-form-item>
        <n-form-item label="级别">
          <n-select v-model:value="level" :options="levelOpts" style="width: 120px" />
        </n-form-item>
        <n-button type="primary" @click="query">查询</n-button>
        <n-button v-if="!following" @click="follow">实时跟随</n-button>
        <n-button v-else type="warning" @click="stopFollow">停止跟随</n-button>
      </n-form>
      <n-tag v-if="following" type="success" style="margin-bottom: 8px">正在跟随</n-tag>
      <n-data-table :columns="lineCols" :data="lines" :max-height="420" size="small" />
    </n-card>
    <n-card title="统计">
      <n-alert v-if="truncated" type="warning" title="已截断">仅统计文件末尾（最多 256 MB），更早的记录未计入。</n-alert>
      <n-form inline>
        <n-form-item label="类型">
          <n-select v-model:value="kind" :options="kindOpts" style="width: 140px" />
        </n-form-item>
        <n-button @click="loadStats">刷新</n-button>
      </n-form>
      <div v-if="stats">
        <p v-if="kind === 'access'">
          PV {{ stats.pv }}　UV {{ stats.uv }}　平均响应 {{ (stats.rt_avg || 0).toFixed(3) }} 秒
        </p>
        <p v-if="kind === 'ssh'">SSH 失败 {{ stats.fails }} 次</p>
        <p v-if="kind === 'slowsql'">慢查询 {{ stats.slow }} 条</p>
        <div v-if="kind === 'access'" style="display: grid; grid-template-columns: 1fr 1fr; gap: 12px">
          <div>
            <h4>状态码</h4>
            <p v-for="[k, v] in kv(stats.status)" :key="k">{{ k }}　{{ v }}</p>
          </div>
          <div>
            <h4>热门 URL</h4>
            <p v-for="[k, v] in kv(stats.url)" :key="k">{{ k }}　{{ v }}</p>
          </div>
          <div>
            <h4>来源 IP</h4>
            <p v-for="[k, v] in kv(stats.ip)" :key="k">{{ k }}　{{ v }}</p>
          </div>
          <div>
            <h4>Referer</h4>
            <p v-for="[k, v] in kv(stats.referer)" :key="k">{{ k }}　{{ v }}</p>
          </div>
        </div>
        <div v-if="kind === 'slowsql' && stats.top">
          <p v-for="(it, i) in stats.top" :key="i">{{ it.seconds }}s　{{ it.sql }}</p>
        </div>
      </div>
    </n-card>
    <n-card title="logrotate">
      <n-alert v-if="drifted" type="warning" title="配置漂移">磁盘与面板上次写入不一致，确认后勾选覆盖。</n-alert>
      <n-form inline>
        <n-form-item label="保留天数"><n-input :value="String(keepDays)" @update:value="setKeepDays" /></n-form-item>
        <n-form-item label="单文件上限"><n-input v-model:value="maxSize" /></n-form-item>
        <n-button type="primary" @click="saveRotate">保存表单</n-button>
        <label><input type="checkbox" v-model="force" /> 确认覆盖磁盘</label>
      </n-form>
      <n-input v-model:value="rotateRaw" type="textarea" :autosize="{ minRows: 8 }" />
      <n-button style="margin-top: 8px" @click="saveRotateRaw">保存原文</n-button>
    </n-card>
  </n-space>
</template>
