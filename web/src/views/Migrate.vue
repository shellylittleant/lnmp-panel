<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { NAlert, NButton, NCard, NCheckbox, NForm, NFormItem, NInput, NSpace, NTabPane, NTabs, useMessage } from "naive-ui";
import { api } from "../api";

const msg = useMessage();
const route = useRoute();
const router = useRouter();
const tab = computed({
  get: () => (route.query.tab === "ha" ? "ha" : "mig"),
  set: (v: string) => router.replace({ query: v === "mig" ? {} : { tab: v } }),
});
const token = ref("");
const expires = ref("");
const address = ref("");
const peerToken = ref("");
const status = ref<any>(null);
const pre = ref<any>(null);
const busy = ref("");
const confirmReplace = ref(false);
const haToken = ref("");
const haExpires = ref("");
const haAddress = ref("");
const haPeerToken = ref("");
const haStatus = ref<any>(null);
let timer: number | undefined;

async function loadStatus() {
  const r = await api<any>("GET", "/migrate/status");
  if (r.ok) status.value = r.data;
  const h = await api<any>("GET", "/ha/status");
  if (h.ok) haStatus.value = h.data;
}

function roleLabel(r: string) {
  if (r === "master") return "主机";
  if (r === "standby") return "备机";
  return "单机";
}

async function genHAToken() {
  const r = await api<{ token: string; expires_at: string }>("POST", "/ha/token", {});
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  haToken.value = r.data.token;
  haExpires.value = r.data.expires_at;
  msg.success("热备令牌已生成，请在备机填写本机地址和此令牌");
}

async function haPair() {
  busy.value = "热备配对";
  try {
    const r = await api("POST", "/ha/pair", { address: haAddress.value, token: haPeerToken.value });
    if (!r.ok) {
      msg.error(r.error.message);
      return;
    }
    msg.success("已配对为本机备机、对端主机");
    await loadStatus();
  } finally {
    busy.value = "";
  }
}

async function haPromote() {
  if (!window.confirm("启用主模式：本机 MySQL 改为可写、站点恢复对外，并立刻改防火墙（不进 60 秒确认）。对端联系不上时可以继续，对端可能仍在服务。对端能连上但降级失败则不会提升。")) return;
  busy.value = "启用主模式";
  try {
    const r = await api("POST", "/ha/promote", {});
    if (!r.ok) {
      msg.error(r.error.message);
      return;
    }
    msg.success("已启用主模式，请人工修改 DNS");
    await loadStatus();
  } finally {
    busy.value = "";
  }
}

async function haDemote() {
  if (!window.confirm("降级为备：本机 MySQL 改为只读，站点改为 404，并立刻改防火墙。")) return;
  busy.value = "降级为备";
  try {
    const r = await api("POST", "/ha/demote", {});
    if (!r.ok) {
      msg.error(r.error.message);
      return;
    }
    msg.success("已降级为备机");
    await loadStatus();
  } finally {
    busy.value = "";
  }
}

async function haStartSync() {
  busy.value = "开始同步";
  try {
    const r = await api("POST", "/ha/sync/start", {});
    if (!r.ok) {
      msg.error(r.error.message);
      return;
    }
    msg.success("备机已开始从主机拉取");
    await loadStatus();
  } finally {
    busy.value = "";
  }
}

async function genToken() {
  const r = await api<{ token: string; expires_at: string }>("POST", "/migrate/token", {});
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  token.value = r.data.token;
  expires.value = r.data.expires_at;
  msg.success("令牌已生成，请在源机填写本机地址和此令牌");
}

async function connect() {
  busy.value = "配对";
  try {
    const r = await api("POST", "/migrate/connect", { address: address.value, token: peerToken.value });
    if (!r.ok) {
      msg.error(r.error.message);
      return;
    }
    msg.success("配对成功");
    await loadStatus();
  } finally {
    busy.value = "";
  }
}

async function preflight() {
  busy.value = "预检";
  try {
    const r = await api<any>("POST", "/migrate/preflight", {});
    if (!r.ok) {
      msg.error(r.error.message);
      return;
    }
    pre.value = r.data;
    if (r.data.ok) msg.success("预检通过");
    else msg.error((r.data.errors || []).join("；") || "预检失败");
  } finally {
    busy.value = "";
  }
}

async function start(mode: string) {
  busy.value = mode === "full" ? "全量同步" : "增量同步";
  try {
    const r = await api("POST", "/migrate/start", { mode, confirm_replace: confirmReplace.value });
    if (!r.ok) {
      msg.error(r.error.message);
      return;
    }
    msg.success("已开始");
    await loadStatus();
  } finally {
    busy.value = "";
  }
}

async function cancel() {
  const r = await api("POST", "/migrate/cancel", {});
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已作废临时钥");
  await loadStatus();
}

onMounted(() => {
  loadStatus();
  timer = window.setInterval(loadStatus, 2000);
});
onUnmounted(() => {
  if (timer) clearInterval(timer);
});
</script>

<template>
  <n-tabs v-model:value="tab" type="line">
    <n-tab-pane name="mig" tab="迁移">
  <n-space vertical>
    <n-card title="本机作为目标（B）">
      <p>生成一次性配对令牌，在源机（A）填写本机 <code>https://主机:端口/路径前缀</code> 与令牌。</p>
      <n-button type="primary" @click="genToken">生成令牌</n-button>
      <n-alert v-if="token" type="success" title="配对令牌" style="margin-top: 12px">
        <p style="word-break: break-all">{{ token }}</p>
        <p>过期：{{ expires }}</p>
      </n-alert>
    </n-card>
    <n-card title="本机作为源（A）">
      <n-form>
        <n-form-item label="目标地址">
          <n-input v-model:value="address" placeholder="https://b.example.com:18443/前缀" />
        </n-form-item>
        <n-form-item label="令牌">
          <n-input v-model:value="peerToken" type="textarea" />
        </n-form-item>
        <n-space>
          <n-button type="primary" :disabled="!!busy" @click="connect">配对</n-button>
          <n-button :disabled="!!busy" @click="preflight">预检</n-button>
          <n-button type="primary" :disabled="!!busy" @click="start('full')">全量同步</n-button>
          <n-button :disabled="!!busy" @click="start('incr')">增量同步</n-button>
          <n-button type="warning" :disabled="!!busy" @click="cancel">作废临时钥</n-button>
          <span v-if="busy">{{ busy }}…</span>
        </n-space>
      </n-form>
      <n-alert v-if="pre" :type="pre.ok ? 'success' : 'error'" title="预检" style="margin-top: 12px">
        <p>系统 {{ pre.os }}　磁盘 {{ pre.disk_ok ? "足够" : "不足" }}　{{ pre.php }}　{{ pre.mysql }}</p>
        <p v-if="pre.site_count > 0">目标机已有 {{ pre.site_count }} 个站点将被替换：{{ (pre.site_keys || []).join("、") }}</p>
        <n-checkbox v-if="pre.site_count > 0" v-model:checked="confirmReplace">确认替换目标机已有站点</n-checkbox>
        <p v-for="(e, i) in pre.errors || []" :key="i">{{ e }}</p>
      </n-alert>
    </n-card>
    <n-card title="进度">
      <p v-if="status">阶段 {{ status.phase || "—" }}　{{ status.detail }}　{{ status.progress || 0 }}%</p>
      <p v-else>尚无任务</p>
    </n-card>
  </n-space>
    </n-tab-pane>
    <n-tab-pane name="ha" tab="热备">
      <n-space vertical>
        <n-alert type="info" title="第一版全部人工">
          切换、回切、DNS 均由运维操作。不做自动接管。配对长期有效，不受迁移 7 天过期限制。
        </n-alert>
        <n-card title="本机作为主机">
          <p>生成配对令牌，在备机填写本机 <code>https://主机:端口/路径前缀</code> 与令牌。备机发起配对并拉取。</p>
          <n-button type="primary" @click="genHAToken">生成热备令牌</n-button>
          <n-alert v-if="haToken" type="success" title="热备令牌" style="margin-top: 12px">
            <p style="word-break: break-all">{{ haToken }}</p>
            <p>握手过期：{{ haExpires }}（握手成功后长期有效）</p>
          </n-alert>
        </n-card>
        <n-card title="本机作为备机">
          <n-form>
            <n-form-item label="主机地址">
              <n-input v-model:value="haAddress" placeholder="https://a.example.com:18443/前缀" />
            </n-form-item>
            <n-form-item label="令牌">
              <n-input v-model:value="haPeerToken" type="textarea" />
            </n-form-item>
            <n-space>
              <n-button type="primary" :disabled="!!busy" @click="haPair">配对（本机为备）</n-button>
              <n-button :disabled="!!busy" @click="haStartSync">开始同步</n-button>
              <n-button type="warning" :disabled="!!busy" @click="haPromote">启用主模式</n-button>
              <n-button :disabled="!!busy" @click="haDemote">降级为备</n-button>
              <span v-if="busy">{{ busy }}…</span>
            </n-space>
          </n-form>
        </n-card>
        <n-card title="状态">
          <template v-if="haStatus">
            <p>角色 {{ roleLabel(haStatus.role) }}　任期 {{ haStatus.term }}　nginx {{ haStatus.nginx_mode || "—" }}</p>
            <p v-if="haStatus.degraded" style="color: #d03050">自我保护：本机已只读并挂维护页</p>
            <p>对端 {{ haStatus.peer_address || "—" }}</p>
            <p>同步间隔 {{ haStatus.interval_sec || 60 }} 秒　上一轮 {{ haStatus.last_scan_ms || 0 }} ms　落后 {{ haStatus.lag_sec || 0 }} 秒</p>
            <p>MySQL 复制 {{ haStatus.mysql_repl_ok ? "正常" : "未就绪" }}　{{ haStatus.syncing ? "同步中" : "未在同步" }}</p>
            <p v-if="haStatus.alert">告警：{{ haStatus.alert }}</p>
            <p v-if="haStatus.detail">{{ haStatus.detail }}</p>
            <p v-if="haStatus.firewall_warning">防火墙：{{ haStatus.firewall_warning }}</p>
          </template>
          <p v-else>尚未加载</p>
        </n-card>
      </n-space>
    </n-tab-pane>
  </n-tabs>
</template>
