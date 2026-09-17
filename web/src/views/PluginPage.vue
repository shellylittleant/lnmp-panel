<script setup lang="ts">
import { onMounted, ref } from "vue";
import { NAlert, NButton, NCard, NForm, NFormItem, NInput, NSpace, NUpload, useMessage } from "naive-ui";
import type { UploadFileInfo } from "naive-ui";
import { api, apiForm, downloadPluginFile } from "../api";

type Action = { name: string; label: string; method: string; path: string };
type Schema = { title: string; fields: { name: string; label: string; type: string }[]; actions: Action[] };
type Plug = { id: string; name: string; enabled: boolean; pages?: { path: string; schema: Schema }[] };

const msg = useMessage();
const schema = ref<Schema | null>(null);
const pluginId = ref("");
const path = ref("");
const listing = ref<{ name: string; dir: boolean; size: number }[]>([]);
const fileList = ref<UploadFileInfo[]>([]);
const busy = ref("");

onMounted(async () => {
  const r = await api<{ items: Plug[] }>("GET", "/plugins");
  if (!r.ok) {
    msg.error(r.error.message);
    return;
  }
  const pagePath = "/files";
  for (const p of r.data.items || []) {
    const pg = (p.pages || []).find((x) => x.path === pagePath);
    if (pg && p.enabled) {
      pluginId.value = p.id;
      schema.value = pg.schema;
      break;
    }
  }
});

async function listDir() {
  if (!pluginId.value) return;
  busy.value = "列出";
  try {
    const q = encodeURIComponent(path.value || ".");
    const r = await api<{ items: { name: string; dir: boolean; size: number }[] }>("GET", `/plugin/${pluginId.value}/list?path=${q}`);
    if (!r.ok) {
      msg.error(r.error.message);
      return;
    }
    listing.value = r.data.items || [];
  } finally {
    busy.value = "";
  }
}

async function upload() {
  if (!pluginId.value || fileList.value.length === 0) {
    msg.error("请选择文件");
    return;
  }
  const raw = fileList.value[0].file;
  if (!raw) {
    msg.error("请选择文件");
    return;
  }
  const form = new FormData();
  form.append("path", path.value);
  form.append("file", raw);
  busy.value = "上传";
  try {
    const r = await apiForm(`/plugin/${pluginId.value}/upload`, form);
    if (!r.ok) {
      msg.error(r.error.message);
      return;
    }
    msg.success("已上传");
    await listDir();
  } finally {
    busy.value = "";
  }
}

async function download() {
  if (!pluginId.value) return;
  const r = await downloadPluginFile(`/plugin/${pluginId.value}/download?path=${encodeURIComponent(path.value)}`, path.value.split("/").pop() || "download");
  if (!r.ok) msg.error(r.error.message);
}
</script>

<template>
  <n-space vertical>
    <n-alert v-if="!schema" type="warning" title="插件未启用">文件上传下载未编入当前二进制，或已在环境安装中停用。</n-alert>
    <n-card v-else :title="schema.title">
      <p>允许根仅 <code>/var/www</code>。禁止 <code>..</code>，禁止面板密钥与数据库文件。上传后属主改为 www-data。</p>
      <n-form>
        <n-form-item label="相对路径">
          <n-input v-model:value="path" placeholder="demo 或 demo/index.html" />
        </n-form-item>
        <n-form-item label="上传文件">
          <n-upload :max="1" :default-upload="false" v-model:file-list="fileList">
            <n-button>选择文件</n-button>
          </n-upload>
        </n-form-item>
        <n-space>
          <n-button :disabled="!!busy" @click="listDir">列出</n-button>
          <n-button type="primary" :disabled="!!busy" @click="upload">上传</n-button>
          <n-button :disabled="!!busy" @click="download">下载</n-button>
          <span v-if="busy">{{ busy }}…</span>
        </n-space>
      </n-form>
      <ul v-if="listing.length" style="margin-top: 12px">
        <li v-for="it in listing" :key="it.name">
          {{ it.dir ? "目录" : "文件" }} {{ it.name }}
          <span v-if="!it.dir">（{{ it.size }} 字节）</span>
        </li>
      </ul>
    </n-card>
  </n-space>
</template>
