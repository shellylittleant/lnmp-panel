<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import { NAlert, NButton, NCard, NCheckbox, NForm, NFormItem, NInput, NInputNumber, NSpace, useMessage } from "naive-ui";
import { api } from "../api";

const msg = useMessage();
const ssh = ref(22);
const panel = ref(0);
const policy = ref("drop");
const publicHTTP = ref(true);
const openPorts = ref("80, 443");
const allow = ref("");
const content = ref("");
const drifted = ref(false);
const rawMode = ref(false);
const pending = ref(false);
const deadline = ref<string | null>(null);
const force = ref(false);
const remain = ref("");
let timer: number | undefined;

async function load() {
  const r = await api<any>("GET", "/firewall");
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  const d = r.data;
  ssh.value = d.form.ssh_port;
  panel.value = d.form.panel_port;
  policy.value = d.form.input_policy;
  publicHTTP.value = d.form.public_http;
  openPorts.value = (d.form.open_ports || []).join(", ");
  allow.value = (d.form.allow_cidrs || []).join(", ");
  content.value = d.content;
  drifted.value = d.drifted;
  rawMode.value = d.raw_mode;
  pending.value = d.pending;
  deadline.value = d.confirm_deadline || null;
}

function tick() {
  if (!deadline.value) {
    remain.value = "";
    return;
  }
  const ms = Date.parse(deadline.value) - Date.now();
  if (ms <= 0) {
    remain.value = "已到期，正在回滚…";
    load();
    return;
  }
  remain.value = "还剩 " + Math.ceil(ms / 1000) + " 秒确认，否则自动回滚";
}

onMounted(async () => {
  await load();
  timer = window.setInterval(tick, 1000);
});
onUnmounted(() => {
  if (timer) clearInterval(timer);
});

function ports(): number[] {
  return openPorts.value
    .split(",")
    .map((x) => parseInt(x.trim(), 10))
    .filter((n) => n > 0);
}

async function save() {
  const r = await api("PUT", "/firewall", {
    ssh_port: ssh.value,
    panel_port: panel.value,
    input_policy: policy.value,
    public_http: publicHTTP.value,
    open_ports: ports(),
    allow_cidrs: allow.value.split(",").map((x) => x.trim()).filter(Boolean),
    force: force.value,
  });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已保存（尚未应用到内核）");
  await load();
}

async function saveRaw() {
  const r = await api("PUT", "/firewall/raw", { content: content.value, force: force.value });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("原文已保存（尚未应用到内核）");
  await load();
}

async function apply() {
  const r = await api<any>("POST", "/firewall/apply", {});
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success(r.data.message || "已应用，请确认");
  await load();
}

async function confirm() {
  const r = await api("POST", "/firewall/confirm", {});
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已确认，规则保留");
  await load();
}

async function rollback() {
  const r = await api("POST", "/firewall/rollback", {});
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已回滚");
  await load();
}

async function useTpl() {
  const r = await api("POST", "/firewall/use-template", {});
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已恢复官方模板");
  await load();
}
</script>

<template>
  <n-space vertical>
    <n-alert v-if="pending" type="warning" :title="remain || '确认窗口'">
      新规则已加载。若 SSH 或面板中断，到期会自动回滚。请立即确认或手动回滚。
      <n-space style="margin-top: 8px">
        <n-button type="primary" @click="confirm">确认保留</n-button>
        <n-button type="error" @click="rollback">立即回滚</n-button>
      </n-space>
    </n-alert>
    <n-alert v-if="drifted" type="warning" title="配置漂移">磁盘与面板上次写入不一致。</n-alert>
    <n-alert v-if="rawMode" type="info" title="原文模式">表单只读。请先恢复模板。</n-alert>
    <n-card title="防火墙（nftables）">
      <p>官方模板始终放行 SSH 与面板端口。应用后有 60 秒确认窗口；超时或面板心跳中断则回滚。</p>
      <n-form>
        <n-form-item label="SSH 端口"><n-input-number v-model:value="ssh" :min="1" :max="65535" :disabled="rawMode" /></n-form-item>
        <n-form-item label="面板端口"><n-input-number v-model:value="panel" :min="1" :max="65535" :disabled="rawMode" /></n-form-item>
        <n-form-item label="入站策略">
          <n-input v-model:value="policy" :disabled="rawMode" placeholder="drop 或 accept" />
        </n-form-item>
        <n-form-item label="公网 80/443"><n-checkbox v-model:checked="publicHTTP" :disabled="rawMode">放行</n-checkbox></n-form-item>
        <n-form-item label="额外 TCP 端口"><n-input v-model:value="openPorts" :disabled="rawMode" placeholder="3306, 8080" /></n-form-item>
        <n-form-item label="额外放行 CIDR"><n-input v-model:value="allow" :disabled="rawMode" placeholder="10.0.0.0/8" /></n-form-item>
        <n-checkbox v-model:checked="force">确认覆盖磁盘</n-checkbox>
        <n-space style="margin-top: 12px">
          <n-button type="primary" :disabled="rawMode" @click="save">保存表单</n-button>
          <n-button type="primary" @click="apply">应用（进入确认窗口）</n-button>
          <n-button @click="useTpl">恢复官方模板</n-button>
        </n-space>
      </n-form>
    </n-card>
    <n-card title="规则集原文">
      <n-input v-model:value="content" type="textarea" :autosize="{ minRows: 16 }" />
      <n-button style="margin-top: 8px" @click="saveRaw">保存原文</n-button>
    </n-card>
  </n-space>
</template>
