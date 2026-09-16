<script setup lang="ts">
import { onMounted, ref } from "vue";
import { NAlert, NButton, NCard, NForm, NFormItem, NInput, NSelect, NSpace, NTag, useMessage } from "naive-ui";
import { api, waitJob } from "../api";

const msg = useMessage();
const env = ref<any>(null);
const versions = ref<{ version: string; installed: boolean; available: boolean; extensions: string[] }[]>([]);
const ver = ref<string | null>(null);
const busy = ref("");
const extName = ref("mysql");
const ini = ref<any>({});
const iniRaw = ref("");
const iniDrift = ref(false);
const iniRawMode = ref(false);
const pool = ref<any>({});
const poolRaw = ref("");
const poolDrift = ref(false);
const poolRawMode = ref(false);
const force = ref(false);

const extOpts = ["mysql", "gd", "curl", "mbstring", "xml", "zip", "bcmath", "intl", "redis", "imagick", "opcache"].map((x) => ({
  label: x,
  value: x,
}));

async function loadEnv() {
  const r = await api<any>("GET", "/env");
  if (r.ok) env.value = r.data;
}

async function loadVers() {
  const r = await api<{ items: typeof versions.value }>("GET", "/php/versions");
  if (r.ok) versions.value = r.data.items || [];
}

async function loadVer() {
  if (!ver.value) return;
  const i = await api<any>("GET", `/php/versions/${ver.value}/ini`);
  if (i.ok) {
    ini.value = i.data.form || {};
    iniRaw.value = i.data.content || "";
    iniDrift.value = i.data.drifted;
    iniRawMode.value = i.data.raw_mode;
  }
  const p = await api<any>("GET", `/php/versions/${ver.value}/pool`);
  if (p.ok) {
    pool.value = p.data.form || {};
    poolRaw.value = p.data.content || "";
    poolDrift.value = p.data.drifted;
    poolRawMode.value = p.data.raw_mode;
  }
  await loadVers();
}

onMounted(async () => {
  await loadEnv();
  await loadVers();
});

async function runJob(title: string, path: string, body: unknown = {}) {
  busy.value = title;
  try {
    const r = await api<{ job_id: string }>("POST", path, body);
    if (!r.ok) {
      msg.error(r.error.message);
      return;
    }
    await waitJob(r.data.job_id, (ph, d) => (busy.value = `${title}：${ph} ${d}`));
    msg.success(title + "完成，配置在 1 到 2 秒内生效");
    await loadEnv();
    await loadVers();
    await loadVer();
  } catch (e: any) {
    msg.error(e.message || String(e));
  } finally {
    busy.value = "";
  }
}

function installPPA() {
  return runJob("添加 PHP 源", "/env/php/repo");
}
function installVer() {
  if (!ver.value) return;
  return runJob("安装 PHP " + ver.value, `/php/versions/${ver.value}/install`);
}

async function addExt() {
  if (!ver.value) return;
  const r = await api("POST", `/php/versions/${ver.value}/extensions`, { name: extName.value });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("扩展已安装，配置在 1 到 2 秒内生效");
  await loadVer();
}

async function delExt(name: string) {
  if (!ver.value) return;
  const r = await api("DELETE", `/php/versions/${ver.value}/extensions/${name}`);
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("扩展已卸载");
  await loadVer();
}

async function saveIni() {
  if (!ver.value) return;
  const r = await api("PUT", `/php/versions/${ver.value}/ini`, { ...ini.value, force: force.value });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("php.ini 已保存，配置在 1 到 2 秒内生效");
  await loadVer();
}

async function saveIniRaw() {
  if (!ver.value) return;
  const r = await api("PUT", `/php/versions/${ver.value}/ini/raw`, { content: iniRaw.value, force: force.value });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("原文已保存，配置在 1 到 2 秒内生效");
  await loadVer();
}

async function useIniTpl() {
  if (!ver.value) return;
  const r = await api("POST", `/php/versions/${ver.value}/ini/use-template`, {});
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已恢复模板渲染");
  await loadVer();
}

async function savePool() {
  if (!ver.value) return;
  const r = await api("PUT", `/php/versions/${ver.value}/pool`, { ...pool.value, force: force.value });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("pool 已保存，配置在 1 到 2 秒内生效");
  await loadVer();
}

async function savePoolRaw() {
  if (!ver.value) return;
  const r = await api("PUT", `/php/versions/${ver.value}/pool/raw`, { content: poolRaw.value, force: force.value });
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("pool 原文已保存，配置在 1 到 2 秒内生效");
  await loadVer();
}

async function usePoolTpl() {
  if (!ver.value) return;
  const r = await api("POST", `/php/versions/${ver.value}/pool/use-template`, {});
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  msg.success("已恢复模板渲染");
  await loadVer();
}

const verOpts = () =>
  versions.value.map((v) => ({
    label: `${v.version}${v.installed ? "（已装）" : v.available ? "（可装）" : ""}`,
    value: v.version,
  }));

const current = () => versions.value.find((v) => v.version === ver.value);
</script>

<template>
  <n-space vertical>
    <n-card title="PHP / 环境">
      <p v-if="env">PPA：{{ env.php_ppa ? "已添加" : "未添加" }}；状态：{{ env.php }}</p>
      <n-space>
        <n-button type="primary" :disabled="!!busy" @click="installPPA">添加 PHP 源（Ondřej PPA）</n-button>
        <span v-if="busy">{{ busy }}</span>
      </n-space>
    </n-card>
    <n-card title="版本">
      <n-space>
        <n-select v-model:value="ver" :options="verOpts()" placeholder="选择版本" style="width: 220px" @update:value="loadVer" />
        <n-button :disabled="!ver || !!busy" @click="installVer">安装此版本 FPM/CLI</n-button>
      </n-space>
    </n-card>
    <n-card v-if="ver && current()?.installed" title="扩展">
      <n-space style="margin-bottom: 8px">
        <n-tag v-for="e in current()?.extensions || []" :key="e" closable @close="delExt(e)">{{ e }}</n-tag>
      </n-space>
      <n-space>
        <n-select v-model:value="extName" :options="extOpts" filterable tag style="width: 180px" />
        <n-button @click="addExt">安装扩展（apt）</n-button>
      </n-space>
    </n-card>
    <n-card v-if="ver && current()?.installed" title="php.ini 片段">
      <n-alert v-if="iniDrift" type="warning" title="配置漂移">磁盘与面板上次写入不一致。保存将覆盖磁盘。</n-alert>
      <n-alert v-if="iniRawMode" type="info" title="原文模式">表单只读，请先恢复为模板渲染。</n-alert>
      <n-form>
        <n-form-item label="upload_max_filesize"><n-input v-model:value="ini['upload_max_filesize']" :disabled="iniRawMode" /></n-form-item>
        <n-form-item label="post_max_size"><n-input v-model:value="ini['post_max_size']" :disabled="iniRawMode" /></n-form-item>
        <n-form-item label="memory_limit"><n-input v-model:value="ini['memory_limit']" :disabled="iniRawMode" /></n-form-item>
        <n-form-item label="max_execution_time"><n-input v-model:value="ini['max_execution_time']" :disabled="iniRawMode" /></n-form-item>
        <n-form-item label="display_errors"><n-input v-model:value="ini['display_errors']" :disabled="iniRawMode" /></n-form-item>
        <n-form-item label="opcache.enable"><n-input v-model:value="ini['opcache.enable']" :disabled="iniRawMode" /></n-form-item>
        <n-form-item label="opcache.memory_consumption"><n-input v-model:value="ini['opcache.memory_consumption']" :disabled="iniRawMode" /></n-form-item>
        <n-form-item label="opcache.max_accelerated_files"><n-input v-model:value="ini['opcache.max_accelerated_files']" :disabled="iniRawMode" /></n-form-item>
        <n-form-item label="opcache.validate_timestamps"><n-input v-model:value="ini['opcache.validate_timestamps']" :disabled="iniRawMode" /></n-form-item>
        <n-space>
          <n-button type="primary" :disabled="iniRawMode" @click="saveIni">保存表单</n-button>
          <n-button @click="useIniTpl">恢复为模板渲染</n-button>
        </n-space>
      </n-form>
      <n-input v-model:value="iniRaw" type="textarea" :autosize="{ minRows: 10 }" style="margin-top: 12px" />
      <n-button style="margin-top: 8px" @click="saveIniRaw">保存原文</n-button>
    </n-card>
    <n-card v-if="ver && current()?.installed" title="FPM pool">
      <n-alert v-if="poolDrift" type="warning" title="配置漂移">磁盘与面板上次写入不一致。</n-alert>
      <n-alert v-if="poolRawMode" type="info" title="原文模式">表单只读。</n-alert>
      <n-form>
        <n-form-item label="user"><n-input v-model:value="pool.user" :disabled="poolRawMode" /></n-form-item>
        <n-form-item label="listen"><n-input v-model:value="pool.listen" :disabled="poolRawMode" /></n-form-item>
        <n-form-item label="pm"><n-input v-model:value="pool.pm" :disabled="poolRawMode" /></n-form-item>
        <n-form-item label="pm.max_children"><n-input v-model:value="pool['pm.max_children']" :disabled="poolRawMode" /></n-form-item>
        <n-space>
          <n-button type="primary" :disabled="poolRawMode" @click="savePool">保存表单</n-button>
          <n-button @click="usePoolTpl">恢复为模板渲染</n-button>
        </n-space>
      </n-form>
      <n-input v-model:value="poolRaw" type="textarea" :autosize="{ minRows: 10 }" style="margin-top: 12px" />
      <n-button style="margin-top: 8px" @click="savePoolRaw">保存原文</n-button>
    </n-card>
    <n-card v-if="ver">
      <label><input type="checkbox" v-model="force" /> 确认覆盖磁盘（漂移时必勾）</label>
    </n-card>
  </n-space>
</template>
