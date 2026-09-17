<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import { NAlert, NButton, NCard, NForm, NFormItem, NInput, NSpace, useMessage } from "naive-ui";
import { api } from "../api";

const msg = useMessage();
const token = ref("");
const expires = ref("");
const address = ref("");
const peerToken = ref("");
const status = ref<any>(null);
const pre = ref<any>(null);
const busy = ref("");
let timer: number | undefined;

async function loadStatus() {
  const r = await api<any>("GET", "/migrate/status");
  if (r.ok) status.value = r.data;
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
    const r = await api("POST", "/migrate/start", { mode });
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
  msg.success("已取消并作废临时钥");
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
          <n-button type="warning" :disabled="!!busy" @click="cancel">取消并作废钥</n-button>
          <span v-if="busy">{{ busy }}…</span>
        </n-space>
      </n-form>
      <n-alert v-if="pre" :type="pre.ok ? 'success' : 'error'" title="预检" style="margin-top: 12px">
        <p>系统 {{ pre.os }}　磁盘 {{ pre.disk_ok ? "足够" : "不足" }}　{{ pre.php }}　{{ pre.mysql }}</p>
        <p v-for="(e, i) in pre.errors || []" :key="i">{{ e }}</p>
      </n-alert>
    </n-card>
    <n-card title="进度">
      <p v-if="status">阶段 {{ status.phase || "—" }}　{{ status.detail }}　{{ status.progress || 0 }}%</p>
      <p v-else>尚无任务</p>
    </n-card>
  </n-space>
</template>
